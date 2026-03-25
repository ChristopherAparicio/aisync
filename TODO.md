# aisync — Next Session TODO

> Last updated: 2026-03-25

## Recently Completed

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
- [x] 1784 total tests passing

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

### 1.2 Project Page
When you click a project in the sidebar → project-scoped view.

### 1.3 Global Search
- Search bar always visible in navbar
- Keyboard shortcut: Ctrl+K or /

### 1.4 Search on Sessions Page
- Add keyword search input (already supported in `SearchRequest.Keyword`)

---

## Priority 2: Quick Actions

### 2.1 Restore Button
Add "Restore to OpenCode" button on session cards.

### 2.2 Analyze on Demand
Add "Analyze" button for sessions without objectives.

### 2.3 Fork Tree Navigation
Show clickable mini-tree in session list.

---

## Priority 3: Data Operations

### 3.1 Worktree Deduplication ✅ DONE
- [x] `ListProjects()` groups by `remote_url`
- [x] Capture-time fallback: `resolveRemoteURLForPath()`
- [x] Backfill CLI: `aisync backfill remote-url`

### 3.2 Branch Recovery ✅ DONE
- [x] Branches extracted from worktree paths

### 3.3 Run Backfill
- [ ] Run `aisync backfill remote-url` on production database
- [ ] Run `aisync backfill forks` on production database
- [ ] Re-capture existing sessions to populate error data + ContentBlocks

## Priority 4: Future Error Analysis

- [ ] LLM classifier for ambiguous tool errors (future, low priority)
- [ ] `CompositeClassifier` — deterministic first, LLM fallback for "unknown"

---

## Priority 5: Technical Debt

- [ ] Compute objectives for existing sessions (batch job)
- [ ] Consider auto-refresh: when LiteLLM cache is stale (>7 days), optionally auto-refresh on startup in background ✅ DONE
