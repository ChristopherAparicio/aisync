# aisync — Next Session TODO

> Last updated: 2026-03-31

## Recently Completed (2026-03-31)

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
- [ ] **File explorer page**: tree view of project files with session count badge (`/projects/{path...}/files`)
- [ ] **Directory-level blame**: "Show me all sessions that modified `auth/`" (glob/prefix query)

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

### 2.2 Activity Sparklines
- [ ] Mini bar charts for daily activity on KPI cards
- [ ] Session count sparkline on project cards
- [ ] Token usage sparkline on home dashboard

### 2.3 Cost Breakdown Charts
- [ ] Treemap or sunburst: project → backend → model → cost
- [ ] Timeline chart: daily cost trend with budget line overlay
- [ ] MCP server cost pie chart

### 2.4 Session Timeline View
- [ ] Session dependency graph (parent → child → fork relationships)
- [ ] Timeline of a session: messages, tool calls, errors as a Gantt-like chart

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

### 3.4 Settings Web UI
- [ ] `/settings` page: visual editor for per-project config
- [ ] CRUD for project classifiers, budgets, search engine selection
- [ ] Live preview of classification rules against existing sessions

---

## Priority 4: Technical Debt

### 4.1 Existing
- [ ] Compute objectives for existing sessions (batch job)
- [ ] LLM classifier for ambiguous tool errors
- [ ] `CompositeClassifier` — deterministic first, LLM fallback for "unknown"

### 4.2 Performance
- [ ] Incremental FTS5 indexing (only new/modified sessions)
- [ ] Lazy loading for costs page sections (HTMX partial load)
- [ ] Cache expensive computations (CacheEfficiency, BudgetStatus)

### 4.3 Testing
- [ ] Integration tests for FTS5 search end-to-end
- [ ] Budget alert webhook delivery tests
- [ ] Classifier cascade integration tests with mock sessions
