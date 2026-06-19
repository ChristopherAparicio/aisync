# Decisions — aisync-agent-skills

<!-- Append entries below. Never overwrite. Format: ## [TIMESTAMP] Task: {id} -->

## [2026-06-13] Plan finalized

- Deployment: symlink via `make install-skills` (not copy)
- Names: aisync-session-finder / aisync-stats / aisync-analyze
- Repo dir: `.opencode/skills/`
- CLI-first; MCP aisync_* fallback only
- `--search` (FTS5) must NOT be primary path (bug: 50k trunc, broken OR, no child→parent)
- cost/efficiency → OPTIONAL (may need auth/pricing)
- analyze/diagnose → OPTIONAL if LLM-backed
- Each SKILL.md ≤ ~350 lines
