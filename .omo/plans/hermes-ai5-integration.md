# Hermes Agent ↔ AI5 Integration (Roadmap H1 → H2 → H3 → Delegation)

## TL;DR

> **Quick Summary**: Add the open-source Hermes Agent (NousResearch) as a new ingestion source in AI5 by writing a `hermes` provider adapter that reads `~/.hermes/state.db`, then layer cron-task tracking, a brand-new message-level agent-to-agent exchange view, and delegation-link enrichment — reusing AI5's generic domain, storage, and lineage UI wherever possible.
>
> **Deliverables**:
> - H1: `internal/provider/hermes/` adapter ingesting Hermes sessions/messages/tokens/cost into AI5's generic model; `hermes` ProviderName; factory + discovery wiring.
> - H2: parser for `~/.hermes/cron/jobs.json`, migration 038 cron table, run↔job linking, and a `/cron` web view of scheduled tasks + run history.
> - H3: new `/sessions/{id}/exchanges` page projecting a merged parent↔child message timeline (the inter-agent exchange view that does not exist today), with lazy child loading.
> - Delegation: ensure Hermes `delegate_task` delegations are detected (tool-name + sentinel-prefix handling) and richly surfaced in `/graph`.
>
> **Estimated Effort**: Large (4 phases, ~23 tasks)
> **Parallel Execution**: YES — 5 implementation waves + 1 final review wave
> **Critical Path**: T2 → T7 → T8 → T17 → T19 → T20 → F1-F4 → user okay

---

## Context

### Original Request
"Je voudrais que tu fasses de l'exploration pour intégrer Hermes Agents avec AI5... analyser toute l'intégration nécessaire... identifier les interactions avec les agents... je pense qu'on n'a pas d'interface pour voir les échanges entre les agents." Then: "fais le plan global du coup? Roadmap de H1-2-3 et ensuite la délégation."

### Interview Summary
**Key Discussions**:
- AI5 already has a generic provider port and a session-lineage model; Hermes is "just another adapter" structurally.
- The real product gap the user cares about: **no UI shows message-level exchanges between agents** — only who-delegated-to-whom (`/graph`), never what was said.
- Roadmap is phased and sequential at the phase level (H1 foundation first) but parallel within phases.

**Research Findings** (verified via code/repo exploration):
- AI5 provider port: `internal/provider/provider.go:15-34` (Detect/Export/Import); registry `internal/provider/registry.go:10-88`.
- OpenCode adapter is the template: `internal/provider/opencode/{opencode.go,reader.go,dbreader.go}`.
- Providers hardcoded: `pkg/cmd/factory/default.go:236-243`. ProviderName is a **closed enum**: `internal/session/enums.go:13-29`.
- **Messages are NOT in a table** — they live in a compressed JSON `payload BLOB` on the `sessions` row (`internal/storage/sqlite/sqlite.go:20-37,201-252,289-347`). `Message` struct: `internal/session/session.go:115-132`.
- Lineage already modeled: `Session{Agent,ParentID,Children}` + `SessionLink{Source,Target,LinkType}` (types `delegated_to/delegated_from/...` at `internal/session/enums.go:314-324`). Delegation detection at `internal/service/session_ingest.go:13-23,180-239`.
- Latest migration is 036.
- Hermes DB: `~/.hermes/state.db` (schema v14, HEAD 689ef5e). `sessions` table carries tokens (input/output/cache_read/cache_write/reasoning), billing (`billing_provider`/`billing_base_url`/`billing_mode`/cost), `parent_session_id` lineage. `messages` table carries role/content(sentinel-prefixed JSON)/tool_calls/tool_name/token_count/finish_reason/reasoning.
- Hermes cron jobs are **NOT in SQLite** — `~/.hermes/cron/jobs.json` `{jobs:[...],updated_at}`. Runs get `session_id = "cron_{job_id}_{timestamp}"`; no FK, link is by ID naming convention.
- Hermes delegation = an assistant message with a `delegate_task` tool call + a child session whose `parent_session_id` points to the parent. No dedicated delegation table.

### Metis Review
**Identified Gaps** (addressed in this plan):
- H3 needs **no migration** — it is a read-time projection over payload BLOBs (merge parent+child `.Messages` in Go). → reflected in waves/migration plan.
- Migration reservation corrected to **037=H1, 038=H2, H3=none**.
- Delegation tool name: AI5's hardcoded list (`session_ingest.go:13-23`) may not contain `delegate_task`. → dedicated task T6 to verify+add (never rename existing entries).
- Sentinel-prefixed message content can break the `session_id` JSON scan used for delegation detection. → T6 strips sentinel before scanning.
- Stall detector is OpenCode-specific (`OpenCodeDBPath`); Hermes stall detection is **OUT of scope** (separate future task). → guardrail.
- H1→H3 dependency: H1 must fully map `Message.ToolCalls`/`Thinking`/`ContentBlocks` or H3 silently renders incomplete data. → explicit H1 acceptance assertions.
- H3 perf: N+1 payload decompression; cap children per view (10) + load-more partial. → T17/T18 design.

---

## Work Objectives

### Core Objective
Integrate Hermes Agent as a first-class AI5 ingestion source — surfacing its sessions, daily token/cost consumption, scheduled cron tasks, and agent-to-agent delegations — and add the missing message-level inter-agent exchange view, reusing AI5's existing generic domain/storage/UI.

### Concrete Deliverables
- `internal/provider/hermes/` adapter (reader + provider impl) reading `~/.hermes/state.db`.
- `session.ProviderName("hermes")` constant + validation; factory + discovery registration.
- Hermes cron parser + migration 038 + `/cron` web view.
- `/sessions/{id}/exchanges` page + `/partials/session-exchanges/{id}` partial.
- Delegation detection covering Hermes `delegate_task` + sentinel-safe scanning; `/graph` badge enrichment.
- Fixture `state.db` + `jobs.json` for tests; Go table-driven tests across all new code.

### Definition of Done
- [ ] `go build ./...` succeeds; `go vet ./...` clean.
- [ ] `go test ./...` passes including all new Hermes tests.
- [ ] With a fixture Hermes home, `aisync capture --provider hermes` ingests sessions visible in `/sessions` filtered by `agent`/provider.
- [ ] `/sessions/{id}/exchanges` renders a merged parent↔child timeline.
- [ ] `/cron` lists Hermes scheduled tasks with last_status/next_run.
- [ ] A Hermes `delegate_task` session produces a `delegated_to` row in `session_session_links`.

### Must Have
- New `hermes` provider implementing the existing `provider.Provider` port — no fork of the port.
- Full message mapping including `ToolCalls`, `Thinking`/reasoning, and `ContentBlocks` (sentinel-decoded).
- Append-only migrations starting at 037; no edits to existing migrations.
- All new UI is CSS-only (no JS framework), using existing CSS variables, appended to `style.css`.

### Must NOT Have (Guardrails)
- NO ingestion of Hermes trajectory JSONL files (`trajectory_samples.jsonl`) — not needed for monitoring.
- NO Hermes Telegram topic tables, NO write-back to `state.db` (no dbwriter equivalent), NO modification of the Hermes repo itself.
- NO renaming of existing config fields (`OpenCodeDBPath`) or existing delegation tool-name entries — additive only.
- NO Hermes stall detection in this roadmap (OpenCode-specific subsystem; defer as separate task).
- NO new migration for H3 (messages are in payload BLOB; H3 is read-only projection).
- NO live account/subscription API polling (Hermes does not persist account_id; out of scope here).
- NO emojis in code; no premature abstraction of a generic "multi-agent" layer beyond what the adapter needs.

### Spec Framework Integration
- **Detected Framework**: None (no `openspec/` or `.specify/` in `/Users/guardix/dev/aisync`). Section omitted from tasks.

---

## Verification Strategy (MANDATORY)

> **ZERO HUMAN INTERVENTION** — all verification is agent-executed.

### Test Decision
- **Infrastructure exists**: YES (Go `testing`, table-driven tests, `internal/testutil/mock_store.go`, `*_store_test.go`).
- **Automated tests**: YES (tests-after per task, matching existing patterns).
- **Framework**: `go test`.
- **Fixtures**: a generated fixture `state.db` (Hermes schema v14 subset) and a fixture `cron/jobs.json` enable adapter/parser tests WITHOUT a live Hermes install.

### QA Policy
Every task includes agent-executed QA scenarios. Evidence saved to `.omo/evidence/task-{N}-{scenario-slug}.{ext}`.
- **Library/adapter/store**: Bash (`go test`, `sqlite3`, `go run` harness).
- **API/handler**: Bash (`curl` against local daemon on port 5100).
- **Web UI**: Playwright (navigate, assert DOM, screenshot).

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (Start Immediately — foundation + scaffolding, all independent):
├── T1: hermes ProviderName enum + validation [quick]
├── T2: Hermes reader interface + state.db DB reader [unspecified-high]
├── T3: Fixture state.db generator for tests [quick]
├── T4: CronJob domain types [quick]
├── T5: Migration 038 cron table + NotificationLog-style store interface [unspecified-high]
└── T6: Verify+add delegate_task to delegation detection + sentinel-safe scan [quick]

Wave 2 (After Wave 1 — core mapping + parsers):
├── T7: Hermes adapter (Provider impl): session + token + cost mapping (depends T2) [deep]
├── T8: Hermes message/toolcall/reasoning/content(sentinel) mapping (depends T2,T7) [deep]
├── T9: Hermes cron jobs.json parser (depends T4) [unspecified-high]
└── T10: Cron store sqlite impl + tests (depends T5) [unspecified-high]

Wave 3 (After Wave 2 — wiring + ingest tests):
├── T11: Register hermes in factory + import discovery (depends T7,T8) [quick]
├── T12: Cron run↔job linking by cron_{job_id}_* convention (depends T9,T10) [deep]
├── T13: H1 adapter fixture tests (sessions/tokens/messages/toolcalls) (depends T7,T8,T3) [unspecified-high]
└── T14: Hermes delegation detection tests (depends T6,T13) [unspecified-high]

