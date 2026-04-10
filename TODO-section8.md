# TODO — Section 8: Investigation Tooling & Agent Support

> **Scope:** Implement the 8 features from NEXT.md Section 8, triggered by the 2026-04-05 session investigation.
> **Estimated effort:** ~11-12h across 4 phases.
> **Validation sessions:** `ses_2a125dde7ffeLfthr64J35CFkq`, `ses_2a1396b09ffeMRMA5b9eEeRxWt` (both captured from an `opencode` provider on the same project — used as empirical test data, not as provider-specific scope).
>
> ## ⚖️ Hard rule for every task below: detection only, zero prescription
>
> aisync is an observation tool. All code, templates, dashboards, alerts, LLM prompts, and output schemas
> produced by this section must describe **observed facts** (counts, bytes, tokens, ratios, percentiles,
> cross-provider deltas). They must **not** tell the user what to do.
>
> - ✅ Allowed: facts, factual comparisons against baselines, percentiles, cross-provider deltas
> - ❌ Forbidden: any `suggestion`, `recommendation`, `consider`, `should`, `try`, `fix`, "you can" language
> - ❌ Forbidden schema fields: `suggestions`, `recommendations`, `cli_suggestions`, `fixes`, `actions`
> - ✅ Allowed schema fields: `*_findings`, `observed_*`, `high_*_commands`, `top_*`, `*_rate`, `*_distribution`
>
> **Every phase must pass a grep-based audit** (see Cross-Phase Tasks) that fails the build if forbidden
> words appear in templates, alert messages, or LLM system prompts.
>
> ## Provider scope
>
> All detectors, aggregators, and dashboards built in this section are **provider-agnostic**. They must
> work for `claude-code`, `opencode`, `cursor`, and any future provider. No branching on `session.Provider`
> unless explicitly required by a feature (e.g. 8.8's comparison view, which groups by provider but does
> not label any provider as "good" or "bad").

---

## Phase 1 — Critical Fixes (~1.5h) ⚡ ✅ COMPLETE (2026-04-06)

> **Goal:** Make compaction detection accurate (provider-agnostic) and make command output cost visible.
> **Validation after Phase 1:** re-run detection on the 2 validation sessions → should show 4 compactions each (not 1), and command detail pages should display output bytes/tokens.
>
> **Actual results (2026-04-06):**
> - `ses_2a125dde` (504 msgs, not 129 as initially counted): 1 → **11 compactions** detected (all ~50% drops just over old threshold)
> - `ses_2a1396b0` (209 msgs): 1 → **5 compactions** detected
> - Command Output Footprint panel: rendering correctly with top commands by output bytes
> - All tests passing: 27 compaction tests, 3 command output tests, all web handler tests

### 8.1 — Fix compaction threshold + 2-pass cascade detection + per-user-msg rate (~45 min)

> **Research (2026-04-05):** Investigated `ses_2a6cb55f6ffeENWr6H1LnWdnLu` (3426 msgs, $360, 403M tokens).
> Discovered OpenCode uses a **2-pass compaction** pattern:
>   - Pass 1: 168k → 105k (~37% drop, cache completely reset to 0)
>   - Pass 2: 105k → 68k (~35% drop, 2 messages later)
>   - Total effective drop: 168k → 68k = 59.5%
>
> Current `< 50%` threshold misses BOTH passes individually.
> Distribution: 25 drops in 30-40% band (many are 2-pass legs), 16 drops in 40-50% band, 15 in 50%+ band.
> With all thresholds combined: 31 real compactions (detected: 15). Cost of invisible compactions: $18.41.
>
> **Sessions analyzed:**
>   - `ses_2a6cb55f` (3426 msgs, $360): 15 → ~45 compactions with fix
>   - `ses_2a125dde` (129 msgs, $12.9): 1 → 4 compactions with fix
>   - `ses_2a1396b0` (113 msgs, $12.5): 1 → 4 compactions with fix

