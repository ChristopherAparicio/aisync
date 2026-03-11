/**
 * opencode-aisync — OpenCode plugin for automatic session capture.
 *
 * Listens to OpenCode's event stream and triggers `aisync capture`
 * at the right moments:
 *
 *   - session.idle     → capture the full session (main trigger)
 *   - session.error    → capture immediately (session may not reach idle)
 *   - session.created  → log for diagnostics (no capture yet)
 *
 * The plugin is intentionally minimal: all analysis (error rates, cost,
 * token counts) happens post-capture inside aisync itself. The plugin's
 * only job is to trigger capture at the right moment with the right
 * session ID.
 *
 * Install:
 *   ln -s /path/to/aisync/plugins/opencode/aisync.ts ~/.config/opencode/plugins/aisync.ts
 *
 * Requires: `aisync` binary in PATH.
 */

import type { Plugin } from "@opencode-ai/plugin"

// Env config. AISYNC_CAPTURE_MODE defaults to "compact".
const CAPTURE_MODE = process.env.AISYNC_CAPTURE_MODE || "compact"
const DEBUG = Boolean(process.env.AISYNC_PLUGIN_DEBUG)

const AisyncPlugin: Plugin = async (ctx) => {
  const { $, worktree } = ctx

  // Track which sessions we've already captured within this OpenCode process
  // to avoid duplicate captures (idle can fire multiple times per session).
  const captured = new Set<string>()

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
  const captureSession = async (sessionId: string | undefined) => {
    if (!sessionId) return
    if (captured.has(sessionId)) {
      log(`session ${sessionId} already captured, skipping`)
      return
    }

    const branch = await currentBranch()
    log(
      `capturing session=${sessionId} branch=${branch || "(none)"} mode=${CAPTURE_MODE}`,
    )

    try {
      await $`aisync capture --provider opencode --session-id ${sessionId} --mode ${CAPTURE_MODE} --auto`
      captured.add(sessionId)
      log(`capture complete: ${sessionId}`)
    } catch (err: any) {
      // Never let capture failure affect the agent.
      log(`capture failed: ${err?.message || err}`)
    }
  }

  return {
    /**
     * The `event` hook receives ALL OpenCode events.
     * We filter for session lifecycle events and dispatch accordingly.
     *
     * Event types we care about:
     *   - session.created  → { properties: { info: { id } } }
     *   - session.idle     → { properties: { sessionID } }
     *   - session.error    → { properties: { sessionID, error } }
     */
    event: async ({ event }) => {
      try {
        switch (event.type) {
          case "session.created": {
            const sessionId = event.properties?.info?.id
            const branch = await currentBranch()
            log(
              `session started: ${sessionId || "unknown"} on branch=${branch || "(none)"}`,
            )
            break
          }

          case "session.idle": {
            const sessionId = event.properties?.sessionID
            await captureSession(sessionId)
            break
          }

          case "session.error": {
            const sessionId = event.properties?.sessionID
            const error = event.properties?.error
            log(
              `session error: ${sessionId || "unknown"} — ${(error as any)?.message || JSON.stringify(error) || "unknown"}`,
            )
            await captureSession(sessionId)
            break
          }
        }
      } catch (err: any) {
        // Absolute safety net — never crash the agent.
        log(`event handler error: ${err?.message || err}`)
      }
    },
  }
}

export default AisyncPlugin
