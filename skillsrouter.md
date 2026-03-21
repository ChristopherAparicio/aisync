# Skill Router & Observability — Specification

> **Destination**: AI-V project
> **Status**: Design spec — not implemented
> **Origin**: Observed during Omogen blue-green deployment session — agents repeatedly forgot to load relevant skills (e.g., `opencode-reference` when writing a skill, `django-migration-writer` when creating migrations)

## Problem Statement

Agents in OpenCode/Claude Code have access to a skill system (`.claude/skills/`, `.opencode/skills/`). Each skill is a SKILL.md file with domain-specific instructions (how to write migrations, how to write tests, code style rules, etc.).

**The agent must decide to load the skill itself.** The skill descriptions appear in the system prompt, but the agent frequently forgets to load the relevant skill, especially:
- Mid-conversation (context has shifted, agent forgets the skill list)
- When the task implicitly requires a skill (e.g., "add a field to Call" implicitly needs migration knowledge)
- When writing meta-artifacts (e.g., writing a skill without loading the skill-writing skill)

**Observed failure rate**: estimated 20-30% of sessions where a skill would have been useful but wasn't loaded. Needs measurement.

## Proposed Solution

Two components that work together:

### Component 1: Skill Recommender (real-time, per-message)

Analyzes each user message and suggests relevant skills. The suggestion is appended to the user message or injected as a system hint before the agent processes it.

**Output format** (appended to user message):
```
[Potentially relevant skills: django-migration-writer, before-commit]
```

The agent sees the suggestion and decides whether to load. No forced injection — the agent keeps control.

**Strategies** (progressive, start simple):

| Strategy | Latency | Cost | Expected recall |
|----------|---------|------|-----------------|
| Keyword matching | <1ms | Free | ~80% |
| Embedding cosine similarity | ~5ms | Free (local) | ~90% |
| LLM local (Qwen 2.5 7B via Ollama) | ~200ms | Free | ~95% |
| LLM API (Haiku/Flash) | ~500ms | ~$0.001/msg | ~98% |

**Start with keyword matching.** Only upgrade if benchmark shows insufficient recall.

### Component 2: Skill Observer (async, post-response)

Runs after each agent response. Analyzes what happened and logs discrepancies for learning.

**What it observes**:
- Which skills were recommended by the Recommender
- Which skills were actually loaded by the agent (via `load_skill()` / `skill()` tool calls)
- Whether the agent's output quality suffered from missing a skill (e.g., migration without blue-green comment, skill without frontmatter)

**What it produces**:
- Per-session report: recommended vs loaded vs missed
- Aggregated daily/weekly stats
- Actionable alerts when miss rate exceeds threshold

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        AI-V Platform                         │
│                                                              │
│  ┌──────────────┐     ┌──────────────┐     ┌─────────────┐ │
│  │   Session     │────▶│   Skill      │────▶│  Skill      │ │
│  │   Ingestion   │     │   Recommender│     │  Observer    │ │
│  │              │     │              │     │             │ │
│  │  Parse LLM   │     │  Keyword/    │     │  Compare    │ │
│  │  sessions    │     │  Embedding/  │     │  recommended│ │
│  │  from all    │     │  Qwen local  │     │  vs loaded  │ │
│  │  providers   │     │              │     │             │ │
│  └──────────────┘     └──────────────┘     └──────┬──────┘ │
│                                                    │        │
│                                              ┌─────▼──────┐ │
│                                              │  Analytics  │ │
│                                              │  & Alerts   │ │
│                                              │             │ │
│                                              │  Daily cron │ │
│                                              │  Thresholds │ │
│                                              │  Reports    │ │
│                                              └─────────────┘ │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                  Skills Registry                      │   │
│  │                                                       │   │
│  │  Per-project cache of:                                │   │
│  │  - Skill name + description                           │   │
│  │  - Keywords for matching                              │   │
│  │  - Embedding vector (optional)                        │   │
│  │  - Usage stats (loaded count, missed count)           │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

