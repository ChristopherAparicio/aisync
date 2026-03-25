/**
 * opencode-aisync — OpenCode plugin for automatic session capture & monitoring.
 *
 * Hooks into OpenCode's plugin API to provide:
 *
 *   Event-based capture:
 *   - session.idle     → capture the full session (main trigger)
 *   - session.error    → capture immediately (session may not reach idle)
 *   - session.created  → log session start
 *
 *   Real-time monitoring:
 *   - chat.message              → track message count, incremental capture
 *   - tool.execute.after        → detect tool errors in real-time
 *   - session.compacting        → re-capture after OpenCode compaction
 *
 * Install:
 *   ln -s /path/to/aisync/plugins/opencode ~/.config/opencode/plugins/opencode-aisync
 *
 * Requires: `aisync` binary in PATH.
 */

import type { Plugin } from "@opencode-ai/plugin"

// Env config.
const CAPTURE_MODE = process.env.AISYNC_CAPTURE_MODE || "compact"
const DEBUG = Boolean(process.env.AISYNC_PLUGIN_DEBUG)
// Capture every N messages (0 = disabled, only capture on idle/error).
const INCREMENTAL_INTERVAL = parseInt(process.env.AISYNC_INCREMENTAL_INTERVAL || "0", 10)

const AisyncPlugin: Plugin = async (ctx) => {
  const { $, worktree } = ctx

  // Track which sessions we've already captured within this OpenCode process.
  const captured = new Set<string>()

  // Per-session message counters for incremental capture.
  const messageCounters = new Map<string, number>()

  // Per-session tool error counters.
  const toolErrors = new Map<string, number>()

  const log = (msg: string) => {
    if (DEBUG) console.log(`[aisync] ${msg}`)
  }

  /**
   * Resolve the current git branch. Returns "" if not in a git repo.
   */
  const currentBranch = async (): Promise<string> => {
    try {
      const result =
        await $`git -C ${worktree} rev-parse --abbrev-ref HEAD 2>/dev/null`.text()
      return result.trim()
    } catch {
      return ""
    }
  }

  /**
   * Run aisync capture for a specific session.
   * Swallows all errors — capture failure must never break the agent.
   */
  const captureSession = async (sessionId: string | undefined, reason?: string) => {
    if (!sessionId) return
    if (captured.has(sessionId) && reason !== "incremental" && reason !== "compacted") {
      log(`session ${sessionId} already captured, skipping (reason=${reason || "duplicate"})`)
      return
    }

    const branch = await currentBranch()
    log(
      `capturing session=${sessionId} branch=${branch || "(none)"} mode=${CAPTURE_MODE} reason=${reason || "idle"}`,
    )

    try {
      const args = [
        "capture",
        "--provider", "opencode",
        "--session-id", sessionId,
        "--mode", CAPTURE_MODE,
        "--auto",
      ]
      if (branch) {
        args.push("--branch", branch)
      }
      await $`aisync ${args}`
      captured.add(sessionId)
      log(`capture complete: ${sessionId}`)
    } catch (err: any) {
      log(`capture failed: ${err?.message || err}`)
    }
  }

  return {
    /**
     * The `event` hook receives ALL OpenCode events.
     */
    event: async ({ event }) => {
      try {
        switch (event.type) {
          case "session.created": {
            const sessionId = event.properties?.info?.id
            const branch = await currentBranch()
            log(`session started: ${sessionId || "unknown"} on branch=${branch || "(none)"}`)
            // Initialize counters for this session.
            if (sessionId) {
              messageCounters.set(sessionId, 0)
              toolErrors.set(sessionId, 0)
            }
            break
          }

          case "session.idle": {
            const sessionId = event.properties?.sessionID
            await captureSession(sessionId, "idle")
            break
          }

          case "session.error": {
            const sessionId = event.properties?.sessionID
            const error = event.properties?.error
            log(
              `session error: ${sessionId || "unknown"} — ${(error as any)?.message || JSON.stringify(error) || "unknown"}`,
            )
            await captureSession(sessionId, "error")
            break
          }
        }
      } catch (err: any) {
        log(`event handler error: ${err?.message || err}`)
      }
    },

    /**
     * Called when a new message is received.
     * Tracks message count and triggers incremental capture if configured.
     */
    "chat.message": async (input, output) => {
      try {
        const { sessionID } = input
        if (!sessionID) return

        const count = (messageCounters.get(sessionID) || 0) + 1
        messageCounters.set(sessionID, count)
        log(`message #${count} in session ${sessionID}`)

        // Incremental capture every N messages (if configured).
        if (INCREMENTAL_INTERVAL > 0 && count % INCREMENTAL_INTERVAL === 0) {
          log(`incremental capture at message #${count}`)
          await captureSession(sessionID, "incremental")
        }
      } catch (err: any) {
        log(`chat.message handler error: ${err?.message || err}`)
      }
    },

    /**
     * Called after each tool execution completes.
     * Detects tool errors and tracks error rate per session.
     */
    "tool.execute.after": async (input, output) => {
      try {
        const { tool, sessionID } = input
        // Check if tool output indicates an error.
        const isError =
          output.title?.toLowerCase().includes("error") ||
          output.title?.toLowerCase().includes("failed") ||
          output.output?.startsWith("Error:") ||
          output.output?.startsWith("error:")

        if (isError) {
          const errors = (toolErrors.get(sessionID) || 0) + 1
          toolErrors.set(sessionID, errors)
          log(`tool error in session ${sessionID}: ${tool} (${errors} total errors)`)
        }
      } catch (err: any) {
        log(`tool.execute.after handler error: ${err?.message || err}`)
      }
    },

    /**
     * Called when OpenCode compacts a session (reduces message history).
     * Re-captures to preserve the pre-compaction state.
     */
    "experimental.session.compacting": async (input, output) => {
      try {
        const { sessionID } = input
        log(`session compacting: ${sessionID} — re-capturing before compaction`)
        await captureSession(sessionID, "compacted")
      } catch (err: any) {
        log(`compacting handler error: ${err?.message || err}`)
      }
    },
  }
}

export default AisyncPlugin
