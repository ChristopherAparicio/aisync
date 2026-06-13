---
name: aisync-stats
description: Use aisync to get token usage, costs, tool statistics, and usage forecasts for AI coding sessions. Load this skill when asked: "how many tokens did this session use?", "what did this cost?", "which tools were called most?", "show me usage stats for this project/branch", "forecast my AI usage", or "compare token usage across sessions".
---

# aisync Stats

Load this skill to get token counts, cost breakdowns, tool-call rankings, and usage forecasts from aisync.

## Quick Routing Table

| User question | aisync command |
|---|---|
| Overall token/session stats | `aisync stats` |
| Cost breakdown | `aisync stats --cost` *(offline, no LLM)* |
| Tool usage ranking | `aisync tool-usage --json <session-id>` |
| Stats by branch/provider/period | `aisync stats --branch <b> --provider <p> --period <N>d` |
| Forecast usage for next N days | `aisync stats --forecast --forecast-days <N>` |
| Per-session token details | `aisync show <id> --tokens --cost --tool-usage` |
| LLM efficiency analysis | `aisync efficiency <id>` *(OPTIONAL -- needs LLM)* |

## Warning: efficiency Requires LLM

> **`aisync efficiency`** requires a running LLM (ollama or Claude CLI). If it errors, use `aisync stats --cost` instead -- it reads directly from SQLite, requires no LLM, and shows the full cost breakdown.
>
> To configure a LLM adapter: `aisync config set analysis.adapter <claude|ollama>`

## Recipes

### Overall stats

```bash
# Summary (human-readable)
aisync stats

# Cost breakdown (offline, no LLM needed)
aisync stats --cost
```

### Scoped stats

```bash
# Stats for a specific branch
aisync stats --branch main

# Stats for a specific provider
aisync stats --provider opencode

# Stats for a period (last N days)
aisync stats --period 7d

# Stats for the current user
aisync stats --me
```

### Usage forecast

```bash
# Forecast next 7 days
aisync stats --forecast --forecast-days 7

# Forecast next 30 days
aisync stats --forecast --forecast-days 30
```

### Tool usage

```bash
# Ranked tool call frequency for a session
aisync tool-usage --json <session-id>
```

### Per-session stats

```bash
# Token + cost + tool usage for a session
aisync show <session-id> --tokens --cost --tool-usage
```

### Efficiency analysis (OPTIONAL -- needs LLM)

```bash
# Requires ollama or Claude CLI configured via:
# aisync config set analysis.adapter <claude|ollama>
aisync efficiency <session-id>
aisync efficiency <session-id> --json
```

## MCP Fallback

If aisync MCP is configured, use the `aisync_stats` tool instead of CLI commands.

## Out of Scope

For `usage compute` internals, see `aisync usage compute --help`.
