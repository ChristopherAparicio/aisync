# Contributing to aisync

## Prerequisites

- **Go 1.22+** (we use modern Go features: range-over-func, etc.)
- **Git 2.30+** (for trailer support, git notes)
- **SQLite** (via `modernc.org/sqlite` — pure Go, no CGO required)
- **GoReleaser** (for building releases locally — optional for dev)

## Getting Started

```bash
git clone https://github.com/<org>/aisync.git
cd aisync
make build        # Build the binary
make test         # Run all tests
make lint         # Run golangci-lint
make install      # Install to $GOPATH/bin
```

---

## Project Structure

We follow the **gh (GitHub CLI) patterns** with an idiomatic Go CLI architecture.

```
aisync/
  cmd/aisync/main.go              # Entry point (trivial, just calls root command)

  internal/                        # Private packages (Go compiler-enforced)
    session/                       # SHARED TYPES — structs, enums, errors (no interfaces)
    config/                        # Config (concrete struct)
    provider/                      # Provider interface + implementations (claude/, opencode/, cursor/)
    storage/                       # Store interface + implementations (sqlite/)
    capture/                       # Capture orchestration service
    restore/                       # Restore orchestration service
    gitsync/                       # Git branch-based session sync
    converter/                     # Cross-provider format conversion (concrete)
    secrets/                       # Secret detection & masking (concrete)
    platform/                      # Code hosting platform integration
    hooks/                         # Git hooks management
    tui/                           # Terminal UI

  pkg/                             # Semi-public packages (CLI layer)
    cmd/                           # CLI commands (1 subpackage per command)
    cmdutil/                       # Factory struct, shared command utilities
    iostreams/                     # I/O abstraction (stdout, stderr, colors)

  git/                             # Git CLI wrapper
```

### Key Principles

1. **`internal/session/`** is the shared vocabulary. It contains only types, enums, and errors — **no interfaces, no business logic**. Every other package imports it.

2. **Interfaces where they earn their keep.** Only 2 interfaces exist: `Provider` (in `provider/`, 3 implementations) and `Store` (in `storage/`, extensibility point). Everything else is concrete.

3. **One subpackage per CLI command.** Each command in `pkg/cmd/` gets its own package with its own `NewCmd*()` function and `*Options` struct.

4. **Factory pattern for DI.** All dependencies are wired in `pkg/cmd/factory/default.go`. Commands receive what they need via the `Factory` struct.

---

## Design Principles — KISS + SOLID

We apply **KISS** (Keep It Simple, Stupid) and **SOLID** rigorously. When in doubt, choose the simpler solution.

### KISS — Simplicity Rules

| Rule | In practice | Anti-pattern to avoid |
|------|------------|----------------------|
| **No premature abstraction** | Don't create an interface until you have 2+ implementations. | Creating `SessionRepository`, `SessionRepositoryImpl`, `SessionRepositoryFactory` when you only have SQLite |
| **Flat is better than nested** | A function with 3 clear steps beats a chain of 5 helper functions | `captureSession()` calling `prepareCaptureContext()` calling `buildCaptureRequest()` calling `validateCaptureInput()` |
| **One file, one responsibility** | If a file grows past ~300 lines, split it. But don't split a 50-line file into 3. | 15 files with 20 lines each in the same package |
| **Obvious code > clever code** | A simple `for` loop beats a generic functional pipeline | `Map(Filter(sessions, isActive), toSummary)` when a for loop is 5 lines |
| **Start concrete, abstract later** | Write the code first. Extract patterns only when you see repetition. | Designing a plugin architecture before writing the first provider |
| **Minimal public API** | Unexport by default. Only export what other packages actually need. | Exporting every struct field and method "just in case" |
| **No dead code** | Don't commit commented-out code, unused functions, or "for later" features. The linter catches `unused` — respect it. | `// TODO: might need this later` with 30 lines of commented code |

### SOLID — Applied to Go

**S — Single Responsibility**

Each package has ONE reason to change:

```
internal/provider/claude/   → changes only when Claude Code format changes
internal/storage/sqlite/    → changes only when DB schema/queries change
internal/secrets/           → changes only when secret detection logic changes
internal/capture/           → changes only when capture workflow changes
```

A function does ONE thing:

```go
// GOOD: one clear job
func (s *Store) Save(sess *session.Session) error { ... }

// BAD: doing too much
func (s *Store) SaveAndNotifyAndSync(sess *session.Session) error { ... }
```

