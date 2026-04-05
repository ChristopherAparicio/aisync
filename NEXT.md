# aisync — Feature Backlog & Discussion

> Last updated: 2026-04-05 (Section 8 added — Investigation Tooling & Agent Support, triggered by cycloplan session analysis)
> Purpose: Catalogue of all feature ideas discussed, prioritized by value and effort.
> Not a commitment — a living document for planning next sessions.

---

## Table of Contents

1. [Skills & MCP Governance](#1-skills--mcp-governance)
2. [Context Window & Compaction Analytics](#2-context-window--compaction-analytics)
3. [Model Fitness Analysis](#3-model-fitness-analysis)
4. [Session Efficiency Signals](#4-session-efficiency-signals)
5. [Packmind-Inspired Ideas](#5-packmind-inspired-ideas)
6. [Model Recommendation & Leaderboard Integration](#6-model-recommendation--leaderboard-integration)
7. [Existing Backlog (from TODO.md)](#7-existing-backlog)
8. [Investigation Tooling & Agent Support](#8-investigation-tooling--agent-support) ⭐ **NEW**

---

## 1. Skills & MCP Governance

> **Theme:** Understand what skills and MCP tools cost, how much context they consume, and whether they're actually useful across projects.

### 1.1 Skill Context Footprint Tracking

**Problem:** When a skill loads, it injects 2,000–10,000 tokens of instructions into the context window. Today this cost is invisible — diluted into subsequent messages' input tokens.

**What we'd build:**
- Enrich `SkillLoadDetail` with `EstimatedTokens` (computed from content size via `len(content)/4`)
- Detect skill content from `<skill_content name="...">` tags in assistant messages — measure the actual text size
- Track per-skill token footprint over time in `EventBucket`
- Dashboard: "Skills by Context Cost" — skill name, avg tokens per load, load count, estimated cost

**Value:** HIGH — Directly answers "is this skill worth the context it consumes?"
**Effort:** MEDIUM — Requires changes to event processor + new dashboard section

### 1.2 Cross-Project MCP Usage Matrix

**Problem:** MCP servers cost $848/month total across projects, but we can't see which projects use which servers, or compare costs per project.

**What we'd build:**
- New dashboard section: matrix view projects × MCP servers
- Each cell: call count, token cost, estimated $
- Row totals per project, column totals per MCP server
- Highlight "mono-project" MCP servers (only used in 1 project → high relative cost)

**Data source:** Already available via `ToolCostSummary()` with project filter — just needs the cross-project view.

**Value:** HIGH — Answers "am I spending too much on Notion MCP in project X?"
**Effort:** LOW — Data exists, needs UI aggregation only

### 1.3 Configured vs Used Analysis

**Problem:** Registry scanner knows what MCP servers are *configured* per project. `tool_usage_buckets` knows what's *actually used*. These two views are disconnected.

**What we'd build:**
- Persist registry snapshots in a `project_capabilities` table (scan on server start, refresh periodically)
- Join `project_capabilities` (configured) with `tool_usage_buckets` (used)
- Surface 3 states per MCP server per project:
  - ✅ **Active** — configured AND used
  - ⚠️ **Idle** — configured but never used (wasting system prompt context)
  - ❓ **Unregistered** — used but not found in registry config

**Value:** HIGH — "You have 12 MCP servers configured, only 5 are used. The 7 idle ones waste ~3,000 tokens of system prompt per session."
**Effort:** MEDIUM — Requires registry persistence + join query + UI

### 1.4 Harmonize Events with Tool Classification

**Problem:** The event processor (`sessionevent.Processor`) stores raw tool names like `mcp__notion__search` in `TopTools`, while cost buckets group by MCP server. They don't speak the same language.

**What we'd build:**
- Call `ClassifyTool()` in the event processor
- Add `TopMCPServers map[string]int` to `EventBucket` (aggregated from classified tool names)
- Make `knownMCPPrefixes` in `toolclass.go` configurable via `config.json` (not hardcoded)
- Auto-populate prefix list from registry's discovered `MCPServer` entries

**Value:** MEDIUM — Consistency improvement, enables better cross-cutting queries
**Effort:** LOW — Small code changes, backwards compatible

### 1.5 Skill Reuse Map

**Problem:** We can't answer "which skills are shared across projects?" vs "which are mono-project?"

**What we'd build:**
- Cross-project query on `EventBucket.TopSkills` grouped by skill name
- Dashboard card: "Shared Skills" (used in 2+ projects) vs "Project-Specific Skills"
- Flag globally-configured skills that are never loaded (idle skills)

**Value:** MEDIUM — Useful for cleanup, but limited impact with ~7 projects
**Effort:** LOW — Query + UI only

---

## 2. Context Window & Compaction Analytics

> **Theme:** Understand when and why the context window fills up, what compaction costs, and when a session becomes inefficient.

### 2.1 Compaction Detection & Cost

**Problem:** When the context window fills up, the AI provider compacts/summarizes the conversation. This loses context quality and costs tokens for the compaction itself. We don't track this at all today.

**Research findings (Q1 ✅):** No provider emits explicit compaction signals. Detection must use token heuristics — see Q1 in Open Questions for full details.

**What we'd build:**
- `DetectCompactions(messages []session.Message) []CompactionEvent` — scans consecutive assistant messages for input_token drops > 50% from baseline > 20K tokens
- Confirm with secondary signal: cache invalidation (`cache_read_tokens` drops to ~0 while `cache_creation_tokens` spikes)
- Extend `CacheEfficiency()` in `session_usage.go` (already walks all messages) to also detect compaction events
- Add `CompactionEvent` struct: `BeforeMessageIdx`, `AfterMessageIdx`, `BeforeInputTokens`, `AfterInputTokens`, `DropRatio`, `CacheInvalidated`
- Store results in `session_events` table (event_type = `"compaction"`)
- Optionally add `IsPostCompaction bool` to `Message` struct for enrichment during parsing

**Dashboard:**
- KPI: "Total Compaction Events (30d)", "Avg Compaction Cost"
- Per-session: "This session was compacted 3 times, losing ~45K tokens of context"
- Alert: "Session X was compacted after only 15 messages — consider a smaller model or splitting the task"

**Value:** VERY HIGH — Directly answers "how much am I losing to compaction?"
**Effort:** MEDIUM (revised down) — Detection logic is straightforward now that research is done. Extension point identified (`CacheEfficiency`). No provider-specific logic needed — pure token heuristic works for all.

### 2.2 Context Saturation Forecast

**Problem:** For a given model's context window (e.g., 200K tokens for Claude), we can't predict when the session will hit saturation and trigger compaction.

**What we'd build:**
- Track cumulative context growth per session (input_tokens over message index)
- For each model, know the context window limit (from pricing catalog or config)
- **Forecast:** "At current token rate, this session will fill the context window at message ~85"
- **Historical:** "On average, sessions with model X are compacted after N messages"
- Histogram: distribution of "messages before first compaction" per model

**Dashboard:**
- "Avg Messages Before First Compaction" KPI per model
- Recommendation: "Claude Opus sessions last ~120 messages before compaction. For longer tasks, consider splitting."
- Chart: messages-to-compaction distribution

**Value:** HIGH — Helps plan session strategy (when to split, when to start fresh)
**Effort:** MEDIUM — Requires context window sizes in catalog + statistical aggregation

### 2.3 Session Freshness & Diminishing Returns

**Problem:** After compaction, the AI loses context fidelity. Each subsequent compaction degrades quality further. At some point, it's more efficient to start a fresh session.

**What we'd build:**
- Track "session age" in terms of compactions (0, 1, 2, 3...)
- Correlate compaction count with error rate, tool retry rate, and cache miss rate
- **Recommendation engine:** "After 2 compactions, your error rate increases by 40%. Consider starting a new session."
- "Optimal session length" metric per model: the sweet spot before quality degradation

**Value:** HIGH — Actionable guidance for developers
**Effort:** HIGH — Requires correlation analysis across many sessions

---

## 3. Model Fitness Analysis

> **Theme:** Is the model well-suited for the task, or is it being overworked/underutilized?

### 3.1 Model Context Efficiency Score

**Problem:** A small model (e.g., Haiku, Gemini Flash) fills its context window quickly. A large model (Opus, Sonnet) has more headroom but costs more. We can't compare their efficiency per dollar.

**What we'd build:**
- **Tokens per dollar per model:** How many useful output tokens do you get per dollar?
- **Context utilization ratio:** `avg_input_tokens / model_context_window` — how full is the context on average?
- **Cost per useful output:** `session_cost / (output_tokens - error_retry_tokens)` — excluding wasted retries

**Dashboard:**
- Table: Model | Avg Context Utilization | Cost/1K Output | Error Rate | Recommendation
- "Claude Haiku uses 85% of its context window on average — consider upgrading to Sonnet for complex tasks"
- "Claude Opus only uses 12% of context — you're paying for capacity you don't need"

**Value:** HIGH — Model selection guidance based on real usage data
**Effort:** MEDIUM — Catalog has context window sizes, just needs aggregation

### 3.2 Model Overload Detection

**Problem:** When a model is "overloaded" (context too full, too many retries, declining response quality), the session becomes wasteful. We want to detect this early.

**Signals to track:**
- Increasing error rate over message index (error rate in last 10 msgs vs first 10)
- Tool call retry rate increase (same tool called >2x in a row)
- Decreasing output-to-input ratio (model gives shorter answers as context fills)
- Sudden increase in "thinking" token usage (model struggling to reason)

**What we'd build:**
- Per-session "health curve": quality score over time (message index)
- Detect inflection point: "Session quality started declining at message 47"
- **Alert/recommendation:** "Model X shows signs of overload after ~50 messages for this type of task"

**Value:** VERY HIGH — Prevents waste by detecting diminishing returns in real-time
**Effort:** HIGH — Requires multi-signal correlation, statistical modeling

### 3.3 Model Recommendation Engine

**Problem:** Users don't know which model is best for a given task type.

**What we'd build:**
- Correlate session type (feature, bugfix, refactor) with model performance metrics
- For each task type: rank models by cost-efficiency, error rate, completion speed
- "For bug fixes, Sonnet is 60% cheaper than Opus with similar error rates"
- "For refactoring, Opus has 30% fewer retries — worth the extra cost"

**Value:** MEDIUM — Requires enough data diversity (multiple models in production)
**Effort:** MEDIUM — Statistical aggregation, needs model diversity in data

---

## 4. Session Efficiency Signals

> **Theme:** Additional metrics to understand session quality beyond what we already have.

### 4.1 Cache Miss Cost Attribution

**Already partially implemented** in `CacheEfficiency()`. Could be enriched:
- Per-session timeline: show when cache misses happen (gaps > 5 min)
- "You lost $12 in cache savings because of a 15-minute break at 2:30pm"
- Recommendation: "Try to keep sessions continuous. The 5-minute cache TTL means breaks > 5 min = full-price input tokens."

**Value:** MEDIUM — Enhances existing cache efficiency feature
**Effort:** LOW — Data exists, needs per-session timeline view

### 4.2 Token Waste Classification

**Problem:** Not all tokens are created equal. Some are useful, some are wasted.

**Categories:**
- **Productive tokens:** Successful tool calls, accepted code, final answers
- **Retry tokens:** Failed tool calls retried, error recovery loops
- **Compaction tokens:** Lost to summarization/compaction
- **Cache miss tokens:** Input tokens re-sent at full price due to cache expiry
- **Idle tokens:** System prompt, MCP tool descriptions, skill instructions (loaded but never used in session)

**What we'd build:**
- Classify each message's tokens into these categories
- Dashboard: pie chart "Token Distribution" — productive vs waste
- "Only 62% of your tokens are productive. 18% are retries, 12% cache misses, 8% idle context."

**Value:** HIGH — Very actionable, shows exactly where money goes
**Effort:** HIGH — Requires multi-signal classification per message

---

## 5. Packmind-Inspired Ideas

> **Theme:** Ideas inspired by Packmind's feature set, adapted to aisync's observation/analytics role.

### 5.1 Capabilities Registry Persistence

**Inspired by:** Packmind's versioned standards with distribution tracking.

**What we'd build:**
- `project_capabilities` table: (project_path, capability_name, capability_kind, scope, first_seen, last_seen, is_active)
- Scan on server start + periodic refresh
- Historical tracking: when was a capability added/removed?
- "Project X added the `sentry` MCP server 2 weeks ago — it's cost $45 since then"

**Value:** HIGH — Foundation for all governance features (1.3, 1.5, 5.2, 5.3)
**Effort:** MEDIUM — New table + scan integration + migration

### 5.2 Cross-Project Capabilities Dashboard

**Inspired by:** Packmind's Distribution Overview (repository × artifact matrix).

**What we'd build:**
- Section in existing dashboard (not separate page): "Capabilities Overview"
- Matrix: projects × (skills + MCP servers) with usage status badges
- Filter: "Show only idle capabilities" / "Show only shared capabilities"
- Cost column: how much each capability costs per project

**Value:** HIGH — Cross-project visibility, identifies waste and sharing patterns
**Effort:** MEDIUM — Requires 5.1 (registry persistence) as prerequisite

### 5.3 CLAUDE.md / AGENTS.md Impact Analysis

**Inspired by:** Packmind's standard versioning and drift detection.

**Problem:** CLAUDE.md and AGENTS.md files are the primary way to inject context into AI sessions. But we don't know how much context they consume or whether changes to them affect session quality.

**Research findings (Q3 ✅):** System prompts are NOT stored in session data. Best proxy: `cache_creation_input_tokens` on the first assistant message ≈ system prompt size. See Q3 in Open Questions for full details.

**What we'd build:**
- Add `SystemPromptEstimate int` to `session.Session` or `TokenUsage`
- In parsers: set from `first_assistant_msg.CacheWriteTokens` (Method A), fallback to `first_msg.InputTokens - roughTokenEstimate(user_content)` (Method B)
- Track system prompt size over time: "CLAUDE.md grew from 2K to 8K tokens between sessions → input cost increased by $X"
- Complementary: registry-based estimation (count MCP servers × ~300 tokens/tool + CLAUDE.md file size + ~3K fixed overhead)
- Correlate prompt size with session metrics (error rate, retries, completion speed)
- "Smaller CLAUDE.md (< 3K tokens) correlates with 15% fewer retries"

**Value:** MEDIUM — Interesting but requires manual investigation to act on
**Effort:** MEDIUM (revised down) — Proxy approach is provider-agnostic and uses existing data fields. No need for provider-specific parsing.

---

## 6. Model Recommendation & Leaderboard Integration

> **Theme:** Cross-reference real usage cost data from aisync with public coding benchmarks to recommend cheaper model alternatives. Answer: "Is model X worth the price premium over model Y for my use case?"

### 6.1 Aider Polyglot Benchmark Integration

**Problem:** Users pay wildly different prices for AI models without knowing whether the quality difference justifies the cost. Claude Opus 4 costs $65.75/session average but scores 72% on the Aider polyglot benchmark. Kimi K2 costs $1.24 but scores 59%. Is the 13% quality gap worth 98% cost savings?

**Data source — Aider Polyglot Leaderboard (captured 2026-03-27):**

225 coding exercises across 6 languages (Python, JavaScript, TypeScript, C#, Java, C++). Measures the model's ability to correctly edit code based on natural language instructions.

| Model | Score | Approx $/session | Notes |
|-------|-------|-------------------|-------|
| GPT-5 | 88.0% | ~$29 | Top scorer |
| o3-pro | 85.3% | ~$146 | Most expensive |
| Gemini 2.5 Pro | 83.1% | ~$50 | Best among Google |
| o3 | 81.3% | ~$21 | Good value for quality |
| Claude Sonnet 3.7 | 64.9% | ~$37 | Thinking mode |
| Claude Opus 4 | 72.0% | ~$66 | Our primary model |
| Claude Sonnet 4 | 60.9% | ~$27 | |
| DeepSeek R1 Reasoner | 73.6% | ~$1.30 | **Best value: near-Opus quality at 2% cost** |
| DeepSeek V3.2 Chat | 70.2% | ~$0.88 | **Cheapest high-performer** |
| Kimi K2 | 59.1% | ~$1.24 | Competitive at ultra-low cost |
| Gemini 2.5 Flash | 73.3% | ~$5 | Good quality/price balance |

**What we'd build:**
- **Benchmark data store:** Embed a static snapshot of Aider leaderboard data (model → score, date captured). Option to refresh periodically via web scrape or API.
- **Cross-reference with LiteLLM catalog:** Join benchmark score + pricing data → compute `quality_per_dollar = benchmark_score / cost_per_1M_tokens`.
- **Cross-reference with our usage data:** Join with `token_usage_buckets` to compute actual session costs per model, not theoretical pricing.
- **"Model Alternatives" view:** For each model the user currently uses, show cheaper alternatives with:
  - Quality drop % (benchmark delta)
  - Cost savings % (from our real data or LiteLLM pricing)
  - "Switch recommendation" score: `savings% * (1 - quality_drop_weight)`
  - Example: "Switch from Claude Opus ($66/session avg) → DeepSeek R1 ($1.30/session) — saves 98% with +1.6% benchmark score gain"

**Dashboard integration:**
- New section in cost dashboard (filter, not separate page): "Model Alternatives"
- Table: Current Model | Alternative | Benchmark Δ | Cost Δ | Monthly Savings Estimate
- Highlight "no-brainer" swaps where quality is equal/better AND cost is lower
- Warning badge for alternatives with significant quality drops (>15%)

**Caveats & honesty signals:**
- Aider benchmark ≠ real-world performance — show disclaimer
- Benchmark tests code editing, not architecture/planning tasks
- Some models excel at specific languages — show per-language breakdown when available
- Our data is from OpenCode/Claude Code usage — different tools may yield different results

**Value:** VERY HIGH — Directly actionable cost savings. The data shows potential 50-98% cost reduction opportunities.
**Effort:** MEDIUM — Benchmark data capture is manual/semi-automated. Cross-referencing with LiteLLM catalog is straightforward. Dashboard view reuses existing patterns.

### 6.2 Quality-Adjusted Cost Metric (QAC)

**Problem:** Raw cost comparison is misleading. A model that costs 50% less but fails 30% more is actually more expensive when accounting for retries, debugging, and wasted sessions.

**What we'd build:**
- `QualityAdjustedCost = actual_cost / (1 - failure_rate)` — normalized cost accounting for quality
- Use our error classification data (Phase 10.6) as the real "failure rate" — not just benchmark scores
- For models we haven't used: estimate failure rate from benchmark score as proxy
- **Formula:** `QAC = (cost_per_session) / (benchmark_score / 100)` — higher benchmark = lower adjusted cost
- Rank all models by QAC rather than raw cost

**Dashboard:**
- "True Cost" column alongside raw cost — accounts for quality
- "If you switch to model X, your adjusted cost would be $Y (accounting for estimated N% more errors)"
- Confidence level: HIGH (based on our data) / MEDIUM (based on benchmarks) / LOW (no data, rough estimate)

**Value:** HIGH — Makes model recommendations trustworthy, not just cheap
**Effort:** MEDIUM — Depends on 6.1 for benchmark data + error classification already exists

### 6.3 Multi-Benchmark Aggregation

**Problem:** Aider polyglot is one benchmark. Other benchmarks measure different skills (reasoning, long-context, tool use, planning). A model might score poorly on Aider but excel at the tasks we actually do.

**Potential data sources:**
- **Aider polyglot** — code editing across 6 languages (captured ✅)
- **SWE-bench** — real GitHub issue resolution (measures end-to-end problem solving)
- **HumanEval / MBPP** — code generation from docstrings
- **GPQA / MMLU-Pro** — reasoning and knowledge
- **ToolBench / API-Bank** — tool/API usage accuracy (closest to MCP use case)
- **Chatbot Arena ELO** — crowdsourced human preference

**What we'd build:**
- `BenchmarkScore` struct: `{Source, ModelName, Score, Date, Category}`
- `BenchmarkCatalog` port interface: `Lookup(model) []BenchmarkScore`, `Sources() []string`
- Embedded adapter with curated snapshots from multiple sources
- Composite score: weighted average across benchmarks (configurable weights)
- Default weights: Aider 40%, SWE-bench 30%, ToolBench 20%, Arena ELO 10%

**Value:** MEDIUM — Improves recommendation quality but adds complexity
**Effort:** HIGH — Multiple data sources to capture, normalize, and maintain

### 6.4 Model Fitness Profiles

**Problem:** Different tasks (bug fix, feature, refactor, documentation) may benefit from different models. A cheap model might be perfect for docs but terrible for complex refactoring.

**What we'd build:**
- Correlate `session_type` (from objectives analysis) with model + cost + error rate
- Build per-task-type model rankings: "For bug fixes, your best model is X. For features, it's Y."
- Recommend model switching strategies: "Use Sonnet for simple tasks, Opus for complex refactoring"
- Track sessions where user switched models mid-project (fork with different model)

**Value:** HIGH — Personalized, data-driven model selection
**Effort:** HIGH — Requires enough session diversity + objective classification + multi-model usage data
**Prerequisite:** Sufficient model diversity in production data (currently limited — see Q5)

---

## 7. Existing Backlog (from TODO.md)

These items are already tracked and remain relevant:

### Quick Actions & Navigation
- [ ] **1.1** Analyze on Demand — "Analyze" button for sessions without objectives
- [ ] **1.2** Fork Tree Navigation — clickable mini-tree in session list
- [ ] **1.3** Project Page Enhancements — sparklines, error trends, session type breakdown

### Future Error Analysis
- [ ] LLM classifier for ambiguous tool errors
- [ ] `CompositeClassifier` — deterministic first, LLM fallback

### Technical Debt
- [ ] Compute objectives for existing sessions (batch job)

### Phase 6.1 — Session Replay (deferred)
- [ ] Replay user messages through a different model
- [ ] Side-by-side comparison (tokens, cost, quality)

---

## 8. Investigation Tooling & Agent Support

> **Theme:** Turn aisync into an investigation platform where analysis agents can drill into sessions
> cheaply and iteratively, instead of stuffing everything into a single LLM prompt.
>
> **Triggered by:** 2026-04-05 manual investigation of 2 cycloplan OpenCode sessions
> (`ses_2a125dde7ffeLfthr64J35CFkq`, `ses_2a1396b09ffeMRMA5b9eEeRxWt`). Key findings:
>
> - **Compaction detection rate: ~25%** — our `< 50%` threshold misses OpenCode's ~49% drops. Real rate:
>   4 compactions per session vs 1 detected. Context climbs to ~168k–183k tokens (85% of 200k window)
>   before each compaction, roughly 1 compaction every 3 user messages.
> - **Command output is a black hole** — `CommandDetail` tracks `base_command` + `full_command` + `duration_ms`
>   but NOT output bytes/tokens. Long bash outputs (lint, tsc, curl verbose, dev server logs) are invisible
>   in cost attribution and re-compacted on every turn.
> - **High CLI wrapper opportunity** — 37% repetition ratio on bash commands, 43% of commands exceed 150 chars.
>   Clear candidates: `vite dev start`, `health check`, `login + token extract`, `port scan`, `pre-commit tsc`.
>   Wrapping these in a project CLI would save significant tokens through compaction amplification.
>
> **Architectural vision: two-level investigation model**
>
> - **Level 1 — Cheap aggregates** (no LLM): precomputed hot-spots stored per session
>   (top commands by output size, compactions with corrected threshold, skill footprints, waste buckets).
>   Instantly consultable, zero token cost.
> - **Level 2 — On-demand LLM drill-down**: the analysis agent starts with a ~500 token hot-spots summary,
>   then pulls individual command details / compaction windows / similar-command queries via targeted tool calls.
>   Replaces the current "dump everything into an 8k token prompt" approach in `BuildAnalysisPrompt`.

### 8.1 Fix Compaction Threshold + Per-User-Msg Rate

**Problem:** `internal/session/compaction.go` uses strict `< 50%` drop threshold. OpenCode compacts at
pile ~49% (observed: 49.76%, 47.22%, 49.42%, 43.17% drops in real cycloplan sessions). Result: our
detector finds 1 compaction when there are actually 4.

**What we'd build:**
- Relax threshold to `<= 55%` drop from `> 20k` baseline (catches the OpenCode 49% pattern)
- Add secondary trigger: `>= 40%` drop AND absolute delta `>= 50k tokens` (catches partial compactions)
- New metric `CompactionsPerUserMessage` in `session.CompactionSummary` — surface the "compaction cascade" signal
- Update existing tests + add regression tests with real OpenCode token patterns

**Value:** 🔴 **CRITICAL** — Without this fix, every downstream compaction feature under-reports.
**Effort:** XS (~30 min)
**Dependencies:** None

### 8.2 Command Output Bytes & Token Tracking

**Problem:** `CommandDetail` struct only captures what goes INTO a command, not what comes OUT.
Long bash outputs (lint, tsc, dev server logs, curl verbose) are re-compacted on every turn
but invisible in aisync cost attribution. The "black hole" of command cost.

**What we'd build:**
- Add `OutputBytes int` and `OutputTokens int` fields to `sessionevent.CommandDetail`
- Populate during `Processor` extraction by measuring the tool call's output string (`len(output)` → bytes, `/4` → tokens estimate)
- Migration 031: backfill existing rows with zero, new rows get real values
- Surface in session detail page: "Top commands by output size" panel (sortable)
- Update `CommandDetail` marshal/unmarshal in `event_store.go` (avoid the compaction storage bug we hit in Sprint H)

**Value:** 🔴 **CRITICAL** — Unlocks 8.3, 8.5, 8.8 and makes command cost visible for the first time.
**Effort:** S (~1h)
**Dependencies:** None

### 8.3 Session Hot-Spots Precomputed Table (Level 1)

**Problem:** Every analysis agent invocation recomputes the same aggregates (top commands, compactions,
skill footprints) from raw events. Wasteful and slow, and the data format isn't optimized for LLM consumption.

**What we'd build:**
- New table `session_hotspots` (migration 031 or 032) with pre-aggregated JSON per session:
  - `top_commands_by_output` — top 20 commands ranked by `OutputBytes` (with event IDs for drill-down)
  - `top_commands_by_reuse` — top 20 commands by duplicate count (normalized form)
  - `compaction_events` — all detected compactions with before/after context pointers
  - `compaction_rate` — compactions per user message
  - `skill_footprints` — per-skill cumulative tokens (from existing `SkillLoadDetail.EstimatedTokens`)
  - `expensive_messages` — messages with `input_tokens > 100k` (compaction warning zone)
- New scheduler task `HotspotsTask` (runs after `analyze_daily`, or on-demand via API)
- New port method `storage.SessionHotspotStore` (GetHotspots / SetHotspots)
- UI: new "Hot Spots" tab on session detail page (cheap, no LLM)

**Value:** 🟠 **HIGH** — Foundation for 8.4 (agent tooling) and 8.5 (CLI candidates dashboard).
**Effort:** M (~2h)
**Dependencies:** 8.2 (needs OutputBytes to rank commands)

### 8.4 `aisync-investigator` — Agent Investigation API

**Problem:** Current `BuildAnalysisPrompt` dumps ~8k tokens of session data into a single LLM prompt
and asks for a report. Expensive (Opus input rate × 8k per session) and imprecise (LLM can't ask
follow-up questions). Your insight: *"permettre à l'agent de lire chaque commande une par une si il souhaite"*.

**What we'd build:**
- New HTTP endpoints on port 8371 (can be wrapped as MCP stdio server later):
  - `GET /api/investigate/sessions/{id}/hotspots` → reads from 8.3 table, returns compact JSON
  - `GET /api/investigate/sessions/{id}/commands?min_bytes=5000` → filtered command list
  - `GET /api/investigate/sessions/{id}/commands/{event_id}` → full command detail (truncated output)
  - `GET /api/investigate/sessions/{id}/compactions/{idx}/window?radius=3` → N messages around a compaction
  - `GET /api/investigate/sessions/{id}/skills` → skill load list with tokens
  - `GET /api/investigate/commands/similar?pattern=...&scope=all` → find repeated commands cross-session (CLI candidates)
- Each endpoint returns JSON shaped for LLM consumption (bounded size, stable schema)
- Optional: `cmd/aisync-investigator-mcp/` stdio wrapper for direct MCP protocol use by agents
- Documented as "investigation API" in a new `docs/investigation-api.md`

**Value:** 🟠 **HIGH** — Transforms aisync from "dashboard" into "investigation platform".
**Effort:** M (~2-3h)
**Dependencies:** 8.3 (hot-spots table must exist)

### 8.5 Repeated Command Detection & CLI Candidates Dashboard

**Problem:** Developers repeatedly paste 150+ character bash commands (vite dev, curl login flows,
lsof port scans) across sessions. Each repetition costs tokens twice: once in the assistant message,
once in every subsequent compaction's summary. But aisync has no way to surface these patterns.

**What we'd build:**
- New analytics query: group commands by normalized form (replace `/path/...` → `/PATH`, numbers → `N`)
- Count occurrences + unique sessions + unique projects per normalized pattern
- Filter: commands with `LENGTH(full_command) > 100` AND `count >= 3`
- Estimate token savings: `total_chars_saved × avg_compaction_amplification_factor`
- New dashboard page `/investigate/cli-candidates`:
  - Cross-project table: pattern, occurrences, sessions, projects, chars, est. tokens saved
  - Click-through to see example invocations
  - Export as JSON/CSV (for actually building the CLI)

**Value:** 🟠 **HIGH** — Directly actionable. You can build `cyclo` CLI from this, saving tokens on every future session.
**Effort:** M (~2h)
**Dependencies:** 8.2 (needs output tracking for savings estimate)

### 8.6 Enhance `BuildAnalysisPrompt` to Use Hot-Spots

**Problem:** `internal/analysis/llm/analyzer.go::BuildAnalysisPrompt` dumps message distribution, tool
breakdown, first 5 user messages, last 10 messages, errors, files, capabilities — all in plain text,
~6-8k tokens per call. Misses the compaction + command output signals entirely.

**What we'd build:**
- Inject `session_hotspots` JSON at the top of the prompt (compact, structured)
- Remove the redundant full message dump (last 10 messages) — agent can query them on demand via 8.4
- Update system prompt to instruct the LLM: *"If the hot-spots show suspicious patterns, use the
  investigation API endpoints to drill down before finalizing your report."*
- Add new analysis dimensions to the output schema:
  - `compaction_findings` — assessment of the compaction cascade
  - `command_waste_findings` — identified expensive/repetitive commands
  - `cli_suggestions` — proposed CLI wrappers with estimated savings
- Backwards compatible: existing fields stay, new fields are additive

**Value:** 🟡 **MEDIUM** — Makes every analyzed session report 3–5× more actionable and ~40% cheaper.
**Effort:** S (~1h)
**Dependencies:** 8.3 (hot-spots), ideally 8.4 (so LLM can drill down)

### 8.7 Compaction Cascade Alert

**Problem:** When a session compacts more than once per 4 user messages, something is wrong
(task too large, context bloat, bad model choice). Today this is invisible — no notification, no UI flag.

**What we'd build:**
- Scheduler task `CompactionCascadeDetectionTask` (runs after `HotspotsTask`)
- Threshold: `compactions_per_user_message > 0.25` AND `total_compactions >= 3`
- New event type `EventCompactionCascade` fired via notification service
- Dedup window: don't re-alert same session within 24h
- Message template: *"Session `{id}` in project `{project}` compacted {N} times across {M} user messages.
  Context climbed to {max_tokens} tokens. Consider: splitting the task, switching to a 1M-token model, or
  wrapping frequent commands in a project CLI (see /investigate/cli-candidates)."*
- Wire into existing Slack notification infrastructure (already supports project channels)

**Value:** 🟡 **MEDIUM** — Catches runaway sessions before they burn $50+ in compaction overhead.
**Effort:** S (~1h)
**Dependencies:** 8.1 (accurate detection), 8.3 (rate computation)

### 8.8 Per-Provider Context Bloat Analysis

**Problem:** Observed: OpenCode pushes context to 168k–183k tokens before compacting, while Claude Code
(anecdotally) stays lower. We have the data to prove/disprove this but no comparative view.

**What we'd build:**
- New analytics query: percentile distribution of `input_tokens` per assistant message, grouped by provider
- Metric: "avg context before compaction" per provider per project
- Metric: "tokens injected per tool_call" per provider (how verbose is each provider's context packing)
- Dashboard card on `/analytics`: "Provider Context Efficiency"
  - Bar chart: avg max context by provider
  - Bar chart: tool_call → context tokens ratio by provider
  - Insight callout when delta > 2× between providers on same project
- Use case: confirm whether OpenCode is genuinely more bloated, data-driven migration decisions

**Value:** 🟡 **MEDIUM** — Validates (or disproves) the "OpenCode is bloated" hypothesis with numbers.
**Effort:** M (~2h)
**Dependencies:** 8.2 (for the tool_call → output_bytes correlation)

---

### Section 8 — Suggested Phased Rollout

**Phase 1 — Critical Fixes (~1.5h)** ⚡ *Unblocks everything else*
- **8.1** Fix compaction threshold (XS, 30 min)
- **8.2** Command output bytes/tokens (S, 1h)
- → After Phase 1, re-run analysis on cycloplan sessions: 4 compactions visible, command costs visible.

**Phase 2 — Level 1 Aggregates (~4h)**
- **8.3** Hot-spots precomputed table (M, 2h)
- **8.5** CLI candidates dashboard (M, 2h)
- → After Phase 2, you can browse "Top CLI candidates" for cycloplan and start building `cyclo` CLI.

**Phase 3 — Level 2 Agent Tooling (~3-4h)**
- **8.4** Investigation API endpoints (M, 2-3h)
- **8.6** Enhanced analysis prompt using hot-spots (S, 1h)
- → After Phase 3, `analyze_daily` produces reports that identify compaction cascades + CLI candidates automatically.

**Phase 4 — Alerts & Validation (~3h)**
- **8.7** Compaction cascade Slack alert (S, 1h)
- **8.8** Per-provider context bloat dashboard (M, 2h)
- → After Phase 4, runaway sessions trigger alerts proactively, provider comparison is data-driven.

**Total estimated effort: ~11-12h across 4 phases.** Each phase delivers standalone value.

---

## Priority Matrix

> **Note:** Items completed in Sprint C–E sessions (2026-03-31 to 2026-04-02) are marked ✅ DONE.

| Feature | Value | Effort | Dependencies | Status |
|---------|-------|--------|-------------|--------|
| ~~**1.2** Cross-Project MCP Matrix~~ | ~~HIGH~~ | ~~LOW~~ | ~~None~~ | ✅ DONE (pre-Sprint F) |
| ~~**1.4a** ClassifyTool in event processor~~ | ~~MEDIUM~~ | ~~LOW~~ | ~~None~~ | ✅ DONE (pre-Sprint F) |
| ~~**1.4b** Configurable MCP tool prefixes~~ | ~~MEDIUM~~ | ~~LOW~~ | ~~None~~ | ✅ DONE (Sprint F) |
| ~~**Q2 fix** Wire MaxInput/OutputTokens~~ | ~~HIGH~~ | ~~TRIVIAL~~ | ~~None~~ | ✅ DONE (pre-Sprint F) |
| ~~**1.1** Skill Context Footprint~~ | ~~HIGH~~ | ~~MEDIUM~~ | ~~None~~ | ✅ DONE (Sprint H) |
| ~~**5.1** Registry Persistence~~ | ~~HIGH~~ | ~~MEDIUM~~ | ~~None~~ | ✅ DONE (Sprint F) |
| ~~**2.1** Compaction Detection & Cost~~ | ~~VERY HIGH~~ | ~~MEDIUM~~ | ~~Research done ✅~~ | ✅ DONE (Sprint H — storage bug fix + session UI) |
| ~~**6.1** Aider Benchmark Integration~~ | ~~VERY HIGH~~ | ~~MEDIUM~~ | ~~LiteLLM catalog~~ | ✅ DONE (Sprint C) |
| ~~**1.3** Configured vs Used~~ | ~~HIGH~~ | ~~MEDIUM~~ | ~~5.1~~ | ✅ DONE (pre-Sprint F — full stack verified) |
| ~~**2.2** Context Saturation Forecast~~ | ~~HIGH~~ | ~~MEDIUM~~ | ~~2.1, Q2 fix~~ | ✅ DONE (Sprint C) |
| ~~**3.1** Model Context Efficiency~~ | ~~HIGH~~ | ~~MEDIUM~~ | ~~Q2 fix~~ | ✅ DONE (Sprint C) |
| ~~**5.2** Cross-Project Dashboard~~ | ~~HIGH~~ | ~~MEDIUM~~ | ~~5.1~~ | ✅ DONE (Sprint G) |
| ~~**6.2** Quality-Adjusted Cost (QAC)~~ | ~~HIGH~~ | ~~MEDIUM~~ | ~~6.1~~ | ✅ DONE (Sprint C — QAC Leaderboard) |
| ~~**4.2** Token Waste Classification~~ | ~~HIGH~~ | ~~HIGH~~ | ~~2.1~~ | ✅ DONE (Sprint D) |
| ~~**3.2** Model Overload Detection~~ | ~~VERY HIGH~~ | ~~HIGH~~ | ~~2.1, 3.1 ✅~~ | ✅ DONE (pre-Sprint F — full stack verified, 8 tests) |
| ~~**2.3** Session Freshness~~ | ~~HIGH~~ | ~~HIGH~~ | ~~2.1~~ | ✅ DONE (Sprint D) |
| ~~**1.5** Skill Reuse Map~~ | ~~MEDIUM~~ | ~~LOW~~ | ~~5.1~~ | ✅ DONE (Sprint H) |
| ~~**5.3** CLAUDE.md Impact Analysis~~ | ~~MEDIUM~~ | ~~MEDIUM~~ | ~~Research done ✅~~ | ✅ DONE (Sprint E) |
| **3.3** Model Recommendation (old) | ~~MEDIUM~~ | ~~MEDIUM~~ | ~~3.1~~ | ~~P4~~ → merged into 6.x |
| ~~**6.3** Multi-Benchmark Aggregation~~ | ~~MEDIUM~~ | ~~HIGH~~ | ~~6.1~~ | ✅ DONE (Sprint E) |
| ~~**6.4** Model Fitness Profiles~~ | ~~HIGH~~ | ~~HIGH~~ | ~~6.1, objectives~~ | ✅ DONE (Sprint E) |
| ~~**4.1** Cache Miss Timeline~~ | ~~MEDIUM~~ | ~~LOW~~ | ~~None~~ | ✅ DONE (Sprint H) |

### Remaining Work — Suggested Order

**Priority Matrix (Sections 1–6): ✅ 100% COMPLETE** — all 22 features delivered through Sprint H.

**Next focus → Section 8 (Investigation Tooling)** — new backlog triggered by cycloplan session
analysis (2026-04-05). See [Section 8](#8-investigation-tooling--agent-support) for the 8 new
features and suggested phased rollout (Phase 1 = critical fixes, Phase 4 = alerts).

### Completed Sprints

**Sprint C** (2026-03-31): 6.1 Aider Benchmark, 2.2 Saturation Forecast, 3.1 Model Efficiency, 6.2 QAC
**Sprint D** (2026-03-31): 4.2 Token Waste, 2.3 Session Freshness
**Sprint E** (2026-03-31): 6.3 Multi-Benchmark, 6.4 Model Fitness, 5.3 CLAUDE.md Impact
**Sprint F** (2026-04-03): 5.1 Registry Persistence, 1.4b Configurable MCP Prefixes (+ verified: Q2 fix, 1.4a, 1.2, 1.3, 3.2 already done)
**Sprint G** (2026-04-04): 5.2 Cross-Project Capabilities Dashboard (MCP Governance Matrix)
**Sprint H** (2026-04-04): 1.1 Skill Context Footprint, 1.5 Skill Reuse Map, 2.1 Compaction (storage fix + session UI), 4.1 Cache Miss Timeline

---

## Open Questions — Research Findings

### Q1: Compaction Signal Detection ✅ ANSWERED

**Finding: No explicit compaction signals exist. Use token-based heuristic detection.**

Neither Claude Code nor OpenCode emit explicit compaction events in their persisted session data:

- **Claude Code JSONL** only has 3 line types: `"summary"` (session title, NOT compaction), `"user"`, `"assistant"`. No `"compaction"` type exists. The `"summary"` type is the first line of every JSONL file and contains a human-readable session title (e.g., "Implemented hello world function"). The `Subtype` field is declared but never used.
- **OpenCode** stores `"user"` and `"assistant"` messages only. Part types are `"text"`, `"tool"`, `"reasoning"`, `"image"`, `"file"` — no compaction type.
- **Cursor** stores only message headers (bubble IDs); no token data at all. Compaction detection not feasible.
- Both providers perform compaction **internally and transparently** — the CLI replaces conversation history with a summary before the next API call, but never persists a compaction event.

**Recommended detection approach — token drop heuristic:**

```
For consecutive assistant messages (msg[i], msg[i+1]):
  if msg[i].InputTokens > 20,000 AND
     msg[i+1].InputTokens < msg[i].InputTokens / 2:
    → Compaction likely occurred (>50% input token drop)
```

**Secondary signal — cache invalidation:**
After compaction, the entire conversation is new content. `cache_read_tokens` drops to ~0 while `cache_creation_tokens` spikes. This confirms the compaction hypothesis.

**Data availability:** Both Claude Code and OpenCode store `InputTokens`, `CacheReadTokens`, `CacheWriteTokens` per message — all fields needed for detection are already captured.

**Recommendation:** Implement `DetectCompactions(messages []session.Message) []CompactionEvent` in the domain layer. Could extend the existing `CacheEfficiency()` method which already walks all messages per session. Add `IsPostCompaction bool` to the `Message` struct for future enrichment.

### Q2: Context Window Sizes ✅ ANSWERED

**Finding: LiteLLM provides context window sizes for 94.9% of chat models. Our catalog needs `MaxInputTokens`/`MaxOutputTokens` fields.**

- `liteLLMModel` struct already parses `MaxInputTokens` and `MaxOutputTokens` (lines 61-62 of `litellm.go`) but **discards them** during conversion to `ModelPrice`.
- Key context windows: Claude Opus = 200K/32K, Claude Sonnet 4 = 1M/64K, GPT-4o = 128K/16K, Gemini 2.5 Pro = 1M/65K, o3/o4-mini = 200K/100K.
- **Implementation:** Add `MaxInputTokens int` and `MaxOutputTokens int` to `ModelPrice`, populate in `parseLiteLLMJSON()`, add to embedded `catalog.yaml`.
- Also discovered: LiteLLM has `tool_use_system_prompt_tokens: 159` for Claude models — useful for Q3.

### Q3: System Prompt Measurement ✅ ANSWERED

**Finding: System prompts are NOT stored in session data. Use `cache_creation_input_tokens` on the first assistant message as a proxy.**

- Neither Claude Code nor OpenCode persists system messages (CLAUDE.md, tool descriptions, built-in instructions) to session files.
- The `RoleSystem` enum exists in the domain model but is only populated by the **Ollama ingest provider** (which explicitly handles `case "user", "system":` together). Claude and OpenCode parsers never set it.
- **Cursor** stores only bubble headers (message IDs, types) — no token data, no system messages. Compaction/system-prompt analysis not feasible.
- MCP tool descriptions are invisible in session data — only tool *invocations* (`mcp__server__tool`) are recorded.

**Best proxy — Method A (first-message cache write):**
The system prompt is the first and largest cacheable block. On the very first API call of a session:
- `cache_creation_input_tokens` ≈ system prompt size (CLAUDE.md + tool schemas + built-in instructions)
- On subsequent messages, this appears as `cache_read_input_tokens` (confirming it's the stable cached prefix)

```
estimated_system_prompt_tokens = first_assistant_msg.CacheWriteTokens
```

**Validation:** For subsequent messages, `cache_read_input_tokens ≈ first_msg.cache_creation_input_tokens`.

**Method B (input token delta):** `system_prompt ≈ first_msg.InputTokens - roughTokenEstimate(user_content)` using existing `byteLen/4` heuristic.

**Method C (registry estimation):** Count configured MCP servers × ~200-500 tokens per tool schema + read CLAUDE.md file size + ~2,000-4,000 fixed overhead for built-in instructions. This gives current-state estimate but not historical.

**Recommendation:** Add `SystemPromptEstimate int` to `session.Session`. In the parsers, set it from `first_assistant_msg.CacheWriteTokens`. Fall back to Method B when cache data unavailable.

### Q4: Compaction Frequency in Production Data ✅ ANSWERED

**Finding: Compaction is CONFIRMED and frequent — 167 sessions (12.8%) show compaction events, with 635 total events detected.**

**Production DB: 1,302 sessions, 245,441 messages.**

**Session length distribution:**

| Messages | Sessions | % of Total |
|----------|----------|-----------|
| 1-10 | 542 | 41.6% |
| 11-50 | 378 | 29.0% |
| 51-100 | 88 | 6.8% |
| 101-200 | 65 | 5.0% |
| 201-500 | 69 | 5.3% |
| **500+** | **125** | **9.6%** |

**Max input tokens per session distribution:**

| Max Tokens | Sessions | % of Total |
|------------|----------|-----------|
| <10K | 89 | 7.0% |
| 10-50K | 188 | 14.8% |
| 50-100K | 457 | 36.1% |
| 100-150K | 226 | 17.8% |
| 150-200K | 171 | 13.5% |
| **200K+** | **136** | **10.7%** |

**42% of sessions (307) reach >150K input tokens** — approaching context window limits. 10.7% exceed 200K.

**Compaction events detected (>50% input_token drop with >10K baseline):**

| Metric | Value |
|--------|-------|
| Total compaction events | **635** |
| Sessions with ≥1 compaction | **167 (12.8%)** |
| Avg drops per affected session | 3.8 |
| After fork deduplication | **144 sessions (11.8%), 537 events** |

**Drop magnitude distribution:**

| Drop Severity | Events |
|---------------|--------|
| 50-60% | 64 |
| 60-70% | 36 |
| 70-80% | 43 |
| 80-90% | 130 |
| 90-95% | 156 |
| 95-99% | 193 |
| 99%+ | 13 |

**Median drop = 91.9%** — compaction is aggressive, removing ~92% of context on average.

**Recovery after compaction:** 87.4% of sessions recover to >50% of pre-drop token levels (context rebuilds as conversation continues). Classic "sawtooth" pattern confirmed.

**Example — session `ses_308b1e87dffeqVj4MaQAlkO9VF`** (29 msgs, 3 compaction events):
```
[1] assistant   input=5K    → starts working
[3] assistant   input=182K  → context full (read 51 test files)
[5] assistant   input=660   → DROP 99.6% — context reset
[7] assistant   input=6K    → rebuilds from scratch
...pattern repeats 2 more times...
```

**LLM Backend distribution (from token_usage_buckets):**

| Backend | Buckets | Input Tokens | Output Tokens |
|---------|---------|-------------|---------------|
| anthropic | 1,282 | 25.3B | 56.8M |
| amazon-bedrock | 369 | 5.3B | 13.5M |
| opencode (free) | 18 | 29M | 91K |
| ollama | 23 | 2.4M | 29K |

**Verdict:** Compaction analytics is **high priority** — confirmed real and impactful:
- **167 sessions** (12.8%) show compaction, with **635 events** total
- Median 91.9% token drop means massive context loss per event
- Sessions with 500+ messages average 3.8 compaction events each
- 42% of sessions reach >150K tokens (approaching Claude's 200K limit)
- The sawtooth pattern (fill → compact → rebuild) is the dominant behavior in long sessions

### Q5: Multi-Model Data Sufficiency ✅ ANSWERED

**Finding: Limited model diversity — anthropic dominates, but bedrock provides some comparison data.**

- **Single provider** in sessions: all 1,299 sessions are `opencode`
- **4 LLM backends** in token buckets: anthropic (1,282 buckets), bedrock (369), opencode (18), ollama (23)
- **Anthropic = 83%** of all token buckets — model fitness comparison mainly anthropic vs bedrock
- **Ollama data is minimal** — 23 buckets, not enough for meaningful analysis
- **Model recommendation engine (3.3) is premature** — not enough model diversity
- **Context efficiency score (3.1) IS viable** — can compare anthropic model variants (opus vs sonnet vs haiku)

---

### Impact on Priorities

Based on all research findings:

| Feature | Priority Change | Reason |
|---------|----------------|--------|
| **2.1 Compaction Detection** | ⬆️ P1 → **P0** | 124 sessions likely compacted, high $ impact |
| **Q2 fix: wire context window** | **NEW P0** | 2-line fix, unblocks 2.2 and 3.1 |
| **2.2 Context Saturation Forecast** | ⬆️ P2 → **P1** | Data is available (LiteLLM), just discarded |
| **3.1 Model Context Efficiency** | Stays P2 | Data available once Q2 fix is done |
| **3.3 Model Recommendation** | ⬇️ Stays P4 | Not enough model diversity yet |
| **5.3 CLAUDE.md Impact** | Stays P4 | Method B+C viable but low priority |

### Updated Sprint Plan

**See [Suggested Implementation Order](#suggested-implementation-order) for the full plan (Sprints A→E).**

Summary of changes after Q1-Q5 research + leaderboard data capture:
- **Q2 fix promoted to Sprint A item 1** — trivial 2-line fix, unblocks 2.2 and 3.1
- **2.1 Compaction Detection promoted to P1** — 124 sessions (9.5%) likely compacted, high $ impact
- **3.3 Model Recommendation merged into new section 6.x** — superseded by leaderboard-based approach with real benchmark data
- **6.1 Aider Benchmark Integration added at P1** — data already captured, VERY HIGH value
- **6.2 Quality-Adjusted Cost added at P2** — combines benchmarks + our error data for trustworthy recommendations
- **6.3/6.4 deferred to Sprint E** — need more model diversity and data maturity
