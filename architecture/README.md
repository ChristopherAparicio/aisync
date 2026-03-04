# aisync — Architecture

> Last updated: 2026-03-04

This directory contains the architectural documentation for aisync:

- **[README.md](./README.md)** — High-level architecture overview (this file)
- **[blame.md](./blame.md)** — AI-Blame feature: design, queries, performance

---

## Design Principles

1. **Server = DDD / Hexagonal, Client = Pragmatic CLI** — The server (`internal/`) follows Hexagonal Architecture (Ports & Adapters) with a service layer, domain entities, and port interfaces. The client side (`client/`, `pkg/cmd/`) stays flat and pragmatic, following gh CLI patterns — thin commands calling an HTTP SDK.
2. **Clean server/client separation** — The server exposes capabilities via HTTP/REST API. The `client/` package is a public Go SDK that talks to the API. The CLI, TUI, Web UI, and external tools all use `client/` — they never import `internal/` directly.
3. **Service Layer as the single orchestration point** — All business logic flows through `internal/service/`. The API server is the only entry point to services. No logic in client-side code.
4. **Incremental evolution** — The codebase evolves from CLI-only to server+clients without big-bang rewrites. Existing packages (`capture/`, `restore/`, `gitsync/`) become internal implementation details of application services.
5. **Interfaces where they earn their keep** — Interfaces exist at domain boundaries: `Provider` (3 implementations), `Store` (extensibility + testing), `SessionConverter` (testing). Everything else is concrete until a second implementation appears.
6. **Factory DI for standalone mode** — Lazy-initializing Factory struct allows CLI to work without a running server (calls services directly in-process). When a server is running, CLI uses `client/` instead.
7. **Plugin-ready** — Secret detection supports built-in regex, external scripts, Go native plugins, and Hashicorp go-plugin (gRPC).

---

## Architecture Overview

aisync is split into two sides: a **Server** (DDD/Hexagonal) and **Clients** (pragmatic).

```
  CLIENTS (pragmatic — flat, gh CLI style)
  ═══════════════════════════════════════════════════════════════════════

  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐
  │ CLI      │  │ TUI      │  │ Web UI   │  │ External │
  │ (Cobra)  │  │(Bubbletea│  │ (SPA)    │  │ tools    │
  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘
       │              │              │              │
       └──────────────┴──────┬───────┴──────────────┘
                             │
                      ┌──────┴──────┐
                      │  client/    │  ← Public Go SDK (HTTP client)
                      │  Client     │    Root-level package, like git/
                      └──────┬──────┘
                             │
                        HTTP/REST
                             │
  ═══════════════════════════╪═══════════════════════════════════════════
  SERVER (DDD / Hexagonal — internal/)
                             │
  ┌──────────────────────────┼────────────────────────────────────────┐
  │                    API + MCP (Inbound)                             │
  │                          │                                        │
  │  ┌───────────────┐  ┌───┴───────────┐                            │
  │  │ HTTP/REST API │  │  MCP Server   │                            │
  │  │ (handlers)    │  │  (tools)      │                            │
  │  └───────┬───────┘  └───────┬───────┘                            │
  │          └──────────┬───────┘                                     │
  │                     │                                             │
  ├─────────────────────┼─────────────────────────────────────────────┤
  │              APPLICATION LAYER (Services)                         │
  │                     │                                             │
  │  ┌─────────────────────────────────────────────────────────────┐  │
  │  │  SessionService       AnalysisService       SyncService    │  │
  │  │  ├─ Capture()         ├─ Investigate()       ├─ Push()     │  │
  │  │  ├─ Restore()         ├─ Explain()           ├─ Pull()     │  │
  │  │  ├─ Export()          ├─ Summarize()         └─ Sync()     │  │
  │  │  ├─ Import()          └─ ProposeChanges()                  │  │
  │  │  ├─ List() / Get()                                         │  │
  │  │  ├─ Search() / Blame()                                      │  │
  │  │  ├─ Link() / Stats()                                       │  │
  │  │  └─ Delete()                                               │  │
  │  └─────────────────────────────────────────────────────────────┘  │
  │                     │                                             │
  ├─────────────────────┼─────────────────────────────────────────────┤
  │              DOMAIN LAYER (Core)                                  │
  │                     │                                             │
  │  ┌─────────────────────────────────────────────────────────────┐  │
  │  │  session.Session     session.Message     session.ToolCall   │  │
  │  │  session.FileChange  session.Link        session.TokenUsage │  │
  │  │                                                             │  │
  │  │  --- Ports (interfaces) ---                                 │  │
  │  │  provider.Provider   storage.Store   converter.SessionConv  │  │
  │  └─────────────────────────────────────────────────────────────┘  │
  │                     │                                             │
  ├─────────────────────┼─────────────────────────────────────────────┤
  │              DRIVEN ADAPTERS (Outbound / Infrastructure)          │
  │                     │                                             │
  │  ┌─────────────────────────────────────────────────────────────┐  │
  │  │  ┌─────────┐ ┌──────────┐ ┌─────────┐ ┌─────────┐         │  │
  │  │  │ SQLite  │ │ Claude   │ │OpenCode │ │ Cursor  │         │  │
  │  │  │ Store   │ │ Provider │ │Provider │ │Provider │         │  │
  │  │  └─────────┘ └──────────┘ └─────────┘ └─────────┘         │  │
  │  │  ┌─────────┐ ┌──────────┐ ┌─────────┐ ┌──────────┐        │  │
  │  │  │ Git     │ │ GitHub   │ │ LLM     │ │Converter │        │  │
  │  │  │ Client  │ │ Client   │ │ Client  │ │          │        │  │
  │  │  └─────────┘ └──────────┘ └─────────┘ └──────────┘        │  │
  │  └─────────────────────────────────────────────────────────────┘  │
  └───────────────────────────────────────────────────────────────────┘
```

