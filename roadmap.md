# aisync — Roadmap

> Last updated: 2026-02-17
> Status: Phase 1 MVP + Phase 2 + Phase 3 PR Integration COMPLETE — Ready for Phase 4.

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
| D11 | Architecture | **DDD-inspired, gh (GitHub CLI) patterns** | Factory DI, interface in domain, 1 package per command |
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

> **Architecture details** → see [architecture.md](./architecture.md)

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

## Phase 4 — CI Automation (Future)

- [ ] GitHub Action: on CI failure → prepare fix session with original context + CI errors
- [ ] Webhook notification: "Session available to fix PR #42"
- [ ] Slack/n8n integration
- [ ] GitLab / Bitbucket support

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

### Remaining

- [ ] Session expiration / cleanup policy
- [ ] Compression for large sessions (zstd)
- [ ] `aisync diff <session-1> <session-2>` to compare sessions
- [ ] Telemetry opt-in (anonymous usage stats)
