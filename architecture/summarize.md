# AI Summarization, Explain, Resume & Rewind — Feature Architecture

> Phase 5.0 — Transform aisync from capture/restore into session intelligence.

## What

Four capabilities that leverage an LLM to add intelligence on top of captured sessions:

| Capability | Command | Description |
|-----------|---------|-------------|
| **Summarize** | `aisync capture --summarize` | AI-generated structured summary at capture time |
| **Explain** | `aisync explain <id>` | On-demand natural language explanation of a session |
| **Resume** | `aisync resume <branch>` | Convenience: `git checkout` + `aisync restore` in one step |
| **Rewind** | `aisync rewind <id>` | Fork a session at message N, restore the truncated version |

Use cases:

1. **Better summaries** — Provider-native summaries are generic ("Worked on auth"). AI summarization produces structured output: intent, outcome, decisions, friction, open items.
2. **Onboarding** — `aisync explain` lets a colleague understand a session without reading the full conversation.
3. **Quick resume** — `aisync resume feat/auth` switches branch + restores context in one command.
4. **Retry from checkpoint** — `aisync rewind` lets you go back to a point in the conversation where things were still good, discarding bad turns.

## Architecture

### New Packages

```
internal/
  llm/                          # LLM client port + adapters
    client.go                   #   LLMClient interface
    claude/                     #   Claude CLI adapter (calls `claude` binary)
      claude.go
      claude_test.go

pkg/cmd/
  explaincmd/explaincmd.go      # aisync explain
  resumecmd/resumecmd.go        # aisync resume
  rewindcmd/rewindcmd.go        # aisync rewind
```

### Layer Responsibilities

| Layer | What it does |
|-------|-------------|
| **Domain** | `StructuredSummary` type (intent, outcome, decisions, friction, open items) |
| **Port** | `LLMClient` interface — `Complete(ctx, prompt) → string` |
| **Adapter** | `llm/claude/` — wraps `claude` CLI binary |
| **Service** | `SessionService.Summarize()`, `.Explain()` — orchestrate LLM calls |
| **Capture** | Hooks into capture flow — auto-summarize after export, before store |
| **CLI** | `aisync explain`, `aisync resume`, `aisync rewind` commands |
| **API** | `POST /api/v1/sessions/explain`, `POST /api/v1/sessions/rewind` |
| **MCP** | `aisync_explain`, `aisync_rewind` tools |
| **Config** | `summarize.enabled`, `summarize.model` settings |

---

## Design

### 1. LLM Client Port

```go
// internal/llm/client.go

// LLMClient is the port for language model interactions.
// Current adapter: claude/ (Claude CLI). Future: openai/, ollama/.
type LLMClient interface {
    // Complete sends a prompt and returns the model's response.
    Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}

type CompletionRequest struct {
    SystemPrompt string
    UserPrompt   string
    Model        string // optional — adapter picks default
    MaxTokens    int    // optional — adapter picks default
}

type CompletionResponse struct {
    Content    string
    Model      string // actual model used
    InputTokens  int
    OutputTokens int
}
```

**Why an interface:** Decision D34. Allows swapping Claude CLI for OpenAI API, Ollama, or any other backend without touching services. Enables testing with a mock LLM.

### 2. Claude CLI Adapter

```go
// internal/llm/claude/claude.go

// Client calls the `claude` CLI binary for completions.
// Requires `claude` in PATH (already required by Claude Code provider).
type Client struct {
    binaryPath string // default: "claude"
    model      string // default: "" (let claude pick)
}

func New(opts ...Option) *Client
func (c *Client) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error)
```

Implementation: pipes `UserPrompt` to `claude --print --output-format json` via stdin. Parses JSON response. The `--print` flag makes Claude non-interactive (single-turn, no conversation).

Command: `echo "<prompt>" | claude --print --output-format json [--model <model>] [--system-prompt "<system>"]`

### 3. Domain Types

