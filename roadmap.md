# aisync — Roadmap

> Last updated: 2026-03-25
> Status: Phase 1-3.5, 5.0-5.5, 6.2-6.3, 7.1-7.4, 8.1-8.9, 9.1-9.3, 10.1-10.6c, 10.8 COMPLETE — LiteLLM pricing integration DONE. Features 6.1, 7.3, tech debt remaining.
> Phase 10.8 completed: LiteLLM pricing database adapter — 2500+ model pricing from GitHub, FallbackCatalog chain, `aisync update-prices` CLI command.
> Phase 9.1 completed: Authentication & Multi-Auth (JWT + API Keys) with auth BC, middleware, CLI, client SDK.
> Phase 8.1 completed: Universal Ingest endpoint with MCP tool, full vertical slice.
> Phase 6.3 completed: Web Dashboard with dynamic columns, error tracking, project selector on all pages.
> Session Analysis BC completed: LLM-powered analysis with OpenCode streaming adapter.
> Dashboard Customization completed: configurable columns, sort, page size, default filters via `aisync config`.

---

## Decisions Log

Decisions taken during the design phase, before any code was written.

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| D1 | Language & stack | **Go** (Cobra CLI, SQLite via modernc.org/sqlite) | Single binary, fast, great CLI ecosystem |
| D2 | Providers (MVP) | **Claude Code + OpenCode** | Both support export AND import. Cursor is read-only (Phase 2) |
| D3 | Session granularity | **Multiple sessions per branch** | Each capture creates a new session. Commands default to the latest. Phase 5.1 removed the 1:1 constraint. |
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
- [x] **5.0.6** `--similar <session-id>` flag: Jaccard similarity on file changes, sorted by overlap
  - Compare AI summaries + file change overlap across sessions on same branch
  - Group similar sessions (same intent / retries) vs standalone sessions
  - Requires AI summaries to be available (warn if not)

### Milestone 5.1 — Multi-Session per Branch (COMPLETE)

**Goal:** Remove the 1:1 branch-to-session constraint. Each capture creates a new session; commands default to the latest.

- [x] **5.1.1** Remove deduplication in capture service — each capture creates a new session with its own ID
- [x] **5.1.2** Rename `GetByBranch` → `GetLatestByBranch` — clarifies semantics (returns most recent session)
- [x] **5.1.3** Add `CountByBranch` to Store interface — lightweight count for UI messages
- [x] **5.1.4** Update `aisync status` to show session count when multiple exist
- [x] **5.1.5** Update TUI dashboard to show branch session count
- [x] **5.1.6** Update `post-checkout.sh` hook template — plural-aware messaging
- [x] **5.1.7** Add tests: CountByBranch, GetLatestByBranch (multi-session), multi-session capture test
- [x] **5.1.8** No schema migration needed — DB already supports multi-session (no UNIQUE constraint)

**Future (deferred to later milestones):**
- [x] **5.1.9** ~~Table `session_relationships`~~ — SKIPPED: computed at runtime from `ParentID` instead of persisting a separate table. No new Store methods needed.
- [x] **5.1.10** Fork detection: sessions with `ParentID` are marked as forks (`IsFork=true`). Detected during tree building via `buildTree()`.
- [x] **5.1.11** `aisync list --tree` — shows sessions as indented tree with fork labels. `ParentID` added to `Summary` type, `ListTree()` service method, `buildTree()` with recursive pointer-based algorithm, 4 tests (noParents, parentChild with grandchild, orphanParent, empty).
- [x] **5.1.12** Off-topic detection: `aisync list --off-topic` compares file overlap across sessions on same branch. `DetectOffTopic()` service method scores each session (0.0-1.0 overlap), flags sessions below threshold (default 20%). Full vertical slice: CLI `--off-topic`, API `GET /api/v1/sessions/off-topic?branch=`, MCP `aisync_off_topic` (24 tools), client SDK `DetectOffTopic()`. 7 tests.
- [x] **5.1.13** `aisync restore --pick` — interactive session picker when multiple sessions exist on the current branch, lists sessions with ID/provider/tokens/summary and prompts for user choice via stdin

### Milestone 5.2 — AI-Blame

**Goal:** Find which AI session modified a file, and restore it.

- [x] **5.2.1** Implement `aisync blame <file>` — reverse lookup from file_changes table
- [x] **5.2.2** Show session info, summary, and suggested actions (restore/show)
- [x] **5.2.3** `aisync blame <file> --restore` — shortcut to restore the session that last touched the file
- [x] **5.2.4** `aisync blame <file> --all` — show all sessions that ever touched this file
- [x] **5.2.5** Integrate with `aisync show --blame` — shows other sessions that touched the same files, per-file breakdown with session ID, provider, date, and summary

### Milestone 5.3 — Tool/MCP Token Accounting

**Goal:** Per-tool token breakdown to understand where tokens are spent.

- [x] **5.3.1** Add `tool_tokens` field to ToolCall struct (estimated or actual)
- [x] **5.3.2** Claude Code provider: estimate per-tool tokens from usage deltas between messages
- [x] **5.3.3** OpenCode provider: extract per-tool tokens if available in part metadata
- [x] **5.3.4** `aisync show <id> --tool-usage` — per-tool breakdown table
- [x] **5.3.5** `aisync stats --tools` — aggregated tool usage across sessions
- [x] **5.3.6** Warn when `compact` mode is used that tool accounting requires `full` mode

### Milestone 5.4 — Cost Tracking

**Goal:** Real monetary cost per session, per branch, per feature.

- [x] **5.4.1** Create `internal/pricing/` package with model pricing table (configurable JSON)
- [x] **5.4.2** Ship default pricing for Claude (Opus, Sonnet, Haiku), GPT-4o, Gemini
- [x] **5.4.3** `aisync show <id> --cost` — cost breakdown for a session
- [x] **5.4.4** `aisync stats --cost` — cost by branch, by provider, by model
- [x] **5.4.5** `aisync stats --cost --branch <name>` — total feature cost (excluding off-topic)
- [x] **5.4.6** Use OpenCode's native cost data when available
- [x] **5.4.7** `aisync config set pricing.<model> <input_price> <output_price>` — custom pricing overrides

### Milestone 5.5 — Session Efficiency Analysis (COMPLETE)

**Goal:** LLM-powered efficiency scoring to identify wasted tokens, anti-patterns, and improvement opportunities.

- [x] **5.5.1** Define `EfficiencyReport` domain type (score 0-100, summary, strengths, issues, suggestions, patterns)
- [x] **5.5.2** Implement `SessionService.AnalyzeEfficiency()` — builds data-rich prompt with token breakdown, tool stats, message patterns, conversation flow, cost estimate; clamps score to [0,100]
- [x] **5.5.3** CLI: `aisync efficiency <id>` with `--json` and `--model` flags
- [x] **5.5.4** API: `POST /api/v1/sessions/efficiency`
- [x] **5.5.5** MCP: `aisync_efficiency` tool
- [x] **5.5.6** Client SDK: `client.AnalyzeEfficiency(req)`
- [x] **5.5.7** 7 tests (success, noLLM, notFound, noMessages, clampsScore, buildPrompt, safePercent)

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

- [x] **6.2.1** Collect historical cost data per branch type (feature, fix, refactor)
- [x] **6.2.2** `aisync stats --forecast` — average cost per feature, per model, confidence interval
- [x] **6.2.3** Model recommendation: "For this type of task, Sonnet saves 60% vs Opus"

### Milestone 6.3 — Web UI

**Goal:** Local web dashboard for visual session browsing and cost analysis.

