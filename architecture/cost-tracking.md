# Phase 5.4 — Cost Tracking

> Last updated: 2026-03-04

## Goal

Transform token counts into real monetary costs ($) per session, per branch, per model. Users can answer: "How much did this feature cost me in AI usage?"

## What We Have Today

| Data | Where | Available? |
|------|-------|-----------|
| Total tokens (input/output/total) | `Session.TokenUsage` | Yes |
| Tokens per message | `Message.Tokens` | Yes (output tokens per assistant message) |
| Model per message | `Message.Model` | Yes — all 3 providers populate it |
| Cost ($) | — | **No — this is what we're building** |

Key insight: we have `Message.Model` (e.g., `"claude-sonnet-4-20250514"`) on every assistant message, and `Message.Tokens` with the output token count. This is enough to compute per-message costs.

## Design

### 1. Pricing Table — `internal/pricing/`

A new package with a **model pricing registry**. Ships with built-in defaults, overridable via config.

```go
// internal/pricing/pricing.go

// ModelPrice defines the cost per token for a model.
type ModelPrice struct {
    Model          string  // model identifier (e.g., "claude-sonnet-4-20250514")
    InputPerMToken  float64 // $ per 1M input tokens
    OutputPerMToken float64 // $ per 1M output tokens
}

// Calculator computes costs from token counts and model identifiers.
type Calculator struct {
    prices map[string]ModelPrice // key: model ID or normalized prefix
}

// NewCalculator creates a Calculator with built-in default prices.
func NewCalculator() *Calculator

// WithOverrides applies user-configured price overrides.
func (c *Calculator) WithOverrides(overrides []ModelPrice) *Calculator

// MessageCost computes the cost for a single message.
func (c *Calculator) MessageCost(model string, inputTokens, outputTokens int) Cost

// SessionCost computes the total cost for a session.
func (c *Calculator) SessionCost(sess *session.Session) *CostEstimate
```

**Model matching strategy:** Models are identified by string (e.g., `"claude-sonnet-4-20250514"`). The calculator uses **prefix matching** to handle dated variants:

1. Exact match: `"claude-sonnet-4-20250514"` → found
2. Prefix match: `"claude-sonnet-4-20250514"` → matches `"claude-sonnet-4"` entry
3. Not found: returns zero cost + marks model as "unknown" in result

This handles the common case where pricing is per model family, not per date variant.

### 2. Built-in Default Prices

Shipped as a Go map (no external file needed). Updated with new releases.

```go
var DefaultPrices = []ModelPrice{
    // Claude models (Anthropic)
    {Model: "claude-opus-4",        InputPerMToken: 15.0,  OutputPerMToken: 75.0},
    {Model: "claude-sonnet-4",      InputPerMToken: 3.0,   OutputPerMToken: 15.0},
    {Model: "claude-haiku-3.5",     InputPerMToken: 0.80,  OutputPerMToken: 4.0},

    // GPT models (OpenAI)
    {Model: "gpt-4o",              InputPerMToken: 2.50,  OutputPerMToken: 10.0},
    {Model: "gpt-4o-mini",         InputPerMToken: 0.15,  OutputPerMToken: 0.60},
    {Model: "gpt-4.1",            InputPerMToken: 2.0,   OutputPerMToken: 8.0},
    {Model: "gpt-4.1-mini",       InputPerMToken: 0.40,  OutputPerMToken: 1.60},
    {Model: "gpt-4.1-nano",       InputPerMToken: 0.10,  OutputPerMToken: 0.40},
    {Model: "o3",                  InputPerMToken: 2.0,   OutputPerMToken: 8.0},
    {Model: "o3-mini",             InputPerMToken: 1.10,  OutputPerMToken: 4.40},
    {Model: "o4-mini",             InputPerMToken: 1.10,  OutputPerMToken: 4.40},

    // Gemini models (Google)
    {Model: "gemini-2.5-pro",     InputPerMToken: 1.25,  OutputPerMToken: 10.0},
    {Model: "gemini-2.5-flash",   InputPerMToken: 0.15,  OutputPerMToken: 0.60},
    {Model: "gemini-2.0-flash",   InputPerMToken: 0.10,  OutputPerMToken: 0.40},
}
```

### 3. Domain Types — `internal/session/session.go`

New types added to the shared vocabulary:

```go
// Cost represents a monetary amount.
type Cost struct {
    InputCost  float64 `json:"input_cost"`
    OutputCost float64 `json:"output_cost"`
    TotalCost  float64 `json:"total_cost"`
    Currency   string  `json:"currency"` // always "USD"
}

// CostEstimate is the full cost breakdown for a session.
type CostEstimate struct {
    TotalCost     Cost              `json:"total_cost"`
    PerModel      []ModelCost       `json:"per_model"`
    UnknownModels []string          `json:"unknown_models,omitempty"` // models without pricing
}

// ModelCost groups cost by model.
type ModelCost struct {
    Model        string `json:"model"`
    InputTokens  int    `json:"input_tokens"`
    OutputTokens int    `json:"output_tokens"`
    Cost         Cost   `json:"cost"`
    MessageCount int    `json:"message_count"`
}
```

