/**
 * opencode-aisync — OpenCode plugin for automatic session capture.
 *
 * What it does:
 *   - session.created  → resolves current git branch, logs session start
 *   - session.idle     → triggers `aisync capture` to snapshot the session
 *   - session.error    → logs the error for diagnostics
 *
 * The plugin is intentionally minimal: all analysis (error rates, cost,
 * token counts) happens post-capture inside aisync itself. The plugin's
 * only job is to trigger capture at the right moment with the right
 * session ID.
 *
 * Install:
 *   cp -r plugins/opencode ~/.config/opencode/plugins/opencode-aisync
 *   # or add "opencode-aisync" to your opencode.json "plugin" array
 *
 * Requires: `aisync` binary in PATH.
 */

// Read env-level config. AISYNC_CAPTURE_MODE defaults to "compact".
const CAPTURE_MODE = process.env.AISYNC_CAPTURE_MODE || "compact";
const DEBUG = Boolean(process.env.AISYNC_PLUGIN_DEBUG);

/**
 * @param {object} ctx - OpenCode plugin context
 * @param {object} ctx.project - Current project info
 * @param {string} ctx.directory - Current working directory
 * @param {string} ctx.worktree - Git worktree path
 * @param {Function} ctx.$ - Bun shell API
 */
export const AisyncPlugin = async (ctx) => {
  const { $, worktree } = ctx;

  // Per-session state: track which sessions we've already captured
  // to avoid duplicate captures within the same OpenCode process.
  const captured = new Set();

  const log = (msg) => {
    if (DEBUG) console.log(`[aisync] ${msg}`);
  };

  /**
   * Resolve the current git branch. Returns "" if not in a git repo.
   */
  const currentBranch = async () => {
    try {
      const result =
        await $`git -C ${worktree} rev-parse --abbrev-ref HEAD 2>/dev/null`.text();
      return result.trim();
    } catch {
      return "";
    }
  };

  /**
   * Run aisync capture for a specific session.
   * Swallows errors — capture failure must never break the agent.
   */
  const captureSession = async (sessionId) => {
    if (!sessionId) return;
    if (captured.has(sessionId)) {
      log(`session ${sessionId} already captured, skipping`);
      return;
    }

    const branch = await currentBranch();
    log(
      `capturing session=${sessionId} branch=${branch || "(none)"} mode=${CAPTURE_MODE}`,
    );

    try {
      await $`aisync capture --provider opencode --session-id ${sessionId} --mode ${CAPTURE_MODE} --auto`;
      captured.add(sessionId);
      log(`capture complete: ${sessionId}`);
    } catch (err) {
      // Never let capture failure affect the agent.
      log(`capture failed: ${err.message || err}`);
    }
  };

  return {
    /**
     * session.created — A new session has started.
     * We log the branch for context but don't capture yet (session is empty).
     */
    "session.created": async (input) => {
      const sessionId = input?.properties?.session?.id;
      const branch = await currentBranch();
      log(
        `session started: ${sessionId || "unknown"} on branch=${branch || "(none)"}`,
      );
    },

    /**
     * session.idle — The agent has finished working.
     * This is the main trigger: capture the full session now.
     */
    "session.idle": async (input) => {
      const sessionId = input?.properties?.session?.id;
      await captureSession(sessionId);
    },

    /**
     * session.error — The session encountered an error.
     * We capture immediately (the session may not reach idle).
     */
    "session.error": async (input) => {
      const sessionId = input?.properties?.session?.id;
      log(
        `session error: ${sessionId || "unknown"} — ${input?.properties?.error || "unknown error"}`,
      );
      await captureSession(sessionId);
    },
  };
};
