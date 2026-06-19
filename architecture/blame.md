# AI-Blame — Feature Architecture

> `aisync blame <file>` — find which AI sessions touched a file.

## What

AI-Blame is the `git blame` equivalent for AI sessions. Given one or more file paths, it looks up all sessions that touched those files (via the `file_changes` table) and returns them with metadata: session ID, provider, agent, branch, date, summary, change type.

Use cases:

1. **"Who (which AI) broke this file?"** — Find the session, read the conversation, understand the decisions
2. **"Restore the context"** — `--restore` flag to directly restore the session that last touched the file
3. **"Cross-provider traceability"** — See Claude Code created the file, OpenCode modified it later
4. **"Audit trail"** — Full history of AI-assisted changes to any file
5. **"Project overview"** — `--project` flag to list every file touched in a project with its last agent

## How

### Layer Responsibilities

| Layer | What it does |
|-------|-------------|
| **Domain** | `BlameEntry` type (session summary + change type + agent + timestamp + `FilePath` for grouping); `BlameQuery` with `FilePaths []string`; `ProjectFileEntry` with `LastAgent string` |
| **Store (port)** | `GetSessionsByFile(query BlameQuery) → []BlameEntry` — single-file or multi-file SQL query (multi-file selects `fc.file_path` into `BlameEntry.FilePath`); `FilesForProject(path, branch, limit) → []ProjectFileEntry` — CTE-based project scan |
| **Service** | `Blame(ctx, BlameRequest) → *BlameResult` — routes to the right store method; groups multi-file results by file (`FilesGrouped`); handles optional restore shortcut; `ParseFileManifest([]byte) → []string` for manifest input |
| **CLI** | `aisync blame [file...]` — multi-file args via `cobra.ArbitraryArgs`; `--files-from` manifest; `--project` flag; AGENT column; multi-file output grouped by file (FILE column) |
| **API** | `GET /api/v1/blame?file=...` — JSON response |
| **MCP** | `aisync_blame` tool — accepts `file` (string), `files` (array), or `files_from` (manifest path); `files`/`files_from` take priority |

### Domain Types

```go
// BlameEntry represents one session that touched a file.
type BlameEntry struct {
    SessionID  session.ID
    Provider   session.ProviderName
    Agent      string             // from sessions.agent via JOIN; empty renders as "-"
    Branch     string
    Summary    string
    ChangeType session.ChangeType // created, modified, deleted, read
    CreatedAt  time.Time
    OwnerID    session.ID
    FilePath   string             // json:"file_path,omitempty" — populated in multi-file mode for grouping
}

// BlameQuery contains parameters for a file blame lookup.
type BlameQuery struct {
    FilePath  string             // single-file path (used when FilePaths is empty)
    FilePaths []string           // multi-file: WHERE file_path IN (?,?)
    Branch    string             // optional filter
    Provider  session.ProviderName // optional filter
    Limit     int
}

// ProjectFileEntry is one row in the project-wide file listing.
type ProjectFileEntry struct {
    FilePath       string
    LastSessionID  session.ID
    LastAgent      string         // from sessions.agent via CTE; empty renders as "-"
    LastProvider   session.ProviderName
    LastBranch     string
    LastCommitSHA  string
    LastSessionTime time.Time
}
```

### Agent Attribution

The `agent` field does not live in `file_changes`. It comes from the `sessions` table via the JOIN that `GetSessionsByFile` already performs. The SELECT uses `COALESCE` so that sessions with no agent value produce an empty string rather than NULL:

```sql
COALESCE(s.agent, '') AS agent
```

The CLI renders an empty agent as `"-"` in table output. JSON output preserves the empty string (no `omitempty`).

### SQL Queries

**Single-file blame** (default, `FilePaths` empty):

```sql
SELECT s.id, s.provider, COALESCE(s.agent, ''), s.branch, s.summary,
       s.created_at, COALESCE(s.owner_id, ''),
       fc.change_type
FROM sessions s
JOIN file_changes fc ON fc.session_id = s.id
WHERE fc.file_path = ?
ORDER BY s.created_at DESC
LIMIT 1;  -- omitted when --all is set
```

**Multi-file blame** (`FilePaths` non-empty, `WHERE ... IN`):

