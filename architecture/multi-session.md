# Multi-Session per Branch — Feature Architecture

> Phase 5.1 — Remove the 1:1 branch-to-session constraint.

## Problem

Currently, aisync enforces **one session per branch**. Each `aisync capture` on the same branch **overwrites** the previous session. This destroys history:

- You lose all previous AI conversations on a long-lived branch.
- Rewinds and forks are the only way to have multiple sessions, but they're manual.
- If a colleague captures on the same branch (via sync), their session replaces yours.
- No way to compare how different AI sessions approached the same feature.

## Goal

Allow **multiple sessions per branch** while keeping the UX simple. Commands that previously assumed "the session" now mean "the latest session" unless specified otherwise.

## Design Principle: Minimal Breaking Change

The database schema already supports multi-session (no UNIQUE constraint on branch). The constraint is enforced **only in application code** — a single dedup block in `capture/service.go:136-141`. The migration strategy is:

1. **Remove the dedup block** — new captures create new sessions.
2. **Rename `GetByBranch` → keep as "get latest"** — callers that need "the session" get the most recent one.
3. **Add `CountByBranch`** — enables UI to show "3 sessions on this branch".
4. **Update hooks and CLI messages** — plural-aware wording.
5. **`aisync list` already works** — no change needed.

---

## What Changes

### Layer 1: Capture (remove dedup)

**File:** `internal/capture/service.go:136-141`

```go
// BEFORE (removes):
if existing, lookupErr := s.store.GetByBranch(sess.ProjectPath, sess.Branch); lookupErr == nil {
    sess.ID = existing.ID
}

// AFTER: Nothing — every capture creates a new session with a fresh ID.
```

The `ON CONFLICT(id) DO UPDATE` upsert in SQLite remains for idempotent re-saves (e.g., summarize re-save), but never triggers from dedup anymore.

### Layer 2: Store interface

**File:** `internal/storage/store.go`

```go
// RENAME existing method for clarity:
// GetByBranch → GetLatestByBranch (same signature, same behavior)
GetLatestByBranch(projectPath string, branch string) (*session.Session, error)

// ADD new method:
// CountByBranch returns the number of sessions for a project+branch pair.
CountByBranch(projectPath string, branch string) (int, error)
```

**Why `CountByBranch` and not `ListByBranch`?** We don't need a new list method — `List(ListOptions{Branch: "...", ProjectPath: "..."})` already returns all sessions for a branch. What we need is a lightweight count for UI messages ("3 sessions on this branch") without loading full summaries.

### Layer 3: SQLite implementation

**File:** `internal/storage/sqlite/sqlite.go`

```go
// Rename GetByBranch → GetLatestByBranch (same SQL, same LIMIT 1)
func (s *Store) GetLatestByBranch(projectPath, branch string) (*session.Session, error) {
    // ... same as today: ORDER BY created_at DESC LIMIT 1
}

// New method
func (s *Store) CountByBranch(projectPath, branch string) (int, error) {
    var count int
    err := s.db.QueryRow(
        "SELECT COUNT(*) FROM sessions WHERE project_path = ? AND branch = ?",
        projectPath, branch,
    ).Scan(&count)
    return count, err
}
```

### Layer 4: All callers of GetByBranch

Every caller of `GetByBranch` → rename to `GetLatestByBranch`. Behavior is identical — they already get the latest session. The rename is purely for clarity: the name now communicates that other sessions may exist.

| Caller | File | What changes |
|--------|------|-------------|
| Capture dedup | `internal/capture/service.go:139` | **REMOVED** (dedup block deleted) |
| Restore fallback | `internal/restore/service.go:139` | Rename call |
| `resolveSession` | `internal/service/session.go:1220` | Rename call |
| Link resolution | `internal/service/session.go:526` | Rename call |
| Comment resolution | `internal/service/session.go:662` | Rename call |
| Status command | `pkg/cmd/status/status.go:73` | Rename call + show count |
| TUI dashboard | `internal/tui/dashboard.go:89` | Rename call + show count |
| **14 mockStores** | Various test files | Rename method |

### Layer 5: UI improvements

#### `aisync status` — show session count

```
# Before:
Session:   abc123 (claude-code, 15 messages)

# After (1 session):
Session:   abc123 (claude-code, 15 messages)

# After (3 sessions):
Sessions:  3 on this branch (latest: abc123, claude-code, 15 messages)
```

#### `aisync list` — mark latest

No change needed structurally. `List()` already returns all sessions for a branch sorted by `created_at DESC`. The first one is the latest.

#### Git hooks — plural-aware

**commit-msg.sh:** `head -1` already picks the latest — correct behavior. No functional change, just update the comment.

**post-checkout.sh:**
```bash
# Before:
echo "[aisync] AI session available for this branch. Run 'aisync restore' to load context."

# After:
SESSION_COUNT=$(aisync list --quiet 2>/dev/null | wc -l | tr -d ' ')
if [ "$SESSION_COUNT" -gt 1 ]; then
    echo "[aisync] $SESSION_COUNT AI sessions on this branch. Run 'aisync list' or 'aisync restore' for the latest."
elif [ "$SESSION_COUNT" -eq 1 ]; then
    echo "[aisync] AI session available for this branch. Run 'aisync restore' to load context."
fi
```

