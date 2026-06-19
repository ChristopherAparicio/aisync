# Learnings — hermes-ai5-integration

## [2026-05-30] Init
- Messages stored in compressed `payload BLOB` on sessions row (NOT a messages table) — sqlite.go:20-37,201-252,289-347
- `delegateToolNames` map at session_ingest.go:13-23 — `delegate_task` is ABSENT, must be added
- Latest migration is 036 (sqlite.go:3906); 037=H1 free, 038=H2 free — confirmed by Oracle
- ProviderName is a closed enum at internal/session/enums.go:13-29
- Providers hardcoded at pkg/cmd/factory/default.go:236-243
- Hermes DB: ~/.hermes/state.db (respect HERMES_HOME env); cron: ~/.hermes/cron/jobs.json
- Hermes cron session_id convention: cron_{job_id}_{timestamp} (no FK — naming convention only)
- OpenCode adapter is the template: internal/provider/opencode/{opencode.go,reader.go,dbreader.go}
- AI5 link types: delegated_to/delegated_from at internal/session/enums.go:314-324
- H3 = read-only projection, NO migration needed

## [2026-05-30] T7: hermes Provider — session/token/cost mapping
- Created `internal/provider/hermes/hermes.go` implementing `provider.Provider` interface
- `New(hermesHome string)` resolves hermesHome from HERMES_HOME env → ~/.hermes fallback; opens dbReader, stores nil reader if state.db absent
- `Detect()` ignores projectPath/branch (Hermes has no project tracking); returns all root sessions (ParentSessionID.Valid == false); nil reader = empty list, no error
- `Export()` maps session metadata only; messages deferred to T8; loads child sessions recursively via `findChildSessions`
- `mapSession()`: StartedAt is float64 epoch **seconds** (not ms) → `time.Unix(int64(hs.StartedAt), 0)`
- ReasoningTokens has no dedicated field in `session.TokenUsage`; included in TotalTokens sum only
- `CanImport() = false`; Import returns error — Hermes state.db is managed exclusively by Hermes process
- `go build ./internal/provider/hermes/...` exits 0, LSP diagnostics clean

## [2026-05-30] T9: ParseCronJobs — cron/jobs.json reader
- Created `internal/provider/hermes/cron.go`: `ParseCronJobs(hermesHome string) ([]session.CronJob, error)`
- Reads `$hermesHome/cron/jobs.json`; unmarshals via `session.CronJobsFile` envelope
- Missing file (`os.IsNotExist`): returns `nil, nil` — not an error
- Malformed/truncated JSON: logs warning, calls `partialDecodeCronJobs` which walks tokens via `json.NewDecoder(bytes.NewReader(...))` to salvage complete job objects before the truncation point
- Hermes timestamps are RFC3339 strings — `*time.Time` fields on `session.CronJob` handle null/omitempty natively via standard `encoding/json`
- Test coverage: `TestParseCronJobs_Normal` (2 jobs, 1 enabled/1 paused, all fields asserted), `TestParseCronJobs_MissingFile` (nil, nil), `TestParseCronJobs_Truncated` (no panic, log emitted)
- Full hermes suite: 4/4 pass (3 new + TestFixture); `go build` exits 0

## [2026-05-30] T10: CronJobStore — SQLite persistence
- Created `internal/storage/sqlite/cron_job_store.go` in `package sqlite`
- `UpsertCronJob`: `INSERT OR REPLACE INTO cron_jobs (...)` with 19 columns; `*time.Time` → `sql.NullFloat64` (Unix epoch seconds); full struct serialized to `raw_json` via `json.Marshal`; `updated_at = time.Now().UTC().Unix()`
- `ListCronJobs(provider)`: queries `raw_json` only, deserializes each row; empty provider = all rows; non-empty = `WHERE provider = ?`; both `ORDER BY name`
- `GetCronJob(jobID)`: `SELECT raw_json WHERE job_id = ?`; returns `nil, nil` on `sql.ErrNoRows`
- Removed 3 stub one-liners from `sqlite.go` lines 3093-3095; methods now live in dedicated file
- Helper `timeToNullFloat64(t *time.Time) sql.NullFloat64` defined in `cron_job_store.go`
- 5 tests pass: `UpsertAndGet`, `Upsert_Idempotent`, `ListByProvider`, `GetNotFound`, `NullableTimestamp`
- `go test ./internal/storage/sqlite/... -run TestCronJob -v` → PASS (0.800s); LSP diagnostics clean on all 3 changed files

