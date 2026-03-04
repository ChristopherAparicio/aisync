# aisync — Product Specification v0.1

## One-liner

CLI that links AI sessions (Cursor, Claude Code, OpenCode) to Git branches, stores them, and allows automatic reloading when an issue occurs on a PR.

---

## The Problem

When a developer uses an AI agent to code on a branch, the conversation context disappears after the session. If a bug is discovered during PR review or in CI, nobody can understand the agent's reasoning, and fixing requires re-explaining everything from scratch.

Today:
- AI sessions are **ephemeral** and **local** to a machine
- No link between a commit and the AI conversation that produced it
- When a PR breaks, context is lost → we waste tokens re-explaining everything
- Handoff between colleagues requires re-describing the entire context to the agent

## The Solution

`aisync` automatically captures AI sessions at commit time, stores them in a shared database, and allows reloading them in the agent when code intervention is needed.

---

## Personas

### P1 — Individual Developer (John Doe)
Works with Claude Code and OpenCode. Wants to find his sessions when returning to a branch after several days.

### P2 — Colleague (Walter)
Does PR review. Sees a problem. Wants to understand why the agent made that choice and potentially reload the session to fix it.

### P3 — Tech Lead
Wants visibility into AI agent usage in the team: which sessions, how many tokens, which files touched.

### P4 — AI Agent Operator
Configures and maintains AI agent skills/tools (OpenCode skills, MCP servers, Claude Code settings). Uses aisync to observe how agents behave in real sessions, identify misconfigurations, and improve tool definitions. The investigation agent is their primary tool.

---

## User Stories

### Capture

**US-1: Automatic capture on commit**
> As a developer, when I do a `git commit`, the active AI session is automatically captured and linked to this commit, without manual action on my part.

**US-2: Manual capture**
> As a developer, I can capture a session anytime with `aisync capture`, even without committing.

**US-3: Provider detection**
> As a developer, `aisync` automatically detects which AI agent(s) are active (Cursor, Claude Code, OpenCode) and captures the right session.

**US-4: Session selection**
> If multiple sessions exist for the same project, `aisync` selects the one most recently modified, or asks me to choose.

### Storage

**US-5: Local SQLite storage**
> Sessions are stored in a local SQLite database for fast queries (which session for this branch? which files touched?).

**US-6: Configurable storage mode (full vs summary)**
> As a developer, I can choose to store either the complete session (all messages, tool calls, thinking) or a compact summary (key decisions, files touched, LLM-generated summary). This allows controlling the size of stored data.

Available modes:
- `full` — Complete session with all messages, tool calls, and optionally thinking/reasoning. Typical size: 1-50 MB.
- `summary` — Structured summary: auto-generated summary, list of decisions, modified files, token count. Keeps first and last user message for context. Typical size: 5-50 KB.
- `compact` — User/assistant messages only (no tool calls or thinking). Good size/context tradeoff. Typical size: 100 KB - 2 MB.

Mode is configured globally or per capture:
```bash
aisync config set storage-mode summary    # global default
aisync capture --mode full                # one-time override
```

**US-7: Commit ↔ session link**
> Each commit with a linked session contains a trailer `AI-Session: <session-id>` in the commit message, creating a bidirectional link.

**US-8: Git synchronization**
> Sessions are pushed to a dedicated Git branch (`aisync/sessions`) so the team can access them. (Phase 2 — for MVP, local SQLite only.)

### Security — Secret Detection

**US-9: Automatic secret detection**
> As a developer, when a session is captured, `aisync` scans the content to detect API keys, tokens, passwords and other secrets before storage.

**US-10: Secret handling options (mask vs show)**
> As a developer, I can configure what `aisync` does when it detects a secret:
> - `mask` (default) — replaces with `***REDACTED:API_KEY***` before storage
> - `warn` — stores as-is but displays a warning
> - `block` — refuses to capture the session until the secret is removed

Built-in detection patterns:
- API keys: `sk-`, `pk_`, `AKIA`, `ghp_`, `gho_`, `glpat-`, `xoxb-`, `xoxp-`
- JWT tokens: `eyJ...`
- Private keys: `-----BEGIN (RSA|EC|OPENSSH) PRIVATE KEY-----`
- Sensitive env variables: content following `PASSWORD=`, `SECRET=`, `TOKEN=`, `API_KEY=`
- AWS: `AKIA[0-9A-Z]{16}`
- Generic high-entropy strings (configurable)

Configuration:
```json
{
  "secrets": {
    "mode": "mask",
    "custom_patterns": [
      {"name": "internal_token", "regex": "omg_[a-zA-Z0-9]{32}"},
      {"name": "db_password", "regex": "DB_PASSWORD=[^\\s]+"}
    ],
    "ignore_patterns": ["sk-test-"],
    "scan_tool_outputs": true
  }
}
```

```bash
aisync config set secrets.mode mask       # mask | warn | block
aisync secrets scan                        # Scan existing sessions
aisync secrets add-pattern "name" "regex"  # Add custom pattern
```

### Restoration

**US-11: Restore by branch**
> As a developer, when I checkout a branch, I can run `aisync restore` to reload the AI session that was used on that branch.

**US-12: Restore by PR**
> As a reviewer, when a PR has an issue, I can run `aisync restore --pr 42` to reload the AI session in my agent and fix with full context.

**US-13: Cross-provider restoration**
> If the session was captured from Claude Code but I'm working with OpenCode, `aisync` converts the format and loads the context as an initial prompt.