```go
// internal/session/session.go — additions

// StructuredSummary is an AI-generated structured analysis of a session.
type StructuredSummary struct {
    Intent    string   `json:"intent"`     // what the user was trying to do
    Outcome   string   `json:"outcome"`    // what was achieved
    Decisions []string `json:"decisions"`  // key decisions made
    Friction  []string `json:"friction"`   // problems encountered
    OpenItems []string `json:"open_items"` // things left undone
}
```

The `Session.Summary` field (string) stores a one-line summary for display. The `StructuredSummary` is stored in `Session.Metadata` (JSON) for rich access.

### 4. Summarize Flow (at capture time)

```
aisync capture [--summarize]
       │
       ├── provider.Export() → session with messages
       │
       ├── [if --message] → manual summary override, skip AI
       │
       ├── [if summarize.enabled OR --summarize flag]
       │     │
       │     ├── Build prompt from session messages (truncate to fit context)
       │     ├── llm.Complete(systemPrompt, userPrompt)
       │     ├── Parse StructuredSummary from JSON response
       │     ├── session.Summary = summary.Intent + ": " + summary.Outcome
       │     └── session.Metadata["structured_summary"] = summary
       │     │
       │     └── On failure: log warning, keep provider-native summary
       │
       └── store.Save(session)
```

**Non-blocking (D27):** If the LLM call fails (binary not found, timeout, error), capture proceeds with the provider-native summary. The user sees a warning but the session is still saved.

**Priority:** `--message` > AI summary > provider-native summary.

### 5. Explain Flow (on demand)

```
aisync explain <session-id>
       │
       ├── store.Get(sessionID) → full session with messages
       │
       ├── Build explanation prompt (different from summarize — more verbose)
       │
       ├── llm.Complete(systemPrompt, userPrompt)
       │
       └── Print explanation to stdout
```

`aisync explain` does NOT store the result — it's ephemeral, printed to stdout. If the user wants to persist, they can redirect to a file.

Flags: `--short` (brief), `--detailed` (include tool calls), `--json` (structured output).

### 6. Resume Flow

```
aisync resume <branch>
       │
       ├── git.Checkout(branch)
       │
       ├── service.Restore(RestoreRequest{Branch: branch, ...})
       │
       └── Print "Resumed on <branch>, session <id> restored"
```

This is a **thin convenience wrapper** (D29). No new service method needed — just a CLI command that composes `git.Checkout()` + `service.Restore()`.

Flags: `--session <id>`, `--provider`, `--as-context`.

### 7. Rewind Flow

```
aisync rewind <session-id> [--message N]
       │
       ├── store.Get(sessionID) → full session
       │
       ├── [if --message N] → truncate at message N
       │   [else] → interactive: show numbered messages, user picks
       │
       ├── Create new session:
       │     ├── ID = NewID()
       │     ├── Messages = original.Messages[0:N]
       │     ├── Summary = "Rewind of <original-id> at message <N>"
       │     ├── Metadata["rewind_of"] = original-id
       │     ├── Metadata["rewind_at_message"] = N
       │     ├── Same branch, provider, file changes (recalculated from truncated messages)
       │
       ├── store.Save(newSession)
       │
       ├── service.Restore(newSession.ID) → import into provider
       │
       └── Print "Rewound to message <N>, new session <new-id>"
```

**Original session is never modified** — rewind creates a fork. The relationship is tracked via metadata (not a new table yet — D28 says context-only rewind, avoid complexity).

---

## Prompts

### Summarize System Prompt

```
You are a technical session analyzer. Given an AI coding session transcript,
produce a structured JSON summary with these fields:
- intent: What the user was trying to accomplish (1 sentence)
- outcome: What was actually achieved (1 sentence)
- decisions: Key technical decisions made (array of short strings)
- friction: Problems or difficulties encountered (array of short strings)
- open_items: Things left unfinished or needing follow-up (array of short strings)

Respond ONLY with valid JSON, no markdown fences, no explanation.
```