- [x] **`internal/session/compaction.go`** — REWRITTEN (~240 lines)
  - [x] Relax `CompactionThreshold` constant: `0.50` → `0.55` (catches 45-50% single-pass drops)
  - [x] Add `CompactionSecondaryThreshold = 0.65` constant (for drops with large absolute delta)
  - [x] Add `CompactionMinAbsoluteDrop = 40000` constant (minimum tokens dropped for secondary trigger)
  - [x] Add `CompactionCascadeWindow = 3` constant (max message gap between 2-pass legs to merge them)
  - [x] Update main detection logic (primary + secondary thresholds)
  - [x] Track `messagesWithTokenData` counter in the main loop
  - [x] Add **2-pass cascade merging** with recovery check (prevents sawtooth patterns from being merged as cascades)
  - [x] Count `RoleUser` messages during the main loop
  - [x] Populate `CompactionsPerUserMessage`, `LastQuartileCompactionRate`, `DetectionCoverage`
  - [x] Update doc comment on `DetectCompactions` to reflect new thresholds + cascade logic
- [x] **`internal/session/session.go`** — all new fields added
  - [x] `CompactionsPerUserMessage`, `LastQuartileCompactionRate`, `MessagesWithTokenData`, `DetectionCoverage`, `CascadeCount` on `CompactionSummary`
  - [x] `IsCascade`, `MergedLegs` on `CompactionEvent`
- [x] **`internal/session/compaction_test.go`** — 27 tests total (17 existing fixed + 10 new)
  - [x] `TestDetectCompactions_OpenCodePattern49Percent`
  - [x] `TestDetectCompactions_SecondaryTrigger_LargeAbsoluteDrop`
  - [x] `TestDetectCompactions_SecondaryTrigger_RequiresCacheInvalidation`
  - [x] `TestDetectCompactions_TwoPassCascade` (redesigned with proper threshold-crossing data)
  - [x] `TestDetectCompactions_TwoPassCascade_TooFarApart` (redesigned)
  - [x] `TestDetectCompactions_CompactionsPerUserMessage`
  - [x] `TestDetectCompactions_LastQuartileRate`
  - [x] `TestDetectCompactions_DetectionCoverage_Full`
  - [x] `TestDetectCompactions_DetectionCoverage_None`
  - [x] `TestDetectCompactions_50PercentNowTriggers` (regression guard)
  - [ ] Add `TestDetectCompactions_LargeSession_RealPattern` with a subset of ses_2a6cb55f data (10 drops including 3 cascades) — **deferred** (not critical, real-data validation done via manual check)
- [x] **`internal/web/handlers.go`** — exposed `CompactionRate`, `CompactionLastQuartile`, `CompactionCascadeCount`, `CompactionDetectionStatus` + `IsCascade`/`MergedLegs` on event views
- [x] **`internal/web/templates/session_detail.html`**
  - [x] Rate display, last-quartile rate in compaction-metrics div
  - [x] Cascade badge on merged events
  - [x] "Compaction detection not available" message when `DetectionCoverage == "none"`
- [x] **`internal/sessionevent/event.go`** — updated stale `CompactionDetail` doc comment (old ">50%" reference → new two-tier thresholds + cascade merging)
- [x] All tests passing: `/opt/homebrew/bin/go test ./internal/session/ ./internal/web/`
- [x] Manual validation: `ses_2a125dde` shows 11 compactions, `ses_2a1396b0` shows 5

### 8.2 — Command output bytes & tokens tracking (~1h)

- [x] **`internal/sessionevent/event.go`**
  - [x] Add `OutputBytes int \`json:"output_bytes,omitempty"\`` to `CommandDetail` struct
  - [x] Add `OutputTokens int \`json:"output_tokens,omitempty"\`` to `CommandDetail` struct
- [x] **`internal/sessionevent/processor.go`**
  - [x] Populate `OutputBytes = len(toolCall.Output)` and `OutputTokens = len(toolCall.Output) / 4`
- [x] **`internal/storage/sqlite/event_store.go`**
  - [x] Verified: `marshalEventPayload`/`unmarshalEventPayload` uses `json.Marshal(e.Command)` — new fields auto-included ✅
- [x] **`internal/sessionevent/processor_test.go`** — 3 new tests:
  - [x] `TestProcessor_CommandOutputBytes` (bash with output → correct bytes and tokens)
  - [x] `TestProcessor_CommandOutputBytes_Empty` (no output → 0/0)
  - [x] `TestProcessor_CommandOutputBytes_LargeOutput` (100KB → 100000 bytes, 25000 tokens)
- [x] **Migration not needed** — JSON payload column, existing rows keep working (fields default to 0)
- [ ] **Backfill existing sessions** (re-process) — **deferred to Phase 2**:
  - [ ] Add `BackfillCommandOutputTask` scheduler task or CLI command
  - [ ] Re-process sessions where `OutputBytes == 0` on command events
