# aisync

**Link AI sessions to Git branches. Capture, restore, share.**

aisync is a CLI tool that captures your AI coding sessions (Claude Code, OpenCode, Cursor), links them to Git commits and branches, and lets you restore them later -- even across different AI tools and team members.

## The Problem

When you use an AI agent to code on a branch, the conversation context disappears after the session:

- **Context is lost** -- if a bug surfaces during PR review or CI, nobody can understand why the agent made that choice
- **Fixing is expensive** -- you waste tokens re-explaining everything from scratch
- **Handoff is painful** -- a colleague taking over your branch has zero context about the AI conversation that produced the code

## How aisync Solves This

```
Developer codes with AI agent
        |
        v
git commit -m "feat: add OAuth2"
        |
        v
[aisync hook] captures the AI session automatically
        |
        +-- detects provider (Claude Code / OpenCode / Cursor)
        +-- scans for secrets, redacts them
        +-- stores session in local SQLite
        +-- adds AI-Session trailer to commit message
        |
        v
Later: PR breaks, colleague reviews, or you come back after a week
        |
        v
aisync restore
        |
        +-- reloads the full AI conversation into your agent
        +-- works cross-provider (Claude -> OpenCode and vice versa)
        +-- full context preserved: decisions, files touched, token usage
```

## Quick Start

```bash
# Install (from source)
git clone https://github.com/ChristopherAparicio/aisync.git
cd aisync
make install

# Initialize in your project
cd your-project
aisync init

# Capture the current AI session manually
aisync capture

# List sessions on the current branch
aisync list

# Restore a session (e.g. after switching branches)
aisync restore

# Show session details
aisync show <session-id>
```

With hooks installed (`aisync init` offers this), capture happens automatically on every `git commit`.

## Commands

| Command | What it does |
|---------|-------------|
| `aisync init` | Initialize aisync in a Git repo, install hooks |
| `aisync capture` | Capture active AI session (auto-detects provider) |
| `aisync restore` | Restore a session for the current branch or PR |
| `aisync explain` | AI-generated explanation of a session |
| `aisync resume` | Checkout branch + restore session in one step |
| `aisync rewind` | Fork a session at message N (discard bad turns) |
| `aisync list` | List captured sessions |
| `aisync show` | Inspect a session (by ID or commit SHA) |
| `aisync export` | Export a session to a file (unified, Claude, or OpenCode format) |
| `aisync import` | Import a session from a file, with cross-provider conversion |
| `aisync link` | Link a session to a PR or commit |
| `aisync comment` | Post a session summary as a PR comment |
| `aisync stats` | Token usage, session counts, most-touched files |
| `aisync push/pull/sync` | Share sessions with your team via a Git branch |
| `aisync hooks install` | Install Git hooks for automatic capture |
| `aisync secrets scan` | Scan sessions for leaked secrets |
| `aisync tui` | Interactive terminal UI to browse sessions |
| `aisync blame` | Find which AI sessions touched a file |
| `aisync serve` | Start HTTP/REST API server (19 endpoints) |
| `aisync search` | Search sessions by keyword, branch, provider, time range |
| `aisync mcp` | Start MCP server for AI tool integration (18 tools) |

Run `aisync <command> --help` for detailed flags and usage.

## Supported AI Providers

| Provider | Capture | Restore | How |
|----------|---------|---------|-----|
| **Claude Code** | Yes | Yes | Reads/writes JSONL from `~/.claude/projects/` |
| **OpenCode** | Yes | Yes | Reads SQLite / uses CLI export/import |
| **Cursor** | Yes | No (generates `CONTEXT.md` fallback) | Reads `state.vscdb` |

### Cross-Provider Restore

Sessions captured from one provider can be restored into another:

- **Claude Code -> OpenCode**: full conversion, preserving messages and tool calls
- **OpenCode -> Claude Code**: flattens sub-agent sessions into a linear thread
- **Any -> Cursor**: generates a `CONTEXT.md` file you can reference with `@CONTEXT.md`

