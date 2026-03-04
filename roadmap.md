# aisync — Roadmap

> Last updated: 2026-03-04
> Status: Phase 1-3.5, 5.0, 5.2 COMPLETE — Phase 4, 5.1, 5.3-5.4, 6 designed.
> Phase 5.0 completed: AI Summarization, Explain, Resume, Rewind (LLM-powered session intelligence).

---

## Decisions Log

Decisions taken during the design phase, before any code was written.

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| D1 | Language & stack | **Go** (Cobra CLI, SQLite via modernc.org/sqlite) | Single binary, fast, great CLI ecosystem |
| D2 | Providers (MVP) | **Claude Code + OpenCode** | Both support export AND import. Cursor is read-only (Phase 2) |
| D3 | Session granularity | **1 session per branch** | A branch = a feature = a conversation. Updated on each capture. |
| D4 | Summary generation | **Use provider's native summary** | Claude Code and OpenCode already generate summaries. No LLM dependency. |
| D5 | Commit trailer | **Visible trailer + Git notes** | Trailer (`AI-Session: <id>`) for traceability, git notes for full metadata |
| D6 | Config location | **Global (`~/.aisync/`) + per-repo (`.aisync/`)** | Like `.gitconfig` + `.git/config`. Per-repo overrides global. |
| D7 | Secret detection (MVP) | **Built-in regex patterns, `mask` mode** | Interface `SecretScanner` for future plugin support |
| D8 | Plugin system (future) | **Go compiled interface (MVP) → Go native plugin + Hashicorp go-plugin** | Start simple, evolve to external plugins |
| D9 | Distribution | **GoReleaser → GitHub Releases** | Cross-platform binaries, checksums, standard Go approach |
| D10 | Tests | **Day 1, table-driven, Go standard `testing` package** | No external test framework. Fixtures with real session data. |
| D11 | Architecture | **Hexagonal / Ports & Adapters + gh CLI patterns** | Service layer, Factory DI, interfaces as ports, multiple driving adapters (CLI, API, MCP) |
| D12 | Performance (hooks) | **No hard constraint for MVP** | Reliability over speed. Can optimize later. |
| D13 | Claude Code restore | **CLI `claude --resume` or JSONL copy** | Investigate both, prefer CLI if available |
| D14 | Storage modes | **full / compact / summary** | Configurable per capture, default `compact` |
| D15 | Export/Import CLI | **`aisync export` / `aisync import`** | Export in unified or native format, import with cross-provider conversion |
| D16 | Go module path | **`github.com/ChristopherAparicio/aisync`** | GitHub repo created |
| D17 | Agent field | **`agent` field in unified format** | "claude" for Claude Code (default), real agent name for OpenCode. `--agent` flag on restore/import |
| D18 | Child sessions (OpenCode) | **Preserve tree in unified, flatten for Claude Code** | Unified format stores parent + children. Convert to Claude = flatten into linear thread |
| D19 | Tool call model | **Keep both models in unified format** | ToolCall has `state` (lifecycle from OpenCode) + `output` (merged from Claude tool_result). Lossless both ways |
| D20 | Git branch injection | **Inject at capture time for OpenCode** | Claude Code has native `gitBranch`. OpenCode doesn't — inject from `git rev-parse` at capture |

---

> **Architecture details** → see [architecture/](./architecture/)

---

## Phase 1 — MVP (Target: 3-4 weeks)

### Milestone 1.1 — Foundation (Week 1)

**Goal:** Project skeleton, domain model, basic CLI working.

- [x] **1.1.1** Initialize Go module, project structure, Makefile
- [x] **1.1.2** Setup golangci-lint (`.golangci.yml`) — run on every milestone
- [x] **1.1.3** Define domain entities: `Session`, `Message`, `FileChange`, `Link`, `TokenUsage`
- [x] **1.1.4** Define domain interfaces: `Provider`, `Store`, `Config`, `SecretScanner`
- [x] **1.1.5** Implement config layer (global `~/.aisync/config.json` + per-repo `.aisync/config.json`, merge logic)
- [x] **1.1.6** Implement SQLite storage (`Store` interface)
  - Schema creation with embedded migrations
  - CRUD: Save, Get, List, Delete sessions
  - Link management: branch, commit, PR links
  - File change tracking