### Boundary Rules

| Side | Depends on | Never depends on |
|------|-----------|-----------------|
| **Clients** (CLI, TUI, Web UI) | `client/` package only | `internal/` (server internals) |
| **client/** (HTTP SDK) | `internal/session/` (shared types for DTOs) | `internal/service/`, adapters |
| **API / MCP** (server inbound) | Application Layer | Client code |
| **Application Layer** (Services) | Domain Layer (entities + ports) | Concrete adapters |
| **Domain Layer** (entities, ports) | stdlib only | Anything else |
| **Driven Adapters** (infra) | Domain Layer (implement ports) | Application Layer, Clients |

**Key rule:** Clients never import `internal/`. They go through `client/` which talks HTTP to the server. In standalone mode (no server), the CLI uses the Factory to wire services directly in-process — but this is a convenience, not the primary path.

**Pragmatic exception:** `client/` imports `internal/session/` for shared types (Session, Summary, enums). This avoids duplicating domain types while keeping the client package usable as a standalone SDK.

**Why MCP is server-side, not client-side:** The MCP server runs inside the aisync process and calls services directly (no HTTP hop). It's an inbound adapter on the server, not a client. When an AI agent (Claude Code, OpenCode) calls an MCP tool like `aisync_restore`, it goes: `MCP tool → SessionService.Restore()` — same service, same interface as the API handler. The MCP server doesn't need its own service interface; it reuses `SessionService` and `SyncService` as-is.

---

## Architecture Evolution

The codebase has evolved through three architectural phases:

### Phase 0 — Flat CLI (Completed)
CLI commands directly called infrastructure. `capture/service.go` and `restore/service.go` were orchestrators called by Cobra commands. This worked well for a single-client CLI.

### Phase 1 — Service Layer Extraction (Completed)
Extracted orchestration logic from `capture/`, `restore/`, and `gitsync/` into `internal/service/SessionService` (10 methods) and `internal/service/SyncService` (4 methods). All 13 CLI commands rewired as thin adapters calling services.

### Phase 2 — API Server + Multiple Clients (Completed)
Added HTTP/REST API (`internal/api/`, 17 endpoints), Client SDK (`client/`, 17 methods), and MCP Server (`internal/mcp/`, 16 tools). Three driving adapters now share the same service layer.

### Phase 3 — User Identity Layer (Completed)
Added `users` table, `OwnerID` field on sessions, auto-detection from git config. Store interface grew from 8 to 12 methods.

### Phase 3.5 — Search (Completed)
Added `Search()` method to Store (now 13 methods), `SearchQuery`/`SearchResult` domain types, search endpoint, MCP tool, CLI command (`aisync search`), and client SDK method. Keyword search uses SQL LIKE on summary (FTS5 for message content deferred).

### Phase 5.2 — AI-Blame (Completed)
Added `GetSessionsByFile()` method to Store (now 14 methods), `BlameEntry`/`BlameQuery` domain types, blame endpoint (`GET /api/v1/blame`), MCP tool (`aisync_blame`), CLI command (`aisync blame <file>`), and client SDK method. File-level reverse lookup from `file_changes` table via JOIN. See [blame.md](./blame.md) for design details.

### What Stays the Same
- Domain entities in `internal/session/` — stable core
- Interfaces (`Provider`, `Store`, `SessionConverter`) — well-defined ports
- Provider implementations — unchanged, they're driven adapters
- SQLite storage — extended (users table, owner_id), still sole Store impl
- Git client, secrets scanner, config — unchanged

---

## Project Structure

```
aisync/
  cmd/
    aisync/
      main.go                      # Entry point (trivial — calls root command)

  internal/                         # Private packages (Go compiler-enforced)

    # ── DOMAIN LAYER ──────────────────────────────────────────────────
    session/                        # Domain entities — structs, enums, errors (no interfaces)
      session.go                    #   Session, Message, FileChange, Link, TokenUsage, ToolCall
      enums.go                      #   ProviderName, StorageMode, MessageRole, etc.
      errors.go                     #   Sentinel errors (ErrSessionNotFound, etc.)

    # ── PORTS (interfaces, defined by their consumers) ────────────────
    provider/                       # Provider port + registry
      provider.go                   #   Provider interface (5 methods)
      registry.go                   #   Registry: auto-detection + manual selection
    storage/                        # Storage port
      store.go                      #   Store interface (14 methods)

    # ── APPLICATION LAYER (services) ──────────────────────────────────
    service/                        # Application services
      session.go                    #   SessionService: capture, restore, export, import, list, get, link, comment, search, blame, stats, delete + resolveOwner
      sync.go                       #   SyncService: push, pull, sync, readIndex

    # ── DRIVEN ADAPTERS (outbound infrastructure) ─────────────────────
    provider/
      claude/                       #   Claude Code provider (implements Provider port)
        claude.go                   #     Parse/write JSONL from ~/.claude/projects/
        claude_test.go              #     MarshalJSONL(), UnmarshalJSONL() — pure functions
        testdata/
      opencode/                     #   OpenCode provider (implements Provider port)
        opencode.go                 #     MarshalJSON(), UnmarshalJSON() — pure functions
        opencode_test.go
        testdata/
      cursor/                       #   Cursor provider (implements Provider port)
        cursor.go
        cursor_test.go

    storage/
      sqlite/                       #   SQLite storage (implements Store port)
        sqlite.go
        migrations.go               #     Embedded SQL migrations (//go:embed)
        sqlite_test.go

    converter/                      # Cross-provider format conversion
      converter.go                  #   Delegates to provider marshal/unmarshal functions
      converter_test.go

    config/                         # Config (concrete struct)
      config.go                     #   JSON config: global ~/.aisync/ + per-repo .aisync/

    secrets/                        # Secret detection & masking
      scanner.go                    #   Built-in regex scanner
      scanner_test.go
      patterns.go                   #   Default patterns (sk-, AKIA, ghp_, JWT, etc.)
      scanplugin/                   #   Plugin infrastructure for external scanners
        shared.go                   #     Handshake config + ScannerPlugin interface
        adapter.go                  #     Adapts gRPC plugin to Scanner
        grpc.go                     #     gRPC plugin host/client (HashiCorp go-plugin)
        native.go                   #     Go native plugin loader (.so)
        script.go                   #     Script-based plugin (stdin/stdout)
        proto/                      #     Protobuf definitions

    platform/                       # Code hosting platform integration
      detect.go                     #   Detects GitHub/GitLab/Bitbucket from remote URL
      github/                       #   GitHub implementation (gh CLI wrapper)
        github.go
        github_test.go

    hooks/                          # Git hooks management
      manager.go                    #   Install / uninstall / chain with existing hooks
      templates/                    #   Embedded hook scripts (//go:embed)

    # ── EXISTING ORCHESTRATORS (to be absorbed into service/) ─────────
    capture/                        # Capture orchestration (→ SessionService.Capture)
      service.go
      service_test.go
    restore/                        # Restore orchestration (→ SessionService.Restore)
      service.go
      service_test.go
    gitsync/                        # Git sync orchestration (→ SyncService)
      service.go
      service_test.go

    # ── DRIVING ADAPTERS (inbound) ────────────────────────────────────
    api/                            # HTTP/REST API server
      server.go                     #   Router, composition root, graceful shutdown
      routes.go                     #   Route registration (17 endpoints)
      handlers.go                   #   Session + search + blame + stats handlers calling SessionService
      sync_handlers.go              #   Sync handlers calling SyncService
      middleware.go                 #   JSON helpers, error mapping, logging
      server_test.go                #   19 integration tests

    mcp/                            # MCP Server for AI tool integration
      server.go                     #   MCP server using mark3labs/mcp-go (stdio)
      tools.go                      #   12 session tool handlers (incl. search + blame)
      sync_tools.go                 #   4 sync tool handlers
      server_test.go                #   17 tests

    tui/                            # Terminal UI (Bubble Tea)
      tui.go
      dashboard.go
      list.go
      detail.go
      styles.go

    testutil/                       # Shared test helpers
      testutil.go

  pkg/                              # Semi-public packages (CLI driving adapter)
    cmd/                            # CLI commands (Cobra — thin wrappers over services)
      root/root.go                  #   Root command, wires all subcommands
      factory/
        default.go                  #   CLI COMPOSITION ROOT — wires dependencies for CLI
      initcmd/init.go               #   aisync init
      capture/capture.go            #   aisync capture → SessionService.Capture()
      restore/restore.go            #   aisync restore → SessionService.Restore()
      listcmd/list.go               #   aisync list → SessionService.List()
      show/show.go                  #   aisync show → SessionService.Get()
      status/status.go              #   aisync status
      hooks/hooks.go                #   aisync hooks install/uninstall
      secrets/secrets.go            #   aisync secrets scan
      export/export.go              #   aisync export → SessionService.Export()
      importcmd/importcmd.go        #   aisync import → SessionService.Import()
      linkcmd/linkcmd.go            #   aisync link → SessionService.Link()
      commentcmd/commentcmd.go      #   aisync comment
      searchcmd/searchcmd.go        #   aisync search → SessionService.Search()
      blamecmd/blamecmd.go          #   aisync blame → SessionService.Blame()
      statscmd/statscmd.go          #   aisync stats → SessionService.Stats()
      synccmd/synccmd.go            #   aisync push/pull/sync → SyncService
      tuicmd/tuicmd.go              #   aisync tui
      servecmd/serve.go              #   aisync serve (starts API server)
      mcpcmd/mcp.go                 #   aisync mcp serve (starts MCP server via stdio)

    cmdutil/
      factory.go                    #   Factory struct (lazy DI container for CLI)

    iostreams/
      iostreams.go                  #   I/O abstraction (stdout, stderr, colors, pager)

  client/                            # Public Go HTTP client SDK
    client.go                       #   Client struct, New(baseURL), HTTP helpers, APIError
    sessions.go                     #   Session methods + search + client-side types (decoupled from internal/)
    sync.go                         #   Sync methods + types
    client_test.go                  #   12 integration tests

  git/                              # Git CLI wrapper (public package)
    client.go                       #   Branch, hooks, notes, sync branch operations, UserName/UserEmail

  examples/
    plugins/
      grpc/main.go                  #   Example gRPC secret scanner plugin
      native/plugin.go              #   Example native (.so) plugin
```

---

## Package Dependencies

```
  DRIVING ADAPTERS (inbound)
  ──────────────────────────

  pkg/cmd/*          internal/api/        internal/mcp/
  (CLI commands)     (HTTP handlers)      (MCP tools)
       │                  │                    │
       └──────────────────┼────────────────────┘
                          │
                          ▼
  APPLICATION LAYER
  ──────────────────────────

  internal/service/SessionService
  internal/service/AnalysisService
  internal/service/SyncService
       │
       │  depends on port interfaces (not concrete implementations)
       │
       ▼
  DOMAIN LAYER
  ──────────────────────────

  internal/session/          (entities, enums, errors)
  internal/provider/         (Provider interface — port)
  internal/storage/          (Store interface — port)
       ▲
       │
       │  implements port interfaces
       │
  DRIVEN ADAPTERS (outbound)
  ──────────────────────────

  internal/provider/claude/   (implements Provider)
  internal/provider/opencode/ (implements Provider)
  internal/provider/cursor/   (implements Provider)
  internal/storage/sqlite/    (implements Store)
  internal/converter/         (format conversion)
  internal/secrets/           (secret scanning)
  internal/platform/github/   (GitHub API)
  internal/config/            (configuration)
  git/                        (Git operations)
```

### Composition Roots

Two separate composition roots wire everything together:

| Composition Root | Location | Wires |
|-----------------|----------|-------|
| **CLI** | `pkg/cmd/factory/default.go` | Factory → Services → Adapters (for CLI usage) |
| **API Server** | `internal/api/server.go` | Router → Handlers → Services → Adapters (for HTTP) |

Both roots create the same services with the same adapters. The difference is the driving adapter (CLI vs HTTP).

---

## Interfaces (Ports)

### Provider (in `internal/provider/provider.go`)

```go
// Provider reads/writes sessions from/to an AI tool.
// Driven port — 3 implementations: claude/, opencode/, cursor/
type Provider interface {
    Name() session.ProviderName
    Detect(projectPath string, branch string) ([]session.Summary, error)
    Export(sessionID session.ID, mode session.StorageMode) (*session.Session, error)
    CanImport() bool
    Import(session *session.Session) error
}
```

**Why an interface:** 3 concrete implementations (Claude Code, OpenCode, Cursor), and users may add more. The Registry iterates over providers to auto-detect sessions — polymorphism is essential here.

### Store (in `internal/storage/store.go`)

```go
// Store persists sessions and users locally.
// Driven port — current implementation: sqlite/
type Store interface {
    Save(session *session.Session) error
    Get(id session.ID) (*session.Session, error)
    GetByBranch(projectPath string, branch string) (*session.Session, error)
    List(opts session.ListOptions) ([]session.Summary, error)
    Delete(id session.ID) error
    AddLink(sessionID session.ID, link session.Link) error
    GetByLink(linkType session.LinkType, ref string) ([]session.Summary, error)
    Search(query session.SearchQuery) (*session.SearchResult, error)
    GetSessionsByFile(query session.BlameQuery) ([]session.BlameEntry, error)
    SaveUser(user *session.User) error
    GetUser(id session.ID) (*session.User, error)
    GetUserByEmail(email string) (*session.User, error)
    Close() error
}
```

**Why an interface:** Enables testing with in-memory mocks (13 mockStores across test files), and leaves the door open for alternative backends (flat file, remote storage).

### SessionConverter (in `internal/restore/service.go`)

```go
// SessionConverter converts sessions between provider formats.
// Consumed by restore service for cross-provider restoration.
type SessionConverter interface {
    ToNative(sess *session.Session, target session.ProviderName) ([]byte, error)
    FromNative(data []byte, source session.ProviderName) (*session.Session, error)
    ToContextMD(sess *session.Session) ([]byte, error)
}
```

### Everything Else Is Concrete

| Package | Type | Why no interface |
|---------|------|-----------------|
| `config/` | `*config.Config` | One implementation. Will never swap JSON config at runtime. |
| `secrets/` | `*secrets.Scanner` | Extensibility comes from the plugin system, not Go interfaces. |
| `platform/github/` | `*github.Client` | One implementation per platform. Added as concrete types when needed. |
| `converter/` | `*converter.Converter` | One implementation. Delegates to provider marshal/unmarshal functions. |

---

## Application Services

The Application Layer is the heart of the architecture. Services orchestrate domain entities and infrastructure adapters to fulfill use cases. **All driving adapters (CLI, API, MCP) call these same services.**

### SessionService

```go
// SessionService orchestrates all session-related workflows.
// It replaces the direct coupling between CLI commands and infrastructure.
type SessionService struct {
    registry  *provider.Registry
    store     storage.Store
    converter SessionConverter
    scanner   *secrets.Scanner
    config    *config.Config
    git       *git.Client
}

func (s *SessionService) Capture(ctx context.Context, req CaptureRequest) (*session.Session, error)
func (s *SessionService) Restore(ctx context.Context, req RestoreRequest) error
func (s *SessionService) Export(ctx context.Context, req ExportRequest) ([]byte, error)
func (s *SessionService) Import(ctx context.Context, req ImportRequest) (*session.Session, error)
func (s *SessionService) List(ctx context.Context, req ListRequest) ([]*session.Summary, error)
func (s *SessionService) Get(ctx context.Context, id session.ID) (*session.Session, error)
func (s *SessionService) Link(ctx context.Context, req LinkRequest) error
func (s *SessionService) Search(ctx context.Context, req SearchRequest) (*session.SearchResult, error)
func (s *SessionService) Blame(ctx context.Context, req BlameRequest) (*BlameResult, error)
func (s *SessionService) Stats(ctx context.Context, req StatsRequest) (*StatsResult, error)
func (s *SessionService) Delete(ctx context.Context, id session.ID) error
```

### AnalysisService

```go
// AnalysisService provides AI-powered session intelligence.
// Requires an LLM client for investigation, explanation, and change proposals.
type AnalysisService struct {
    store     storage.Store
    llm       LLMClient           // Port: interface for LLM calls
    git       *git.Client
    platform  *github.Client      // Optional: for PR creation
}

func (s *AnalysisService) Investigate(ctx context.Context, req InvestigateRequest) (*Investigation, error)
func (s *AnalysisService) Explain(ctx context.Context, sessionID session.ID) (*Explanation, error)
func (s *AnalysisService) Summarize(ctx context.Context, sess *session.Session) (*Summary, error)
func (s *AnalysisService) ProposeChanges(ctx context.Context, req ProposalRequest) (*Proposal, error)
```

**Key use case — Investigation Agent:**
When an AI agent in OpenCode misuses a skill, a developer can trigger an investigation:
1. AnalysisService reads the session from the Store
2. It accesses the codebase via Git client to understand the skill definition
3. It calls the LLM to analyze what went wrong (bad prompt? missing context? wrong skill?)
4. It proposes concrete changes to the repository (improved skill definition, updated config)
5. Optionally, it creates a PR with the proposed changes via the GitHub client

### SyncService

```go
// SyncService handles team session sharing via Git branches.
type SyncService struct {
    store storage.Store
    git   *git.Client
}

func (s *SyncService) Push(ctx context.Context) error
func (s *SyncService) Pull(ctx context.Context) error
func (s *SyncService) Sync(ctx context.Context) error
```

---

## Driving Adapters

### CLI (Current — pkg/cmd/)

Thin Cobra commands that parse flags, call a service, and format output:

```go
// pkg/cmd/capture/capture.go
func runCapture(f *cmdutil.Factory, opts *CaptureOptions) error {
    svc := f.SessionService()               // Get service from Factory
    sess, err := svc.Capture(ctx, service.CaptureRequest{
        Provider:    opts.Provider,
        Mode:        opts.Mode,
        Message:     opts.Message,
        ProjectPath: opts.ProjectPath,
    })
    if err != nil { return err }
    fmt.Fprintf(f.IOStreams.Out, "Captured session %s\n", sess.ID)
    return nil
}
```

### HTTP/REST API (internal/api/) — IMPLEMENTED

RESTful API exposing the same services over HTTP (stdlib `net/http`, no external router):

```
GET    /api/v1/health                    → Health check
POST   /api/v1/sessions/capture          → SessionService.Capture()
POST   /api/v1/sessions/restore          → SessionService.Restore()
GET    /api/v1/sessions/{id}             → SessionService.Get()
GET    /api/v1/sessions                  → SessionService.List()
DELETE /api/v1/sessions/{id}             → SessionService.Delete()
POST   /api/v1/sessions/export           → SessionService.Export()
POST   /api/v1/sessions/import           → SessionService.Import()
POST   /api/v1/sessions/link             → SessionService.Link()
POST   /api/v1/sessions/comment          → SessionService.Comment()
GET    /api/v1/sessions/search           → SessionService.Search()
GET    /api/v1/blame                     → SessionService.Blame()
GET    /api/v1/stats                     → SessionService.Stats()
POST   /api/v1/sync/push                 → SyncService.Push()
POST   /api/v1/sync/pull                 → SyncService.Pull()
POST   /api/v1/sync/sync                 → SyncService.Sync()
GET    /api/v1/sync/index                → SyncService.ReadIndex()
```

Launched via `aisync serve --port 8080 --host 127.0.0.1`.

### MCP Server (internal/mcp/) — IMPLEMENTED

Exposes aisync capabilities as MCP tools callable from within Claude Code or OpenCode.
Uses `mark3labs/mcp-go` v0.44.1 over stdio transport (JSON-RPC 2.0).

```
Tool: aisync_capture        → SessionService.Capture()
Tool: aisync_restore        → SessionService.Restore()
Tool: aisync_get             → SessionService.Get()
Tool: aisync_list            → SessionService.List()
Tool: aisync_delete          → SessionService.Delete()
Tool: aisync_export          → SessionService.Export()
Tool: aisync_import          → SessionService.Import()
Tool: aisync_link            → SessionService.Link()
Tool: aisync_comment         → SessionService.Comment()
Tool: aisync_search          → SessionService.Search()
Tool: aisync_blame           → SessionService.Blame()
Tool: aisync_stats           → SessionService.Stats()
Tool: aisync_push            → SyncService.Push()
Tool: aisync_pull            → SyncService.Pull()
Tool: aisync_sync            → SyncService.Sync()
Tool: aisync_index           → SyncService.ReadIndex()
```

Launched via `aisync mcp serve`. AI agents call tools directly during coding sessions.

---

## Session Types (internal/session/)

All shared data types live in `internal/session/`. This package has no business logic, no interfaces, and no external dependencies beyond stdlib.

### Core Types

```
Session
+-- id: ID                           (wrapped string, UUID)
+-- owner_id: ID                     (optional — references users table)
+-- version: int
+-- provider: ProviderName           <- typed const ("claude-code" | "opencode" | "cursor")
+-- agent: string                    (open-ended — "claude" default, or OpenCode agent name)
+-- branch: string
+-- commit_sha: string
+-- project_path: string
+-- exported_by: string
+-- exported_at: time.Time
+-- created_at: time.Time
+-- summary: string
+-- storage_mode: StorageMode        <- typed const (full | compact | summary)
+-- messages: []Message
+-- children: []Session              (optional — OpenCode sub-agent sessions)
+-- parent_id: ID                    (optional — set on child sessions)
+-- file_changes: []FileChange
+-- token_usage: TokenUsage
+-- links: []Link

User
+-- id: ID                           (UUID)
+-- name: string                     (from git config user.name)
+-- email: string                    (unique — from git config user.email)
+-- source: string                   ("git", "config", "api")
+-- created_at: time.Time

Message
+-- id: string
+-- role: MessageRole                <- typed const ("user" | "assistant" | "system")
+-- content: string
+-- model: string
+-- thinking: string                 (optional, only in full mode)
+-- tool_calls: []ToolCall           (optional, only in full mode)
+-- tokens: int
+-- timestamp: time.Time

ToolCall
+-- id: string
+-- name: string
+-- input: string
+-- output: string
+-- state: ToolState                 <- typed const ("pending" | "running" | "completed" | "error")
+-- duration_ms: int

FileChange
+-- file_path: string
+-- change_type: ChangeType          <- typed const ("created" | "modified" | "deleted" | "read")

Link
+-- link_type: LinkType              <- typed const ("branch" | "commit" | "pr")
+-- ref: string

TokenUsage
+-- input_tokens: int
+-- output_tokens: int
+-- total_tokens: int

SearchQuery                          (all filters optional, AND logic)
+-- keyword: string                  (LIKE %keyword% on summary)
+-- project_path: string
+-- branch: string
+-- provider: ProviderName
+-- owner_id: ID
+-- since: time.Time
+-- until: time.Time
+-- limit: int                       (default 50, max 200)
+-- offset: int

SearchResult
+-- sessions: []Summary
+-- total_count: int
+-- limit: int
+-- offset: int

BlameQuery                           (file_path required, rest optional)
+-- file_path: string                (required)
+-- branch: string
+-- provider: ProviderName
+-- limit: int

BlameEntry
+-- session_id: ID
+-- provider: ProviderName
+-- branch: string
+-- summary: string
+-- change_type: ChangeType
+-- created_at: time.Time
+-- owner_id: ID
```

### Typed Enums

| Type | Values | Parse function |
|------|--------|---------------|
| `ProviderName` | `claude-code`, `opencode`, `cursor` | `ParseProviderName(s)` |
| `StorageMode` | `full`, `compact`, `summary` | `ParseStorageMode(s)` |
| `SecretMode` | `mask`, `warn`, `block` | `ParseSecretMode(s)` |
| `MessageRole` | `user`, `assistant`, `system` | `ParseMessageRole(s)` |
| `ChangeType` | `created`, `modified`, `deleted`, `read` | `ParseChangeType(s)` |
| `LinkType` | `branch`, `commit`, `pr` | `ParseLinkType(s)` |
| `ToolState` | `pending`, `running`, `completed`, `error` | `ParseToolState(s)` |
| `ID` | UUID string | `ParseID(s)`, `NewID()` |

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
| **Tool calls** | 2 messages: `tool_use` -> `tool_result` (next message) | 1 entry with lifecycle: `state: pending -> running -> completed -> error` |
| **Export** | Read 1 file | Read hundreds of files, or use `opencode session export -f json` |
| **Summary** | Provider-generated summary available | Provider-generated summary available |

### How the Unified Format Handles This

The aisync unified format is a **superset** of both models:

**1. Agent field**
- On export: Claude Code sessions get `agent: "claude"` (default). OpenCode sessions get the real agent name.
- On import/restore: `--agent <name>` flag lets the user choose which agent to target.

**2. Child sessions (OpenCode sub-agents)**
- **OpenCode -> aisync**: Preserve the full tree (parent + children).
- **aisync -> Claude Code**: Flatten the tree — children messages are concatenated in chronological order into a single linear thread.
- **aisync -> OpenCode**: Restore the tree structure if children are present.

**3. Tool call representation**

The unified format preserves both models to avoid information loss:
- **Claude Code -> aisync**: Merge `tool_use` + `tool_result` message pairs into a single `ToolCall` with state.
- **OpenCode -> aisync**: Map directly — OpenCode's tool lifecycle maps 1:1.
- **aisync -> Claude Code**: Split back into `tool_use` message + `tool_result` message.
- **aisync -> OpenCode**: Map directly — keep the lifecycle state.

**4. Git branch injection**
- **Claude Code**: Already has `gitBranch` in messages — extract and use as-is.
- **OpenCode**: No branch info — inject `branch` field at capture time from `git rev-parse --abbrev-ref HEAD`.

### Provider Marshal/Unmarshal (Pure Functions)

Each provider exposes pure marshal/unmarshal functions that the converter delegates to:

```go
// Claude Code
claude.MarshalJSONL(sess *session.Session) ([]byte, error)
claude.UnmarshalJSONL(data []byte, mode session.StorageMode) (*session.Session, error)

// OpenCode
opencode.MarshalJSON(sess *session.Session) ([]byte, error)
opencode.UnmarshalJSON(data []byte) (*session.Session, error)
```

The converter (`internal/converter/`) composes these functions for cross-provider conversion. It has no format-specific logic of its own — it's a thin delegation layer.

---

## Key Flows

### Flow 1: Capture (CLI → Service → Infrastructure)

```
aisync capture --mode full
       │
       └── CLI command: parse flags, get SessionService from Factory
              │
              └── SessionService.Capture(ctx, CaptureRequest{...})
                     │
                     ├── registry.DetectAll(projectPath, branch)
                     │     ├── Claude provider: scan ~/.claude/projects/...
                     │     └── OpenCode provider: scan sessions
                     │
                     ├── Select best match (most recent, matching branch)
                     │
                     ├── provider.Export(sessionID, storageMode)
                     │     └── Returns session.Session
                     │
                     ├── scanner.Mask(session)  [if mode = mask]
                     │
                     ├── store.Save(session)
                     │
                     └── Returns session.Session (with ID)
```

### Flow 2: Capture via API (HTTP → Service → Infrastructure)

```
POST /api/v1/sessions/capture
     {"provider": "claude-code", "mode": "full"}
       │
       └── HTTP handler: parse JSON body, validate
              │
              └── SessionService.Capture(ctx, CaptureRequest{...})
                     │
                     └── (same flow as CLI — identical service call)
```

### Flow 3: Restore (Service orchestration)

```
SessionService.Restore(ctx, RestoreRequest{...})
       │
       ├── git.CurrentBranch()
       │
       ├── store.GetByBranch(projectPath, branch)
       │
       ├── Detect target provider (or use --provider flag)
       │
       ├── provider.CanImport()?
       │     ├── YES → provider.Import(session)
       │     └── NO  → converter.ToContextMD(session) → write file
       │
       └── Return result
```

### Flow 4: Investigation Agent (AnalysisService)

```
AnalysisService.Investigate(ctx, InvestigateRequest{
    SessionID: "abc123",
    Question:  "Why did the agent misuse the Read skill?",
})
       │
       ├── store.Get(sessionID) → load session with all messages
       │
       ├── git.ReadFile(skillDefinition) → read skill/tool config from codebase
       │
       ├── llm.Analyze(session, codebaseContext, question)
       │     └── LLM examines: messages, tool calls, skill definition
       │     └── Returns: root cause, evidence, recommendations
       │
       ├── Optional: ProposeChanges()
       │     ├── Generate diff for improved skill definition
       │     └── platform.CreatePR(changes)
       │
       └── Return Investigation{RootCause, Evidence, Recommendations, ProposedPR}
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
  "max_session_size_mb": 10,
  "server": {
    "port": 8080,
    "host": "127.0.0.1"
  }
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
    owner_id TEXT,               -- references users(id), nullable
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

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT NOT NULL UNIQUE,
    source TEXT NOT NULL DEFAULT 'git',  -- 'git', 'config', 'api'
    created_at TEXT NOT NULL
);

CREATE INDEX idx_sessions_branch ON sessions(branch);
CREATE INDEX idx_sessions_commit ON sessions(commit_sha);
CREATE INDEX idx_sessions_project ON sessions(project_path);
CREATE INDEX idx_links_ref ON session_links(link_ref);
CREATE INDEX idx_files_path ON file_changes(file_path);
```

### Git Branch Sync

```
aisync/sessions/
+-- <session-id>.json           # Unified session format
+-- <session-id>.raw.jsonl      # Raw provider data (for exact re-import)
+-- index.json                  # Lightweight index for fast lookup
```

---

## Plugin Architecture

Secret detection is designed as a pluggable system. The core `secrets.Scanner` is a concrete struct, but it delegates to plugins when configured:

| Mechanism | Description |
|-----------|-------------|
| Built-in Go code | Regex patterns compiled into the binary (default) |
| External scripts | `stdin` -> `stdout` pipeline, language-agnostic |
| Go native plugin | `.so` loaded via `plugin.Open()` (Linux/macOS) |
| Hashicorp go-plugin | gRPC-based, cross-platform, most robust |

All plugin mechanisms are managed by `internal/secrets/scanplugin/`. The `Scanner` struct composes built-in patterns with any loaded plugins transparently.

---

## CLI Reference

### Core Commands

| Command | Description | Service |
|---------|-------------|---------|
| `aisync init` | Initialize aisync in current repo | — |
| `aisync status` | Show current state | — |
| `aisync capture` | Capture active AI session | `SessionService.Capture()` |
| `aisync restore` | Restore session into AI tool | `SessionService.Restore()` |
| `aisync list` | List captured sessions | `SessionService.List()` |
| `aisync show <id>` | Show session details | `SessionService.Get()` |
| `aisync export` | Export session to file | `SessionService.Export()` |
| `aisync import` | Import session from file | `SessionService.Import()` |
| `aisync link` | Link session to git objects | `SessionService.Link()` |
| `aisync comment` | Post session summary on PR | `SessionService` + `Platform` |
| `aisync search` | Search sessions | `SessionService.Search()` |
| `aisync blame <file>` | Find AI sessions that touched a file | `SessionService.Blame()` |
| `aisync stats` | Show usage statistics | `SessionService.Stats()` |
| `aisync config` | View or edit configuration | — |
| `aisync push/pull/sync` | Team session sharing | `SyncService` |
| `aisync hooks` | Manage git hooks | — |
| `aisync secrets` | Secret detection | — |
| `aisync tui` | Interactive terminal UI | `SessionService` |
| `aisync serve` | Start API server | All services |

### `aisync serve` (New)

Start the HTTP/REST API server:

```bash
aisync serve                         # Start on default port (8080)
aisync serve --port 9090             # Custom port
aisync serve --host 0.0.0.0         # Listen on all interfaces (team use)
```

### Existing Commands (Unchanged)

```bash
# Capture
aisync capture                       # Auto-detect provider, capture current session
aisync capture --provider claude-code  # Force a specific provider
aisync capture --mode full           # Override storage mode

# Restore
aisync restore                       # Restore latest session for current branch
aisync restore --session a1b2c3d4    # Restore a specific session
aisync restore --provider opencode   # Force restore into a specific provider
aisync restore --as-context          # Generate CONTEXT.md instead of native import

# Query
aisync list                          # Sessions for current branch
aisync list --all                    # All sessions in this project
aisync show a1b2c3d4                 # By session ID
aisync show --files                  # Show files changed in session

# Export/Import
aisync export --format claude -o session.jsonl
aisync import session.json --into opencode

# Team
aisync push                          # Push sessions to git sync branch
aisync pull                          # Pull sessions from git sync branch

# Git hooks
aisync hooks install                 # Install pre-commit + commit-msg + post-checkout
aisync hooks uninstall               # Remove aisync hooks

# Stats
aisync stats                         # Token usage, session counts
aisync stats --json                  # JSON output

# TUI
aisync tui                           # Interactive terminal UI
```
