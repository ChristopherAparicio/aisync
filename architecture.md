# aisync — Architecture

> Last updated: 2026-02-16

---

## Design Principles

1. **DDD-inspired** — Pure domain layer with zero external dependencies. All business rules live in interfaces and value types.
2. **gh (GitHub CLI) patterns** — Factory DI, Options pattern per command, 1 subpackage per CLI command.
3. **Accept interfaces, return structs** — Interfaces defined in `internal/domain/`, implementations in dedicated packages.
4. **Provider as Adapter** — Each AI tool (Claude Code, OpenCode, Cursor) is an adapter implementing the same `Provider` interface. Adding a new provider never touches existing code.
5. **Plugin-ready** — Secret detection uses an interface (`SecretScanner`) that can be implemented as built-in regex, Go native plugin, Hashicorp go-plugin, or external script.

---

## High-Level Diagram

```
┌──────────────────────────────────────────────────────────────────┐
│                          aisync CLI                              │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    Provider Layer                        │    │
│  │  ┌──────────┐  ┌──────────────┐  ┌──────────────────┐  │    │
│  │  │  Cursor   │  │  Claude Code  │  │    OpenCode      │  │    │
│  │  │ (Phase 2) │  │              │  │                  │  │    │
│  │  │ SQLite    │  │  JSONL files │  │  SQLite / CLI    │  │    │
│  │  │ state.vscdb│  │  ~/.claude/  │  │                  │  │    │
│  │  │           │  │  projects/   │  │                  │  │    │
│  │  │ Export: ✅ │  │  Export: ✅  │  │  Export: ✅      │  │    │
│  │  │ Import: ❌ │  │  Import: ✅  │  │  Import: ✅      │  │    │
│  │  └──────────┘  └──────────────┘  └──────────────────┘  │    │
│  └─────────────────────────────────────────────────────────┘    │
│                              │                                   │
│                    Unified Session Model                         │
│                     (internal/domain/)                           │
│                              │                                   │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    Service Layer                         │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │    │
│  │  │   Capture     │  │   Restore    │  │   Secrets    │  │    │
│  │  │   Service     │  │   Service    │  │   Scanner    │  │    │
│  │  └──────────────┘  └──────────────┘  └──────────────┘  │    │
│  └─────────────────────────────────────────────────────────┘    │
│                              │                                   │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    Storage Layer                         │    │
│  │  ┌──────────────────┐  ┌────────────────────────────┐  │    │
│  │  │  SQLite Local     │  │  Git Branch Sync           │  │    │
│  │  │  ~/.aisync/db     │  │  aisync/sessions           │  │    │
│  │  │                  │  │  (Phase 2)                  │  │    │
│  │  │  - Index rapide  │  │  - Team sharing             │  │    │
│  │  │  - Queries       │  │  - Payloads JSON            │  │    │
│  │  │  - Offline       │  │  - Linked to repo           │  │    │
│  │  └──────────────────┘  └────────────────────────────┘  │    │
│  └─────────────────────────────────────────────────────────┘    │
│                              │                                   │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    Git Hooks Layer                       │    │
│  │  pre-commit  →  capture active session                  │    │
│  │  commit-msg  →  append AI-Session trailer               │    │
│  │  post-checkout → notify session available               │    │
│  └─────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────┘
```

---

## Project Structure