## Key Features

### AI-Powered Session Intelligence
- **Auto-summarize** -- `aisync capture --summarize` generates structured AI summaries (intent, outcome, decisions, friction, open items)
- **Explain** -- `aisync explain <id>` produces a natural language explanation of what happened during a session
- **Resume** -- `aisync resume <branch>` switches branch and restores session context in one step
- **Rewind** -- `aisync rewind <id> --message N` forks a session at a specific point, discarding bad turns

### Automatic Capture on Commit
Git hooks capture the active AI session every time you commit. A `AI-Session: <id>` trailer is added to the commit message for traceability.

### Secret Detection
Before storing, aisync scans for API keys, tokens, passwords, JWTs, and private keys. Secrets are redacted by default (`***REDACTED:TYPE***`). Configurable modes: `mask`, `warn`, `block`.

### Storage Modes
Control how much data is stored per session:

| Mode | What's stored | Typical size |
|------|--------------|-------------|
| `full` | Everything (messages, tool calls, thinking) | 1-50 MB |
| `compact` | User/assistant messages only | 100 KB - 2 MB |
| `summary` | Auto summary + decisions + file list | 5-50 KB |

### Team Sharing
Push sessions to a shared `aisync/sessions` Git branch so colleagues can pull them. When someone takes over your branch, they get the full AI context.

### PR Integration
Link sessions to PRs, post session summaries as comments, restore sessions from PR numbers, and view per-PR statistics.

## MCP Server Integration

