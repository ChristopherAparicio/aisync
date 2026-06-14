# Decisions — ai5-blame-file-attribution

## [2026-06-14] Architectural Decisions

- **Agent field**: `BlameEntry.Agent string \`json:"agent"\`` — NO omitempty (COALESCE gives "" which must appear in JSON)
- **Multi-file**: `BlameQuery.FilePaths []string` alongside existing `FilePath string` (rétro-compat)
- **Empty agent in table**: render as "-" (never drop the row)
- **Mode project**: `aisync blame --project <path>` = new flag on existing command, routes to FilesForProject+LastAgent
- **MCP rétro-compat**: `aisync_blame` keeps `file` (string) + adds `files` (array); `files` takes precedence
- **Service routing**: BlameRequest.ProjectPath → FilesForProject; BlameRequest.FilePaths → GetSessionsByFile(IN); BlameRequest.FilePath → existing path
- **No MCP project mode**: --project is CLI only in this plan
- **Dedup policy**: per-file, most recent session wins (ExcludeReads already handled in store)
- **Cobra args**: custom validator (cobra.ArbitraryArgs or MinimumNArgs(0)) + manual check: require ≥1 arg OR --project set