- [x] **6.3.1** Decide tech stack: **Go templates + HTMX + D3.js** (decision D25)
- [x] **6.3.2** `aisync web` — launch local HTTP server (Go templates embedded via `go:embed`)
- [x] **6.3.3** Dashboard page: KPIs, recent sessions, top branches, cost forecast
- [x] **6.3.4** Session list page: filterable table with search bar (keyword, branch, provider) + HTMX live filtering + pagination
- [x] **6.3.5** Session detail page: messages, tool calls (collapsible), file changes (color-coded), cost breakdown, conversation view with thinking blocks
- [x] **6.3.6** Branch explorer: per-branch cards with session tree timeline, fork visualization (CSS-based timeline with recursive template), 3 tests (empty, withData, withForks)
- [x] **6.3.7** Cost dashboard: KPIs (total/avg/projected), cost-over-time bar chart, model breakdown with share bars + recommendations, per-branch cost table, 2 tests (empty, withData)
- [x] **6.3.8** Click-to-restore: restore panel on session detail with provider selector, HTMX dynamic command generation, copy-to-clipboard button, 7 tests (panel render, default/provider/context/notFound partials, buildRestoreCmd unit tests)

---

## Phase 7 — Server-First Architecture

> **Goal:** Migrate from CLI-with-embedded-DB to a client-server architecture. The CLI becomes a thin HTTP client when a server is available, with SQLite-direct as fallback for offline/solo use.
>
> **Key principle:** Everything works locally without a server (backward compatible). The server adds sharing, caching, and ingest capabilities.
>
> **Why first:** The ingest endpoints (Phase 8) and all future features need a running service. Building the dual-mode CLI first means every subsequent feature automatically works in both modes.

### Milestone 7.1 — CLI Auto-Detection (Dual Mode)

**Goal:** The CLI transparently switches between server mode and standalone mode.

- [x] **7.1.1** Extract `SessionServicer` interface (Use Case Port)
  - 25-method interface in `internal/service/iface.go`
  - Compile-time check: `var _ SessionServicer = (*SessionService)(nil)`
  - Updated 9 source files + 10 test files to use interface instead of concrete type
  - API server, Web server, MCP server all accept `SessionServicer`

- [x] **7.1.2** Add `server.url` config key (e.g., `http://localhost:8371`)
  - `serverConf` struct added to `configData` with `URL` field
  - Get/Set cases with URL validation (must start with `http://` or `https://`)
  - Merge logic in `loadFrom()`, `GetServerURL()` accessor
  - Env override: `AISYNC_SERVER_URL` takes precedence over config file

- [x] **7.1.3** Add `IsAvailable()` to client SDK
  - `GET /api/v1/health` with 500ms timeout
  - Returns `bool` — used by Factory for mode detection

- [x] **7.1.4** Create `internal/service/remote/` adapter package
  - `remote.SessionService` implements `service.SessionServicer`
  - Wraps `client.Client` — maps each method to HTTP calls
  - Type conversion: `client.*` → `session.*` / `service.*` with explicit field mapping
  - JSON round-trip for complex analytics types (Stats, Blame, Forecast, etc.)
  - Compile-time check: `var _ service.SessionServicer = (*SessionService)(nil)`

- [x] **7.1.5** Update Factory for dual-mode switching
  - `SessionServiceFunc` probes `server.url` on first call
  - If server responds → returns `remote.SessionService`, logs `[aisync] connected to server at ...`
  - If server unavailable → falls back to local `SessionService`, logs warning
  - `sync.Once` ensures detection happens only once

- [x] **7.1.6** Tests: factory dual-mode (IsAvailable probe, remote List/Stats, auth token, dead server fallback)

### Milestone 7.2 — Database Configuration

**Goal:** Externalize database configuration so the server can use shared storage.

- [x] **7.2.1** Add `database.path` config key
  - `databaseConf{Path}` struct added to `configData`
  - Get/Set cases, merge logic, `GetDatabasePath()` accessor
  - Env override: `AISYNC_DATABASE_PATH` takes precedence
  - Factory `StoreFunc` reads config before defaulting to `~/.aisync/sessions.db`

- [x] **7.2.2** Create `user_preferences` table (migration004)
  - Schema: `user_id TEXT UNIQUE`, `preferences TEXT (JSON)`, `updated_at TEXT`
  - `user_id = ''` → global defaults (anonymous/shared, no auth needed)
  - `user_id = 'xxx'` → per-user preferences (ready for Phase 9 auth)
  - Full JSON structure matching `DashboardPreferences` — not flat key/value

- [x] **7.2.3** Add `GetPreferences()` / `SavePreferences()` to `storage.Store`
  - Returns `*session.UserPreferences` with `DashboardPreferences` JSON struct
  - Returns nil (not error) when no preferences stored — callers use system defaults
  - Upsert semantics: save creates or replaces
  - All 14 mock stores updated with stubs

- [x] **7.2.4** Wire dashboard to read from user_preferences
  - Priority chain: DB preferences → config file → system defaults
  - `getDashboardPrefs()` reads from Store (global defaults for now)
  - `getDashboardPageSize()`, `getDashboardSortBy()`, `getDashboardSortOrder()`, `buildColumnDefs()` all check DB first
  - Web server receives optional `Store` via `Config.Store`

- [x] **7.2.5** `database.driver` config key (validates "sqlite" only, prepares for future drivers)
  - `sqlite` (default) or `postgres`
  - PostgreSQL adapter implements same `Store` interface
  - Deferred until real multi-user deployment need

### Milestone 7.3 — `aisync serve` as Unified Daemon

**Goal:** One command, one process — API + web dashboard + ingest.

- [x] **7.3.1** Merge `aisync web` into `aisync serve`
  - Unified server: API (`/api/v1/*`) + Web (`/*`) on a single port (default 8371)
  - `api.Server.RegisterRoutes(mux)` and `web.Server.RegisterRoutes(mux)` exported
  - Composite Handler pattern: each package registers routes on shared mux (SRP)
  - Single logging middleware with `[aisync]` prefix
  - Single graceful shutdown (SIGINT/SIGTERM, 10s drain)
  - `aisync serve --web-only` flag disables API endpoints
  - `aisync web` is now a thin alias that delegates to `aisync serve --web-only`

- [x] **7.3.2** `--daemon` flag: re-exec with PID file (`~/.aisync/aisync.pid`), `--stop` to send SIGTERM, log redirect to `~/.aisync/aisync.log`

- [x] **7.3.3** systemd + launchd service files in `contrib/`

### Milestone 7.4 — Stats Cache

**Goal:** Pre-computed aggregates for fast dashboard loading.

- [x] **7.4.1** Create `stats_cache` table (migration006)
  - Schema: `key TEXT PRIMARY KEY`, `value BLOB`, `updated_at TEXT`
  - `GetCache(key, maxAge)` — returns nil on miss or expired entries
  - `SetCache(key, value)` — upsert with current timestamp
  - `InvalidateCache(prefix)` — delete by prefix, or all if empty
  - All 14 mock stores updated with stubs

- [x] **7.4.2** Implement lazy cache with auto-invalidation
  - `cachedStats()` and `cachedForecast()` in web handlers — check cache, compute on miss, store result
  - Stats TTL: 30 seconds. Forecast TTL: 5 minutes.
  - Cache keys: `stats:{projectPath}:{branch}`, `forecast:{projectPath}:{period}`
  - Auto-invalidation: `Save()`, `Delete()`, `DeleteOlderThan()` in SQLite store invalidate `stats:*` and `forecast:*`
  - **Result: 30x speedup** (2.1s → 0.07s on cached dashboard loads)

- [x] **7.4.3** Add global error KPIs to dashboard
  - New fields: `TotalErrors`, `SessionsWithErrors`, `TotalToolCalls`
  - Aggregated from `session.Summary.ErrorCount` and `ToolCallCount`
  - Dashboard template: "Tool Calls" + "Errors" KPI cards
  - Error card highlighted with `kpi-error` class (red border) when errors > 0