- [x] **1.1.7** Setup Cobra CLI skeleton with root command + `version`, `init`, `status`
- [x] **1.1.8** Setup Factory pattern (dependency wiring)
- [x] **1.1.9** Setup GoReleaser config (`.goreleaser.yml`)
- [x] **1.1.10** Setup CI: GitHub Actions for test + lint + build
- [x] **1.1.11** Run `make lint` — must pass with zero issues before moving to 1.2

### Milestone 1.2 — Providers (Week 2)

**Goal:** Read sessions from Claude Code and OpenCode.

- [x] **1.2.1** Research: Claude Code session format
  - Explore `~/.claude/projects/` directory structure
  - Understand path encoding
  - Parse JSONL message format
  - Identify branch info, timestamps, file changes, summary
  - Write integration test with real fixture data
- [x] **1.2.2** Implement Claude Code provider
  - `Detect()`: find active sessions matching current project + branch
  - `Export()`: parse JSONL → unified `Session` model
  - `CanImport()`: return true
  - `Import()`: copy JSONL back / use `claude --resume`
- [x] **1.2.3** Research: OpenCode session format
  - Explore OpenCode's internal SQLite or session files
  - Test `opencode session list` / `opencode session export` if available
  - Understand session structure, metadata
  - Write integration test with real fixture data
- [x] **1.2.4** Implement OpenCode provider
  - `Detect()`: find active sessions
  - `Export()`: read session → unified `Session` model
  - `CanImport()`: return true
  - `Import()`: write session back / use OpenCode CLI
- [x] **1.2.5** Implement provider registry (auto-detection + manual selection)
- [x] **1.2.6** Run `make lint && make test` — must pass with zero issues before moving to 1.3

### Milestone 1.3 — Capture & Restore (Week 3)

**Goal:** Core workflows working end-to-end.

- [x] **1.3.1** Implement capture service
  - Detect active providers
  - Correlate staged files with session file changes
  - Export session via provider
  - Store in SQLite
  - Support `--mode full|compact|summary`
  - Support `--provider` override
  - Support `--message` for manual summary
- [x] **1.3.2** Implement `aisync capture` CLI command
- [x] **1.3.3** Implement restore service
  - Lookup session by branch / session-id
  - Detect target provider (or use source provider)
  - Import via provider (or generate CONTEXT.md fallback)
- [x] **1.3.4** Implement `aisync restore` CLI command
- [x] **1.3.5** Implement `aisync list` CLI command (sessions for current branch)
- [x] **1.3.6** Implement `aisync show <session-id|commit-sha>` CLI command
- [x] **1.3.7** Run `make lint && make test` — must pass with zero issues before moving to 1.4

### Milestone 1.4 — Git Integration (Week 3-4)

**Goal:** Automatic capture on commit, trailer in commit messages.

