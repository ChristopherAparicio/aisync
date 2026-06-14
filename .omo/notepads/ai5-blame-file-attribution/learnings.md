# Learnings — ai5-blame-file-attribution

## [2026-06-14] Init: Codebase conventions (grounded)

- Go module: `github.com/ChristopherAparicio/aisync`
- `BlameEntry` struct: `internal/session/session.go:985-994` — no Agent field yet
- `Agent string \`json:"agent"\`` pattern: already in `Summary` (:91) and `VoiceSummary` (:981) — **copy exactly**
- `BlameQuery`: `session.go:996-1003` — only `FilePath string` today; `FilePaths []string` to be added
- `ProjectFileEntry`: `internal/session/fileops.go:464-476` — has LastProvider/LastBranch/LastCommitSHA, add LastAgent
- `GetSessionsByFile`: `sqlite.go:1907-1932` — JOIN sessions exists, COALESCE(s.agent,'') needed in SELECT
- `file_changes` schema: `sqlite.go:46-51` — no agent col → agent MUST come from JOIN sessions
- `FilesForProject`: `sqlite.go:2148` with CTE `last_session` at `:2175-2186` — add `last_agent`
- `handleBlame`: `tools.go:339-374` — only `file` string today; add `files` array
- MCP registration: `server.go:171-177` — tool schema to extend
- MCP tests: `server_test.go:393-445` — existing suite to extend
- Web handler is ONLY other consumer of FilesForProject — must not break: `internal/web/handlers.go`

## TDD Protocol
- RED (failing test) FIRST, then GREEN implementation, then REFACTOR
- Table-driven tests following existing sqlite_test.go patterns
- Evidence: `.omo/evidence/task-{N}-{slug}.txt`

## Guardrails
- NO ALTER on file_changes table
- NO N+1 — agent comes from same JOIN SELECT
- NO glob/wildcard on paths
- NO --quiet pollution (agent col only in table mode)
- NO new CLI command (--project is a flag on blame)
- NO MCP project mode (only files[]/file for MCP)
- [2026-06-14] Added ProjectFileEntry.last_agent JSON serialization test and field.

## [2026-06-14] Task 1 edit summary

- `BlameEntry` now spans `internal/session/session.go:985-995` with `Agent string `json:"agent"`` inserted after `Provider`.
- `BlameQuery` now spans `internal/session/session.go:998-1005` with `FilePaths []string` added beside `FilePath string`.

## [2026-06-14] Task 3: Store layer — agent field + multi-file blame

### Changed SELECT lines in sqlite.go
- Line 1937: `GetSessionsByFile` SELECT — `COALESCE(s.agent, '')` inserted between `s.created_at` and `COALESCE(s.owner_id, '')`
- Line 2189: `FilesForProject` CTE `last_session` SELECT — `COALESCE(s.agent, '') AS last_agent` added after `last_commit_sha`... correction: added after `last_provider` (before `last_commit_sha`)
- Line 2197: `FilesForProject` outer SELECT — `last_agent` added between `last_provider` and `last_commit_sha`

### Changed Scan lines in sqlite.go
- Line 1957: `GetSessionsByFile` Scan — `&e.Agent` added between `&createdAt` and `&e.OwnerID`
- Line 2220: `FilesForProject` Scan — `&e.LastAgent` added between `&provider` and `&e.LastCommitSHA`

### Multi-file IN clause (GetSessionsByFile lines 1911-1921)
- `strings.TrimRight(strings.Repeat("?,", len(q.FilePaths)), ",")` used for safe placeholder construction
- FilePaths takes priority over FilePath when non-empty (backward-compat: FilePath still used as fallback)

### TDD outcome
- RED: TestGetSessionsByFile_AgentFromJoin FAIL, TestGetSessionsByFile_MultiFile FAIL, TestFilesForProject_LastAgent FAIL
- TestGetSessionsByFile_EmptyAgent passed trivially in RED (zero-value string already matches expected "")
- GREEN: all 4 pass; 15 total blame/FilesForProject tests pass (11 existing + 4 new)
- go build ./... clean, go build ./internal/web/... clean
