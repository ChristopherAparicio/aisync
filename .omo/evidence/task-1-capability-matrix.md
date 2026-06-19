# aisync Capability Matrix — 2026-06-13

## A1: Binary
- Path: /Users/guardix/go/bin/aisync
- Version: aisync version 0.1.0-dev

## A2: Cost/Efficiency
- Command: `aisync efficiency <session-id>`
- Status: OPTIONAL(auth) — requires LLM client in PATH
- Evidence (verbatim stderr/stdout):
  ```
  Error: LLM efficiency analysis: ollama HTTP 404: {"error":"model 'qwen3.5:35b' not found"}
  EXIT:1
  ```
- Note: `aisync stats --cost` WORKS without auth (shows $7280.68 total cost from DB). `aisync efficiency` requires a running LLM (ollama or Claude CLI). The model `qwen3.5:35b` was not found in the local ollama instance.
- Alternative cost command: `aisync stats --cost` — Status: WORKS (reads from SQLite, no LLM needed)

## A3: Frontmatter Shape
- opencode-sessions (lines 1-4):
  ```yaml
  ---
  name: opencode-sessions
  description: Quick reference for finding, inspecting, and resuming OpenCode sessions via CLI
  ---
  ```
- replay-tester (lines 1-4):
  ```yaml
  ---
  name: replay-tester
  description: Test evolutions (skills, agents, prompts) by replaying past sessions and comparing results
  ---
  ```
- Conclusion: **single-line** description (plain string, no block scalar, no quotes). Both skills use identical frontmatter structure: `name` + `description` as flat single-line strings.

## A4: Metadata Filters
- `aisync list --provider opencode --limit 5 --json`: **WORKS** — exit 0, returns valid JSON array with full session objects
- `aisync list --branch master --limit 3`: **WORKS** — exit 0, returns 3 sessions on master branch
- `aisync list --search "authentication token"`: **WORKS** (exit 0) but behavior is unreliable — see FTS section below

## FTS --search
- Command run: `aisync list --search "authentication token"`
- Exit code: 0
- Status: **AVOID (unreliable)**
- Evidence: Returns 16 results for "authentication token" on branch master, but the results include sessions whose summaries mention "pricing & billing" and "telemetry & remote architecture" — clearly not matching the search terms. FTS5 is matching on full message content (50k truncated), not summaries, producing false positives. The OR operator and child→parent traversal are also known broken (per decisions.md).
- Verbatim first result summary: `"Investigate pricing & billing (@explore subagent)"` — not related to "authentication token"

---

## Command Matrix

| Command | Status | Key Flags |
|---------|--------|-----------|
| list | WORKS | --all --branch --global --json --limit --me --off-topic --pr --project --provider --quiet --remote --search --similar --since --tag --tree --type --until --user |
| blame | WORKS | --all --branch --json --provider --quiet --restore |
| inspect | WORKS | --apply --generate-fix --json --section |
| show | WORKS | --blame --cost --files --tokens --tool-usage |
| restore | WORKS | --agent --clean-errors --dry-run --exclude --file --fix-orphans --pick --pr --provider --redact-secrets --session --strip-empty --worktree |
| resume | WORKS | --clean-errors --dry-run --exclude --fix-orphans --provider --redact-secrets --session --strip-empty |
| stats | WORKS | --all --branch --cost --forecast --forecast-days --me --period --provider --tools --user |
| usage | WORKS | subcommand: compute |
| tool-usage | WORKS | --json |
| efficiency | OPTIONAL(auth) | --json --model (requires LLM: ollama or Claude CLI) |
| analyze | OPTIONAL(auth) | --json (requires LLM adapter configured via `aisync config set analysis.adapter`) |
| diagnose | WORKS (quick) / OPTIONAL(auth) (--deep) | --deep --json --model --quiet |
| errors | WORKS | --category --json --limit --recent; subcommand: reclassify |
| validate | WORKS | --fix --json --quiet |
| skill-observe | WORKS | --json --quiet |

### Commands NOT in the 15-command list (present in binary but not probed)
The task specified 15 commands. All 15 were probed. Additional commands exist in the binary (init, capture, export, import, link, comment, push, pull, sync, search, mcp, tui, serve, rewind, explain, hooks, secrets, config) but were not in scope.

### Commands needing auth/LLM
- `efficiency` — requires LLM (ollama or Claude CLI); ollama model `qwen3.5:35b` not found → OPTIONAL(auth)
- `analyze` — requires LLM adapter configured → OPTIONAL(auth)
- `diagnose --deep` — requires LLM for deep analysis; `diagnose` (quick scan) works without LLM → OPTIONAL(auth) for --deep only