### 4. Service Method — `SessionService.EstimateCost()`

```go
// EstimateCost computes the cost breakdown for a session.
func (s *SessionService) EstimateCost(ctx context.Context, id session.ID) (*session.CostEstimate, error)
```

Logic:
1. `store.Get(id)` → load full session
2. Iterate `session.Messages` where `Role == assistant`
3. For each message: look up `Message.Model` in Calculator, compute cost from `Message.Tokens` (output) and estimated input tokens
4. Aggregate per-model, compute totals
5. Return `CostEstimate`

**Input tokens estimation:** Claude Code provides `TokenUsage.InputTokens` at session level but not per-message. Strategy:
- If `TokenUsage.InputTokens > 0` and `TokenUsage.OutputTokens > 0`, distribute input proportionally across messages by their output token share.
- If only `Message.Tokens` is available (output only), compute output cost exactly, input cost as estimate from session total.
- If no token data at all, return zero cost with a warning.

### 5. Config — Pricing Overrides

Add `pricing` section to `configData`:

```go
type pricing struct {
    Overrides []pricingOverride `json:"overrides"`
}

type pricingOverride struct {
    Model           string  `json:"model"`
    InputPerMToken  float64 `json:"input_per_mtoken"`
    OutputPerMToken float64 `json:"output_per_mtoken"`
}
```

Config keys:
- `aisync config set pricing.add <model> <input_price> <output_price>` — add/update a custom price
- `aisync config get pricing` — list all active prices (built-in + overrides)

### 6. Vertical Slice — All Adapters

| Layer | What | Details |
|-------|------|---------|
| **Domain** | `Cost`, `CostEstimate`, `ModelCost` | New types in `session/session.go` |
| **Service** | `EstimateCost(ctx, id)` | New method on `SessionService` |
| **Pricing** | `Calculator` | New package `internal/pricing/` |
| **CLI: show** | `--cost` flag | Shows cost breakdown after token info |
| **CLI: stats** | `--cost` flag | Adds cost column to branch table + total |
| **API** | `GET /api/v1/sessions/{id}/cost` | New endpoint |
| **MCP** | `aisync_cost` tool | New tool |
| **Client** | `Client.Cost(id)` | New method |
| **Config** | `pricing.overrides` | Optional custom pricing |

### 7. CLI Output — `aisync show <id> --cost`

```
Session:  abc123-def456
Provider: claude-code
Agent:    claude
Branch:   feature/auth
...
Tokens:   25,000 in / 8,500 out / 33,500 total

Cost Estimate:
  Model              Input $    Output $    Total $
  claude-sonnet-4    $0.075     $0.128      $0.203
  ──────────────────────────────────────────────────
  Total              $0.075     $0.128      $0.203
```

### 8. CLI Output — `aisync stats --cost`

```
=== Overall Statistics ===
  Sessions:  12
  Messages:  248
  Tokens:    485.2k
  Cost:      $3.42

=== By Branch ===
  BRANCH                          SESSIONS    TOKENS       COST
  feature/auth                           3    125.0k      $1.12
  fix/login-error                        2     85.3k      $0.74
  refactor/db-layer                      4    180.5k      $1.05
  ...
```

## Implementation Plan

| Step | What | Files |
|------|------|-------|
| 1 | Create `internal/pricing/` package | `pricing.go`, `pricing_test.go` |
| 2 | Add domain types (`Cost`, `CostEstimate`, `ModelCost`) | `session/session.go` |
| 3 | Add `EstimateCost()` to `SessionService` | `service/session.go` |
| 4 | Add `--cost` flag to `aisync show` | `pkg/cmd/show/show.go` |
| 5 | Add `--cost` flag to `aisync stats` | `pkg/cmd/statscmd/statscmd.go`, `service/session.go` (extend `StatsResult`) |
| 6 | Add API endpoint `GET /api/v1/sessions/{id}/cost` | `api/handlers.go`, `api/routes.go` |
| 7 | Add MCP tool `aisync_cost` | `mcp/tools.go`, `mcp/server.go` |
| 8 | Add client method `Client.Cost(id)` | `client/sessions.go` |
| 9 | Add config pricing overrides | `config/config.go` |
| 10 | Tests + agent review | All test files |

## Performance Notes

- Cost calculation is **pure computation** — no DB changes, no new tables, no migrations.
- `EstimateCost()` loads the full session once (already needed for messages), then iterates messages in O(n).
- The pricing Calculator is created once at service initialization, not per request.
- No Store interface changes needed — this phase only reads existing data.

## What This Does NOT Do

- No per-tool-call cost breakdown (that's Phase 5.3 Token Accounting)
- No cost forecasting (that's Phase 6.2)
- No cost alerts or budgets (future)
- No real-time cost tracking during sessions (aisync captures after the fact)