Wave 4 (After Wave 3 — web handlers + UI):
├── T15: Cron tasks web handler + route + nav (depends T12) [unspecified-high]
├── T16: Cron tasks template (CSS-only) (depends T15) [visual-engineering]
├── T17: H3 exchange handler /sessions/{id}/exchanges merge+cap (depends T8) [deep]
├── T18: H3 load-more partial /partials/session-exchanges/{id} (depends T17) [unspecified-high]
└── T19: H3 exchange template + link from session_detail (depends T17) [visual-engineering]

Wave 5 (After Wave 4 — tests, enrichment, CLI, smoke):
├── T20: H3 handler tests (merge ordering, empty, missing child, sentinel) (depends T17,T18) [unspecified-high]
├── T21: /graph badge enrichment for Hermes delegations (depends T14) [visual-engineering]
├── T22: CLI capture --provider hermes support (depends T11) [quick]
└── T23: End-to-end smoke with fixture Hermes home (depends T11,T12,T17,T22) [unspecified-high]

Wave FINAL (After ALL tasks — 4 parallel reviews, then user okay):
├── F1: Plan compliance audit (oracle)
├── F2: Code quality review (unspecified-high)
├── F3: Real manual QA (unspecified-high)
└── F4: Scope fidelity check (deep)
-> Present results -> Get explicit user okay

