# aisync — Next Session TODO

> Last updated: 2026-03-25

## Recently Completed

### Pricing Catalog Refactoring + LiteLLM Integration (Phase 10.8)
- [x] `Catalog` port interface with `Lookup(model)` and `List()` methods
- [x] `EmbeddedCatalog` adapter: YAML source of truth (go:embed, 15 models, tiers)
- [x] `OverrideCatalog` decorator: user config overrides on base catalog
- [x] `LiteLLMCatalog` adapter: 2500+ models from GitHub JSON cache
- [x] `FallbackCatalog`: chains LiteLLM → Embedded (first match wins)
- [x] `aisync update-prices` CLI command with `--info` flag
- [x] Tiered/dynamic pricing with multipliers (Opus 4, Gemini 2.5 Pro/Flash)
- [x] Factory wiring: full chain with graceful degradation
- [x] 88 pricing tests, 1761 total tests passing

### Error Classification (Phase 10.6)
- [x] Domain model, deterministic classifier, OpenCode extraction
- [x] SQLite store, API endpoints, CLI, MCP tool, scheduler task
- [x] Dashboard filters: Status + HasErrors

---

## Priority 1: UX Redesign v2 — GitHub-style Layout

### 1.1 Home Page Redesign
Current: KPI dashboard (monitor-first)
Target: GitHub-style "find & act" layout

```
┌─────────────────────────────────────────────────────────┐
│  aisync                    [Search sessions...]         │
├──────────────┬──────────────────────────────────────────┤
│  YOUR        │  Recent Sessions (all projects)          │
│  PROJECTS    │                                          │
│              │  📌 "Fix phone extraction" — omogen      │
│  ● omogen    │     feature · 42 msgs · [Restore] [View]│
│    backend   │                                          │
│  ● aisync    │  📌 "Fork detection" — aisync            │
│  ● cycloplan │     feature · 2966 msgs · [View]         │
│  ● opencode  │                                          │
│  ● jarvis    │  📌 "iOS Import" — cycloplan             │
│              │     bug · 124 msgs · [View]              │
│              ├──────────────────────────────────────────┤
│              │  Quick Stats (compact, right side)       │
│              │  585 sessions · 13B tokens · 899 errors  │
└──────────────┴──────────────────────────────────────────┘
```

**Implementation:**
- CSS grid layout: `grid-template-columns: 250px 1fr`
- Sidebar: project list (deduplicated, sorted by recent activity)
- Center: recent sessions feed with action buttons
- Bottom/right: compact KPIs

### 1.2 Project Page
When you click a project in the sidebar → project-scoped view:

```
omogen/backend
───────────────────────────────────────────────────
KPIs: 340 sessions | 2.1B tokens | subscription

Tabs: [Sessions] [Branches] [Usage] [Costs]

Sessions (filterable):
┌─────────────────────────────────────────────────┐
│ [Search...] [Branch ▾] [Type ▾]                │
│                                                  │
│ 📌 "Criteria deep assessment"        3h ago      │
│    feature · main · 2966 msgs · 587M tokens     │
│    3 forks · [Restore] [Analyze] [Fork Tree]    │
│                                                  │
│ 📌 "Blue-green deployment"           1d ago      │
│    ops · main · 1583 msgs                        │
│    [Restore] [Analyze]                           │
└─────────────────────────────────────────────────┘
```

### 1.3 Global Search
- Search bar always visible in navbar
- Searches across session titles, branches, project names
- Keyboard shortcut: Ctrl+K or /
- Autocomplete suggestions from recent sessions

### 1.4 Search on Sessions Page
The sessions page has filter dropdowns but no **text search** field for the session title/summary.
- Add a keyword search input at the top of the sessions filter bar
- Filter by session Summary (title) with case-insensitive LIKE query
- Already supported in `SearchRequest.Keyword` — just needs to be wired in the UI

---

## Priority 2: Quick Actions

### 2.1 Restore Button
Add "Restore to OpenCode" button on session cards and session detail page.
Generates and copies the `aisync restore <id>` command.

### 2.2 Analyze on Demand
Add "Analyze" button that triggers `ComputeObjective()` for sessions without objectives.

### 2.3 Fork Tree Navigation
When a session has forks, show a clickable mini-tree directly in the session list.

---

## Priority 3: Data Quality (Partially Done)

### 3.1 Worktree Deduplication ✅ DONE
- [x] `ListProjects()` groups by `remote_url`
- [x] Display name from repo name, not worktree folder

### 3.2 Branch Recovery ✅ DONE
- [x] Branches extracted from worktree paths
- [x] One-time backfill completed

### 3.3 Re-capture Sessions
- [ ] Re-capture existing sessions to populate error data
- [ ] Re-capture to populate ContentBlocks and ImageMeta

## Priority 4: Future Error Analysis

- [ ] LLM classifier for ambiguous tool errors (future, low priority)
- [ ] `CompositeClassifier` — deterministic first, LLM fallback for "unknown"

---

## Priority 5: Technical Debt

- [ ] Run fork detection on all 1085+ sessions and persist to `session_forks` table
- [ ] Compute objectives for existing sessions (batch job)
- [ ] Fix the analytics page (route commented out, handler missing)
- [ ] Re-capture sessions to populate ContentBlocks and ImageMeta
- [ ] Add `aisync usage compute` CLI command for on-demand bucket computation
- [ ] Consider auto-refresh: when LiteLLM cache is stale (>7 days), optionally auto-refresh on startup in background