## [2026-05-30] T8: mapMessage — hermesMessage → session.Message

- Created `internal/provider/hermes/sentinel.go`: `stripSentinel(s string) string` strips `\x00\x01` prefix if present; independent copy from service package as required
- Added `mapMessage(hm hermesMessage) session.Message` to `hermes.go`:
  - `Role`: direct cast `session.MessageRole(hm.Role)` — Hermes uses same strings as session constants
  - `Content`: `stripSentinel(hm.Content.String)` if valid, else empty string
  - `Thinking`: `hm.Reasoning.String` if non-empty, else `hm.ReasoningContent.String` if valid — two-field fallback
  - `Timestamp`: `time.Unix(int64(hm.Timestamp), 0)` — float64 epoch seconds, same pattern as `mapSession`
  - `OutputTokens`: `hm.TokenCount` for assistant messages only; user/system get 0
  - `ToolCalls`: unmarshal `hm.ToolCalls.String` (JSON array) via local `hermesToolCallJSON` struct; `Input` field stored as raw JSON string; `State = ToolStateCompleted`
  - `session.Message` has no `FinishReason` or `ToolName` fields — those were skipped (struct does not define them)
- `Export()` wired: `if mode != StorageModeSummary { listMessages → mapMessage each }` — added before child session loading
- Added `internal/provider/hermes/messages_test.go` with 4 tests: `TestMapMessage_Basic`, `TestMapMessage_SentinelDecoded`, `TestMapMessage_ToolCall`, `TestMapMessage_Reasoning`
- Full hermes suite: 8/8 pass; `go build ./internal/provider/hermes/...` exits 0; LSP diagnostics clean on all 3 new/changed files
- Evidence saved to `.omo/evidence/task-8-map-messages.txt`

## [2026-05-30] T12: LinkCronRuns — cron session correlation

- Created `internal/provider/hermes/cronlink.go`: `CronRunLink` struct + `LinkCronRuns(jobs []session.CronJob, sessions []session.Summary) []CronRunLink`
- Parsing: `strings.Split(id, "_")` → jobID = `strings.Join(parts[1:len-1], "_")` — strips leading "cron" and trailing Unix timestamp; handles multi-underscore job IDs correctly
- Sessions without `"cron_"` prefix are silently skipped; sessions with <3 segments (no timestamp) are also skipped
- Job lookup via `map[string]*session.CronJob` built once from the jobs slice; pointer preserves Job field identity
- Orphan sessions (cron-prefixed but no matching job): `CronRunLink{Orphan: true, Job: nil}` — no panic, surfaced cleanly
- 4 tests pass: `TestLinkCronRuns_Match`, `TestLinkCronRuns_Orphan`, `TestLinkCronRuns_NonCronSkipped`, `TestLinkCronRuns_MultiSegment`
- Full hermes suite: 12/12 pass; LSP diagnostics clean on both new files
- Evidence saved to `.omo/evidence/task-12-link.txt`

## [2026-05-30] T11: Factory registration
- Added `hermes.New("")` to `provider.NewRegistry(...)` in `pkg/cmd/factory/default.go` so Hermes is auto-detected alongside Claude, OpenCode, and Cursor
- Added import for `github.com/ChristopherAparicio/aisync/internal/provider/hermes`
- Verification pending: `go build ./...`, `go test ./pkg/cmd/factory/...`

## [2026-05-30] T13: adapter end-to-end tests