```
aisync/
  cmd/
    aisync/
      main.go                      # Entry point (tiny — just calls root command)

  internal/                         # Private packages (Go compiler-enforced)
    domain/                         # DOMAIN LAYER — pure types & interfaces, zero deps
      session.go                    #   Session, Message, FileChange, Link, TokenUsage
      provider.go                   #   interface Provider { Detect, Export, CanImport, Import }
      store.go                      #   interface Store { Save, Get, List, Delete, Link }
      config.go                     #   interface Config
      scanner.go                    #   interface SecretScanner
      converter.go                  #   interface Converter { ToNative, FromNative }
      errors.go                     #   Sentinel errors (ErrSessionNotFound, etc.)

    config/                         # Config implementation
      config.go                     #   JSON config: global ~/.aisync/ + per-repo .aisync/
                                    #   Per-repo overrides global (like .gitconfig)

    provider/                       # Provider implementations (adapters)
      registry.go                   #   Provider registry: auto-detection + manual selection
      claude/                       #   Claude Code provider
        claude.go                   #     Parse JSONL from ~/.claude/projects/
        claude_test.go
        testdata/                   #     Real (anonymized) JSONL fixtures
      opencode/                     #   OpenCode provider
        opencode.go                 #     Read SQLite / CLI wrapper
        opencode_test.go
        testdata/                   #     Real session fixtures

    storage/                        # Storage implementations
      sqlite/                       #   SQLite implementation of Store
        sqlite.go
        migrations.go               #     Embedded SQL migrations (//go:embed)
        sqlite_test.go

    capture/                        # Capture service (orchestration)
      service.go                    #   Detect → Export → Scan secrets → Store → Link

    restore/                        # Restore service (orchestration)
      service.go                    #   Lookup → Import or generate CONTEXT.md

    converter/                      # Cross-provider format conversion
      converter.go                  #   Unified ↔ native format conversion
      converter_test.go

    secrets/                        # Secret detection & masking
      scanner.go                    #   Built-in regex scanner (implements SecretScanner)
      patterns.go                   #   Default patterns (sk-, AKIA, ghp_, JWT, etc.)

    hooks/                          # Git hooks management
      manager.go                    #   Install / uninstall / chain with existing hooks
      templates/                    #   Embedded hook scripts (//go:embed)

  pkg/                              # Shared packages
    cmd/                            # CLI commands (Cobra, 1 subpackage per command)
      root/root.go                  #   Root command, wires all subcommands
      init/init.go                  #   aisync init
      capture/capture.go            #   aisync capture
      restore/restore.go            #   aisync restore
      list/list.go                  #   aisync list
      show/show.go                  #   aisync show
      status/status.go              #   aisync status
      hooks/hooks.go                #   aisync hooks install/uninstall
      config/config.go              #   aisync config get/set
      secrets/secrets.go            #   aisync secrets scan
      export/export.go              #   aisync export
      import/import.go              #   aisync import
      factory/
        default.go                  #   Wires all dependencies (composition root)

    cmdutil/
      factory.go                    #   Factory struct definition

    iostreams/
      iostreams.go                  #   I/O abstraction (stdout, stderr, colors, pager)

  git/                              # Git operations abstraction
    client.go                       #   Branch, hooks dir, trailers, notes, staged files
```

---

## Layer Dependencies

```
    pkg/cmd/*  (CLI layer)
        │
        ▼
    pkg/cmdutil/Factory  (DI container)
        │
        ├──► internal/capture/   (service)
        ├──► internal/restore/   (service)
        ├──► internal/converter/ (service)
        ├──► internal/secrets/   (service)
        │         │
        │         ▼
        ├──► internal/provider/* (adapters)
        ├──► internal/storage/*  (adapters)
        ├──► internal/config/    (adapter)
        └──► internal/hooks/     (adapter)
                  │
                  ▼
            internal/domain/     (interfaces & entities — NO external deps)
```

**Rules:**
- `internal/domain/` imports **nothing** (only stdlib).
- Services (`capture/`, `restore/`, `secrets/`) depend only on `domain` interfaces.
- Adapters (`provider/*`, `storage/*`, `config/`) implement `domain` interfaces.
- CLI commands (`pkg/cmd/*`) depend on the `Factory` which provides everything.
- `git/` is a standalone utility package (wraps git CLI operations).

---

## Domain Model

### Core Entities