- [x] **1.4.1** Implement git hooks manager
  - Install/uninstall pre-commit + commit-msg + post-checkout hooks
  - Respect existing hooks (chain, don't replace)
  - Embedded hook scripts via `//go:embed`
- [x] **1.4.2** `pre-commit` hook: trigger `aisync capture --auto`
- [x] **1.4.3** `commit-msg` hook: append `AI-Session: <id>` trailer
- [x] **1.4.4** `post-checkout` hook: notify if session available for new branch
- [x] **1.4.5** Git notes: store full session metadata as git note on commit
- [x] **1.4.6** Implement `aisync hooks install` / `aisync hooks uninstall` CLI commands
- [x] **1.4.7** Implement `aisync init` (full init flow: config + hooks + first status)
- [x] **1.4.8** Run `make lint && make test` — must pass with zero issues before moving to 1.5

### Milestone 1.5 — Secret Detection (Week 4)

**Goal:** Built-in secret scanning before storage.

- [x] **1.5.1** Define `SecretScanner` interface in domain
- [x] **1.5.2** Implement built-in regex scanner
  - Patterns: `sk-`, `pk_`, `AKIA`, `ghp_`, `gho_`, `glpat-`, `xoxb-`, `xoxp-`, JWT, private keys, env vars
  - Scan message content + tool call outputs (configurable)
- [x] **1.5.3** Implement masking: replace secrets with `***REDACTED:TYPE***`
- [x] **1.5.4** Support 3 modes: `mask` (default), `warn`, `block`
- [x] **1.5.5** Integrate scanner into capture service (scan before store)
- [x] **1.5.6** Implement `aisync secrets scan` (scan existing sessions)
- [x] **1.5.7** Implement `aisync config set secrets.mode <mode>`
- [x] **1.5.8** Run `make lint && make test` — must pass with zero issues before moving to 1.6

### Milestone 1.6 — Export, Import & Convert (Week 4-5)

**Goal:** Dump sessions to files, import from files, convert between provider formats.

- [x] **1.6.1** Implement `aisync export` CLI command
  - Export current branch session or by `--session <id>`
  - `--format aisync` (default): unified JSON format
  - `--format claude`: native Claude Code JSONL
  - `--format opencode`: native OpenCode format
  - `--format context`: CONTEXT.md fallback
  - `-o <file>` or stdout
- [x] **1.6.2** Implement `aisync import` CLI command
  - Import from a file: auto-detect format or `--format` flag
  - `--into <provider>`: convert and inject into target provider
  - `--into aisync` (default): store in local SQLite only
  - Validate imported data, scan for secrets before storing
- [x] **1.6.3** Implement cross-provider conversion
  - Claude Code JSONL → unified → OpenCode format
  - OpenCode format → unified → Claude Code JSONL
  - Any format → CONTEXT.md fallback
  - Converter: `ToNative(session, target) ([]byte, error)` / `FromNative(data, source) (*Session, error)`
- [x] **1.6.4** Run `make lint && make test` — full MVP gate: zero lint issues, all tests green

---

## Phase 2 — Team Sharing (Target: 2 weeks after MVP)

### Milestone 2.1 — Git Sync

- [x] Sync sessions to `aisync/sessions` Git branch
- [x] `aisync push` / `aisync pull` / `aisync sync` commands
- [x] Conflict resolution: append-only strategy
- [x] Index file (`index.json`) for fast remote lookup

### Milestone 2.2 — Cursor Provider

- [x] Research Cursor `state.vscdb` format (SQLite, `cursorDiskKV` table)
- [x] Implement Cursor provider (export only, `CanImport() = false`)
- [x] `CONTEXT.md` generation as fallback restore mechanism

### Milestone 2.3 — Cross-Provider Restore

- [x] Claude Code → OpenCode conversion
- [x] OpenCode → Claude Code conversion
- [x] Any provider → CONTEXT.md fallback

### Milestone 2.4 — Plugin System for Secrets

- [x] Define plugin interface (Go compiled)
- [x] Support external scripts (`stdin` → `stdout` pipeline)
- [x] Custom regex patterns via config
- [x] `aisync secrets add-pattern <name> <regex>`
- [x] Evaluate Go native plugin (`plugin.Open`) support
- [x] Evaluate Hashicorp go-plugin (gRPC) support

---

## Phase 3 — PR Integration (Target: 2 weeks)

### Milestone 3.1 — Platform Domain + GitHub Implementation

- [x] Define `Platform` interface in domain (GetPRForBranch, GetPR, ListPRsForBranch, AddComment, UpdateComment, ListComments)
- [x] Define `PullRequest` and `PRComment` domain types with JSON tags
- [x] Add `PlatformName` typed enum (github, gitlab, bitbucket)
- [x] Add `ErrPRNotFound` and `ErrPlatformNotDetected` sentinel errors
- [x] Implement GitHub platform client using `gh` CLI
- [x] 5 tests for GitHub client (name, toDomain, parseJSON, jsonEscape)

### Milestone 3.2 — Platform Detection + Factory Wiring

- [x] Implement `DetectFromRemoteURL()` — parses SSH/HTTPS/SSH-protocol URLs
- [x] Case-insensitive host matching, port stripping, self-hosted suffix support
- [x] 22 table-driven tests (15 detection + 7 extractHost)
- [x] Add `PlatformFunc` to Factory, wire GitHub detection via git remote URL

### Milestone 3.3 — PR ↔ Session Linking

- [x] Add `Store.GetByLink(linkType, ref)` to domain interface
- [x] Implement `GetByLink` in SQLite storage (JOIN session_links + sessions)
- [x] Add `git.Client.RemoteURL(name)` method
- [x] Implement `aisync link` command (--session, --pr, --commit, --auto flags)
- [x] 6 tests for link command

### Milestone 3.4 — PR-Aware Restore & List

- [x] Add `--pr <number>` flag to `aisync restore` — looks up session via `store.GetByLink()`
- [x] Add `--pr <number>` flag to `aisync list` — filters sessions linked to a PR
- [x] 2 tests for restore --pr (success + not found)
- [x] 5 tests for list --pr (PR list, PR not found, PR quiet, basic list, flags)

### Milestone 3.5 — PR Comments

- [x] Implement `aisync comment` command — posts/updates session summary as PR comment
- [x] Idempotent via `<!-- aisync -->` HTML marker detection
- [x] Auto-detect PR from branch or explicit `--pr` flag
- [x] Markdown summary with session ID, provider, branch, summary, token usage, file changes
- [x] 7 tests (flags, new comment, update existing, session flag, no PR, body builder, minimal body)

### Milestone 3.6 — Session Statistics

- [x] Implement `aisync stats` command — token totals, session counts, most-touched files
- [x] Overall stats: sessions, messages, tokens
- [x] Per-provider breakdown
- [x] Per-branch breakdown (sorted by token usage)
- [x] Top 10 most-touched files across sessions
- [x] `--branch`, `--provider`, `--all` filter flags
- [x] 9 tests (flags, no sessions, with sessions, formatTokens, truncate)

---

## Phase 3.5 — Architecture Evolution: Server + Services (COMPLETE)

> **Goal:** Transform aisync from a CLI-only tool into a **server that exposes services**, enabling multiple clients (CLI, Web UI, TUI, MCP Server) to share the same application logic. Follows Hexagonal / Ports & Adapters architecture.
>
> **See [architecture/](./architecture/)** for the full architectural design.

### Milestone 3.5.A — Extract Service Layer (COMPLETE)

**Goal:** Move orchestration logic from CLI commands into proper application services. CLI commands become thin adapters.

- [x] **3.5.A.1** Create `internal/service/` package with `SessionService` struct
- [x] **3.5.A.2** Extract `Capture()` method — wraps `internal/capture/service.go`, adds owner identity resolution
- [x] **3.5.A.3** Extract `Restore()` method — wraps `internal/restore/service.go`
- [x] **3.5.A.4** Extract `Export()`, `Import()`, `List()`, `Get()`, `Link()`, `Comment()`, `Stats()`, `Delete()` methods (10 total)
- [x] **3.5.A.5** Create `SyncService` — wraps `internal/gitsync/service.go` with `Push()`, `Pull()`, `Sync()`, `ReadIndex()`
- [x] **3.5.A.6** Update all 13 CLI commands in `pkg/cmd/*` to call services instead of infrastructure directly
- [x] **3.5.A.7** Update Factory to expose `SessionServiceFunc()` and `SyncServiceFunc()` via lazy init
- [x] **3.5.A.8** Old orchestrators (`capture/`, `restore/`, `gitsync/`) now internal implementation details of services
- [x] **3.5.A.9** All 35+ test packages pass with zero regressions

### Milestone 3.5.B — HTTP/REST API Server (COMPLETE)

**Goal:** Add an API server as a second driving adapter. Exposes the same services over HTTP.

- [x] **3.5.B.1** Create `internal/api/server.go` — stdlib `net/http` router, graceful shutdown, composition root
- [x] **3.5.B.2** Create `internal/api/handlers.go` — session CRUD handlers calling `SessionService` (capture, restore, get, list, delete, export, import, link, comment, stats)
- [x] **3.5.B.3** Create `internal/api/sync_handlers.go` — push/pull/sync/index handlers calling `SyncService`
- [x] **3.5.B.4** Request/response types inline in handlers (DTOs decoupled from domain)
- [x] **3.5.B.5** Create `pkg/cmd/servecmd/serve.go` — `aisync serve` command with `--port` and `--host` flags
- [x] **3.5.B.6** 15 API endpoints registered in `internal/api/routes.go`
- [x] **3.5.B.7** Middleware: JSON content-type, error mapping (404/400/422), request logging
- [x] **3.5.B.8** 14 integration tests in `internal/api/server_test.go`
- [x] **3.5.B.9** All tests pass

### Milestone 3.5.C — Client SDK (COMPLETE)

**Goal:** Public Go HTTP client for interacting with the API server.

- [x] **3.5.C.1** Create `client/client.go` — `Client` struct, `New(baseURL)`, HTTP helpers, `APIError` type
- [x] **3.5.C.2** Create `client/sessions.go` — 12 session methods + client-side types (Session, Summary, decoupled from internal/)
- [x] **3.5.C.3** Create `client/sync.go` — sync methods + types
- [x] **3.5.C.4** 12 integration tests in `client/client_test.go`

### Milestone 3.5.D — MCP Server Integration (COMPLETE)

**Goal:** Expose aisync capabilities as MCP tools callable from within Claude Code or OpenCode.

- [x] **3.5.D.1** Create `internal/mcp/server.go` — MCP server using `mark3labs/mcp-go` v0.44.1
- [x] **3.5.D.2** Define 10 session tools: `aisync_capture`, `aisync_restore`, `aisync_get`, `aisync_list`, `aisync_delete`, `aisync_export`, `aisync_import`, `aisync_link`, `aisync_comment`, `aisync_stats`
- [x] **3.5.D.3** Define 4 sync tools: `aisync_push`, `aisync_pull`, `aisync_sync`, `aisync_index`
- [x] **3.5.D.4** Create `pkg/cmd/mcpcmd/mcp.go` — `aisync mcp serve` command (stdio transport)
- [x] **3.5.D.5** 10 tests in `internal/mcp/server_test.go`
- [x] **3.5.D.6** Documentation: how to configure Claude Code / OpenCode to use aisync MCP server

### Milestone 3.5.E — User Identity Layer (COMPLETE)

**Goal:** Add user ownership to sessions, auto-detected from git config.

- [x] **3.5.E.1** Add `UserName()` and `UserEmail()` methods to `git/client.go`
- [x] **3.5.E.2** Create `User` domain type in `internal/session/session.go` (ID, Name, Email, Source, CreatedAt)
- [x] **3.5.E.3** Add `OwnerID` field to `Session` and `Summary` structs
- [x] **3.5.E.4** Extend `Store` interface with `SaveUser()`, `GetUser()`, `GetUserByEmail()` (now 12 methods)
- [x] **3.5.E.5** Add `users` table + `owner_id` column migration to SQLite
- [x] **3.5.E.6** Implement all 3 user methods in `sqlite.Store`, update Save/List/GetByLink queries
- [x] **3.5.E.7** Add `resolveOwner()` to `SessionService` — auto-detects git identity, creates/finds user
- [x] **3.5.E.8** Wire into `Capture()` (via `capture.Request.OwnerID`) and `Import()` flows
- [x] **3.5.E.9** Add `OwnerID` to client SDK types
- [x] **3.5.E.10** Update all 11 mockStores with new user methods, all 37 test files pass

---

## Phase 4 — CI Automation (Future)

- [ ] GitHub Action: on CI failure → prepare fix session with original context + CI errors
- [ ] Webhook notification: "Session available to fix PR #42"
- [ ] Slack/n8n integration
- [ ] GitLab / Bitbucket support

---

## Phase 5 — Session Intelligence & Cost Tracking

> **Goal:** Transform aisync from a capture/restore tool into a session intelligence platform. Multiple sessions per branch, file-level blame, per-tool token accounting, and real cost tracking.

### Milestone 5.0 — AI Summarization, Explain & Resume (COMPLETE)

**Goal:** Add AI-powered session intelligence: auto-summarization, session explanation, resume workflow, and session rewind.

- [x] **5.0.1** Create LLM client port + Claude CLI adapter
  - `llm.Client` interface in `internal/llm/client.go` with `Complete(ctx, CompletionRequest) → CompletionResponse`
  - Claude CLI adapter in `internal/llm/claude/claude.go` — calls `claude --print --output-format json`
  - `StructuredSummary` domain type in `internal/session/session.go`
  - 6 tests: mock binary, fallback, context cancellation
- [x] **5.0.2** Integrate auto-summarization into capture service
  - New config: `summarize.enabled` (bool), `summarize.model` (string)
  - `aisync capture --summarize` flag for one-time override
  - `SessionService.Summarize()` method with JSON response parsing
  - Non-blocking: failure logs warning, capture proceeds with provider-native summary
  - Priority: `--message` > AI summary > provider-native summary
- [x] **5.0.3** Implement `aisync explain` command (full vertical slice)
  - `SessionService.Explain()` with short/detailed modes
  - CLI: `aisync explain <id>` with `--short`, `--json`, `--model` flags
  - API: `POST /api/v1/sessions/explain`
  - MCP: `aisync_explain` tool
  - Client SDK: `client.Explain()`
- [x] **5.0.4** Implement `aisync resume <branch>` command
  - `git.Client.Checkout()` method
  - CLI: `aisync resume <branch>` with `--session`, `--provider`, `--as-context`
  - Pure composition: no new service method
- [x] **5.0.5** Implement `aisync rewind <session-id>` command (full vertical slice)
  - `SessionService.Rewind()` — creates fork with truncated messages
  - CLI: `aisync rewind <id> --message N` with `--json`
  - API: `POST /api/v1/sessions/rewind`
  - MCP: `aisync_rewind` tool
  - Client SDK: `client.Rewind()`
  - Original session never modified — new session with `ParentID = original.ID`
- [ ] **5.0.6** Add `--similar` flag to `aisync list`
  - Compare AI summaries + file change overlap across sessions on same branch
  - Group similar sessions (same intent / retries) vs standalone sessions
  - Requires AI summaries to be available (warn if not)

### Milestone 5.1 — Multi-Session per Branch

**Goal:** Remove the 1:1 branch-to-session constraint. Version sessions, detect forks and off-topic conversations.

- [ ] **5.1.1** Change data model: remove deduplication in capture service (stop overwriting same-branch sessions)
- [ ] **5.1.2** Migrate existing sessions: existing single sessions become "v1" entries
- [ ] **5.1.3** Update `aisync list` to show all sessions per branch (not just the latest)
- [ ] **5.1.4** Add `session_relationships` table: parent/child, fork-of, off-topic flags
- [ ] **5.1.5** Fork detection: hash first N user messages, compare across sessions on same branch
- [ ] **5.1.6** `aisync list --tree` to show fork visualization (include rewind forks from 5.0.5)
- [ ] **5.1.7** Off-topic detection: compare file changes + topic overlap between sessions on same branch
- [ ] **5.1.8** Update `aisync restore` to let user pick from multiple sessions per branch

### Milestone 5.2 — AI-Blame

**Goal:** Find which AI session modified a file, and restore it.

- [x] **5.2.1** Implement `aisync blame <file>` — reverse lookup from file_changes table
- [x] **5.2.2** Show session info, summary, and suggested actions (restore/show)
- [x] **5.2.3** `aisync blame <file> --restore` — shortcut to restore the session that last touched the file
- [x] **5.2.4** `aisync blame <file> --all` — show all sessions that ever touched this file
- [ ] **5.2.5** Integrate with `aisync show` — link from session detail to blame view

### Milestone 5.3 — Tool/MCP Token Accounting

**Goal:** Per-tool token breakdown to understand where tokens are spent.

- [ ] **5.3.1** Add `tool_tokens` field to ToolCall struct (estimated or actual)
- [ ] **5.3.2** Claude Code provider: estimate per-tool tokens from usage deltas between messages
- [ ] **5.3.3** OpenCode provider: extract per-tool tokens if available in part metadata
- [ ] **5.3.4** `aisync show <id> --tool-usage` — per-tool breakdown table
- [ ] **5.3.5** `aisync stats --tools` — aggregated tool usage across sessions
- [ ] **5.3.6** Warn when `compact` mode is used that tool accounting requires `full` mode

### Milestone 5.4 — Cost Tracking

**Goal:** Real monetary cost per session, per branch, per feature.

- [ ] **5.4.1** Create `internal/pricing/` package with model pricing table (configurable JSON)
- [ ] **5.4.2** Ship default pricing for Claude (Opus, Sonnet, Haiku), GPT-4o, Gemini
- [ ] **5.4.3** `aisync show <id> --cost` — cost breakdown for a session
- [ ] **5.4.4** `aisync stats --cost` — cost by branch, by provider, by model
- [ ] **5.4.5** `aisync stats --cost --branch <name>` — total feature cost (excluding off-topic)
- [ ] **5.4.6** Use OpenCode's native cost data when available
- [ ] **5.4.7** `aisync config set pricing.<model> <input_price> <output_price>` — custom pricing overrides

---

## Phase 6 — Replay, Forecasting & Web UI

> **Goal:** Session replay for model comparison, cost forecasting, and a visual web dashboard.

### Milestone 6.1 — Session Replay

**Goal:** Replay user messages from a captured session through a different model.

- [ ] **6.1.1** Design replay architecture: how to call target model (embedded client vs provider CLI vs MCP)
- [ ] **6.1.2** `aisync replay <session-id> --model <model>` — replay with different model
- [ ] **6.1.3** `aisync replay <session-id> --provider <provider>` — replay with different provider
- [ ] **6.1.4** Store replay sessions with `replay_of` link to original
- [ ] **6.1.5** `aisync compare <id1> <id2>` — side-by-side comparison (tokens, cost, files, output diff)

### Milestone 6.2 — Cost Forecasting

**Goal:** Predict future AI costs based on historical patterns.

- [ ] **6.2.1** Collect historical cost data per branch type (feature, fix, refactor)
- [ ] **6.2.2** `aisync stats --forecast` — average cost per feature, per model, confidence interval
- [ ] **6.2.3** Model recommendation: "For this type of task, Sonnet saves 60% vs Opus"

### Milestone 6.3 — Web UI

**Goal:** Local web dashboard for visual session browsing and cost analysis.

- [x] **6.3.1** Decide tech stack: **Go templates + HTMX + D3.js** (decision D25)
- [ ] **6.3.2** `aisync web` — launch local HTTP server (Go templates embedded via `go:embed`)
- [ ] **6.3.3** Session list page: filterable table with search bar (keyword, branch, user, date)
- [ ] **6.3.4** Session detail page: messages, tool calls, file changes, cost
- [ ] **6.3.5** Branch tree view: all sessions per branch with fork visualization (D3.js)
- [ ] **6.3.6** Cost dashboard: per-branch, per-model, trends over time
- [ ] **6.3.7** Click-to-restore: generate restore command from the UI

---

## Decisions Log (Phase 3.5)

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| D30 | Architecture style | **Hexagonal / Ports & Adapters** | Service layer enables multiple driving adapters (CLI, API, MCP). Incremental migration from flat CLI. |
| D31 | Service extraction strategy | **Incremental, not big-bang** | Wrap existing capture/restore/gitsync services, then gradually inline. Old packages deprecated, not deleted. |
| D32 | API framework | **stdlib `net/http` only** | No external router. Minimal dependencies, idiomatic Go. |
| D33 | API server lifecycle | **`aisync serve` command** | Server runs as a separate process. CLI commands still work standalone (no server required). |
| D34 | LLM client port | **Interface `LLMClient`** with Claude CLI adapter | Allows future backends (OpenAI, Ollama) without changing AnalysisService. Deferred to Phase 5. |
| D35 | MCP Server implementation | **Embedded in aisync binary** | `aisync mcp serve` starts MCP server via stdio. Same binary, different mode. Uses `mark3labs/mcp-go`. |
| D36 | Service layer testing | **Mock-based unit tests** | Services accept interfaces (Store, Provider, LLMClient), easily mockable. |
| D37 | User Identity source | **Auto-detect from `git config`** | Best-effort — silently skips if unavailable. `user.email` as unique key. |
| D38 | Client SDK location | **Root-level `client/` package** | Public Go SDK alongside `git/`. Imports `internal/session/` for shared types (pragmatic choice). |
| D39 | MCP SDK | **`mark3labs/mcp-go` v0.44.1** | Only supported Go MCP SDK. Stdio transport. `mcp.WithNumber` (no WithInteger). |

---

## Decisions Log (Phase 5-6)

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| D21 | Multi-session migration | Keep existing as "v1", new captures create new entries | Non-breaking, backward compatible |
| D22 | Fork detection method | Hash first 3 user messages | Balance between accuracy and false positives |
| D23 | Cost data source | Built-in pricing table + OpenCode native data + user overrides | Flexible, works offline |
| D24 | Replay execution | TBD — needs architecture design | Options: embedded LLM client, provider CLI wrapper, MCP bridge |
| D25 | Web UI stack | **Go templates + HTMX** (D3.js for graphs) | Single binary (`go:embed`), no JS build step, HTMX consumes existing HTTP API. D3.js/Mermaid inlined for fork graph visualization. |
| D26 | Auto-summarization backend | **Claude CLI** (`claude` command in PATH) | Already required by providers, no new dependency. Configurable for future backends. |
| D27 | Summarization timing | **At capture time, non-blocking** | Simpler than async queue. Failure does not block capture. Can re-summarize later. |
| D28 | Rewind scope | **Session context only** (messages 1..N), not code state | Code state restore is complex (needs git stash integration). Start with context-only rewind. |
| D29 | Resume implementation | **Convenience command** wrapping `git checkout` + `aisync restore` | Not a new feature, just UX improvement. Thin wrapper. |

---

## Technical Debt & Future Improvements

### Completed (Technical Debt Cleanup)

- [x] Extracted shared test helpers to `internal/testutil/` (InitTestRepo, MustOpenStore, NewSession)
- [x] Added 8 capture CLI tests (`pkg/cmd/capture/capture_test.go`)
- [x] Added 9 restore CLI tests (`pkg/cmd/restore/restore_test.go`)
- [x] Session deduplication in capture service — same project+branch reuses existing session ID
- [x] Deduplicated CONTEXT.md generation — restore service now uses `converter.ToContextMD()`
- [x] Fixed hardcoded version — injected via `-ldflags` from `main.go`, `aisync version` subcommand + `--version` flag
- [x] Added shell completions — `aisync completion [bash|zsh|fish|powershell]` subcommand
- [x] Added `make test-cover` target with coverage profile
- [x] Factory now closes SQLite store on exit (`CloseFunc`, deferred in `main.go`)
- [x] Fixed `gitsync/service.go` — cleaner error handling for PullSyncBranch
- [x] Fixed `git/client.go` — replaced `exec.Command("rm", "-f", ...)` with `os.Remove()`
- [x] Fixed `opencode/opencode.go` — removed dead `runtime.GOOS` branch
- [x] Fixed `converter/converter.go` — removed unused `markerContent` variable in child flattening
- [x] Fixed capture service — `session.StorageMode` now explicitly set from request mode

### TUI — Interactive Terminal UI (COMPLETE)

- [x] Added bubbletea + bubbles + lipgloss dependencies
- [x] Created `internal/tui/` package with Elm architecture (Model → Update → View)
- [x] Dashboard view — branch, provider, session status, hooks, sync, project stats, quick actions
- [x] Session list view — browsable table with keyboard nav (j/k/pgup/pgdn), cursor, toggle all/branch filter
- [x] Session detail view — full session inspection with metadata, tokens, summary, links, file changes, messages, tool calls, scrollable viewport
- [x] Tab navigation between views, esc to go back, ? for contextual help
- [x] `aisync tui` command wired into root
- [x] 15 tests (formatTokenCount, truncateStr, formatTimeAgo, wrapText, changeIcon, renderField, buildLines, view states, navigation, scrolling)

### Provider/Converter Refactoring (COMPLETE — 2026-02-25)

- [x] Exported `claude.MarshalJSONL()` and `claude.UnmarshalJSONL()` as public pure functions
- [x] Exported `opencode.MarshalJSON()` and `opencode.UnmarshalJSON()` with `ExportBundle` structs
- [x] Gutted `converter.go` from 870 → 165 lines — delegates to provider marshal/unmarshal
- [x] Eliminated all 11 duplicate struct pairs and 6 duplicate functions between provider/converter
- [x] Defined `SessionConverter` interface in restore service
- [x] Removed double-conversion round-trip in `restoreWithConversion()`
- [x] All 31 test packages pass with zero regressions

### Remaining

- [ ] Session expiration / cleanup policy (ties into multi-session + garbage collection)
- [ ] Compression for large sessions (zstd)
- [ ] `aisync diff <session-1> <session-2>` to compare sessions (ties into Phase 6 Replay/Compare)
- [ ] Telemetry opt-in (anonymous usage stats)
- [ ] Fix `Import()` for Claude Code and OpenCode providers (currently returns `ErrImportNotSupported`)
- [ ] Fix `aisync show <commit-sha>` to parse AI-Session trailer from commit message and resolve to session
- [ ] Add `forked_at_message` field to session relationships (needed for rewind → fork linking in 5.0.5 + 5.1.4)
