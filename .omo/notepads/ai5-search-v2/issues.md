# issues.md — ai5-search-v2

## Known Risks / Issues

### Risk: OpenCode user-turn consolidation (A1)
- Observed ~1 user msg per 16-31 assistant messages — may mean multi-turn user content is being lost or merged
- T4 spike MUST clarify this before T5 is finalized
- If signal loss confirmed: T5 must compensate (e.g., preserve all user content blocks)

### Risk: 2% orphan compaction markers (A2)
- Markers without a following assistant text part
- T5 must skip gracefully: no panic, no empty marked messages

### Risk: Path/command discoverability regression (A3)
- Dropping tool outputs from FTS content risks losing file-path and bash-command search
- T2 fixture MUST include path + command queries
- T7 eval MUST assert zero regression on these
- Fallback: aisync blame + file_changes table covers file-level discovery

## DB Path
- Real DB: ~/.aisync/sessions.db (NEVER write to this directly)
- Always: cp ~/.aisync/sessions.db /tmp/aisync-eval-$(date +%s).db