- Created `internal/provider/hermes/adapter_test.go` in `package hermes` with 4 integration tests
- `TestAdapter_Sessions`: `Detect("","")` returns 2 root sessions; fixture-parent-001 has TotalTokens=1500 (1000+500), EstimatedCost=0.05, ActualCost=0.045; child session correctly filtered
- `TestAdapter_Messages`: `Export("fixture-parent-001", StorageModeCompact)` yields 2 messages; delegate_task tool call name confirmed; sentinel \x00\x01 prefix absent in mapped Content
- `TestAdapter_ChildLineage`: Children[0].ID=="fixture-child-001", Children[0].ParentID=="fixture-parent-001"
- `TestAdapter_NullTolerant`: hand-built minimal DB with NULL cost columns; user_id must be "" not omitted — plain `string` field in hermesSession cannot scan SQL NULL (no pointer or NullString); EstimatedCost and ActualCost correctly map to 0
- Full suite 16/16 pass after adding the 4 new tests; LSP diagnostics clean
- Evidence saved to `.omo/evidence/task-13-adapter-tests.txt`

## [2026-05-30] T17: handleSessionExchanges — H3 inter-agent exchange view

- Added `ExchangeEntry`, `exchangePage` types and `maxInlineChildren = 10` const to `internal/web/handlers.go`
- `buildExchangeTimeline(parent, children)` merges parent + child messages, sorts by `Msg.Timestamp`
- `isDelegationMessage(msg)` checks `ToolCall.Name` (lowercase) for `delegate_task`, `delegate`, `ask_subagent`
- `handleSessionExchanges`: loads parent via `s.sessionSvc.Get(id)`; falls back to `s.store.GetLinkedSessions(sess.ID)` filtered by `SessionLinkDelegatedTo` + `SourceSessionID == sess.ID` when `sess.Children` is empty
- Uses `s.render(w, "exchanges.html", data)` — consistent with all other page handlers
- Route `GET /sessions/{id}/exchanges` registered in `RegisterRoutes` after `GET /sessions/{id}`
- `{"templates/exchanges.html"}` added to `pageSpecs` in `server.go`
- Placeholder `internal/web/templates/exchanges.html` created with outer `{{define "exchanges.html"}}` wrapper (required by `render` helper pattern)
- `go build ./...` exits 0; LSP diagnostics clean on both changed files
- Evidence: `.omo/evidence/task-17-exchanges.txt`

## [2026-05-30] T22: capture/list Hermes CLI wiring

- `pkg/cmd/capture/capture.go` already routes `--provider` through `session.ParseProviderName`, so `hermes` is accepted without an enum change; help text now includes Hermes explicitly.
- Added a Hermes capture regression test in `pkg/cmd/capture/capture_test.go` covering both a real fixture provider and an empty Hermes home.
- Empty Hermes homes now exit 0 from `aisync capture --provider hermes` by treating `session.ErrSessionNotFound` as a no-op only for the Hermes provider path.
- End-to-end CLI verification with a temporary runner: empty `HERMES_HOME` returned 0 with no output; seeded `HERMES_HOME` captured `fixture-hermes-001`; `aisync list --global --provider hermes` showed the ingested session.
- Isolated registry verification with Hermes only: no-flag capture selected `fixture-hermes-001` from the Hermes fixture home, proving the factory detect path works when Hermes is the active provider.
- Evidence saved to `.omo/evidence/task-22-cli-capture.txt`

## [2026-05-30] T15: /cron web page — handleCron + template

- Added `fmtTimePtr(*time.Time) string` to `internal/web/funcs.go` templateFuncs — nil guard returns "—", delegates to `timeAgo(*t)`; no existing pointer-time formatter existed
- Added `cronPage` struct and `handleCron` handler to end of `internal/web/handlers.go`: nil-store guard, `s.store.ListCronJobs("")` for all providers, `s.render(w, "cron.html", ...)`
- `{"templates/cron.html"}` appended to `pageSpecs` in `server.go`; `GET /cron` route registered in `RegisterRoutes` after `/alerts`
- Cron nav link added to `layout.html` after Alerts `<li>`, before Settings gear; uses `{{if eq .Nav "cron"}}active{{end}}` pattern
- `internal/web/templates/cron.html` created: empty-state branch + data-table with 7 columns; `ScheduleDisplay` preferred over `Schedule`; badge classes from existing dashboard only
- `go build ./...` exits 0; `go test ./internal/web/...` → ok (4.126s)
- Evidence: `.omo/evidence/task-15-cron-handler.txt`

