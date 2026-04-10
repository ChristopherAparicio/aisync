# aisync — Next Session TODO

> Last updated: 2026-04-05

## 🔥 Active: Phase 4 — CQRS Read Models (no cache)

**Mandate**: user rejected the TTL-based `stats_cache` as non-sense. Target is
sub-50ms page latency for 10-20 concurrent devs, directly from SQLite, via
materialized read models updated on-write (CQRS).

### Bottleneck benchmark — 2026-04-05 (prod DB, 1764 sessions, cache disabled)

Run via: `AISYNC_BENCH_DB=~/.aisync/sessions.db go test -tags=bench_bottlenecks -run=TestBottleneckHotPaths -v ./internal/service/...`

| Function (90d window, all projects) | Cold | Warm | Self-project cold | Verdict |
|---|---:|---:|---:|---|
| `Stats` | 210ms | 142ms | 39ms | 🟢 **No CQRS needed** |
| `Trends` (7d) | 234ms | 237ms | 107ms | 🟢 **No CQRS needed** |
| `SkillROI` | 211ms | 264ms | 283ms | 🟢 Fix #8 already solved |
| `Forecast` | **12.7s** | **14.5s** | 4.9s | 🔴 **CRITICAL** |
| `CacheEfficiency` | **14.0s** | **12.8s** | 4.3s | 🔴 **CRITICAL** |
| `ContextSaturation` | **12.1s** | **12.1s** | 3.8s | 🔴 **CRITICAL** |
| `AgentROI` | **12.4s** | **12.6s** | 5.0s | 🔴 **CRITICAL** |
| `CacheEfficiency` (7d) | 4.0s | 4.1s | — | 🟠 Bad |

**Root cause (confirmed by warm ≈ cold):** the 12s is NOT disk I/O (SQLite page
cache would make warm fast). It is **CPU-bound per-message work on loaded
session bodies** — JSON decode of messages arrays + walking each message for
compaction detection, cache-miss gap analysis, peak-input tracking, model
attribution, per-message cost calc. `GetBatch()` already batched the fetch; the
remaining cost is deterministic post-processing.

**Design implication:** we do NOT need row-level aggregate tables keyed by
project. The unit of caching is **one row per session** — because each
session's analytics are immutable once the session is finalized, and the
expensive work is identical call after call. Computing it **once at write time**
(or once in a backfill) and reading it back eliminates ALL of the 12s.

### CQRS design — `session_analytics` sidecar table

One new table, one row per `sessions.id`, written in the same transaction as
`Save()` and `UpdateCosts()`. All fields below are derived from `session.Session`
+ pricing calculator.

```sql
CREATE TABLE session_analytics (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,

    -- ContextSaturation fields (per-session outputs, aggregated in handler)
    peak_input_tokens       INTEGER NOT NULL DEFAULT 0,
    dominant_model          TEXT    NOT NULL DEFAULT '',
    max_context_window      INTEGER NOT NULL DEFAULT 0,
    peak_saturation_pct     REAL    NOT NULL DEFAULT 0,
    has_compaction          INTEGER NOT NULL DEFAULT 0,  -- bool
    compaction_count        INTEGER NOT NULL DEFAULT 0,
    compaction_drop_pct     REAL    NOT NULL DEFAULT 0,
    compaction_wasted_tokens INTEGER NOT NULL DEFAULT 0,

    -- CacheEfficiency fields
    cache_read_tokens       INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens      INTEGER NOT NULL DEFAULT 0,
    input_tokens            INTEGER NOT NULL DEFAULT 0,
    cache_miss_count        INTEGER NOT NULL DEFAULT 0,
    cache_wasted_tokens     INTEGER NOT NULL DEFAULT 0,
    longest_gap_mins        INTEGER NOT NULL DEFAULT 0,
    session_avg_gap_mins    REAL    NOT NULL DEFAULT 0,

    -- Forecast / Cost breakdown fields (per-session, rolled up in handler)
    backend                 TEXT    NOT NULL DEFAULT '',  -- claude, openai, ...
    estimated_cost          REAL    NOT NULL DEFAULT 0,   -- already in sessions, duped for join-free access
    actual_cost             REAL    NOT NULL DEFAULT 0,
    fork_offset             INTEGER NOT NULL DEFAULT 0,   -- messages deduplicated from parent
    deduplicated_cost       REAL    NOT NULL DEFAULT 0,   -- cost of messages AFTER fork_offset

    -- AgentROI fields (per-session aggregates)
    total_agent_invocations INTEGER NOT NULL DEFAULT 0,
    unique_agents_used      INTEGER NOT NULL DEFAULT 0,
    agent_tokens            INTEGER NOT NULL DEFAULT 0,
    agent_cost              REAL    NOT NULL DEFAULT 0,

    -- Token waste + freshness + fitness — small denormalized summaries,
    -- details remain in events if needed.
    total_wasted_tokens     INTEGER NOT NULL DEFAULT 0,
    freshness_score         REAL    NOT NULL DEFAULT 0,
    fitness_score           REAL    NOT NULL DEFAULT 0,

    -- Housekeeping
    schema_version          INTEGER NOT NULL DEFAULT 1,
    computed_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_session_analytics_project
    ON sessions(project_path, created_at)
    WHERE id IN (SELECT session_id FROM session_analytics);
-- (Actually: indexes will be on sessions.project_path + sessions.created_at,
--  joined to session_analytics. No extra index on session_analytics itself
--  beyond the PK.)
```

**Per-agent rollup** is handled via a second small table to avoid blowing up
columns:

```sql
CREATE TABLE session_agent_usage (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    invocations INTEGER NOT NULL DEFAULT 0,
    tokens      INTEGER NOT NULL DEFAULT 0,
    cost        REAL    NOT NULL DEFAULT 0,
    errors      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, agent_name)
);
CREATE INDEX idx_session_agent_usage_agent ON session_agent_usage(agent_name);
```

### How each hot function becomes fast

1. **`Forecast`** — no more `GetBatch`. Query is:
   ```sql
   SELECT s.project_path, s.created_at, sa.backend, sa.estimated_cost,
          sa.actual_cost, sa.deduplicated_cost, sa.fork_offset
   FROM sessions s
   JOIN session_analytics sa ON sa.session_id = s.id
   WHERE s.created_at >= :since
     AND (:project = '' OR s.project_path = :project);
   ```
   Then bucket + linear-regress in Go. Expected: **<100ms** for 90d / all projects.

2. **`CacheEfficiency`** — pure SQL aggregation:
   ```sql
   SELECT SUM(sa.cache_read_tokens), SUM(sa.input_tokens),
          SUM(sa.cache_miss_count), SUM(sa.cache_wasted_tokens),
          AVG(sa.session_avg_gap_mins), MAX(sa.longest_gap_mins)
   FROM sessions s
   JOIN session_analytics sa ON sa.session_id = s.id
   WHERE s.created_at >= :since
     AND (:project = '' OR s.project_path = :project);
   ```
   Expected: **<50ms**.

3. **`ContextSaturation`** — same pattern, plus a second query for per-model
   groupings. Expected: **<80ms**.

4. **`AgentROI`** — join to `session_agent_usage` + GROUP BY agent_name.
   Expected: **<100ms**.

### Write-path hook

A new `stampAnalytics(sess *session.Session)` method on `SessionService`,
called in the SAME places as the existing `stampCosts()`:

- `session_capture.go`: `Capture`, `CaptureAll`, `CaptureByID`
- `session_ingest.go`: `Ingest`
- `session_export.go`: `Export`
- `session_restore.go`: `Rewind`

It computes all fields by walking `sess.Messages` ONCE (merging the work
currently done by `ContextSaturation`, `CacheEfficiency`, `Forecast` into a
single pass) and persists via `store.UpsertSessionAnalytics(sess.ID, rec)`.

### Backfill strategy

New scheduler task `AnalyticsBackfillTask` modeled on `CostBackfillTask`:
- Processes 200 sessions per run every 30min (`*/30 * * * *`)
- Skips sessions already in `session_analytics` (LEFT JOIN IS NULL)
- Also runs at daemon WarmUp for quick catch-up
- First run will backfill all 1764 sessions in ~9 batches (~5 hours), but the
  read paths will be gated: handlers fall back to live computation for any
  session not yet in `session_analytics` (ensures zero-downtime migration)

Alternatively: one-shot migration command `aisync debug backfill-analytics`
for faster local catch-up.

### Deletion list (after CQRS ships)

- [ ] Delete `cachedForecast`, `cachedCacheEfficiency`, `cachedSaturation`,
      `cachedAgentROI`, `cachedCostsPage`, `cachedFilesForProject` wrappers
      (handlers.go:45-336)
- [ ] Delete `cachedStats`, `cachedTrends`, `cachedSidebarGroups`,
      `cachedSkillROI` wrappers (benchmark showed they were cache-wrapping
      already-fast functions — ~200ms not worth the complexity)
- [ ] Drop `stats_cache` table via new migration
- [ ] Remove `dashboard_warm_task` (no longer needed — everything is live)
- [ ] Remove `cacheeff_precompute` scheduler task

### Implementation order

1. [x] **Migration + schema** (additive, safe on prod) — migration 031 adds
   `session_analytics` + `session_agent_usage` tables with 6 JSON blob columns
2. [x] **Domain type** `session.Analytics` with all fields (flat scalars + 6
   rich-struct pointer fields for WasteBreakdown/Freshness/Overload/PromptData/
   FitnessData/ForecastInput)
3. [x] **Pure function** `session.ComputeAnalytics(sess, pricing, forkOffset)`
   extracted from ContextSaturation + CacheEfficiency + Forecast per-session
   loops. Uses narrow `session.AnalyticsPricingLookup` interface to avoid
   import cycle with `pricing` package.
4. [x] **Unit tests** for the pure function — 26 tests in
   `internal/session/analytics_compute_test.go` covering: empty session, schema
   version stamp, cache field copy, peak/dominant model (skip-first-message
   behaviour), saturation with and without pricing, 100% cap, compaction,
   cache-miss gap detection, backend detection, fork dedup (proportional, no
   fork, zero-shared fallback), rich-struct population, nil-when-missing rules
   for PromptData/FitnessData, agent rollup (single row + unknown fallback),
   token growth rate, msg-at-first-compaction, and a purity guard.
5. [x] **Store port**: `UpsertSessionAnalytics`, `GetSessionAnalytics`,
   `QueryAnalytics(filter) []AnalyticsRow` added to `SessionWriter` in
   `internal/storage/store.go` (under a "Materialized analytics (CQRS read
   model)" section header with full contract docs). Sibling concrete types
   updated to keep the tree building: `*sqlite.Store` got stub
   implementations returning `"not yet implemented (phase 4 step 6)"` errors,
   `service.mockStore` (the fat mock in `session_test.go` used across the
   whole service package including `analysis_test.go`) got one-line no-op
   stubs matching the file's existing style, and `testutil.MockStore` got a
   **real** in-memory implementation (new `Analytics map[ID]Analytics`
   field, sort-by-CreatedAt-DESC semantics matching the documented SQLite
   ordering contract, filter-field honoring, defensive skip of orphaned
   rows). The testutil mock being real means step 8's `stampAnalytics()`
   write-path hook can be exercised end-to-end against it without waiting
   for the sqlite implementation. Full `go build` / `go vet` / `go test ./...`
   all green.
6. [x] **SQLite impl** + store unit tests — all three methods now real SQL in
   `internal/storage/sqlite/sqlite.go`: `UpsertSessionAnalytics` uses a
   transaction (BEGIN/COMMIT) to INSERT OR REPLACE the parent row with 25
   scalar + 6 JSON blob columns, then DELETE+INSERT the sibling
   `session_agent_usage` rows atomically. `GetSessionAnalytics` reads the
   parent row + hydrates `AgentUsage` from the sibling table, returns
   `(nil, nil)` for missing rows. `QueryAnalytics` does a JOIN against
   `sessions` for ProjectPath/CreatedAt/Branch, dynamic WHERE from filter,
   ordered by `created_at DESC`. Helper functions `marshalJSONBlob` (handles
   typed nil pointers → "" via "null" normalization) and `unmarshalJSONBlob`
   (empty string → no-op). Reuses existing `boolToInt`. 13 tests in
   `sqlite_test.go` (5 top-level + 8 subtests): full round-trip with all 6
   blobs + 2-row AgentUsage, not-found → nil/nil, upsert-update path
   verifying old agent rows are replaced, nil-blobs round-trip, and
   QueryAnalytics filter tests (ProjectPath, Backend, Since, Until,
   MinSchemaVersion, combined, no-match, blobs hydrated in query results).
7. [x] **Mock updates** — all mocks already satisfy the interface from step 5.
   `testutil.MockStore` has a real in-memory impl, `service.mockStore` has
   no-op stubs, scheduler test mocks embed `storage.Store` (nil fallthrough).
   No additional work needed.
8. [x] **Write-path hook** `stampAnalytics()` wired into all 9 mutation
   entry points (matching every `stampCosts` call site): 6 in
   `session_capture.go` (single capture parent+children, summarization
   re-save, CaptureAll parent+children, CaptureOne), 1 in
   `session_export.go` (import/aisync format), 1 in `session_ingest.go`
   (API ingest), 1 in `session_ai.go` (rewind). The method lives in
   `session.go` alongside `stampCosts`, calls `GetForkRelations` for fork
   offset, `session.ComputeAnalytics` for computation, and
   `UpsertSessionAnalytics` to persist. Errors are logged but non-fatal
   (backfill task catches missed rows).
9. [ ] **Backfill task** `AnalyticsBackfillTask`
10. [ ] **Rewrite** `Forecast`, `CacheEfficiency`, `ContextSaturation`,
    `AgentROIAnalysis` to read from `session_analytics` (fallback to live
    computation for missing rows)
11. [ ] **Delete cache wrappers + stats_cache table** (separate commit for clean
    revert)
12. [ ] **Re-run benchmark** to confirm <100ms on all 4 critical paths
13. [ ] **Production verify**: reload each page, measure latency, tail
    `~/.aisync/aisync.log` for regressions

### Open questions (to resolve before step 1)

- **Q1**: Should we compute analytics inline on every `Save()` (blocks writer
  for ~50ms per session) OR enqueue a background job (eventually-consistent,
  but Save() stays fast)? Recommendation: **inline**, because (a) single-writer
  model, (b) 50ms is invisible, (c) eventual consistency creates UI surprises.
- **Q2**: Version the `schema_version` column: when we add new fields, do we
  force-rebuild all rows or allow mixed versions? Recommendation: force rebuild
  by marking all rows `schema_version=0` and letting the backfill task
  reprocess. Cheap at 1764 rows.
