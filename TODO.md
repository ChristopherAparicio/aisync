# aisync — Next Session TODO

## Priority 1: Fix Data Quality

### 1.1 Worktree Deduplication
OpenCode creates worktrees per session/branch (`~/.local/share/opencode/worktree/{hash}/{name}`).
These all point to the same git repo but appear as separate "projects" in aisync.

**Problem:** 40+ "projects" shown when there are really 5-6 repos.

**Fix:**
- In `ListProjects()` and storage layer: group by `remote_url` instead of `project_path`
- When `remote_url` is empty, fall back to the **repo root** (resolve via `git rev-parse --show-toplevel` or check if multiple paths share the same `.git` origin)
- The display name should be the repo name (e.g., "omogen/backend") not the worktree folder name ("sunny-wizard")
- Store a `canonical_project_path` on capture that resolves worktrees to their source repo

### 1.2 Branch Recovery
Sessions captured with `--all` lost their branch info (all set to current branch at capture time).

**Fix:**
- For OpenCode: the worktree directory name often IS the branch name (e.g., `worktree/.../feat/assign-criteria-command`)
- Parse the worktree path to extract the branch name when the session has no branch
- Run a one-time backfill to fix existing sessions

### 1.3 Search on Sessions Page
The sessions page (`/sessions`) has filter dropdowns but no **text search** field for the session title/summary.

**Fix:**
- Add a keyword search input at the top of the sessions filter bar
- Filter by session Summary (title) with case-insensitive LIKE query
- Already supported in `SearchRequest.Keyword` — just needs to be wired in the UI

---

## Priority 2: GitHub-style Layout

### 2.1 Home Page Redesign
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

### 2.2 Project Page
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

### 2.3 Global Search
- Search bar always visible in navbar
- Searches across session titles, branches, project names
- Keyboard shortcut: Ctrl+K or /
- Autocomplete suggestions from recent sessions

---

## Priority 3: Quick Actions

### 3.1 Restore Button
Add "Restore to OpenCode" button on session cards and session detail page.
Generates and copies the `aisync restore <id>` command.

### 3.2 Analyze on Demand  
Add "Analyze" button that triggers `ComputeObjective()` for sessions without objectives.

### 3.3 Fork Tree Navigation
When a session has forks, show a clickable mini-tree directly in the session list.

---

## Technical Debt

- [ ] Run fork detection on all 585 sessions and persist to `session_forks` table
- [ ] Compute objectives for existing sessions (batch job)
- [ ] Fix the analytics page (route commented out, handler missing)
- [ ] Re-capture sessions to populate ContentBlocks and ImageMeta
- [ ] Add `aisync usage compute` CLI command for on-demand bucket computation