**US-14: Automatic restoration on CI failure**
> (Future) Via a webhook, when CI fails on a PR, `aisync` can automatically create a fix session with the original context + CI errors.

### Consultation

**US-15: View session for a commit**
> As a developer, I can run `aisync show <commit-sha>` to see the AI conversation that produced this commit.

**US-16: List sessions for a branch**
> `aisync list` shows all sessions linked to the current branch with a summary.

### Replay (Model Comparison)

**US-17: Replay a session with a different model**
> As a developer, I can take a captured session and replay the user-side messages through a different model/provider to compare outputs.
>
> ```bash
> aisync replay <session-id> --model claude-sonnet-4-20250514
> aisync replay <session-id> --provider opencode --agent coder
> ```
>
> The replay:
> 1. Extracts all user messages from the original session
> 2. Feeds them sequentially to the target model
> 3. Captures the new session as a "replay" linked to the original
> 4. Produces a comparison report: tokens, cost, files produced, diff of outputs

**US-18: Compare replayed sessions**
> As a developer, after replaying a session with one or more models, I can view a comparison:
>
> ```bash
> aisync compare <session-original> <session-replay-1> [<session-replay-2>...]
> ```
>
> Shows side-by-side: token usage, cost, number of tool calls, files changed, success/failure of the task. Helps decide which model is most cost-effective for a given type of work.

### Tool & MCP Token Accounting

**US-19: Per-tool token breakdown**
> As a developer, I can see how many tokens each tool/MCP call consumed within a session.
>
> ```bash
> aisync show <session-id> --tool-usage
> ```
>
> Output:
> ```
> Tool usage breakdown:
>   Read         45 calls    12,000 tokens  (21%)
>   Edit         12 calls     8,000 tokens  (14%)
>   Bash          3 calls     2,500 tokens   (4%)
>   Thinking     —           35,000 tokens  (61%)
>   Total        60 calls    57,500 tokens
> ```
>
> This requires `full` storage mode (tool calls are stripped in `compact`). If captured in `compact` mode, show a warning that tool-level accounting is unavailable.

**US-20: MCP skill cost tracking across sessions**
> As a developer or team lead, I can see aggregated tool/MCP usage across sessions to identify expensive patterns (e.g., "Read is called 10x more than needed" or "Bash tool calls cost the most tokens").
>
> ```bash
> aisync stats --tools
> aisync stats --tools --branch feature/auth
> ```

### AI-Blame (File → Session Lookup)

**US-21: Find which AI session modified a file**
> As a developer, when I see a bug or want to understand a file, I can find the AI session that last modified it.
>
> ```bash
> aisync blame src/auth/oauth.py
> ```
>
> Output:
> ```
> src/auth/oauth.py
>   Session:  abc123 (claude-code)
>   Branch:   feature/auth-oauth2
>   Author:   Christopher
>   Date:     2026-02-16
>   Summary:  "Implemented OAuth2 with PKCE flow"
>   Action:   created
>
>   → aisync restore --session abc123
>   → aisync show abc123
> ```

**US-22: Restore session from file blame**
> As a developer, after finding the session via blame, I can directly restore it to debug or continue the work.
>
> ```bash
> aisync blame src/auth/oauth.py --restore
> ```
>
> This is a shortcut for `aisync blame` → get session ID → `aisync restore --session <id>`.

### AI-Powered Session Summarization

**US-30: Auto-summarize sessions with AI**
> As a developer, when a session is captured, aisync can automatically generate an intelligent summary using an AI model. This summary captures intent, outcome, key decisions, friction points, and open items — far richer than the provider's native summary.
>
> ```bash
> aisync capture --summarize                  # One-time AI summarization
> aisync config set summarize.enabled true    # Enable auto-summarization on every capture
> ```
>
> Auto-summarization is triggered at capture time (or post-capture as a background step). It reads the session messages and produces a structured summary:
> ```
> Intent:     Implement OAuth2 with PKCE for the auth module
> Outcome:    Completed — 3 files created, 1 modified, all tests passing
> Decisions:  PKCE over implicit flow, httpOnly cookies for tokens
> Friction:   Had to retry CORS config twice, model confused redirect URIs
> Open items: Rate limiting not yet implemented on token endpoint
> ```
>
> Requirements:
> - An AI model must be available (Claude CLI in PATH, or configurable model endpoint)
> - Summarization is non-blocking: failures are logged but do not prevent capture
> - Works with any storage mode (`full`, `compact`, `summary`)
> - In `summary` mode, the AI summary replaces the heuristic summary

**US-31: Detect similar sessions on a branch**
> As a developer, when multiple sessions exist on the same branch, aisync uses AI summaries to detect whether sessions are doing similar or overlapping work (e.g., multiple retries of the same task).
>
> ```bash
> aisync list --branch feature/auth --similar
> ```
>
> Output:
> ```
> Similar session groups on feature/auth:
>   Group 1 (same intent: "Implement OAuth2"):
>     abc123  57K tokens  2 hours ago   origin
>     def456  32K tokens  1 hour ago    ~85% similar — retried CORS config
>   Standalone:
>     ghi789  12K tokens  30 min ago    different topic (logging refactor)
> ```
>
> Detection uses AI summary comparison + file change overlap. This helps identify wasted retries and understand the real cost of a feature.

### Session Explanation

