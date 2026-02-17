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

### Phase 4 — CI Automation (future)
- [ ] GitHub Action / Webhook: on CI failure → prepare fix session
- [ ] Notify dev: "Session available to fix PR #42"
- [ ] Slack integration / notifications via n8n
- [ ] Support for GitHub / GitLab / Bitbucket

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