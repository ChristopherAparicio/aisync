# aisync — Next Session TODO

> Last updated: 2026-03-26

## Recently Completed

### Project Detail Page (2026-03-26)
- [x] **Project Detail Page** (`GET /projects/{path...}`): Full project-scoped view
  - Project header: display name, remote URL, provider badge, category badge
  - KPI strip: sessions, tokens, cost, errors, tool calls, 30d forecast (project-scoped)
  - Trend strip: 7d weekly comparison (sessions, tokens, errors) with project-level `TrendRequest.ProjectPath`
  - Two-column layout: activity feed (15 recent sessions) + aside panels (capabilities, branches, analytics, forecast)
  - Analytics panel: 30d tool calls, skill loads, top tools from event buckets
  - Quick action links: All sessions, Analytics, Costs (filtered by project)
- [x] **Sidebar links updated**: All 7 templates now link to `/projects/{path}` instead of `/?project=` or `/{page}?project=`
- [x] **Projects list page**: Cards now link to project detail page
- [x] **TrendRequest.ProjectPath**: Added project filtering to weekly trend analysis
- [x] **CSS**: Project detail header, analytics summary panel, responsive breakpoints
- [x] 1784 tests passing

### UX Redesign v2 (2026-03-26)
- [x] **Home Page Redesign**: GitHub-style two-column layout (feed + aside panels)
  - KPI strip: sessions, tokens, cost, errors, tool calls, 30d forecast
  - Inline trend strip: 7d weekly comparisons (sessions, tokens, errors)
  - Left column: activity feed with enriched session cards (project link, branch badge, tags, stats, actions)
  - Right column: Top Branches panel + Forecast panel + Capabilities panel
- [x] **Global Search (Ctrl+K / Cmd+K)**: Modal overlay with HTMX live search
  - Click navbar search trigger or press Ctrl+K / / to open
  - 250ms debounce, returns top 8 results with link to full results
  - `GET /partials/search-results?keyword=...` endpoint
  - `templates/search_results.html` partial template
- [x] **Restore Buttons**: Already on session cards in the activity feed
- [x] **Sessions page keyword search**: Already existed via `SearchRequest.Keyword`
- [x] **Data Backfills (production)**:
  - `aisync backfill remote-url`: 237 candidates, 39 updated
  - `aisync backfill forks`: 1161 scanned, 55 forks detected
  - `aisync backfill events`: 1161 sessions processed (2m43s)
  - `aisync usage compute`: 1295 buckets, 169K messages scanned
- [x] **CLI: `aisync backfill events`** — Extract session events + recompute analytics buckets
  - Supports `--session` (single), `--recompute-only`, `--json` flags
- [x] 1784 total tests passing

### Backend Tasks (2026-03-25)
- [x] **Worktree Dedup Fix**: `resolveRemoteURLForPath()` fallback — resolves git remote from session's ProjectPath
- [x] **Backfill remote_url**: `ListSessionsWithEmptyRemoteURL()` + `UpdateRemoteURL()` store methods
- [x] **BackfillRemoteURLs service**: groups by project_path to avoid redundant git calls
- [x] **DetectForksBatch service**: batch fork detection across all sessions, persists to session_forks
- [x] **Get() overlay**: mutable columns (remote_url, session_type, project_category, status) now overlay JSON payload
- [x] **CLI: `aisync backfill remote-url`** — resolves git remotes for sessions missing them
- [x] **CLI: `aisync backfill forks`** — detects fork relationships across all sessions
- [x] **CLI: `aisync usage compute`** — on-demand token usage bucket computation
- [x] **API: `POST /api/v1/backfill/remote-url`** — manual trigger endpoint
- [x] **API: `POST /api/v1/backfill/forks`** — manual trigger endpoint
- [x] **Scheduler tasks**: `BackfillRemoteURLTask` + `ForkDetectionTask`
- [x] **LiteLLM auto-refresh**: background refresh on server start when cache >7 days

### Pricing Catalog Refactoring + LiteLLM Integration (Phase 10.8)
- [x] `Catalog` port interface with `Lookup(model)` and `List()` methods
- [x] `EmbeddedCatalog` adapter: YAML source of truth (go:embed, 15 models, tiers)
- [x] `OverrideCatalog` decorator: user config overrides on base catalog
- [x] `LiteLLMCatalog` adapter: 2500+ models from GitHub JSON cache
- [x] `FallbackCatalog`: chains LiteLLM → Embedded (first match wins)
- [x] `aisync update-prices` CLI command with `--info` flag
- [x] Tiered/dynamic pricing with multipliers (Opus 4, Gemini 2.5 Pro/Flash)
- [x] Factory wiring: full chain with graceful degradation
- [x] 88 pricing tests

### Error Classification (Phase 10.6)
- [x] Domain model, deterministic classifier, OpenCode extraction
- [x] SQLite store, API endpoints, CLI, MCP tool, scheduler task
- [x] Dashboard filters: Status + HasErrors

---

## Priority 1: Quick Actions & Navigation

### 1.1 Analyze on Demand
Add "Analyze" button for sessions without objectives (session detail page).

### 1.2 Fork Tree Navigation
Show clickable mini-tree in session list for fork relationships.

### 1.3 Project Page Enhancements
- [ ] Daily activity sparkline/mini bar chart in analytics panel
- [ ] Error rate trend chart (last 30d)
- [ ] Session type breakdown (pie/donut)

---

## Priority 2: Future Error Analysis

- [ ] LLM classifier for ambiguous tool errors (future, low priority)
- [ ] `CompositeClassifier` — deterministic first, LLM fallback for "unknown"

---

## Priority 3: Technical Debt

- [ ] Compute objectives for existing sessions (batch job)
- [x] Auto-refresh LiteLLM cache on startup when stale (>7 days)
