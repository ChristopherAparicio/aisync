---
name: aisync-analyze
description: Use aisync to analyze session health, diagnose errors, validate sessions, and detect off-topic drift. Load this skill when asked: "why did this session fail?", "diagnose what went wrong in session <id>", "find sessions with errors", "validate this session or database", "did this session go off-topic?", or "check the health of my sessions".
---

# aisync Session Health and Diagnosis

Load this skill to inspect session health, find errors, validate integrity, and detect off-topic drift.

## Quick Routing Table

| User question | aisync command |
|---|---|
| "find sessions with errors" | `aisync errors --recent --limit 10` |
| "validate session integrity" | `aisync validate <id> --json` |
| "quick health check (offline)" | `aisync diagnose <id>` |
| "deep LLM diagnosis" | `aisync diagnose <id> --deep` *(OPTIONAL -- needs LLM)* |
| "did session go off-topic?" | `aisync list --off-topic --limit 5` |
| "LLM-powered analysis" | `aisync analyze <id>` *(OPTIONAL -- needs LLM)* |

## Offline-Safe Core (no LLM required)

These commands work without any LLM or auth configured:

- **`aisync errors`** -- list sessions with errors, filterable by category
- **`aisync validate <id>`** -- check structural integrity of a session
- **`aisync diagnose <id>`** (quick scan, no `--deep`) -- instant health score + error timeline, no LLM needed
- **`aisync list --off-topic`** -- find sessions that drifted from their intent

## OPTIONAL: analyze and diagnose --deep require LLM

> **`aisync analyze <id>`** and **`aisync diagnose <id> --deep`** require a configured LLM adapter.
> Configure: `aisync config set analysis.adapter <claude|ollama>`
> If these error, use `aisync diagnose <id>` (quick scan) or `aisync errors` for immediate offline results.

## Recipes

### Find sessions with errors

```bash
# Most recent sessions with errors
aisync errors --recent

# Most recent errors, machine-readable
aisync errors --recent --json

# Limit to last N recent error sessions
aisync errors --recent --limit 10

# Filter by error category (requires --recent or a session ID)
aisync errors --recent --category tool-failure --limit 5
```

### Validate session integrity

```bash
# Validate a specific session
aisync validate <session-id> --json

# Quiet mode (exit code only)
aisync validate <session-id> --quiet

# Attempt to fix issues
aisync validate <session-id> --fix
```

### Quick health diagnosis (offline)

```bash
# Quick health scan (no LLM needed)
aisync diagnose <session-id>

# Machine-readable output
aisync diagnose <session-id> --json

# Quiet mode
aisync diagnose <session-id> --quiet
```

### Detect off-topic sessions

```bash
# Find off-topic sessions globally
aisync list --off-topic --limit 5

# Off-topic sessions on a specific branch
aisync list --off-topic --branch main --limit 5
```

### LLM-powered analysis (OPTIONAL -- needs LLM)

```bash
# Requires: aisync config set analysis.adapter <claude|ollama>
aisync analyze <session-id>
aisync analyze <session-id> --json
```

### Deep diagnosis (OPTIONAL -- needs LLM)

```bash
# Requires LLM adapter configured
aisync diagnose <session-id> --deep
aisync diagnose <session-id> --deep --json
```

## MCP Fallback

If aisync MCP is configured, use `aisync_explain` or other analysis tools instead of CLI commands.

## See Also

For session content diff, replay, or rewind, see `aisync --help`.