All "enum-like" fields use **typed string constants** with `Parse*()` validation at boundaries.
See [CONTRIBUTING.md — Domain Types](./CONTRIBUTING.md#domain-types--avoiding-primitive-obsession) for the full pattern.

```
Session
├── id: SessionID                    (wrapped string, UUID)
├── version: int
├── provider: ProviderName           ← typed enum ("claude-code" | "opencode" | "cursor")
├── agent: string                    (open-ended — "claude" default, or OpenCode agent name)
├── branch: string                   (open-ended — not wrapped)
├── commit_sha: string               (open-ended — not wrapped)
├── project_path: string             (open-ended — not wrapped)
├── exported_by: string
├── exported_at: time.Time
├── created_at: time.Time
├── summary: string
├── storage_mode: StorageMode        ← typed enum (full | compact | summary)
├── messages: []Message
├── children: []Session              (optional — OpenCode sub-agent sessions)
├── parent_id: SessionID             (optional — set on child sessions)
├── file_changes: []FileChange
├── token_usage: TokenUsage
└── links: []Link

Message
├── id: string
├── role: MessageRole                ← typed enum ("user" | "assistant" | "system")
├── content: string
├── model: string                    (open-ended — too many LLMs to enumerate)
├── thinking: string                 (optional, only in full mode)
├── tool_calls: []ToolCall           (optional, only in full mode)
├── tokens: int
└── timestamp: time.Time

FileChange
├── file_path: string
└── change_type: ChangeType          ← typed enum ("created" | "modified" | "deleted" | "read")

Link
├── link_type: LinkType              ← typed enum ("branch" | "commit" | "pr")
└── ref: string

TokenUsage
├── input_tokens: int
├── output_tokens: int
└── total_tokens: int

ToolCall
├── id: string                       (tool call identifier)
├── name: string
├── input: string
├── output: string                   (tool result — merged from tool_result for Claude Code)
├── state: ToolState                 ← typed enum ("pending" | "running" | "completed" | "error")
└── duration_ms: int                 (optional — execution time)
```

### Typed Enums Summary

| Type | Values | Parse function |
|------|--------|---------------|
| `ProviderName` | `claude-code`, `opencode`, `cursor` | `ParseProviderName(s)` |
| `StorageMode` | `full`, `compact`, `summary` | `ParseStorageMode(s)` |
| `SecretMode` | `mask`, `warn`, `block` | `ParseSecretMode(s)` |
| `MessageRole` | `user`, `assistant`, `system` | `ParseMessageRole(s)` |
| `ChangeType` | `created`, `modified`, `deleted`, `read` | `ParseChangeType(s)` |
| `LinkType` | `branch`, `commit`, `pr` | `ParseLinkType(s)` |
| `ToolState` | `pending`, `running`, `completed`, `error` | `ParseToolState(s)` |
| `SessionID` | UUID string | `ParseSessionID(s)`, `NewSessionID()` |

### Domain Interfaces

```go
// Provider reads/writes sessions from/to an AI tool.
type Provider interface {
    Name() ProviderName
    Detect(projectPath string, branch string) ([]SessionSummary, error)
    Export(sessionID SessionID, mode StorageMode) (*Session, error)
    CanImport() bool
    Import(session *Session) error
}

// Store persists sessions locally.
type Store interface {
    Save(session *Session) error
    Get(id SessionID) (*Session, error)
    GetByBranch(projectPath string, branch string) (*Session, error)
    List(opts ListOptions) ([]*SessionSummary, error)
    Delete(id SessionID) error
    AddLink(sessionID SessionID, link Link) error
}

// SecretScanner detects and handles secrets in session content.
type SecretScanner interface {
    Scan(content string) []SecretMatch
    Mask(content string) string
    Mode() SecretMode
}

// Config provides configuration values.
type Config interface {
    Get(key string) (string, error)
    Set(key string, value string) error
    GetProviders() []ProviderName
    GetStorageMode() StorageMode
    GetSecretsMode() SecretMode
}

// Converter transforms sessions between provider-native formats.
type Converter interface {
    // ToNative converts a unified Session to the native format of a target provider.
    // Returns the raw bytes (JSONL for Claude, JSON for OpenCode, etc.)
    ToNative(session *Session, target ProviderName) ([]byte, error)

    // FromNative parses raw provider-native data into a unified Session.
    FromNative(data []byte, source ProviderName) (*Session, error)

    // SupportedFormats returns which provider conversions are available.
    SupportedFormats() []ProviderName
}
```

---

## Provider Format Comparison

Understanding the structural differences between providers is essential for the converter.

### Claude Code vs OpenCode — Key Differences

| Aspect | Claude Code | OpenCode |
|--------|------------|----------|
| **Storage format** | 1 JSONL file per session (monolithic) | ~180 JSON files per session (distributed) |
| **Session structure** | Flat — single conversation thread | Hierarchical — parent session + child sub-agent sessions |
| **Agent concept** | Implicit — always "claude" | Explicit — named agents (`coder`, `task`, custom...) |
| **Git branch tracking** | Native — `gitBranch` field in messages | Not tracked — must inject at capture time |
| **Tool calls** | 2 messages: `tool_use` → `tool_result` (next message) | 1 entry with lifecycle: `state: pending → running → completed → error` |
| **Export** | Read 1 file | Read hundreds of files, or use `opencode session export -f json` |
| **Summary** | Provider-generated summary available | Provider-generated summary available |

### How the Unified Format Handles This

The aisync unified format is a **superset** of both models:

**1. Agent field**
```json
{
  "agent": "claude",           // Claude Code → always "claude" (default)
  "agent": "coder",            // OpenCode → the actual agent name
  "agent": "custom-agent"      // OpenCode → custom agent
}
```
- On export: Claude Code sessions get `agent: "claude"` (default). OpenCode sessions get the real agent name.
- On import/restore: `--agent <name>` flag lets the user choose which agent to target. If not specified, uses the original agent name or provider default.

**2. Child sessions (OpenCode sub-agents)**

The unified format supports an optional `children` array:

```json
{
  "id": "parent-session-id",
  "agent": "coder",
  "messages": [...],
  "children": [
    {
      "id": "child-session-id",
      "agent": "task",
      "parent_id": "parent-session-id",
      "messages": [...]
    }
  ]
}
```

Conversion rules:
- **OpenCode → aisync**: Preserve the full tree (parent + children).
- **aisync → Claude Code**: Flatten the tree — children messages are concatenated in chronological order into a single linear thread. A system message marks each sub-agent boundary: `[Sub-agent: task] ...`.
- **aisync → OpenCode**: Restore the tree structure if children are present. Target agent can be overridden with `--agent`.

**3. Tool call representation**

The unified format preserves **both** models to avoid information loss:

```json
{
  "tool_calls": [
    {
      "id": "tc_001",
      "name": "Read",
      "input": "{\"path\": \"src/auth.py\"}",
      "output": "file contents...",
      "state": "completed",
      "duration_ms": 150
    }
  ]
}
```

Conversion rules:
- **Claude Code → aisync**: Merge `tool_use` + `tool_result` message pairs into a single `ToolCall` with `state: "completed"` (or `"error"` if tool_result has `is_error: true`). Store the output from `tool_result`.
- **OpenCode → aisync**: Map directly — OpenCode's tool lifecycle maps 1:1.
- **aisync → Claude Code**: Split back into `tool_use` message + `tool_result` message (2 separate messages in the JSONL).
- **aisync → OpenCode**: Map directly — keep the lifecycle state.

**4. Git branch injection**

- **Claude Code**: Already has `gitBranch` in messages — extract and use as-is.
- **OpenCode**: No branch info in session data — inject `branch` field at capture time from the current Git branch (`git rev-parse --abbrev-ref HEAD`).

### Conversion Matrix

| From → To | Claude Code | OpenCode | aisync (unified) |
|-----------|------------|----------|-----------------|
| **Claude Code** | — | Flatten → linear, map tools, set agent | Extract branch, merge tool pairs |
| **OpenCode** | Flatten children, split tools, agent→"claude" | — | Preserve tree, inject branch |
| **aisync** | Flatten, split tools | Restore tree, map tools | — |

---

## Key Flows

### Flow 1: Capture on Commit

```
git commit -m "feat: add OAuth2"
       │
       ▼
[pre-commit hook] → aisync capture --auto
       │
       ├── Registry.DetectAll(projectPath, branch)
       │     ├── Claude provider: scan ~/.claude/projects/...
       │     └── OpenCode provider: scan sessions
       │
       ├── Select best match (most recent, matching branch)
       │
       ├── Provider.Export(sessionID, storageMode)
       │     └── Returns unified Session
       │
       ├── SecretScanner.Mask(session)  [if mode = mask]
       │
       ├── Store.Save(session)
       │
       └── Returns session.ID

[commit-msg hook]
       │
       └── Append trailer: "AI-Session: <session-id>"
           + Store metadata as git note
```

### Flow 2: Restore on Branch

```
aisync restore
       │
       ├── Git: get current branch
       │
       ├── Store.GetByBranch(projectPath, branch)
       │     └── Returns Session
       │
       ├── If not found locally → error (Phase 2: pull from remote)
       │
       ├── Detect target provider (or use --provider flag)
       │
       ├── Provider.CanImport()?
       │     ├── YES → Provider.Import(session)
       │     └── NO  → Generate CONTEXT.md
       │
       └── Print: "Session restored. Launch your agent to continue."
```

### Flow 3: Export to File

```
aisync export --format claude -o session.jsonl
       │
       ├── Resolve session (current branch, or --session <id>)
       │
       ├── Store.GetByBranch() or Store.Get()
       │
       ├── --format flag?
       │     ├── "aisync" (default) → JSON marshal unified Session
       │     ├── "claude"  → Converter.ToNative(session, ProviderClaudeCode)
       │     └── "opencode" → Converter.ToNative(session, ProviderOpenCode)
       │
       ├── -o flag?
       │     ├── YES → write to file
       │     └── NO  → write to stdout
       │
       └── Print: "Exported session <id> (claude format, 2.3 MB)"
```

### Flow 4: Import from File

```
aisync import session.jsonl --into opencode
       │
       ├── Read file, detect format (or use --format flag)
       │     ├── Starts with '{' → JSON (aisync unified or OpenCode)
       │     ├── Multiple lines of JSON → JSONL (Claude Code)
       │     └── --format override
       │
       ├── Converter.FromNative(data, detectedProvider) → unified Session
       │
       ├── SecretScanner.Mask(session)  [if mode = mask]
       │
       ├── --into flag?
       │     ├── "aisync" (default) → Store.Save(session) only
       │     ├── "claude"  → Converter.ToNative() + Provider.Import()
       │     └── "opencode" → Converter.ToNative() + Provider.Import()
       │
       └── Print: "Imported session <id> into opencode."
```

---

## Configuration

Two-level config (global + per-repo), per-repo overrides global:

| Location | Scope | Shared via Git |
|----------|-------|----------------|
| `~/.aisync/config.json` | Global defaults | No |
| `.aisync/config.json` | Per-repo overrides | Yes (optional) |

```json
{
  "version": 1,
  "providers": ["claude-code", "opencode"],
  "auto_capture": true,
  "storage_mode": "compact",
  "secrets": {
    "mode": "mask",
    "custom_patterns": [],
    "ignore_patterns": [],
    "scan_tool_outputs": true
  },
  "commit_trailer": "AI-Session",
  "exclude_thinking": false,
  "max_session_size_mb": 10
}
```

---

## Storage

### SQLite Schema (local)

```sql
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    agent TEXT NOT NULL DEFAULT 'claude',
    branch TEXT,
    commit_sha TEXT,
    project_path TEXT NOT NULL,
    parent_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
    storage_mode TEXT NOT NULL DEFAULT 'compact',
    summary TEXT,
    message_count INTEGER,
    total_tokens INTEGER,
    payload BLOB,               -- JSON-encoded full session
    created_at DATETIME,
    exported_at DATETIME,
    exported_by TEXT
);

CREATE TABLE session_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
    link_type TEXT NOT NULL,     -- 'branch', 'commit', 'pr'
    link_ref TEXT NOT NULL
);

CREATE TABLE file_changes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    change_type TEXT NOT NULL    -- 'created', 'modified', 'deleted', 'read'
);

CREATE INDEX idx_sessions_branch ON sessions(branch);
CREATE INDEX idx_sessions_commit ON sessions(commit_sha);
CREATE INDEX idx_sessions_project ON sessions(project_path);
CREATE INDEX idx_links_ref ON session_links(link_ref);
CREATE INDEX idx_files_path ON file_changes(file_path);
```

### Git Branch Sync (Phase 2)

```
aisync/sessions/
├── <session-id>.json           # Unified session format
├── <session-id>.raw.jsonl      # Raw provider data (for exact re-import)
└── index.json                  # Lightweight index for fast lookup
```

---

## Plugin Architecture (Future)

Secret detection is designed as a pluggable system:

| Phase | Mechanism | Description |
|-------|-----------|-------------|
| MVP | Built-in Go code | Regex patterns compiled into the binary |
| Phase 2 | External scripts | `stdin` → `stdout` pipeline, language-agnostic |
| Phase 2+ | Go native plugin | `.so` loaded via `plugin.Open()` (Linux/macOS) |
| Phase 2+ | Hashicorp go-plugin | gRPC-based, cross-platform, most robust |

All mechanisms implement the same `domain.SecretScanner` interface. The capture service doesn't know or care which implementation is used.

---

## CLI Reference (MVP)

Complete list of commands available in Phase 1.

### `aisync init`

Initialize aisync in the current Git repository.

```bash
aisync init
# → Creates .aisync/config.json (per-repo config)
# → Proposes to install git hooks
# → Shows detected providers

aisync init --no-hooks              # Skip hook installation
```

### `aisync status`

Show current state: branch, detected sessions, hooks status.

```bash
aisync status
# Branch:    feature/auth-oauth2
# Providers: claude-code (active), opencode (not detected)
# Sessions:  1 session on this branch (captured 2h ago)
# Hooks:     pre-commit ✅  commit-msg ✅  post-checkout ✅
```

### `aisync capture`

Capture the active AI session and store it.

```bash
aisync capture                       # Auto-detect provider, capture current session
aisync capture --provider claude-code  # Force a specific provider
aisync capture --mode full           # Override storage mode (full|compact|summary)
aisync capture --message "Implemented OAuth2 with PKCE"  # Add manual summary
aisync capture --auto                # Used by git hooks (non-interactive, silent)
```

### `aisync restore`

Restore a previously captured session into an AI tool.

```bash
aisync restore                       # Restore latest session for current branch
aisync restore --session a1b2c3d4    # Restore a specific session
aisync restore --provider opencode   # Force restore into a specific provider
aisync restore --agent coder         # Target a specific agent (OpenCode: coder, task, custom...)
aisync restore --as-context          # Generate CONTEXT.md instead of native import

# Examples:
aisync restore --provider opencode --agent task
# → Session originally from claude-code (agent: claude)
# → Converted to opencode format, targeting agent "task"
# → Children sessions flattened into linear thread
# → Launch opencode to continue.
```

### `aisync list`

List captured sessions.

```bash
aisync list                          # Sessions for current branch
aisync list --all                    # All sessions in this project

# Example output:
# ID        PROVIDER     BRANCH              MESSAGES  TOKENS   CAPTURED
# a1b2c3d4  claude-code  feature/auth-oauth2  23       57,000   2 hours ago
# e5f6a7b8  opencode     fix/login-bug        8        12,000   1 day ago
```

### `aisync show`

Show details of a session.

```bash
aisync show a1b2c3d4                 # By session ID
aisync show abc1234                  # By commit SHA (finds linked session)
aisync show --files                  # Show files changed in session
aisync show --tokens                 # Show token usage breakdown

# Example output:
# Session:  a1b2c3d4
# Provider: claude-code
# Branch:   feature/auth-oauth2
# Captured: 2026-02-16 14:30:00
# Mode:     compact
# Messages: 23 (12 user, 11 assistant)
# Tokens:   45,000 in / 12,000 out / 57,000 total
# Summary:  Implement OAuth2 flow with PKCE for the auth module
#
# Files changed:
#   + src/auth/oauth.py        (created)
#   ~ src/auth/handler.py      (modified)
#   + tests/test_oauth.py      (created)
#
# Linked to:
#   branch: feature/auth-oauth2
#   commit: abc1234
```

### `aisync export`

Export a session to a file.

```bash
aisync export                        # Export current branch session (unified JSON, stdout)
aisync export -o session.json        # Export to file
aisync export --format claude        # Export as Claude Code native JSONL
aisync export --format opencode      # Export as OpenCode native format
aisync export --format aisync        # Export as unified JSON (default)
aisync export --session a1b2c3d4     # Export a specific session

# Examples:
aisync export --format claude -o my-session.jsonl
# → Exported session a1b2c3d4 as claude format (2.3 MB) → my-session.jsonl

aisync export | jq '.messages | length'
# → 23
```

### `aisync import`

Import a session from a file.

```bash
aisync import session.json           # Import file (auto-detect format)
aisync import session.jsonl --format claude  # Force source format
aisync import session.json --into opencode   # Convert & inject into OpenCode
aisync import session.json --into claude-code  # Convert & inject into Claude Code
aisync import session.json --into aisync     # Store in local DB only (default)
aisync import session.json --into opencode --agent task  # Target specific agent

# Examples:
aisync import colleague-session.jsonl --into opencode
# → Detected format: claude-code (JSONL)
# → Converted to opencode format
# → Imported session a1b2c3d4 into opencode
# → Launch opencode to continue with this context.

aisync import session.json
# → Detected format: aisync (unified JSON)
# → Stored session e5f6a7b8 locally
# → Use 'aisync restore --session e5f6a7b8' to load into your agent.
```

### `aisync hooks`

Manage git hooks.

```bash
aisync hooks install                 # Install pre-commit + commit-msg + post-checkout
aisync hooks uninstall               # Remove aisync hooks (preserves other hooks)
aisync hooks status                  # Show which hooks are installed

# Example output:
# pre-commit:    ✅ installed (aisync capture --auto)
# commit-msg:    ✅ installed (append AI-Session trailer)
# post-checkout: ✅ installed (notify session available)
```

### `aisync config`

View and update configuration.

```bash
aisync config                        # Show current effective config (global + repo merged)
aisync config set storage-mode full  # Set default storage mode
aisync config set providers "claude-code,opencode"  # Set active providers
aisync config set auto-capture true  # Enable/disable auto-capture on commit
aisync config set secrets.mode mask  # Set secret handling mode (mask|warn|block)
```

### `aisync secrets`

Secret detection utilities.

```bash
aisync secrets scan                  # Scan all stored sessions for secrets
aisync secrets scan --session a1b2c3d4  # Scan a specific session

# Example output:
# Scanning 3 sessions...
# ⚠ Session a1b2c3d4: 2 secrets found
#   - Line 45: AWS_ACCESS_KEY (masked)
#   - Line 128: GITHUB_TOKEN (masked)
# ✅ Session e5f6a7b8: clean
# ✅ Session c9d0e1f2: clean
```

### `aisync version`

```bash
aisync version
# aisync v0.1.0 (darwin/arm64)
```
