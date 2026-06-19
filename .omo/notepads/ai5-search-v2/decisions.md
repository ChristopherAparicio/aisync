# decisions.md — ai5-search-v2

## Decisions Recorded

### doc.ToolNames field
- KEEP `doc.ToolNames` (list of tool names, no content) — lightweight signal
- DROP tool inputs/outputs from `Content` (bash outputs, edit/write content)
- Document this choice with a comment in document.go

### Full reindex strategy
- One-shot rebuild of full FTS5 index (not incremental) — acceptable for local one-time run
- Always on a tmp COPY of ~/.aisync/sessions.db, NEVER in-place

### bm25 weights / FTS schema
- FROZEN unless eval explicitly requires change
- If change needed: document in report + flag as guardrail deviation
