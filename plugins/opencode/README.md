# opencode-aisync

OpenCode plugin that automatically captures AI sessions into [aisync](https://github.com/ChristopherAparicio/aisync) when the agent finishes working.

## What it does

The plugin hooks into OpenCode's full plugin API for capture and real-time monitoring:

| Hook | Trigger | Plugin Action |
|---|---|---|
| `event: session.created` | Agent starts | Logs session start + initializes counters |
| `event: session.idle` | Agent finishes | Captures full session (main trigger) |
| `event: session.error` | Agent errors | Captures immediately (may not reach idle) |
| `chat.message` | Each new message | Tracks message count + optional incremental capture |
| `tool.execute.after` | After each tool call | Detects tool errors in real-time |
| `experimental.session.compacting` | Session compaction | Re-captures before compaction (preserves history) |

All deep analysis (error rates, cost breakdown, token counts, tool usage, project categorization) happens inside aisync after capture. The plugin handles capture timing and real-time error tracking.

## Prerequisites

- [aisync](https://github.com/ChristopherAparicio/aisync) binary installed and in your `PATH`
- [OpenCode](https://opencode.ai) v0.2+

Verify aisync is available:

```bash
aisync --help
```

## Installation

### Option A: Symlink (recommended)

```bash
ln -s /path/to/aisync/plugins/opencode ~/.config/opencode/plugins/opencode-aisync
```

### Option B: Copy

```bash
cp -r /path/to/aisync/plugins/opencode ~/.config/opencode/plugins/opencode-aisync
```

### Option C: Project-level

```bash
mkdir -p .opencode/plugins
cp -r /path/to/aisync/plugins/opencode .opencode/plugins/opencode-aisync
```

## Configuration

The plugin is configured via environment variables. No config file needed.

| Variable | Default | Description |
|---|---|---|
| `AISYNC_CAPTURE_MODE` | `compact` | Storage mode: `full`, `compact`, or `summary` |
| `AISYNC_PLUGIN_DEBUG` | _(unset)_ | Set to `1` to enable debug logging |
| `AISYNC_INCREMENTAL_INTERVAL` | `0` | Capture every N messages (0 = disabled, only on idle/error) |

### Storage modes

| Mode | What's stored | Size |
|---|---|---|
| `full` | Messages + tool calls + thinking/reasoning blocks | Largest |
| `compact` | Messages + tool calls (no thinking) | Medium |
| `summary` | Metadata + token counts only (no messages) | Smallest |

## How it works

```
You code with OpenCode
       |
       | (agent works: messages, tool calls, bash commands...)
       |
       v
Agent finishes (session.idle event)
       |
       v
Plugin triggers: aisync capture --provider opencode --session-id <id> --auto
       |
       v
aisync reads the session from OpenCode's local storage
  (~/.local/share/opencode/storage/)
       |
       v
Session stored in aisync's SQLite database
  (~/.config/aisync/aisync.db)
       |
       v
Browse with: aisync list / aisync stats / aisync web
```

Key design decisions:

- **Capture on idle, not on every message.** The session is complete when the agent goes idle. Capturing mid-session would produce an incomplete snapshot.
- **Capture on error too.** If the session errors out, it may never reach idle. We capture immediately so the failed session is preserved.
- **Incremental capture (optional).** Set `AISYNC_INCREMENTAL_INTERVAL=20` to capture every 20 messages for long sessions.
- **Tool error tracking.** The plugin monitors tool call results and counts errors per session in real-time.
- **Compaction awareness.** When OpenCode compacts a session, the plugin re-captures to preserve the full history before compaction.
- **Deduplication built-in.** The plugin tracks captured session IDs in memory to avoid duplicate captures within the same OpenCode process. Across processes, aisync's SQLite upsert (INSERT ON CONFLICT) handles deduplication.
- **Failure is silent.** Capture errors are swallowed. The plugin must never break the agent workflow.

## Plugin API

This plugin uses OpenCode's official Plugin API (`@opencode-ai/plugin`). It exports a default function conforming to the `Plugin` type, and returns a `Hooks` object with a single `event` handler that dispatches on `event.type`:

```js
export default async (ctx) => ({
  event: async ({ event }) => {
    switch (event.type) {
      case "session.idle":    // → capture
      case "session.error":   // → capture
      case "session.created": // → log
    }
  },
});
```

## After capture

Once sessions are captured, use aisync to analyze them:

```bash
# List all captured sessions
aisync list

# View stats (tokens, cost, error rates)
aisync stats

# Search sessions
aisync search "authentication"

# Open the web dashboard
aisync web
# -> http://localhost:8372

# Share with your team via git
aisync push
```

## Debug mode

Enable debug logging to see what the plugin does:

```bash
AISYNC_PLUGIN_DEBUG=1 opencode
```

Output looks like:

```
[aisync] session started: ses_abc123 on branch=feature/auth
[aisync] capturing session=ses_abc123 branch=feature/auth mode=compact
[aisync] capture complete: ses_abc123
```

## Architecture

```
OpenCode Process
  |
  |-- Plugin: opencode-aisync (this)
  |     |-- event hook dispatches on event.type:
  |     |     session.created  -> log + resolve branch
  |     |     session.idle     -> aisync capture --session-id <id>
  |     |     session.error    -> aisync capture --session-id <id>
  |
  v
aisync (Go binary, runs as subprocess)
  |-- Reads OpenCode's native storage files
  |-- Parses messages, tool calls, tokens, errors
  |-- Stores in SQLite with full session data
  |-- Links to current git branch
```

The plugin does NOT:
- Parse messages or tool calls (aisync does this post-capture)
- Track cost in real-time (aisync computes this from the captured snapshot)
- Maintain its own database (aisync handles all persistence)
- Send data to any remote server (everything is local)

The plugin DOES:
- Track message counts per session (in-memory)
- Track tool errors per session (in-memory)
- Re-capture before session compaction
- Support incremental capture for long sessions

## Roadmap

- [ ] Integration with `aisync activity emit` for richer event timeline
- [ ] Expose aisync tools via the plugin `tool` hook (alongside MCP)

## License

MIT