aisync exposes 18 tools via the [Model Context Protocol](https://modelcontextprotocol.io/) (MCP), allowing your AI assistant to capture, restore, explain, rewind, list, search, and manage sessions directly from within your coding conversation.

Start the MCP server manually:

```bash
aisync mcp
```

The server communicates over stdio (JSON-RPC 2.0). In practice, you configure your AI tool to launch it automatically.

### Configure for Claude Code

Add a `.mcp.json` file at your project root:

```json
{
  "aisync": {
    "command": "aisync",
    "args": ["mcp"]
  }
}
```

Or if `aisync` is not in your `$PATH`, use the full path:

```json
{
  "aisync": {
    "command": "/usr/local/bin/aisync",
    "args": ["mcp"]
  }
}
```

Claude Code will auto-start the MCP server and make all aisync tools available during your conversation. The tools appear with the `aisync_` prefix (e.g., `aisync_capture`, `aisync_list`).

### Configure for OpenCode

Add an `mcp` section to your `opencode.json` (at project root or `~/.config/opencode/config.json`):

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "aisync": {
      "type": "local",
      "command": ["aisync", "mcp"],
      "enabled": true
    }
  }
}
```

### Available MCP Tools

| Tool | Description |
|------|-------------|
| `aisync_capture` | Capture the current AI session |
| `aisync_restore` | Restore a session into the current provider |
| `aisync_get` | Get session details by ID |
| `aisync_list` | List captured sessions |
| `aisync_search` | Search sessions by keyword, branch, provider, time range |
| `aisync_blame` | Find which AI sessions touched a file |
| `aisync_explain` | AI-generated explanation of a session |
| `aisync_rewind` | Fork a session at message N |
| `aisync_delete` | Delete a session |
| `aisync_export` | Export a session (aisync, Claude, OpenCode, or context format) |
| `aisync_import` | Import a session from raw data |
| `aisync_link` | Link a session to a PR or commit |
| `aisync_comment` | Post/update a PR comment with session summary |
| `aisync_stats` | Get session statistics (tokens, counts, files) |
| `aisync_push` | Push sessions to the git sync branch |
| `aisync_pull` | Pull sessions from the git sync branch |
| `aisync_sync` | Pull then push (bidirectional sync) |
| `aisync_index` | Read the sync branch index |

### Example: Capture from Within Claude Code

Once configured, you can ask your AI assistant to interact with aisync directly:

> "Capture this session with aisync"
>
> "List all aisync sessions on this branch"
>
> "Search aisync for sessions related to authentication"
>
> "Show me the stats for this project"

The AI assistant will call the appropriate MCP tools automatically.

## Configuration

Two-level config: global (`~/.aisync/config.json`) + per-repo (`.aisync/config.json`).

```json
{
  "version": 1,
  "providers": ["claude-code", "opencode"],
  "auto_capture": true,
  "storage_mode": "compact",
  "secrets": {
    "mode": "mask",
    "scan_tool_outputs": true
  }
}
```

```bash
aisync config set storage-mode summary
aisync config set secrets.mode warn
aisync config set auto-capture false
aisync config set summarize.enabled true    # AI-powered summaries on every capture
aisync config set summarize.model sonnet    # Specific model for summarization
```

## Build from Source

**Requirements:** Go 1.22+, Git 2.30+

```bash
make build        # Build binary to bin/aisync
make test         # Run all tests
make lint         # Run golangci-lint
make install      # Install to $GOPATH/bin
```

Cross-platform releases (Linux, macOS, Windows / amd64, arm64) are built with GoReleaser and published on [GitHub Releases](https://github.com/ChristopherAparicio/aisync/releases).

## Architecture

aisync follows **Hexagonal Architecture** (Ports & Adapters) with a clear server/client split:

- **Service layer** -- `SessionService` (15 methods incl. Summarize, Explain, Rewind) and `SyncService` (4 methods) orchestrate all business logic
- **Three driving adapters** -- CLI (Cobra), HTTP/REST API (stdlib `net/http`), MCP Server (`mark3labs/mcp-go`)
- **LLM integration** -- optional `llm.Client` port with Claude CLI adapter for AI-powered features
- **Provider layer** -- pluggable readers/writers for each AI tool (3 implementations)
- **Storage layer** -- local SQLite with 4 tables (sessions, session_links, file_changes, users)
- **Client SDK** -- public Go HTTP client (`client/` package) for programmatic access
- **User identity** -- auto-detected from git config, sessions tagged with owner

5 interfaces as ports: `Provider` (3 implementations), `Store` (extensibility + testing), `SessionConverter` (testing), `llm.Client` (LLM backends), `platform.Platform` (GitHub/GitLab).

For full details, see [architecture/](./architecture/), [spec.md](./spec.md), and [CONTRIBUTING.md](./CONTRIBUTING.md).

## Project Status

| Phase | Status | Description |
|-------|--------|-------------|
| Phase 1-3.5 -- Core | Done | Capture, restore, export/import, Git sync, PR linking, stats, service layer, HTTP API, Client SDK, MCP Server |
| Phase 5.0-5.5 -- AI Intelligence | Done | Summarize, Explain, Rewind, Blame, Off-topic detection, Cost tracking, Forecast |
| Phase 6.2-6.3 -- Web Dashboard | Done | Server-rendered dark theme dashboard, sessions/branches/costs/projects pages, HTMX |
| Phase 7.1-7.4 -- Infrastructure | Done | Client/server dual-mode, SQLite with stats cache, daemon mode, systemd/launchd |
| Phase 8.1-8.9 -- Platform | Done | Universal ingest, session tagging, project identity, analysis (4 LLM adapters), session replay, skill resolver, webhooks |
| Phase 9.1-9.3 -- Operations | Done | JWT + API key auth, webhooks (4 event types), scheduled tasks (GC, capture, stats), zstd compression, telemetry framework |
| Phase 6.1 -- Cross-model Replay | Future | Replay sessions with different LLM models for comparison |
| Phase 4 -- CI Automation | Future | Auto-fix sessions on CI failure, GitHub Actions integration |

**1456 tests across 87 packages. 5 providers supported: Claude Code, OpenCode, Cursor, Parlay, Ollama.**

See [roadmap.md](./roadmap.md) for detailed milestones, [spec.md](./spec.md) for user stories and future vision.

## License

MIT