**O — Open/Closed**

Adding a new provider never modifies existing providers or the capture service:

```go
// The capture service works with ANY provider via the interface.
// Adding OpenCode doesn't touch claude/ or capture/.
type Provider interface {
    Name() ProviderName
    Detect(projectPath string, branch string) ([]Summary, error)
    Export(sessionID ID, mode StorageMode) (*Session, error)
    CanImport() bool
    Import(session *Session) error
}
```

**L — Liskov Substitution**

Every `Provider` implementation must be interchangeable. If Cursor can't import, it still implements `Provider` — `CanImport()` returns `false` and `Import()` returns an error. The caller checks `CanImport()` first.

```go
// DO: respect the contract
func (c *CursorProvider) CanImport() bool { return false }
func (c *CursorProvider) Import(sess *session.Session) error {
    return fmt.Errorf("cursor does not support session import")
}

// DON'T: panic or silently no-op
func (c *CursorProvider) Import(sess *session.Session) error {
    return nil  // BAD — caller thinks it worked
}
```

**I — Interface Segregation**

Keep interfaces small. If a consumer only needs read access, don't force it to depend on write methods:

```go
// Full provider (Claude Code, OpenCode)
type Provider interface {
    Name() ProviderName
    Detect(projectPath string, branch string) ([]Summary, error)
    Export(sessionID ID, mode StorageMode) (*Session, error)
    CanImport() bool
    Import(session *Session) error
}

// Read-only consumer only needs this subset
type ReadOnlyProvider interface {
    Name() ProviderName
    Detect(projectPath string, branch string) ([]Summary, error)
    Export(sessionID ID, mode StorageMode) (*Session, error)
}
```

**D — Dependency Inversion**

High-level modules depend on abstractions **where warranted**, and on concrete types where there's only one implementation:

```go
// capture/service.go depends on storage.Store (interface — multiple impls possible)
// but uses *secrets.Scanner directly (concrete — only one implementation).
type Service struct {
    registry *provider.Registry
    store    storage.Store       // interface — abstracts SQLite, future backends
    scanner  *secrets.Scanner    // concrete — no need for abstraction
}

// Wiring happens in the factory (composition root), nowhere else.
```

### Decision Checklist

Before writing code, ask yourself:

- [ ] **Can I explain what this function does in one sentence?** If not, split it.
- [ ] **Does this package have one clear responsibility?** If not, restructure.
- [ ] **Am I adding an abstraction because I need it NOW or "just in case"?** If just in case, don't.
- [ ] **Would a new team member understand this code in 5 minutes?** If not, simplify.
- [ ] **Am I duplicating code?** If yes across 3+ places, extract. If only 2, it's OK to duplicate.
- [ ] **Does this function take more than 3-4 parameters?** If yes, consider a struct.
- [ ] **Is this function longer than ~40 lines?** If yes, consider splitting (but only if the split is natural).

---

## Session Types — Avoiding Primitive Obsession

The `internal/session/` package uses **typed constants with validation** (Level 2 pattern) instead of raw strings/ints. This catches bugs at boundaries (CLI input, JSON deserialization, DB reads) rather than deep in business logic.

### Pattern: String-Based Enum with Parse/Validate

Every "enum-like" concept follows this template:

```go
// ── Type definition ──

type ProviderName string

// ── Known values (exhaustive) ──

const (
    ProviderClaudeCode ProviderName = "claude-code"
    ProviderOpenCode   ProviderName = "opencode"
    ProviderCursor     ProviderName = "cursor"
)

// ── Validation ──

// allProviders is the single source of truth for valid values.
var allProviders = []ProviderName{
    ProviderClaudeCode,
    ProviderOpenCode,
    ProviderCursor,
}

func (p ProviderName) Valid() bool {
    for _, v := range allProviders {
        if p == v {
            return true
        }
    }
    return false
}

// ── Factory (boundary entry point) ──

// ParseProviderName converts a raw string to a validated ProviderName.
// This is the ONLY way external code should create a ProviderName.
func ParseProviderName(s string) (ProviderName, error) {
    p := ProviderName(strings.ToLower(strings.TrimSpace(s)))
    if !p.Valid() {
        return "", fmt.Errorf("unknown provider %q: valid values are %v", s, allProviders)
    }
    return p, nil
}

// ── Stringer (for display/logging) ──

func (p ProviderName) String() string {
    return string(p)
}
```

