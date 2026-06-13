# aisync Agent Skills

A bundle of 3 CLI-first [OpenCode](https://opencode.ai) skills that teach your agents to use the `aisync` CLI for session retrieval, analytics, and health analysis.

## Skills

### aisync-session-finder

Use aisync to find, inspect, restore and resume AI coding sessions. Load this skill when asked: "which session worked on X?", "find the session that touched this file", "show me what happened in session <id>", "restore a past session", "resume work on branch Y", or "which sessions are on this branch/PR/tag".

**Triggers**: "which session worked on X?", "find the session that touched this file", "restore a past session", "resume work on branch Y"

### aisync-stats

Use aisync to get token usage, costs, tool statistics, and usage forecasts for AI coding sessions. Load this skill when asked: "how many tokens did this session use?", "what did this cost?", "which tools were called most?", "show me usage stats for this project/branch", "forecast my AI usage", or "compare token usage across sessions".

**Triggers**: "how many tokens did this use?", "what did this cost?", "which tools were called most?", "forecast my usage"

### aisync-analyze

Use aisync to analyze session health, diagnose errors, validate sessions, and detect off-topic drift. Load this skill when asked: "why did this session fail?", "diagnose what went wrong in session <id>", "find sessions with errors", "validate this session or database", "did this session go off-topic?", or "check the health of my sessions".

**Triggers**: "why did this session fail?", "find sessions with errors", "did this session go off-topic?"

## Installation

Add the skills to your global OpenCode skills directory as symlinks:

```bash
make install-skills
```

This creates symlinks from `~/.config/opencode/skills/aisync-*` to `<repo>/.opencode/skills/aisync-*`.

Because they are symlinks, any edits to the skill files in this repo propagate immediately -- no reinstall needed.

To remove:

```bash
make uninstall-skills
```

## Mechanism

All 3 skills are **CLI-first**: they drive the `aisync` binary directly and do not require the aisync MCP server to be configured. MCP tools (`aisync_list`, `aisync_stats`, `aisync_explain`, etc.) are mentioned as an alternative when MCP is available.

## Known Limitations

**FTS5 `--search` is unreliable** (as of aisync 0.1.0-dev): the full-text search engine exits 0 but returns false positives. It matches raw truncated message content rather than summaries, and the OR operator is broken. The `aisync-session-finder` skill routes to metadata filters (`--branch`, `--provider`, `--tag`, `--since`) and `aisync blame` instead.
A fix is tracked in `.omo/plans/ai5-search-architecture.md`.

**`efficiency`, `analyze`, and `diagnose --deep`** require a configured LLM adapter (`aisync config set analysis.adapter <claude|ollama>`). The skills mark these as OPTIONAL and document the offline-safe alternatives (`aisync stats --cost`, `aisync diagnose <id>`, `aisync errors`).