**US-32: Explain a session or commit**
> As a developer, I can ask aisync to explain what happened in a session or what a specific commit did, in natural language.
>
> ```bash
> aisync explain <session-id>
> aisync explain <commit-sha>
> aisync explain                              # Explain current branch's latest session
> ```
>
> Output:
> ```
> Session abc123 — Explain
>
> This session implemented OAuth2 authentication with PKCE flow for the Django app.
> The developer asked the agent to create a secure auth module. The agent:
> 1. Read the existing handler.py to understand the current auth setup
> 2. Created a new oauth.py with PKCE code verifier/challenge generation
> 3. Modified handler.py to use the new OAuth flow
> 4. Added comprehensive tests in test_oauth.py
>
> The agent initially tried an implicit flow but the developer corrected it to use PKCE.
> Two tool calls failed (file read on wrong path) before succeeding.
>
> Files: 3 created, 1 modified | Tokens: 57,000 | Cost: ~$1.57
> ```
>
> Requires an AI model to generate the explanation. Falls back to showing the stored summary if no model is available.

### Session Rewind (Fork from Message)

**US-33: Rewind to a specific point in a session**
> As a developer, when an AI agent goes off track mid-session, I can rewind to a specific message in the session and fork from there — creating a new session that starts with the context up to that point.
>
> ```bash
> aisync rewind <session-id>                  # Interactive: select a message to rewind to
> aisync rewind <session-id> --message <n>    # Rewind to message N
> ```
>
> The rewind:
> 1. Shows the session messages as a numbered list (interactive selection or `--message N`)
> 2. Creates a new session containing only messages up to the selected point
> 3. Restores this truncated session into the target provider
> 4. The developer can continue from that exact context with a fresh agent response
>
> The original session is preserved. The new (rewound) session is linked as a "fork-of" the original with a `forked_at_message` reference.
>
> ```bash
> aisync list --tree
> ```
> ```
> abc123 (origin, 57K tokens)
> ├── def456 (fork at msg 5, 32K tokens) — rewound and retried
> └── jkl012 (fork at msg 3, 28K tokens) — rewound earlier
> ```
>
> This is the actionable counterpart to fork detection (US-24): while US-24 detects forks retroactively, `rewind` lets the developer intentionally create a fork from a known-good point.

### Resume Workflow

**US-34: Resume work on a branch in one command**
> As a developer, I can switch to a branch and restore its AI session in a single command, without needing to know session IDs.
>
> ```bash
> aisync resume <branch>                       # Checkout + restore latest session
> aisync resume <branch> --session <id>        # Checkout + restore specific session
> aisync resume <branch> --provider opencode   # Checkout + restore into specific provider
> ```
>
> `resume` is a convenience command that combines:
> 1. `git checkout <branch>`
> 2. `aisync restore` (latest session for that branch, or specified session)
> 3. Print continuation instructions (which command to run to continue with the agent)
>
> If the branch has multiple sessions (Phase 5), `resume` restores the most recent one by default, or prompts interactively if `--session` is not specified.

### Server & API

**US-35: API server for multi-client access**
> As a developer, I can start an aisync server that exposes all capabilities over HTTP/REST, enabling Web UI, TUI, and external tools to interact with aisync without going through the CLI.
>
> ```bash
> aisync serve                         # Start API server on default port (8080)
> aisync serve --port 9090             # Custom port
> aisync serve --host 0.0.0.0         # Listen on all interfaces (team use)
> ```
>
> The API exposes the same operations as the CLI — capture, restore, list, show, export, import, link, stats, push, pull — through RESTful endpoints. The CLI continues to work standalone (no server required).

**US-36: MCP Server integration**
> As a developer using Claude Code or OpenCode, I can configure aisync as an MCP server so the AI agent can directly capture, restore, list, or analyze sessions during a conversation — without leaving the chat.
>
> ```bash
> aisync mcp serve                    # Start MCP server mode
> ```
>
> MCP tools exposed:
> - `aisync_capture` — capture the current session
> - `aisync_restore` — restore a session into the current agent
> - `aisync_list` — list sessions for the current branch
> - `aisync_show` — show session details
> - `aisync_analyze` — trigger AI investigation of a session
> - `aisync_explain` — explain what happened in a session
>
> This makes aisync a first-class tool within AI agent workflows.

### Investigation Agent (AI-Powered Analysis)

**US-37: Investigate a session with AI**
> As a developer, when I observe an AI agent making poor decisions (e.g., misusing a skill, going in circles, ignoring context), I can ask aisync to investigate the session and explain what went wrong.
>
> ```bash
> aisync analyze <session-id>
> aisync analyze <session-id> --question "Why did the agent keep re-reading the same file?"
> ```
>
> The investigation:
> 1. Loads the full session from storage (messages, tool calls, file changes)
> 2. Reads relevant files from the codebase (skill definitions, tool configs)
> 3. Sends the context to an LLM for analysis
> 4. Returns: root cause, evidence from the conversation, concrete recommendations
>
> ```
> Investigation: Session abc123
>
> Root cause: The agent's Read skill definition does not specify a file size limit.
> When the agent tried to read a 2MB log file, it consumed 15,000 tokens on a single
> tool call, then re-read the same file 3 more times because it couldn't fit the
> content in its context window.
>
> Evidence:
>   - Message 12: Read(server.log) → 15,234 tokens
>   - Message 15: Read(server.log) → 15,234 tokens (duplicate)
>   - Message 18: Read(server.log) → 15,234 tokens (duplicate)
>
> Recommendations:
>   1. Add max_size parameter to Read skill (limit to 500 lines or 50KB)
>   2. Add a grep/search skill for large files
>   3. Update .opencode/skills/read.md to mention the size limit
> ```

