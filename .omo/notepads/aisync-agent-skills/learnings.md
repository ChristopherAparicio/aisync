# Learnings — aisync-agent-skills

<!-- Append entries below. Never overwrite. Format: ## [TIMESTAMP] Task: {id} -->

## [2026-06-13] Task: 1 (Preflight)
- A1: aisync at /Users/guardix/go/bin/aisync, version 0.1.0-dev
- A2: efficiency = OPTIONAL(auth) — requires LLM (ollama model `qwen3.5:35b` not found, exit 1). Alternative: `aisync stats --cost` = WORKS (reads from SQLite, no LLM, shows $7280.68 total cost)
- A3: frontmatter shape = description: single-line (plain string, no block scalar, no quotes). Both opencode-sessions and replay-tester use identical `name` + `description` flat structure.
- A4: metadata filters = WORKS (--provider, --branch, --json all exit 0 with correct output); --search = AVOID(buggy) — exits 0 but returns false positives (FTS5 matches raw message content, not summaries; "authentication token" returns "Investigate pricing & billing" as top result)
- Commands NOT present (none missing from the 15): all 15 probed successfully
- Commands needing auth/LLM: efficiency (OPTIONAL), analyze (OPTIONAL), diagnose --deep (OPTIONAL for deep mode only)
- diagnose (quick scan, no --deep) = WORKS without LLM — instant health score, error timeline, verdict

## [2026-06-13] Task: 2 (Scaffold)
- Frontmatter shape used: single-line
- 3 dirs created: aisync-session-finder, aisync-stats, aisync-analyze
- Committed: chore(skills): scaffold aisync agent skill bundle

## [2026-06-13] Task: 5 (aisync-analyze)
- Line count: 112
- QA: errors [PASS], validate [PASS], off-topic [PASS], diagnose-quick [PASS], optional caveat [PRESENT]
- Committed: feat(skills): add aisync-analyze skill

## [2026-06-13] Task: 3 (aisync-session-finder)
- Line count: 237
- QA: list --branch PASS, blame PASS, limitation note PRESENT
- Committed: feat(skills): add aisync-session-finder skill

## [2026-06-13] Task: 4 (aisync-stats)
- Line count: 95
- QA: stats --json [PASS (no --json flag; plain `aisync stats` used, exit 0)], tool-usage [PASS], optional caveat [PRESENT]
- Committed: feat(skills): add aisync-stats skill

## [2026-06-13] Task: 7 (docs)
- .opencode/skills/README.md: created, 3 skills listed, install workflow, limitations
- README.md: Agent Skills section added after MCP Server Integration
- QA: all 3 skills named, install-skills documented, FTS limitation noted
- Committed: docs(skills): document agent skill bundle
## [2026-06-13] Task: 6 (install-skills)
- Makefile targets: install-skills, uninstall-skills added
- Symlinks verified: 3/3 pointing to repo .opencode/skills/
- Idempotent: YES; uninstall-safe: YES (guards against non-symlinks)
- Committed: feat(build): add install-skills target for agent skill bundle