### Rules

| Rule | Why |
|------|-----|
| **Never cast raw strings** directly: `ProviderName("foo")` | Use `ParseProviderName("foo")` at boundaries instead |
| **Use constants** inside the codebase: `ProviderClaudeCode` | Compile-time safety, IDE autocomplete |
| **Validate at boundaries** (CLI flags, JSON unmarshal, DB reads) | Errors surface early with clear messages |
| **`Valid()` uses a slice, not a switch** | Single source of truth, easy to extend |
| **`allProviders` is unexported** | Consumers use `Valid()` or `Parse()`, not the raw list |

### Complete Type Catalog

All domain types that MUST use this pattern (not raw primitives):

```go
// ── Provider identification ──

type ProviderName string  // "claude-code", "opencode", "cursor"

const (
    ProviderClaudeCode ProviderName = "claude-code"
    ProviderOpenCode   ProviderName = "opencode"
    ProviderCursor     ProviderName = "cursor"
)

// ── Storage mode ──

type StorageMode string  // How much of the session to store

const (
    StorageModeFull    StorageMode = "full"     // Everything: messages, tools, thinking
    StorageModeCompact StorageMode = "compact"  // Messages only (no tools/thinking)
    StorageModeSummary StorageMode = "summary"  // Summary + file list only
)

// ── Secret handling ──

type SecretMode string  // What to do when a secret is detected

const (
    SecretModeMask  SecretMode = "mask"   // Replace with ***REDACTED:TYPE***
    SecretModeWarn  SecretMode = "warn"   // Store as-is, print warning
    SecretModeBlock SecretMode = "block"  // Refuse to capture
)

// ── Message roles ──

type MessageRole string

const (
    RoleUser      MessageRole = "user"
    RoleAssistant MessageRole = "assistant"
    RoleSystem    MessageRole = "system"
)

// ── File change types ──

type ChangeType string

const (
    ChangeCreated  ChangeType = "created"
    ChangeModified ChangeType = "modified"
    ChangeDeleted  ChangeType = "deleted"
    ChangeRead     ChangeType = "read"
)

// ── Link types (session ↔ git object) ──

type LinkType string

const (
    LinkBranch LinkType = "branch"
    LinkCommit LinkType = "commit"
    LinkPR     LinkType = "pr"
)

// ── Tool call state (lifecycle) ──

type ToolState string

const (
    ToolStatePending   ToolState = "pending"
    ToolStateRunning   ToolState = "running"
    ToolStateCompleted ToolState = "completed"
    ToolStateError     ToolState = "error"
)

// ── Session ID (wrapped for type safety) ──

type ID string

func NewID() ID {
    return ID(uuid.New().String())
}

func ParseID(s string) (ID, error) {
    if s == "" {
        return "", fmt.Errorf("session ID cannot be empty")
    }
    // Accept any non-empty string (UUIDs, provider-native IDs, etc.)
    return ID(s), nil
}

func (id ID) String() string {
    return string(id)
}
```

### JSON Marshaling

The string-based types marshal/unmarshal to JSON naturally:

```go
type Session struct {
    ID          ID           `json:"id"`
    Provider    ProviderName `json:"provider"`
    StorageMode StorageMode  `json:"storage_mode"`
    // ...
}

// No custom MarshalJSON needed — Go handles it:
// {"id": "a1b2c3d4", "provider": "claude-code", "storage_mode": "compact"}
```

### SQLite Storage

Same benefit — values are stored as readable strings in the DB:

```sql
SELECT * FROM sessions WHERE provider = 'claude-code';
-- No need for int → string lookup tables
```

### Validation at Boundaries

**CLI flags** (in `pkg/cmd/`):
```go
func runCapture(opts *CaptureOptions) error {
    mode, err := session.ParseStorageMode(opts.ModeFlag)
    if err != nil {
        return err  // "unknown storage mode "foo": valid values are [full compact summary]"
    }
    // From here on, mode is guaranteed valid
    sess, err := captureService.Capture(mode)
    // ...
}
```

**JSON deserialization** (in providers):
```go
func (p *ClaudeProvider) Export(sessionID session.ID, mode session.StorageMode) (*session.Session, error) {
    var raw rawClaudeSession
    if err := json.Unmarshal(data, &raw); err != nil {
        return nil, err
    }

    // Validate at the boundary
    prov, err := session.ParseProviderName(raw.Provider)
    if err != nil {
        return nil, fmt.Errorf("invalid session data: %w", err)
    }

    return &session.Session{
        Provider: prov,  // Safe from here
        // ...
    }, nil
}
```