**US-38: Propose changes from investigation**
> As a developer, after an investigation identifies issues, aisync can automatically propose concrete changes to the repository and optionally create a PR.
>
> ```bash
> aisync analyze <session-id> --propose-changes
> aisync analyze <session-id> --propose-changes --create-pr
> ```
>
> This creates a diff with the recommended changes (e.g., updated skill definition, new tool config) and optionally pushes it as a pull request for review.

### Multi-Session Branch Intelligence

**US-23: Keep multiple sessions per branch**
> As a developer, when I start a new AI conversation on an existing branch, aisync should NOT overwrite the previous session. Instead, it should store both and version them.
>
> ```bash
> aisync list --branch feature/auth
> ```
>
> Output:
> ```
> ID        PROVIDER     TYPE    MESSAGES  TOKENS    CAPTURED
> abc123    claude-code  origin  23        57,000    2 hours ago
> def456    claude-code  fork    15        32,000    1 hour ago   ← fork of abc123
> ghi789    opencode     new     8         12,000    30 min ago   ← unrelated
> ```

**US-24: Fork detection (same-start conversations)**
> As a developer, when I start a new conversation on the same branch that begins with the same initial messages as a previous session, aisync detects this is a "fork" (retry with different approach) and links them.
>
> Detection: hash the first N user messages. If two sessions share the same prefix hash → fork relationship.
>
> ```bash
> aisync list --branch feature/auth --tree
> ```
>
> Output:
> ```
> abc123 (origin, 57K tokens)
> ├── def456 (fork, 32K tokens) — retried from message 5
> └── jkl012 (fork, 28K tokens) — retried from message 3
> ghi789 (standalone, 12K tokens) — unrelated topic
> ```

**US-25: Off-topic session detection**
> When a new conversation on a branch is unrelated to the branch's purpose (e.g., asking about a different bug on a `feature/auth` branch), aisync can flag it as "off-topic" or "standalone" rather than linking it to the feature work.
>
> Heuristic: compare the session's file changes and topic keywords with the branch's previous sessions. Low overlap → flag as off-topic.
>
> Off-topic sessions can be:
> - Ignored in branch cost aggregation
> - Garbage collected after N days
> - Moved to a "misc" category

### Cost Tracking & Forecasting

**US-26: Real cost per session**
> As a developer, I can see the actual monetary cost of each AI session based on the model's pricing.
>
> ```bash
> aisync show <session-id> --cost
> ```
>
> Output:
> ```
> Cost breakdown:
>   Model:   claude-opus-4-5 ($15/M input, $75/M output)
>   Input:   45,000 tokens → $0.675
>   Output:  12,000 tokens → $0.900
>   Total:   $1.575
> ```
>
> Pricing table is configurable and ships with defaults for major models. OpenCode already provides cost info — use it when available.

**US-27: Feature cost aggregation**
> As a developer or team lead, I can see the total cost of a feature (branch) across all sessions, including forks and retries.
>
> ```bash
> aisync stats --cost --branch feature/auth
> ```
>
> Output:
> ```
> Branch: feature/auth
>   Sessions: 4 (2 forks, 1 off-topic excluded)
>   Total tokens: 129,000
>   Total cost: $4.85
>     abc123 (origin):     $1.57
>     def456 (fork):       $1.20   ← retry, same work
>     jkl012 (fork):       $2.08   ← retry, same work
>     ghi789 (off-topic):  $0.45   (excluded from total)
> ```

**US-28: Cost forecasting**
> Over time, aisync builds a model of cost patterns: "a feature branch typically requires 3.2 sessions and costs $4-8". This helps teams estimate AI costs for project planning.
>
> ```bash
> aisync stats --forecast
> ```
>
> Output:
> ```
> Cost forecast (based on 23 completed branches):
>   Avg cost per feature branch:  $5.40  (±$2.10)
>   Avg sessions per branch:      3.2
>   Avg tokens per session:       42,000
>   Most expensive model:         claude-opus-4-5 ($3.80/branch avg)
>   Most cost-effective:          claude-sonnet-4 ($1.60/branch avg)
> ```

### Web UI

**US-29: Web dashboard for session browsing**
> As a developer or team lead, I can launch a local web UI to browse sessions visually.
>
> ```bash
> aisync web
> ```
>
> The web UI shows:
> - Branch tree with all associated sessions (origin, forks, off-topic)
> - Session details with messages, tool calls, file changes
> - Cost breakdown per branch, per session, per model
> - Fork visualization (which sessions are retries of which)
> - Click-to-restore: select a session and generate the restore command
>
> Initially a local server (localhost). Team-shared version is a future consideration.

---

## Technical Architecture

### Overview

