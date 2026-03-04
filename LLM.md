# aisync -- LLM Quick Reference

> CLI that captures AI coding sessions (Claude Code, OpenCode, Cursor), links them to Git branches/commits/PRs, and restores them later -- even across different AI tools and team members.

## Project Layout

```
cmd/aisync/main.go           Entry point
pkg/cmd/root/root.go         All commands registered here (28 subcommands)
pkg/cmd/<name>/              One subpackage per CLI command (Cobra)
pkg/cmdutil/factory.go       Factory struct (lazy DI container)
pkg/cmd/factory/default.go   Composition root (wires all dependencies)
internal/session/            Shared types: Session, Message, User, StructuredSummary, enums, errors
internal/provider/           Provider interface + claude/, opencode/, cursor/
internal/storage/            Store interface (15 methods) + sqlite/ implementation
internal/capture/            Capture orchestration service
internal/restore/            Restore orchestration service
internal/service/            SessionService (high-level ops: Summarize, Explain, Rewind), SyncService (push/pull)
internal/llm/                LLM client port (interface) + claude/ adapter
internal/api/                HTTP API server (19 endpoints, stdlib net/http)
internal/mcp/                MCP server (18 tools, via mark3labs/mcp-go)
internal/gitsync/            Git branch sync service (push/pull)
internal/converter/          Cross-provider format conversion (SessionConverter)
internal/secrets/            Secret detection & masking + plugin system
internal/platform/           GitHub/GitLab platform integration
internal/hooks/              Git hooks management
internal/tui/                Interactive terminal UI (Bubble Tea)
internal/config/             Config with summarize.enabled / summarize.model
client/                      HTTP client SDK (19 methods, mirrors API 1:1)
git/                         Git CLI wrapper (includes UserName/UserEmail, Checkout)
```

## Architecture

**Server = Hexagonal (DDD), Client/CLI = Pragmatic/flat.**

Three layers:
1. **Domain** (`internal/session/`) -- pure types, no I/O
2. **Ports** (`Provider`, `Store`, `SessionConverter` interfaces) -- boundaries
3. **Adapters** (`sqlite/`, `claude/`, `opencode/`, `cursor/`, `api/`, `mcp/`) -- implementations

Service orchestration:
- `internal/capture/` and `internal/restore/` -- core capture/restore logic
- `internal/service/SessionService` -- high-level operations (Capture with owner resolution, Restore, Summarize, Explain, Rewind, ListByBranch, etc.)
- `internal/service/SyncService` -- git-based session sync (push/pull/bidirectional)

LLM integration:
- `internal/llm/client.go` -- `LLMClient` interface (port) with `Complete(ctx, CompletionRequest) → CompletionResponse`
- `internal/llm/claude/` -- Claude CLI adapter (`claude --print --output-format json`)

HTTP API flow: `CLI cmd` -> `client.Client` -> HTTP -> `internal/api/` -> `SessionService` -> `Store`/`Provider`

## Commands (28 total)

| Command | Description | Key Flags |
|---------|-------------|-----------|
| `aisync init` | Initialize aisync in current repo, create `.aisync/config.json`, offer to install hooks | `--no-hooks` |
| `aisync status` | Show branch, detected providers, sessions, hooks status | |
| `aisync capture` | Capture active AI session and store it | `--provider`, `--mode full\|compact\|summary`, `--message`, `--auto`, `--summarize` |
| `aisync restore` | Restore a session into an AI tool | `--session <id>`, `--provider`, `--agent`, `--pr <n>`, `--as-context` |
| `aisync explain <id>` | AI-generated explanation of a session | `--short`, `--json`, `--model` |
| `aisync resume <branch>` | Checkout branch + restore session in one step | `--session <id>`, `--provider`, `--as-context` |
| `aisync rewind <id>` | Fork a session at message N, discard later messages | `--message <n>`, `--json` |
| `aisync list` | List captured sessions | `--all`, `--branch`, `--pr <n>` |
| `aisync show <id>` | Show session details (by session ID or commit SHA) | `--files`, `--tokens` |
| `aisync export` | Export a session to file | `--format aisync\|claude\|opencode\|context`, `--session <id>`, `-o <file>` |
| `aisync import <file>` | Import a session from file | `--format`, `--into aisync\|claude-code\|opencode`, `--agent` |
| `aisync link` | Link a session to a Git object | `--pr <n>`, `--commit <sha>`, `--session <id>`, `--auto` |
| `aisync comment` | Post session summary as PR comment | `--pr <n>`, `--session <id>` |
| `aisync search` | Search sessions by keyword, branch, user, date | `[keyword]`, `--branch`, `--provider`, `--owner-id`, `--since`, `--until`, `--limit`, `--json`, `-q` |
| `aisync blame <file>` | Find which AI sessions touched a file | `--all`, `--restore`, `--branch`, `--provider`, `--json`, `-q` |
| `aisync stats` | Show usage statistics (tokens, sessions, files) | `--branch`, `--provider`, `--all`, `--json` |
| `aisync hooks install` | Install git hooks (pre-commit, commit-msg, post-checkout) | |
| `aisync hooks uninstall` | Remove aisync hooks | |
| `aisync secrets scan` | Scan stored sessions for secrets | `--session <id>` |
| `aisync config` | View or edit aisync configuration | |
| `aisync push` | Push sessions to git sync branch (`aisync/sessions`) | |
| `aisync pull` | Pull sessions from git sync branch | |
| `aisync sync` | Bidirectional sync (push + pull) | |
| `aisync serve` | Start HTTP API server | `--port`, `--host` |
| `aisync mcp serve` | Start MCP (Model Context Protocol) server | |
| `aisync tui` | Launch interactive terminal UI | |
| `aisync version` | Print version | |
| `aisync completion` | Generate shell completions | `bash\|zsh\|fish\|powershell` |

