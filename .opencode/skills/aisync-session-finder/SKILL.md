---
name: aisync-session-finder
description: Use aisync to find, inspect, restore and resume AI coding sessions. Load this skill when asked: "which session worked on X?", "find the session that touched this file", "show me what happened in session <id>", "restore a past session", "resume work on branch Y", or "which sessions are on this branch/PR/tag".
---

# aisync Session Finder

Load this skill any time you need to find, inspect, or restore an AI coding session.

## Quick Routing Table

| User question | aisync command |
|---|---|
| "find sessions on a branch" | `aisync list --branch <branch> --limit 10` |
| "which sessions touched a file" | `aisync blame <file>` |
| "find sessions for PR #N" | `aisync list --pr <number>` |
| "show session details" | `aisync show <id>` |
| "inspect session structure" | `aisync inspect <id>` |
| "restore session context" | `aisync restore --session <id>` |
| "resume work on a branch" | `aisync resume <branch>` |
| "list recent sessions" | `aisync list --limit 10 --json` |
| "find sessions by provider/type/tag" | `aisync list --provider opencode --type <type> --tag <tag>` |

## WARNING: --search (FTS5) is Unreliable

> **--search is unreliable**: The FTS5 full-text search engine exits 0 but returns false positives. It matches raw truncated message content (50k chars), not summaries. The OR operator is broken and child-to-parent propagation is missing.
>
> **Prefer**: metadata filters (`--branch`, `--provider`, `--tag`, `--since`, `--type`) combined with `blame` and `inspect`/`show` for reliable retrieval.
>
> A separate search-architecture plan is in progress to fix FTS5.

## Recipes

### List Sessions (with Filters)

```bash
# List recent sessions globally
aisync list --limit 10 --json

# Filter by branch
aisync list --branch main --limit 5

# Filter by provider and output JSON
aisync list --provider opencode --json --limit 10

# Sessions in a date range
aisync list --since 2024-01-01 --until 2024-12-31

# Off-topic sessions
aisync list --off-topic --limit 5

# Sessions with a specific tag
aisync list --tag "auth" --limit 5

# Sessions for a PR
aisync list --pr 42

# Quiet mode (IDs only, one per line)
aisync list --quiet | head -20

# Tree view of sessions
aisync list --tree --limit 10

# All sessions across all repos
aisync list --all --limit 20
```

### Blame a File

Find every session that touched a specific file. This is the most reliable way to trace which AI conversation produced a change.

```bash
# Find all sessions that touched a specific file
aisync blame README.md

# Output as JSON
aisync blame README.md --json

# Blame across all branches
aisync blame src/auth.go --all

# Blame filtered to a specific branch
aisync blame src/auth.go --branch feature/oauth

# Blame filtered to a specific provider
aisync blame src/auth.go --provider opencode

# Quiet mode (session IDs only)
aisync blame README.md --quiet
```

### Inspect a Session

`inspect` gives you the full structured breakdown of a session: messages, tool calls, file changes, errors, and metadata. Use it when you need to understand what actually happened inside a session.

```bash
# Full structured inspection (JSON)
aisync inspect <session-id> --json

# Inspect a specific section
aisync inspect <session-id> --section summary
aisync inspect <session-id> --section errors
aisync inspect <session-id> --section files

# Apply a suggested fix from inspection
aisync inspect <session-id> --apply

# Generate a fix suggestion
aisync inspect <session-id> --generate-fix
```

### Show Session Details

`show` gives a human-readable view of a session. Faster than `inspect` for a quick overview.

```bash
# Human-readable view
aisync show <session-id>

# Include token usage and cost
aisync show <session-id> --tokens --cost

# Include file changes
aisync show <session-id> --files

# Include tool usage breakdown
aisync show <session-id> --tool-usage

# Include blame information
aisync show <session-id> --blame
```

### Restore Session

Restores a captured session into your current AI provider so you can pick up where you left off.

```bash
# Restore most recent session on current branch
aisync restore

# Restore a specific session by ID
aisync restore --session <session-id>

# Dry run (preview what would be restored, no changes)
aisync restore --dry-run

# Restore from a specific PR
aisync restore --pr 42

# Restore using a specific provider format
aisync restore --provider opencode

# Restore a specific file's context only
aisync restore --file src/auth.go

# Restore into a specific worktree
aisync restore --worktree /path/to/worktree

# Restore with secret redaction
aisync restore --redact-secrets

# Restore with error cleanup
aisync restore --clean-errors --strip-empty --fix-orphans
```

### Resume Branch + Session

`resume` combines a branch checkout with a session restore in one step. The fastest way to pick up AI context on a different branch.

```bash
# Checkout branch and restore its most recent session
aisync resume <branch>

# Resume with a specific session
aisync resume <branch> --session <session-id>

# Dry run (preview without switching branch)
aisync resume <branch> --dry-run

# Resume using a specific provider
aisync resume <branch> --provider opencode

# Resume with cleanup flags
aisync resume <branch> --clean-errors --strip-empty --fix-orphans

# Resume with secret redaction
aisync resume <branch> --redact-secrets
```

## Common Workflows

### "Which session produced this file change?"

```bash
aisync blame src/payments/stripe.go
# Pick the session ID from the output, then:
aisync show <session-id> --files --tool-usage
```

### "What did the AI do on this branch last week?"

```bash
aisync list --branch feature/payments --since 2024-01-01 --json
# Then inspect the most relevant one:
aisync inspect <session-id> --section summary
```

### "I need to continue work from a PR"

```bash
aisync list --pr 123
aisync restore --pr 123
```

### "Hand off context to a colleague"

```bash
# They run on their machine:
aisync resume <branch>
# aisync fetches the session linked to that branch and restores it.
```

### "Something broke, find the session that changed this file"

```bash
aisync blame src/broken-module.go --json
aisync inspect <session-id> --section errors
aisync inspect <session-id> --generate-fix
```

## MCP Fallback

If aisync MCP is configured, use `aisync_list`, `aisync_blame`, or `aisync_get` tools instead of CLI commands.

## Out of Scope

For session diff, replay, and rewind, see `aisync --help` or `aisync rewind --help`.