- [x] **`internal/web/handlers.go`** + **`templates/session_detail.html`**
  - [x] Added "Command Output Footprint" panel — top 10 commands by output bytes
  - [x] Shows: base command, invocations, total output, avg/run, estimated tokens
  - [x] Added `formatBytes()` helper to `internal/web/funcs.go`
  - [x] Panel hidden when no command output data (covers Cursor + sessions with no bash commands)
- [x] All tests passing: `/opt/homebrew/bin/go test ./internal/sessionevent/ ./internal/web/`
- [x] Manual validation: `ses_2a125dde` shows Command Output Footprint with grep (62 runs), pnpm, git, etc.

---

## Phase 2 — Level 1 Aggregates (~4h)

> **Goal:** Precomputed hot-spots table + command patterns dashboard. Zero LLM cost, instant browsing.
> **Validation after Phase 2:** visit `/investigate/command-patterns` → see top repeated long commands across all sessions, with counts, avg length, and cumulative re-transmitted characters (pure measurements).

### 8.3 — Session hot-spots precomputed table (~2h)

- [ ] **Design** — decide table name: `session_hotspots` vs extending `sessions` table. Recommend new table (keeps `sessions` lean).
- [ ] **`internal/storage/sqlite/sqlite.go`**
  - [ ] Add migration 031: `CREATE TABLE session_hotspots (session_id TEXT PRIMARY KEY, computed_at INTEGER NOT NULL, payload BLOB NOT NULL, FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE);`
- [ ] **`internal/storage/storage.go`** (or equivalent port file)
  - [ ] Define `SessionHotspotStore` interface: `GetHotspots(id)`, `SetHotspots(id, data)`, `ListStaleHotspots(olderThan)`
- [ ] **`internal/storage/sqlite/hotspot_store.go`** (new file)
  - [ ] Implement the interface with compressed JSON payload (zstd like sessions)
- [ ] **`internal/session/hotspots.go`** (new file — pure domain)
  - [ ] Define `SessionHotspots` struct with: `TopCommandsByOutput`, `TopCommandsByReuse`, `CompactionEvents`, `CompactionRate`, `SkillFootprints`, `ExpensiveMessages`
  - [ ] Define `ComputeHotspots(session Session) SessionHotspots` pure function
- [ ] **`internal/session/hotspots_test.go`**
  - [ ] Test with synthetic sessions covering all hot-spot categories
- [ ] **`internal/scheduler/hotspots_task.go`** (new file)
  - [ ] `HotspotsTask` that iterates stale sessions, calls `ComputeHotspots`, writes to store
  - [ ] Run after `AnalyzeDailyTask` in the scheduler chain
  - [ ] Add schedule config key `hotspots_cron` (default: `15 3 * * *` — 3:15 AM)
- [ ] **`internal/testutil/mock_store.go`** — add mock impl of `SessionHotspotStore`
- [ ] **`pkg/cmd/servecmd/serve.go`** — wire the task + schedule
- [ ] **`internal/web/handlers.go`** + new template
  - [ ] New tab on session detail page: "Hot Spots" (reads from the new table)
- [ ] Run full test suite
- [ ] Manual validation: trigger task manually, check hot-spots appear on cycloplan sessions

### 8.5 — Repeated command pattern dashboard (~2h)

- [ ] **`internal/session/command_patterns.go`** (new file — pure domain)
  - [ ] Normalization function: `NormalizeCommand(cmd string) string` (replace `/abs/path/...` → `/PATH`, digits → `N`, quoted strings → `"..."`)
  - [ ] Aggregation function: `FindCommandPatterns(events []CommandEvent, minLength, minCount int) []CommandPattern`
  - [ ] `CommandPattern` struct: `Pattern`, `Occurrences`, `Sessions`, `Projects`, `AvgLength`, `TotalChars`, `RetransmittedChars`
    - [ ] Note: `RetransmittedChars` is a factual measurement (`TotalChars × avg_compaction_multiplier`), not a "savings estimate". No field named `EstimatedTokensSaved`.
- [ ] **`internal/session/command_patterns_test.go`**
  - [ ] Test normalization with observed session examples (lsof, curl login, vite dev patterns)
  - [ ] Test aggregation with duplicates across sessions/projects
  - [ ] Test `RetransmittedChars` computation against known-good fixture data