- **Q3**: What about sessions currently open (not finalized)? Their analytics
  will be recomputed on every append. Recommendation: only stamp analytics on
  sessions where `sess.FinishedAt != nil` OR `sess.Status == "completed"`. For
  in-flight sessions, fall back to live computation (rare, bounded to the few
  open sessions per dev).

## Recently Completed (2026-04-05)

### N+1 Query Performance Fixes — Phase 2 ✅
- [x] **Fix #17: `store.GetBatch(ids)`** — Root 3-query amplifier eliminated
  - Added `GetBatch(ids []session.ID) (map[session.ID]*session.Session, error)` to `storage.SessionReader` interface
  - SQLite implementation: **3 queries total** instead of 3N — IN-clause for sessions + links + file_changes, dedup input IDs, silent skip of missing rows, per-row error logging, resets payload-embedded slices before hydrating from companion tables (DB is source of truth)
  - **Benchmark results** (500-session dataset): `Get()` loop = 37.1ms, `GetBatch()` = 15.2ms → **2.45× speedup**, eliminates 1497 SQL round-trips
  - Refactored 3 consumers: `Forecast()` (session_forecast.go:98), `ContextSaturation()` (session_usage.go:662), `CacheEfficiency()` (session_usage.go:1066) — each pre-filters → batch-fetches → loops with map lookup
  - 6 unit tests (all requested returned, hydrates companion tables, missing IDs omitted, empty input, dedup, Get parity) + 2 benchmarks
  - All mock stores updated: `testutil/mock_store.go`, `service/session_test.go`, `scheduler/analyze_task_test.go`
  - **Production deployed** on 1764 sessions, 0 regressions, all endpoints healthy (dashboard 75-80ms warm, stats 123-162ms)
- [x] **Fix #2: Forecast N+1** — Now uses GetBatch (was separate issue, fixed via #17 refactor)
- [x] **Fix #3: ContextSaturation N+1** — Now uses GetBatch (was separate issue, fixed via #17 refactor)
- [x] **Fix #4: CacheEfficiency N+1** — Now uses GetBatch (was separate issue, fixed via #17 refactor)
- [x] **Fix #10: Dashboard Stats+List redundancy** — Eliminated duplicate full table scans per dashboard hit
   - Extended `service.StatsResult` with 4 new fields: `TotalErrors`, `TotalToolCalls`, `SessionsWithErrors`, `RecentSessions` (JSON tags for wire-format compat)
   - Populated inline in `Stats()` summary loop (zero added cost — same iteration) with dedicated RecentSessions sort+truncate (top 10 by UpdatedAt desc, uses local `copy()` to avoid mutating store slice)
   - Deleted redundant "Recent sessions + error counts" goroutine (~45 lines) from `handleDashboard` — was doing a second full `sessionSvc.List(req)` with identical filters
   - Mirrored in `client.StatsResult` for remote service round-trip
   - **Bonus**: Fixed latent bug in `countErrors()` (`digest_task.go`) — old impl summed from `PerType` buckets, silently dropping sessions without `SessionType` classification. Now reads `stats.TotalErrors` directly (O(1), correct). Added `TestCountErrors_UntypedSessions` regression test.
   - **Production verified** (1764 sessions): `total_errors=4354`, `total_tool_calls=404503`, `sessions_with_errors=585`, `recent_sessions=10`. Dashboard warm latency **9.8ms** (down from 75-80ms baseline — goroutine removal shaved ~65ms). Stats endpoint 141ms.
   - All 92 test packages green
- [x] **Fix #8: SkillROI N+1** — Eliminated per-session `GetSessionEvents` loop in `SkillROIAnalysis`
   - Added `SessionEventStore.GetSessionEventsBatch(ids, types...)` port (`internal/storage/store.go`) with variadic event-type filter — single query, dedup input, buckets events into `map[session.ID][]sessionevent.Event`
   - SQLite impl (`internal/storage/sqlite/event_store.go`): one query with optional `event_type IN (...)` clause, reuses `scanEvents` helper, orders by `session_id, occurred_at, message_index`
   - Refactored `SkillROIAnalysis` (`internal/service/session_roi.go:211-260`): pre-loop collects IDs + `summaryByID` lookup, single batched call filtered to `EventSkillLoad` at SQL level, outer loop over result map. Best-effort fallback preserves existing contract (ROI must not hard-fail on event store errors).
   - **Why variadic type filter is critical**: prod has 532,891 total events / avg 300 per session, but skill ROI only needs 1029 `skill_load` rows across 335 sessions. SQL-level filter avoids transferring irrelevant payloads.
   - Mocks updated: `testutil/mock_store.go`, inline mock in `session_test.go`
   - Added `TestGetSessionEventsBatch` (`storage/sqlite/sqlite_test.go`) with 7 subtests: bucketing, type filter narrowing, variadic multi-type, missing IDs omitted, empty input, dedup, semantic parity with `GetSessionEvents` for single ID. Seeds parent sessions before events (FK requirement).
   - **Production verified** on `/projects/Users/guardix/dev/aisync` (130 skill_load events, 90d window): cold **3.78s** (ROI computation + cache write), warm **110ms** (cache hit). SkillROI section renders correctly with ghost badges, verdict classes, and downstream recommendations populated. Daemon log clean.
   - All 92 test packages green

## Recently Completed (2026-04-04)

### N+1 Query Performance Fixes — Phase 1 ✅
- [x] **Fix #14: GC dry-run** (`session_gc.go`): Uses `sm.CreatedAt` from Summary instead of `store.Get()` — eliminates ~4801 queries for 1638 sessions
- [x] **Fix #1: Stats() N+1** (`session_stats.go`): Costs from denormalized `sm.EstimatedCost`/`sm.ActualCost`, file changes from `GetSessionFileChanges()`, full `Get()` only for `IncludeTools` — eliminates ~6401 queries for default Stats() path
- [x] **Fix #16: SearchFacets 7→1** (`sqlite.go`): Rewrote 7 sequential GROUP BY queries into single UNION ALL query
- [x] **Fix #6+7: Batch fork relations** (`sqlite.go` + `session_timeline.go` + `handlers.go`): `GetForkRelationsForSessions(ids)` batch method replaces per-session `GetForkRelations()` loops
- [x] **Cost denormalization**: Migration 029 adds `estimated_cost`/`actual_cost` columns; `Save()` writes both; `List()` reads into Summary; `stampCosts()` on all write paths (Capture, CaptureAll, CaptureByID, Ingest, Export, Rewind)
- [x] **CostBackfillTask**: New scheduler task processes 200 sessions/run, registered at `*/30 * * * *` + WarmUp
- [x] **MockStore `GetSessionFileChanges` enhanced**: Synthesizes `SessionFileRecord[]` from session's `FileChanges` field (mirrors real store behavior)
- [x] **All 92 test packages passing**, 0 failures

### Remaining N+1 Issues
_All 17 audit items resolved. See Phase 1 (2026-04-04) and Phase 2 (2026-04-05) above._

### Phase 3 Candidates (Non-Blocking, Low-Priority)
Discovered during post-Phase-2 audit on 2026-04-05. None are on hot web paths — all are
background tasks, CLI one-shots, or branch-scoped handlers. Safe to leave for later.

- [ ] **`ComputeTokenBuckets`** (`session_usage.go:124`) — periodic scheduler task, loops `store.Get(sm.ID)` per session to access per-message timestamps/tokens. Could use `GetBatch` but requires the full payload anyway (not just Summary). Low impact: runs in background.
- [ ] **`ClassifyProjectSessions`** (`session_classifier.go:146`) — CLI-triggered classification pass, loops `store.Get(sm.ID)` for classifier rules. Could use `GetBatch`. Low impact: one-shot CLI command.
- [ ] **`DetectOffTopic`** (`session_offtopic.go:46+74`) — web handler but scoped to a single branch (N ≈ 5-20 sessions). Could use `GetBatch`. Low impact: small N.
- [ ] _Legitimate one-shots (no action needed)_: `BackfillFileBlame` (file_blame.go:78), `IndexAllSessions` (session_index.go:50), `CostBackfillTask` (cost_backfill_task.go:70) — all document themselves as expensive one-shot/migration tasks.

### Feature 1.5 — Skill Reuse Map ✅ (Sprint H)
- [x] **Domain** (`internal/registry/domain.go`): `SkillReuseMap`, `SkillReuseEntry`, `SkillUsageInput` structs, `BuildSkillReuseMap()` pure function. Classifies skills as shared (2+ projects), mono (1 project), or idle (configured never loaded). Sorts by load count descending.
- [x] **Domain tests** (`internal/registry/domain_test.go`): 4 tests — `TestBuildSkillReuseMap_Empty`, `_SharedSkills`, `_IdleSkills`, `_SortByLoads`. All pass.
- [x] **Service** (`internal/service/registry.go`): `SkillReuseAnalysis(since)` method combining registry capabilities (`KindSkill`) with event bucket `TopSkills`/`SkillTokens` data.
- [x] **Handler** (`internal/web/handlers.go`): `skillReuseView` struct, `HasSkillReuse` + KPI fields on `costDashboardPage`, wired in `buildCostsData()` for cross-project view.
- [x] **Template** (`internal/web/templates/cost_tools_partial.html`): New "Skill Reuse Map" section with KPI cards (Total Loads/Shared/Mono/Idle), 3 sub-tables (Shared Skills, Mono-Project Skills, Idle Skills) with status badges.

### Feature 5.2 — Cross-Project Capabilities Dashboard ✅ (Sprint G)
- [x] **Domain** (`internal/registry/domain.go`): `MCPGovernanceMatrix`, `MCPGovernanceProjectRow`, `MCPGovernanceCell`, `MCPGovernanceInput` structs, `BuildMCPGovernanceMatrix()` pure function reusing `AnalyzeConfiguredVsUsed()` per project.
- [x] **Domain tests** (`internal/registry/domain_test.go`): 4 tests — `TestBuildMCPGovernanceMatrix_Empty`, `_SingleProject`, `_MultiProject`, `_Costs`.
- [x] **Service** (`internal/service/registry.go`): `CrossProjectMCPGovernance(since)` method, extracted shared `aggregateMCPUsage()` helper, refactored `ConfiguredVsUsed()` to use it (DRY).
- [x] **Handler** (`internal/web/handlers.go`): `mcpGovMatrixView`, `mcpGovMatrixRowView`, `mcpGovCellView` structs, wired in `buildCostsData()`.
- [x] **Template** (`internal/web/templates/cost_tools_partial.html`): "MCP Governance Across Projects" section with KPI cards, matrix table, per-cell status badges (✓/👻/?/—), cost/summary columns.

### Feature 5.1 — Registry Persistence ✅ (Sprint F)
- [x] Migration 030 (`project_capabilities` table), store interface extended, SQLite implementation, scheduler task, config, all wired.

### Feature 1.4b — Configurable MCP Tool Prefixes ✅ (Sprint F)
- [x] Auto-populate `knownMCPPrefixes` from registry's discovered `MCPServer` entries.

### Session Graph — Interactive Tree Visualization ✅
- [x] **New page**: `/graph` — interactive expandable tree grouping sessions by PR, project, or branch
- [x] **3 view modes**: `?mode=pr` (default), `?mode=project`, `?mode=branch` — sidebar quick-switch
- [x] **Tree construction**: Reuses `ParentID` fork relationships to build parent→children hierarchy; root sessions (no parent in set) become top-level; forked sessions nested as children
- [x] **Session links**: Link badges (delegation, continuation, replay, related) enriched recursively on each node via `GetLinkedSessions()`
- [x] **Handler `handleSessionGraph`**: 3 build methods — `buildGraphByPR` (via `ListPRsWithSessions`), `buildGraphByProject` (groups `List()` by project path), `buildGraphByBranch` (groups by branch name). Project filter support via `?project=` query param
- [x] **View models**: `sgGroup` (top-level grouping with icon, state, token total), `sgNode` (recursive with depth, indent, fork indicator, link badges, summary), `sgBadge` (link type with CSS modifier), `sgStats` (totals)
- [x] **Template**: `session_graph.html` with sidebar (view mode + project list), stats bar, collapsible group cards, recursive `sg-node` template with connector lines, fork/link badges, session ID links
- [x] **JavaScript**: Toggle group/node expand/collapse, expand all/collapse all, text filter with 150ms debounce (searches group names + node text)
- [x] **CSS**: ~250 lines of `.sg-*` classes (group cards, node headers, connector lines, dot indicators, fork/link badges, state badges, filter)
- [x] **Navbar**: "Graph" link added between PRs and Analytics
- [x] **Files**: `templates/session_graph.html`, `handlers.go` (+400 lines), `server.go` (route + pageSpec), `layout.html` (nav link), `style.css` (+250 lines)

## Previously Completed (2026-04-02)

### File Tree Page — Interactive Expandable Tree View ✅
- [x] **New page**: `/tree/{path...}` — full recursive tree of all files with AI session activity
- [x] **Recursive Go template**: `ft-node` self-referencing pattern (like `bt-node` in branch timeline), renders dirs first then files, with indent via `padding-left`
- [x] **HTMX lazy-loading**: Clicking a file expands to show all sessions that touched it (`/partials/file-sessions` endpoint), loaded on demand — avoids 378 upfront session queries
- [x] **Handler `handleFileTree`**: Builds recursive `fileTreeNode` tree from `FilesForProject()` flat entries, no new store methods needed
- [x] **Handler `handleFileTreeSessions`**: HTMX partial returning `blameEntryView` list per file via `GetSessionsByFile()`
- [x] **Template**: `file_tree.html` with toolbar (Expand all / Collapse all), stats bar, recursive tree, inline JavaScript for toggle/expand/collapse
- [x] **Partial**: `file_tree_sessions_partial.html` for HTMX session list per file
- [x] **CSS**: ~200 lines of `.ft-*` classes (tree connectors, expand/collapse animation, session cards with colored dots, change type badges, spinner)
- [x] **Navbar**: "Tree" link added to layout.html
- [x] **Stats**: 117 directories, 378 AI-touched files rendered for aisync project (~755 Ko HTML), render in ~80ms warm cache
- [x] **Files**: `templates/file_tree.html`, `templates/file_tree_sessions_partial.html`, `handlers.go` (+200 lines), `server.go` (routes + template registration), `layout.html` (nav link), `style.css` (+200 lines)