### Explain System Prompt

```
You are a technical analyst. Given an AI coding session transcript,
write a clear explanation of what happened during this session.
Cover: what was the goal, what approach was taken, what files were changed,
what decisions were made and why, and what the outcome was.
Write for a developer who is taking over this branch.
```

---

## Config

```json
{
  "summarize": {
    "enabled": false,
    "model": ""
  }
}
```

- `summarize.enabled` — `true` to auto-summarize every capture. Default `false`.
- `summarize.model` — model name passed to `claude --model`. Empty = let Claude pick default.
- CLI override: `aisync capture --summarize` forces summarization even if config says disabled.

Config keys: `aisync config set summarize.enabled true`, `aisync config set summarize.model sonnet`.

---

## Implementation Order

Each step leaves the codebase green (all tests pass):

| Step | What | Files | Tests |
|------|------|-------|-------|
| **1** | `LLMClient` interface + `CompletionRequest/Response` types | `internal/llm/client.go` | — (types only) |
| **2** | Claude CLI adapter | `internal/llm/claude/claude.go` | Unit tests with mock binary |
| **3** | `StructuredSummary` domain type | `internal/session/session.go` | — (type only) |
| **4** | Config: `summarize.enabled`, `summarize.model` | `internal/config/config.go` | Config get/set tests |
| **5** | `SessionService.Summarize()` method | `internal/service/session.go` | Mock LLM test |
| **6** | Integrate summarize into capture flow | `internal/capture/service.go`, `internal/service/session.go` | Capture test with mock LLM |
| **7** | `aisync capture --summarize` flag | `pkg/cmd/capture/capture.go` | Flag test |
| **8** | `SessionService.Explain()` method | `internal/service/session.go` | Mock LLM test |
| **9** | `aisync explain` CLI + API + MCP | `pkg/cmd/explaincmd/`, `internal/api/`, `internal/mcp/` | Full vertical tests |
| **10** | `git.Client.Checkout()` method | `git/client.go` | Unit test |
| **11** | `aisync resume` CLI | `pkg/cmd/resumecmd/` | CLI test |
| **12** | `aisync rewind` CLI + service logic | `pkg/cmd/rewindcmd/`, `internal/service/` | Full tests |
| **13** | API + MCP for rewind | `internal/api/`, `internal/mcp/` | Integration tests |
| **14** | Docs: LLM.md, README.md, architecture/README.md, roadmap.md | Docs | — |

---

## Performance

### Summarize at Capture Time

| Concern | Strategy |
|---------|----------|
| **Large sessions (50k+ tokens)** | Truncate messages to fit model context. Keep first 3 + last 5 user messages + all file changes. |
| **LLM latency (2-10s)** | Non-blocking: failure logs warning, capture proceeds with native summary |
| **Claude CLI not installed** | Graceful fallback: log "claude binary not found, skipping summarization" |
| **Token cost** | Summarize prompt uses ~500 tokens input + ~200 output. At $3/M (Sonnet), ~$0.002 per capture. |

### Explain

| Concern | Strategy |
|---------|----------|
| **Large sessions** | Same truncation as summarize, but with higher budget (allow more context for detailed explanations) |
| **No caching** | Explain is ephemeral — not stored. If called twice, LLM runs twice. Acceptable for on-demand use. |

### Rewind

| Concern | Strategy |
|---------|----------|
| **No interactive mode in non-TTY** | `--message N` is required in non-interactive contexts. Interactive picker only when TTY detected. |
| **File changes recalculation** | Truncated session keeps original file changes (they come from provider, not derived from messages). |

---

## Contract

> **LLMClient is a port** — adapters (Claude CLI, future OpenAI, Ollama) are interchangeable.
> **Summarization is optional** — disabled by default, failure never blocks capture.
> **Resume is pure composition** — no new service method, just `git checkout` + `restore`.
> **Rewind creates a fork** — original session is never modified.
