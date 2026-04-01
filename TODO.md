# aisync — Next Session TODO

> Last updated: 2026-04-01

## Recently Completed (2026-04-01)

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

### 1.2 Elasticsearch / Typesense Adapter
- [ ] `search/elastic/` adapter implementing `search.Engine`
- [ ] Faceted search: group results by project, branch, agent, session type
- [ ] Fuzzy matching for typo tolerance
- [ ] Config: `search.engine: "elasticsearch"` + URL

### 1.3 Advanced Search UI ✅ COMPLETE (highlights + engine badge)
- [x] Show search engine capabilities in UI (badge: "FTS5" / "Semantic")
- [x] Highlighted snippets in search results (FTS5 `<mark>` tags rendered)
- [x] Rich result metadata: project, branch, type badge, error count
- [ ] Facet sidebar: filter by project, branch, type, date range (deferred — needs aggregation queries)
- [ ] Search within a session (find specific tool calls, messages)

---

## Priority 2: Dashboard & Visualization

### 2.1 Branch Session Tree ⭐ HIGH VALUE
Interactive tree visualization when clicking on a branch in the project page.

**Concept:**
- Click on a branch → opens a tree view showing all sessions on that branch
- Tree root = first session created on this branch
- Children = subsequent sessions, forks, subagents (parallel work)
- Each node shows: summary, agent badge, status, token count, errors
- Click a node → navigate to session detail
- Parallel sessions visible side-by-side (same branch, overlapping time)
- Fork relationships shown as branch-off from parent
- Inspired by OpenAPI tree / git graph visualization

**Data model:**
- Group sessions by branch + project
- Order by `created_at`
- Connect forks via `session_forks` table
- Connect subagents via `parent_id`
- Detect concurrent sessions (overlapping time ranges on same branch)

**UI:**
- Collapsible tree (HTMX or lightweight JS)
- Each level: session card with agent badge, status, token count
- Visual connector lines between parent/child/fork
- Color-coded by status: 🟢 completed, 🟡 active, 🔴 errors

### 2.2 Activity Sparklines ✅ COMPLETE
- [x] Mini bar charts (14-day) for daily activity under KPI strip items
- [x] Session count, tokens, cost, errors sparklines on dashboard + project detail
- [x] Pure CSS implementation (no JS library), color-coded per metric

### 2.3 Cost Breakdown Charts ✅ COMPLETE
- [x] **MCP server cost pie chart**: Conic-gradient donut chart computed server-side, `template.CSS` for safe style injection, 10-color palette, legend with percentages
- [x] **Budget overlay line**: Dashed red horizontal line on Cost Over Time chart, sum of project daily limits, bar rescaling when budget exceeds max cost
- [ ] Treemap or sunburst: project → backend → model → cost (deferred)

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

### 3.1 Slack Integration
- [ ] Rich Slack webhook payloads (blocks, buttons, color-coded alerts)
- [ ] Budget alerts with inline bar chart
- [ ] Daily/weekly digest: session count, cost, top errors, recommendations

### 3.2 Team & Ownership
- [ ] Multi-user: track who spawned which session (git user identity)
- [ ] Per-user cost tracking and budget allocation
- [ ] Team dashboard: compare agent usage across developers
- [ ] Billing entity per project (for client invoicing)

### 3.3 Export & Reporting
- [ ] Weekly PDF/HTML report per project (auto-generated)
- [ ] CSV export for cost data (for accounting)
- [ ] API for external dashboards (Grafana, Datadog)

### 3.4 Settings Web UI ✅ COMPLETE (full CRUD)
- [x] `/settings` page: display of all config sections (11 sections) with inline editing
- [x] Navbar gear icon, responsive grid layout, enabled/disabled color coding
- [x] **Inline CRUD via HTMX**: 4 input types — toggle (checkbox switch), select (dropdown), text, number
- [x] **POST /api/settings**: Updates `Config.Set()` + `Config.Save()`, returns HTMX partial with "saved" indicator
- [x] **~25 editable settings**: storage mode, auto capture, file blame, search engine, analysis adapter/model, tagging, secrets mode, error classifier, dashboard page size/sort, scheduler tasks
- [x] **8 tests**: toggle, select, text, number, missing key, invalid value, no config, persistence to disk
- [ ] CRUD for project classifiers and budgets (complex nested objects — future)
- [ ] Live preview of classification rules against existing sessions (future)

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

### 4.3 Testing
- [ ] Integration tests for FTS5 search end-to-end
- [ ] Budget alert webhook delivery tests
- [ ] Classifier cascade integration tests with mock sessions