```
┌──────────────────────────────────────────────────────────────────┐
│                          aisync CLI                              │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    Provider Layer                        │    │
│  │  ┌──────────┐  ┌──────────────┐  ┌──────────────────┐  │    │
│  │  │  Cursor   │  │  Claude Code  │  │    OpenCode      │  │    │
│  │  │           │  │              │  │                  │  │    │
│  │  │ SQLite    │  │  JSONL files │  │  CLI wrapper     │  │    │
│  │  │ state.vscdb│  │  ~/.claude/  │  │  opencode session│  │    │
│  │  │           │  │  projects/   │  │  export/import   │  │    │
│  │  │ Export: ✅ │  │  Export: ✅  │  │  Export: ✅      │  │    │
│  │  │ Import: ❌ │  │  Import: ✅  │  │  Import: ✅      │  │    │
│  │  └──────────┘  └──────────────┘  └──────────────────┘  │    │
│  └─────────────────────────────────────────────────────────┘    │
│                              │                                   │
│                    Unified Session Model                         │
│                              │                                   │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    Storage Layer                         │    │
│  │  ┌──────────────────┐  ┌────────────────────────────┐  │    │
│  │  │  SQLite Local     │  │  Git Branch Sync           │  │    │
│  │  │  ~/.aisync/db     │  │  aisync/sessions           │  │    │
│  │  │                  │  │                            │  │    │
│  │  │  - Fast index    │  │  - Team sharing            │  │    │
│  │  │  - Queries       │  │  - JSON payloads           │  │    │
│  │  │  - Offline       │  │  - Linked to repo          │  │    │
│  │  └──────────────────┘  └────────────────────────────┘  │    │
│  └─────────────────────────────────────────────────────────┘    │
│                              │                                   │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    Git Hooks Layer                       │    │
│  │  pre-commit  →  capture session active                  │    │
│  │  commit-msg  →  ajouter trailer AI-Session              │    │
│  │  post-checkout → notifier session disponible            │    │
│  └─────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────┘
```

### Active session detection

Central problem: **How do we know which session corresponds to current work?**

Detection strategy per provider:

**Claude Code:**
- Read `~/.claude/projects/<encoded-path>/`
- Find the most recently modified `.jsonl` file
- Verify that `gitBranch` in messages matches current branch
- If multiple sessions match → take the one with most recent timestamp

**OpenCode:**
- Call `opencode session list` (or read internal SQLite DB)
- Filter by current project
- Select the most recent session

**Cursor:**
- Locate `state.vscdb` for current workspace via `workspace.json`
- Read `cursorDiskKV` table → keys `composerData:<id>`
- Parse JSON to find conversations with recent `status: "completed"`
- Correlate with files modified in commit

**Fallback heuristic:**
If automatic detection fails, `aisync` looks at which files are in the commit (staging area) and finds an AI session that touched the same files.

### Hybrid storage: SQLite + Git

**Why both?**

| Need | SQLite | Git Branch |
|--------|--------|------------|
| "Which session for this commit?" | ✅ Fast | ❌ Slow (grep) |
| "Share with team" | ❌ Local | ✅ git push/pull |
| "Works offline" | ✅ | ✅ (local) |
| "Backup" | ❌ | ✅ (remote) |
| "Complex queries" | ✅ | ❌ |

**Local SQLite** (`~/.aisync/sessions.db`):
```sql
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,          -- 'claude-code', 'cursor', 'opencode'
    branch TEXT,
    commit_sha TEXT,
    project_path TEXT NOT NULL,
    summary TEXT,
    message_count INTEGER,
    total_tokens INTEGER,
    created_at DATETIME,
    exported_at DATETIME,
    exported_by TEXT
);

CREATE TABLE session_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT REFERENCES sessions(id),
    link_type TEXT NOT NULL,          -- 'branch', 'commit', 'pr'
    link_ref TEXT NOT NULL
);

CREATE TABLE file_changes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT REFERENCES sessions(id),
    file_path TEXT NOT NULL,
    change_type TEXT NOT NULL         -- 'created', 'modified', 'deleted', 'read'
);

CREATE INDEX idx_sessions_branch ON sessions(branch);
CREATE INDEX idx_sessions_commit ON sessions(commit_sha);
CREATE INDEX idx_links_ref ON session_links(link_ref);
CREATE INDEX idx_files_path ON file_changes(file_path);
```

**Git branch** (`aisync/sessions`):
```
aisync/sessions/
├── <session-id-1>.json          # Complete session (unified format)
├── <session-id-2>.json
├── <session-id-1>.raw.jsonl     # Raw Claude Code data (for exact import)
├── <session-id-2>.raw.json      # Raw OpenCode data
└── index.json                   # Lightweight index for fast lookup
```

### Unified session format

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "version": 1,
  "provider": "claude-code",
  "branch": "feature/auth-oauth2",
  "commit_sha": "abc1234",
  "project_path": "/home/chris/my-app",
  "exported_by": "Christopher",
  "exported_at": "2026-02-16T14:30:00Z",
  "created_at": "2026-02-16T13:00:00Z",
  "summary": "Implement OAuth2 flow with PKCE for the auth module",
  "messages": [
    {
      "id": "msg-001",
      "role": "user",
      "content": "Implement OAuth2 with PKCE...",
      "timestamp": "2026-02-16T13:00:00Z"
    },
    {
      "id": "msg-002",
      "role": "assistant",
      "model": "claude-opus-4-5",
      "content": "I'll implement the OAuth2 PKCE flow...",
      "thinking": "The user needs a secure auth flow...",
      "tool_calls": [
        {"name": "Read", "input": "{\"path\": \"src/auth/handler.py\"}"},
        {"name": "Edit", "input": "{\"path\": \"src/auth/oauth.py\", ...}"}
      ],
      "tokens": 1500,
      "timestamp": "2026-02-16T13:00:15Z"
    }
  ],
  "file_changes": [
    {"file_path": "src/auth/oauth.py", "change_type": "created"},
    {"file_path": "src/auth/handler.py", "change_type": "modified"}
  ],
  "token_usage": {
    "input_tokens": 45000,
    "output_tokens": 12000,
    "total_tokens": 57000
  },
  "links": [
    {"type": "branch", "ref": "feature/auth-oauth2"},
    {"type": "commit", "ref": "abc1234"},
    {"type": "pr", "ref": "42"}
  ]
}
```

---

## CLI Commands

### Initialization

```bash
aisync init                          # Initialize aisync in current repo
                                     # → Create .aisync/ config
                                     # → Configure aisync/sessions branch
                                     # → Offer to install hooks

