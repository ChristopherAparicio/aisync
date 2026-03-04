# AI-Blame — Feature Architecture

> `aisync blame <file>` — find which AI sessions touched a file.

## What

AI-Blame is the `git blame` equivalent for AI sessions. Given a file path, it looks up all sessions that touched that file (via the `file_changes` table) and returns them with metadata: session ID, provider, branch, date, summary, change type.

Use cases:

1. **"Who (which AI) broke this file?"** — Find the session, read the conversation, understand the decisions
2. **"Restore the context"** — `--restore` flag to directly restore the session that last touched the file
3. **"Cross-provider traceability"** — See Claude Code created the file, OpenCode modified it later
4. **"Audit trail"** — Full history of AI-assisted changes to any file

## How

### Layer Responsibilities

| Layer | What it does |
|-------|-------------|
| **Domain** | `BlameEntry` type (session summary + change type + timestamp) |
| **Store (port)** | `GetSessionsByFile(filePath, opts) → []BlameEntry` — SQL query |
| **Service** | `Blame(ctx, BlameRequest) → []BlameEntry` — orchestrates query + optional restore |
| **CLI** | `aisync blame <file>` — formats table output |
| **API** | `GET /api/v1/blame?file=...` — JSON response |
| **MCP** | `aisync_blame` tool — callable from AI assistants |

### Domain Types

```go
// BlameEntry represents one session that touched a file.
type BlameEntry struct {
    SessionID   session.ID
    Provider    session.ProviderName
    Branch      string
    Summary     string
    ChangeType  session.ChangeType    // created, modified, deleted, read
    CreatedAt   time.Time
    OwnerID     session.ID            // who ran the session
}

// BlameRequest contains parameters for a blame lookup.
type BlameRequest struct {
    FilePath string                   // required — relative to project root
    All      bool                     // false = last session only, true = all sessions
    Branch   string                   // optional filter
    Provider session.ProviderName     // optional filter
}
```

### SQL Query

```sql
-- Basic blame: all sessions that touched a file
SELECT s.id, s.provider, s.branch, s.summary, s.created_at, s.owner_id,
       fc.change_type
FROM sessions s
JOIN file_changes fc ON fc.session_id = s.id
WHERE fc.file_path = ?
ORDER BY s.created_at DESC;

-- With optional filters
-- ... AND s.branch = ?
-- ... AND s.provider = ?

-- Single (default): add LIMIT 1
-- All (--all flag): no LIMIT
```

### Key Design Decisions

1. **Service layer orchestrates, Store layer queries** — The service decides *what* to ask for (file path, filters). The store decides *how* to get it (SQL, indexes). If we optimize later, only the store changes.

2. **`--restore` is a convenience shortcut** — It calls `Blame()` to get the last session, then calls `Restore()` with that session ID. No new logic needed — just composition.

3. **File path matching** — Paths stored in `file_changes` are relative to the project root (as captured by providers). The CLI normalizes the input path before querying.

4. **No line-level blame** — Unlike `git blame` which shows per-line authorship, `aisync blame` operates at the file level. Per-line attribution would require storing diffs (not just file paths), which is a Phase 6+ optimization.

## Performance

### Current State

The `file_changes` table already has an index:

```sql
CREATE INDEX idx_files_path ON file_changes(file_path);
```

For a typical project with ~100 sessions and ~1000 file changes, the query is instant (<1ms). The JOIN with `sessions` uses the primary key (`sessions.id`), which is also indexed.

### Known Limitations

| Scenario | Impact | Threshold |
|----------|--------|-----------|
| Large monorepo with 10k+ sessions | Query may take 10-50ms | >5000 sessions |
| File touched by 100+ sessions (`--all`) | Large result set | >100 entries |
| Wildcard/glob patterns (future) | Full table scan on `file_path` | Any size |

### Optimization Path

All optimizations are **Store-layer only** — service, API, CLI, MCP adapters never change.

| Level | What | When | Store change |
|-------|------|------|-------------|
| **L0 (current)** | Index on `file_changes(file_path)` | Already done | — |
| **L1** | Composite index `(file_path, session_id)` | If JOINs slow down | Add index |
| **L2** | Denormalized `blame_cache` table | If >10k sessions | New table, populated at capture time |
| **L3** | In-memory LRU cache in Store | If same files queried repeatedly | Add cache layer in SQLite Store |
| **L4** | FTS5 on file paths | If glob/wildcard patterns needed | Virtual table |

### Contract

> **Only the Store (persistence) layer changes for optimization. The `Blame()` service method, API endpoint, MCP tool, and CLI command remain unchanged.**

This is guaranteed by the port interface: `GetSessionsByFile()` returns `[]BlameEntry` regardless of how the data is fetched internally.
