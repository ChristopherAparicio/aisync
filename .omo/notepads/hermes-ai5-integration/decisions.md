# Decisions — hermes-ai5-integration

## [2026-05-30] Init
- H3 = new page /sessions/{id}/exchanges (not a panel), cap 10 children + load-more partial
- Tests = Go table-driven + fixture state.db/jobs.json (no live Hermes required)
- Migrations: 037=H1 (sessions/provider), 038=H2 (cron_jobs table)
- H3 = NO migration (messages in payload BLOB, read-time projection)
- Hermes stall detection = OUT OF SCOPE (OpenCode-specific, defer)
- No trajectory JSONL ingestion, no write-back to state.db
- CSS-only UI, existing CSS vars only, append-only to style.css
- delegate_task added to session_ingest.go list (never rename existing entries)
- stripSentinel helper in internal/provider/hermes/sentinel.go (reused by T8 + T6)