### 3 Bug Fixes — CSS/UX ✅
- [x] **Projects page badge overflow**: `.project-card` now has `overflow: hidden`, `.project-card-header` has `flex-wrap: wrap`, badges have `flex-shrink: 0; white-space: nowrap`, project name has `text-overflow: ellipsis`
- [x] **Sessions list relative dates**: Fixed `timeAgoString()` — added clock-skew guard (negative durations → "just now" or date fallback instead of "-5m ago"), added month/year buckets ("3mo ago", "Jan 2026") instead of unbounded "Xd ago" for old sessions. Fixed `timeAgo()` — month calculation now rounds (`30.44` avg days/month + `+0.5`) instead of truncating (was undercounting months)
- [x] **Analytics table data repetition**: Daily Activity table now deduplicates buckets by date — when viewing all projects (no filter), multiple project buckets for the same day are merged into a single row via `dailyMap`. Previously showed N duplicate rows per date (one per project). KPI totals were already correct (they aggregate all buckets), but the table was misleading.

### Dashboard Performance Optimization — Concurrent Handlers + Caching ✅
- [x] **Root cause identified**: Project detail pages (`/projects/{path}`) timed out at 30s for large projects (900+ sessions). The handler ran 14+ service calls **sequentially**, including catastrophically expensive ones:
  - `AgentROIAnalysis()`: Full table scan + 900 `store.Get()` calls (full payload decompression) via `ContextSaturation()`
  - `SkillROIAnalysis()`: Full table scan + 900 `GetSessionEvents()` individual queries
  - `SecurityDetector.ScanProject()`: 900 `store.Get()` calls + rule scanning on every message
  - `GenerateRecommendations()` fallback: Calls **all** of the above + `CacheEfficiency()` — the single worst offender
- [x] **Concurrent handler (`buildProjectDetailData`)**: Rewrote to run **14 independent sections in goroutines** using `sync.WaitGroup` + `sync.Mutex`. All data fetches that don't depend on each other now run in parallel.
- [x] **New cache wrappers**: `cachedAgentROI()` (2h TTL), `cachedSkillROI()` (2h TTL) — matches existing patterns for saturation/trends/cacheEfficiency
- [x] **Security scan moved to cache-only**: Removed inline `SecurityDetector.ScanProject()` — reads from cache only (scheduler should pre-warm). On cold cache, section is silently skipped instead of blocking the page for 30+ seconds.
- [x] **Removed `GenerateRecommendations` fallback**: The on-the-fly computation was the single most expensive operation (triggers AgentROI + SkillROI + CacheEfficiency + ContextSaturation all inline). Now reads only from pre-computed store recommendations.
- [x] **Dashboard page (`/`) also optimized**: Same concurrent pattern applied to `buildDashboardData()` — 6 goroutines for List, Forecast, CacheEfficiency, Trends, Capabilities, Sparklines.
- [x] **Used `cachedSidebarGroups()` in project detail**: Replaced direct `ListProjects()` call (full table GROUP BY) with existing cached sidebar accessor.
- [x] **Performance results** (warm cache): `/projects/{path}`: **47-67ms** (was 30s+ timeout), `/`: **117ms** (was 10s+ timeout), `/settings`: **2ms**
- [x] **Cold cache** first-hit: `/projects/aisync` (21 sessions): ~7s, `/projects/omogen-backend` (900+ sessions): ~0.6s (most expensive sections cached on first project visit), `/`: ~23s (CacheEfficiency global scan — cached after)
- [x] **All 92 test packages passing**, 0 failures

### Settings Page Emoji Encoding Fix ✅
- [x] **Bug**: Section icons showed HTML entities (`&#x2699;`, `&#x1F6A9;`, `&#x1F50D;`) instead of actual emojis
- [x] **Root cause**: `Icon` field was `string` with HTML entities, but Go's `html/template` auto-escapes `&` to `&amp;`, turning `&#x2699;` into `&amp;#x2699;` (rendered as literal text)
- [x] **Fix**: Replaced all 11 HTML entity strings in `handleSettings()` with actual Unicode emoji characters (⚙, 🚩, 🔍, 🧠, 🏷, 🔒, ⚠, 📊, 🖥, ⏰)
- [x] **Verified**: Settings page now shows proper emojis for all section headers

### Build Fix ✅
- [x] Fixed `pkg/cmd/servecmd/serve.go` — removed dead `LLMQueue`/`Enricher` references in RecommendationConfig

### LLM-Enhanced Recommendations via Queue (Étape 3) ✅
- [x] **Domain type** (`session/recommendation.go`): `EnrichedRecommendation` struct — `Title`, `Message`, `Impact` fields for LLM-enriched content, JSON-serializable for structured LLM responses
- [x] **Port interface** (`scheduler/enricher.go`): `RecommendationEnricher` interface with `Enrich(ctx, recs) ([]EnrichedRecommendation, error)` — consumer-side interface (Go idiom), batch-oriented for efficiency
- [x] **LLM adapter** (`scheduler/enricher.go`): `LLMEnricher` uses `llm.Client.Complete()` with a structured system prompt that instructs the model to rewrite recommendations with (1) clearer titles, (2) actionable 2-4 sentence explanations, (3) concrete impact descriptions. Configurable `MaxBatch` (default 10). JSON response parsing with fallback `extractJSONArray()` for markdown-fenced output.
- [x] **Queue integration** (`scheduler/recommendation_task.go`): `submitEnrichmentJob()` submits an async `llmqueue.Job` after deterministic persistence + analysis bridge (Step 6). Non-blocking — the job runs in the LLM queue worker goroutine. `runEnrichment()` applies enriched fields (title/message/impact) and updates `Source` to `RecSourceLLM` via `UpsertRecommendation`. Skips enrichments with empty Message (keeps original).
- [x] **Task config enriched**: `RecommendationConfig` now accepts `Enricher RecommendationEnricher`, `LLMQueue LLMJobQueue`, `MaxEnrich int` (default 10). `LLMJobQueue` port interface decouples from concrete `llmqueue.Queue`. `LLMJob` type alias avoids leaking imports.
- [x] **Factory wiring** (`serve.go`): Creates `llmfactory.NewClientFromConfig()` → `LLMEnricher` → passed to `RecommendationConfig` alongside `llmQ`. Graceful degradation: if LLM client creation fails (no claude CLI, etc.), enrichment is simply disabled.
- [x] **Mock store fix**: `UpsertRecommendation` now also updates the `Source` field (was missing), matching the real SQLite `ON CONFLICT DO UPDATE SET source = excluded.source` behavior
- [x] **16 new tests**: 6 enricher adapter (`Enrich` success, empty recs, LLM error, invalid JSON, batch limit, default maxBatch), 5 helper unit tests (`extractJSONArray` 6 subtests, `parseEnrichResponse` 4 subtests, `buildEnrichPrompt` content verification), 6 task integration (`EnrichesViaQueue` — full flow with source/title/message/impact verification, `SkipsEmptyMessage`, `NoEnrichmentWithoutEnricher`, `EnrichmentQueueFull`, `EnrichmentErrorNonFatal`, `DefaultMaxEnrich`)
- [x] **All tests passing**: 94 test packages, 0 new failures (1 pre-existing in `internal/service`)

### Bridge Analysis → Recommendations (Étape 2) ✅
- [x] **`bridgeAnalysisRecommendations()` method** (`scheduler/recommendation_task.go`): Iterates all projects, lists recent sessions (last 7 days), fetches LLM analysis via `store.GetAnalysisBySession()`, and bridges analysis insights into the recommendation store with `Source=RecSourceAnalysis`
- [x] **Analysis recommendation bridge**: Maps `analysis.Recommendation` → `session.RecommendationRecord` — `Type = "analysis_" + category`, `Description → Message`, `mapAnalysisPriority()` (1→high, 2-3→medium, 4-5→low), `mapAnalysisIcon()` (🧩 skill, ⚙️ config, 🔄 workflow, 🔧 tool)
- [x] **Skill suggestion bridge**: Maps `analysis.SkillSuggestion` → rec with `Type = "analysis_skill_suggestion"`, `Priority = "low"`, `Icon = "💡"`, `Skill = ss.Name`, `Title = "Suggested skill: <name>"`
- [x] **Missed skill bridge**: Maps `analysis.SkillObservation.Missed` → rec with `Type = "analysis_skill_missed"`, `Priority = "medium"`, `Icon = "🔍"`, `Skill = missed`, contextual message explaining the observation
- [x] **Graceful skip**: Sessions without analyses are silently skipped (uses `analysis.ErrAnalysisNotFound` sentinel)
- [x] **Integrated into `Run()` flow**: Bridge runs as Step 5 after deterministic persistence + notifications, only when store is available
- [x] **7 new bridge tests**: `BridgeAnalysisRecommendations` (2 recs, priority/icon/message verification), `BridgeSkillSuggestions` (2 suggestions, source/priority/icon/skill fields), `BridgeMissedSkills` (1 missed, priority/source/skill/icon), `BridgeSkipsSessionsWithoutAnalysis` (2 sessions, 1 with analysis → only 1 rec), `BridgeFullScenario` (integration: 1 deterministic + 1 config rec + 1 suggestion + 2 missed = 5 total, source count verification)
- [x] **2 unit tests**: `TestMapAnalysisPriority` (7 cases: 0-5 + 99), `TestMapAnalysisIcon` (5 cases: 4 categories + unknown)
- [x] **24 total recommendation tests** (was 17), all passing. Full test suite: 114 packages, 0 new failures

### Recommendations Persistence & Cache (Étape 1) ✅
- [x] **Domain types** (`session/recommendation.go`): `RecommendationRecord` with lifecycle tracking (ID, fingerprint, status, created_at, updated_at, dismissed_at, snoozed_until), `RecommendationStatus` (active/dismissed/snoozed/expired), `RecommendationSource` (deterministic/analysis/llm), `RecommendationFilter`, `RecommendationStats`, `RecommendationFingerprint()` (FNV-1a hash for dedup)
- [x] **Store interface** (`storage/store.go`): `RecommendationStore` with 8 methods — `UpsertRecommendation` (INSERT ON CONFLICT fingerprint, preserves dismissed/snoozed), `ListRecommendations` (filter + priority sort), `DismissRecommendation`, `SnoozeRecommendation`, `ExpireRecommendations` (maxAge TTL), `ReactivateSnoozed`, `RecommendationStats`, `DeleteRecommendationsByProject`
- [x] **SQLite migration 028**: `recommendations` table with all columns + unique fingerprint index + composite (project_path, status) index
- [x] **SQLite implementation** (`sqlite/recommendation_store.go`): Full 8-method implementation with `sql.NullString` for nullable time fields, `CASE priority` ordering, `RowsAffected` counting
- [x] **Mock stores updated**: `testutil.MockStore` with real in-memory logic (upsert by fingerprint, filter/sort, dismiss/snooze tracking) + `service/session_test.go` stubs
- [x] **Scheduler task enriched**: `RecommendationTask` now persists recs to store (upsert by fingerprint), expires stale recs (14-day default TTL), reactivates snoozed recs, AND sends notifications. Works in store-only mode (no notification service required).
- [x] **serve.go updated**: Task now runs even without `notifSvc` (store-only persistence). Store passed to task config.
- [x] **Web handler: store-first reads**: Project detail page reads recommendations from store (pre-computed, instant) with fallback to on-the-fly `GenerateRecommendations()` when store is empty
- [x] **Dismiss/Snooze API**: `POST /api/recommendations/dismiss` (by ID) and `POST /api/recommendations/snooze` (by ID + optional days, default 7, max 90)
- [x] **insightView enriched**: Added `ID` field for HTMX dismiss/snooze actions
- [x] **17 new tests**: 14 RecommendationTask (name, nil notif, no projects, list error, sends notification, skips low-only, skips empty, multiple projects, severity escalation, items cap, recs error, store persistence, store-only mode, default expiry), 3 Slack formatter
- [x] **All tests passing** across 114 packages, 0 failures

### `aisync resume` — Smart Restore Filter Propagation ✅
- [x] **Filter flags added to resume command**: `--clean-errors`, `--strip-empty`, `--fix-orphans`, `--redact-secrets`, `--exclude` — identical to `aisync restore`, same filter chain order (exclude → fix-orphans → strip-empty → clean-errors → redact-secrets)
- [x] **`buildFilters()` + `parseExcludeFlag()`**: Reuses `internal/restore/filter` package directly; supports message indices, role names, and `/regex/` content patterns
- [x] **Filter results displayed**: "Smart Restore filters applied:" section shows which filters were applied with summaries
- [x] **Help text updated**: Long description includes filter flag documentation + examples
- [x] **16 new tests**: Flag registration (8 flags verified), buildFilters (4 — none, all, single, invalid), parseExcludeFlag (7 — indices, roles, pattern, mixed, negative, multiple patterns, invalid regex), integration (4 — strip-empty with output, clean-errors, multiple filters, invalid exclude error)
- [x] **All tests passing**, full build clean

### `aisync restore --dry-run` — Preview Before Applying ✅
- [x] **Domain types**: `DryRunPreview` struct in both `internal/restore/` (engine) and `internal/service/` (service layer) — SessionID, Provider, Branch, Summary, Method, MessageCount, ToolCallCount, ErrorCount, InputTokens, OutputTokens, TotalTokens, FileChanges, FilterResults
- [x] **`DryRun bool` field** added to `restore.Request`, `service.RestoreRequest`, and `client.RestoreRequest` (JSON transport)
- [x] **Engine dry-run short-circuit**: After session lookup + agent override + filter application, but **before** any I/O (no CONTEXT.md generation, no provider import), builds preview via `buildDryRunPreview()`
- [x] **Method detection**: Determines `native`/`converted`/`context` without actually performing the restore, by checking provider/target match and `AsContext` flag
- [x] **`--dry-run` flag** on both `aisync restore` and `aisync resume` CLI commands
- [x] **`renderDryRunPreview()`** / **`renderResumeDryRun()`**: Human-readable preview output with session info, token counts, file changes, filter effects, and "Run without --dry-run to apply" hint
- [x] **Remote service updated**: `DryRun` field mapped through `client.RestoreRequest` → `service/remote/session.go`
- [x] **5 engine tests**: dryRunByBranch (full field verification + no CONTEXT.md written), dryRunWithFilters (filter applied + reported), dryRunMethodDetection (3 sub-tests: native/converted/context)
- [x] **3 resume tests**: dryRun (output verification), dryRunWithFilters (filter info in output), dryRunFlag registration
- [x] **All tests passing** across 92 packages, 0 failures