aisync hooks install                 # Install git hooks
aisync hooks uninstall               # Uninstall hooks
```

### Capture

```bash
aisync capture                       # Capture active session (auto-detect provider)
aisync capture --provider claude     # Force specific provider
aisync capture --session <id>        # Capture specific session
aisync capture --all                 # Capture all active sessions
aisync capture --message "Impl OAuth"  # Add manual summary
```

### Query

```bash
aisync list                          # Sessions linked to current branch
aisync list --all                    # All sessions in project
aisync list --branch feature/auth    # Sessions for specific branch
aisync list --pr 42                  # Sessions linked to PR

aisync show <session-id>             # Session details
aisync show <commit-sha>             # Session linked to commit
aisync show --files                  # Show impacted files
aisync show --tokens                 # Show token consumption

aisync status                        # Current state: branch, detected sessions, active hooks
```

### Restore

```bash
aisync restore                       # Restore latest session for current branch
aisync restore --session <id>        # Restore specific session
aisync restore --pr 42               # Restore session linked to PR
aisync restore --provider opencode   # Force restore to specific provider
aisync restore --as-context          # Instead of importing, generate CONTEXT.md file
                                     # that agent can read (useful for Cursor)
```

### Resume

```bash
aisync resume <branch>               # Checkout branch + restore latest session
aisync resume <branch> --session <id> # Checkout + restore specific session
aisync resume <branch> --provider opencode  # Checkout + restore into specific provider
```

### Rewind

```bash
aisync rewind <session-id>           # Interactive: pick message to rewind to
aisync rewind <session-id> --message 5  # Rewind to message 5
```

### Explain

```bash
aisync explain                       # Explain current branch's latest session
aisync explain <session-id>          # Explain a specific session
aisync explain <commit-sha>          # Explain what a commit's AI session did
```

### Sync

```bash
aisync push                          # Push sessions to Git branch
aisync pull                          # Fetch sessions from Git branch
aisync sync                          # Push + Pull
```

### Configuration

```bash
aisync config                        # Display config
aisync config set auto-capture true  # Enable auto capture on commit
aisync config set providers "claude,opencode"  # Providers to detect
aisync config set sync-mode "auto"   # auto | manual | hook
```

---

## Detailed Flows

### Flow 1: Automatic capture on commit

```
Developer runs: git commit -m "feat: add OAuth2"
       │
       ▼
[pre-commit hook] aisync capture --auto
       │
       ├── Detect active providers
       │     ├── Claude Code: ~/.claude/projects/... → session JSONL
       │     ├── OpenCode: opencode session list → active session
       │     └── Cursor: state.vscdb → recent composerData
       │
       ├── Correlate: staged files ↔ files touched by session
       │
       ├── Export session → unified format
       │
       ├── Store in local SQLite + write JSON to aisync/sessions branch
       │
       └── Return session-id
       
[commit-msg hook]
       │
       └── Add to commit message:
           "AI-Session: a1b2c3d4"
```

### Flow 2: Breaking PR → restoration

```
CI fails on PR #42 (branch: feature/auth)
       │
       ▼
Reviewer or dev runs: aisync restore --pr 42
       │
       ├── Lookup in SQLite: session_links WHERE link_type='pr' AND link_ref='42'
       │     (or: session_links WHERE link_type='branch' AND link_ref='feature/auth')
       │
       ├── If not local → aisync pull (fetch from Git branch)
       │
       ├── Read raw_provider_data of session
       │
       ├── Detect dev's provider (or use session's provider)
       │
       ├── Provider.CanImport() ?
       │     ├── YES (Claude Code): copy JSONL → claude --resume <session-id>
       │     ├── YES (OpenCode): opencode session import <file.json>
       │     └── NO (Cursor): generate CONTEXT.md with summary + decisions
       │
       └── Display: "Session restored. Launch your agent to continue."
```

### Flow 3: Handoff between colleagues

```
Christopher works on feature/auth with Claude Code
       │
       ├── Commit + push → session captured and synced
       │
       ▼
Ghalia runs: git checkout feature/auth && aisync restore
       │
       ├── aisync pull → fetch Christopher's session
       │
       ├── Ghalia uses OpenCode → cross-provider restore
       │     └── Conversion: Claude JSONL → OpenCode JSON
       │         (or fallback: generate CONTEXT.md)
       │
       └── Ghalia has full context to continue work
```

---

## Cross-provider handling (Cursor)

Cursor does not support session import. Fallback strategy:

**Generating a CONTEXT.md file:**
```markdown
# AI Session Context — Restored by aisync

## Session info
- Provider: claude-code
- Branch: feature/auth-oauth2  
- Date: 2026-02-16
- Author: Christopher

