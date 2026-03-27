# aisync — Feature Backlog & Discussion

> Last updated: 2026-03-27 (Q1-Q5 research completed with production DB validation, leaderboard data captured)
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

## Priority Matrix

| Feature | Value | Effort | Dependencies | Priority |
|---------|-------|--------|-------------|----------|
| **1.2** Cross-Project MCP Matrix | HIGH | LOW | None | ⭐ P0 |
| **1.4** Harmonize Events + ClassifyTool | MEDIUM | LOW | None | ⭐ P0 |
| **Q2 fix** Wire MaxInput/OutputTokens | HIGH | TRIVIAL | None | ⭐ P0 |
| **1.1** Skill Context Footprint | HIGH | MEDIUM | None | ⭐ P1 |
| **5.1** Registry Persistence | HIGH | MEDIUM | None | ⭐ P1 |
| **2.1** Compaction Detection & Cost | VERY HIGH | MEDIUM | Research done ✅ | ⭐ P1 |
| **6.1** Aider Benchmark Integration | VERY HIGH | MEDIUM | LiteLLM catalog | ⭐ P1 |
| **1.3** Configured vs Used | HIGH | MEDIUM | 5.1 | P2 |
| **2.2** Context Saturation Forecast | HIGH | MEDIUM | 2.1, Q2 fix | P2 |
| **3.1** Model Context Efficiency | HIGH | MEDIUM | Q2 fix | P2 |
| **5.2** Cross-Project Dashboard | HIGH | MEDIUM | 5.1 | P2 |
| **6.2** Quality-Adjusted Cost (QAC) | HIGH | MEDIUM | 6.1 | P2 |
| **4.2** Token Waste Classification | HIGH | HIGH | 2.1 | P3 |
| **3.2** Model Overload Detection | VERY HIGH | HIGH | 2.1, 3.1 | P3 |
| **2.3** Session Freshness | HIGH | HIGH | 2.1 | P3 |
| **1.5** Skill Reuse Map | MEDIUM | LOW | 5.1 | P3 |
| **5.3** CLAUDE.md Impact Analysis | MEDIUM | MEDIUM | Research done ✅ | P3 |
| **3.3** Model Recommendation (old) | ~~MEDIUM~~ | ~~MEDIUM~~ | ~~3.1~~ | ~~P4~~ → merged into 6.x |
| **6.3** Multi-Benchmark Aggregation | MEDIUM | HIGH | 6.1 | P4 |
| **6.4** Model Fitness Profiles | HIGH | HIGH | 6.1, objectives | P4 |
| **4.1** Cache Miss Timeline | MEDIUM | LOW | None | P4 |

### Suggested Implementation Order

**Sprint A** (quick wins + foundation):
1. **Q2 fix** — Wire `MaxInputTokens`/`MaxOutputTokens` in LiteLLM adapter (trivial, 2 lines)
2. 1.4 — Harmonize events with ClassifyTool (LOW effort, unblocks better queries)
3. 1.2 — Cross-project MCP matrix (LOW effort, HIGH value, data exists)
4. 5.1 — Registry persistence (MEDIUM effort, unblocks 1.3, 1.5, 5.2)

**Sprint B** (compaction + skills):
5. 2.1 — Compaction detection (input_tokens drop heuristic, new fields)
6. 1.1 — Skill context footprint tracking
7. 2.2 — Context saturation forecast (requires Q2 fix + 2.1)

**Sprint C** (governance + model recommendations):
8. 1.3 — Configured vs Used analysis (requires 5.1)
9. 5.2 — Cross-project capabilities dashboard (requires 5.1)
10. **6.1 — Aider benchmark integration** (embed leaderboard data, cross-ref with LiteLLM pricing)
11. 3.1 — Model context efficiency score (uses Q2 fix data)

**Sprint D** (quality-adjusted analytics):
12. **6.2 — Quality-Adjusted Cost metric** (combines 6.1 benchmark + error classification)
13. 3.2 — Model overload detection
14. 4.2 — Token waste classification
15. 2.3 — Session freshness & diminishing returns

**Sprint E** (advanced, when data matures):
16. 6.3 — Multi-benchmark aggregation (multiple data sources)
17. 6.4 — Model fitness profiles (per-task-type recommendations)
18. 5.3 — CLAUDE.md impact analysis

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