### Recommendations Engine — Enrichment & Notification ✅
- [x] **Fixed LSP errors**: `SystemPromptImpact` field names (`AvgPromptTokens` → `AvgEstimate`, `CostPercent` → `AvgPromptCostPct`) and `ModelSaturation` field name (`AvgPeakSaturation` → `AvgPeakPct`) in `session_recommendations.go`
- [x] **12 new recommendation types** enriching `GenerateRecommendations()`:
  - `token_waste_retry` — >15% retry waste
  - `token_waste_compaction` — >20% compaction waste
  - `token_waste_cache` — >10% cache miss waste
  - `token_waste_low_productivity` — <60% productive tokens
  - `model_fitness` — bridges `FitnessAnalysis.Recommendations` strings
  - `freshness_error_growth` — error rate spike after compaction (>50%)
  - `freshness_output_decay` — output ratio decay after compaction (>30%)
  - `freshness_optimal_length` — optimal session length recommendation
  - `prompt_large` — system prompts averaging >8K tokens
  - `prompt_growing` — growing prompt size trend
  - `model_oversized` — using large model but <20% utilization
  - `model_saturated` — consistently hitting context limits
- [x] **Notification domain** (`notification/domain.go`): `EventRecommendation` event type + `RecommendationData` / `RecommendationItem` structs
- [x] **Router** (`notification/router.go`): `EventRecommendation` routes to project channel (or default)
- [x] **Slack Block Kit formatter** (`adapter/slack/formatter.go`): `formatRecommendations()` — title with project, total/high count summary, divider, per-item cards with priority emoji (red/orange/white), impact line, `divider()` helper
- [x] **RecommendationTask** (`scheduler/recommendation_task.go`): Daily cron (7 AM) — iterates all projects, generates recommendations, filters to high-priority, sends notification with max 10 items. Severity escalation (≥3 high → warning). Errors per-project are logged and non-fatal.
- [x] **Wired in serve.go**: Task registered when `notifSvc != nil`
- [x] **16 new tests**: 11 RecommendationTask (name, nil notif, no projects, list error, sends high-priority, skips low-only, skips empty, multiple projects, severity escalation, items limited to 10, recs error continues), 3 Slack formatter (valid data, invalid data, empty project), 2 router (routes to project, falls back to default)
- [x] **All tests passing** across 114 packages, 0 failures

### `aisync diagnose <session-id>` — Unified Session Debugging ✅
- [x] **Domain types** (`internal/session/diagnosis.go`): `DiagnosisReport`, `ErrorTimelineEntry`, `PhaseAnalysis`, `SessionPhase`, `ToolReport`, `ToolReportEntry`, `DiagnosisVerdict`, `RestoreAdvice` — all pure domain, no I/O
- [x] **Pure domain functions**: `BuildErrorTimeline()` (positions errors in message flow with phase + escalation detection), `AnalyzePhases()` (splits session into quarters, labels clean/degrading/broken), `BuildToolReport()` (per-tool call+error counts, sorted by error rate), `ComputeVerdict()` (derives healthy/degraded/broken status from health score + overload + phases), `ComputeRestoreAdvice()` (suggests rewind point + filter flags)
- [x] **Phase pattern classification**: `classifyPattern()` detects "healthy", "clean-start-late-crash", "error-from-start", "steady-decline", "intermittent" patterns from phase quality progression
- [x] **Service layer** (`internal/service/session_diagnose.go`): `Diagnose(ctx, DiagnoseRequest)` with quick scan (pure domain, instant) + optional deep analysis (LLM-powered via `AnalyzeEfficiency` reuse). `sessionToSummary()` helper builds lightweight Summary from full Session for health score computation
- [x] **`SessionAI` interface updated** in `iface.go`: Added `Diagnose(ctx, DiagnoseRequest) (*session.DiagnosisReport, error)`
- [x] **Remote adapter stub**: `remote/session.go` returns "not supported in remote mode"
- [x] **Mock stubs**: Updated `scheduler/tasks_test.go` mock (inherited by all embedded mocks: budgetMockService, mockSaturationSessionService, etc.)
- [x] **CLI command** (`pkg/cmd/diagnosecmd/diagnosecmd.go`): `aisync diagnose <session-id>` with `--deep` (LLM analysis), `--json` (structured JSON), `--quiet` (one-liner verdict). Full text renderer with verdict icon, health score breakdown, phase analysis, overload, tool report, error timeline, error summary, restore advice, and deep analysis sections
- [x] **Registered in root.go**: `diagnosecmd.NewCmdDiagnose(f)` added to command tree
- [x] **17 domain tests** (`diagnosis_test.go`): BuildErrorTimeline (3), classifyPhase (1), AnalyzePhases (4), BuildToolReport (2), ComputeVerdict (3), ComputeRestoreAdvice (3), sortToolReportEntries (1)
- [x] **6 service tests** (`session_diagnose_test.go`): Quick scan healthy/broken/empty/not-found, deep scan with LLM, deep scan without LLM (graceful fallback)
- [x] **6 CLI tests** (`diagnosecmd_test.go`): No args error, session not found, quick scan output, JSON output, quiet output, broken session with restore advice
- [x] **29 tests total**, all passing, full build clean across entire codebase

### Slack Integration Phase 3 — Identity Matching ✅
- [x] **Identity bounded context** (`internal/identity/`): Clean hexagonal architecture with domain types, ports (SlackClient interface), and service orchestration
- [x] **Domain types** (`domain.go`): `SlackMember`, `Suggestion`, `SyncResult`, `MatchConfidence` (exact/high/medium/low/none), `SuggestionStatus` (pending/approved/rejected), `ScoreToConfidence()`
- [x] **Fuzzy name matcher** (`matcher.go`): 5-strategy matching — exact email, exact name, token overlap (Jaccard similarity), Levenshtein distance, email username vs name tokens. Handles dot/dash/underscore separators, case-insensitive. Pure functions, zero dependencies.
- [x] **Slack API client** (`slack.go`): `SlackClient` port interface + `slackAPIClient` adapter using `net/http` directly (no external deps). Fetches workspace members via `users.list` API with cursor pagination. Filters USLACKBOT. Configurable base URL for testing.
- [x] **Identity service** (`service.go`): `SyncSlackMembers()` orchestrates fetch → match → suggest/auto-link flow. Skips already-linked users, machine accounts. Prevents double-linking (same Slack member matched to multiple Git users). `LinkUser()` for manual linking. Configurable min confidence threshold and auto-link mode.
- [x] **CLI `aisync users sync-slack`**: Fetches Slack members, matches against Git users, displays suggestion table with scores/confidence/reasons. Flags: `--auto-link` (auto-link exact matches), `--min-confidence` (filter threshold), `--dry-run`.
- [x] **CLI `aisync users link`**: Manual identity linking with user existence verification. Args: `<user-id> <slack-id> [slack-name]`.
- [x] **54 new tests**: 33 matcher tests (11 MatchNames, 7 tokenize, 5 tokenOverlap, 4 levenshtein, 5 extractEmailUsername, 4 normalizeName, 9 ScoreToConfidence, 1 min3), 15 service tests (nil guards, sync scenarios, auto-link, filters, sorting, errors, no-double-link), 6 Slack client tests (nil/create, success with member types, pagination, API error, HTTP error)
- [x] **All tests passing** across 114 packages, 0 failures

### Enhanced `aisync restore` — PR, File & Worktree Restore ✅
- [x] **Store — `GetPRByBranch()`**: New `PullRequestStore` method returning the most recent PR for a given branch (`ORDER BY updated_at DESC LIMIT 1`); SQLite implementation + 3 tests
- [x] **RestoreRequest enrichment**: Added `FilePath string`, `Worktree bool` to `service.RestoreRequest` + `client.RestoreRequest`; added `WorktreePath string` to `RestoreResult`; remote session service mapping updated
- [x] **4-step session resolution** in `session_restore.go`: explicit SessionID → `--pr N` (via `GetSessionsForPR`) → `--file path` (via `GetSessionsByFile`) → default branch lookup. Replaces old `GetByLink(LinkPR)` mechanism with rich PR metadata
- [x] **`resolveSessionFromPR()`**: Reads `github.default_owner` / `github.default_repo` from config, queries `PullRequestStore.GetSessionsForPR()`, picks most recent session
- [x] **`resolveSessionFromFile()`**: Queries `GetSessionsByFile(BlameQuery{FilePath})`, picks most recent entry
- [x] **`createWorktree()`**: Creates git worktree at session's `CommitSHA` via `git.WorktreeAdd()`, auto-generates descriptive path (`.worktrees/pr-42-abc12345` or `.worktrees/restore-abc12345`)
- [x] **CLI `--file` flag**: File-based restore — finds the most recent session that touched a file
- [x] **CLI `--worktree` flag**: Creates a git worktree at the session's commit SHA for isolated work
- [x] **Contextual `--pick`**: Works with `--pr` and `--file` — shows table of matching sessions, user picks interactively
- [x] **`pickSessionForPR()` / `pickSessionForFile()` / `promptPickFromSummaries()`**: Contextual pickers with `pickStore` interface for minimal dependency
- [x] **Git client extensions**: `SwitchBranch()`, `WorktreeAdd()`, `WorktreeRemove()` in `git/client.go` + 6 tests
- [x] **MockStore enrichment**: Added `PRSessions map[string][]session.Summary` field, `GetSessionsForPR()` now checks it (supports test data)
- [x] **42 tests total**: 3 SQLite PR store, 6 git client, 10 service restore helpers, 11 CLI pickers/flags, 2 pre-existing PR tests fixed, all passing
- [x] **All tests passing**, full build clean

### Notification System (Slack Integration Phase 2) ✅
- [x] **Hexagonal architecture**: `internal/notification/` bounded context with domain, ports, service, router, and adapters (Slack + webhook)
- [x] **6 event types**: budget.alert, error.spike, session.captured, daily.digest, weekly.report, personal.daily — all with Block Kit formatting
- [x] **Config section**: `notification.*` with Slack (webhook_url, bot_token), webhook (url, secret), routing (default_channel, project_channels), alert/digest toggles, cron schedules
- [x] **Factory + scheduler wiring**: NotificationService built from config, injected into PostCaptureFunc + BudgetCheckTask + DailyDigestTask + WeeklyReportTask + UserKindBackfillTask
- [x] **2,492 tests passing** across 113 packages (70 new notification tests)

### File Explorer Blame Page ✅
- [x] **`blame.html` template**: Full blame page showing all sessions that modified/read a file — change type badges (created/modified/deleted/read), session links, branch pills, provider, time ago, back navigation to file explorer
- [x] **`handleBlame()` handler**: Constructs absolute path from project + relative file, calls `GetSessionsByFile(BlameQuery)`, maps `BlameEntry` to `blameEntryView` with CSS class mapping
- [x] **Route**: `GET /blame?file=...&project=...` registered in `server.go`
- [x] **File explorer redesign**: AI column now shows AI badge linking directly to last session + `+N` link to blame page when >1 session
- [x] **CSS**: `.blame-*` classes (file header, entry list, change type badges with color per type, session links, branch pills, provider/time layout); `.fe-ai-more` for +N links
- [x] **End-to-end verified**: `handlers.go` shows 46 sessions, `funcs.go` 27 sessions (19 modified + 8 read), all links functional

## Previously Completed (2026-04-01)

### Slack Integration Phase 1 — Foundation ✅
- [x] **User struct enriched** (`session.User`): Added `Kind` (human/machine/unknown), `SlackID`, `SlackName`, `Role` (admin/member) fields; `UserKind` and `UserRole` typed constants; `OwnerStat` aggregate struct
- [x] **UserStore enriched** (`storage.UserStore`): 5 new interface methods — `ListUsers()`, `ListUsersByKind(kind)`, `UpdateUserSlack(id, slackID, slackName)`, `UpdateUserKind(id, kind)`, `UpdateUserRole(id, role)`, `OwnerStats(project, since, until)`
- [x] **SQLite migration 027**: Adds `kind`, `slack_id`, `slack_name`, `role` columns to `users` table; creates `idx_sessions_owner_id` index on `sessions(owner_id)` for GROUP BY performance
- [x] **SQLite implementation**: All 6 new methods + rewritten `scanUser()` helper with all 9 columns; `SaveUser` now persists kind/role with defaults; `OwnerStats` via GROUP BY owner_id with LEFT JOIN users
- [x] **Mock stores updated**: `testutil.MockStore` and `service.mockStore` both implement all new UserStore methods
- [x] **Config `owners` section**: New `ownersConf` struct with `MachinePatterns` (email glob patterns); 7 default patterns (`*[bot]@*`, `ci@*`, `bot@*`, `automation@*`, `dependabot*`, `renovate*`, `github-actions*`); `GetMachinePatterns()` getter; `loadFrom()` merge
- [x] **`ClassifyUserKind(email, patterns)`**: Exported pure function in `service/owner.go` — glob matching with `*` wildcard, case-insensitive, machine patterns → machine, noreply without bot → unknown, everything else → human
- [x] **`resolveOwner()` enriched**: Now calls `ClassifyUserKind()` at user creation with configured patterns; sets `Kind` and `Role` on new users
- [x] **CLI `aisync users`**: 5 subcommands — `list` (with `--kind` filter and `--json`), `set-kind`, `set-slack`, `set-role`, `backfill-kind` (with `--dry-run`)
- [x] **`UserKindBackfillTask`**: Scheduler task that reclassifies all users based on current machine patterns; logs updated count
- [x] **50 new tests**: 15 ClassifyUserKind + 18 globMatch, 11 SQLite (kind/role save, defaults, list, list-by-kind, update-slack, update-kind, update-role, owner-stats, owner-stats-empty, get-by-email-new-fields), 4 backfill task (name, run, nil config, empty), CLI validation in set-kind/set-role
- [x] **All 2,422 tests passing** across 109 packages, 0 failures

### Live Preview of Classification Rules ✅
- [x] **Preview endpoint**: `POST /api/settings/project/preview` — parses proposed rules from form, queries all sessions, filters by project name match (remote URL display name, raw URL, project path, basename), applies classification cascade in dry-run mode, returns HTMX partial
- [x] **Classification functions exported**: `MatchBranchRule()`, `MatchConventionalCommit()`, `MatchSummaryPrefix()` in service package — enables reuse in web preview handler
- [x] **`RemoteDisplayName()` exported**: Config helper for extracting `org/repo` from git remote URLs
- [x] **`sessionMatchesProject()`**: Matches sessions to project names via 4 candidate keys (display name, raw URL, path, basename)
- [x] **`classifySessionPreview()`**: Dry-run classification with same priority cascade (commit > branch > agent), detects type and status changes vs current values
- [x] **HTMX partial**: `classification_preview_partial.html` — stats header (sessions, type/status changes), results table with current→new type/status, change highlighting, truncation note for >50 sessions
- [x] **UI integration**: "Preview Rules" button in both edit and new project forms (settings page + HTMX partial), targets inline preview results div, JS `pcPreview()` function with loading state
- [x] **CSS**: `.pc-preview-section`, `.pc-btn--preview`, `.cp-*` classes (header stats, table, change indicators with strikethrough old → highlighted new, truncation message)
- [x] **20 tests**: 7 integration (no store, empty name, no matching sessions, with matching sessions, status rule changes, type change detection, project path match), 6 unit `sessionMatchesProject` (remote URL, raw URL, project path, basename, no match, empty name), 7 unit `classifySessionPreview` (commit priority, branch match, agent match, status match, already classified same/different, no match)