- [x] **7.4.4** Tests: 6 cache tests
  - `TestCache_MissAndPopulate`, `TestCache_TTLExpiry`, `TestCache_InvalidateByPrefix`
  - `TestCache_InvalidateAll`, `TestCache_InvalidatedOnSave`, `TestCache_Upsert`

---

## Phase 8 — Session Ingest & Ollama Support

> **Goal:** Enable non-git, non-IDE AI tools (voice assistants, local LLMs, custom agents) to push sessions into aisync. Two ingest paths: universal format + Ollama native format.
>
> **Prerequisite:** Phase 7 (server-first) — ingest endpoints live in `aisync serve`.

### Milestone 8.1 — Universal Ingest Endpoint ✅

**Goal:** A simple POST endpoint that accepts any session in aisync's lightweight format.

- [x] **8.1.1** Add `ProviderParlay` (`"parlay"`) and `ProviderOllama` (`"ollama"`) to `session.ProviderName`
  - Add to `allProviders` slice — validation works automatically
  - Add short names: `"PA"` (parlay), `"OL"` (ollama) in web dashboard
  - No file-based provider adapter needed (push-only, no Detect/Export)

- [x] **8.1.2** Create `POST /api/v1/ingest` endpoint
  - Accepts a **plain JSON session** (not base64-encoded like `/import`)
  - Minimal required fields: `provider`, `messages[]`
  - Optional fields: `agent`, `branch`, `summary`, `model`, `project_path`, `tokens`, `session_id`
  - Auto-generates `session_id` if not provided (UUID)
  - Auto-sets `created_at` to now if not provided
  - Runs secret scanner before storage
  - Returns `{ "session_id": "...", "provider": "parlay" }`

  Example request:
  ```json
  {
    "provider": "parlay",
    "agent": "jarvis",
    "project_path": "/Users/guardix/dev/Jarvis",
    "summary": "Question sur l'heure",
    "messages": [
      {"role": "user", "content": "Quelle heure il est ?"},
      {"role": "assistant", "content": "Il est minuit quarante-deux.", "model": "qwen3-coder:30b",
       "tool_calls": [
         {"name": "bash", "input": "date +%H:%M", "output": "00:42", "state": "completed"}
       ],
       "input_tokens": 450, "output_tokens": 35}
    ]
  }
  ```

- [x] **8.1.3** Add `Ingest()` method to `SessionService`
  - Converts the lightweight ingest format to a full `session.Session`
  - Computes `TokenUsage`, `MessageCount`, `ToolCallCount`, `ErrorCount`
  - Stores in SQLite via `Store.Save()`
  - No provider detection, no git hooks, no file-system reads

- [x] **8.1.4** Add `Ingest()` to client SDK (`client/sessions.go`)
  - `client.Ingest(IngestRequest) (*IngestResult, error)`

