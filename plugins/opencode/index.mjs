/**
 * opencode-aisync — OpenCode plugin for automatic session capture & monitoring.
 *
 * Hooks into OpenCode's plugin API to provide:
 *   - session.idle/error → capture
 *   - chat.message → incremental capture + message tracking
 *   - tool.execute.after → tool error detection
 *   - session.compacting → re-capture before compaction
 *
 * Install:
 *   ln -s /path/to/aisync/plugins/opencode ~/.config/opencode/plugins/opencode-aisync
 *
 * Requires: `aisync` binary in PATH.
 */

const CAPTURE_MODE = process.env.AISYNC_CAPTURE_MODE || "compact";
const DEBUG = Boolean(process.env.AISYNC_PLUGIN_DEBUG);
const INCREMENTAL_INTERVAL = parseInt(process.env.AISYNC_INCREMENTAL_INTERVAL || "0", 10);

const AisyncPlugin = async (ctx) => {
  const { $, worktree } = ctx;
  const captured = new Set();
  const messageCounters = new Map();
  const toolErrors = new Map();

  const log = (msg) => {
    if (DEBUG) console.log(`[aisync] ${msg}`);
  };

  const currentBranch = async () => {
    try {
      const result = await $`git -C ${worktree} rev-parse --abbrev-ref HEAD 2>/dev/null`.text();
      return result.trim();
    } catch {
      return "";
    }
  };

  const captureSession = async (sessionId, reason) => {
    if (!sessionId) return;
    if (captured.has(sessionId) && reason !== "incremental" && reason !== "compacted") {
      log(`session ${sessionId} already captured, skipping (reason=${reason || "duplicate"})`);
      return;
    }

    const branch = await currentBranch();
    log(`capturing session=${sessionId} branch=${branch || "(none)"} mode=${CAPTURE_MODE} reason=${reason || "idle"}`);

    try {
      await $`aisync capture --provider opencode --session-id ${sessionId} --mode ${CAPTURE_MODE} --auto`;
      captured.add(sessionId);
      log(`capture complete: ${sessionId}`);
    } catch (err) {
      log(`capture failed: ${err?.message || err}`);
    }
  };

  return {
    event: async ({ event }) => {
      try {
        switch (event.type) {
          case "session.created": {
            const sessionId = event.properties?.info?.id;
            const branch = await currentBranch();
            log(`session started: ${sessionId || "unknown"} on branch=${branch || "(none)"}`);
            if (sessionId) {
              messageCounters.set(sessionId, 0);
              toolErrors.set(sessionId, 0);
            }
            break;
          }
          case "session.idle": {
            const sessionId = event.properties?.sessionID;
            await captureSession(sessionId, "idle");
            break;
          }
          case "session.error": {
            const sessionId = event.properties?.sessionID;
            const error = event.properties?.error;
            log(`session error: ${sessionId || "unknown"} — ${error?.message || JSON.stringify(error) || "unknown"}`);
            await captureSession(sessionId, "error");
            break;
          }
        }
      } catch (err) {
        log(`event handler error: ${err?.message || err}`);
      }
    },

    "chat.message": async (input, output) => {
      try {
        const { sessionID } = input;
        if (!sessionID) return;

        const count = (messageCounters.get(sessionID) || 0) + 1;
        messageCounters.set(sessionID, count);
        log(`message #${count} in session ${sessionID}`);

        if (INCREMENTAL_INTERVAL > 0 && count % INCREMENTAL_INTERVAL === 0) {
          log(`incremental capture at message #${count}`);
          await captureSession(sessionID, "incremental");
        }
      } catch (err) {
        log(`chat.message handler error: ${err?.message || err}`);
      }
    },

    "tool.execute.after": async (input, output) => {
      try {
        const { tool, sessionID } = input;
        const isError =
          output.title?.toLowerCase().includes("error") ||
          output.title?.toLowerCase().includes("failed") ||
          output.output?.startsWith("Error:") ||
          output.output?.startsWith("error:");

        if (isError) {
          const errors = (toolErrors.get(sessionID) || 0) + 1;
          toolErrors.set(sessionID, errors);
          log(`tool error in session ${sessionID}: ${tool} (${errors} total errors)`);
        }
      } catch (err) {
        log(`tool.execute.after handler error: ${err?.message || err}`);
      }
    },

    "experimental.session.compacting": async (input, output) => {
      try {
        const { sessionID } = input;
        log(`session compacting: ${sessionID} — re-capturing before compaction`);
        await captureSession(sessionID, "compacted");
      } catch (err) {
        log(`compacting handler error: ${err?.message || err}`);
      }
    },
  };
};

export default AisyncPlugin;