### Elasticsearch Search Adapter ✅
- [x] **`internal/search/elastic/`**: Full `search.Engine` implementation using Elasticsearch 8.x HTTP API (no external Go client dependency — uses `net/http` directly)
- [x] **Index management**: `ensureIndex()` auto-creates index with custom mapping — `code_analyzer` (standard + lowercase + asciifolding), keyword fields for filters, date/integer for metrics
- [x] **Search**: Multi-match query across `summary^3`, `content`, `tool_names`, `branch^2` with BM25 ranking, `fuzziness: AUTO`, highlighted snippets (`<mark>` tags)
- [x] **All filter types**: project_path, remote_url, branch, provider, agent, session_type, project_category, date range (since/until), has_errors — mapped to Elasticsearch bool/filter clauses
- [x] **Faceted aggregations**: Any keyword field can be requested via `FacetFields`, returns bucketed counts (top 20)
- [x] **Incremental indexing**: `IndexedSessionIDs()` via scroll API — enumerates all document IDs in batches of 10K, cleans up scroll context
- [x] **Factory wiring**: `buildSearchEngine()` in `default.go` — `case "elasticsearch"` creates engine with config URL + index, falls back to LIKE on connection error, chains with LIKE fallback
- [x] **Config getters**: `GetElasticsearchURL()` (default `http://localhost:9200`), `GetElasticsearchIndex()` (default `aisync-sessions`)
- [x] **Capabilities**: FullText, Facets, Highlights, FuzzyMatch, Ranking (Semantic=false)
- [x] **16 tests**: All using `httptest.Server` mocks — constructor (create index, index exists, defaults), capabilities, search (empty query, with results + highlights, with filters, with facets, server error), index (document, server error), delete (existing, not found), IndexedSessionIDs (scroll), buildSearchQuery (all filters + facets), close

### Bugfixes ✅
- [x] **`pulls.html` template fix**: Missing `{{end}}` for `{{if .NoPlatform}}` block caused "unexpected EOF" parse error — added closing `{{end}}`
- [x] **Scheduler mock fix**: `mockAnalysisService` in `analyze_task_test.go` was missing `AvailableModules()` method — added stub returning nil

### GitHub Integration — PR ↔ Session Linking ✅
- [x] **Domain enrichment**: `session.PullRequest` extended with `MergedAt`, `ClosedAt`, `RepoOwner`, `RepoName`, `Additions`, `Deletions`, `Comments`; new `session.PRWithSessions` struct
- [x] **Platform port**: `ListRecentPRs(state, limit)` added to `platform.Platform` interface
- [x] **GitHub adapter**: `prJSONFields` constant, enriched `ghPR` struct, `extractRepoFromURL()` helper, `ListRecentPRs()` method, all existing methods enriched
- [x] **Storage layer**: Migration 026 — `pull_requests` table + `session_pull_requests` junction table; `PullRequestStore` interface (Save, Get, List, Link, GetSessionsForPR, GetPRsForSession, ListPRsWithSessions); full SQLite implementation
- [x] **Config**: `github.token`, `github.default_owner`, `github.default_repo`, `github.sync_enabled`, `github.sync_cron` with Get/Set/loadFrom merge, cron validation
- [x] **Scheduler task**: `PRSyncTask` — fetches PRs via platform, saves to store, links sessions by branch name; wired in serve.go scheduler when `github.sync_enabled` is true
- [x] **`/pulls` page**: Filter by state (open/merged/closed), KPI strip, PR cards with state badges, +/- stats, expandable session tables, empty state with setup instructions
- [x] **Session detail PR badge**: Linked PRs shown as colored badges (green/purple/red) with repo/number, linking to GitHub
- [x] **CSS**: ~180 lines of `.pr-*` and `.detail-prs` classes
- [x] **Navbar**: "PRs" link added
- [x] **24 tests**: 9 SQLite PR store (save, get, list, link, sessions-for-PR, PRs-for-session, list-with-sessions, upsert, merged-at), 5 PR sync task (name, nil platform, fetch+save+link, platform error, skip missing repo), 8 extractRepoFromURL + enriched toDomain, 5 config (set/get, getters, defaults, invalid cron, save/reload)

### Project Setup & Onboarding — `aisync project` Command Group ✅
- [x] **Config enrichment**: `ProjectClassifierConf` extended with 5 new fields — `DefaultBranch`, `PREnabled`, `GitRemote`, `Platform`, `ProjectPath`; JSON serialization via existing `loadFrom`/`Save` flow; no new Get/Set cases needed (uses `SetProjectClassifier()` directly)
- [x] **Git client extensions**: `DefaultBranch()` (symbolic-ref + fallback to common names), `ListBranches()` (for-each-ref), `RepoDir()` accessor
- [x] **`aisync project init` wizard**: Auto-detects git remote, default branch, platform (GitHub/GitLab/Bitbucket) from remote URL; interactive prompts (name, branch, PR tracking, budget, tags) with defaults; `--no-prompt` flag for CI/scripting; `--name`, `--branch`, `--pr-enabled`, `--budget`, `--tags` flags; shows session count from DB; saves to `~/.aisync/config.json`
- [x] **`aisync project list`**: Merges DB-discovered projects (from sessions) with config-defined projects; table output with columns: project, sessions, branch, PRs, budget, status (configured/unconfigured); sorted: configured first, then alphabetical
- [x] **`aisync project show`**: Displays full project config — path, remote, platform, branch, PR sync, sessions, budget (monthly+daily+alert), rules (tickets, branch, agent, tags); auto-detects from current directory or accepts explicit name argument
- [x] **`aisync project sync-prs`**: Manual PR sync for current directory; creates GitHub platform client, fetches PRs via `gh` CLI, saves to store, links sessions by branch
- [x] **PRSyncTask multi-repo refactor**: `NewMultiRepoPRSyncTask(cfg, store, logger)` — iterates all projects with `pr_enabled: true`, creates per-project GitHub client, syncs PRs independently; backward-compatible `NewPRSyncTask()` preserved for single-platform mode; `savePRs()` extracted as shared helper; serve.go auto-selects multi-repo when any project has `pr_enabled`
- [x] **Registered in root.go**: `projectcmd.NewCmdProject(f)` added to root command
- [x] **18 tests**: 4 init wizard (non-interactive, with PR+budget, interactive prompts, update existing), 2 list (empty, with projects), 2 show (not configured, fully configured), 3 utility (platformDisplayName, truncate, prompt/promptYesNo), 5 existing PRSync tests still passing
- [x] **End-to-end verified**: `aisync project init --no-prompt --pr-enabled --tags "devtools, go, cli"` → auto-detected aisync repo, 220 sessions, saved config; `aisync project list` → 29 projects (3 configured, 26 unconfigured); `aisync project show` → full config display with budget, rules, sessions

### Session Conversation Filters: Skills + Assistant ✅
- [x] **Skill detection**: `detectSkillsPerMessage()` — scans messages for skill loads via tool calls (`name == "skill"`, parses JSON input for skill name) and content tags (`<skill_content name="xxx">` in assistant messages)
- [x] **Handler integration**: `handleSessionDetail()` now builds `SkillMap` (message index → skill names), `SkillCount`, `UniqueSkills` (sorted list)
- [x] **New filter buttons**: "Assistant" (filter by role), "Skills (N)" (filter messages with any skill), per-skill chip filters (click "replay-tester" → shows only messages with that skill)
- [x] **Skill badges on messages**: Purple `badge-skill` badges in chat header, clickable → triggers skill-specific filter
- [x] **Visual indicators**: `has-skills` CSS class on messages with skills → purple left border; `.conv-skill-chips` bar with per-skill pill buttons
- [x] **JS filter logic**: Extended `getFiltered()` with `assistant`, `skills`, and `skill:<name>` filters; active state synced across buttons + chips
- [x] **7 filter types total**: All, User, Assistant, Errors, Tools, Skills (any), Skill:specific-name
- [x] **CSS**: `.badge-skill`, `.conv-btn-skill`, `.conv-chip`, `.conv-chip-skill`, `.conv-skill-chips`, `.chat-msg.has-skills` — purple theme consistent with skill styling elsewhere
- [x] **7 tests**: no skills, tool call skill, content tag skill, multiple skills one message, mixed detection, extractSkillNameFromInput (5 cases), extractSkillContentNames (4 cases)
- [x] **All 2,372 tests passing** across 87 packages (107 total including no-test-file packages)

### Branch Session Tree (2.1) ✅
- [x] **Enhanced handler**: `handleBranchTimeline` now calls `ListTree()` for parent-child hierarchy + `GetForkRelations()` for fork connections on top of the existing flat timeline
- [x] **View models**: `branchTreeNodeView` (ID, summary, agent, provider, type, status, tokens, errors, depth, indent, fork flag), `branchTreeStats` (total sessions, roots, forks, max depth), `branchForkView` (original→fork with fork point + shared msgs)
- [x] **Tree builder**: `buildBranchTreeNodeView()` recursive — converts `SessionTreeNode` to view with pre-computed `IndentPx`, status CSS class, summary truncation; counts forks and max depth
- [x] **Helpers**: `countTreeNodes()`, `collectTreeIDs()`, `truncateID()`
- [x] **Template**: `branch_timeline.html` — Session Tree section (stats strip, indented tree nodes with connector lines/dots, color-coded by status, fork badges, error counts) + Fork Relationships panel + existing Timeline section
- [x] **CSS**: `.bt-*` classes (~120 lines) — tree nodes with indent, connector lines (`bt-connector-fork`/`bt-connector-line`), colored dots (active/completed/review/idle/error), fork badges, stats strip, fork entry links
- [x] **Sidebar**: Added `SidebarProjects` to branch timeline page for consistent navigation
- [x] **6 tests**: empty branch, branch with parent-child sessions (verifies tree rendering), redirect on empty name, `buildBranchTreeNodeView` (depth/indent/status/truncation/children), `countTreeNodes`, `truncateID`
- [x] **All 2,273 tests passing** across 105 packages

### Settings CRUD for Project Classifiers & Budgets ✅
- [x] **Config layer**: `SetProjectClassifier(name, pc)` and `DeleteProjectClassifier(name)` methods — validates regex patterns, budget limits (non-negative, 0-100 alert %), cost mode enum; `ProjectBudgetConf` exported
- [x] **API endpoints**: `POST /api/settings/project` (create/update), `DELETE /api/settings/project` (delete) — full form parsing for all `ProjectClassifierConf` fields including nested rule maps and budget
- [x] **Form parsing**: `parseRulesFromForm()` — reads repeated `prefix_pattern[]`/`prefix_type[]` form fields into `map[string]string`; handles branch, agent, commit, and status rules
- [x] **View models**: `projectClassifierView` + `ruleView` structs, `buildProjectClassifierViews()` builder (sorted by name, formatted budget values)
- [x] **Settings page**: Replaced read-only display with full CRUD — per-project cards with read/edit toggle, inline forms for all fields (ticket pattern/source/URL, tags, 4 rule types, budget with 5 fields), add/delete buttons
- [x] **HTMX partial**: `project_classifiers_partial.html` — standalone partial for HTMX responses after save/delete, identical structure to settings page section
- [x] **CSS**: `.pc-*` classes — card layout, read-only fields, form inputs, rule rows with dynamic add/remove, fieldsets, budget fields, action buttons (edit/delete/save/cancel/add)
- [x] **JavaScript**: `pcToggleEdit()`/`pcCancelEdit()` for view↔edit toggle, `addRuleRow()` for dynamic rule row creation
- [x] **18 tests**: 7 config tests (basic set, empty name, invalid regex, invalid budget, delete, delete not found, persist roundtrip) + 11 web tests (new project, with rules, with budget, update existing, empty name, invalid regex, delete, delete not found, persist to disk, no config, settings page display, tags)
- [x] **All 2,267 tests passing** across 105 packages

### File Explorer Page ✅
- [x] **Domain struct**: `ProjectFileEntry` in `fileops.go` — `FilePath`, `SessionCount`, `WriteCount`, `LastChangeType`, `LastSessionID`, `LastSessionTime`, `LastSummary`, `LastBranch`, `LastProvider`
- [x] **Store interface**: `FilesForProject(projectPath, dirPrefix, limit)` added to `SearchStore` in `store.go`
- [x] **SQLite implementation**: CTE-based query in `sqlite.go` — aggregates per file, joins back to get last session info, filters to project-only paths
- [x] **Handler**: `handleFileExplorer()` — two-pass directory aggregation, project-forced redirect, clickable breadcrumbs, garbage path filtering
- [x] **Template**: `file_explorer.html` — project selector, breadcrumb navigation, KPI strip (files/directories), directory grid cards, file table (name, sessions, last change, session link, branch, when)
- [x] **CSS**: `.fe-*` classes — directory grid, file cells, breadcrumbs, summary ellipsis
- [x] **Route**: `GET /files/{path...}` with `?dir=` parameter for directory navigation
- [x] **Navbar**: "Files" link added
- [x] **Project detail**: "Explore →" link in Top Files panel
- [x] **5 unit tests**: basic, multiple sessions, dir prefix, exclude outside paths, empty project
- [x] **All 2,200 tests passing** across 105 packages

## Previously Completed (2026-03-31)

### Performance Optimization — Background Caching & Scheduler ✅
- [x] **`SaturationTask`** (`saturation_task.go`): Pre-computes `ContextSaturation()` for global + all projects every 2h, writes to cache table
- [x] **`CacheEfficiencyTask`** (`cacheeff_task.go`): Pre-computes `CacheEfficiency()` for 7d + 90d windows, global + per-project, every 2h
- [x] **`cachedSaturation()`**: Cache-first accessor with 2h TTL, cold-cache fallback to live compute
- [x] **`cachedCacheEfficiency()`**: Cache-first accessor with 2h TTL, dual window support (7d/90d)
- [x] **`cachedTrends()`**: Cache-first accessor with 5min TTL for Trends() (2 full scans per call)
- [x] **`cachedSidebarGroups()`**: Cache-first accessor with 2min TTL — `ListProjects()` called on 11+ pages
- [x] **`cachedCostsPage()`**: Caches full cost dashboard struct (60s TTL) — prevents 3 tab partials from recomputing
- [x] **`StatsReportTask` enhanced**: Now warms global + per-project stats (was global only), writes directly to cache table
- [x] **3 unwired tasks wired**: `backfill_remote_url` (daily 4:00), `fork_detection` (daily 4:30), `budget_check` (hourly :50)
- [x] **12 new tests**: 7 SaturationTask + 5 CacheEfficiencyTask (name, defaults, global-only, with-projects, error, non-fatal project list error)
- [x] **Scheduler now has 10 tasks** (was 6), all cron-scheduled with staggered times to avoid contention
- [x] **All 84 packages passing**, 0 regressions

