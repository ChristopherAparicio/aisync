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

## [2026-06-14] Task 4: Service layer — multi-file and project blame

### TDD Protocol
- RED: compilation failure — `BlameRequest.FilePaths` undefined, `BlameResult.ProjectFiles` undefined
- GREEN: 4 tests pass — TestBlame_SingleFile, TestBlame_MultiFile, TestBlame_Project, TestBlame_Validation

### Changes made
- `internal/testutil/mock_store.go`: added `ProjectFileEntries []session.ProjectFileEntry` field; updated `FilesForProject` to return `m.ProjectFileEntries`
- `internal/service/session_blame.go`: added `FilePaths []string` to `BlameRequest`; added `ProjectFiles []session.ProjectFileEntry` to `BlameResult`; implemented 3-way routing in `Blame()` with updated validation error message
- `internal/service/session_blame_test.go`: new file with `blameMockStore` extending local `mockStore`; 4 TDD tests

### Routing rules
- `ProjectPath != "" && FilePath == "" && len(FilePaths) == 0` → `FilesForProject(path, "", 0)` → `BlameResult{ProjectFiles: ...}`
- `len(FilePaths) > 0` → `GetSessionsByFile(BlameQuery{FilePaths: ...})` → `BlameResult{Entries: ...}` (no Limit=1 even when All=false)
- `FilePath != ""` → existing single-file path with optional Restore shortcut
- None set → error "file path or project path is required"

### Build/test outcome
- `go build ./internal/service/...` clean
- `go test ./internal/service/...` PASS (all existing tests + 4 new blame tests)

## [2026-06-14] Task 6: MCP layer — files[] array param + agent in result

### API discovery
- `mcp-go` v0.44.1 has native `WithArray(name, opts...)` + `WithStringItems(opts...)` — no comma-string workaround needed
- `BindArguments` does `json.Marshal(map[string]any)` then `json.Unmarshal([]byte, &struct)` so `[]string` in the args map correctly populates `Files []string` in the args struct

### Changes made
- `internal/mcp/tools.go`: added `Files []string \`json:"files"\`` to handleBlame args struct; changed validation to error when BOTH File=="" AND len(Files)==0; new error message "file or files parameter is required"; routing: if len(Files)>0 → BlameRequest{FilePaths: args.Files}; else → BlameRequest{FilePath: args.File}
- `internal/mcp/server.go`: removed `mcp.Required()` from "file" param; added `mcp.WithArray("files", mcp.WithStringItems(), mcp.Description(...))` line; updated tool description to note files takes priority
- `internal/mcp/server_test.go`: added 3 new tests — TestHandleBlame_AgentField (GREEN in RED; agent already populated by T1/T3), TestHandleBlame_FilesArray (RED: "file parameter is required"), TestHandleBlame_NeitherFileNorFiles (RED: error message lacked "file or files")

### TDD outcome
- RED: TestHandleBlame_FilesArray FAIL ("unexpected tool error: file parameter is required"), TestHandleBlame_NeitherFileNorFiles FAIL ("expected error about 'file or files'"), TestHandleBlame_AgentField PASS (already green)
- GREEN: all 6 blame tests pass
- `go build ./internal/mcp/...` clean; `go test ./internal/mcp/...` PASS (full suite)
- Committed: `feat(blame): MCP aisync_blame accepts files[] and returns agent`

## [2026-06-14] Task 5: CLI layer — multi-file args, --project flag, AGENT column

### TDD Protocol
- RED: compilation failure — `Options.FilePaths` and `Options.ProjectPath` undefined in test file
- GREEN: 13 tests pass (7 original updated + 6 new)

### Options struct changes
- Removed: `FilePath string`
- Added: `FilePaths []string`, `ProjectPath string`

### Cobra changes
- `cobra.ExactArgs(1)` → `cobra.ArbitraryArgs`
- `RunE`: `opts.FilePaths = args`
- New flag: `--project` via `StringVar(&opts.ProjectPath, "project", "", ...)`

### runBlame routing
- Manual validation: `len(FilePaths)==0 && ProjectPath==""` → error "requires at least one file argument or --project flag"
- `effectiveProjectPath`: `gitClient.TopLevel()` unless `--project` is set, then `opts.ProjectPath`
- Single file → `req.FilePath = opts.FilePaths[0]` (preserves --restore shortcut in service)
- Multi-file → `req.FilePaths = opts.FilePaths`
- Project-mode → `renderProjectView()`: FILE|SESSION_ID|AGENT|DATE; --json encodes ProjectFiles; --quiet prints LastSessionIDs
- File-mode → `renderFileMode()`: SESSION_ID|PROVIDER|AGENT|BRANCH|CHANGE|DATE|SUMMARY; empty agent → "-"

### Key design decision
- Single file still uses `req.FilePath` (not `req.FilePaths`) so that `--restore` continues to trigger the Restore shortcut in the service layer (service Restore only fires in single-file path)

### Build/test outcome
- `go test ./pkg/cmd/blamecmd/ -v` PASS (13 tests)
- `make build` → `bin/aisync` produced, exit 0
- LSP diagnostics: no errors on blamecmd.go or blamecmd_test.go
- Committed: `feat(blame): CLI multi-file args, --project, AGENT column`

## [2026-06-15] Task 7: Documentation — agent attribution, multi-file, project mode

### Files updated
- `architecture/blame.md`: Full rewrite. Added agent attribution section (COALESCE from sessions JOIN), multi-file section (IN clause, explicit list only), project-view section (CTE last_session explanation), updated domain types (BlameEntry.Agent, BlameQuery.FilePaths, ProjectFileEntry.LastAgent), updated Layer Responsibilities table, updated SQL query section with COALESCE and IN variant, added CLI usage and MCP tool parameter tables.
- `README.md`: Updated commands table blame row to mention AGENT column, multi-file, and --project. Updated MCP tools table blame row to mention files[] parameter. Added "Blame Examples" subsection under Commands with 7 usage examples.

### Guardrails respected
- No --agent filter documented (does not exist)
- No glob/wildcard documented (not supported)
- No MCP project mode documented (CLI only)
- No fictional flags invented — all examples verified against blamecmd.go

### Evidence
- `.omo/evidence/task-7-docs-grep.txt` confirms "project" in architecture/blame.md and "agent" in README.md