```sql
SELECT s.id, s.provider, COALESCE(s.agent, ''), s.branch, s.summary,
       s.created_at, COALESCE(s.owner_id, ''),
       fc.change_type
FROM sessions s
JOIN file_changes fc ON fc.session_id = s.id
WHERE fc.file_path IN (?, ?)   -- placeholders built dynamically, no glob
ORDER BY s.created_at DESC;
```

Placeholders are constructed with `strings.Repeat("?,", n)` trimmed of the trailing comma. No glob or wildcard expansion is supported; callers pass an explicit list.

**Project-wide listing** (`--project` mode, `FilesForProject`):

```sql
WITH last_session AS (
    SELECT fc.file_path,
           s.id          AS last_session_id,
           s.provider    AS last_provider,
           COALESCE(s.agent, '') AS last_agent,
           s.branch      AS last_branch,
           s.commit_sha  AS last_commit_sha,
           s.created_at  AS last_session_time,
           ROW_NUMBER() OVER (PARTITION BY fc.file_path ORDER BY s.created_at DESC) AS rn
    FROM file_changes fc
    JOIN sessions s ON s.id = fc.session_id
    WHERE s.project_path = ?
)
SELECT file_path, last_session_id, last_provider, last_agent,
       last_branch, last_commit_sha, last_session_time
FROM last_session
WHERE rn = 1
ORDER BY last_session_time DESC;
```

The CTE `last_session` uses `ROW_NUMBER()` partitioned by `file_path` to pick the most recent session per file in a single pass. `last_agent` is scanned directly into `ProjectFileEntry.LastAgent`.

### Service Routing

`SessionService.Blame()` dispatches to the correct store method based on the request fields:

| Condition | Store call | Result field |
|-----------|-----------|-------------|
| `ProjectPath != ""` and no file paths | `FilesForProject(path, branch, limit)` | `BlameResult.ProjectFiles` |
| `len(FilePaths) > 0` | `GetSessionsByFile(BlameQuery{FilePaths: ...})` then `groupBlameByFile()` | `BlameResult.FilesGrouped` |
| `FilePath != ""` | `GetSessionsByFile(BlameQuery{FilePath: ...})` | `BlameResult.Entries` (+ optional Restore) |
| None set | error: "file path or project path is required" | — |

The `--restore` shortcut only fires in single-file mode (`FilePath` set). It calls `SessionService.Restore()` with the session ID from the first entry.

### Multi-file Grouping

Multi-file blame returns results **grouped by file** rather than a flat entry list. `groupBlameByFile(files, entries, all)` buckets the store's `[]BlameEntry` by `BlameEntry.FilePath`, then emits one `FileBlame` per requested path, preserving the requested order and deduplicating repeated paths:

```go
type FileBlame struct {
    File     string               `json:"file"`
    Sessions []session.BlameEntry `json:"sessions"` // most-recent first; empty if file untouched
}
```

Rules:

- Entries arrive `created_at DESC` from the store, so each bucket stays most-recent-first (no re-sort, no N+1 — one query for all files).
- A requested file with no matching entries yields an empty `Sessions` slice (the file still appears in the output).
- When `all` is false, only the most recent session per file is kept (`sessions[:1]`).

This unifies all multi-file inputs — variadic CLI args, MCP `files[]`, and `--files-from`/`files_from` manifests — on the same grouped shape. Single-file blame keeps the flat `Entries` shape so `--restore` composition is unchanged.

### Manifest Input

`ParseFileManifest(content []byte) ([]string, error)` reads a file list from a manifest, auto-detecting the format by the first non-whitespace byte:

- Starts with `[` → parsed as a JSON array of strings (a malformed array is a hard error).
- Otherwise → one path per line, skipping blank lines and `#` comments, trimming surrounding whitespace.

Both the CLI (`--files-from`) and MCP (`files_from`) read the file from disk and merge the parsed paths into the file list before routing. The manifest path itself is never queried.

### Key Design Decisions

1. **Agent comes from the JOIN, not `file_changes`** — The `file_changes` table has no `agent` column and never will. Agent attribution is a session-level property, so it flows through the existing `sessions` JOIN at zero extra cost.

2. **`FilePaths` takes priority over `FilePath`** — The CLI sends `FilePath` for a single argument (to preserve the `--restore` shortcut) and `FilePaths` for two or more. The store checks `len(FilePaths) > 0` first.