### Model Fitness Profiles (6.4) — Sprint E ✅
- [x] **Domain** (`fitness.go`): `ModelFitnessProfile`, `TaskTypeProfile`, `FitnessAnalysis`, `SessionFitnessData` structs — per-task-type model fitness scoring
- [x] **`AnalyzeModelFitness()`**: Pure domain function — groups sessions by (task_type, model), computes fitness scores, ranks models within each task type, generates recommendations
- [x] **`computeFitnessScore()`**: 4-component composite (error discipline 25pts, retry discipline 25pts, cost efficiency 25pts via log10, productivity 25pts) — 0-100 scale with grade via `scoreToGrade()`
- [x] **`buildFitnessRecommendations()`**: Model switching (fitness gap >15pts), cost saving (>50% cheaper at similar fitness), high error rate (>30%) — capped at 5 recommendations
- [x] **`log10Approx()`**: Custom log10 without `math` import — iterative digit-count approach
- [x] **Service integration**: `ContextSaturation()` collects `SessionFitnessData` per session (model, task type, cost, messages, errors, retries, total tokens), aggregates via `AnalyzeModelFitness()`, stores in `result.Fitness`
- [x] **View structs**: `fitnessTaskTypeView`, `fitnessModelView` with pre-computed formatted fields; 9 new fields on `costDashboardPage`
- [x] **Dashboard**: "Model Fitness by Task Type" section — per-task-type expandable tables (model, sessions, avg cost, avg msgs, err%, retry%, fitness grade), recommendations list
- [x] **17 unit tests**: empty input, no valid data, single model/type, multiple models, multiple task types, error rate impact, retry rate impact, perfect score, terrible score, bounds check, log10 approx (2 cases), recommendations (empty, significant gap, cost saving, max five), avg metrics verification
- [x] **All tests passing** (84 packages, 0 failures)

### CLAUDE.md Impact Analysis (5.3) — Sprint E ✅
- [x] **Domain**: `SystemPromptEstimate()` — estimates system prompt tokens from first assistant message's `CacheWriteTokens` (Method A) with fallback to `InputTokens - roughTokenEstimate(userContent)` (Method B); `roughTokenEstimate()` — `len(content)/4` approximation
- [x] **Domain**: `SystemPromptImpact` struct — aggregate analysis with size buckets (small <3K, medium 3K-8K, large >8K), quality correlation (error rate, retry rate per bucket), trend detection, cost impact
- [x] **Domain**: `SessionPromptData` struct — per-session input data; `AnalyzeSystemPromptImpact()` — pure domain function computing basic stats, size bucket distribution, quality correlation, trend analysis (first vs last quarter), recommendation engine
- [x] **Domain**: `computePromptTrend()` — compares first vs last quarter of sessions (>20% change = growing/shrinking); `buildPromptRecommendation()` — generates actionable advice based on size, trend, error correlation
- [x] **Service integration**: `ContextSaturation()` collects `SessionPromptData` per session (prompt estimate, total input, error rate, retry rate), calls `AnalyzeSystemPromptImpact()`, stores in `result.PromptImpact`
- [x] **Dashboard**: "System Prompt Impact (CLAUDE.md / MCP)" section — KPIs (avg/median size, range, cost %, trend), quality-by-prompt-size table (error/retry rates per bucket), recommendation box
- [x] **22 unit tests**: empty messages, no assistant, Method A cache write, cache write edge case, Method B input-minus-user, Method B too small, fallback raw input, fallback too small, rough token estimate (4 cases), impact analysis (empty, all zero, basic stats, size buckets, cost pct), trend (growing, stable, shrinking, too few), recommendations (large+errors, growing, very large, normal range), format helpers
- [x] **All tests passing** (84 packages, 0 failures)

### Multi-Benchmark Aggregation (6.3) — Sprint E ✅
- [x] **Domain types**: `BenchmarkScore`, `CompositeWeights`, `CompositeEntry` structs; new sources `SourceSWEBench`, `SourceToolBench`, `SourceArenaELO`, `SourceHumanEval`
- [x] **`MultiCatalog` port interface**: Extends `Catalog` with `LookupScores()`, `CompositeScore()`, `Sources()` — supports multi-source benchmark aggregation
- [x] **`MultiEmbeddedCatalog`**: go:embed adapter loading multiple YAML files from `data/` directory + original Aider catalog; configurable weights; weight renormalization for missing sources
- [x] **Benchmark data files**: `swe_bench.yaml` (15 models, real GitHub issue resolution), `toolbench.yaml` (16 models, tool/API usage), `arena_elo.yaml` (18 models, human preference ELO normalized to 0-100)
- [x] **Default weights**: Aider 40%, SWE-bench 30%, ToolBench 20%, Arena ELO 10% (per NEXT.md spec)
- [x] **Recommender updates**: `Benchmarks()` accessor, auto-populates `CurrentScores`/`AltScores` on `ModelAlternative` and `Scores`/`SourceCount` on `QACLeaderEntry` when `MultiCatalog` is detected
- [x] **Factory wiring**: `BenchmarkRecommenderFunc` now uses `NewMultiEmbeddedCatalog` instead of `NewEmbeddedCatalog`
- [x] **Dashboard**: "Multi-Benchmark Composite" header with source weight badges; per-source mini-badges in Score and Sources columns for both Model Alternatives and QAC Leaderboard tables; conditional rendering for single vs multi benchmark mode
- [x] **CSS**: `.bench-badge`, `.bench-mini`, `.bench-breakdown` styles with per-source colors (Aider blue, SWE-bench purple, ToolBench amber, Arena ELO green)
- [x] **14 unit tests**: multi-catalog creation, composite lookup, multi-source scores, alias lookup, not-found, weight renormalization, all-sources composite, custom weights, list sorting, composite entries, default weights sum, recommender with multi-benchmark, empty composite, source sorting
- [x] **All tests passing** (84 packages, 0 failures)

### Session Freshness & Diminishing Returns (2.3) — Sprint D ✅
- [x] **Domain**: `SessionFreshness`, `FreshnessPhase`, `FreshnessAggregate`, `CompactionDepthStats` structs in `freshness.go`
- [x] **`AnalyzeFreshness()`**: Pure domain function — segments session into phases bounded by compaction events, measures error rate / retry rate / output ratio per phase, detects quality degradation, finds optimal message index via sliding window
- [x] **`AggregateFreshness()`**: Combines per-session results — averages error growth, retry growth, output decay; builds per-compaction-depth quality stats (depth 0, 1, 2, 3+)
- [x] **Recommendation engine**: `buildFreshnessRecommendation()` and `buildAggregateRecommendation()` generate actionable guidance based on error rate growth, output decay, compaction count
- [x] **Service integration**: `ContextSaturation()` calls `AnalyzeFreshness()` per session, aggregates via `AggregateFreshness()`, stores in `result.Freshness`
- [x] **Dashboard**: "Session Freshness & Diminishing Returns" section — KPIs (compacted %, avg compactions, error rate growth, output decay, optimal length), per-depth quality table, recommendation box
- [x] **10 unit tests**: no compaction, one compaction, multiple compactions, empty session, optimal message idx, aggregate basic, aggregate empty, depth stats, recommendation variants

### Token Waste Classification (4.2) — Sprint D ✅
- [x] **Domain**: `TokenWasteBreakdown`, `WasteBucket`, `WasteCategory` structs in `waste.go`
- [x] **`ClassifyTokenWaste()`**: Pure domain function — classifies session tokens into productive, retry, compaction, cache_miss, idle_context categories
- [x] **`AggregateWaste()`**: Combines waste breakdowns across sessions with re-computed percentages
- [x] **`identifyRetryMessages()`**: Detects retry of failed tool calls (same tool called after error)
- [x] **`identifyCacheMissMessages()`**: Detects assistant msgs after >5min cache TTL expiry from last assistant response
- [x] **Service integration**: `ContextSaturation()` calls `ClassifyTokenWaste()` per session, aggregates via `AggregateWaste()`, stores in `result.TokenWaste`
- [x] **Dashboard**: "Token Waste Classification" section — pie chart (conic-gradient), 5-category KPI cards with colored borders, waste % summary
- [x] **10 unit tests**: empty session, all productive, retries, compaction, cache miss, idle context, percentages, aggregate, retry messages, cache miss messages

### Model Context Efficiency Score (3.1) — Sprint C ✅
- [x] **Domain**: New fields on `ModelSaturation`: `TotalInputTokens`, `TotalOutputTokens`, `TotalCost`, `TotalMessages`, `TotalToolErrors`, `TotalToolCalls`, `ErrorRate`, `CostPer1KOutput`, `OutputPerDollar`, `AvgOutputRatio`, `EfficiencyScore` (0-100), `EfficiencyGrade` (A-F)
- [x] **`ComputeModelEfficiency()`**: Pure domain function in `efficiency.go` — computes error rate, cost per 1K useful output (adjusted for error overhead), output per dollar, output/input ratio, composite 0-100 score with 4 components (context utilization, output productivity, error discipline, cost efficiency via log-scaled output/dollar)
- [x] **`contextUtilizationScore()`**: Rewards sweet-spot 40-60% utilization (25 pts), penalizes oversized (<10% → 5 pts) and saturated (>90% → 5 pts)
- [x] **Service integration**: `ContextSaturation()` now accumulates per-model token counts, cost (via `SessionCost()`), tool call/error counts during the session loop, then calls `ComputeModelEfficiency()` in finalization
- [x] **Dashboard**: Redesigned "Saturation & Efficiency by Model" table — added Cost/1K Out, Out/$, Error%, Out/In ratio, composite Score (grade badge with 0-100 value). Removed Context Window/Max Peak/Unused columns for cleaner layout.
- [x] **View structs**: Extended `modelSaturationView` with formatted `CostPer1KOutput`, `OutputPerDollar`, `ErrorRate`, `AvgOutputRatio`, `EfficiencyScore`, `EfficiencyGrade`, `GradeClass`
- [x] **9 unit tests**: basic metrics, zero cost, high error rate, oversized model, no tool calls, context utilization score (11 data points), score-to-grade (10 data points), clamp (5 data points)
- [x] **All 2,090 tests passing** across 105 packages

### Context Saturation Forecast (2.2)
- [x] **Domain**: `SaturationForecast`, `ModelSaturationForecast`, `HistogramBucket`, `SessionForecastInput` structs in `forecast.go`
- [x] **`ForecastSaturation()`**: Pure domain function — computes per-model predictions from session token growth data, histogram of messages-before-compaction, linear extrapolation to 80%/100% thresholds
- [x] **`forecastRecommendation()`**: Generates actionable guidance per model (split tasks, underutilized, well-sized, etc.)
- [x] **`buildCompactionHistogram()`**: Bucketed distribution (20-msg buckets) with trailing empty trim
- [x] **Service integration**: `ContextSaturation()` now collects `SessionForecastInput` per session (avg token growth, first compaction msg index), calls `ForecastSaturation()`, populates `Forecast` field
- [x] **Dashboard**: New "Saturation Forecast" section in cost optimization partial — KPIs (growth/msg, avg msgs to compaction, compacted rate), per-model table (predicted msgs to 80%/100%, recommendations), messages-to-compaction histogram
- [x] **View structs**: `modelForecastView`, `histogramBucketView`, 11 new fields on `costDashboardPage`
- [x] **CSS**: `.forecast-histogram`, `.forecast-hist-bar`, `.forecast-hist-fill` (purple bars), `.forecast-hist-count`, `.forecast-hist-label`
- [x] **12 unit tests**: empty input, single session, compacted sessions, multi-model, histogram, skip invalid, recommendation variants, peak cap, mean/median/histogram helpers, itoa
- [x] **All tests passing** across 105 packages

### Skill Context Footprint Tracking (1.1)
- [x] **`EstimatedTokens` / `ContentBytes`** added to `SkillLoadDetail` event struct
- [x] **`extractSkillContentTagsWithSize()`**: Measures actual text between `<skill_content>` open/close tags, returns name + content bytes
- [x] **Token estimation**: Content tags use `len(content)/4`, tool calls use `len(tc.Output)/4`
- [x] **`SkillTokens map[string]int`** added to `EventBucket` — tracks per-skill token footprint
- [x] **Bucket aggregation**: `addEvent()` accumulates skill tokens, `MergeBuckets()` merges maps
- [x] **`SkillROIAnalysis()`** updated — uses measured avg tokens instead of hardcoded 2000 when data available
- [x] **Dashboard**: "Skills by Context Cost" table in analytics events page (name, loads, avg tokens, total tokens, share %)
- [x] **9 new tests**: `TestExtractSkillContentTagsWithSize` (6 sub-tests), `TestSkillContentTagEvent_PopulatesTokens`, `TestSkillToolCallEvent_PopulatesTokens`
- [x] **2,052+ tests passing** across 105 packages