- [ ] **`internal/storage/sqlite/event_store.go`**
  - [ ] New query method: `ListCommandEvents(minOutputBytes, minFullCommandLength)` → returns events with full command + output size
- [ ] **`internal/service/`** — add analysis service method or direct handler
- [ ] **`internal/web/handlers.go`**
  - [ ] New handler `handleCommandPatterns` for `/investigate/command-patterns`
  - [ ] Register route in `server.go`
- [ ] **`internal/web/templates/command_patterns.html`** (new)
  - [ ] Table: pattern, count, sessions, projects, avg length, cumulative characters, re-transmitted characters
  - [ ] Click-through to see raw invocations
  - [ ] Export buttons (JSON/CSV)
  - [ ] **Page copy must describe what the data is, not what to do with it.** No "candidates for X", no "consider wrapping", no "refactor targets". Page title: "Repeated Command Patterns". Subtitle: "Commands observed ≥3 times across sessions, sorted by cumulative characters transmitted to model."
- [ ] Add sidebar link under "Investigate" section (label: "Command Patterns", not "CLI Candidates")
- [ ] Run full test suite
- [ ] Manual validation: visit `/investigate/command-patterns` — repeated commands from validation sessions visible

---

## Phase 3 — Level 2 Agent Tooling (~3-4h)

> **Goal:** Turn aisync into an investigation API so analysis agents can drill down cheaply.
> **Validation after Phase 3:** an analysis agent produces a structured observation report describing (not prescribing) compaction cascades and repeated command patterns.

### 8.4 — `aisync-investigator` investigation API (~2-3h)

- [ ] **`internal/web/investigate_handlers.go`** (new file)
  - [ ] `GET /api/investigate/sessions/{id}/hotspots` → returns hot-spots JSON (from 8.3 table)
  - [ ] `GET /api/investigate/sessions/{id}/commands?min_bytes=5000` → filtered command events
  - [ ] `GET /api/investigate/sessions/{id}/commands/{event_id}` → full command detail with truncated output
  - [ ] `GET /api/investigate/sessions/{id}/compactions/{idx}/window?radius=3` → messages around compaction
  - [ ] `GET /api/investigate/sessions/{id}/skills` → skill load list with tokens
  - [ ] `GET /api/investigate/commands/similar?pattern=...&scope=all` → cross-session CLI candidates
- [ ] Register routes in `server.go`
- [ ] Response shapes: stable JSON, bounded size (truncate long outputs), include schema version
- [ ] **`internal/web/investigate_handlers_test.go`**
  - [ ] Integration tests for each endpoint using test SQLite
- [ ] **`docs/investigation-api.md`** (new)
  - [ ] Document each endpoint with example request/response
  - [ ] Usage pattern for analysis agents
- [ ] *(Optional)* `cmd/aisync-investigator-mcp/` — thin MCP stdio wrapper calling the HTTP endpoints
- [ ] Manual validation: `curl http://localhost:8371/api/investigate/sessions/ses_2a125dde.../hotspots` returns compaction + command data

### 8.6 — Enhance `BuildAnalysisPrompt` to use hot-spots — observation mode (~1h)

- [ ] **`internal/analysis/llm/analyzer.go`**
  - [ ] Accept `hotspots *SessionHotspots` via `AnalyzeRequest`
  - [ ] Inject compact hot-spots JSON at top of prompt
  - [ ] Remove the redundant "last 10 messages" dump (agent can query via 8.4 if needed)
  - [ ] Rewrite `systemPrompt` to enforce observation-only output:
    - [ ] Include explicit forbidden-words list in the prompt: *"Do not use the words should, consider, recommend, suggest, try, fix, better, worse, bloated, efficient."*
    - [ ] Instruct the LLM to describe what happened with numbers, not to prescribe action
    - [ ] Mention the investigation API endpoints (from 8.4) as drill-down options for facts it needs
  - [ ] Extend output schema with new **observation** fields (all additive, no recommendation fields):
    - [ ] `compaction_findings` — counts, rates, drop sizes, context peaks
    - [ ] `high_output_commands` — ranked by measured `OutputBytes`
    - [ ] `observed_command_patterns` — normalized patterns with counts, lengths, session spread
    - [ ] `context_saturation` — max input tokens per assistant turn, binned
  - [ ] **Explicitly forbid** adding any of these field names: `suggestions`, `recommendations`, `cli_suggestions`, `fixes`, `actions`, `waste_reduction`