## Integration Points

### Option A: OpenCode Plugin (passive observation + hint)

```typescript
// .opencode/plugins/skill-router.ts
export const SkillRouter: Plugin = async ({ client }) => {
  const registry = await loadSkillsRegistry()
  let recommendations: string[] = []

  return {
    // When user submits a prompt, log what we would recommend
    "message.updated": async ({ event }) => {
      const msg = event.properties.message
      if (msg.role === "user") {
        recommendations = recommend(extractText(msg), registry)
        // Log recommendation for later analysis
        await logRecommendation(event.properties.sessionID, recommendations)
      }
    },

    // When session goes idle, compare recommendations vs actual skill loads
    "session.idle": async ({ event }) => {
      const messages = await client.session.messages({
        path: { id: event.properties.sessionID }
      })
      const loaded = extractSkillToolCalls(messages)
      const missed = recommendations.filter(s => !loaded.includes(s))
      const discovered = loaded.filter(s => !recommendations.includes(s))

      await logObservation({
        sessionID: event.properties.sessionID,
        recommended: recommendations,
        loaded,
        missed,
        discovered,
        timestamp: new Date().toISOString()
      })
    }
  }
}
```

**Limitation**: Cannot modify the user message before it reaches the agent. Can only observe.

### Option B: MCP Server (active recommendation)

```typescript
// mcp-server/src/index.ts — tool: suggest_skills
{
  name: "suggest_skills",
  description: "Analyze a task and suggest relevant skills to load",
  inputSchema: {
    type: "object",
    properties: {
      task_description: {
        type: "string",
        description: "What the user asked to do"
      }
    }
  },
  handler: async ({ task_description }) => {
    const recommendations = recommend(task_description, registry)
    if (recommendations.length === 0) {
      return "No specific skills needed for this task."
    }
    return `Recommended skills to load:\n${recommendations.map(s =>
      `- ${s}: ${registry[s].description}`
    ).join('\n')}`
  }
}
```

**Agent system prompt addition**:
```
Before starting any coding task, call suggest_skills() with the user's request.
Load the recommended skills before writing code.
```

**Advantage**: Works with OpenCode AND Claude Code (any MCP-compatible platform).

### Option C: Wrapper CLI (full control)

A CLI that wraps the OpenCode SDK. Intercepts the user message, enriches it, sends it.

```typescript
// wrapper/cli.ts
const userMessage = process.argv.slice(2).join(' ')
const recommendations = recommend(userMessage, registry)

let enrichedMessage = userMessage
if (recommendations.length > 0) {
  enrichedMessage += `\n\n[Potentially relevant skills: ${recommendations.join(', ')}]`
}

await client.session.prompt({
  path: { id: sessionId },
  body: {
    parts: [{ type: "text", text: enrichedMessage }]
  }
})
```

**Advantage**: Full control over the message. No dependency on hooks.
**Disadvantage**: Replaces the TUI interaction model.

## Skills Registry

Per-project file that caches skill metadata for fast matching.

```json
{
  "project": "omogen",
  "skills_dir": ".claude/skills",
  "last_scan": "2026-03-17T12:00:00Z",
  "skills": {
    "django-migration-writer": {
      "description": "Create DB migrations following blue-green safe patterns",
      "keywords": [
        "migration", "makemigrations", "migrate",
        "add field", "remove field", "rename field", "alter field",
        "add column", "drop column", "rename column",
        "schema change", "database change",
        "AddField", "RemoveField", "RenameField", "AlterField",
        "RunSQL", "RunPython"
      ],
      "trigger_patterns": [
        "add .+ to .+ model",
        "remove .+ from .+ model",
        "rename .+ column",
        "change .+ type",
        "modify .+ schema"
      ],
      "stats": {
        "recommended_count": 0,
        "loaded_count": 0,
        "missed_count": 0,
        "discovered_count": 0
      }
    },
    "opencode-reference": {
      "description": "Reference for creating OpenCode agents, skills, and commands",
      "keywords": [
        "skill", "agent", "command", "plugin",
        "SKILL.md", "frontmatter", "opencode config",
        "create a skill", "create an agent", "write a skill",
        "write an agent", "new skill", "new agent"
      ],
      "trigger_patterns": [
        "create .+ skill",
        "write .+ skill",
        "new .+ agent",
        "add .+ command"
      ],
      "stats": {
        "recommended_count": 0,
        "loaded_count": 0,
        "missed_count": 0,
        "discovered_count": 0
      }
    }
  }
}
```