## Summary
Implementation of OAuth2 with PKCE for the authentication module.

## Key decisions made
1. Using PKCE instead of implicit flow for security
2. Storing tokens in httpOnly cookies, not localStorage
3. Refresh token rotation enabled

## Files modified
- src/auth/oauth.py (created)
- src/auth/handler.py (modified)
- tests/test_oauth.py (created)

## Last conversation context
User asked: "Implement OAuth2 with PKCE for our Django app"
Agent approach: Created a new oauth.py module with PKCE flow,
modified the existing handler to use the new flow, added tests.

## Open questions / TODO
- Rate limiting on token endpoint not yet implemented
- Need to add CORS configuration for the redirect URI
```

This file is placed at project root or in `.aisync/CONTEXT.md` and can be referenced by Cursor agent via `@CONTEXT.md`.

---

## Configuration (`.aisync/config.json`)

```json
{
  "version": 1,
  "providers": ["claude-code", "opencode", "cursor"],
  "auto_capture": true,
  "capture_mode": "on-commit",
  "storage_mode": "compact",
  "storage": {
    "backend": "sqlite",
    "path": "~/.aisync/sessions.db"
  },
  "sync": {
    "enabled": false,
    "branch": "aisync/sessions",
    "auto_push": false
  },
  "secrets": {
    "mode": "mask",
    "custom_patterns": [],
    "ignore_patterns": [],
    "scan_tool_outputs": true
  },
  "fallback_restore": "context-md",
  "exclude_thinking": false,
  "max_session_size_mb": 10,
  "commit_trailer": "AI-Session"
}
```

### Storage modes

| Mode | Content | Typical size | Use case |
|------|---------|-------------|----------|
| `full` | Everything (messages, tools, thinking, raw) | 1-50 MB | Debug, full audit, short sessions |
| `compact` | User/assistant messages only | 100 KB - 2 MB | Daily usage, good tradeoff |
| `summary` | Auto summary + decisions + files | 5-50 KB | Teams with many sessions, limited storage |

### Secret detection modes

| Mode | Behavior | Usage |
|------|----------|-------|
| `mask` | Replace secrets with `***REDACTED:TYPE***` | Default — friction-free security |
| `warn` | Store as-is but display warning | Solo dev, confident in local storage |
| `block` | Refuse capture | Strict compliance |

---

## Development Phases

### Phase 1 — MVP (2-3 weeks)
- [ ] Claude Code provider: export JSONL → unified format
- [ ] OpenCode provider: CLI wrapper export/import
- [ ] Local SQLite storage only
- [ ] 3 storage modes: full / compact / summary
- [ ] Secret detection and masking (built-in patterns)
- [ ] Commands: `capture`, `list`, `show`, `restore`
- [ ] Git hooks: `pre-commit` + `commit-msg` (trailer)
- [ ] Restore: Claude Code `--resume` + OpenCode `import`

### Phase 2 — Team Sharing (1-2 weeks)
- [ ] Sync to Git branch `aisync/sessions`
- [ ] `push` / `pull` / `sync`
- [ ] Cursor provider: export from SQLite `state.vscdb`
- [ ] CONTEXT.md fallback for Cursor restore
- [ ] Cross-provider conversion
- [ ] Configurable secret patterns (custom regex)

### Phase 3 — PR Integration (2 weeks)
- [ ] Automatic PR ↔ session linking (via GitHub API or trailer)
- [ ] `aisync restore --pr <number>`
- [ ] CLI dashboard: tokens consumed, sessions per branch
- [ ] Correlation between commit files and session files

### Phase 3.5 — Architecture Evolution (COMPLETE)
- [x] Extract Service Layer (`internal/service/SessionService`, `SyncService`)
- [x] HTTP/REST API server (`aisync serve`) — 15 endpoints
- [x] Client SDK (`client/`) — 15 methods, full Go HTTP client
- [x] MCP Server integration (`aisync mcp serve`) — 14 tools
- [x] User Identity Layer — `users` table, `owner_id` on sessions, auto-detect from git
- [ ] Analysis Service with LLM-powered investigation agent (moved to Phase 5)

### Phase 4 — CI Automation (future)
- [ ] GitHub Action / Webhook: on CI failure → prepare fix session
- [ ] Notify dev: "Session available to fix PR #42"
- [ ] Slack integration / notifications via n8n
- [ ] Support for GitHub / GitLab / Bitbucket

### Phase 5 — Session Intelligence & Cost Tracking
- [ ] Multi-session per branch (remove 1:1 overwrite, version sessions)
- [ ] Fork detection (hash-based prefix matching of user messages)
- [ ] Off-topic session detection (file overlap + topic heuristic)
- [ ] AI-Blame: `aisync blame <file>` → find session that modified it
- [ ] AI-Blame with restore: `aisync blame <file> --restore`
- [ ] Per-tool/MCP token accounting (`aisync show --tool-usage`)
- [ ] Aggregated tool stats (`aisync stats --tools`)
- [ ] Real cost tracking per session (model pricing table)
- [ ] Feature cost aggregation per branch (`aisync stats --cost --branch`)

### Phase 6 — Replay & Forecasting
- [ ] Session replay with different model (`aisync replay <id> --model`)
- [ ] Session comparison (`aisync compare <id1> <id2>`)
- [ ] Cost forecasting based on historical patterns
- [ ] Web UI for session browsing, fork visualization, cost dashboard

---

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Cursor format changes between versions | Export breaks | Version parser, detect Cursor version |
| Sessions too large (>10MB) | Slow Git, storage | Compact: exclude thinking blocks, limit tool outputs |
| Multiple active sessions simultaneously | Wrong detection | Staged files heuristic + interactive prompt |
| Sensitive data in sessions | Secret leaks | Configurable filter (regex) before export |
| Claude Code changes JSONL format | Parser breaks | Format stable for months, but monitor |
| Conflicts on aisync/sessions branch | Push fails | Merge strategy: always accept-theirs (append-only) |
| Model pricing changes frequently | Cost estimates become inaccurate | Configurable pricing table, auto-update from public API or manual override |
| Replay produces different results than original | Non-deterministic LLMs | Replay is for comparison, not exact reproduction. Temperature=0 where possible |
| Fork detection false positives | Common prompts hash the same | Use N first user messages (not just 1), require minimum overlap threshold |
| Multi-session per branch → storage growth | SQLite grows large | Session expiration policy, garbage collection for old off-topic sessions |
| Tool token accounting unavailable in compact mode | Users confused by missing data | Clear warning, suggest `full` mode for accounting use cases |

---

## Differentiation vs Entire CLI

| Aspect | Entire | aisync |
|--------|--------|--------|
| Providers | Claude Code, Gemini | Claude Code, OpenCode, Cursor |
| Session restore | `entire resume` (same provider) | Cross-provider + CONTEXT.md fallback |
| PR linking | No | Yes (trailer + lookup) |
| Storage | Git branch only | SQLite + Git (hybrid) |
| Open source | No (SaaS) | Yes |
| Focus | Observability / audit | Restoration / team handoff |
| Price | Paid (after $60M seed) | Free |

---

## Open Questions to Decide

### ✅ Resolved

1. **~~Should we store thinking/reasoning?~~** → Configurable via `storage_mode`. In `full` mode yes, in `compact` and `summary` no.

2. **~~Security / secrets in sessions?~~** → Yes, automatic detection with regex patterns. Configurable mode: `mask` (default), `warn`, `block`. Custom patterns supported.

3. **~~Storage backend?~~** → Local SQLite for MVP. Git branch sync in Phase 2.

4. **~~Git provider support?~~** → Generic Git first (works with any remote). GitHub API in Phase 3 for PR linking.

### 🔄 To Decide

5. **Capture granularity:** one session per commit, or can one session cover multiple commits? → Probably link one session to N commits (a long session may produce multiple commits). The trailer in each commit points to same session.

6. **Max size:** what if a session is 50MB even in `compact` mode? → Auto truncation (keep last N messages)? Or refuse and suggest `summary` mode?

7. **Summary generation:** for `summary` mode, use LLM to summarize or do heuristic parsing (first user message + last assistant message)? → LLM would be better but adds dependency. Start with heuristic, LLM optional.

8. **Secret scan scope:** also scan tool call contents (e.g. a `cat .env` whose output contains secrets)? → Yes by default (`scan_tool_outputs: true`), but can be disabled for performance.

9. **Replay execution:** How does replay actually call the target model? Options: (a) aisync embeds an LLM client and calls APIs directly, (b) aisync generates a script/session file that the provider can execute, (c) aisync uses MCP or provider CLI to feed messages. Option (a) adds a big dependency but gives the most control.

10. **Fork detection threshold:** How many initial messages need to match to consider two sessions a "fork"? Just the first user message? First 3? Hash-based or content-similarity-based?

11. **Off-topic detection:** Heuristic (file overlap) or LLM-assisted (summarize both sessions, compare)? Heuristic is cheaper but less accurate.

12. **Cost data source:** Use OpenCode's built-in cost info when available? Maintain our own pricing table? Allow user overrides for enterprise/custom pricing?

13. **Web UI tech stack:** Go template-based (simple, single binary), or separate frontend (React/Svelte) served by aisync? Single binary is simpler but limits interactivity.

14. **Multi-session migration:** When we switch from 1-session-per-branch to N-sessions-per-branch, how do we migrate existing data? Keep the old session as "v1", new captures create new entries?

15. **Auto-summarization model:** Which AI model should be used for auto-summarization? Use Claude CLI if available? Allow configurable endpoint? Should we support local models (Ollama) for privacy-sensitive teams?

16. **Auto-summarization timing:** Summarize at capture time (synchronous, blocking) or post-capture (async background job)? Sync is simpler but adds latency to commits. Async needs a queue/retry mechanism.

17. **Rewind scope:** When rewinding to message N, should we also restore the working directory state to that point? Or only restore the session context (messages 1..N) and let the developer decide what to do with the code? Code state restore would require git stash/checkpoint integration.

18. **Explain depth:** Should `explain` generate a short summary (3-5 lines) or a detailed analysis (agent reasoning, failed attempts, decisions)? → Offer `--short` / `--detailed` flags?

19. **API authentication:** For the `aisync serve` API, do we need auth for local-only usage? What about team/remote deployment — API keys? OAuth? → Start with no auth for localhost, add API key for remote.

20. **MCP transport:** Which MCP transport to use — stdio (simple, one process) or HTTP/SSE (allows remote)? → Start with stdio for local AI tool integration.

21. **Investigation scope:** Should the investigation agent have write access to the codebase (create files, modify configs) or only propose changes as diffs? → Start with read-only analysis + diff proposals, optionally create PR.