**Database reads** (in `storage/sqlite/`):
```go
func (s *Store) Get(id session.ID) (*session.Session, error) {
    row := s.db.QueryRow("SELECT provider, storage_mode, ... FROM sessions WHERE id = ?", id)

    var providerStr, modeStr string
    row.Scan(&providerStr, &modeStr)

    prov, err := session.ParseProviderName(providerStr)
    if err != nil {
        return nil, fmt.Errorf("corrupt data for session %s: %w", id, err)
    }

    mode, err := session.ParseStorageMode(modeStr)
    if err != nil {
        return nil, fmt.Errorf("corrupt data for session %s: %w", id, err)
    }

    return &session.Session{Provider: prov, StorageMode: mode}, nil
}
```

### What NOT to Wrap

Not every string needs a custom type. Use raw primitives for:

| Field | Type | Why no wrapper |
|-------|------|---------------|
| `summary` | `string` | Free-form text, no validation needed |
| `content` | `string` | Message content, arbitrary |
| `project_path` | `string` | File path, validated by OS |
| `commit_sha` | `string` | Git SHA, validated by git operations |
| `branch` | `string` | Branch name, any string is valid in git |
| `model` | `string` | LLM model name, too many to enumerate |
| `exported_by` | `string` | Username, free-form |
| `message_count` | `int` | Simple counter |
| `total_tokens` | `int` | Simple counter |

**Rule of thumb:** wrap it if the value comes from a **closed set** (enum). Leave it raw if it's **open-ended** (free text, paths, counters).

---

## Code Conventions

### Go Style

We follow the standard Go conventions:

- **[Effective Go](https://go.dev/doc/effective_go)**
- **[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)**
- **[Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)** (as a reference, not a strict requirement)

### Naming

| What | Convention | Example |
|------|-----------|---------|
| Packages | Short, lowercase, no underscores | `provider`, `claude`, `sqlite` |
| Interfaces | Descriptive noun or `-er` suffix | `Provider`, `Store`, `Platform` |
| Structs | Descriptive noun | `Session`, `CaptureService`, `SQLiteStore` |
| Constructors | `New` + type name | `NewCaptureService()`, `NewSQLiteStore()` |
| Test files | `*_test.go` in same package | `claude_test.go` |
| Test functions | `Test` + function name | `TestClaudeProvider_Detect` |
| Errors | `Err` prefix for sentinel, wrap with `%w` | `ErrSessionNotFound`, `fmt.Errorf("...: %w", err)` |

### Error Handling

```go
// DO: Use sentinel errors for expected cases
var ErrSessionNotFound = errors.New("session not found")

// DO: Wrap errors with context
if err != nil {
    return fmt.Errorf("failed to read session %s: %w", id, err)
}

// DO: Check for specific errors
if errors.Is(err, ErrSessionNotFound) {
    // handle
}

// DON'T: Discard errors silently
_ = file.Close()  // BAD — log or handle the error
```

### Interfaces

```go
// DO: Keep interfaces small (3-5 methods)
// DO: Use session types, not primitives (ProviderName, ID, StorageMode...)
type Provider interface {
    Name() session.ProviderName
    Detect(projectPath string, branch string) ([]session.Summary, error)
    Export(sessionID session.ID, mode session.StorageMode) (*session.Session, error)
    CanImport() bool
    Import(session *session.Session) error
}

// DO: Define interfaces in the package that owns the abstraction
//     Provider → internal/provider/    Store → internal/storage/    Platform → internal/platform/
// DON'T: Define interfaces in a central "domain" package

// DO: Use interface composition for read-only providers (e.g. Cursor)
type ReadOnlyProvider interface {
    Name() session.ProviderName
    Detect(projectPath string, branch string) ([]session.Summary, error)
    Export(sessionID session.ID, mode session.StorageMode) (*session.Session, error)
}
```

### Dependency Injection

We use **interfaces where they earn their keep** and **concrete types everywhere else**:

```go
// DO: Accept interfaces for things with multiple implementations
func NewService(registry *provider.Registry, store storage.Store) *Service {
    return &Service{registry: registry, store: store}
}

// DO: Accept concrete types when there's only one implementation
func NewServiceWithScanner(registry *provider.Registry, store storage.Store, scanner *secrets.Scanner) *Service {
    return &Service{registry: registry, store: store, scanner: scanner}
}

// DON'T: Create dependencies inside constructors
func NewService() *Service {
    store := sqlite.NewStore("~/.aisync/sessions.db")  // BAD
    return &Service{store: store}
}
```

---

## Testing

### Framework

We use Go's standard `testing` package exclusively. No external test frameworks (testify, gomega, etc.).

### Table-Driven Tests

**This is our primary testing pattern.** Use it for any function with multiple input/output scenarios.

```go
func TestMaskSecrets(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        patterns []Pattern
        want     string
    }{
        {
            name:     "masks AWS access key",
            input:    "my key is AKIAIOSFODNN7EXAMPLE",
            patterns: DefaultPatterns(),
            want:     "my key is ***REDACTED:AWS_ACCESS_KEY***",
        },
        {
            name:     "masks GitHub token",
            input:    "token: ghp_ABCDEFghijklmnop1234567890abcdef",
            patterns: DefaultPatterns(),
            want:     "token: ***REDACTED:GITHUB_TOKEN***",
        },
        {
            name:     "no secrets found",
            input:    "just regular text",
            patterns: DefaultPatterns(),
            want:     "just regular text",
        },
        {
            name:     "multiple secrets in one string",
            input:    "key=AKIAIOSFODNN7EXAMPLE token=ghp_ABC123def456",
            patterns: DefaultPatterns(),
            want:     "key=***REDACTED:AWS_ACCESS_KEY*** token=***REDACTED:GITHUB_TOKEN***",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := MaskSecrets(tt.input, tt.patterns)
            if got != tt.want {
                t.Errorf("MaskSecrets() = %q, want %q", got, tt.want)
            }
        })
    }
}
```

### Subtests for Complex Scenarios

```go
func TestCaptureService(t *testing.T) {
    t.Run("with active Claude Code session", func(t *testing.T) {
        // setup, test, assert
    })

    t.Run("with no active sessions", func(t *testing.T) {
        // setup, test, assert
    })

    t.Run("with multiple providers", func(t *testing.T) {
        // setup, test, assert
    })
}
```

### Test Helpers

```go
// testutil/helpers.go — shared test utilities

// MustCreateTempDB creates a temporary SQLite database for testing.
// It returns the store and a cleanup function.
func MustCreateTempDB(t *testing.T) (*sqlite.Store, func()) {
    t.Helper()
    // ...
}

// FixturePath returns the path to a test fixture file.
func FixturePath(t *testing.T, name string) string {
    t.Helper()
    return filepath.Join("testdata", name)
}
```

### Test Fixtures

Store real (anonymized) session data in `testdata/` directories:

```
internal/provider/claude/testdata/
    session_simple.jsonl       # Simple Claude Code session (5 messages)
    session_complex.jsonl      # Complex session with tool calls + thinking
    session_empty.jsonl        # Edge case: empty session
    session_with_secrets.jsonl # Session containing secrets to detect

internal/provider/opencode/testdata/
    session_simple.json
    session_complex.json
```

### Mocks

For the 3 interfaces (`Store`, `Provider`, `Platform`), create manual mocks in the test file or a shared mock package:

```go
// mock store — defined in test files or a shared mock package
type MockStore struct {
    SaveFunc   func(sess *session.Session) error
    GetFunc    func(id session.ID) (*session.Session, error)
    ListFunc   func(opts session.ListOptions) ([]*session.Session, error)
}

func (m *MockStore) Save(sess *session.Session) error {
    return m.SaveFunc(sess)
}

// Usage in tests:
store := &MockStore{
    SaveFunc: func(sess *session.Session) error {
        return nil
    },
}
```

### Running Tests

```bash
make test                     # All tests
make test-verbose             # Verbose output
go test ./internal/provider/claude/...  # Specific package
go test -run TestMaskSecrets ./internal/secrets/  # Specific test
go test -race ./...           # Race detector
go test -count=1 ./...        # No test caching
```

---

## Git Workflow

### Branches

- `main` — stable, always passes CI
- `feature/<name>` — feature branches
- `fix/<name>` — bugfix branches
- `aisync/sessions` — reserved for session storage (Phase 2)

### Commits

We use **conventional commits**:

```
feat: add Claude Code provider with JSONL parsing
fix: handle empty session files gracefully
refactor: extract provider detection into registry
test: add table-driven tests for secret masking
docs: update roadmap with Phase 2 milestones
chore: configure GoReleaser for cross-platform builds
```

Format: `<type>: <description>`

Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`, `perf`, `ci`

### Pull Requests

- PRs should be focused (one feature or fix per PR)
- Tests are required for new functionality
- Run `make lint` and `make test` before submitting

---

## CLI Command Pattern

Every command follows this structure (inspired by gh):

```go
// pkg/cmd/capture/capture.go

package capture

import (
    "github.com/<org>/aisync/pkg/cmdutil"
    "github.com/spf13/cobra"
)

// CaptureOptions holds all inputs for the capture command.
type CaptureOptions struct {
    IO       *iostreams.IOStreams
    Store    storage.Store
    Registry *provider.Registry

    // Raw flag values (strings — validated in RunE before calling runCapture)
    ProviderFlag string
    ModeFlag     string
    Message      string
    Auto         bool
}

// NewCmdCapture creates the `aisync capture` command.
func NewCmdCapture(f *cmdutil.Factory) *cobra.Command {
    opts := &CaptureOptions{}

    cmd := &cobra.Command{
        Use:   "capture",
        Short: "Capture the active AI session",
        Long:  "Captures the currently active AI session and stores it linked to the current branch.",
        RunE: func(cmd *cobra.Command, args []string) error {
            // Resolve lazy dependencies from factory
            opts.IO = f.IOStreams
            opts.Store = f.Store()
            opts.Registry = f.Registry()
            return runCapture(opts)
        },
    }

    cmd.Flags().StringVar(&opts.ProviderFlag, "provider", "", "Force a specific provider (claude-code, opencode)")
    cmd.Flags().StringVar(&opts.ModeFlag, "mode", "", "Storage mode: full, compact, summary")
    cmd.Flags().StringVar(&opts.Message, "message", "", "Manual summary message")
    cmd.Flags().BoolVar(&opts.Auto, "auto", false, "Auto mode (used by git hooks)")

    return cmd
}

// runCapture contains the actual business logic.
// At this point, all inputs are validated domain types.
func runCapture(opts *CaptureOptions) error {
    // Parse & validate flags at the boundary
    var providerFilter *session.ProviderName
    if opts.ProviderFlag != "" {
        p, err := session.ParseProviderName(opts.ProviderFlag)
        if err != nil {
            return err  // Clear error: "unknown provider "foo": valid values are [...]"
        }
        providerFilter = &p
    }

    mode := session.StorageModeFull // ... get default from config
    if opts.ModeFlag != "" {
        m, err := session.ParseStorageMode(opts.ModeFlag)
        if err != nil {
            return err
        }
        mode = m
    }

    // From here on, only validated domain types are used
    _ = providerFilter
    _ = mode
    return nil
}
```

---

## Linting

We use **[golangci-lint](https://golangci-lint.run/)** — the standard Go meta-linter. It runs multiple linters in parallel and is used by gh, Kubernetes, Docker, Hugo, etc.

### Installation

```bash
# macOS
brew install golangci-lint

# or via Go (any platform)
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Verify
golangci-lint --version
```

### Configuration (`.golangci.yml`)

This is the actual config file at the root of the repo:

```yaml
# .golangci.yml
run:
  timeout: 3m
  go: "1.22"

linters:
  # Disable all defaults, enable only what we want (explicit > implicit)
  disable-all: true
  enable:
    # ── Bug detection ──
    - govet          # Reports suspicious constructs (printf args, struct tags, etc.)
    - errcheck       # Checks that errors are handled, not silently discarded
    - staticcheck    # The gold standard static analyzer (SA*, S*, ST*, QF* checks)
    - ineffassign    # Detects assignments to variables that are never used after
    - typecheck      # Standard Go type checking

    # ── Code simplification ──
    - gosimple       # Suggests simpler code constructs (part of staticcheck suite)
    - unused         # Finds unused constants, variables, functions, types

    # ── Formatting ──
    - gofmt          # Checks code is gofmt'd
    - goimports      # Checks imports are sorted and grouped correctly

    # ── Style & quality ──
    - revive         # Configurable replacement for golint. Style + best practices.
    - misspell       # Catches common typos in comments, strings, variable names

linters-settings:
  revive:
    rules:
      - name: exported
        severity: warning
        arguments:
          - "checkPrivateReceivers"
      - name: blank-imports
      - name: context-as-argument
      - name: context-keys-type
      - name: error-return
      - name: error-strings
      - name: error-naming
      - name: increment-decrement
      - name: var-naming
      - name: range
      - name: receiver-naming
      - name: indent-error-flow     # Encourages early returns
      - name: unexported-return
      - name: superfluous-else      # if/else with return → just if + return
      - name: unreachable-code

  govet:
    enable-all: true

  errcheck:
    # Don't require checking errors from fmt.Print*, io.Close in defers
    exclude-functions:
      - fmt.Fprintf
      - fmt.Fprintln
      - fmt.Fprint

  goimports:
    # Enforce import grouping: stdlib | third-party | internal
    local-prefixes: github.com/aisync/aisync

issues:
  # Don't skip any issue categories
  exclude-use-default: false

  # But allow some known patterns
  exclude-rules:
    # Test files can have longer functions and unused params
    - path: _test\.go
      linters:
        - errcheck

    # Allow dot imports in test files (for test helpers if needed)
    - path: _test\.go
      text: "dot-imports"
```

### What Each Linter Catches

| Linter | Category | What it catches | KISS/SOLID link |
|--------|----------|----------------|-----------------|
| `govet` | Bugs | Printf format mismatches, struct tag errors, unreachable code | Correctness |
| `errcheck` | Bugs | Unhandled errors (`_ = f.Close()`) | Reliability |
| `staticcheck` | Bugs + Style | Deprecated API usage, useless assignments, impossible conditions | Correctness |
| `ineffassign` | Dead code | `x = 5` then `x = 10` without reading x | KISS (no dead code) |
| `unused` | Dead code | Functions, types, constants that nothing calls | KISS (no dead code) |
| `gosimple` | Simplification | `if x == true` → `if x`, `select { case <-ch: }` → `<-ch` | KISS |
| `gofmt` | Formatting | Non-standard formatting | Consistency |
| `goimports` | Formatting | Unsorted imports, missing import groups | Consistency |
| `revive` | Style | Unexported return types, bad naming, missing error returns | SOLID (contracts) |
| `misspell` | Quality | `recieve` → `receive`, `occured` → `occurred` | Professionalism |

### Workflow

The linter runs:

1. **Before every commit** (manually: `make lint`)
2. **In CI** (GitHub Actions, blocks merge on failure)
3. **In your editor** (optional but recommended — most Go editors support golangci-lint)

```bash
# Run all linters
make lint

# Run on a specific package
golangci-lint run ./internal/provider/claude/...

# Auto-fix what can be fixed (gofmt, goimports)
make fmt

# See what a specific linter would report
golangci-lint run --enable-only govet ./...
```

---

## Makefile Targets

```makefile
build         # Build the aisync binary
test          # Run all tests
test-verbose  # Run tests with verbose output
test-race     # Run tests with race detector
lint          # Run golangci-lint (must pass before commit)
fmt           # Auto-format: gofmt + goimports
vet           # Run go vet only (fast check)
install       # Install to $GOPATH/bin
clean         # Remove build artifacts
release-dry   # GoReleaser dry run (no publish)
```

---

## Adding a New Provider

1. Create `internal/provider/<name>/<name>.go`
2. Implement the `provider.Provider` interface (defined in `internal/provider/provider.go`)
3. Add test fixtures in `internal/provider/<name>/testdata/`
4. Write table-driven tests in `internal/provider/<name>/<name>_test.go`
5. Register the provider in `internal/provider/registry.go`
6. Add to the default config's provider list

## Adding a New CLI Command

1. Create `pkg/cmd/<name>/<name>.go`
2. Define `<Name>Options` struct with all inputs
3. Define `NewCmd<Name>(f *cmdutil.Factory) *cobra.Command`
4. Define `run<Name>(opts *<Name>Options) error` with business logic
5. Wire the command in `pkg/cmd/root/root.go`
6. Add tests in `pkg/cmd/<name>/<name>_test.go`

## Adding a New Interface

Before adding a new interface, ask: **do I have 2+ implementations, or a clear extensibility need?** If not, use a concrete type.

If you do need an interface:

1. Define the interface **in the package that owns the abstraction** (e.g., `internal/storage/store.go`, NOT in `internal/session/`)
2. Keep it small (3-5 methods max) — follow the Interface Segregation principle
3. Create a manual mock in test files for consumers that depend on it
4. Implement in the appropriate `internal/` subpackage
5. Wire via the Factory in `pkg/cmd/factory/default.go`