- [ ] **`internal/analysis/analyzer.go`**
  - [ ] Add `Hotspots *session.SessionHotspots` to `AnalyzeRequest` struct
- [ ] **`internal/service/analysis.go`**
  - [ ] Load hot-spots from store before calling analyzer (cold-fall to on-demand compute if missing)
- [ ] **`internal/analysis/llm/analyzer_test.go`**
  - [ ] Test prompt includes hot-spots section
  - [ ] Test output parses new observation fields correctly
  - [ ] **Test forbidden-words audit:** assert the rendered system prompt contains the explicit forbidden-words clause
  - [ ] **Test schema safety:** assert output schema struct has no field matching `/suggest|recommend|fix|action|waste/i`
- [ ] Run full test suite
- [ ] Manual validation: trigger analysis on a validation session, inspect LLM input/output for prescription drift

---

## Phase 4 — Alerts & Validation (~3h)

> **Goal:** Fact-based notifications + cross-provider context usage comparison.
> **Validation after Phase 4:** Slack notification fires on high-compaction sessions with facts-only wording; provider context usage dashboard shows numeric comparison across providers on identical projects.

### 8.7 — Compaction cascade notification (fact report) (~1h)

> **Timing caveat:** Notifications are forensic, not real-time. The detection task runs on a
> schedule (after HotspotsTask), so notifications arrive after the session has ended and tokens
> have been spent. aisync is downstream — it cannot intercept a running session. The notification
> template should include the session's end time to make this explicit.

- [ ] **`internal/notification/events.go`** — add `EventCompactionCascade` type
- [ ] **`internal/scheduler/cascade_detection_task.go`** (new)
  - [ ] Iterate sessions with hot-spots where `CompactionRate > 0.25` AND `TotalCompactions >= 3`
  - [ ] **Skip sessions with `DetectionCoverage == "none"`** (e.g. Cursor) — no false "0 compactions" notifications
  - [ ] Fire notification with dedup window (24h per session)
- [ ] Wire task in `serve.go` (run hourly)
- [ ] **Notification formatter** (follow existing Slack Block Kit pattern in `internal/notification/adapter/slack/formatter.go` — no separate template engine):
  - [ ] Add `formatCompactionCascade` method in the Slack formatter
  - [ ] Fields: session id, project, provider, N compactions, M user messages, rate, list of peak input tokens before each compaction, model window, total rebuild cost, total tokens lost, **session end time**, aisync URL
  - [ ] **No verbs of action, no "consider", no "try", no "we recommend", no "this session needs".** Pure descriptive sentences.
  - [ ] See NEXT.md §8.7 for the exact wording
- [ ] Config key: `notifications.compaction_cascade.threshold_ratio` (default 0.25)
- [ ] Config key: `notifications.compaction_cascade.min_events` (default 3)
- [ ] **Template-lint test** in `internal/notification/templates_test.go`:
  - [ ] Render template with sample data, assert rendered output contains none of: `should`, `consider`, `recommend`, `suggest`, `try`, `fix`, `wrap`, `split`, `switch to`
  - [ ] This test is the enforcement mechanism for the philosophy rule on alerts
- [ ] Unit test the detection task (trigger condition, dedup window)
- [ ] Manual validation: force-trigger on validation sessions → Slack message received, verify wording is descriptive only

### 8.8 — Per-provider context usage comparison (~2h)

- [ ] **`internal/service/provider_analytics.go`** (new)
  - [ ] `ProviderContextStats` struct: provider, avg max context, p50/p95 context, context-tokens-per-tool-call ratio
  - [ ] `ComputeProviderContextStats(sessions)` function — pure stats, no labels like "efficient"/"bloated"
- [ ] **`internal/web/handlers.go`**
  - [ ] Add handler for provider context usage section
- [ ] **`internal/web/templates/analytics_providers.html`** (new or extend existing)
  - [ ] Bar charts: avg max context by provider, p95 max context by provider, context tokens per tool_call by provider
  - [ ] Per-project drilldown (same metric, filtered to one project)
  - [ ] **Factual callout template** (fires when delta ≥ 2× between providers on same project):
    > *"On project `{name}`: provider `{a}` avg max context = {x}k, provider `{b}` avg max context = {y}k (delta {ratio}×)."*
  - [ ] The word `bloat`/`bloated` must **not** appear anywhere in the template, handler strings, or page title. Page title: "Provider Context Usage". Section header: "Max context by provider".