## Supported Providers

| Provider | Export | Import | Session Location |
|----------|--------|--------|------------------|
| Claude Code | Yes | Yes | `~/.claude/projects/<encoded-path>/*.jsonl` |
| OpenCode | Yes | Yes | SQLite / `opencode session export` |
| Cursor | Yes | No (CONTEXT.md fallback) | `state.vscdb` SQLite |

## Key Interfaces (5)

- **`Provider`** in `internal/provider/provider.go` -- `Name()`, `Detect()`, `Export()`, `CanImport()`, `Import()`
- **`Store`** in `internal/storage/store.go` -- 14 methods: `Save`, `Get`, `GetByBranch`, `List`, `Delete`, `AddLink`, `GetByLink`, `Search`, `GetSessionsByFile`, `SaveUser`, `GetUser`, `GetUserByEmail`, `Close`
- **`SessionConverter`** in `internal/converter/converter.go` -- `Convert(session, targetFormat)`, `SupportedFormats()`
- **`llm.Client`** in `internal/llm/client.go` -- `Complete(ctx, CompletionRequest) → CompletionResponse`
- **`platform.Platform`** in `internal/platform/` -- GitHub/GitLab integration

## HTTP API (19 endpoints)

All served by `internal/api/` using stdlib `net/http`. Client SDK in `client/` mirrors 1:1.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/sessions` | Create/capture a session |
| GET | `/api/sessions` | List sessions (query: branch, provider, limit) |
| GET | `/api/sessions/{id}` | Get session by ID |
| DELETE | `/api/sessions/{id}` | Delete session |
| POST | `/api/sessions/{id}/links` | Add link to session |
| GET | `/api/sessions/links/{type}/{ref}` | Get session by link |
| GET | `/api/sessions/branch/{branch}` | Get sessions by branch |
| GET | `/api/sessions/search` | Search sessions |
| GET | `/api/sessions/stats` | Get usage statistics |
| POST | `/api/sessions/{id}/export` | Export session |
| POST | `/api/sessions/import` | Import session |
| POST | `/api/sessions/{id}/restore` | Restore session |
| GET | `/api/v1/blame` | Find sessions that touched a file |
| POST | `/api/v1/sessions/explain` | AI-generated session explanation |
| POST | `/api/v1/sessions/rewind` | Fork session at message N |
| POST | `/api/sync/push` | Push sessions |
| POST | `/api/sync/pull` | Pull sessions |
| POST | `/api/sync` | Bidirectional sync |

## MCP Server (18 tools)

Served via `internal/mcp/` using `mark3labs/mcp-go`. Tools: `aisync_capture`, `aisync_restore`, `aisync_get`, `aisync_list`, `aisync_delete`, `aisync_export`, `aisync_import`, `aisync_link`, `aisync_comment`, `aisync_search`, `aisync_blame`, `aisync_explain`, `aisync_rewind`, `aisync_stats`, `aisync_push`, `aisync_pull`, `aisync_sync`, `aisync_index`.

## SQLite Schema

```sql
sessions       -- id, branch, provider, mode, commit_sha, message, owner_id, metadata, timestamps
messages       -- session_id, role, content, tool_calls, token counts, cost, timestamp
links          -- session_id, type (pr/commit/issue), ref, platform
users          -- id, name, email, created_at
```

## User Identity

Sessions have an `owner_id` linking to the `users` table. Owner is resolved from `git config user.name` / `user.email` at capture time. The `User` type is defined in `internal/session/session.go`.

## Build & Test

```bash
make build       # Build binary to bin/aisync
make test        # Run all tests (37 test files)
make lint        # Run golangci-lint
make install     # Install to $GOPATH/bin
```

## Config

Two levels: global (`~/.aisync/config.json`) + per-repo (`.aisync/config.json`). Per-repo overrides global.

Key settings: `storage_mode` (full/compact/summary), `providers`, `auto_capture`, `secrets.mode` (mask/warn/block), `summarize.enabled` (bool), `summarize.model` (string).

## Search Feature

Search across all captured sessions using keyword matching, filters, or a combination.

- **Domain types:** `SearchQuery` and `SearchResult` in `internal/session/session.go`
- **Store method:** `Search(query SearchQuery) (*SearchResult, error)` using composable `buildSearchWhere()` SQL builder
- **Keyword search:** case-insensitive LIKE on `summary` column (FTS5 for message content deferred)
- **Filters:** branch, provider, owner_id, project_path, since/until (AND logic)
- **Pagination:** default limit 50, max 200
- **Time parsing:** `parseFlexibleTime()` accepts both RFC3339 and `YYYY-MM-DD` formats

## Testing Notes

- 39 test files, all passing
- 14 test files contain `mockStore` structs implementing `Store` -- when adding methods to `Store`, all 14 must be updated
- `internal/service/session_test.go` -- 12 tests for Summarize, Explain, Rewind, OneLine, transcript builder
- `internal/llm/claude/claude_test.go` -- 6 tests with mock binary, fallback, context cancellation
- Mock locations: `internal/capture/`, `internal/restore/`, `internal/service/`, and 11 `pkg/cmd/*/` test files (including blamecmd)
