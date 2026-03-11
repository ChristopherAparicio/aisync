# opencode-aisync

OpenCode plugin that automatically captures AI sessions into [aisync](https://github.com/ChristopherAparicio/aisync) when the agent finishes working.

## What it does

| OpenCode Event | Plugin Action |
|---|---|
| `session.created` | Resolves current git branch, logs session start |
| `session.idle` | Triggers `aisync capture` to snapshot the full session |
| `session.error` | Captures immediately (session may not reach idle) |

The plugin is intentionally minimal. All analysis (error rates, cost breakdown, token counts, tool usage) happens inside aisync after capture. The plugin's only job is to trigger capture at the right moment with the exact session ID.

## Prerequisites

- [aisync](https://github.com/ChristopherAparicio/aisync) binary installed and in your `PATH`
- [OpenCode](https://opencode.ai) v0.2+

Verify aisync is available:

```bash
aisync --help
```

## Installation

### Option A: Local plugin (recommended for getting started)

Copy the plugin into your OpenCode global plugins directory:

```bash
# From the aisync repo
cp -r plugins/opencode ~/.config/opencode/plugins/opencode-aisync
```

Or symlink it (useful during development):

```bash
ln -s /path/to/aisync/plugins/opencode ~/.config/opencode/plugins/opencode-aisync
```

### Option B: Project-level plugin

Copy into your project's `.opencode/plugins/` directory:

```bash
mkdir -p .opencode/plugins
cp -r /path/to/aisync/plugins/opencode .opencode/plugins/opencode-aisync
```

### Option C: npm (when published)

Add to your `opencode.json`:

```json
{
  "plugin": ["opencode-aisync"]
}
```

## Configuration

The plugin is configured via environment variables. No config file needed.

| Variable | Default | Description |
|---|---|---|
| `AISYNC_CAPTURE_MODE` | `compact` | Storage mode: `full`, `compact`, or `summary` |
| `AISYNC_PLUGIN_DEBUG` | _(unset)_ | Set to `1` to enable debug logging |

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
- **Deduplication built-in.** The plugin tracks captured session IDs in memory to avoid duplicate captures within the same OpenCode process. Across processes, aisync's SQLite upsert (INSERT ON CONFLICT) handles deduplication.
- **Failure is silent.** Capture errors are swallowed. The plugin must never break the agent workflow.

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
  |     |-- session.created  -> log + resolve branch
  |     |-- session.idle     -> aisync capture --session-id <id>
  |     |-- session.error    -> aisync capture --session-id <id>
  |
  v
aisync (Go binary, runs as subprocess)
  |-- Reads OpenCode's native storage files
  |-- Parses messages, tool calls, tokens, errors
  |-- Stores in SQLite with full session data
  |-- Links to current git branch
```

The plugin does NOT:
- Parse messages or tool calls (aisync does this)
- Track errors in real-time (aisync computes this from the captured snapshot)
- Maintain its own database (aisync handles all persistence)
- Send data to any remote server (everything is local)

## Roadmap

- [ ] `tool.execute.after` hook for real-time error notifications (desktop notification on repeated failures)
- [ ] `session.compacted` hook to re-capture after OpenCode compaction
- [ ] Integration with `aisync activity emit` for richer event timeline (SessionActivity domain object)

## License

MIT