The registry is auto-generated by scanning the skills directory and extracting keywords from each SKILL.md. Can be enriched manually with `trigger_patterns`.

## Cron Jobs & Alerting

### Daily Analysis (midnight cron)

```
Schedule: 0 0 * * *

1. Ingest all sessions from the last 24 hours
2. For each session:
   a. Extract user messages
   b. Run Recommender on each message
   c. Extract actual skill loads from agent responses
   d. Compute: recommended, loaded, missed, discovered
3. Aggregate stats
4. Check thresholds
5. Generate report
```

### Thresholds & Alerts

| Metric | Threshold | Alert |
|--------|-----------|-------|
| **Skill miss rate** (recommended but not loaded) | > 20% | Warning: agents are ignoring skill suggestions |
| **Skill miss rate** | > 40% | Critical: recommender or agent prompt needs fixing |
| **False positive rate** (recommended but irrelevant) | > 30% | Warning: keywords too broad, refine registry |
| **Discovery rate** (loaded but not recommended) | > 15% | Info: recommender missing patterns, add keywords |
| **Skill never loaded** (a skill exists but 0 loads in 30 days) | any | Info: dead skill, consider removing or fixing description |

### Weekly Report Format

```markdown
# Skill Router Report — Week of 2026-03-17

## Summary
- Sessions analyzed: 142
- Sessions with skill recommendations: 89 (63%)
- Overall skill load rate: 72% (recommendations acted on)
- Overall miss rate: 28%

## Top Missed Skills
| Skill | Recommended | Loaded | Missed | Miss Rate |
|-------|------------|--------|--------|-----------|
| before-commit | 45 | 28 | 17 | 38% |
| django-migration-writer | 23 | 15 | 8 | 35% |
| testing-conventions | 34 | 25 | 9 | 26% |

## Improvement Actions
- [ ] before-commit: miss rate too high (38%). Consider adding to agent system prompt as mandatory.
- [ ] django-migration-writer: add trigger pattern for "add .+ model" (3 misses from this pattern)

## New Patterns Discovered
- "refactor" → agents loaded code-style (not in keywords). Add to registry.
- "webhook" → agents loaded django-service-patterns. Add trigger pattern.
```

## Quality Rules (beyond skill loading)

The Observer can also check if the skill's rules were actually followed, not just whether it was loaded.

| Rule | How to detect | Severity |
|------|---------------|----------|
| Migration created without MIGRATIONS.md entry | Check if new migration file exists without corresponding doc entry | High |
| Migration with destructive op without `# bluegreen:` comment | Parse migration AST | High |
| Celery task arg without type hint | Parse task function AST | Medium |
| Celery task new arg without default | Compare with HEAD version | Medium |
| Skill created without frontmatter | Check SKILL.md starts with `---` | Medium |
| Commit without running lint | Check if lint was called before git commit | Low |

These rules can be checked by the **daily cron** by scanning git diffs of the day. If a rule is violated, it means either:
1. The skill was loaded but the agent ignored it → skill instructions unclear
2. The skill wasn't loaded → recommender/agent missed it

Both are actionable signals.

## Implementation Phases

### Phase 0: Measure the Problem (1 day)
- Export recent OpenCode sessions
- Manually tag 50 sessions: which skills should have been loaded?
- Calculate baseline miss rate
- **Decision gate**: if miss rate < 10%, stop. Problem isn't worth solving.