**pre-commit.sh:** No change — `aisync capture --auto` creates a new session (no longer overwrites).

---

## What Does NOT Change

| Component | Why no change |
|-----------|---------------|
| `List()` / `List` SQL | Already returns all sessions per branch |
| `aisync list` CLI | Already displays multiple sessions |
| `AddLink` / `GetByLink` | No branch uniqueness assumed |
| Database schema | No UNIQUE constraint on (project_path, branch) — already multi-safe |
| `aisync explain` / `aisync rewind` | Take explicit session ID — no branch resolution |
| `aisync blame` | Returns all sessions that touched a file — already multi-safe |
| `aisync search` | Returns all matching sessions — already multi-safe |
| `aisync export/import` with `--session` | Explicit ID — no branch resolution |
| Client SDK methods | Follow API contracts — transparent |

---

## Implementation Order

Each step leaves the codebase green (all tests pass):

| Step | What | Files | Impact |
|------|------|-------|--------|
| **1** | Add `CountByBranch` to Store interface + SQLite impl | `internal/storage/store.go`, `internal/storage/sqlite/sqlite.go` | + 1 method on Store, all 14 mockStores must add stub |
| **2** | Rename `GetByBranch` → `GetLatestByBranch` in Store interface + SQLite | Same files + all callers + all mockStores | Pure rename, no behavior change |
| **3** | Remove dedup block in capture | `internal/capture/service.go` | The core change — 5 lines deleted |
| **4** | Update `aisync status` to show count | `pkg/cmd/status/status.go` | UI improvement |
| **5** | Update `post-checkout.sh` hook template | `internal/hooks/templates/post-checkout.sh` | Plural-aware message |
| **6** | Tests: capture creates distinct sessions, count works | `internal/capture/service_test.go`, `internal/storage/sqlite/sqlite_test.go` | Verify multi-session behavior |
| **7** | Update TUI dashboard to show count | `internal/tui/dashboard.go` | UI improvement |
| **8** | Documentation updates | `architecture/README.md`, `roadmap.md`, `LLM.md` | Docs |

### Step Dependency Graph

```
Step 1 (CountByBranch) ─┐
                        ├──▶ Step 4 (status)
Step 2 (rename)  ───────┤
                        ├──▶ Step 5 (hooks)
Step 3 (dedup removal) ─┤
                        ├──▶ Step 6 (tests)
                        ├──▶ Step 7 (TUI)
                        └──▶ Step 8 (docs)
```

Steps 1, 2, 3 are sequential (each extends the interface). Steps 4-7 can be done in any order after 1-3.

---

## MockStore Update Plan

Adding `CountByBranch` adds 1 method to the `Store` interface. All 14 mockStores need a stub:

```go
func (m *mockStore) CountByBranch(projectPath, branch string) (int, error) { return 0, nil }
```

Renaming `GetByBranch` → `GetLatestByBranch` is a pure rename across all 14 mockStores.

**Locations (14 files):**
1. `internal/capture/service_test.go`
2. `internal/restore/service_test.go`
3. `internal/service/session_test.go`
4. `pkg/cmd/capture/capture_test.go`
5. `pkg/cmd/commentcmd/commentcmd_test.go`
6. `pkg/cmd/export/export_test.go`
7. `pkg/cmd/importcmd/importcmd_test.go`
8. `pkg/cmd/linkcmd/linkcmd_test.go`
9. `pkg/cmd/listcmd/list_test.go`
10. `pkg/cmd/restore/restore_test.go`
11. `pkg/cmd/searchcmd/searchcmd_test.go`
12. `pkg/cmd/secrets/secrets_test.go`
13. `pkg/cmd/statscmd/statscmd_test.go`
14. `pkg/cmd/blamecmd/blamecmd_test.go`

---

## Migration

No data migration needed. Existing sessions remain as-is — they simply become "the only session" on their branch. Future captures create new sessions alongside them.

---

## Performance

| Concern | Strategy |
|---------|----------|
| **More sessions in DB** | SQLite handles millions of rows. Indexed on `branch`. No concern. |
| **`CountByBranch` cost** | `SELECT COUNT(*)` with index is O(log N). Negligible. |
| **`GetLatestByBranch` unchanged** | Same `LIMIT 1` query. O(1) with index. |
| **`List` unchanged** | Already returns all sessions for branch. Pagination via SearchQuery if needed. |
| **Hook performance** | `aisync list --quiet | wc -l` adds ~50ms. Acceptable for post-checkout. |

---

## Contract

> **Every capture creates a new session** — history is never overwritten.
> **"Get by branch" means "get latest"** — the rename makes this explicit.
> **Commands without `--session` default to latest** — same behavior as today, just documented.
> **No schema migration** — the database was already multi-session capable.
> **Backward compatible** — existing sessions, hooks, and workflows continue to work.