## [2026-05-30] T18: handleSessionExchangesMore — H3 load-more partial

- Added `handleSessionExchangesMore` to `internal/web/handlers.go` (after `handleSessionExchanges`)
- Reads `offset` query param (default 10, matching `maxInlineChildren`); slices `children[offset:offset+10]`; returns 200 empty when offset >= len(children)
- Reuses `buildExchangeTimeline(sess, batch)` from T17 — no duplication
- Same children fallback as `handleSessionExchanges`: `s.store.GetLinkedSessions` filtered by `SessionLinkDelegatedTo + SourceSessionID == sess.ID`
- Renders via `s.renderPartial(w, "exchanges_more_partial.html", data)` — consistent with other partial handlers
- `"templates/exchanges_more_partial.html"` added to `ParseFS` partials list in `server.go`
- Route `GET /partials/session-exchanges/{id}` registered in `RegisterRoutes` after `GET /partials/session-messages/{id}`
- Template uses `hx-trigger="revealed"` sentinel div for HTMX infinite-scroll; no `<html>/<head>/<body>` wrapper
- `go build ./...` exits 0; LSP diagnostics clean on both changed files
- Evidence: `.omo/evidence/task-18-loadmore.txt`

## [2026-05-30] T21: /graph Hermes badge + exchanges jump link