- [x] **8.1.5** Add `aisync_ingest` MCP tool
  - Tool name: `aisync_ingest` with 9 parameters (provider, messages_json, agent, project_path, branch, summary, session_id, remote_url, delegated_from_session_id)
  - Messages passed as JSON string (MCP doesn't support nested arrays natively)
  - 6 tests: success, with tool calls, missing provider, missing messages, invalid JSON, bad provider

- [x] **8.1.6** Tests: ingest handler (valid, missing fields, secret scanning, duplicate ID)
  - 13 service-level tests + 6 API-level tests + 6 MCP tests = **25 total ingest tests**

### Milestone 8.2 — Ollama Native Ingest ✅

**Goal:** Accept Ollama's native `/api/chat` format directly — zero conversion needed on the client side.

- [x] **8.2.1** Create `POST /api/v1/ingest/ollama` endpoint
  - Accepts the **Ollama chat conversation history** as-is
  - Request wraps an Ollama conversation with minimal metadata:
  ```json
  {
    "project_path": "/Users/guardix/dev/Jarvis",
    "agent": "jarvis",
    "summary": "Weather query",
    "conversation": [
      {"role": "user", "content": "What's the weather?"},
      {"role": "assistant", "content": "", "tool_calls": [
        {"type": "function", "function": {"name": "get_weather", "arguments": {"city": "Paris"}}}
      ]},
      {"role": "tool", "tool_name": "get_weather", "content": "22°C, sunny"},
      {"role": "assistant", "content": "It's 22°C and sunny in Paris."}
    ],
    "model": "qwen3-coder:30b",
    "prompt_eval_count": 450,
    "eval_count": 35,
    "total_duration": 1200000000,
    "eval_duration": 700000000
  }
  ```

- [x] **8.2.2** Create `internal/ingest/ollama/` converter package
  - Maps Ollama format → aisync `IngestRequest` (adapter-layer conversion):
    - `role: "user"/"assistant"/"system"` → `IngestMessage` with matching role
    - `tool_calls[].function.name` + `.arguments` → `IngestToolCall{Name, Input}`
    - `role: "tool"` + `tool_name` + `content` → matched back to parent `ToolCall.Output`
    - `prompt_eval_count` → `InputTokens`, `eval_count` → `OutputTokens` (on first assistant msg)
    - `DurationNsToMs()` helper for nanoseconds → milliseconds conversion
    - `model` → set on each assistant message's `Model` field
  - Sets `provider: "ollama"` automatically
  - Handles orphan tool results (no matching tool call) gracefully
  - Handles multiple tool calls with same name via FIFO matching

- [x] **8.2.3** Add `IngestOllama()` to client SDK
  - `client.IngestOllama(OllamaIngestRequest) (*IngestResult, error)`
  - `OllamaIngestRequest`, `OllamaMessage`, `OllamaToolCall`, `OllamaFunctionCall` types

- [x] **8.2.4** Tests: Ollama format conversion (messages, tool calls, tool results matching, tokens, duration)
  - 11 converter tests + 6 API endpoint tests (17 total)

### Milestone 8.3 — Voice-Friendly Search ✅

**Goal:** Optimize search results for voice consumption (Parlay/Jarvis).

- [x] **8.3.1** Add `GET /api/v1/sessions/search?keyword=...&voice=true` mode
  - Returns compact `voice_results[]` array with `VoiceSummary` type
  - Each result has: `id`, `summary` (plain text, 2 sentences max), `time_ago`, `agent`, `branch`
  - Defaults limit to 5 when `voice=true` (overridable via explicit `limit=N`)
  - `time_ago` computed with `humanTimeAgo()`: "just now", "2 hours ago", "yesterday", "3 days ago", etc.
  - Summary sanitized via `sanitizeForVoice()`: strips `**bold**`, `` `code` ``, ` ```fenced``` `, `_italic_`
  - Truncates to 2 sentences max
  - Propagated through all layers: API handler → service → remote adapter → client SDK

- [x] **8.3.2** Add `Search()` voice mode to client SDK
  - `client.Search(SearchOptions{Voice: true})`
  - `VoiceSummary` type in client package
  - `SearchResult.VoiceResults []VoiceSummary` field (omitempty)

- [x] **8.3.3** Tests: voice search returns short summaries, respects limit
  - 5 service-level tests (voice defaults, voice results, non-voice omits, explicit limit, truncation)
  - 3 API-level tests (voice mode, default limit, non-voice omits)
  - 8 total new tests

### Milestone 8.4 — Delegate Tracking ✅

**Goal:** Track when Parlay delegates a task to OpenCode/Opus and link the sessions.

- [x] **8.4.1** Add `delegate` tool call type recognition in ingest
  - Heuristic scan: tool calls named `delegate`, `ask_subagent`, `run_subagent`, `subagent`, `computer_use`
  - Extracts `session_id` from JSON input/output → creates `delegated_to` link automatically
  - Explicit: `IngestRequest.DelegatedFromSessionID` for direct caller-supplied parent links

- [x] **8.4.2** Add `session.SessionLinkType` enum with 5 types + `Inverse()` helper
  - `delegated_to`, `delegated_from`, `related`, `continuation`, `follow_up`
  - `session_session_links` SQLite table (migration 007) — bidirectional links auto-created by store

- [x] **8.4.3** `SessionIngester` interface: `LinkSessions`, `GetLinkedSessions`, `DeleteSessionLink`
  - `SessionService` + `RemoteSessionService` implementations
  - Client SDK: `client.LinkSessions`, `GetLinkedSessions`, `DeleteSessionLink`

- [x] **8.4.4** API endpoints
  - `POST /api/v1/sessions/session-links` — create link
  - `GET /api/v1/sessions/{id}/session-links` — list links for a session
  - `DELETE /api/v1/sessions/session-links/{linkID}` — remove link

- [x] **8.4.5** Tests
  - 4 service-level tests (explicit delegation, heuristic tool scan, non-delegate tool skipped, self-link skipped)
  - 1 unit test for `extractSessionID`
  - 5 API-level tests (create link, invalid type, same session, get links + inverse, delete)

- [x] **8.4.6** Web dashboard: show delegation links in session detail

### Milestone 8.5 — Project Identity & Grouping ✅

**Goal:** Identify sessions by git remote URL (not just local path), enabling cross-machine session grouping.

- [x] **8.5.1** Add `remote_url` field to `Session` + `Summary` domain types
  - Normalized format: `github.com/org/repo` (no protocol, no `.git`, no `user@`)
  - `NormalizeRemoteURL()` helper handles HTTPS, SSH, git:// formats

- [x] **8.5.2** SQLite migration 008: `remote_url` column + index on `sessions` table
  - Save/List/Search/GetByLink queries updated to include `remote_url`
  - `ProjectPath` also added to `Summary` (was missing)

- [x] **8.5.3** Auto-capture `remote_url` from git during `Capture()` and `Ingest()`
  - `resolveRemoteURL()` helper on `SessionService` reads `git remote get-url origin`
  - `IngestRequest.RemoteURL` allows explicit remote URL from external clients

- [x] **8.5.4** `ListOptions.RemoteURL` + `SearchQuery.RemoteURL` filtering
  - Filter sessions by normalized remote URL in both List and Search queries

- [x] **8.5.5** `ProjectGroup` domain type + `ListProjects()` store/service/API
  - Groups sessions by `remote_url` (git repos) or `project_path` (non-git)
  - Includes `display_name`, `session_count`, `total_tokens`, `provider`
  - `GET /api/v1/projects` endpoint + client SDK `ListProjects()`

- [x] **8.5.6** Tests
  - `NormalizeRemoteURL`: 10 cases (HTTPS, SSH, ssh://, edge cases)
  - `ListProjects` API endpoint: ingest 2 projects, verify grouping

### Milestone 8.6 — Analysis Platform (Ollama + API + Cron)

**Goal:** Make session analysis a first-class feature: configurable LLM backend (Anthropic API, Ollama local, OpenCode CLI), REST API, and scheduled batch analysis.

> **Prerequisite:** Phase 5.5 (Session Efficiency Analysis — exists), Analysis BC (exists).
>
> **Context:** Today analysis works but is limited: only via CLI (`aisync analyze`) or web button, only Claude CLI or OpenCode as LLM backends, no API endpoints, no scheduling.
> The target: a developer with a local GPU runs nightly cron jobs on Ollama to analyze all sessions of the day. Or an Anthropic subscriber uses Claude API with a token.

#### 8.6.1 — Ollama Analysis Adapter

**Architecture**: New adapter `internal/analysis/ollama/analyzer.go` implementing the existing `analysis.Analyzer` port interface.

- [x] **8.6.1a** Create `analysis.AdapterOllama` enum value (`"ollama"`) in `internal/analysis/domain.go`
- [x] **8.6.1b** Implement `ollama.Analyzer` — calls `POST /api/chat` with structured system prompt
  - Reuses `llmadapter.BuildAnalysisPrompt()` for the session data section
  - JSON mode: Ollama's `format: "json"` parameter
  - Configurable: model name, base URL, request timeout
- [x] **8.6.1c** Constructor: `NewAnalyzer(OllamaConfig{BaseURL, Model, Timeout})`
  - Default BaseURL: `http://localhost:11434`
  - Default Model: `qwen3:30b` (or configurable)
  - Default Timeout: 120s (local models can be slow)
- [x] **8.6.1d** Tests: 15 unit tests (httptest mock server) + 1 integration test (skipped if Ollama unavailable)
  - Success, markdown-fenced JSON, score clamping, empty messages/response, invalid JSON
  - Server error, context cancellation, extractJSON edge cases, parseReport validation
  - Integration: tries qwen3:8b/4b/1.7b, llama3.2:3b, gemma3:4b, phi4-mini

#### 8.6.2 — Anthropic API Adapter (direct, no CLI)

**Architecture**: New adapter `internal/analysis/anthropic/analyzer.go` — calls Anthropic Messages API directly via HTTP, no dependency on `claude` CLI binary.

- [x] **8.6.2a** Implement `anthropic.Analyzer` — calls `POST https://api.anthropic.com/v1/messages`
  - Uses API key from config (`analysis.api_key`) or env `ANTHROPIC_API_KEY`
  - Config key overrides env var; `NewAnalyzer` fails if neither is set
  - Reuses `llmadapter.BuildAnalysisPrompt()` for content
  - Default model: `claude-sonnet-4-20250514`; configurable
  - JSON extraction fallback (markdown fences, brace matching)
  - `AdapterAnthropic` enum added to `analysis.AdapterName`
- [x] **8.6.2b** Config: `analysis.adapter` accepts `"anthropic"`, new key `analysis.api_key`
  - API key masked in `aisync config get` output (`****xxxx`)
  - Factory wiring creates `anthropicanalyzer.NewAnalyzer(Config{APIKey, Model})`
- [x] **8.6.2c** Tests: 13 unit tests + 1 integration test
  - API key: missing, from env, config overrides env
  - Analyze: success, markdown fenced, score clamping, empty messages/response, invalid JSON
  - API errors: 429, context cancellation
  - Integration: uses `claude-haiku-4-20250514` (skipped if no ANTHROPIC_API_KEY)

#### 8.6.3 — Extended Configuration

**Architecture**: Extend `internal/config/config.go` analysis section.

- [x] **8.6.3a** Extend `analysisConf` struct:
  ```json
  {
    "analysis": {
      "auto": true,
      "adapter": "ollama",
      "model": "qwen3:30b",
      "error_threshold": 20,
      "min_tool_calls": 5,
      "ollama_url": "http://localhost:11434",
      "api_key": "",
      "schedule": "0 22 * * *"
    }
  }
  ```
- [x] **8.6.3b** Add config keys with validation in `Set()`:
  - `analysis.adapter`: accept `"llm"`, `"opencode"`, `"ollama"`, `"anthropic"`
  - `analysis.ollama_url`: URL validation
  - `analysis.api_key`: string (never displayed in `aisync config list`)
  - `analysis.schedule`: cron expression validation
- [x] **8.6.3c** Update factory wiring (`pkg/cmd/factory/default.go`) to create Ollama adapter
- [x] **8.6.3d** Tests: ResolveProfile edge cases, provider infra resolution, adapter round-trips

**Test plan:**
- Config Set/Get round-trip for each new key
- Factory creates correct adapter based on config
- Invalid adapter name rejected

#### 8.6.4 — Analysis API Endpoints

**Architecture**: Add REST endpoints to `internal/api/` for remote analysis.

- [x] **8.6.4a** `POST /api/v1/sessions/{id}/analyze` — trigger analysis
  - Request body: `{"trigger": "manual"}` (or empty = manual)
  - Returns `201 Created` with `SessionAnalysis` JSON
  - Long-running: may take 30-120s for Ollama models
- [x] **8.6.4b** `GET /api/v1/sessions/{id}/analysis` — get latest analysis for a session
  - Returns `200 OK` with `SessionAnalysis` JSON, or `404`
- [x] **8.6.4c** `GET /api/v1/sessions/{id}/analyses` — list all analyses for a session
  - Returns `200 OK` with `[]SessionAnalysis` JSON
- [x] **8.6.4d** Injected `*AnalysisService` as optional field in `api.Server` (ISP — separate from `SessionServicer`)
  - Handlers return 404 when `analysisSvc` is nil
  - Wired in `servecmd` alongside existing services
- [x] **8.6.4e** Client SDK: `client.AnalyzeSession()`, `GetAnalysis()`, `ListAnalyses()` + types
- [x] **8.6.4f** Remote adapter: `remote.AnalysisService` implements `AnalysisServicer` via HTTP
  - `AnalysisServicer` interface extracted (ISP — separate BC from SessionServicer)
  - Factory returns `AnalysisServicer` (not concrete type) — dual-mode ready
  - Factory auto-detects remote mode and creates `remote.AnalysisService` when server is connected
- [x] **8.6.4g** Tests: 8 API tests (trigger, trigger-auto, get, get-404, list, list-empty, no-service-analyze, no-service-get)

**Test plan:**
- API test with in-memory store + mock analyzer
- Test 404 for non-existent session
- Test analysis persistence after trigger

#### 8.6.5 — Cron Scheduler in `aisync serve`

**Architecture**: Lightweight goroutine-based scheduler in the server.

- [x] **8.6.5a** Created `internal/scheduler/` package with `robfig/cron/v3`
  - `Scheduler` struct: wraps cron with Start/Stop lifecycle, `Task` port interface
  - `TaskResult` tracks last execution per task (name, time, duration, error)
  - `Status()` returns all task results for monitoring
- [x] **8.6.5b** Built-in task `analyze_daily` (`AnalyzeDailyTask`):
  - Lists sessions from configurable lookback window (default 24h)
  - Filters sessions not yet analyzed (checks `GetLatestAnalysis`)
  - Applies `min_tool_calls` and `error_threshold` filters
  - Runs analysis on each via `AnalysisServicer`, logs results (analyzed/skipped/failed)
  - Respects context cancellation for graceful shutdown
- [x] **8.6.5c** Wired scheduler into `aisync serve` lifecycle
  - Starts after server is ready, stops before HTTP shutdown on signal
  - Only enabled when `analysis.schedule` is configured (e.g. `"0 22 * * *"`)
  - Reads `analysis.error_threshold` and `analysis.min_tool_calls` from config
- [x] **8.6.5d** `GET /api/v1/scheduler/status` — show scheduled tasks and last execution
- [x] **8.6.5e** Tests: 13 tests total
  - Scheduler: invalid schedule, empty schedule skip, start/stop idempotent, task execution, task error, status
  - AnalyzeDailyTask: name, analyze new sessions, skip analyzed, skip old, min_tool_calls filter, no sessions, context cancellation

**Test plan:**
- Unit test scheduler with short intervals (100ms)
- Test `analyze_daily` task filters un-analyzed sessions correctly
- Test graceful shutdown stops scheduler

#### 8.6.6 — Skill Observer

**Goal**: Detect which skills were loaded vs should have been loaded during a session.
Produces a `SkillObservation` report that feeds into analysis and future Skill Resolver agent.

**Architecture**: New package `internal/skillobs/` (Skill Observation bounded context).
- `SkillRegistry`: reads available skills from `.opencode/skill/*/SKILL.md` and `.claude/skills/*/SKILL.md`
- `Recommender`: keyword-based matching of user messages → recommended skills
- `Detector`: scans tool calls for skill-loading patterns (`load_skill`, `mcp_skill`, `skill`)
- `Observer`: combines recommender + detector → `SkillObservation`

- [x] **8.6.6a** Add `SkillObservation` domain type to `internal/analysis/domain.go`
  ```go
  type SkillObservation struct {
    Available   []string  // all skills known for this project
    Recommended []string  // skills the recommender suggests based on user messages
    Loaded      []string  // skills actually loaded (detected from tool calls)
    Missed      []string  // recommended but not loaded
    Discovered  []string  // loaded but not recommended (agent found it on its own)
  }
  ```
- [x] **8.6.6b** Skill load detector (`internal/skillobs/detector.go`): scan tool calls for skill-loading patterns
  - Tool names: `load_skill`, `mcp_skill`, `skill`, `read_skill` (configurable list)
  - Also detect `<skill_content name="X">` patterns in message text
  - Extract skill name from tool Input JSON (`name`, `skill_name` fields)
- [x] **8.6.6c** Keyword-based skill recommender (`internal/skillobs/recommender.go`)
  - Each `registry.Capability` with `Kind=skill` has a `Description`
  - Extract keywords from description + skill name
  - Match user messages against keywords (case-insensitive, word boundary)
  - Configurable: add custom `keywords` and `trigger_patterns` per skill
- [x] **8.6.6d** Integrate into `AnalysisReport` as optional `SkillObservation` field
  - Populated during analysis (post-LLM enrichment step)
  - Included in API response and CLI output
- [x] **8.6.6e** Tests: 17 tests total
  - Unit: keyword matcher (user message → recommended skills)
  - Unit: tool call detector (tool calls → loaded skills)
  - Unit: observer combines both → missed/discovered
  - Integration: full session with known skills → correct observation

#### 8.7 — LLM Provider Profiles & Session Tagging

##### 8.7.A — LLM Provider Profiles (Config Refactoring)

**Goal**: Separate LLM provider infrastructure (URLs, API keys) from functional usage
(which model to use for analysis, tagging, etc.). Enable per-feature LLM profile selection.

**Current state**: `analysis.adapter`, `analysis.model`, `analysis.ollama_url`, `analysis.api_key`
are all flat keys mixing provider infra with functional config.

**Target config structure**:
```json
{
  "llm": {
    "providers": {
      "ollama": { "url": "http://localhost:11434" },
      "anthropic": { "api_key": "sk-ant-..." }
    },
    "profiles": {
      "default": { "provider": "ollama", "model": "qwen3:30b" },
      "fast":    { "provider": "ollama", "model": "qwen3:4b" },
      "cloud":   { "provider": "anthropic", "model": "claude-haiku-4-20250514" }
    }
  },
  "analysis": { "auto": true, "profile": "default" },
  "tagging":  { "auto": true, "profile": "fast", "tags": [...] }
}
```

- [x] **8.7.A1** Add `LLMProviderConfig` and `LLMProfile` types to `internal/config/`
  ```go
  type LLMProviderConfig struct {
    URL    string `json:"url,omitempty"`     // Ollama base URL
    APIKey string `json:"api_key,omitempty"` // Anthropic API key
  }
  type LLMProfile struct {
    Provider string `json:"provider"` // "ollama", "anthropic", "opencode", "llm"
    Model    string `json:"model"`
  }
  ```
- [x] **8.7.A2** Add `llm.providers` and `llm.profiles` sections to config
  - Backward compatible: old `analysis.adapter` / `analysis.model` still work
  - New `analysis.profile` takes precedence over legacy keys
- [x] **8.7.A3** Create `internal/llmfactory/` — factory that creates an `analysis.Analyzer`
  from a profile name:
  - Reads profile → finds provider → creates the right adapter
  - Replaces the switch/case in `pkg/cmd/factory/default.go`
  - SRP: one place to create analyzers, used by analysis, tagging, and future features
- [x] **8.7.A4** Migrate existing config keys (backward compat layer)
  - `analysis.adapter` + `analysis.model` → maps to `llm.profiles.default`
  - `analysis.ollama_url` → maps to `llm.providers.ollama.url`
  - `analysis.api_key` → maps to `llm.providers.anthropic.api_key`
- [x] **8.7.A5** Tests: 7 tests (set/get profiles, set/get providers, invalid provider, resolve from profiles/analysis.profile/legacy/override)
- [x] **8.7.A6** CLI: `aisync config set llm.profiles.fast.provider ollama`
  - Dot-notation for nested config

##### 8.7.B — Session Tagging & Classification

**Goal**: Tag sessions by type (feature, bug, refactor, exploration, etc.) to enable
per-type analytics and trend monitoring. This is the foundation for measuring whether
skills and agent improvements actually reduce errors and token usage over time.

**Depends on**: 8.7.A (LLM Profiles — uses `tagging.profile` to select the LLM)

- [x] **8.7.B1** Add `SessionType` field to `session.Session` + `Summary`:
  - Default types: `feature`, `bug`, `refactor`, `exploration`, `review`, `devops`, `other`
  - Custom tags per project in config: `tagging.tags`
  - Stored as columns in SQLite sessions table (migration 009)
- [x] **8.7.B2** LLM-based auto-classifier (`internal/tagger/`):
  - Triggered post-capture (like auto-analysis)
  - Uses `tagging.profile` (e.g. `"fast"` → Ollama qwen3:4b for speed)
  - Input: first 10 user messages + session summary
  - Prompt: "Given these tags [...], classify this session. Return JSON: {tag, confidence, reasoning}"
  - Falls back to "other" if confidence < 0.5 or LLM unavailable
- [x] **8.7.B3** Manual tagging via API:
  - `aisync tag <session-id> --type feature`
  - `PATCH /api/v1/sessions/{id}` with `{"session_type": "bug"}`
  - Manual tag overrides auto-classification
- [x] **8.7.B4** Post-capture auto-tagging hook (same pattern as auto-analysis)
- [x] **8.7.B4b** Per-type analytics:
  - `aisync stats --by-type` → tokens/errors/sessions grouped by type
  - `GET /api/v1/stats?group_by=session_type`
  - Dashboard: chart showing tokens per type, error rate per type
- [x] **8.7.B5** Trend monitoring:
  - Weekly comparison: are bugs taking fewer tokens this week vs last week?
  - Per-skill impact: after adding skill X, did error rate for `bug` sessions decrease?
  - Dashboard: trend line over time per session type
  - API: `GET /api/v1/stats/trends?type=bug&period=weekly`

#### 8.8 — Session Replay & Validation ✅

**Goal**: Replay a captured session against the same agent/provider in the same project
context to validate that improvements (skill changes, agent config) produce better results.
This is regression testing for AI agents.

> **Key insight**: When we detect a missed skill and improve the SKILL.md, we need to
> verify the improvement actually works. Replay the same user messages, same project,
> same commit — and check if the agent now loads the skill and responds better.

- [x] **8.8.1** Domain model: `ReplayRequest`, `ReplayResult`, `Comparison` (`internal/replay/domain.go`)
- [x] **8.8.2** Runner port interface + OpenCode/Claude Code implementations (`internal/replay/runner.go`)
  - `Runner` interface: `Run(ctx, workDir, message, opts) (string, error)`
  - `OpenCodeRunner`: calls `opencode run --dir <worktree> --agent <agent> --format json`
  - `ClaudeCodeRunner`: calls `claude -p --output-format json` from worktree cwd
- [x] **8.8.3** Compare function (`internal/replay/compare.go`)
  - Tokens delta, errors delta, tool calls, skills loaded diff
  - Weighted verdict: `improved` / `same` / `degraded`
- [x] **8.8.4** Git worktree sandbox (`internal/replay/worktree.go`)
  - `CreateWorktree(repoDir, commitSHA)` → detached worktree in `/tmp/aisync-replay-*`
  - `Remove()` → `git worktree remove --force` + `os.RemoveAll`
- [x] **8.8.5** Engine orchestrator (`internal/replay/engine.go`)
  - Load session → extract user messages → create worktree → run agent → compare → cleanup
  - Deferred cleanup on failure, context cancellation support
- [x] **8.8.6** CLI: `aisync replay <session-id>` (`pkg/cmd/replaycmd/replaycmd.go`)
  - Flags: `--provider`, `--agent`, `--model`, `--commit`, `--max-messages`, `--json`
  - Text output with comparison table and verdict icon (↑ improved / = same / ↓ degraded)
- [x] **8.8.7** API endpoint: `POST /api/v1/sessions/{id}/replay`
  - Wired into `api.Server` via optional `*replay.Engine` field
  - Server creates engine with OpenCode + Claude Code runners
- [x] **8.8.8** Client SDK: `ReplaySession(sessionID, ReplayRequest)` (`client/sessions.go`)
  - Types: `ReplayRequest`, `ReplayResult`, `ReplayComparison`
- [x] **8.8.9** Tests: 14 tests (compare, extract, worktree, engine error cases)
- [x] **8.8.10** Replay session capture from worktree (`internal/replay/capturer.go`)
  - `Capturer` port interface: `CaptureReplay(provider, worktreePath, originalID) (*Session, error)`
  - `ProviderCapturer` implementation: uses existing providers (OpenCode, Claude Code) with worktree path
  - Detects the most recent session in the worktree, exports it, saves to store
  - Creates `replay_of` link between replay session and original
  - Added `SessionLinkReplayOf` link type to `session.SessionLinkType`
  - Wired into Engine, CLI (`replaycmd`), and API (`servecmd`)
  - Providers use global data dirs — session matched by exact worktree path
- [x] **8.8.11** Integration with Skill Resolver (depends on 8.9):
  - After improving a SKILL.md, replay missed-skill sessions
  - Verify the skill is now loaded → accept/reject the improvement
  - Implemented via `aisync skills validate` command

#### 8.9 — Skill Resolver Agent ✅

**Goal**: When skill misses are detected, propose concrete improvements to make skills
more discoverable. Consumes `SkillObservation` output from 8.6.6.

> **Depends on**: 8.6.6 (Skill Observer), 8.8 (Replay for validation)

- [x] **8.9.1** Domain types: `SkillImprovement`, `ResolveRequest`, `ResolveResult`, `Verdict`, `ImprovementKind`
  - Validation, string methods, helper functions (8 tests)
- [x] **8.9.2** `SkillAnalyzer` port interface + LLM adapter (`llmanalyzer`)
  - Wraps `analysis.Analyzer`, structured JSON prompt, response parsing (10 tests)
- [x] **8.9.3** SKILL.md reader/writer with YAML frontmatter support
  - Reads description/keywords, applies improvements (keywords, description, content) (19 tests)
- [x] **8.9.4** Resolver service orchestration
  - Observer → analyze → propose → apply (11 tests)
- [x] **8.9.5** CLI: `aisync skills suggest` + `aisync skills fix`
  - Text + JSON output, `--skills` filter flag
- [x] **8.9.6** API endpoint + client SDK + validation
  - `POST /api/v1/sessions/{id}/skills/resolve` handler (5 API tests)
  - Client SDK: `ResolveSkills()` method
  - Server wiring in `aisync serve` (best-effort, nil-safe)
  - `aisync skills validate <session-id>` — fix + replay loop
    - Applies improvements → replays session → checks skill loading
    - Verdicts: `validated`, `partial`, `not_improved`, `replay_no_comparison`

---

**Phase 8.6 implementation order (completed):**
1. ~~**8.6.1** Ollama adapter~~ ✅
2. ~~**8.6.3** Config~~ ✅
3. ~~**8.6.4** API endpoints~~ ✅
4. ~~**8.6.2** Anthropic adapter~~ ✅
5. ~~**8.6.5** Cron scheduler~~ ✅
6. ~~**8.6.6** Skill observation~~ ✅

---

## Phase 9 — Multi-User & Notifications (Future)

> **Goal:** Team features — user-scoped views, webhooks, and scheduled tasks.
>
> **Prerequisite:** Phase 7 (server-first architecture).
>
> **Timeline:** Deferred — build when team/multi-user need is concrete.

### Milestone 9.1 — Authentication & Multi-User ✅

**Goal:** Identify users and scope data access.

- [x] **9.1.1** Add API key + JWT authentication to `aisync serve`
  - Two auth methods: JWT Bearer tokens (CLI/dashboard) + API Keys (`X-API-Key` header)
  - Auth BC: `internal/auth/` with domain types, service, JWT manager
  - Auth middleware: Bearer JWT + X-API-Key, injects Claims into context
  - First user = admin automatically
  - Auth disabled by default (`server.auth.enabled = false`)
  - Token file: `~/.aisync/token` with 0600 permissions
  - View DTOs separated from domain with explicit mappers
  - CLI: `aisync auth register/login/logout/me/keys` subcommands
  - Client SDK: `WithAuthToken()` and `WithAPIKey()` options
  - 50 auth tests total (domain, JWT, service, tokenfile, SQLite)

- [x] **9.1.2** User-scoped views: owner filter on web dashboard sessions page + `owner_id` param on API list/stats
  - "My sessions" filter (based on `owner_id` from API key → user mapping)
  - "All sessions" for team dashboards
  - KPIs: global vs personal toggle

- [x] **9.1.3** `--user` / `--me` flags on `aisync list` and `aisync stats` commands
  - `aisync list --me` → filter by current user's `owner_id`
  - `aisync stats --me` → personal stats only

### Milestone 9.2 — Webhooks ✅

**Goal:** Notify external systems when events happen in aisync.

- [x] **9.2.1** Add `webhooks` config section (`internal/config/config.go`)
  ```json
  {
    "webhooks": {
      "hooks": [
        { "url": "http://parlay:8080/webhook", "events": ["session.captured", "session.analyzed"] },
        { "url": "https://hooks.slack.com/...", "events": ["session.captured"] }
      ]
    }
  }
  ```

- [x] **9.2.2** Implement webhook dispatcher (`internal/webhooks/dispatcher.go`)
  - Fire-and-forget HTTP POST with JSON payload on configured events
  - Events: `session.captured`, `session.analyzed`, `session.tagged`, `skill.missed`
  - Event filtering per webhook (empty = all events)
  - Configurable retry (default: 1) with linear backoff
  - Nil-safe (no panic when dispatcher is nil)
  - Headers: `Content-Type`, `User-Agent`, `X-AiSync-Event`

- [x] **9.2.3** Integrated into post-capture hook
  - `session.captured` fired immediately after capture
  - `session.tagged` fired after auto-classification
  - `session.analyzed` fired after auto-analysis
  - 7 tests: success, event filter, all events, retry, nil-safe, no hooks, matches

- [ ] **9.2.4** Parlay as webhook consumer *(future — requires Parlay webhook endpoint)*

### Milestone 9.3 — Scheduled Tasks ✅

**Goal:** Background jobs for recurring operations.

- [x] **9.3.1** Add `scheduler` config section with dot-notation keys
  - `scheduler.gc.enabled`, `scheduler.gc.cron`, `scheduler.gc.retention_days`
  - `scheduler.capture_all.enabled`, `scheduler.capture_all.cron`
  - `scheduler.stats_report.enabled`, `scheduler.stats_report.cron`
  - Cron expression validation via `robfig/cron/v3` parser in `Set()`
  - Sensible defaults: GC at 3 AM / 90d retention, capture every 30m, stats hourly

- [x] **9.3.2** Implement three scheduled tasks in `internal/scheduler/`
  - `gc_task.go` — calls `SessionServicer.GarbageCollect()` with retention days
  - `capture_task.go` — calls `SessionServicer.CaptureAll()` from all providers
  - `stats_task.go` — calls `SessionServicer.Stats()` to warm the stats cache
  - All follow existing `Task` interface pattern (Name + Run)
  - Wired into `pkg/cmd/servecmd/serve.go` alongside existing analyze_daily task

- [x] **9.3.3** `GET /api/v1/scheduler/status` — shows all task results (existing endpoint)
  - Already existed from Phase 8.6.5d, now covers all registered tasks

---

## Decisions Log (Phase 7-9)

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| D40 | Phase ordering | **Server-first (7) before ingest (8)** | Ingest needs a running service. Build foundations first, features second. |
| D41 | Parlay integration method | **Push via HTTP API** (Option A) | Parlay is already a Go server — POST directly to `aisync serve`. No disk-based provider adapter needed. |
| D42 | Ingest endpoints | **`/ingest` (universal) + `/ingest/ollama` (native)** | Universal for any client. Ollama-native so clients don't need to convert — zero friction. |
| D43 | Ollama format handling | **Server-side converter** | The server understands Ollama's native format (tool_calls, tool role, eval_count). Client sends raw Ollama data. |
| D44 | CLI dual-mode strategy | **Auto-detect with fallback** | If `server.url` is configured and healthy → use HTTP. Otherwise → SQLite direct. Zero-config for existing users. |
| D45 | Server merge strategy | **Merge web into serve** | One process, one port, one command. `aisync web` becomes alias. Simpler operations. |
| D46 | Authentication (MVP) | **API key, optional** | Localhost = no auth by default. Remote = API key in header. Simple, no OAuth complexity. |
| D47 | Webhook delivery | **Fire-and-forget + 1 retry** | aisync is not a message broker. Best-effort delivery is sufficient. |
| D48 | Cron location | **Inside `aisync serve`** | Only runs when server is active. No standalone daemon. Config-driven, not code-driven. |

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
- [x] ~~Session deduplication~~ — Removed in Phase 5.1. Each capture now creates a new session (multi-session per branch)
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

- [x] Session expiration / cleanup policy — `aisync gc --older-than --keep-latest --dry-run`, API `POST /api/v1/gc`, MCP `aisync_gc`, client SDK `GarbageCollect()`, `DeleteOlderThan()` in Store interface
- [x] Compression for large sessions (zstd) — backwards-compatible magic-byte detection, sync.Pool encoders
- [x] `aisync diff <session-1> <session-2>` — side-by-side session comparison: token/cost deltas, file overlap, tool diff, message divergence. Service `Diff()` + 6 tests, CLI `aisync diff <id1> <id2> [--json]`, API `GET /api/v1/sessions/diff?left=&right=`, MCP `aisync_diff` (23 tools), client SDK `Diff()`
- [x] Telemetry opt-in — `internal/telemetry/` package with Collector interface, LocalCollector (JSONL), NoopCollector, config key `telemetry.enabled`
- [x] Fix `Import()` for Claude Code and OpenCode providers — both fully implemented (CanImport=true)
- [x] Fix `aisync show <commit-sha>` — verified: Get() correctly chains commit SHA → trailer → session ID; added tests for ParseSessionTrailer, looksLikeCommitSHA, and Get()
- [x] Add `forked_at_message` field to session relationships (needed for rewind → fork linking in 5.0.5 + 5.1.4)

---

## Phase 10 — Structured Error Analysis (2026-03-25)

**Goal:** Capture, classify, and surface errors from AI coding sessions to answer "is this Anthropic's fault or ours?"

### 10.1 Domain Model (COMPLETE)
- [x] `SessionError` entity with classification fields (Category, Source, HTTPStatus, ProviderName, etc.)
- [x] `ErrorCategory` enum: provider_error, rate_limit, context_overflow, auth_error, validation, tool_error, network_error, aborted, unknown
- [x] `ErrorSource` enum: provider, tool, client
- [x] `SessionErrorSummary` with `NewSessionErrorSummary()` aggregator
- [x] `ErrorClassifier` port interface (Classify + Name)
- [x] `IsExternal()` helper for quick internal/external triage
- [x] 6 domain tests

### 10.2 Deterministic Classifier (COMPLETE)
- [x] `DeterministicClassifier` adapter in `internal/errorclass/`
- [x] HTTP status code classification (500→provider, 429→rate_limit, 401/403→auth, 400→validation/context_overflow, 529→overloaded)
- [x] Tool error patterns (permission denied, file not found, command not found, OOM, disk full, exit codes, network)
- [x] Message-based fallback patterns (aborted, internal server error, rate limit, context overflow, auth keywords)
- [x] 19 tests

### 10.3 OpenCode Error Extraction (COMPLETE)
- [x] `ocAPIError`/`ocAPIErrorData` structs added to `ocMessage`
- [x] `ExtractErrors()` function in `internal/provider/opencode/errors.go`
- [x] Extracts both API-level errors (from `message.data.error`) and tool-level errors (from tool call results)
- [x] Integrated into `Export()` — populates `Session.Errors`
- [x] `resolveProviderName()` from request URL
- [x] 6 tests

### 10.4 Storage Layer (COMPLETE)
- [x] `ErrorStore` interface: SaveErrors, GetErrors, GetErrorSummary, ListRecentErrors
- [x] Migration 017: `session_errors` table with 3 indexes (session_id, category, occurred_at)
- [x] SQLite implementation in `internal/storage/sqlite/error_store.go`
- [x] MockStore updated (centralized + inline mock stores)

### 10.5 ErrorService + PostCapture Integration (COMPLETE)
- [x] `ErrorService` in `internal/service/error.go` — classify + persist
- [x] `ErrorServicer` interface in `iface.go`
- [x] `ProcessSession()` — classify raw errors, persist via ErrorStore (idempotent/upsert)
- [x] Query methods: `GetErrors()`, `GetSummary()`, `ListRecent()`
- [x] Skips already-classified errors (re-classification guard)
- [x] Factory wiring: `ErrorServiceFunc` in Factory, lazy singleton with DeterministicClassifier
- [x] PostCapture hook: error processing runs automatically on every capture (fast, deterministic)
- [x] 11 tests (total: 1693 tests passing)

### 10.6 API Endpoints (DONE)
- [x] `GET /api/v1/sessions/{id}/errors` — all classified errors for a session
- [x] `GET /api/v1/sessions/{id}/errors/summary` — aggregated error statistics
- [x] `GET /api/v1/errors/recent?limit=50&category=provider_error` — recent errors cross-sessions
- [x] View DTOs + mappers (domain/view boundary separation, following auth handler pattern)
- [x] Optional service (nil-check guard, 404 when error service not configured)
- [x] Query param validation (category, limit with bounds)
- [x] Wired ErrorService into `api.Config` in serve command
- [x] 11 tests (total: 1704 tests passing)

### 10.6b CLI/MCP/Config/Scheduler (COMPLETE)
- [x] Config toggles: `errors.classifier` (deterministic/composite), `errors.llm_fallback`, `errors.llm_schedule`, `errors.llm_profile`
- [x] Config accessor methods with defaults, Get/Set with validation (classifier enum, cron validation)
- [x] `aisync errors <session-id>` — CLI command for session errors (table + JSON output)
- [x] `aisync errors --recent` — recent errors across all sessions
- [x] `--category` filter, `--limit`, `--json` flags
- [x] `aisync_errors` MCP tool (session_id, recent, category, limit params)
- [x] MCP wiring: ErrorServicer added to Config, handlers, mcpcmd
- [x] `ReclassifyTask` scheduler task — finds unknown errors and reclassifies via ErrorService
- [x] Scheduler wiring in serve.go (uses `errors.llm_schedule` config)
- [x] `POST /api/v1/errors/reclassify` — manual trigger API endpoint
- [x] 17 new tests, 1718 total tests passing

### 10.6c Dashboard Filters: Status + Has Errors (COMPLETE)
- [x] Domain: `Status` and `HasErrors *bool` fields added to `SearchQuery`
- [x] Service: `Status` and `HasErrors` string fields in `SearchRequest`, wired in `Search()`
- [x] Storage: `WHERE status = ?` and `WHERE error_count > 0` clauses in `buildSearchWhere()`
- [x] Storage: Fixed `Search()` SELECT to include `project_category` and `status` columns (were missing)
- [x] Web handler: reads `status` and `has_errors` query params, passes to service, echoes to template
- [x] Template: 2 new `<select>` dropdowns (Status: Active/Idle/Archived, Errors: Has errors/No errors)
- [x] Bugfix: pagination links now propagate ALL filters (was missing `session_type`, `project_category`, `owner`)
- [x] 4 new storage tests (filter by status, filter by has_errors, combined, project_category+status in results)
- [x] 1723 total tests passing

### 10.7 LLM Classifier (FUTURE)
- [ ] `LLMClassifier` adapter using LLM for ambiguous tool errors
- [ ] `CompositeClassifier` — deterministic first, LLM fallback for "unknown"

### 10.8 LiteLLM Pricing Database Integration (COMPLETE)

Integrated the LiteLLM open-source pricing database (2500+ models) as a secondary pricing catalog,
chained via `FallbackCatalog` with the existing `EmbeddedCatalog` as offline fallback.

**Catalog Architecture:**
```
OverrideCatalog (user config overrides — top layer)
  └─ FallbackCatalog (first match wins)
       ├─ LiteLLMCatalog  ← 2500+ models from GitHub JSON cache
       └─ EmbeddedCatalog ← 15 models from catalog.yaml (offline fallback)
```

- [x] `LiteLLMCatalog` adapter (`internal/pricing/litellm.go`)
  - Parses LiteLLM JSON format (per-token → per-M-token conversion)
  - Extracts tiered pricing from `_above_Nk_tokens` suffix fields
  - Regex-based threshold extraction (128k, 200k, 256k, 272k patterns)
  - Computes tier multipliers (tier rate / base rate)
  - Cache-aware pricing: `cache_read_input_token_cost`, `cache_creation_input_token_cost`
  - Filters to `mode: "chat"` models only (1983 of 2594 entries)
  - `HTTPClient` interface for testability
- [x] `FallbackCatalog` — chains multiple `Catalog` implementations, first match wins
- [x] `UpdateCache()` — fetches from GitHub, validates JSON, writes to `~/.aisync/litellm_prices.json`
- [x] `CacheInfo()` — returns cache path, age, model count, last update time
- [x] `aisync update-prices` CLI command (`pkg/cmd/pricescmd/pricescmd.go`)
  - `--info` flag: show cache status without fetching
  - Spot-check output: verifies key models (Claude Opus 4, Sonnet 4, GPT-4o) are findable
  - Reports total models loaded, cache age, file size
- [x] Factory wiring: `initCalc()` in `pkg/cmd/factory/default.go` builds full chain
  - `FallbackCatalog(LiteLLMCatalog, EmbeddedCatalog)` → `OverrideCatalog` on top
  - Graceful degradation: if LiteLLM cache missing/corrupt, falls through to Embedded
- [x] 37 new LiteLLM tests (`internal/pricing/litellm_test.go`)
  - JSON parsing, tier extraction, provider filtering, cache management
  - FallbackCatalog semantics, calculator integration
  - Real-data test with actual LiteLLM JSON structure
  - `assertFloatApprox` helper for tolerance-based comparisons
- [x] 88 total pricing tests (51 existing + 37 new), all passing
- [x] 1761 total tests across 95 packages, 0 failures