### Compaction Detection & Cost (2.1)
- [x] **Domain types**: `CompactionEvent`, `CompactionSummary` structs with token loss, drop percent, cache invalidation, rebuild cost
- [x] **DetectCompactions()**: Pure domain function — token-drop heuristic (>50% drop with >10K baseline), cache invalidation confirmation, sawtooth cycle detection, fill/recovery stats
- [x] **20 unit tests**: No compaction, single, sawtooth, cache invalidation, exact threshold, cost estimation, message indices, fill/recovery stats, production-like scenario, median helpers
- [x] **Service integration**: Replaced boolean `hasCompaction` in both `session_saturation.go` and `session_usage.go` with `DetectCompactions()` call — now tracks per-event data, costs, and aggregates
- [x] **Compaction labels on saturation curve**: Points annotated with "⚡ Compaction (170K lost)" label at compaction events
- [x] **Per-session UI** (saturation_partial.html): Purple compaction markers on bar chart, KPI row (count, tokens lost, avg drop, sawtooth cycles, rebuild cost, msgs to fill), legend entry
- [x] **Project-level UI** (cost_optimization_partial.html): "Compaction Impact" section with aggregate KPIs (total events, tokens lost, avg drop, total rebuild cost)
- [x] **New domain fields**: `ContextSaturation.TotalCompactionEvents`, `TotalTokensLost`, `TotalRebuildCost`, `AvgDropPercent`
- [x] **CSS**: `.sat-compaction` (purple #a855f7), `.sat-row-compaction`, `.compaction-summary`, `.compact-kpi-row`, `.compact-kpi`
- [x] **2,052 tests passing** across 105 packages

### Cost Breakdown Charts (2.3)
- [x] **MCP pie chart**: `CostPercent` on `mcpServerView`, conic-gradient donut chart computed server-side, `template.CSS` for HTML safety
- [x] **10-color palette**: `--pie-color-0` through `--pie-color-9`, `.mcp-pie` donut with inner cutout via `::after`
- [x] **Budget overlay**: Dashed red line on Cost Over Time chart at daily budget sum, bar height rescaling
- [x] **Threshold**: Pie chart shown with ≥1 MCP server (was 2)

### Settings Inline CRUD (3.4)
- [x] **4 input types**: toggle (CSS checkbox switch), select (dropdown), text, number — all with HTMX POST
- [x] **POST /api/settings**: Validates via `Config.Set()`, persists via `Config.Save()`, returns partial HTML with "saved" fade-out
- [x] **~25 editable keys**: storage_mode, auto_capture, features.file_blame, search.engine, analysis.adapter, analysis.model, dashboard.page_size, scheduler.gc.enabled, etc.
- [x] **Template functions**: `settingID` (dots→dashes for HTML IDs), `split` (comma-separated options)
- [x] **CSS**: `.settings-toggle`, `.settings-toggle-slider`, `.settings-inline-form`, `.settings-save-btn`, `.settings-saved-indicator` with fade-out animation
- [x] **8 tests**: toggle, select, text, number, missing key, invalid value, no config (503), persistence to disk

### Activity Sparklines (2.2)
- [x] **Sparkline data model**: `sparklineBar` struct with `Value`, `HeightPct` (0-100%), `Label`
- [x] **buildSparklineBars / buildSparklineBarsFloat**: Pure functions converting raw values to percentage-height bars (min 2% for non-zero)
- [x] **Dashboard sparklines**: 14-day mini bar charts under Sessions, Tokens, Cost, and Errors KPI strips
- [x] **Project detail sparklines**: Same 14-day sparklines on project KPI strip
- [x] **Data source**: `store.QueryTokenBuckets("1d")` — direct store access (avoids remote-mode "not supported")
- [x] **Day aggregation**: Multiple providers/backends aggregated per day, sorted chronologically
- [x] **CSS**: `.sparkline`, `.sparkline-bar`, color variants (`--sessions`, `--tokens`, `--cost`, `--errors`)
- [x] **KPI layout**: `.kpi-strip-item--with-sparkline` column layout with `.kpi-strip-top` row

### Settings Web UI (3.4)
- [x] **`/settings` route**: New page accessible via navbar gear icon
- [x] **11 configuration sections**: General, Features, Search, Analysis, Tagging, Secrets, Errors, Dashboard, Server, Scheduler, Project Classifiers
- [x] **Read-only display**: All config values shown with descriptions, enabled/disabled color coding
- [x] **Config getters added**: `GlobalDir()`, `IsAutoCapture()`, `IsScanToolOutputs()`, `GetAllProjectClassifiers()`
- [x] **Navbar**: Settings gear icon added to layout navigation bar
- [x] **CSS**: `.settings-grid`, `.settings-section`, `.settings-row` — responsive 2-column grid
- [x] **Footer**: CLI usage hint (`aisync config set <key> <value>`)
- [x] 2,024 tests passing across 105 packages

### Costs Page Organization (2.5)
- [x] **HTMX tab navigation**: Overview / Tools & Agents / Optimization — lazy-loaded via partials
- [x] **Overview tab**: Real costs, Budgets, Cache Efficiency, Backend Breakdown, API-Equivalent KPIs, Cost Over Time
- [x] **Tools & Agents tab**: MCP Servers, MCP Governance, MCP Matrix, Top 20 Tools, Agents, Branches
- [x] **Optimization tab**: Context Saturation, Model Breakdown, Model Alternatives (Aider Benchmark), QAC Leaderboard
- [x] **Page size**: 808-line monolithic template → 75-line shell + 3 partials (~230, ~250, ~230 lines)
- [x] **Initial render**: 177KB → ~15KB (content lazy-loaded on tab click)
- [x] **CSS**: Tab styling with active indicator, hover states, HTMX loading state
- [x] **Project filter preserved**: `?project=` query param flows through to all tab partial URLs

### Advanced Search UI (1.3)
- [x] **FTS5 highlights surfaced**: `<mark>` tags from FTS5 `highlight()`/`snippet()` now rendered in search results
- [x] **Content snippets**: `search-result-snippet` shows highlighted excerpt from message content (up to 40 tokens)
- [x] **Engine badge**: "fts5" badge + result count displayed at top of search dropdown
- [x] **Rich metadata per result**: project name, branch, session type badge, error count — all shown
- [x] **Domain preservation**: `session.SearchHighlight` struct carries highlights without `search` import in domain
- [x] **Service layer**: `searchViaEngine()` now populates `Highlights` map and `Engine` field in `SearchResult`
- [x] **Template safety**: Highlights rendered via `template.HTML` for safe `<mark>` tag output
- [x] 2,013 tests passing across 105 packages

---

## Completed (2026-03-29)

### Session File Blame (Backend + Web Views)
- [x] **Domain extractor**: `ExtractFileOperations()` — parses Write/Edit/Read/Bash tool calls, bash heuristics (rm/touch/cp/mv/sed), merge dedup
- [x] **Storage**: Migration 024 (`tool_name` column, index), `ReplaceSessionFiles()`, `GetSessionFileChanges()`, `CountSessionsWithFiles()`, `TopFilesForProject()`
- [x] **Service**: `ExtractAndSaveFiles()`, `BackfillFileBlame()`, `GetSessionFiles()`
- [x] **Config**: `features.file_blame: true` opt-in toggle
- [x] **Post-capture hook**: auto-extraction after classification when file_blame enabled
- [x] **CLI**: `aisync backfill files` subcommand, `aisync blame <file>` already worked
- [x] **Web — Session detail**: File Changes section populated from `file_changes` table (496 entries rendered), shows tool name, color-coded badges
- [x] **Web — Project detail**: "Top Files" aside panel with session count and write count
- [x] **Web — Stats**: TopFiles now uses `file_changes` table instead of empty provider-supplied FileChanges
- [x] 40,343 file records from 1,342 sessions, 2,014 tests passing, 58 extractor tests

---

## Previously Completed (2026-03-27)

### Per-Tool Cost Analytics
- [x] `tool_usage_buckets` table — per-tool token/cost tracking with MCP classification
- [x] Tool classification: `builtin` vs `mcp:notion`, `mcp:sentry`, `mcp:langfuse`, `mcp:context7`
- [x] Cost dashboard: Cost by MCP Server, Cost by Tool (top 20), Cost by Agent
- [x] 2,609 tool buckets from 1,278 sessions

### Prompt Cache Efficiency
- [x] Per-message `CacheReadTokens` / `CacheWriteTokens` on Message struct
- [x] Cache efficiency service: hit rate, savings, waste, gap detection (>5min = cache miss)
- [x] Dashboard: Cache Efficiency panel (hit rate 94.5%, $288K savings, $1.1K waste)
- [x] Home + Project Detail: Cache mini-panel (7d window)

### Per-Project Classifiers
- [x] `projects` config map with `ticket_pattern`, `ticket_url`, `branch_rules`, `agent_rules`, `commit_rules`
- [x] Ticket extraction: regex from branch/summary → `LinkTicket` links
- [x] Smart classification cascade: commit message > summary keyword > branch rules > agent rules
- [x] Conventional Commits: `fix:` → bug, `feat:` → feature, `refactor:` → refactor
- [x] Status detection: `[WIP]` → active, `[DONE]` → completed, `[PR]` → review
- [x] Backfill: 866 sessions classified (32 → 898 typed)

### Per-Project Budget System
- [x] Budget config: `monthly_limit`, `daily_limit`, `alert_at_percent`
- [x] Budget service: actual spend vs limit, progress bars, projected end-of-month
- [x] Costs page: Project Budgets table with colored progress bars
- [x] Project detail: Budget aside panel with monthly/daily bars
- [x] Webhook: `budget.alert` event, scheduler `BudgetCheckTask`

### Pluggable Search Engine (FTS5)
- [x] `search.Engine` port interface with Capabilities
- [x] Chain fallback: FTS5 → LIKE
- [x] FTS5 adapter: full-text in summary + message content + tools + branch, BM25 ranking, highlights
- [x] Config: `search.engine: "fts5"` — single switch
- [x] Post-capture indexing + bulk `IndexAllSessions()`
- [x] 1,325 sessions indexed, search now finds content inside messages

### Dashboard Enhancements
- [x] Project column in sessions table (links to project page)
- [x] Session ID truncation 8→12 chars
- [x] Fix session detail layout (CSS `:has()` for no-sidebar pages)
- [x] Forecast breakdown: subscription vs API spend
- [x] HTMX search bar on project detail Recent Sessions
- [x] Word-boundary truncation for summaries

### Stats
- [x] 1,936 tests passing across 103 packages
- [x] ~4,600 lines added in this session

---

## Priority 0: Agent Efficiency & Observability

### 0.1 Context Saturation Monitor ⭐ HIGH IMPACT
Detect when sessions reach the "degradation zone" of the model's context window.

**What it does:**
- Track cumulative input tokens per message in each session
- Define quality zones per model:
  - 🟢 **Optimal** (0-40% of context): full quality, fast responses
  - 🟡 **Degraded** (40-80%): quality starts dropping, more hallucinations
  - 🔴 **Critical** (80-100%): significant quality loss, compaction risk
- Per-project KPIs:
  - Average messages before reaching 80% context
  - % of sessions that enter degraded/critical zone
  - Tokens "wasted" in the critical zone (expensive + low quality)
  - Compaction events detected
- Session detail: context saturation curve (tokens per message, cumulative)
- Recommendation: "Your Omogen sessions reach 80% in 4 messages avg — split tasks"

**What causes saturation — breakdown:**
- System prompt / agent instructions (fixed per session start)
- Skill system prompts loaded at init (can be very large: 2-5K tokens each)
- AGENTS.md / CONTEXT.md injected context
- Cumulative conversation (each message adds to context)
- Tool call results (bash output can be huge)
- Per session: show "init overhead" (tokens before first user message) vs "conversation growth"

**Session detail view:**
- Context saturation curve: X=message #, Y=cumulative tokens, colored zones
- Annotated: "Skill loaded here (+3K)", "Bash output (+8K)", "Compaction triggered"
- Init overhead badge: "This session starts at 15K tokens before your first message"

**Model context limits:**
| Model | Context Window | Optimal Zone | Degraded Zone | Critical Zone |
|-------|---------------|-------------|--------------|--------------|
| Opus 4.6 | 1M tokens | 0-200K | 200-600K | 600K-1M |
| Sonnet 4.6 | 1M tokens | 0-200K | 200-600K | 600K-1M |
| Opus 4.0/4.1 | 200K tokens | 0-80K | 80-160K | 160-200K |
| Haiku 4.5 | 200K tokens | 0-80K | 80-160K | 160-200K |

### 0.2 Agent & Skill ROI Dashboard ⭐ HIGH IMPACT
Measure the return on investment of each agent and skill per project.

**Agent ROI:**
| Metric | Description |
|--------|-------------|
| Cost/session | Average estimated cost per session for this agent |
| Error rate | % of tool calls that error for this agent |
| Context efficiency | % of context used productively (not wasted in degraded zone) |
| Completion rate | % of sessions that reach [DONE] or [COMMIT] status |
| Avg messages | Average messages before task completion |
| ROI score | Composite score (low cost + low errors + high completion = high ROI) |

**Skill ROI:**
| Metric | Description |
|--------|-------------|
| Usage frequency | How often the skill is loaded |
| Context cost | Tokens consumed by the skill's system prompt/context |
| Error correlation | Does loading this skill increase or decrease errors? |
| Ghost skills | Skills configured but never used → wasted context |
| Bloated skills | Skills that add >5K tokens to every session for minimal benefit |

### 0.3 Recommendations Engine
Auto-generated, actionable suggestions per project based on data analysis.

**Recommendation types:**
- **Context optimization**: "Sessions reach 80% in N messages — split tasks or use shorter prompts"
- **Ghost skill removal**: "Skill X loaded in 45 sessions but never triggered — remove to save 2K tokens/session"
- **Agent switching**: "Agent 'review' has 8% error rate vs 3% average — consider using 'build' instead"
- **Model downgrade**: "Agent 'explore' uses Opus ($15/M) but tasks are simple — Haiku ($1/M) would suffice"
- **Cache optimization**: "N sessions this week had cache misses — respond within 5 min or start fresh"
- **Budget alert**: "Omogen at 75% of monthly budget with 10 days remaining"

### 0.4 Session Health Score
A composite score per session (0-100) combining multiple signals:
- Error rate (fewer errors = higher score)
- Context saturation (less = higher)
- Cache hit rate (more = higher)
- Completion status (DONE/COMMIT = higher)
- Token efficiency (fewer tokens for same outcome = higher)

Displayed as a colored badge on each session card: 🟢 90+, 🟡 70-89, 🔴 <70.

---

## Priority 1: Search & Discovery

### 1.0 Session File Blame ("git blame by session") ✅ COMPLETE
Backend + web views + CLI all working. See "Recently Completed" section above.

Remaining enhancement opportunities:
- [x] **File explorer page**: `/files/{path...}` — browse project files, directory navigation, session blame per file ✅
- [x] **Directory-level blame**: `?dir=` prefix filtering supports browsing any directory scope ✅

### 1.1 Search Strategy (Deferred)
Search is a separate effort. For now FTS5 handles keyword search.
Future: semantic search needs chunking (sessions are 100K+ tokens).
- Index by summary + git changes summary (not full message content)
- Summarize git add/commit activity per session for indexing
- Semantic search on summarized work products, not raw conversation

### 1.2 Elasticsearch Adapter ✅ COMPLETE
- [x] `search/elastic/` adapter implementing `search.Engine` (HTTP-only, no external client dependency)
- [x] Faceted search: group results by project, branch, agent, session type (via ES aggregations)
- [x] Fuzzy matching for typo tolerance (`fuzziness: AUTO`)
- [x] Config: `search.engine: "elasticsearch"` + URL + index name
- [x] Incremental indexing via scroll API + Chain fallback to LIKE
- [x] 16 tests with httptest mocks

### 1.3 Advanced Search UI ✅ COMPLETE (highlights + engine badge + facets + search within)
- [x] Show search engine capabilities in UI (badge: "FTS5" / "Semantic")
- [x] Highlighted snippets in search results (FTS5 `<mark>` tags rendered)
- [x] Rich result metadata: project, branch, type badge, error count
- [x] **Facet sidebar**: `SearchFacets()` store query — aggregates counts by provider, session type, agent, branches, projects, categories, statuses. Displayed in sessions page sidebar with clickable filter links. Active facet highlighted. Top 30 values per facet.
- [x] **Search within session**: Client-side text search input in the conversation section — filters messages and tool calls by text content in real-time, combined with existing role/error/skill filters. Styled search input with focus highlight.

---

## Priority 2: Dashboard & Visualization

### 2.1 Branch Session Tree ✅ COMPLETE
- [x] Tree visualization on branch timeline page (`/branches/{name}`)
- [x] Parent-child hierarchy via `ListTree()` + `SessionTreeNode`
- [x] Fork relationships via `GetForkRelations()` with fork point and shared message count
- [x] Color-coded dots by status (active/completed/review/idle/error)
- [x] Visual connectors with indented nesting (24px per depth level)
- [x] Agent/provider/type/status/fork badges per node
- [x] Stats strip (total sessions, forks, max depth)
- [x] Fork Relationships panel with linked session IDs
- [x] 6 tests (empty, with sessions, redirect, node builder, tree counter, ID truncation)

### 2.2 Activity Sparklines ✅ COMPLETE
- [x] Mini bar charts (14-day) for daily activity under KPI strip items
- [x] Session count, tokens, cost, errors sparklines on dashboard + project detail
- [x] Pure CSS implementation (no JS library), color-coded per metric

### 2.3 Cost Breakdown Charts ✅ COMPLETE
- [x] **MCP server cost pie chart**: Conic-gradient donut chart computed server-side, `template.CSS` for safe style injection, 10-color palette, legend with percentages
- [x] **Budget overlay line**: Dashed red horizontal line on Cost Over Time chart, sum of project daily limits, bar rescaling when budget exceeds max cost
- [x] **Cost Treemap**: Backend → model hierarchical treemap visualization — `CostTreemapNode` domain struct with `BuildCostTreemap()` pure function (sort + share computation); cross-tabulation in `Forecast()` using `estimate.PerModel` + message-level backend mapping; `treemapNodeView` with pre-computed widths + color indices; pure CSS flexbox rectangles (`.tm-container`, `.tm-backend`, `.tm-model`); 4th "Cost Map" tab on costs page; legend table with per-backend/model breakdown; 6 unit tests; verified on production data (1,302 sessions)

### 2.4 Session Timeline View ✅ COMPLETE
- [x] Session dependency graph (parent → child → fork relationships)
- [x] Timeline of a session: messages, tool calls, errors as a Gantt-like chart
- [x] HTMX lazy-loaded partial: `GET /partials/session-timeline/{id}`
- [x] Gantt bars colored by role (user/assistant/system), positioned by timestamp
- [x] Tool call chips with state icons (✅/❌/⏳) and duration
- [x] Saturation zone overlay (optimal/degraded/critical) with % display
- [x] Compaction markers (purple bars)
- [x] Fork point markers (⑂)
- [x] Overload inflection dashed line + health badge
- [x] Mini dependency graph: parent → self → children/forks with clickable links
- [x] Pure CSS (no external JS), dark theme consistent

### 2.5 Costs Page Organization ✅ COMPLETE
- [x] HTMX tabs: Overview / Tools & Agents / Optimization (lazy-loaded partials)
- [x] "Overview" tab: budgets + cache + backend breakdown
- [x] "Tools" tab: per-tool, MCP, agent costs, branches
- [x] "Optimization" tab: recommendations, saturation, model alternatives

---

## Priority 3: Platform & Integration

### 3.1 Slack Integration — Phase 1 ✅ COMPLETE (Foundation)
- [x] User identity enrichment: kind (human/machine/unknown), slack_id, slack_name, role (admin/member)
- [x] Machine account detection: configurable email glob patterns with 7 built-in defaults
- [x] OwnerStats query: GROUP BY owner_id with LEFT JOIN users for digest data collection
- [x] `idx_sessions_owner_id` index for GROUP BY performance
- [x] CLI `aisync users list/set-kind/set-slack/set-role/backfill-kind`
- [x] Scheduler `UserKindBackfillTask` for reclassifying existing users
- [x] Full spec: `SLACK_SPEC.md` with 4-phase implementation plan

### 3.1 Slack Integration — Phase 2 ✅ COMPLETE (Notification System)
Architecture: Clean **Notification bounded context** (hexagonal) — channel-agnostic, decoupled from Slack.
- [x] **Domain** (`notification/domain.go`): 6 event types (budget.alert, error.spike, session.captured, daily.digest, weekly.report, personal.daily), Severity, data structs, Recipient, RenderedMessage
- [x] **Ports** (`notification/ports.go`): Channel, Formatter, Router interfaces
- [x] **Service** (`notification/service.go`): `NewService()`, `Notify()` (async fire-and-forget), `NotifySync()` (sync for tests/scheduler), nil-safe
- [x] **Router** (`notification/router.go`): `DefaultRouter` with RoutingConfig — default channel, project channel overrides, alert/digest enable toggles, personal DM routing
- [x] **Slack adapter** (`notification/adapter/slack/`): `Client` (webhook + bot mode via net/http), `Formatter` (Block Kit JSON for all 6 event types — progress bars, leaderboards, dashboard links)
- [x] **Webhook adapter** (`notification/adapter/webhook/`): `Client` (generic HTTP POST with retries), `Formatter` (raw JSON event serialization)
- [x] **Config** (`config.go`): `notificationConf` with Slack (webhook_url, bot_token), generic webhook (url, secret), default_channel, project_channels, alert toggles (budget/errors/capture), digest toggles (daily/weekly/personal), error spike thresholds, digest cron schedules, dashboard_url; full merge in `loadFrom()`; 20+ getter methods; `IsNotificationEnabled()` convenience
- [x] **Factory wiring** (`default.go`): Builds `notification.Service` from config, injects into PostCaptureFunc for `session.captured` events
- [x] **BudgetCheckTask enriched**: Fires `notification.Event{EventBudgetAlert}` alongside legacy webhooks for both monthly and daily alerts
- [x] **DailyDigestTask** (`scheduler/digest_task.go`): Collects yesterday's stats via `Stats()` + `OwnerStats()` + per-project breakdown, fires `EventDailyDigest`; skips when 0 sessions
- [x] **WeeklyReportTask** (`scheduler/digest_task.go`): Collects last week's stats with owner leaderboard, fires `EventWeeklyReport`; week-boundary calculation via `mostRecentMonday()`
- [x] **Scheduler wiring** (`serve.go`): `notification.Service` built once, shared across budget_check, daily_digest, weekly_report tasks; user_kind_backfill also wired (daily 5 AM)
- [x] **43 new tests**: 13 router, 9 service, 21 adapter (client + formatter), 19 digest task (daily/weekly send, skip, error, helpers)
- [x] **2,492 tests passing** across 113 packages

### 3.1 Slack Integration — Phase 3 (Error Spikes & Dedup) ✅ COMPLETE
- [x] Error spike detection task — `ErrorSpikeTask` checks error count in configurable time window, fires `EventErrorSpike` notifications
- [x] Wired in `serve.go` with `*/15 * * * *` schedule, configurable threshold + window from config
- [x] Alert dedup — `Deduplicator` in notification service suppresses duplicate alerts (same event type + project) within 30min cooldown. Digests never deduplicated. Integrated into `Notify()` and `NotifySync()`, wired in `serve.go`.
- [x] `users.lookupByEmail` — `SlackResolveTask` auto-resolves slack_id from Slack API for human users without slack_id. `Client.LookupByEmail()` adapter uses Slack Web API (requires bot token with `users:read.email` scope). Wired in `serve.go` at `0 6 * * *` (daily 6 AM).

### 3.1 Slack Integration — Phase 4 (Personal DMs) ✅
- [x] Personal daily digests via DM — `PersonalDigestTask` sends per-user DMs with individual stats + team avg
- [x] `EventPersonalDaily` firing for each human user with slack_id, uses `NotifySync()` for reliable DM delivery
- [x] Wired in `serve.go` with `30 8 * * *` schedule (8:30 AM, after global digest)

### 3.2 Team & Ownership — Mostly Done ✅
- [x] Multi-user: track who spawned which session (git user identity) — enriched with kind/role
- [x] Per-user cost tracking — OwnerStats query ready (GROUP BY owner_id)
- [x] **Team Dashboard** (`/team`): Leaderboard with per-user stats (sessions, tokens, errors, error rate), progress bars showing % share, human/machine badges, period filter (today/yesterday/week/month) via sidebar. Navbar link added. KPI strip with team size, total sessions, tokens, errors.
- [ ] Billing entity per project (for client invoicing)

### 3.3 Export & Reporting
- [ ] Weekly PDF/HTML report per project (auto-generated)
- [ ] CSV export for cost data (for accounting)
- [ ] API for external dashboards (Grafana, Datadog)

### 3.4 Settings Web UI ✅ COMPLETE (full CRUD + Notifications)
- [x] `/settings` page: display of all config sections (12 sections) with inline editing
- [x] Navbar gear icon, responsive grid layout, enabled/disabled color coding
- [x] **Inline CRUD via HTMX**: 4 input types — toggle (checkbox switch), select (dropdown), text, number
- [x] **POST /api/settings**: Updates `Config.Set()` + `Config.Save()`, returns HTMX partial with "saved" indicator
- [x] **Notifications section** (global): Slack webhook/bot token, generic webhook, default channel, dashboard URL, alert toggles (budget/errors/capture), error spike thresholds, digest toggles (daily/weekly/personal), digest cron schedules — 17 config keys with full `Get()`/`Set()` support
- [x] **Per-project notification channel**: Added `notif_channel` field to Project Classifiers — allows per-project Slack channel override (e.g. `#backend-ai`). Stored in `notification.project_channels` map via `SetNotificationProjectChannel()`
- [x] **~25 editable settings**: storage mode, auto capture, file blame, search engine, analysis adapter/model, tagging, secrets mode, error classifier, dashboard page size/sort, scheduler tasks
- [x] **8 tests**: toggle, select, text, number, missing key, invalid value, no config, persistence to disk
- [x] CRUD for project classifiers and budgets — full inline editing with HTMX, 4 rule types + budget with 5 fields, dynamic add/remove rule rows
- [x] Live preview of classification rules against existing sessions ✅

---

## Priority 4: Technical Debt

### 4.1 Existing
- [x] **Compute objectives for existing sessions (batch job)**: `ObjectiveBackfillTask` — scheduler task running daily at 3:30 AM, processes up to 50 sessions/run with `ComputeObjective()` (Summarize + ExplainShort LLM calls), skips sessions with < 5 messages and those already having objectives; 7 unit tests
- [x] **LLM classifier for ambiguous tool errors**: `LLMClassifier` (`internal/errorclass/llm.go`) — sends raw error text, tool name, HTTP status, provider info to LLM with structured JSON response; graceful fallback to "unknown" on LLM error/parse failure/timeout; strips markdown code fences; validates category/source; truncates long raw errors (>2K); 10 unit tests
- [x] **`CompositeClassifier`** (`internal/errorclass/composite.go`) — chains classifiers in priority order (deterministic first, LLM fallback for unknowns); first non-unknown result wins; factory-wired in `default.go` when `errors.classifier: "composite"` is set; auto-creates LLM client from `errors.llm_profile` config; falls back to deterministic-only if LLM client unavailable; 8 unit tests
- [x] **`NewClient`/`NewClientFromConfig`** added to `llmfactory` — creates `llm.Client` from resolved profile (supports ollama + claude CLI); used by composite classifier factory wiring

### 4.2 Performance ✅ MOSTLY COMPLETE
- [x] **Incremental FTS5 indexing**: `search.IncrementalIndexer` optional interface + `IndexedSessionIDs()` on FTS5 engine; `IndexAllSessions()` now skips sessions already in the index (was re-indexing all 1,300+ each time); 3 new FTS5 tests (interface, empty, with-docs+delete)
- [x] **Lazy loading for costs page sections**: HTMX tab partials + `cachedCostsPage()` prevents recomputing on tab switch (60s TTL)
- [x] **Background pre-compute ContextSaturation()**: `SaturationTask` (every 2h) pre-warms global + per-project results; web handlers use `cachedSaturation()` with 2h TTL, cold-cache fallback
- [x] **Background pre-compute CacheEfficiency()**: `CacheEfficiencyTask` (every 2h) pre-warms 7d + 90d windows for global + per-project; web handlers use `cachedCacheEfficiency()` with 2h TTL
- [x] **Cache Trends()**: `cachedTrends()` with 5min TTL — avoids 2 full-scan period comparisons on every dashboard/project page load
- [x] **Cache sidebar projects**: `cachedSidebarGroups()` with 2min TTL — `ListProjects()` was called on 11+ page loads, now cached centrally
- [x] **Fix cost tab partials**: `cachedCostsPage()` caches the full `costDashboardPage` struct (60s TTL) so HTMX tab switches don't recompute ~150 fields
- [x] **Wire unwired tasks**: `backfill_remote_url` (daily 4:00), `fork_detection` (daily 4:30), `budget_check` (hourly :50)
- [x] **Stats warming per project**: `StatsReportTask` enhanced to warm global + per-project stats caches via `store.SetCache()`
- [x] **12 new tests**: 7 SaturationTask + 5 CacheEfficiencyTask, all passing
- [x] **Scheduler now has 10 tasks**: gc, capture_all, stats_report, usage_compute, saturation_precompute, cacheeff_precompute, backfill_remote_url, fork_detection, budget_check, reclassify_errors (+ analyze_daily if LLM configured)

### 4.3 Testing ✅ COMPLETE
- [x] **FTS5 search integration tests** (`internal/search/fts5/integration_test.go`): Full lifecycle (index 3 docs → search by summary/content/filter/highlights → delete → upsert → verify), incremental reindex simulation, `DocumentFromSession` pipeline (session → doc → FTS5 → search hit + tool output search) — 3 tests
- [x] **Budget alert webhook delivery tests** (`internal/scheduler/budget_integration_test.go`): Full pipeline BudgetCheckTask → webhook HTTP delivery via httptest, monthly exceeded + daily warning + multiple projects (2 alert, 1 under) + no alerts + nil dispatcher safety — 5 tests
- [x] **Classifier cascade integration tests** (`internal/errorclass/integration_test.go`): Full pipeline raw errors → CompositeClassifier (deterministic + smart LLM mock) → ErrorService → mock store, tests 6 error types (HTTP 500/429, tool pattern, LLM fallback for TypeScript/segfault, already-classified skip), all-deterministic path (LLM never called), LLM failure graceful fallback, deterministic-only mode — 4 tests