- `sgBadge.Type` is the link-type modifier ("delegation", "continuation", "replay", "related"); the per-node provider string lives on `sgNode.Provider` directly, so the Hermes badge is applied conditionally in the template via `{{if eq .Provider "hermes"}}badge-hermes{{end}}`
- Existing template already rendered a generic `badge-provider` span at line 184 — extended it in place rather than adding a new badge to avoid duplicating provider text
- Added a tiny `↔` jump anchor (`sg-badge-exchanges`) next to the session-ID link, gated on `.HasChildren` so it only appears on parent sessions that actually have exchanges to view; `onclick="event.stopPropagation()"` prevents the parent header's collapse toggle from firing
- Codebase does NOT define `--primary`; brand colour is `--accent` (#6c7ee1) / `--accent-light` (#8b9cf0). Used `var(--primary, var(--accent))` fallback chain in `.badge-hermes` so the task-spec token name is honored without introducing a new variable
- CSS section comments use `/* ── Name ── */` as a consistent convention across the whole file (lines 1833, 6834, 6841 etc.) — kept matching markers for `Graph Hermes provider badge`, `Compact badge variant`, `Exchanges jump link on graph nodes`
- Manual QA against live `aisync serve --web-only --addr 127.0.0.1:18371`: HTTP 200 on `/graph`, 3367 `badge-provider` instances total with exactly 1 carrying `badge-hermes` (the Hermes session) and 3366 plain; 178 `sg-badge-exchanges` anchors all resolving to `/sessions/{id}/exchanges`
- `go build ./...` exit 0; `go test ./internal/web/...` ok 4.218s; `go vet ./internal/web/...` clean
- Evidence: `.omo/evidence/task-21-graph-badge.txt`

## [2026-05-30] T16: cron-template CSS polish
- style.css :root vars: `--bg`, `--bg-card` (surface), `--bg-hover`, `--border`, `--text`, `--text-muted`, `--accent` (primary), `--accent-light`, `--green`, `--red`, `--yellow`, `--radius`, `--shadow` — NO `--surface`, `--primary`, or `--warning` exist; task spec naming was misleading, used actual names
- Existing badge convention: `.badge` base (line 1835) provides pill shape + uppercase + letter-spacing; semantic variants only set background+color (e.g. `.badge-info`, `.badge-warn`, `.badge-danger`, `.badge-neutral` at 7199-7219)
- cron.html (T15) references `.data-table`, `.table-container`, `.badge-success/error/muted`, `.page-subtitle`, `.fw-medium` — NONE existed; added all in single append block at EOF (line 7806+)
- exchanges.html/partial reference `.exchange-entry/agent/role` + `.badge-delegated` — added per spec, color-mix() with `--yellow` fallback for warning tone
- Followed existing append-only pattern; section headers use `/* ── Section ── */` to match in-file convention (1833, 1927, 3572, etc.)
- `go build ./...` -> exit 0

## [2026-05-30] T20: exchange handler tests

- Created `internal/web/exchanges_test.go` (package web, 8 tests) covering `handleSessionExchanges` and `handleSessionExchangesMore`
- Test helper used: `newTestServerWithStoreAndConfig(t)` (already in server_test.go) — returns `*Server, *sqlite.Store, *config.Config`; seeding via `store.Save(sess)` preserves `Children []Session` through JSON payload round-trip
- `buildExchangeTimeline` sorts by `Msg.Timestamp` — tests use explicit base+offset timestamps; string-index comparison verifies order in rendered HTML
- Child cap test: parent saved with 12 embedded children; template renders `from 12 agents` (plural) and `hx-get` load-more sentinel in `<div class="load-more-sentinel">`
- Missing-child-skip test: parent saved without embedded children; child saved and linked via `store.LinkSessions`; child deleted via `store.Delete`; handler skips the dangling link, returns 200 with 1 entry and "from 0 agents"
- Sentinel test required a small handler change: added `stripSentinelContent(s string) string` to `handlers.go` and called it on `msg.Content` inside `buildExchangeTimeline` — strips `\x00\x01` prefix before it reaches the template
- exchanges.html actual template text differs from initial placeholder: empty-state uses "No messages found…" div; stats use `<span class="badge">N messages</span>` + `<span class="text-muted">from M agents</span>`
- `go test ./internal/web/... -run TestExchanges -v` → 8/8 PASS (0.75s); full `go test ./internal/web/...` → ok 3.8s (no regressions); LSP diagnostics clean
- Evidence: `.omo/evidence/task-20-exchange-tests.txt`

## [2026-05-30] T23: TestHermesE2E — end-to-end smoke test

- Test location: `internal/e2e/hermes_e2e_test.go`, package `e2e`
- Setup: `hermestestdata.NewFixtureDB(t)` → hermesHome = `filepath.Dir(dbPath)`; writes `cron/jobs.json` with 1 job; `testutil.MustOpenStore(t)` for fresh SQLite; `hermes.New(hermesHome)` + `provider.NewRegistry(prov)` + `service.NewSessionService`; captures via `svc.CaptureByID`; parses and upserts cron jobs; `web.New(web.Config{SessionService: svc, Store: store})`
- Four subtests pass: `/sessions` lists fixture-parent-001, `/sessions/fixture-parent-001/exchanges` responds 200 with content, `/cron` responds 200 with fixture-cron-job, `/graph` responds 200
- Root cause of cron.html fix: `render(w, "cron.html", data)` calls `ExecuteTemplate(w, "cron.html", data)` which executes the file-level template (named "cron.html" by ParseFS basename convention). Since cron.html only had `{{define "title"}}` and `{{define "content"}}` blocks with no content outside them, the "cron.html" template was empty — producing a blank response. Fix: added `{{define "cron.html"}}{{template "layout" .}}{{end}}` wrapper (same pattern as all other page templates like dashboard.html, sessions.html, exchanges.html).
- `go test ./internal/e2e/... -run TestHermesE2E -v` → PASS (0.95s); `go build ./...` exits 0
- Evidence: `.omo/evidence/task-23-e2e.txt`
- Pre-existing failures (NOT caused by T23): `TestExchanges_Empty`, `TestExchanges_ChildCap`, `TestExchanges_MissingChildSkip` in `internal/web/exchanges_test.go` — these stem from T17/T18/T20 mismatches between test expectations and template/store behavior (children not populated from `store.Get` since they're stored as separate rows, not in the blob)