Critical Path: T2 → T7 → T8 → T17 → T19 → T20 → F1-F4 → user okay
Max Concurrent: 6 (Wave 1)
```

### Dependency Matrix

- **T1**: deps none — unblocks T11
- **T2**: deps none — unblocks T7, T8, T13
- **T3**: deps none — unblocks T13, T20, T23
- **T4**: deps none — unblocks T9
- **T5**: deps none — unblocks T10
- **T6**: deps none — unblocks T14
- **T7**: deps T2 — unblocks T8, T11, T13
- **T8**: deps T2, T7 — unblocks T11, T13, T17
- **T9**: deps T4 — unblocks T12
- **T10**: deps T5 — unblocks T12
- **T11**: deps T7, T8 — unblocks T22, T23
- **T12**: deps T9, T10 — unblocks T15, T23
- **T13**: deps T7, T8, T3 — unblocks T14
- **T14**: deps T6, T13 — unblocks T21
- **T15**: deps T12 — unblocks T16
- **T16**: deps T15 — final
- **T17**: deps T8 — unblocks T18, T19, T20, T23
- **T18**: deps T17 — unblocks T20
- **T19**: deps T17 — final
- **T20**: deps T17, T18 — final
- **T21**: deps T14 — final
- **T22**: deps T11 — unblocks T23
- **T23**: deps T11, T12, T17, T22 — final

### Agent Dispatch Summary

- **Wave 1**: 6 — T1 `quick`, T2 `unspecified-high`, T3 `quick`, T4 `quick`, T5 `unspecified-high`, T6 `quick`
- **Wave 2**: 4 — T7 `deep`, T8 `deep`, T9 `unspecified-high`, T10 `unspecified-high`
- **Wave 3**: 4 — T11 `quick`, T12 `deep`, T13 `unspecified-high`, T14 `unspecified-high`
- **Wave 4**: 5 — T15 `unspecified-high`, T16 `visual-engineering`, T17 `deep`, T18 `unspecified-high`, T19 `visual-engineering`
- **Wave 5**: 4 — T20 `unspecified-high`, T21 `visual-engineering`, T22 `quick`, T23 `unspecified-high`
- **FINAL**: 4 — F1 `oracle`, F2 `unspecified-high`, F3 `unspecified-high`, F4 `deep`

---

## TODOs

- [x] 1. Add `hermes` ProviderName enum + validation

  **What to do**:
  - In `internal/session/enums.go` add a `ProviderName` constant `ProviderHermes = "hermes"` next to the existing OpenCode/Claude/Cursor constants (around lines 13-29).
  - Add `"hermes"` to whatever validation/`IsValid()`/`AllProviders()` set governs ProviderName so it passes validation.
  - If a display-name or icon map exists for providers, add a `"Hermes"` label entry.

  **Must NOT do**:
  - Do NOT rename or reorder existing provider constants.
  - Do NOT touch the adapter logic yet (this is enum-only).

  **Recommended Agent Profile**:
  - **Category**: `quick` — single-file enum addition, no design judgment.
  - **Skills**: none — trivial Go edit.
  - **Skills Evaluated but Omitted**: `git-master` — commit handled at phase boundary, not needed mid-task.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with T2-T6)
  - **Blocks**: T11
  - **Blocked By**: None (can start immediately)

  **References**:
  - Pattern: `internal/session/enums.go:13-29` — existing ProviderName constants and validation; follow exact style.
  - WHY: The provider enum is closed; ingestion and factory registration will reject `"hermes"` until this constant + validation entry exist.

  **Acceptance Criteria**:
  - [ ] `go build ./...` succeeds.
  - [ ] `go test ./internal/session/...` passes.

  **QA Scenarios**:
  ```
  Scenario: hermes is a recognized provider
    Tool: Bash (go run harness)
    Preconditions: enum edited
    Steps:
      1. Write a tiny harness or use an existing test that calls the ProviderName validator with "hermes".
      2. Run: go test ./internal/session/ -run TestProviderName -v
    Expected Result: validation returns true/valid for "hermes"; tests PASS
    Failure Indicators: validator returns invalid, or build fails
    Evidence: .omo/evidence/task-1-enum-valid.txt

  Scenario: unknown provider still rejected
    Tool: Bash
    Preconditions: enum edited
    Steps:
      1. Validate "not-a-provider" via the same validator path.
    Expected Result: returns invalid (regression guard — we only added hermes)
    Evidence: .omo/evidence/task-1-enum-reject-error.txt
  ```

  **Commit**: YES (groups with H1) — `feat(session): add hermes provider name` — files `internal/session/enums.go` — pre-commit `go test ./internal/session/...`

- [x] 2. Hermes reader interface + `state.db` DB reader

  **What to do**:
  - Create `internal/provider/hermes/reader.go` defining a `Reader` interface mirroring `internal/provider/opencode/reader.go:4-40` (methods to list sessions, fetch a session, fetch its messages, find child sessions by `parent_session_id`).
  - Create `internal/provider/hermes/dbreader.go` implementing `Reader` against the Hermes SQLite DB at `~/.hermes/state.db` (respect `HERMES_HOME` env; default `~/.hermes/state.db`). Use targeted `SELECT`s with `json_extract`/column reads — NOT full-table scans.
  - Read columns from `sessions` (id, source, user_id, model, parent_session_id, started_at, ended_at, end_reason, message_count, tool_call_count, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, billing_provider, billing_base_url, billing_mode, estimated_cost_usd, actual_cost_usd, cost_status, title, api_call_count, handoff_state, handoff_error) and from `messages` (id, session_id, role, content, tool_call_id, tool_calls, tool_name, timestamp, token_count, finish_reason, reasoning, reasoning_content, platform_message_id, observed).
  - Open the DB read-only (`?mode=ro` / `_query_only=1`) and tolerate WAL + a held `compression_locks` row (do not block).

  **Must NOT do**:
  - Do NOT write to `state.db` (no INSERT/UPDATE/DELETE; read-only connection).
  - Do NOT read the FTS tables (`messages_fts*`) or telegram topic tables.
  - Do NOT do the domain mapping here — this layer returns raw Hermes rows/structs only.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — DB schema fidelity + read-only/locking correctness require care.
  - **Skills**: none required (Go + database/sql).
  - **Skills Evaluated but Omitted**: `librarian` — schema already documented in this plan/draft.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with T1,T3-T6)
  - **Blocks**: T7, T8, T13
  - **Blocked By**: None

  **References**:
  - Pattern: `internal/provider/opencode/reader.go:4-40` — interface shape (project/session/messages/parts) to mirror.
  - Pattern: `internal/provider/opencode/dbreader.go:19-203` — read-only sqlite connection setup, incremental reads, child lookup by ParentID.
  - Schema: `~/.hermes/state.db` schema v14 — `sessions` and `messages` columns listed in this plan's Context.
  - WHY: The reader is the seam that isolates Hermes's physical schema from AI5's domain; copying the OpenCode reader's structure keeps the adapter consistent and testable with a fixture DB.

  **Acceptance Criteria**:
  - [ ] `go build ./internal/provider/hermes/...` succeeds.
  - [ ] Reader opens a fixture DB read-only and returns sessions + messages.

  **QA Scenarios**:
  ```
  Scenario: reader lists sessions and messages from fixture DB
    Tool: Bash (go test against fixture from T3, or a temp DB if T3 not ready)
    Preconditions: a fixture state.db with >=2 sessions (one parent, one child) and messages
    Steps:
      1. go test ./internal/provider/hermes/ -run TestReader -v
      2. Assert sessions count == fixture count; child session ParentID matches parent id.
    Expected Result: tests PASS; child lineage preserved
    Failure Indicators: zero rows, panic on NULL columns, write attempt error
    Evidence: .omo/evidence/task-2-reader.txt

  Scenario: reader does not error when compression_locks row present
    Tool: Bash
    Preconditions: fixture DB with a row in compression_locks
    Steps:
      1. Open reader and list sessions.
    Expected Result: succeeds (read-only ignores lock)
    Evidence: .omo/evidence/task-2-reader-lock-error.txt
  ```

  **Commit**: YES (groups with H1) — `feat(provider): hermes state.db reader` — files `internal/provider/hermes/reader.go`, `internal/provider/hermes/dbreader.go` — pre-commit `go test ./internal/provider/hermes/...`

- [x] 3. Fixture `state.db` generator for tests

  **What to do**:
  - Create `internal/provider/hermes/testdata/` with a small Go helper (e.g. `fixture.go` build-tagged for tests, or a `_test.go` helper) that creates a temp Hermes-schema-v14 SQLite DB: the `sessions` and `messages` tables (and an empty `compression_locks`) with the exact column definitions from this plan.
  - Seed deterministic rows: a parent session, a child session (`parent_session_id` = parent), an assistant message containing a `delegate_task` tool call (in `tool_calls`/`tool_name`), one message with sentinel-prefixed JSON `content`, token + cost values, and a `source='cron'` session named `cron_job123_1700000000`.
  - Expose a `NewFixtureDB(t)` returning the DB path so adapter/reader/handler tests can use it without a live Hermes install.

  **Must NOT do**:
  - Do NOT depend on a real `~/.hermes/state.db` or network.
  - Do NOT include FTS or telegram tables (not needed).

  **Recommended Agent Profile**:
  - **Category**: `quick` — mechanical fixture creation from a known schema.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none relevant.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1
  - **Blocks**: T13, T20, T23
  - **Blocked By**: None

  **References**:
  - Pattern: `internal/storage/sqlite/notification_log_store_test.go` — how the repo builds temp sqlite DBs in tests.
  - Schema: Hermes `sessions`/`messages` column lists in this plan's Context.
  - WHY: Tests for the adapter, delegation, and H3 exchange view all need a deterministic Hermes DB; one shared fixture avoids duplicated setup and guarantees the delegation + sentinel + cron edge cases are always covered.

  **Acceptance Criteria**:
  - [ ] `go test ./internal/provider/hermes/...` can build the fixture and open it.

  **QA Scenarios**:
  ```
  Scenario: fixture builds and contains the seeded edge cases
    Tool: Bash
    Preconditions: fixture helper written
    Steps:
      1. go test ./internal/provider/hermes/ -run TestFixture -v
      2. sqlite3 on the temp path: assert 1 parent, 1 child, 1 delegate_task message, 1 sentinel-content message, 1 cron_* session.
    Expected Result: all seeded rows present; tests PASS
    Evidence: .omo/evidence/task-3-fixture.txt

  Scenario: fixture has no FTS/telegram tables
    Tool: Bash
    Steps:
      1. sqlite3 ".tables" on fixture path.
    Expected Result: only sessions, messages, compression_locks (no messages_fts, no telegram_*)
    Evidence: .omo/evidence/task-3-fixture-tables-error.txt
  ```

  **Commit**: YES (groups with H1) — `test(provider): hermes fixture db` — files `internal/provider/hermes/testdata/*`, fixture helper — pre-commit `go test ./internal/provider/hermes/...`

- [x] 4. CronJob domain types

  **What to do**:
  - Create `internal/session/cron_job.go` (or `internal/cron/` package — match where domain types live; OpenCode-style is `internal/session`) defining a `CronJob` struct mapping the Hermes `jobs.json` fields: `ID`, `Name`, `Prompt`, `Skills []string`, `Model`, `Provider`, `BaseURL`, `Schedule`, `ScheduleDisplay`, `Repeat`, `Enabled bool`, `State`, `PausedAt`, `PausedReason`, `CreatedAt`, `NextRunAt`, `LastRunAt`, `LastStatus`, `LastError`, `LastDeliveryError`, `Origin`, `Workdir`, `Profile`.
  - Add a `CronRun` (or reuse Session linkage) concept only if needed for run history — keep minimal; the run is just a Session with `source='cron'`.
  - Use pointer or zero-value-tolerant types for optional fields (timestamps may be absent).

  **Must NOT do**:
  - Do NOT add persistence here (that is T5/T10).
  - Do NOT model job↔session as a DB foreign key (Hermes has none; linkage is by ID convention — handled in T12).

  **Recommended Agent Profile**:
  - **Category**: `quick` — pure struct definition from a known JSON shape.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1
  - **Blocks**: T9
  - **Blocked By**: None

  **References**:
  - Pattern: `internal/session/notification_log.go` — how a domain type with JSON-ish fields is defined in this repo.
  - Schema: Hermes job fields listed in this plan's Context (`cron/jobs.py` job schema).
  - WHY: The parser (T9) and store (T10) both need a stable in-memory type; defining it first unblocks both in parallel.

  **Acceptance Criteria**:
  - [ ] `go build ./...` succeeds.

  **QA Scenarios**:
  ```
  Scenario: CronJob round-trips representative JSON
    Tool: Bash (go test)
    Preconditions: type defined with json tags
    Steps:
      1. Unmarshal a sample jobs.json job object into CronJob.
      2. Assert key fields (id, schedule, enabled, last_status) populated.
    Expected Result: tests PASS; no field dropped
    Evidence: .omo/evidence/task-4-cronjob.txt

  Scenario: missing optional fields tolerated
    Tool: Bash
    Steps:
      1. Unmarshal a job object lacking paused_at/last_error.
    Expected Result: zero values, no error
    Evidence: .omo/evidence/task-4-cronjob-optional-error.txt
  ```

  **Commit**: YES (groups with H2) — `feat(cron): cron job domain type` — files `internal/session/cron_job.go` — pre-commit `go test ./internal/session/...`

- [x] 5. Migration 038 cron table + store interface

  **What to do**:
  - Add migration 038 in `internal/storage/sqlite/sqlite.go` (follow the migration-036 pattern at `:3906`) creating a `cron_jobs` table: `job_id TEXT PRIMARY KEY`, `provider TEXT`, `name`, `prompt`, `schedule`, `schedule_display`, `repeat`, `enabled INTEGER`, `state`, `model`, `next_run_at REAL`, `last_run_at REAL`, `last_status`, `last_error`, `origin`, `workdir`, `profile`, `raw_json TEXT`, `updated_at REAL`. Provider column lets future agents share the table.
  - Add a `CronJobStore` interface to `internal/storage/store.go` (mirror `NotificationLogStore` at `:625-649`): `UpsertCronJob`, `ListCronJobs(provider)`, `GetCronJob(jobID)`.
  - Add the interface method set to `internal/testutil/mock_store.go`.

  **Must NOT do**:
  - Do NOT edit any existing migration (≤037). Append 038 only.
  - Do NOT implement the SQLite store body here (that is T10) — interface + migration + mock only.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — migration correctness + interface design across 3 files.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1
  - **Blocks**: T10
  - **Blocked By**: None

  **References**:
  - Pattern: `internal/storage/sqlite/sqlite.go:3906` (migration 036) — exact migration registration style + numbering.
  - Pattern: `internal/storage/store.go:625-649` (`NotificationLogStore`) — interface shape + naming.
  - Pattern: `internal/testutil/mock_store.go` — how new store methods are mocked.
  - WHY: Migration + interface are the contract T10 implements and T12/T15 consume; defining them first unblocks the store impl and handler in parallel.

  **Acceptance Criteria**:
  - [ ] `go build ./...` succeeds (mock satisfies interface).
  - [ ] Migration applies cleanly on a fresh DB; `cron_jobs` table exists.

  **QA Scenarios**:
  ```
  Scenario: migration 038 creates cron_jobs table
    Tool: Bash
    Preconditions: migration added
    Steps:
      1. Run the app's migrate path on a temp DB (existing migration test harness if present).
      2. sqlite3 temp.db ".schema cron_jobs"
    Expected Result: table exists with expected columns; migration version advances to 038
    Failure Indicators: duplicate migration number, table missing
    Evidence: .omo/evidence/task-5-migration.txt

  Scenario: mock store satisfies CronJobStore
    Tool: Bash
    Steps:
      1. go build ./... and go vet ./internal/testutil/...
    Expected Result: compiles (interface fully mocked)
    Evidence: .omo/evidence/task-5-mock-error.txt
  ```

  **Commit**: YES (groups with H2) — `feat(storage): cron_jobs migration 038 + store interface` — files `internal/storage/sqlite/sqlite.go`, `internal/storage/store.go`, `internal/testutil/mock_store.go` — pre-commit `go test ./internal/storage/...`

- [x] 6. Verify + add `delegate_task` to delegation detection; sentinel-safe scan

  **What to do**:
  - Inspect the hardcoded delegation tool-name list at `internal/service/session_ingest.go:13-23` and the detection logic at `:190-239`.
  - Add Hermes's delegation tool name (`delegate_task`) to the list IF absent. Confirm the exact name against Hermes `tools/delegate_tool.py` (the explore identified `delegate_task`).
  - Make the `session_id`/`sessionId`/`session-id` JSON scan sentinel-safe: if the tool input/output content carries a sentinel prefix (Hermes encodes structured/multimodal content with a sentinel before JSON), strip the prefix before `json.Unmarshal`/scan. Add a small helper `stripSentinel(content string) string`.

  **Must NOT do**:
  - Do NOT rename or remove existing tool-name entries (`delegate`, `ask_subagent`, `run_subagent`, `subagent`, `computer_use`).
  - Do NOT change the link types (`delegated_to`/`delegated_from`) or their semantics.

  **Recommended Agent Profile**:
  - **Category**: `quick` — additive list entry + a guarded string-strip helper.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: `librarian` — delegate tool name already identified; only confirm if uncertain.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1
  - **Blocks**: T14
  - **Blocked By**: None

  **References**:
  - Pattern: `internal/service/session_ingest.go:13-23` — hardcoded tool-name list.
  - Pattern: `internal/service/session_ingest.go:190-239` — delegation detection scanning tool input/output for session ids.
  - External: Hermes `tools/delegate_tool.py` (delegate_task) + `hermes_state.py:1576-1602` (sentinel content encoding) — confirm exact tool name + sentinel format.
  - WHY: Without `delegate_task` in the list and sentinel stripping, Hermes agent-to-agent delegations are silently NOT detected, so `/graph` and H3 would show no links for Hermes.

  **Acceptance Criteria**:
  - [ ] `go build ./...` succeeds; existing `session_ingest` tests still pass.

  **QA Scenarios**:
  ```
  Scenario: delegate_task triggers delegation detection
    Tool: Bash (go test)
    Preconditions: detection edited; unit input = tool call named delegate_task whose JSON references a child session_id
    Steps:
      1. go test ./internal/service/ -run TestDelegationDetect -v
      2. Assert a delegated_to link is produced for delegate_task.
    Expected Result: link created; PASS
    Evidence: .omo/evidence/task-6-delegate-task.txt

  Scenario: sentinel-prefixed tool content still parsed
    Tool: Bash
    Preconditions: tool content begins with the Hermes sentinel then JSON
    Steps:
      1. Run detection on sentinel-prefixed content referencing a session_id.
    Expected Result: session_id extracted; link created (no silent failure)
    Evidence: .omo/evidence/task-6-sentinel-error.txt

  Scenario: existing tool names unchanged (regression)
    Tool: Bash
    Steps:
      1. go test ./internal/service/ -run TestDelegation -v (full suite)
    Expected Result: delegate/ask_subagent/etc still detected; all PASS
    Evidence: .omo/evidence/task-6-regression.txt
  ```

  **Commit**: YES (groups with H1) — `feat(ingest): detect hermes delegate_task + sentinel-safe scan` — files `internal/service/session_ingest.go` — pre-commit `go test ./internal/service/...`

- [x] 7. Hermes adapter (Provider impl): session + token + cost mapping

  **What to do**:
  - Create `internal/provider/hermes/hermes.go` implementing the `provider.Provider` port (`internal/provider/provider.go:15-34`): `Detect()`, `Export()`/list, `Import()` (or whatever the port requires — mirror `internal/provider/opencode/opencode.go`).
  - In `Detect()`, return true when `~/.hermes/state.db` (or `$HERMES_HOME/state.db`) exists.
  - Map each Hermes `sessions` row (via T2 reader) to AI5's `session.Session`: set `Provider=ProviderHermes`, `Agent` (from source/model), `ParentID` from `parent_session_id`, timestamps, `Title`, and token/cost fields. Aggregate tokens (input/output/cache_read/cache_write/reasoning) into AI5's token model; map `estimated_cost_usd`/`actual_cost_usd`/`cost_status` into AI5's cost fields.
  - Populate `Children` lineage from reader child lookups so daily-consumption + lineage queries work.

  **Must NOT do**:
  - Do NOT map messages here (that is T8) — keep this commit focused on session/token/cost.
  - Do NOT invent fields AI5's domain lacks; if a Hermes field has no AI5 home, drop it or stash in an existing metadata field — do NOT alter the domain struct beyond minimal additive needs.

  **Recommended Agent Profile**:
  - **Category**: `deep` — faithful semantic mapping across two schemas + cost/token aggregation logic.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: `librarian` — both schemas already mapped in this plan.

  **Parallelization**:
  - **Can Run In Parallel**: NO (start of Wave 2; T8 depends on it)
  - **Parallel Group**: Wave 2 (alongside T9, T10 which are independent)
  - **Blocks**: T8, T11, T13
  - **Blocked By**: T2

  **References**:
  - Pattern: `internal/provider/opencode/opencode.go` — Provider impl shape (Detect/Export/Import) to mirror.
  - Port: `internal/provider/provider.go:15-34` — exact interface to satisfy.
  - Domain: `internal/session/session.go:115-132` (Message) and the surrounding `Session` struct — target fields for mapping.
  - Schema: Hermes `sessions` columns (this plan's Context).
  - WHY: This adapter is the core of H1; getting token/cost mapping right is what enables the "consommation d'un agent Hermes au fil des jours" the user asked for.

  **Acceptance Criteria**:
  - [ ] `go build ./internal/provider/hermes/...` succeeds.
  - [ ] `Detect()` true with fixture home, false without.
  - [ ] A fixture session maps to a `session.Session` with correct Provider/ParentID/tokens/cost.

  **QA Scenarios**:
  ```
  Scenario: session + tokens + cost map correctly
    Tool: Bash (go test against fixture from T3)
    Preconditions: fixture state.db
    Steps:
      1. go test ./internal/provider/hermes/ -run TestMapSession -v
      2. Assert mapped Session.Provider == "hermes", ParentID set on child, token totals == fixture sums, cost == fixture estimated/actual.
    Expected Result: PASS; values match fixture exactly
    Failure Indicators: zero tokens, wrong provider, lineage lost
    Evidence: .omo/evidence/task-7-map-session.txt

  Scenario: Detect false when no hermes home
    Tool: Bash
    Steps:
      1. Run Detect() with HERMES_HOME pointing at empty temp dir.
    Expected Result: false (no spurious detection)
    Evidence: .omo/evidence/task-7-detect-error.txt
  ```

  **Commit**: YES (groups with H1) — `feat(provider): hermes session/token/cost mapping` — files `internal/provider/hermes/hermes.go` — pre-commit `go test ./internal/provider/hermes/...`

- [x] 8. Hermes message / tool-call / reasoning / content (sentinel) mapping

  **What to do**:
  - Extend the Hermes adapter to map `messages` rows into AI5 `session.Message` (`internal/session/session.go:115-132`): `Role`, `Content`, `ToolCalls`, `ToolName`, `Thinking`/reasoning (from `reasoning`/`reasoning_content`), `FinishReason`, `TokenCount`, timestamps, ordering.
  - Decode Hermes sentinel-prefixed `content`: Hermes encodes structured/multimodal content as `<sentinel>` + JSON. Reuse/extract the `stripSentinel` helper from T6 (or a shared `internal/provider/hermes/sentinel.go`) to decode to text/content-blocks before populating `Message.Content`/`ContentBlocks`.
  - Parse `tool_calls` JSON into AI5's tool-call representation; carry `tool_call_id`/`tool_name`.
  - Store mapped messages into the session payload exactly as AI5 expects (the payload BLOB is built by the existing capture/store path — feed messages through the same domain types so compression/storage is unchanged).

  **Must NOT do**:
  - Do NOT create a new messages DB table (AI5 stores messages in the compressed payload BLOB — `internal/storage/sqlite/sqlite.go:201-252`).
  - Do NOT drop `ToolCalls`/`Thinking` — H3 depends on full message fidelity (Metis gap).

  **Recommended Agent Profile**:
  - **Category**: `deep` — sentinel decoding + tool-call JSON + content-block fidelity is the trickiest mapping.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: `librarian` — sentinel format identified (`hermes_state.py:1576-1602`); confirm only if decode fails.

  **Parallelization**:
  - **Can Run In Parallel**: NO (depends on T7 adapter skeleton)
  - **Parallel Group**: Wave 2
  - **Blocks**: T11, T13, T17
  - **Blocked By**: T2, T7

  **References**:
  - Pattern: `internal/provider/opencode/dbreader.go:19-203` — how OpenCode maps messages/parts/tool-calls into domain messages.
  - Domain: `internal/session/session.go:115-132` (Message struct: Role/Content/ToolCalls/Thinking/etc).
  - External: Hermes `hermes_state.py:1576-1602` (sentinel content encoding) + `trajectory-format.md` (message/tool_calls shape).
  - WHY: H3 (the inter-agent exchange view the user explicitly wants) renders these messages; any field dropped here is invisible downstream. Sentinel decoding is the single highest-risk correctness item.

  **Acceptance Criteria**:
  - [ ] Fixture messages map with Role/Content/ToolCalls/Thinking intact.
  - [ ] Sentinel-prefixed content decodes to readable text/blocks (no raw sentinel leaks).

  **QA Scenarios**:
  ```
  Scenario: messages incl. tool calls + reasoning map fully
    Tool: Bash (go test, fixture from T3)
    Preconditions: fixture with assistant delegate_task tool call + a reasoning message
    Steps:
      1. go test ./internal/provider/hermes/ -run TestMapMessages -v
      2. Assert message count matches; the delegate_task ToolCalls present; Thinking populated for the reasoning message; order preserved.
    Expected Result: PASS; no field dropped
    Evidence: .omo/evidence/task-8-map-messages.txt

  Scenario: sentinel content decoded (no raw sentinel)
    Tool: Bash
    Preconditions: fixture message with sentinel-prefixed JSON content
    Steps:
      1. Map it; inspect resulting Content/ContentBlocks.
    Expected Result: decoded text/blocks; the sentinel marker is NOT present in output
    Failure Indicators: raw sentinel string leaks into Content; JSON parse error
    Evidence: .omo/evidence/task-8-sentinel-error.txt
  ```

  **Commit**: YES (groups with H1) — `feat(provider): hermes message/toolcall/sentinel mapping` — files `internal/provider/hermes/hermes.go`, `internal/provider/hermes/sentinel.go` — pre-commit `go test ./internal/provider/hermes/...`

- [x] 9. Hermes cron `jobs.json` parser

  **What to do**:
  - Create `internal/provider/hermes/cron.go` (or `internal/cron/hermes.go` matching domain layout) with a `ParseCronJobs(path string) ([]session.CronJob, error)` that reads `$HERMES_HOME/cron/jobs.json` (default `~/.hermes/cron/jobs.json`), unmarshals `{jobs:[...], updated_at}`, and returns `CronJob` values (T4 type).
  - Tolerate a missing file (return empty slice, no error) and malformed entries (skip + log, do not abort the batch).
  - Normalize timestamps (epoch float vs RFC3339) into the `CronJob` time fields.

  **Must NOT do**:
  - Do NOT read or write Hermes SQLite for cron (cron lives only in JSON).
  - Do NOT execute or schedule jobs — parse/observe only.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — JSON tolerance + timestamp normalization edge cases.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 2 (independent of T7/T8)
  - **Blocks**: T12
  - **Blocked By**: T4

  **References**:
  - Type: `internal/session/cron_job.go` (T4) — target struct.
  - External: Hermes `cron/jobs.py` + sample `~/.hermes/cron/jobs.json` shape (`{jobs:[...],updated_at}`).
  - WHY: This parser is H2's ingestion seam; the store (T10) and linking (T12) consume its output. Tolerance matters because a partially-written jobs.json must not crash capture.

  **Acceptance Criteria**:
  - [ ] Parses a fixture jobs.json into `[]CronJob` with all fields.
  - [ ] Missing file → empty slice, nil error.

  **QA Scenarios**:
  ```
  Scenario: parse fixture jobs.json
    Tool: Bash (go test with a testdata/jobs.json)
    Preconditions: fixture jobs.json with 2 jobs (1 enabled, 1 paused)
    Steps:
      1. go test ./internal/provider/hermes/ -run TestParseCron -v
      2. Assert 2 jobs; enabled flags + last_status mapped; timestamps normalized.
    Expected Result: PASS
    Evidence: .omo/evidence/task-9-parse-cron.txt

  Scenario: missing/malformed file handled
    Tool: Bash
    Steps:
      1. ParseCronJobs("/tmp/does-not-exist.json") → expect empty, nil err.
      2. ParseCronJobs on a truncated JSON → expect skip/partial, no panic.
    Expected Result: graceful; no crash
    Evidence: .omo/evidence/task-9-parse-cron-error.txt
  ```

  **Commit**: YES (groups with H2) — `feat(cron): parse hermes jobs.json` — files `internal/provider/hermes/cron.go` — pre-commit `go test ./internal/provider/hermes/...`

- [x] 10. Cron store SQLite implementation + tests

  **What to do**:
  - Implement the `CronJobStore` interface (T5) in `internal/storage/sqlite/cron_job_store.go` (mirror `internal/storage/sqlite/notification_log_store.go` if present, else the nearest store impl): `UpsertCronJob`, `ListCronJobs(provider)`, `GetCronJob(jobID)`.
  - `UpsertCronJob` is idempotent on `job_id` (INSERT … ON CONFLICT(job_id) DO UPDATE) and stores `raw_json` for round-trip fidelity.
  - Add `internal/storage/sqlite/cron_job_store_test.go` with table-driven tests against a temp DB (upsert→list→get, update path, provider filter).

  **Must NOT do**:
  - Do NOT change the `CronJobStore` interface signature (defined in T5) — implement it as-is.
  - Do NOT couple the store to Hermes specifics — `provider` column keeps it source-agnostic.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — store correctness + idempotent upsert + tests.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: YES (independent of T7/T8/T9 bodies)
  - **Parallel Group**: Wave 2
  - **Blocks**: T12
  - **Blocked By**: T5

  **References**:
  - Pattern: `internal/storage/sqlite/notification_log_store.go` (+ its `_test.go`) — store impl + test layout to mirror.
  - Interface: `internal/storage/store.go` `CronJobStore` (added in T5).
  - WHY: Persisting cron jobs lets `/cron` (T15) render from the DB rather than re-parsing JSON on every request, and gives T12 a place to correlate runs.

  **Acceptance Criteria**:
  - [ ] `go test ./internal/storage/sqlite/ -run TestCronJobStore` passes.
  - [ ] Upsert is idempotent; provider filter works.

  **QA Scenarios**:
  ```
  Scenario: upsert → list → get round-trip
    Tool: Bash
    Preconditions: migration 038 applied on temp DB
    Steps:
      1. go test ./internal/storage/sqlite/ -run TestCronJobStore -v
      2. Assert upsert twice on same job_id yields 1 row with updated fields; ListCronJobs("hermes") returns it; GetCronJob returns raw_json.
    Expected Result: PASS
    Evidence: .omo/evidence/task-10-cron-store.txt

  Scenario: provider filter isolates rows
    Tool: Bash
    Steps:
      1. Insert one "hermes" + one "other" job; ListCronJobs("hermes").
    Expected Result: only the hermes job returned
    Evidence: .omo/evidence/task-10-cron-store-filter-error.txt
  ```

  **Commit**: YES (groups with H2) — `feat(storage): cron_jobs sqlite store` — files `internal/storage/sqlite/cron_job_store.go`, `internal/storage/sqlite/cron_job_store_test.go` — pre-commit `go test ./internal/storage/sqlite/...`

- [x] 11. Register `hermes` in factory + import/capture discovery

  **What to do**:
  - Add the Hermes provider to the hardcoded provider construction at `pkg/cmd/factory/default.go:236-243` (append a `hermes.New(...)` next to opencode/claude/cursor/parlay/ollama — follow the exact existing call style).
  - Ensure the provider participates in auto-detection/discovery so `capture` (and any "detect all providers" path) includes Hermes.
  - Wire any provider-name→constructor map so `--provider hermes` resolves (T22 builds on this).

  **Must NOT do**:
  - Do NOT reorder or remove existing provider registrations.
  - Do NOT add CLI flags here (T22 owns the `--provider hermes` UX surface if any new flag is needed).

  **Recommended Agent Profile**:
  - **Category**: `quick` — additive wiring following an obvious pattern.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: NO (needs T7/T8 adapter to exist)
  - **Parallel Group**: Wave 3 (alongside T12, T13, T14)
  - **Blocks**: T22, T23
  - **Blocked By**: T7, T8

  **References**:
  - Pattern: `pkg/cmd/factory/default.go:236-243` — provider construction list (opencode/claude/cursor/parlay/ollama).
  - Enum: `internal/session/enums.go` `ProviderHermes` (T1).
  - WHY: The adapter is inert until the factory constructs it; this is the switch that makes Hermes show up in capture/discovery.

  **Acceptance Criteria**:
  - [ ] `go build ./...` succeeds.
  - [ ] Factory returns a provider set that includes hermes.

  **QA Scenarios**:
  ```
  Scenario: factory includes hermes provider
    Tool: Bash (go test on factory if test exists, else go run harness)
    Steps:
      1. Construct providers via factory; assert one reports name "hermes".
    Expected Result: hermes present; others unchanged (count = previous + 1)
    Evidence: .omo/evidence/task-11-factory.txt

  Scenario: existing providers still registered (regression)
    Tool: Bash
    Steps:
      1. Assert opencode/claude/cursor/parlay/ollama all still present.
    Expected Result: all present
    Evidence: .omo/evidence/task-11-factory-regression-error.txt
  ```

  **Commit**: YES (groups with H1) — `feat(factory): register hermes provider` — files `pkg/cmd/factory/default.go` — pre-commit `go build ./... && go test ./pkg/cmd/factory/...`

- [x] 12. Cron run ↔ job linking by `cron_{job_id}_*` convention

  **What to do**:
  - During/after ingestion, correlate Hermes sessions whose id matches `cron_{job_id}_{timestamp}` (source='cron') to the corresponding `CronJob.ID`. Implement a helper `LinkCronRuns(jobs []CronJob, sessions []Session)` (place in the cron package) that parses the `job_id` segment out of the session id and tags/returns the association.
  - Persist the latest run correlation by updating `last_run_at`/`last_status` on the stored CronJob if the session carries fresher info (optional, gated on availability).
  - Expose the association so T15's `/cron` view can show, per job, its recent runs (sessions) and link to their detail pages.

  **Must NOT do**:
  - Do NOT add a DB foreign key between sessions and cron_jobs (Hermes has none; linkage is by id-naming convention only).
  - Do NOT fabricate a job for an orphan `cron_*` session whose job_id is absent from jobs.json — surface it as orphan, do not crash.

  **Recommended Agent Profile**:
  - **Category**: `deep` — convention parsing + orphan handling + correlation correctness.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: NO (needs T9 parser + T10 store)
  - **Parallel Group**: Wave 3
  - **Blocks**: T15, T23
  - **Blocked By**: T9, T10

  **References**:
  - Convention: session id `cron_{job_id}_{timestamp}` (this plan's Context — Hermes assigns this to cron-triggered runs).
  - Types: `CronJob` (T4), parser (T9), store (T10).
  - WHY: This is what turns "a scheduled task" into "the tasks an agent triggered over time" — directly answering the user's "les tâches qu'il trigger" requirement.

  **Acceptance Criteria**:
  - [ ] Given fixture jobs + a `cron_job123_*` session, the run links to job `job123`.
  - [ ] An orphan `cron_unknown_*` session is reported as orphan (no panic).

  **QA Scenarios**:
  ```
  Scenario: cron run links to its job
    Tool: Bash (go test, fixtures from T3 + T9)
    Steps:
      1. go test ./... -run TestLinkCronRuns -v
      2. Assert the cron_job123_1700000000 session associates to CronJob "job123".
    Expected Result: PASS; correct association
    Evidence: .omo/evidence/task-12-link.txt

  Scenario: orphan cron session handled
    Tool: Bash
    Steps:
      1. Provide a cron_ghost_* session with no matching job.
    Expected Result: returned as orphan; no error/panic
    Evidence: .omo/evidence/task-12-orphan-error.txt
  ```

  **Commit**: YES (groups with H2) — `feat(cron): link hermes cron runs to jobs` — files cron package + tests — pre-commit `go test ./...`

- [x] 13. H1 adapter fixture tests (sessions / tokens / messages / tool-calls)

  **What to do**:
  - Add `internal/provider/hermes/hermes_test.go` table-driven tests using the T3 fixture DB end-to-end through the adapter: list → map sessions → map messages.
  - Assert: session count, provider name, parent/child lineage, token totals, cost fields, message count, tool-call presence (`delegate_task`), reasoning/thinking populated, sentinel content decoded.
  - Cover NULL-tolerant columns (a session with no cost, a message with no reasoning).

  **Must NOT do**:
  - Do NOT require a live `~/.hermes` — fixture only.
  - Do NOT duplicate the unit assertions already in T7/T8; this is the integrated adapter-level pass.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — comprehensive table-driven coverage.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: YES (alongside T11, T12, T14)
  - **Parallel Group**: Wave 3
  - **Blocks**: T14
  - **Blocked By**: T7, T8, T3

  **References**:
  - Pattern: `internal/provider/opencode/*_test.go` — adapter test structure.
  - Fixture: T3 `NewFixtureDB`.
  - WHY: This is the H1 safety net; it locks the session/token/message mapping so later UI work (H3) builds on verified data.

  **Acceptance Criteria**:
  - [ ] `go test ./internal/provider/hermes/...` passes with full assertions.

  **QA Scenarios**:
  ```
  Scenario: end-to-end adapter pass over fixture
    Tool: Bash
    Steps:
      1. go test ./internal/provider/hermes/ -run TestAdapter -v -count=1
      2. Assert all mapping assertions (sessions/tokens/messages/toolcalls/sentinel/lineage).
    Expected Result: PASS, 0 failures
    Evidence: .omo/evidence/task-13-adapter-tests.txt

  Scenario: NULL-tolerant columns
    Tool: Bash
    Steps:
      1. Fixture row with NULL cost + message with NULL reasoning mapped.
    Expected Result: zero-values, no panic
    Evidence: .omo/evidence/task-13-null-error.txt
  ```

  **Commit**: YES (groups with H1) — `test(provider): hermes adapter fixture tests` — files `internal/provider/hermes/hermes_test.go` — pre-commit `go test ./internal/provider/hermes/...`

- [x] 14. Hermes delegation detection tests

  **What to do**:
  - Add tests (extend `internal/service/session_ingest_test.go` or a new `*_test.go`) feeding a Hermes-shaped assistant message with a `delegate_task` tool call referencing a child session id, asserting a `delegated_to` `SessionLink` is produced.
  - Include a sentinel-prefixed tool-content case (verifies T6 `stripSentinel`).
  - Include a regression case proving existing tool names (`delegate`, `ask_subagent`, etc.) still produce links.

  **Must NOT do**:
  - Do NOT modify detection logic here (that was T6) — tests only.
  - Do NOT assert on OpenCode-specific stall behavior (out of scope).

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — careful fixture construction for delegation edge cases.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: YES (alongside T11-T13)
  - **Parallel Group**: Wave 3
  - **Blocks**: T21
  - **Blocked By**: T6, T13

  **References**:
  - Pattern: `internal/service/session_ingest.go:190-239` (detection) + existing `session_ingest_test.go` cases.
  - Fixture: T3 seeds a `delegate_task` message; reuse it.
  - WHY: Delegation is the user's "interactions entre agents"; without tests this silently regresses and `/graph` + H3 lose Hermes links.

  **Acceptance Criteria**:
  - [ ] `go test ./internal/service/ -run TestDelegation` passes including the new Hermes cases.

  **QA Scenarios**:
  ```
  Scenario: hermes delegate_task → delegated_to link
    Tool: Bash
    Steps:
      1. go test ./internal/service/ -run TestDelegation -v -count=1
      2. Assert a delegated_to link for the delegate_task message; sentinel case passes; legacy names still link.
    Expected Result: PASS
    Evidence: .omo/evidence/task-14-delegation-tests.txt

  Scenario: no link when no child session referenced
    Tool: Bash
    Steps:
      1. delegate_task tool call with no resolvable session id.
    Expected Result: no link, no error (graceful)
    Evidence: .omo/evidence/task-14-nolink-error.txt
  ```

  **Commit**: YES (groups with H1) — `test(ingest): hermes delegation detection` — files `internal/service/session_ingest_test.go` — pre-commit `go test ./internal/service/...`

- [x] 15. Cron tasks web handler + route + nav

  **What to do**:
  - Add a `/cron` handler in `internal/web/handlers.go` (mirror an existing list handler like `handleSessions`/the costs page). It reads `CronJobStore.ListCronJobs("hermes")` (extensible to all providers later) and, per job, its recent runs via T12 linking, then renders the T16 template.
  - Register the route in the router and add a `/cron` nav link in the layout/header alongside existing nav entries.
  - Support an optional `?provider=` query to filter; default to all.

  **Must NOT do**:
  - Do NOT re-parse `jobs.json` in the handler — read from the store (capture path populates it).
  - Do NOT add JS — server-rendered only (CSS-only requirement).

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — handler + routing + data assembly (runs per job).
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: `frontend-ui-ux` — template visuals are T16's job.

  **Parallelization**:
  - **Can Run In Parallel**: NO (needs T12 linking)
  - **Parallel Group**: Wave 4 (alongside T17/T18)
  - **Blocks**: T16
  - **Blocked By**: T12

  **References**:
  - Pattern: `internal/web/handlers.go:1601-2104` (handleSessionDetail) + the existing costs/sessions list handlers — handler shape, template execution, router registration.
  - Store: `CronJobStore` (T5/T10); linking (T12).
  - WHY: This is H2's surface — "voir les tâches qu'un agent trigger" needs a page; the handler assembles job + run history for the view.

  **Acceptance Criteria**:
  - [ ] `GET /cron` returns 200 and lists fixture jobs with last_status/next_run.
  - [ ] Nav shows a `/cron` link.

  **QA Scenarios**:
  ```
  Scenario: /cron renders jobs with runs
    Tool: Bash (curl against daemon on :5100 with fixture home)
    Steps:
      1. curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:5100/cron  → 200
      2. curl -s http://127.0.0.1:5100/cron | grep -c "job123"  → >=1
    Expected Result: 200; job rows present with status/next-run
    Evidence: .omo/evidence/task-15-cron-handler.txt

  Scenario: empty store renders empty-state (no 500)
    Tool: Bash
    Steps:
      1. curl /cron with no cron jobs ingested.
    Expected Result: 200 with empty-state message, not an error
    Evidence: .omo/evidence/task-15-cron-empty-error.txt
  ```

  **Commit**: YES (groups with H2) — `feat(web): cron tasks handler + route` — files `internal/web/handlers.go`, router/nav — pre-commit `go test ./internal/web/...`

- [x] 16. Cron tasks template (CSS-only)

  **What to do**:
  - Add a server-rendered template (in the existing templates dir used by `internal/web`) rendering the cron jobs: a table with columns Name, Schedule (display), Enabled/State, Last Status, Last Run, Next Run, and an expandable/linked list of recent runs (each linking to `/sessions/{id}` and `/sessions/{id}/exchanges`).
  - Append any needed styles to `style.css` using existing CSS variables only; match the dark-theme dashboard. Provide a clear empty-state and a paused/error visual treatment (status pill via existing color vars).

  **Must NOT do**:
  - Do NOT introduce a JS framework or inline scripts; use CSS-only (e.g. `<details>` for expand).
  - Do NOT add new CSS variables or hardcoded colors; reuse existing tokens. Append-only to `style.css` (do not rewrite existing rules).

  **Recommended Agent Profile**:
  - **Category**: `visual-engineering` — UI rendering + dark-theme consistency + status affordances.
  - **Skills**: `frontend-ui-ux` — table/empty-state/status-pill design judgment within an existing design system.
  - **Skills Evaluated but Omitted**: `playwright` — verification (T15/F3) handles browser checks, not authoring.

  **Parallelization**:
  - **Can Run In Parallel**: NO (needs T15 handler/data)
  - **Parallel Group**: Wave 4
  - **Blocks**: none (final)
  - **Blocked By**: T15

  **References**:
  - Pattern: existing dashboard templates (sessions/costs pages) + `style.css` token set + the layout/header partial.
  - WHY: Visual consistency is a hard requirement; reusing tokens + table patterns keeps `/cron` indistinguishable in style from existing pages.

  **Acceptance Criteria**:
  - [ ] `/cron` renders a styled table matching the dark theme; empty-state present; status pills use existing vars.

  **QA Scenarios**:
  ```
  Scenario: /cron visually correct (browser)
    Tool: Playwright
    Preconditions: daemon :5100 with fixture cron jobs
    Steps:
      1. Navigate http://127.0.0.1:5100/cron
      2. Assert a table with header cells "Name","Schedule","Last Status","Next Run".
      3. Assert at least one row contains job123 and a status pill element.
      4. Screenshot.
    Expected Result: styled table, dark theme, pill rendered
    Evidence: .omo/evidence/task-16-cron-template.png

  Scenario: empty-state renders
    Tool: Playwright
    Steps:
      1. Navigate /cron with no jobs; assert empty-state text visible, no broken layout.
    Expected Result: graceful empty-state
    Evidence: .omo/evidence/task-16-cron-empty-error.png
  ```

  **Commit**: YES (groups with H2) — `feat(web): cron tasks template + styles` — files cron template, `style.css` (append) — pre-commit `go test ./internal/web/...`

- [x] 17. H3 exchange handler `/sessions/{id}/exchanges` (merge + cap)

  **What to do**:
  - Add a `handleSessionExchanges` handler in `internal/web/handlers.go` (mirror `handleSessionDetail` at `:1601-2104`). Load the parent session, resolve its children (lineage `Children`/`SessionLink delegated_to`), decompress each session's payload BLOB, and build a single merged, time-ordered timeline of messages tagged by which agent/session they belong to.
  - Cap children rendered initially (Metis perf gap): load at most 10 child sessions inline; the rest are fetched via the T18 load-more partial. Decompress lazily to avoid N+1 cost on large lineages.
  - Each timeline entry carries: agent/session label, role, content (sentinel-decoded via T8 mapping), tool-call summary, timestamp, and a `delegated_to/from` marker where a message triggered a delegation.

  **Must NOT do**:
  - Do NOT add a DB table or migration (read-time projection over existing payload BLOBs — Metis decision).
  - Do NOT load ALL descendants unbounded; respect the 10-child cap + load-more.
  - Do NOT mutate sessions; this is read-only rendering.

  **Recommended Agent Profile**:
  - **Category**: `deep` — multi-session merge ordering, payload decompression, lineage traversal, perf cap.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: `frontend-ui-ux` — visual layer is T19.

  **Parallelization**:
  - **Can Run In Parallel**: NO (needs T8 message mapping fidelity)
  - **Parallel Group**: Wave 4 (alongside T15/T16)
  - **Blocks**: T18, T19, T20, T23
  - **Blocked By**: T8

  **References**:
  - Pattern: `internal/web/handlers.go:1601-2104` (handleSessionDetail — payload decompress + message render) and `:2107-2167` (messagesMore — pagination pattern to reuse for child load-more).
  - Domain: `Session.Children`, `SessionLink` (`internal/session/enums.go:314-324`), `Message` (`session.go:115-132`).
  - Storage: payload BLOB decompress path (`internal/storage/sqlite/sqlite.go:289-347`).
  - WHY: THIS is the feature the user said is missing ("pas d'interface pour voir les échanges entre les agents"). The merge+tag logic is the substance of the inter-agent exchange view.

  **Acceptance Criteria**:
  - [ ] `GET /sessions/{id}/exchanges` returns 200 with a merged parent+children timeline.
  - [ ] With >10 children, only 10 render inline; a load-more affordance is present.

  **QA Scenarios**:
  ```
  Scenario: merged parent+child timeline, correct order + tagging
    Tool: Bash (curl, fixture-ingested parent with 1 child)
    Steps:
      1. curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:5100/sessions/PARENT/exchanges → 200
      2. curl -s .../exchanges | assert messages from BOTH parent and child appear, in timestamp order, each tagged with its agent/session.
    Expected Result: 200; interleaved, tagged, ordered
    Failure Indicators: only parent messages; wrong order; raw sentinel leaks
    Evidence: .omo/evidence/task-17-exchanges.txt

  Scenario: child cap + missing-child resilience
    Tool: Bash
    Steps:
      1. Parent referencing a child id that has no payload → handler skips gracefully (no 500).
      2. Parent with >10 children → assert load-more marker present, only 10 inline.
    Expected Result: graceful skip; cap enforced
    Evidence: .omo/evidence/task-17-exchanges-edge-error.txt
  ```

  **Commit**: YES (groups with H3) — `feat(web): inter-agent exchange handler` — files `internal/web/handlers.go`, router — pre-commit `go test ./internal/web/...`

- [x] 18. H3 load-more partial `/partials/session-exchanges/{id}`

  **What to do**:
  - Add a partial handler (mirror `messagesMore` at `internal/web/handlers.go:2107-2167`) that returns the next batch of child-session timelines (HTML fragment) given an offset/cursor, so the exchange page can lazily append children beyond the initial 10.
  - Reuse the merge/tag logic from T17 (extract a shared `buildExchangeTimeline` helper so handler + partial share one implementation).
  - Return only the fragment markup (no full layout) consistent with the existing HTMX-style partial pattern in the repo.

  **Must NOT do**:
  - Do NOT duplicate the merge logic — share T17's helper.
  - Do NOT introduce a JS framework; follow the existing server-fragment/HTMX approach already used by `messagesMore`.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — pagination correctness + shared-helper extraction.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: NO (needs T17 helper)
  - **Parallel Group**: Wave 4
  - **Blocks**: T20
  - **Blocked By**: T17

  **References**:
  - Pattern: `internal/web/handlers.go:2107-2167` (messagesMore — fragment pagination) — exact cursor/offset + fragment-return style.
  - WHY: Without load-more, large delegation trees either over-render (perf) or hide data; this partial keeps the exchange view bounded yet complete.

  **Acceptance Criteria**:
  - [ ] `GET /partials/session-exchanges/{id}?offset=10` returns the next child batch as an HTML fragment.

  **QA Scenarios**:
  ```
  Scenario: load-more returns next child batch
    Tool: Bash (curl, parent with >10 children)
    Steps:
      1. curl -s "http://127.0.0.1:5100/partials/session-exchanges/PARENT?offset=10" → fragment with child #11+
      2. Assert it is a fragment (no <html>/<head> wrapper) and contains the next child's messages.
    Expected Result: correct next batch fragment
    Evidence: .omo/evidence/task-18-loadmore.txt

  Scenario: offset past end returns empty fragment
    Tool: Bash
    Steps:
      1. curl with offset beyond child count.
    Expected Result: empty/terminal fragment, 200, no error
    Evidence: .omo/evidence/task-18-loadmore-end-error.txt
  ```

  **Commit**: YES (groups with H3) — `feat(web): exchange load-more partial` — files `internal/web/handlers.go` — pre-commit `go test ./internal/web/...`

- [x] 19. H3 exchange template + link from session detail

  **What to do**:
  - Add a template rendering the merged timeline: a vertical conversation, each entry visually attributed to its agent/session (distinct lane/label/color via existing CSS vars), showing role, decoded content, tool-call chips, timestamp, and a delegation marker arrow where a message spawned a child.
  - Add a "View exchanges" link on the existing session detail page (`handleSessionDetail` template) pointing to `/sessions/{id}/exchanges`.
  - Wire the T18 load-more fragment append (server-fragment/HTMX attribute, no custom JS framework). Append styles to `style.css` using existing tokens only.

  **Must NOT do**:
  - Do NOT add a JS framework or new CSS variables/hardcoded colors; CSS-only with existing tokens, append-only to `style.css`.
  - Do NOT render raw sentinel markers or raw JSON tool-call blobs — show decoded/summarized content.

  **Recommended Agent Profile**:
  - **Category**: `visual-engineering` — multi-agent conversation layout + attribution clarity + delegation affordances.
  - **Skills**: `frontend-ui-ux` — design judgment for distinguishing agents in one timeline within an existing dark theme.
  - **Skills Evaluated but Omitted**: `playwright` — used for QA (F3), not authoring.

  **Parallelization**:
  - **Can Run In Parallel**: NO (needs T17 data)
  - **Parallel Group**: Wave 4
  - **Blocks**: none (final)
  - **Blocked By**: T17

  **References**:
  - Pattern: session detail template (rendered by `handleSessionDetail`) for message rendering + `style.css` tokens + existing HTMX partial usage.
  - WHY: This is the visible deliverable the user asked for; clear per-agent attribution in one timeline is what makes inter-agent exchanges legible (vs `/graph` which only shows topology).

  **Acceptance Criteria**:
  - [ ] Exchange page renders attributed, ordered messages; "View exchanges" link present on session detail; load-more appends children.

  **QA Scenarios**:
  ```
  Scenario: exchange view renders attributed multi-agent timeline
    Tool: Playwright
    Preconditions: daemon :5100, fixture parent+child ingested
    Steps:
      1. Navigate /sessions/PARENT (detail); click "View exchanges".
      2. Assert URL is /sessions/PARENT/exchanges.
      3. Assert messages from parent AND child are visible, each with a distinct agent label/lane.
      4. Assert a delegation marker exists where parent delegated. Screenshot.
    Expected Result: attributed, ordered, styled timeline
    Evidence: .omo/evidence/task-19-exchanges-view.png

  Scenario: load-more appends extra children in-browser
    Tool: Playwright
    Preconditions: parent with >10 children
    Steps:
      1. Open exchanges; click the load-more control; assert child #11 appears.
    Expected Result: additional children appended without full reload
    Evidence: .omo/evidence/task-19-loadmore-error.png
  ```

  **Commit**: YES (groups with H3) — `feat(web): exchange timeline template + detail link` — files exchange template, session-detail template, `style.css` (append) — pre-commit `go test ./internal/web/...`

- [x] 20. H3 handler tests (merge ordering, empty, missing child, sentinel)

  **What to do**:
  - Add `internal/web/*_test.go` tests for `handleSessionExchanges` + the load-more partial using `httptest` and the T3 fixture-ingested store: assert merged timeline ordering, per-agent tagging, the >10-child cap + load-more cursor, an empty/childless session, a missing-child-payload skip, and sentinel-decoded content (no raw sentinel in output).
  - Reuse existing web handler test scaffolding (router + in-memory/mock store + fixture sessions).

  **Must NOT do**:
  - Do NOT spin up a real Hermes — use ingested fixture sessions in the store.
  - Do NOT assert pixel/visual specifics (that is F3/Playwright) — assert HTML structure + data presence.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — handler test coverage across ordering + edge cases.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: YES (alongside T21/T22)
  - **Parallel Group**: Wave 5
  - **Blocks**: none (final)
  - **Blocked By**: T17, T18

  **References**:
  - Pattern: existing `internal/web/*_test.go` handler tests (httptest router + store setup).
  - WHY: H3 is logic-heavy (merge/cap/skip/decode); these tests lock correctness so refactors or future providers do not break the exchange view.

  **Acceptance Criteria**:
  - [ ] `go test ./internal/web/ -run TestExchanges` passes covering all listed cases.

  **QA Scenarios**:
  ```
  Scenario: exchange handler tests pass
    Tool: Bash
    Steps:
      1. go test ./internal/web/ -run TestExchanges -v -count=1
      2. Assert ordering/tagging/cap/empty/missing-child/sentinel cases all PASS.
    Expected Result: PASS, 0 failures
    Evidence: .omo/evidence/task-20-exchange-tests.txt

  Scenario: missing-child case is covered (regression guard)
    Tool: Bash
    Steps:
      1. Confirm a test exists that references a child id with no payload and asserts no 500.
    Expected Result: case present + green
    Evidence: .omo/evidence/task-20-missing-child-error.txt
  ```

  **Commit**: YES (groups with H3) — `test(web): exchange handler tests` — files `internal/web/*_test.go` — pre-commit `go test ./internal/web/...`

- [x] 21. `/graph` badge enrichment for Hermes delegations

  **What to do**:
  - In the existing `/graph` view, ensure Hermes delegation edges (produced via T6/T14 `delegated_to` links) render with provider attribution and link each edge/node to the new `/sessions/{id}/exchanges` view so users can jump from topology to actual content.
  - Add a small provider badge/affordance (Hermes) on nodes, reusing existing graph styling + CSS vars. Append-only to `style.css`.

  **Must NOT do**:
  - Do NOT redesign the graph or add a JS graph library — enhance the existing rendering only.
  - Do NOT add new CSS variables/hardcoded colors; reuse tokens.

  **Recommended Agent Profile**:
  - **Category**: `visual-engineering` — graph affordance + cross-link UX within existing rendering.
  - **Skills**: `frontend-ui-ux` — badge/edge affordance design consistent with the existing graph.
  - **Skills Evaluated but Omitted**: `playwright` — QA only.

  **Parallelization**:
  - **Can Run In Parallel**: YES (alongside T20/T22)
  - **Parallel Group**: Wave 5
  - **Blocks**: none (final)
  - **Blocked By**: T14

  **References**:
  - Pattern: existing `/graph` handler + template (find via the router; renders SessionLinks) + `style.css` tokens.
  - Links: `SessionLink delegated_to/from` (`internal/session/enums.go:314-324`).
  - WHY: Closes the loop — `/graph` shows WHO delegated; the new cross-link takes the user to WHAT was exchanged (H3), directly satisfying "voir les échanges entre les agents".

  **Acceptance Criteria**:
  - [ ] Hermes delegation edges appear on `/graph`; nodes link to `/sessions/{id}/exchanges`; provider badge visible.

  **QA Scenarios**:
  ```
  Scenario: graph shows hermes delegation + links to exchanges
    Tool: Playwright
    Preconditions: daemon :5100 with fixture parent→child delegation ingested
    Steps:
      1. Navigate /graph.
      2. Assert an edge/node attributed to hermes is present.
      3. Click the node; assert navigation to /sessions/{id}/exchanges (or a visible link to it). Screenshot.
    Expected Result: delegation visible + cross-link works
    Evidence: .omo/evidence/task-21-graph-badge.png

  Scenario: graph unaffected when no hermes data
    Tool: Playwright
    Steps:
      1. /graph with only non-hermes sessions; assert no errors, existing edges intact.
    Expected Result: no regression
    Evidence: .omo/evidence/task-21-graph-regression-error.png
  ```

  **Commit**: YES (groups with Delegation) — `feat(graph): hermes delegation badges + exchange links` — files graph template/handler, `style.css` (append) — pre-commit `go test ./internal/web/...`

- [x] 22. CLI `capture --provider hermes` support

  **What to do**:
  - Verify the `capture` command resolves `--provider hermes` to the Hermes provider via the factory (T11). If the provider flag uses an allowlist/switch, add `hermes` to it (additive).
  - Ensure `HERMES_HOME` is honored end-to-end so a fixture home can be captured: `HERMES_HOME=/tmp/fixture-hermes aisync capture --provider hermes`.
  - Confirm captured Hermes sessions appear in `aisync list`/`/sessions` filtered by provider.

  **Must NOT do**:
  - Do NOT add Hermes-only flags beyond what mirrors existing provider flags.
  - Do NOT change capture semantics for other providers.

  **Recommended Agent Profile**:
  - **Category**: `quick` — flag/allowlist wiring on top of T11 factory work.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: none.

  **Parallelization**:
  - **Can Run In Parallel**: YES (alongside T20/T21)
  - **Parallel Group**: Wave 5
  - **Blocks**: T23
  - **Blocked By**: T11

  **References**:
  - Pattern: `capture` command implementation (in `pkg/cmd/...`) — how `--provider` is parsed/validated for existing providers.
  - Factory: `pkg/cmd/factory/default.go:236-243` (T11).
  - WHY: This is the user-facing entry point for ingesting Hermes consumption/tasks; without it the adapter is only reachable via auto-detect.

  **Acceptance Criteria**:
  - [ ] `aisync capture --provider hermes` (fixture HERMES_HOME) ingests sessions; they appear in `aisync list`.

  **QA Scenarios**:
  ```
  Scenario: CLI capture ingests hermes fixture
    Tool: Bash
    Preconditions: built binary; fixture Hermes home at /tmp/fixture-hermes
    Steps:
      1. HERMES_HOME=/tmp/fixture-hermes aisync capture --provider hermes
      2. aisync list | grep -i hermes  (or filter by provider)
    Expected Result: capture succeeds; hermes sessions listed
    Evidence: .omo/evidence/task-22-cli-capture.txt

  Scenario: invalid provider rejected
    Tool: Bash
    Steps:
      1. aisync capture --provider not-real
    Expected Result: clear error, non-zero exit (regression guard)
    Evidence: .omo/evidence/task-22-cli-invalid-error.txt
  ```

  **Commit**: YES (groups with H1) — `feat(cli): capture --provider hermes` — files capture command — pre-commit `go test ./pkg/cmd/...`

- [x] 23. End-to-end smoke with fixture Hermes home

  **What to do**:
  - Add an end-to-end smoke test/script (Go test under `internal/web` or a `scripts/` harness invoked by a test) that: builds a fixture `HERMES_HOME` (state.db from T3 + a cron/jobs.json), runs capture (or the ingest service directly), starts the web server on a test port, then asserts the full chain: `/sessions` lists Hermes sessions, `/sessions/{id}/exchanges` shows the merged timeline, `/cron` lists the job + its run, and `/graph` shows the delegation.
  - Keep it hermetic (temp dirs, ephemeral port, no live Hermes/network).

  **Must NOT do**:
  - Do NOT depend on a developer's real `~/.hermes`.
  - Do NOT assert visual styling here (F3 covers browser visuals) — assert HTTP/data chain.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high` — multi-component wiring + hermetic harness.
  - **Skills**: none.
  - **Skills Evaluated but Omitted**: `playwright` — smoke asserts HTTP/data; browser visuals are F3.

  **Parallelization**:
  - **Can Run In Parallel**: NO (integrates T11/T12/T17/T22)
  - **Parallel Group**: Wave 5
  - **Blocks**: none (final)
  - **Blocked By**: T11, T12, T17, T22

  **References**:
  - Pattern: existing integration/server tests (httptest server bootstrap + store setup) and T3 fixture, T9 cron fixture.
  - WHY: This is the regression backstop proving H1+H2+H3+Delegation work together against a known fixture — the single test that fails loudly if any phase breaks.

  **Acceptance Criteria**:
  - [ ] One command (`go test ./... -run TestHermesE2E`) ingests the fixture and asserts all four surfaces (sessions/exchanges/cron/graph) respond with the expected data.

  **QA Scenarios**:
  ```
  Scenario: full chain green on fixture home
    Tool: Bash
    Steps:
      1. go test ./... -run TestHermesE2E -v -count=1
      2. Assert: sessions listed, exchanges merged timeline non-empty, cron job + run present, delegation link present.
    Expected Result: PASS end-to-end
    Evidence: .omo/evidence/task-23-e2e.txt

  Scenario: e2e is hermetic (no real hermes)
    Tool: Bash
    Steps:
      1. Run the e2e with HOME pointed at a temp dir lacking ~/.hermes (only the fixture HERMES_HOME set).
    Expected Result: still PASS (uses fixture only)
    Evidence: .omo/evidence/task-23-e2e-hermetic-error.txt
  ```

  **Commit**: YES (groups with H3/integration) — `test: hermes end-to-end smoke` — files e2e test/harness — pre-commit `go test ./...`

---

## Final Verification Wave (MANDATORY — after ALL implementation tasks)

> 4 review agents run in PARALLEL. ALL must APPROVE. Present consolidated results to user and get explicit "okay" before completing.
> Do NOT auto-proceed after verification. Never mark F1-F4 checked before user's okay.

- [x] F1. **Plan Compliance Audit** — `oracle`
  Read this plan end-to-end. For each "Must Have": verify implementation exists (read file / run command). For each "Must NOT Have": grep codebase for forbidden patterns (trajectory JSONL ingestion, dbwriter for hermes, renamed OpenCodeDBPath, new H3 migration, telegram tables) — reject with file:line if found. Check evidence files exist in `.omo/evidence/`. Compare deliverables against plan.
  Output: `Must Have [N/N] | Must NOT Have [N/N] | Tasks [N/N] | VERDICT: APPROVE/REJECT`

- [x] F2. **Code Quality Review** — `unspecified-high`
  Run `go build ./...`, `go vet ./...`, `go test ./...`. Review changed files for: `interface{}`/`any` overuse, swallowed errors, leftover debug prints, dead code, generic names. Check AI slop: excessive comments, over-abstraction, copy-paste between opencode and hermes adapters that should share a helper.
  Output: `Build [PASS/FAIL] | Vet [PASS/FAIL] | Tests [N pass/N fail] | Files [N clean/N issues] | VERDICT`

- [x] F3. **Real Manual QA** — `unspecified-high` (+ `playwright` skill for UI)
  Start daemon on port 5100 with a fixture Hermes home. Execute EVERY QA scenario from EVERY task. Test cross-task integration: ingest Hermes → view session → open exchanges → view cron → confirm delegation badge. Edge cases: empty jobs.json, orphan cron session, session with no children, sentinel content. Save to `.omo/evidence/final-qa/`.
  Output: `Scenarios [N/N pass] | Integration [N/N] | Edge Cases [N tested] | VERDICT`

- [x] F4. **Scope Fidelity Check** — `deep`
  For each task: read "What to do", read actual diff (git log/diff). Verify 1:1 — everything specced was built, nothing beyond. Check "Must NOT do" compliance per task. Detect cross-task contamination and unaccounted changes (especially edits to opencode adapter or existing migrations).
  Output: `Tasks [N/N compliant] | Contamination [CLEAN/N issues] | Unaccounted [CLEAN/N files] | VERDICT`

---

## Commit Strategy

Group commits by phase boundary; one commit per task is acceptable. Pre-commit: `go build ./... && go test ./...`.
- H1 (T1-T3,T6-T8,T11,T13,T14,T22): `feat(provider): add hermes ingestion adapter`
- H2 (T4,T5,T9,T10,T12,T15,T16): `feat(cron): track hermes scheduled tasks`
- H3 (T17,T18,T19,T20): `feat(web): inter-agent exchange timeline view`
- Delegation/graph (T21): `feat(graph): enrich hermes delegation badges`

---

## Success Criteria

### Verification Commands
```bash
go build ./...                                  # Expected: no output, exit 0
go vet ./...                                    # Expected: clean
go test ./...                                   # Expected: ok, all packages
HERMES_HOME=/tmp/fixture-hermes aisync capture --provider hermes  # Expected: ingests N sessions
curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:5100/cron            # Expected: 200
curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:5100/sessions/{id}/exchanges  # Expected: 200
```

### Final Checklist
- [x] All "Must Have" present
- [x] All "Must NOT Have" absent
- [x] All tests pass
- [x] F1-F4 all APPROVE
- [x] User gave explicit okay