3. **No glob support** — Multi-file blame requires an explicit list. Glob expansion would need a full table scan on `file_path` (no index benefit). If needed in the future, FTS5 on file paths is the right path (see L4 in the optimization table below).

4. **`--project` is CLI-only** — The MCP tool does not expose a project mode. MCP callers use `file` or `files[]` for targeted lookups; project-wide scans are better served by the CLI or the HTTP API.

5. **`--restore` is a convenience shortcut** — It calls `Blame()` to get the last session, then calls `Restore()` with that session ID. No new logic needed, just composition.

6. **File path matching (dual-format)** — `file_changes.file_path` uses two formats by design: files **inside** the project root are stored **relative** to it (e.g. `internal/storage/sqlite/sqlite.go`), while files **outside** the project root are kept **absolute** (e.g. `/etc/hosts`, `C:\tmp\x`). Normalization happens at the Store layer on write (`Save` + `ReplaceSessionFiles` call `session.NormalizeFilePath`), so the on-disk format is guaranteed regardless of the provider's input. On read, the Store expands each query path into both candidate forms via `session.BlameMatchCandidates(path, projectRoot)` and matches with `file_path IN (...)`, so a caller may pass either a relative path (matching `git status` output), an absolute path, or a path with a trailing slash — all resolve to the same stored row. Absolute-vs-relative is discriminated by whether the file lives under `project_path` (SQL: `file_path LIKE '/%' OR file_path LIKE '_:%'` for the Windows drive-letter case), never by a naive leading-slash check. Legacy rows captured before this scheme can be migrated in place with `aisync backfill normalize-paths` (idempotent; `--dry-run`/`--json` supported).

7. **No line-level blame** — Unlike `git blame` which shows per-line authorship, `aisync blame` operates at the file level. Per-line attribution would require storing diffs, which is a Phase 6+ optimization.

## CLI Usage

```bash
# Last session that touched a file (shows AGENT column)
aisync blame src/main.go

# All sessions that touched a file
aisync blame --all src/main.go

# Sessions that touched multiple files (explicit list, no glob; output grouped by file)
aisync blame src/a.go src/b.go

# Read the file list from a manifest (auto-detects a JSON array or one path per line)
aisync blame --files-from changed.txt
aisync blame --all --files-from changed.json

# Filter by branch
aisync blame --branch feat/auth handler.go

# Restore the last session that touched a file
aisync blame --restore handler.go

# Machine-readable output
aisync blame --json src/main.go

# List all files touched in a project (project mode)
aisync blame --project /path/to/project
```

**Single-file table columns:** `SESSION_ID`, `PROVIDER`, `AGENT`, `BRANCH`, `CHANGE`, `DATE`, `SUMMARY`

**Multi-file (grouped) table columns:** `FILE`, `SESSION_ID`, `PROVIDER`, `AGENT`, `BRANCH`, `CHANGE`, `DATE`, `SUMMARY` — rows are grouped by file; an untouched file still appears with empty session columns.

**Project-mode table columns:** `FILE`, `SESSION_ID`, `AGENT`, `DATE`

Empty agent values render as `"-"` in all table modes. `--quiet` prints only session IDs (single-file and grouped modes) or last session IDs (project mode). `--json` encodes the flat `Entries` list in single-file mode and the `FilesGrouped` array (`[{file, sessions}]`) in multi-file mode.

## MCP Tool

The `aisync_blame` MCP tool accepts a single file, an array of files, or a manifest path:

| Parameter | Type | Notes |
|-----------|------|-------|
| `file` | string | File path relative to project root; optional when `files` or `files_from` is set |
| `files` | string[] | Multiple file paths; takes priority over `file` when both are provided |
| `files_from` | string | Path to a manifest file (JSON array or one path per line, auto-detected); merged with `files` |
| `branch` | string | Optional filter by git branch |
| `provider` | string | Optional filter: `claude-code`, `opencode`, or `cursor` |
| `all` | boolean | If true, return all sessions (default: most recent only) |

At least one of `file`, `files`, or `files_from` is required. When `files`/`files_from` resolve to two or more paths, the response is grouped by file (`{file, sessions}` array), matching the CLI. Project mode is not available via MCP.

## Performance

### Current State

The `file_changes` table has an index:

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

This is guaranteed by the port interface: `GetSessionsByFile()` returns `[]BlameEntry` and `FilesForProject()` returns `[]ProjectFileEntry` regardless of how the data is fetched internally.