### Phase 1: Passive Observer Plugin (2 days)
- OpenCode plugin that logs recommendations vs actual loads
- Keyword-based recommender (no LLM)
- JSON log output for analysis
- Run for 1 week, collect data

### Phase 2: Active Recommender via MCP (2 days)
- MCP server with `suggest_skills` tool
- Add to agent system prompts: "call suggest_skills before coding"
- Measure improvement in skill load rate

### Phase 3: Quality Rules Observer (3 days)
- Daily cron that scans git diffs
- Check migration rules, celery rules, skill format rules
- Generate weekly report

### Phase 4: LLM-based Routing (optional, 2 days)
- Replace keyword matching with Qwen 2.5 via Ollama
- Only if keyword recall < 85%
- Benchmark: same test cases, compare precision/recall

### Phase 5: Feedback Loop (ongoing)
- Observer feeds missed patterns back to registry
- Auto-suggest new keywords based on discovered patterns
- Dashboard for miss rate trends over time

## Tech Stack

| Component | Tech | Why |
|-----------|------|-----|
| MCP Server | TypeScript + @modelcontextprotocol/sdk | Standard MCP, works everywhere |
| OpenCode Plugin | TypeScript | Native plugin system |
| Skills Registry | JSON file | Simple, versionable, no DB needed |
| Daily Cron | Python or TypeScript | Parse sessions, generate reports |
| Keyword Matching | TypeScript (regex) | Fast, zero dependencies |
| Embedding (optional) | Python + sentence-transformers | Local, free |
| LLM Routing (optional) | Ollama + Qwen 2.5 7B | Local, free, fast |
| Session Storage | AI-V existing storage | Already ingests LLM sessions |

## File Structure in AI-V

```
ai-v/
├── packages/
│   ├── skill-router/
│   │   ├── src/
│   │   │   ├── recommender/
│   │   │   │   ├── keyword.ts         # Keyword matching strategy
│   │   │   │   ├── embedding.ts       # Embedding similarity (optional)
│   │   │   │   └── llm.ts            # LLM routing via Ollama (optional)
│   │   │   ├── observer/
│   │   │   │   ├── plugin.ts          # OpenCode plugin (passive)
│   │   │   │   └── analyzer.ts        # Post-session analysis
│   │   │   ├── registry/
│   │   │   │   ├── scanner.ts         # Auto-scan skills dir → registry
│   │   │   │   └── types.ts           # Registry schema
│   │   │   ├── mcp-server/
│   │   │   │   └── index.ts           # MCP server with suggest_skills tool
│   │   │   └── quality/
│   │   │       ├── rules.ts           # Quality rules (migration, celery, etc.)
│   │   │       └── checker.ts         # Check rules against git diffs
│   │   ├── registry/
│   │   │   └── omogen.json            # Omogen skills registry
│   │   ├── benchmarks/
│   │   │   ├── test_cases.json        # message → expected skills
│   │   │   └── run.ts                # Benchmark runner
│   │   └── package.json
│   │
│   └── cron/
│       ├── daily-analysis.ts          # Nightly session analysis
│       ├── weekly-report.ts           # Weekly report generator
│       └── alert-rules.ts            # Threshold-based alerting
│
└── docs/
    └── skill-router.md               # This file
```

## Example: What Would Have Caught Today's Bug

During the blue-green deployment session, the agent was asked to create a skill (`django-migration-writer`). The recommender would have done:

```
User message: "create a skill for making migrations SQL in our project"

Keyword match:
  "skill" → opencode-reference ✓
  "migration" → django-migration-writer (already being created, skip)

Recommendation: [opencode-reference]

Agent response: created SKILL.md WITHOUT frontmatter (---name/description---)

Observer: skill loaded = [] (none loaded)
          recommended = [opencode-reference]
          missed = [opencode-reference]

Quality check: SKILL.md missing frontmatter → rule violation

Report: "Agent created a skill without loading opencode-reference.
         The skill is missing required frontmatter."
```

This is exactly what happened. The recommender + observer would have caught it.
