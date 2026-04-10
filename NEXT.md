# aisync — Feature Backlog & Discussion

> Last updated: 2026-04-05 (Section 8 added — Investigation Tooling & Agent Support, triggered by 2026-04-05 session investigation; detection-only philosophy, provider-agnostic)
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
9. [Actionable Diagnostics & Token Economy](#9-actionable-diagnostics--token-economy) ⭐ **NEW**

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
> ### ⚖️ Philosophy: Detection, not prescription
>
> **aisync is a detection/observation tool.** It reports *what happened* with numbers, frequencies,
> and comparisons against factual baselines (project averages, percentiles, cross-provider deltas).
> It does **not** prescribe fixes, suggest refactors, or recommend tooling changes. Users and their
> agents draw their own conclusions from the observed data.
>
> **Allowed language** (facts & factual comparisons):
> - ✅ *"Session reached 183k input tokens (91% of 200k window) before each of 4 compactions"*
> - ✅ *"Command pattern `curl -X POST .../login ...` observed 12× across 5 sessions, avg length 377 chars"*
> - ✅ *"Compaction rate 0.36/user_msg exceeds this project's P90 of 0.12"*
> - ✅ *"Provider `opencode` averages 168k max context vs `claude-code` 94k on same project"*
>
> **Forbidden language** (prescription / recommendations):
> - ❌ *"Consider splitting the task"* / *"Switch to a larger model"* / *"Wrap these in a CLI"*
> - ❌ *"Suggested refactor"* / *"Recommended action"* / *"You should..."*
> - ❌ Output schemas with fields like `suggestions`, `recommendations`, `fixes`, `actions_to_take`
>
> Observation fields describe **what is** (`observed_command_patterns`, `compaction_findings`,
> `high_output_commands`). Interpretation and action are the user's job.
>
> ### Provider scope
>
> All Section 8 features apply across aisync's supported providers (`claude-code`, `opencode`, `cursor`
> and any future provider). The investigation data comes from the provider-agnostic `sessionevent`
> pipeline. Where empirical values are cited below (e.g. "~49% drop threshold"), they come from specific
> observed sessions — detectors remain heuristic-based and provider-neutral.
>
> ### Triggering investigation
>
> **2026-04-05 manual investigation** of 2 sessions (`ses_2a125dde7ffeLfthr64J35CFkq`,
> `ses_2a1396b09ffeMRMA5b9eEeRxWt`) — both captured from an `opencode` provider on the same project.
> Key observations that revealed gaps in current detection:
>
> - **Compaction detection miss rate** — current `< 50%` strict threshold in `internal/session/compaction.go`
>   misses drops in the 47–49% range. Observed drops in these sessions: 49.76%, 47.22%, 49.42%, 43.17%.
>   Detector reports 1 compaction per session; actual count is 4. Context peaks at 168k–183k tokens
>   (85–91% of the model's 200k window) before each compaction. Ratio: ~0.36 compactions per user message.
> - **Command output not measured** — `sessionevent.CommandDetail` stores `base_command`, `full_command`
>   and `duration_ms`, but no output size. Commands producing large outputs (lint/test/tsc/curl verbose/
>   dev server logs) are invisible to cost attribution even though their output is re-sent in every
>   subsequent compaction cycle.
> - **Command repetition measurable but unsurfaced** — bash command repetition ratio in these 2 sessions:
>   37% (13 duplicates / 35 total). 43% of commands exceed 150 characters. The data exists in
>   `session_events` but no aggregation or dashboard exposes it.
>
> These are all *detection gaps* — aisync currently can't *see* these patterns. Section 8 makes them
> visible. What the user does with the visibility is out of scope.
>
> ### Architectural vision: two-level investigation model
>
> - **Level 1 — Cheap aggregates** (no LLM): precomputed hot-spots stored per session
>   (top commands by output size, compactions with corrected threshold, skill footprints, command patterns).
>   Instantly consultable, zero token cost.
> - **Level 2 — On-demand LLM drill-down**: the analysis agent starts with a ~500 token hot-spots summary,
>   then pulls individual command details / compaction windows / similar-command queries via targeted tool calls.
>   Replaces the current "dump everything into an 8k token prompt" approach in `BuildAnalysisPrompt`.
>   The agent's job is to **describe observed facts** in natural language, not to recommend fixes.

### 8.1 Fix Compaction Threshold + 2-Pass Cascade Detection + Per-User-Msg Rate

**Problem:** `internal/session/compaction.go` uses a strict `< 50%` drop threshold. But investigation of
`ses_2a6cb55f6ffeENWr6H1LnWdnLu` (3426 msgs, $360, 403M tokens) revealed that OpenCode uses a
**2-pass compaction pattern**:
- Pass 1: `168k → 105k` (~37% drop, `cache_read_tokens` completely reset to 0)
- Pass 2: `105k → 68k` (~35% drop, 2 messages later, partial cache recovery)
- Total effective: `168k → 68k` = 59.5% drop — but each pass is individually below our 50% threshold!

Distribution across `ses_2a6cb55f`: 25 drops in 30–40% band (many are 2-pass cascade legs),
16 drops in 40–50% band, 15 in 50%+ band. With all thresholds combined: **31 real compactions
detected vs only 15 by current code. $18.41 in compaction cost invisible.** The detector is
provider-agnostic but calibrated too strictly; the 2-pass pattern also appeared in shorter sessions
(`ses_2a125dde`, `ses_2a1396b0` — 4 compactions each vs 1 detected).

**What we'd build:**
- Relax primary threshold to `< 55%` (captures single-pass 45–50% drops)
- Add secondary threshold: `< 65% AND delta > 40k tokens AND CacheInvalidated == true` (captures 35% drops
  with large absolute delta). **Cache invalidation is required** for this secondary trigger to avoid false
  positives from normal response size variation (e.g. loading a large file then a small response).
- Add **2-pass cascade merging**: when two detected drops arrive ≤ 3 messages apart, merge them into a
  single compaction event with combined `DropPercent`. Tagged with `IsCascade: true`, `MergedLegs: 2`.
  This captures the `168k → 105k → 68k` pattern as a single 59.5% compaction event.
- New metric `CompactionsPerUserMessage` in `session.CompactionSummary` (pure factual ratio)
- New metric `LastQuartileCompactionRate float64` — compaction rate computed over the last 25% of user
  messages only. Detects end-of-session acceleration (3 compactions in last 8 messages vs 0 in first 80).
  Stored alongside global rate in `CompactionSummary` and `session_hotspots`.
- New field `CascadeCount` in `CompactionSummary` (how many 2-pass events were detected)
- New field `MessagesWithTokenData int` in `CompactionSummary` — count of messages where `InputTokens > 0`.
  If this is 0, the detector had nothing to analyze (distinct from "analyzed and found 0 compactions").
- New enum field `DetectionCoverage string` in `CompactionSummary`: `"full"` (majority of assistant
  messages have token data), `"partial"` (some data), `"none"` (zero messages with token data — e.g.
  Cursor provider). Dashboard displays `"Compaction detection: not available (no per-message token data)"`
  instead of misleading `"0 compactions"` when coverage is `"none"`.
- Update tests using real token patterns from `ses_2a6cb55f` including cascade merging scenarios
- Detector remains provider-neutral — no branching on `session.Provider`

> **Known provider limitation (Cursor):** Cursor sessions have `InputTokens == 0` on all messages
> (message content is stored server-side, only IDs available locally). Compaction detection returns
> `DetectionCoverage: "none"`, `MessagesWithTokenData: 0`. The dashboard must surface this as "no data"
> rather than "no compactions". This is a Cursor data limitation, not an aisync bug.

**Value:** 🔴 **CRITICAL** — Without this fix, compaction under-reports by 2–3× for providers using incremental compaction.
**Effort:** S (~45 min — expanded from XS after discovering the 2-pass pattern)
**Dependencies:** None

### 8.2 Command Output Bytes & Token Tracking

**Problem:** `CommandDetail` struct only captures what goes INTO a command, not what comes OUT.
Long bash outputs (lint, tsc, dev server logs, curl verbose) are re-compacted on every turn
but invisible in aisync cost attribution. The "black hole" of command cost.

**What we'd build:**
- Add `OutputBytes int` and `OutputTokens int` fields to `sessionevent.CommandDetail`
- Populate during `Processor` extraction by measuring the tool call's output string (`len(output)` → bytes, `/4` → tokens estimate)
- No migration needed — JSON payload column, existing rows keep working (fields default to 0)
- Surface in session detail page: "Top commands by output size" panel (sortable)
- Update `CommandDetail` marshal/unmarshal in `event_store.go` (avoid the compaction storage bug we hit in Sprint H)

> **Backfill strategy:** `ToolCall.Output` is populated at ingestion time for `claude-code` and `opencode`
> providers (verified in code). The raw session data (compressed in `sessions` table) still contains the
> full `ToolCall.Output` strings. However, existing `session_events` rows have `CommandDetail` without
> `OutputBytes`. Two options:
>
> 1. **Re-process existing sessions** — call `Processor.ExtractAll()` again on sessions that have
>    `CommandDetail` events with `OutputBytes == 0`. This regenerates all events from the raw session
>    data and populates the new fields. Cost: ~5 min for 1687 sessions, one-time batch.
> 2. **Accept the gap** — only new sessions (captured after this change) get output tracking. Simpler,
>    no risk of re-processing side effects, but historical data stays incomplete.
>
> **Decision: Option 1 (re-process)** — the raw data exists, the cost is low, and it's needed for the
> validation checklist (cycloplan sessions are historical). Implement as a one-time scheduler task
> `BackfillCommandOutputTask` that re-extracts events for sessions with stale event data.

> **Known provider limitation (Cursor):** Cursor sessions have zero `ToolCall` entries (message content
> is stored server-side). `CommandDetail` events are never created for Cursor. The "Top commands by
> output size" panel will show "No command data available for this provider" for Cursor sessions.

**Value:** 🔴 **CRITICAL** — Unlocks 8.3, 8.5, 8.8 and makes command cost visible for the first time.
**Effort:** S (~1h)
**Dependencies:** None

### 8.3 Session Hot-Spots Precomputed Table (Level 1)

**Problem:** Every analysis agent invocation recomputes the same aggregates (top commands, compactions,
skill footprints) from raw events. Slow, and the data format isn't optimized for LLM consumption.

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

**Value:** 🟠 **HIGH** — Foundation for 8.4 (agent tooling) and 8.5 (command pattern dashboard).
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
  - `GET /api/investigate/commands/similar?pattern=...&scope=all` → find repeated normalized commands cross-session (returns raw patterns + counts, no recommendation wrapper)
- Each endpoint returns JSON shaped for LLM consumption (bounded size, stable schema)
- Optional: `cmd/aisync-investigator-mcp/` stdio wrapper for direct MCP protocol use by agents
- Documented as "investigation API" in a new `docs/investigation-api.md`

**Value:** 🟠 **HIGH** — Transforms aisync from "dashboard" into "investigation platform".
**Effort:** M (~2-3h)
**Dependencies:** 8.3 (hot-spots table must exist)

### 8.5 Repeated Command Pattern Dashboard

**Problem:** Long bash commands (150+ characters) recur across sessions — the same `vite dev` invocation,
the same `curl` login flow, the same `lsof` port scan. Each repetition takes tokens twice: once in the
assistant message, once again in every subsequent compaction summary that still references it. aisync
currently has no aggregation or view that surfaces these patterns.

**What we'd build:**
- New analytics query: group commands by normalized form (replace `/path/...` → `/PATH`, digits → `N`, quoted literals → `"..."`)
- Per pattern, compute: occurrence count, unique session count, unique project count, avg length, total characters
- Filter surface: `LENGTH(full_command) > 100` AND `count >= 3` (configurable)
- Compute a factual re-transmission estimate: `total_chars × observed_compaction_multiplier_per_session`
  (this is a measurement, not a savings claim — it describes how many characters the model re-ingested)
- New dashboard page `/investigate/command-patterns`:
  - Cross-project table: pattern, occurrences, sessions, projects, avg length, cumulative re-transmitted characters
  - Drill-down to see raw invocations per pattern
  - Export as JSON/CSV for external analysis

**Value:** 🟠 **HIGH** — Makes repetition patterns measurable for the first time. No recommendation is produced;
the dashboard describes what was sent to the model, repeatedly.
**Effort:** M (~2h)
**Dependencies:** 8.2 (needs output tracking to also rank patterns by output cost)

### 8.6 Enhance `BuildAnalysisPrompt` to Use Hot-Spots (Observation Mode)

**Problem:** `internal/analysis/llm/analyzer.go::BuildAnalysisPrompt` dumps message distribution, tool
breakdown, first 5 user messages, last 10 messages, errors, files, capabilities — all in plain text,
~6-8k tokens per call. It misses the compaction and command output signals entirely, and its prompt
currently allows the LLM to drift into recommendation territory.

**What we'd build (observation-mode rewrite):**
- Inject `session_hotspots` JSON at the top of the prompt (compact, structured)
- Remove the redundant full message dump (last 10 messages) — agent can query them on demand via 8.4
- Rewrite the system prompt to enforce observation-only output:
  > *"You are a session observer. Describe observed facts about the session: token usage patterns,
  > compaction events with numbers, commands with their measured output sizes, repeated patterns with
  > their frequencies. Do NOT recommend actions, suggest refactors, propose tooling changes, or use
  > words like 'should', 'consider', 'recommend'. If the hot-spots data is insufficient, query the
  > investigation API endpoints for additional facts before responding."*
- Output schema — **observation fields only**, no recommendation fields:
  - `compaction_findings` — factual description of compactions (count, rate, drop sizes, context peaks)
  - `high_output_commands` — ranked list of commands by observed output bytes (no "waste" framing)
  - `observed_command_patterns` — repeated normalized commands with counts, lengths, session spread
  - `context_saturation` — max input tokens reached per assistant turn, binned
  - (Explicitly excluded: `suggestions`, `recommendations`, `cli_suggestions`, `fixes`, `waste_reduction_estimate`)
- Backwards compatible: existing fields stay, new fields are additive

**Value:** 🟡 **MEDIUM** — Analysis reports become structured observation records, ~40% cheaper,
and cannot drift into prescription. Users and external agents reading the reports decide what to do.
**Effort:** S (~1h)
**Dependencies:** 8.3 (hot-spots), ideally 8.4 (so LLM can drill down)

### 8.7 Compaction Cascade Notification (Fact Report)

**Problem:** Sessions with a high compaction rate (many compactions per user message) currently
generate no surface signal — no notification, no UI flag. There is no configurable fact-based
threshold for surfacing these patterns.

**What we'd build:**
- Scheduler task `CompactionCascadeDetectionTask` (runs after `HotspotsTask`)
- Trigger (pure numeric conditions, configurable): `compactions_per_user_message > 0.25`
  AND `total_compactions >= 3`
- New event type `EventCompactionCascade` fired via the existing notification service
- Dedup window: do not re-notify the same session within 24h
- **Notification template — facts only, no recommendations:**
  > *"Session `{id}` · project `{project}` · provider `{provider}`*
  > *Compactions: **{N}** across **{M}** user messages (rate: {rate}/msg)*
  > *Peak input tokens before compactions: {p1}, {p2}, {p3}, ... (model window: {model_window})*
  > *Rebuild cost: ${total_rebuild_cost} · Total tokens lost: {tokens_lost}*
  > *Details: {aisync_url}/sessions/{id}"*
- Template contains no "consider", "suggest", "recommend", or action verbs. Purely descriptive.
- Wire into existing Slack notification infrastructure (project channels)
- Include a **template-lint test** that greps the rendered message for forbidden prescription words
  (`should`, `consider`, `recommend`, `suggest`, `try`) and fails the test suite if any appear

> **Timing caveat:** Notifications are **forensic, not real-time**. The `CompactionCascadeDetectionTask`
> runs on a schedule (after `HotspotsTask`), so the notification arrives after the session has ended
> and tokens have already been spent. aisync is a downstream observation tool — it cannot intercept or
> prevent a running session's token spend. The notification's value is for pattern awareness across
> sessions, not for intervention on the current session. This should be documented in the notification
> message itself (e.g. include the session's end time, not just its start time).

**Value:** 🟡 **MEDIUM** — Surfaces high-compaction sessions in pure factual form, for post-session awareness.
**Effort:** S (~1h)
**Dependencies:** 8.1 (accurate detection), 8.3 (rate computation)

### 8.8 Per-Provider Context Usage Comparison

**Problem:** The 2026-04-05 investigation showed one `opencode` provider session reaching 168k–183k
tokens before compaction. Whether other providers (`claude-code`, `cursor`) exhibit similar, lower,
or higher peaks on comparable projects is currently unmeasurable — aisync has the per-message token
data but no cross-provider aggregation or comparative view.

**What we'd build (measurement-only, no verdict):**
- New analytics query: percentile distribution of `input_tokens` per assistant message, grouped by provider
- Metric: avg / p50 / p95 max context reached before compaction, per provider, per project
- Metric: mean context tokens per `tool_call` event, per provider (density of context per operation)
- Dashboard card on `/analytics`: "Provider Context Usage"
  - Bar chart: avg / p95 max context by provider
  - Bar chart: context tokens per tool_call by provider
  - Factual callout when delta > 2× between providers on the same project — e.g.
    *"On project `<name>`: provider `opencode` avg max context = 168k, provider `claude-code` avg max context = 94k (delta 1.79×)"*
  - No labels like "bloated", "efficient", "better". Just the numbers and the ratio.
- Per-project drilldown showing which sessions contributed to each provider's statistics

**Value:** 🟡 **MEDIUM** — Makes cross-provider context usage measurable and comparable on identical workloads.
**Effort:** M (~2h)
**Dependencies:** 8.2 (for the tool_call → output_bytes correlation)

---

### Section 8 — Suggested Phased Rollout

**Phase 1 — Critical Fixes (~1.5h)** ⚡ *Unblocks everything else*
- **8.1** Fix compaction threshold (XS, 30 min)
- **8.2** Command output bytes/tokens (S, 1h)
- → After Phase 1, re-run detection on the validation sessions: 4 compactions visible per session, command output sizes visible.

**Phase 2 — Level 1 Aggregates (~4h)**
- **8.3** Hot-spots precomputed table (M, 2h)
- **8.5** Command pattern dashboard (M, 2h)
- → After Phase 2, `/investigate/command-patterns` surfaces repeated commands across all sessions with measured counts and cumulative characters. Users interpret the data themselves.

**Phase 3 — Level 2 Agent Tooling (~3-4h)**
- **8.4** Investigation API endpoints (M, 2-3h)
- **8.6** Analysis prompt rewrite to observation mode (S, 1h)
- → After Phase 3, `analyze_daily` produces structured observation reports with `compaction_findings`, `high_output_commands`, `observed_command_patterns`, `context_saturation` fields. No recommendation fields.

**Phase 4 — Notifications & Comparisons (~3h)**
- **8.7** Compaction cascade fact notification (S, 1h)
- **8.8** Per-provider context usage comparison (M, 2h)
- → After Phase 4, high-compaction sessions surface via facts-only notifications, and cross-provider context usage is measurable on identical projects.

**Total estimated effort: ~11-12h across 4 phases.** Each phase delivers standalone measurement value.

**Every phase must pass the philosophy audit** (grep for forbidden words in templates/prompts/alerts).
See `TODO-section8.md` → Cross-Phase Tasks → Philosophy audit.

---

## 9. Actionable Diagnostics & Token Economy

> **Theme:** Transform aisync from a passive observation tool into a **diagnostic + prescription platform**.
> When `aisync inspect` identifies token economy problems (expensive screenshots, verbose command output,
> excessive compaction), it generates **provider-specific, ready-to-apply artefacts** — scripts, config
> patches, skills — that the user can review and install.
>
> ### Philosophy evolution: Detection → Detection + Prescription
>
> Section 8 established aisync as strictly observational. Section 9 adds a second layer: **generated
> artefacts that address observed problems**. The distinction is critical:
>
> - **Section 8** = the diagnostic report. Facts, numbers, ratios. No opinion.
> - **Section 9** = the prescription. Concrete, provider-specific fixes. Always opt-in (`--generate-fix`).
>   Never applied automatically. The user reviews and installs.
>
> This is the difference between a lab report (Section 8) and a doctor's prescription (Section 9).
> The lab report never says "take antibiotics". The prescription does, but the patient chooses.
>
> ### Provider-specific artefact targets
>
> Each fix is adapted to the user's provider ecosystem:
>
> | Provider | Agent instructions file | CLI/scripts location | Skills/commands | Config |
> |----------|------------------------|---------------------|-----------------|--------|
> | `opencode` | `AGENTS.md` | `.opencode/bin/` | `.opencode/skills/` | `.opencode/config.json` |
> | `claude-code` | `CLAUDE.md` | `.claude/commands/` | n/a | `.claude/settings.json` |
> | `cursor` | `.cursorrules` | n/a | n/a | `.cursor/settings.json` |
>
> aisync detects the provider from the session metadata and generates fixes targeting the right
> ecosystem. Cross-provider sessions get fixes for each detected provider.
>
> ### Triggering observation (2026-04-06)
>
> Investigation of `ses_2a6cb55f6ffeENWr6H1LnWdnLu` (3978 msgs, OpenCode, $360, 468M input tokens)
> revealed that **228 screenshot reads** consumed an estimated **27.1M billed input tokens** ($81 at
> Sonnet pricing) because each image (~1,500 tokens) stays in context for an average of **79 assistant
> turns** before the next compaction clears it. The screenshots were captured at 1000px resolution via
> `sips -Z 1000`; reducing to 500px and converting to JPEG Q50 would reduce per-image tokens by ~66%.
>
> No mechanism exists to:
> 1. Compress images before they enter the LLM context
> 2. Instruct the agent to describe-and-discard screenshots after reading them
> 3. Provide optimized capture scripts that the agent can discover and use
>
> These are all **solvable with provider-specific artefacts** that aisync can generate from diagnostic data.

### 9.1 `aisync inspect` — Unified Session Diagnostic CLI (✅ DONE)

**Status:** Implemented 2026-04-06. `pkg/cmd/inspectcmd/inspectcmd.go`, registered in root.go.

**What it does:**
- Single command `aisync inspect <session-id>` that produces a comprehensive analysis report.
- Four sections: **tokens** (input/output/cache/image breakdown per model), **images** (screenshot
  count, simctl captures, sips resizes, per-image billed tokens based on context duration until
  next compaction, estimated cost), **compactions** (events, cascades, detection coverage, interval
  stats), **commands** (total output bytes, top 20 commands by output size).
- Flags: `--json` (machine-readable), `--section tokens|images|compactions|commands` (limit output).
- All output is observational — facts, counts, ratios. No recommendations.

**Provider scope:** Works on all providers. Command output section falls back to direct ToolCall
scanning when session events are not yet backfilled (8.2). Image section detects both inline
`ImageMeta` and tool-read images (`.png`/`.jpg` via `read`/`mcp_read`).

### 9.2 Problem Detectors — Identify Actionable Issues

**Problem:** `aisync inspect` dumps raw data. A user (or LLM) must manually interpret it. We need
a layer that **identifies specific problems** from the diagnostic data and names them.

**What we'd build:**

A `ProblemDetector` pipeline that runs after `inspect` data collection and emits a list of
**named problems** with severity and quantified impact:

```go
// internal/diagnostic/detector.go

type Problem struct {
    ID          string  // e.g. "expensive-screenshots", "verbose-commands", "frequent-compaction"
    Severity    string  // "high", "medium", "low"
    Category    string  // "images", "commands", "compaction", "tokens"
    Provider    string  // detected provider
    Impact      string  // factual: "228 images × 79 avg turns × 1500 tok = 27.1M billed tokens ($81)"
    Observation string  // factual: "screenshots resized to 1000px, avg 79 assistant turns in context"
}
```

**Detectors (initial set):**

| Detector | Trigger | Impact formula |
|----------|---------|---------------|
| `expensive-screenshots` | `ToolReadImages > 10 AND AvgTurnsInCtx > 20` | images × avgTurns × tokPerImage × $rate |
| `oversized-screenshots` | `SimctlCaptures > 0 AND SipsResizes with -Z > 600` | token reduction from smaller size |
| `verbose-command-output` | `TopCommand.TotalBytes > 100KB` | outputBytes × contextTurns × $rate |
| `frequent-compaction` | `PerUserMsg > 0.2` | totalTokensLost × inputRate |
| `no-cache-utilization` | `CachePct < 50%` | cacheable tokens × (full_price - cache_price) |
| `large-context-at-compaction` | `AvgBeforeTokens > 0.85 × windowSize` | tokens wasted on near-limit context |

Each detector is **provider-agnostic** for the detection logic, but the **fix generation** (9.3) is
provider-specific.

**Effort:** M, 3h. **Priority:** HIGH — gates all fix generation.

### 9.3 Fix Generators — Provider-Specific Artefacts (✅ DONE)

**Status:** Implemented 2026-04-06. `internal/diagnostic/fixgen.go` + `fix.go`, integrated in
`pkg/cmd/inspectcmd/inspectcmd.go` via `--generate-fix` flag.

**What it does:**

For each detected problem, a **fix generator** produces ready-to-apply artefacts adapted to
the session's provider. Triggered via `aisync inspect --generate-fix <session-id>`.

**Fix: `expensive-screenshots` / `oversized-screenshots`**

Generated artefacts:

1. **Capture script** — `capture-screen.sh` (or provider equivalent):
   ```bash
   #!/bin/bash
   # Generated by aisync — optimized screenshot capture
   # Reduces token cost by ~66% (1500 → ~500 tokens per image)
   DEVICE="${DEVICE_ID:-booted}"
   OUT="${1:-/tmp/screenshot.png}"
   xcrun simctl io "$DEVICE" screenshot /tmp/_raw_capture.png
   sips -Z 500 /tmp/_raw_capture.png --out /tmp/_resized.png
   sips -s format jpeg -s formatOptions 50 /tmp/_resized.png --out "${OUT%.png}.jpg"
   rm -f /tmp/_raw_capture.png /tmp/_resized.png
   echo "${OUT%.png}.jpg"
   ```

2. **Agent instructions patch** — appended to AGENTS.md / CLAUDE.md:
   ```markdown
   ## Screenshot Protocol (generated by aisync)
   - Use ./capture-screen.sh instead of raw simctl + sips
   - After reading a screenshot, describe what you see in 2-3 sentences of text
   - Never re-read a screenshot you already described in the last 10 messages
   - Prefer JPEG over PNG for all screenshots
   - Target resolution: 500px max dimension (not 1000px)
   ```

3. **OpenCode skill** (if provider is `opencode`):
   ```
   .opencode/skills/screenshot-capture/
     SKILL.md      — instructions for the agent
     capture.sh    — the optimized script
   ```

4. **Claude Code command** (if provider is `claude-code`):
   ```
   .claude/commands/capture-screenshot.md
   ```

**Fix: `verbose-command-output`**

Generated artefacts:
- Agent instructions: "Pipe long command output through `head -100` or `tail -50`. Never run
  unbounded `cat` on files > 500 lines."
- For detected commands: specific alternatives (e.g. `pnpm test 2>&1 | tail -30` instead of
  bare `pnpm test`).

**Fix: `frequent-compaction`**

Generated artefacts:
- Agent instructions: "When context exceeds 150K tokens, proactively summarize the current state
  and start a new sub-task."
- Session splitting guide for the user.

**Output modes:**

| Flag | Behaviour |
|------|-----------|
| `--generate-fix` | Print fixes to stdout (review mode) |
| `--generate-fix --apply` | Write fixes to disk (creates files, appends to AGENTS.md) |
| `--generate-fix --json` | Structured JSON with file paths + content |

**Effort:** L, 6-8h across all generators. **Priority:** HIGH. **Status:** ✅ DONE.

### 9.4 Provider Documentation Links

**Problem:** Fix artefacts reference provider-specific concepts (skills, commands, config) but
users may not know how to install them.

**What we'd build:**

Each generated fix includes a **"Learn more"** section with links to the provider's documentation:

- OpenCode skills: link to OpenCode docs on skills directory structure
- Claude Code commands: link to Anthropic docs on `.claude/commands/`
- Cursor rules: link to Cursor docs on `.cursorrules`
- General: link to aisync's own docs on `inspect --generate-fix`

The links are provider-version-aware (fetched from a simple registry in
`internal/diagnostic/provider_docs.go`).

**Effort:** S, 1h. **Priority:** LOW — nice-to-have polish.

### 9.5 Historical Trend Detection

**Problem:** A single `inspect` is a snapshot. Users need to know if problems are **getting worse**
(e.g. screenshot count growing session over session) or **already fixed**.

**What we'd build:**

Compare current session's diagnostic data against the project's historical baseline:

```
Image cost: $81 (this session) vs $12 avg (project last 30 days) — 6.75× above baseline
Compaction rate: 0.152/user_msg vs 0.089 avg — 1.71× above baseline
Command output: 0 B (this session) vs 45KB avg — below baseline (good)
```

Uses the existing `session_events` and `session_analytics` tables. Only triggers problem
detectors when current session is **significantly above baseline** (>2× for images/commands,
>1.5× for compaction rate).

**Effort:** M, 3h. **Priority:** MEDIUM.

### 9.6 MCP Tool: `inspect_session`

**Problem:** When an agent (Claude Code, OpenCode) is analyzing sessions via MCP, it currently
calls multiple separate tools. A single `inspect_session` MCP tool would give the agent the
complete diagnostic in one call, including detected problems.

**What we'd build:**

New MCP tool `inspect_session` that returns the same JSON as `aisync inspect --json`, plus
the detected problems from 9.2. The agent can then:
1. Read the diagnostic
2. Identify the problems
3. Generate or apply fixes from its own context

This closes the loop: aisync provides the data, the agent acts on it.

**Effort:** S, 2h (wiring existing code to MCP handler). **Priority:** MEDIUM.

### Rollout

**Phase 1 — Foundation (9.1 ✅ + 9.2 ✅):** Inspect CLI + problem detectors.
- 9.1 `aisync inspect` CLI — ✅ DONE
- 9.2 Problem detectors — ✅ DONE (16 detectors)

**Phase 2 — Fixes (9.3 ✅):** Fix generators for each detected problem × provider.
- 12 fix generators covering images, compaction, commands, tokens, tool errors, patterns — ✅ DONE
- Provider-specific artefacts: OpenCode (AGENTS.md + skills), Claude Code (CLAUDE.md + commands), Cursor (.cursorrules)
- `--generate-fix` (review mode) + `--apply` (write to disk) + `--json` (structured output)

**Phase 3 — Intelligence (9.4 + 9.5):** Provider docs links + historical trend comparison.

**Phase 4 — Agent integration (9.6):** MCP tool for in-agent diagnostics.

**Total estimated effort: ~15-18h across 4 phases.** Phase 1-2 deliver immediate actionable value.

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

**Next focus → Section 8 (Investigation Tooling) + Section 9 (Actionable Diagnostics):**
- Section 8: 8 features for investigation tooling. Phase 1 (8.1 + 8.2) ✅ DONE. See [Section 8](#8-investigation-tooling--agent-support).
- Section 9: 6 features for actionable diagnostics & token economy. 9.1 ✅ + 9.2 ✅ + 9.3 ✅ DONE. See [Section 9](#9-actionable-diagnostics--token-economy).

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