- [ ] Add to analytics page sidebar
- [ ] Unit tests for the stats computation
- [ ] **Template-lint test**: assert none of the templates in this section contain `bloat`, `efficient`, `better`, `worse`, `recommend`
- [ ] Manual validation: dashboard shows numeric comparison across providers on validation project

---

## Cross-Phase Tasks

- [ ] Update `NEXT.md` Section 8: mark each feature ✅ DONE as we complete them
- [ ] Update `TODO.md` (global): add Section 8 completion entries after each phase
- [ ] Add Sprint I entry in "Completed Sprints" log when Phase 1 + Phase 2 done
- [ ] Add Sprint J entry when Phase 3 + Phase 4 done
- [ ] Run `go clean -testcache && /opt/homebrew/bin/go test ./...` after each phase (stale cache is sneaky)
- [ ] Manual smoke test on the 2 validation sessions after each phase

### Philosophy audit (must pass after every phase)

- [ ] **Automated grep audit** — a script `scripts/audit-philosophy.sh` (or a Go test) that fails the build if any of the following appear in:
  - [ ] `internal/notification/templates/**`
  - [ ] `internal/web/templates/**`
  - [ ] `internal/analysis/llm/*.go` (system prompts)
  - [ ] Any `*.tmpl` file under `internal/`
  - Forbidden substrings (case-insensitive): `should`, `consider`, `recommend`, `suggest ` (with trailing space to allow "suggestion" in doc comments), `we advise`, `you could`, `you should`, `bloated`, `wasteful`, `inefficient`, `fix this`, `wrap these`, `split the`, `switch to`
  - Exemptions list: none for user-facing output. Go code comments and test fixtures may contain these words (the grep excludes `_test.go` and `// ` line comments).
- [ ] **Schema field audit** — assert no struct field under `internal/analysis/llm/` or `internal/notification/` matches regex `/suggest|recommend|action_to|fix_|waste_reduc/i`
- [ ] **Manual cross-read** — at the end of each phase, re-read the rendered templates and LLM system prompt as a user, asking: *"Is anything here telling me what to do?"* If yes, rewrite.

---

## Validation Checklist (After All Phases)

Replay on `ses_2a125dde7ffeLfthr64J35CFkq`:

- [ ] Shows **4 compactions** (not 1)
- [ ] Shows **compaction rate** ≈ 0.36/user_msg
- [ ] Shows **top commands by output size** (observed long-output commands ranked with bytes)
- [ ] Hot-spots tab loads instantly (precomputed)
- [ ] `/investigate/command-patterns` lists the observed repeated commands (normalized patterns) with their counts and cumulative characters
- [ ] Analysis agent report includes `compaction_findings`, `high_output_commands`, `observed_command_patterns`, `context_saturation` — and **does not** include any field matching `suggest|recommend|fix|action`
- [ ] Slack notification fires (compaction cascade) with facts-only wording (no `should`/`consider`/`try`)
- [ ] Provider dashboard shows numeric comparison across providers on the same project, with no "bloat"/"efficient" language
- [ ] **Philosophy grep audit passes** (see Cross-Phase Tasks)

---

## Notes / Gotchas

- **Storage serialization bug** — In Sprint H we discovered `marshalEventPayload`/`unmarshalEventPayload`
  silently dropped new fields. **Always double-check** when adding fields to event detail structs.
- **`rtk go test` swallows stdout** — use `/opt/homebrew/bin/go test` directly for visible output.
- **IOStreams in tests** uses `*bytes.Buffer`, NOT `*strings.Builder`.
- **Web templates pattern**: `{{define "pagename.html"}}{{template "layout" .}}{{end}}` with sidebar/content blocks.
- **DELETE requests**: Go's `ParseForm()` doesn't read body for DELETE — parse manually if needed.
- **Icon encoding**: Use Unicode chars (`"\U0001F7E2"`) not HTML entities (`&#x1F7E2;`) — Go auto-escapes `&`.
- When updating `storage.Store` or `service.SessionServicer` interfaces, update all mocks:
  `internal/testutil/mock_store.go`, `internal/service/session_test.go`, `internal/service/remote/session.go`, `internal/scheduler/tasks_test.go`.
