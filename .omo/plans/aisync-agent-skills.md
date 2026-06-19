# aisync Agent Skills — CLI-first Skill Bundle for OpenCode Agents

## TL;DR

> **Quick Summary**: Author a bundle of 3 CLI-first OpenCode `SKILL.md` skills that teach the user's agents to use the `aisync` CLI for finding sessions, reading stats, and analyzing session health — so a question like "which session worked on X?" reliably makes the agent reach for aisync.
>
> **Deliverables**:
> - `.opencode/skills/aisync-session-finder/SKILL.md` (list / search / blame / inspect / show / restore / resume)
> - `.opencode/skills/aisync-stats/SKILL.md` (stats / cost / tool-usage / efficiency / forecast / usage)
> - `.opencode/skills/aisync-analyze/SKILL.md` (analyze / diagnose / errors / validate / off-topic)
> - `make install-skills` target that symlinks the repo skills into `~/.config/opencode/skills/`
> - Documentation (repo `.opencode/skills/README.md` + a section in the main `README.md`)
>
> **Estimated Effort**: Short
> **Parallel Execution**: YES — 3 waves + final review wave
> **Critical Path**: Task 1 (capability matrix) → Tasks 3/4/5 (skills) → Task 6 (deploy) → F1–F4 → user okay

---

## Context

### Original Request
User saw a post: "If Stripe launched today its homepage wouldn't say `curl` — it would say `npx skills add https://docs.stripe.com`. The pitch is a skill: one line and any agent can use your product." User wants the equivalent idea applied **internally** for aisync: easy-to-add skills so the user's own OpenCode agents/sessions can use aisync effortlessly when the user asks questions.

### Interview Summary
**Key Discussions / Decisions**:
- Ambition: **internal only** — easily integrable `SKILL.md` files. NO public distribution, NO hosting endpoint, NO `npx`-installer.
- Count: a **bundle of 3 focused skills**, not one mega-skill.
- Skill 1: `aisync-session-finder` (search/retrieval). Skill 2: `aisync-stats` (analytics/cost). Skill 3: `aisync-analyze` (health/debug).
- Mechanism: **CLI-first** (drive the `aisync` binary); mention MCP `aisync_*` tools only as a fallback when configured.
- Location: authored & **versioned in the aisync repo** (`.opencode/skills/`) **and documented**, plus **deployed to global** `~/.config/opencode/skills/`.

**Research Findings**:
- No aisync product-skill exists. Existing `aisync skills` / `skill-observe` are the inverse concept (improving/observing how agents load skills) — reusable for QA, not the deliverable.
- CLI surface verified via `--help`: `list` (filters `--search` FTS5, `--type`, `--since/--until`, `--branch`, `--project`, `--provider`, `--remote`, `--pr`, `--tag`, `--similar`, `--off-topic`, `--tree`, `--me/--user`, `--json`, `--quiet`), `blame`, `inspect`, `show`, `restore`, `resume`, `stats`, `usage`, `tool-usage`, `efficiency`, `analyze`, `diagnose`, `errors`, `validate`, `diff`, `replay`, `rewind`.
- MCP server `aisync mcp` exposes ~40 `aisync_*` tools (not exposed in the current OpenCode session → CLI is the safe default).
- **KNOWN BUG**: aisync FTS5 `--search` is unreliable (content truncated at 50k, OR operator broken, no child→parent propagation). A SEPARATE plan (`.omo/plans/ai5-search-architecture.md`) fixes the engine. This bundle must NOT rely on `--search` alone — it leans on metadata filters + `blame` + `inspect`/`show`, and documents the limitation.
- SKILL.md format reference: `~/.config/opencode/skills/opencode-sessions/SKILL.md` (frontmatter `name` + `description`, then markdown body with command recipes/tables) and `~/.config/opencode/skills/replay-tester/SKILL.md`.
- QA against real DB `~/.aisync/sessions.db` (3682 sessions). Binary: `/Users/guardix/go/bin/aisync`.

### Metis Review
**Identified Gaps (addressed)**:
- G1 Deployment copy-vs-symlink → **symlink** via `make install-skills` (repo = source of truth, no drift).
- G2 Naming → keep `aisync-session-finder` / `aisync-stats` / `aisync-analyze` (descriptive, user-accepted).
- G3 `skillsrouter.md` → leave untouched (unrelated design spec).
- G4 `efficiency`/`--cost` may need LLM/auth/pricing → documented as **optional**; QA must not hard-fail on them.
- G5 Repo↔global staleness → symlink solves it; documented.
- G6 `diff`/`replay`/`rewind` → **out of scope**; mention "see `aisync --help`".
- Trigger descriptions must embed concrete user-question phrasings (generic descriptions = 20–30% skill-load miss rate per the user's own `skillsrouter.md`).

---

## Work Objectives

### Core Objective
Ship 3 CLI-first `SKILL.md` skills (+ symlink deploy target + docs) that make aisync trivially usable by the user's OpenCode agents, with descriptions tuned so agents auto-load them at the right moment.

### Concrete Deliverables
- 3 `SKILL.md` files under `.opencode/skills/{name}/SKILL.md` in the aisync repo.
- `make install-skills` (+ optional `uninstall-skills`) Makefile target — symlinks each skill dir into `~/.config/opencode/skills/`.
- `.opencode/skills/README.md` (bundle doc) + a "Agent Skills" section in the main `README.md`.

### Definition of Done
- [ ] All 3 skills exist with valid frontmatter and run their documented recipes successfully against `~/.aisync/sessions.db`.
- [ ] `make install-skills` creates working symlinks in `~/.config/opencode/skills/` (verified by `ls -la`).
- [ ] Each skill's command recipes produce correct output on a real session ID (evidence captured).
- [ ] Docs exist and list all 3 skills + install instructions.

### Must Have
- CLI-first recipes that work without MCP configured.
- Frontmatter `description` with explicit trigger phrasings ("which session worked on…", "how many tokens…", "why did this session fail…").
- Explicit note in `aisync-session-finder` that `--search` (FTS5) is currently unreliable → prefer metadata filters + `blame` + `inspect`/`show`.
- Every skill recipe uses real, runnable commands (no invented flags).

### Must NOT Have (Guardrails)
- NO 4th skill; NO splitting into more than 3.
- NO changes to aisync Go source (unless a preflight finds a recipe-blocking bug → then DOCUMENT it, do not fix here).
- NO FTS5 engine fix (separate plan).
- NO public distribution, hosting endpoint, or `npx` installer.
- NO deep docs for `diff` / `replay` / `rewind` / `usage compute` (mention + defer to `--help`).
- NO invented CLI flags — every flag must appear in `aisync <cmd> --help`.
- Each `SKILL.md` ≤ ~350 lines.

---

## Verification Strategy (MANDATORY)

> **ZERO HUMAN INTERVENTION** — all verification is agent-executed. No "user manually confirms".

### Test Decision
- **Infrastructure exists**: N/A (deliverables are markdown skills + a Makefile target, not application code).
- **Automated tests**: None (no unit tests for SKILL.md content).
- **Framework**: none.
- **Primary verification**: agent-executed QA — run each skill's documented recipes against the real `~/.aisync/sessions.db` and assert correct output.
- **Optional objective trigger check**: `aisync skill-observe <session-id>` to confirm a skill would be *recommended* for representative questions (coverage signal, non-blocking).

### QA Policy
Every task includes agent-executed QA scenarios. Evidence saved to `.omo/evidence/task-{N}-{scenario-slug}.{ext}`.
- **CLI**: Use `Bash` to run `aisync …`, parse stdout/`--json`, assert fields + exit code.
- **Filesystem (symlinks)**: Use `Bash` (`ls -la`, `readlink`) to assert symlink targets.

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (Start Immediately — foundation):
├── Task 1: Preflight capability matrix (validate A1–A4, probe every command) [unspecified-low]
└── Task 2: Repo skills scaffold + frontmatter template + naming [quick]

Wave 2 (After Wave 1 — author the 3 skills, MAX PARALLEL):
├── Task 3: aisync-session-finder SKILL.md (depends: 1, 2) [writing]
├── Task 4: aisync-stats SKILL.md (depends: 1, 2) [writing]
└── Task 5: aisync-analyze SKILL.md (depends: 1, 2) [writing]

Wave 3 (After Wave 2 — deploy + document):
├── Task 6: make install-skills symlink target + global deploy (depends: 3, 4, 5) [quick]
└── Task 7: Documentation — skills/README.md + main README section (depends: 3, 4, 5) [writing]

Wave FINAL (After ALL tasks — 4 parallel reviews, then user okay):
├── Task F1: Plan compliance audit (oracle)
├── Task F2: Skill content quality + recipe validity (unspecified-high)
├── Task F3: Real agent QA + trigger check (unspecified-high)
└── Task F4: Scope fidelity check (deep)
-> Present results -> Get explicit user okay

Critical Path: Task 1 → Tasks 3/4/5 → Task 6 → F1–F4 → user okay
Max Concurrent: 3 (Wave 2)
```

### Dependency Matrix

- **1**: depends — None | blocks — 3, 4, 5
- **2**: depends — None | blocks — 3, 4, 5
- **3**: depends — 1, 2 | blocks — 6, 7
- **4**: depends — 1, 2 | blocks — 6, 7
- **5**: depends — 1, 2 | blocks — 6, 7
- **6**: depends — 3, 4, 5 | blocks — F1–F4
- **7**: depends — 3, 4, 5 | blocks — F1–F4

### Agent Dispatch Summary

- **Wave 1**: 2 — T1 → `unspecified-low`, T2 → `quick`
- **Wave 2**: 3 — T3/T4/T5 → `writing` (load_skills: `customize-opencode`)
- **Wave 3**: 2 — T6 → `quick`, T7 → `writing`
- **FINAL**: 4 — F1 → `oracle`, F2 → `unspecified-high`, F3 → `unspecified-high`, F4 → `deep`

---

## TODOs

> Implementation + verification = ONE task. EVERY task has Agent Profile + Parallelization + QA Scenarios.
> Task labels use bare numbers (`1.`, `2.`). Final wave uses `F1.`, `F2.`.

- [x] 1. Preflight capability matrix — validate A1–A4 and probe every command

  **What to do**:
  - Confirm A1: `command -v aisync` resolves (expected `/Users/guardix/go/bin/aisync`); capture version with `aisync --version`.
  - Probe every command the bundle will reference and record real `--help` flag surface: `list`, `blame`, `inspect`, `show`, `restore`, `resume`, `stats`, `usage`, `tool-usage`, `efficiency`, `analyze`, `diagnose`, `errors`, `validate`, `skill-observe`.
  - Validate A2: run `aisync stats --json` and an `aisync efficiency`/cost-style command; note whether they succeed offline or require auth/pricing (`aisync update-prices`). Mark cost/efficiency recipes as OPTIONAL if they error.
  - Validate A4: pick a real session id via `aisync list --quiet | head -1`, then exercise metadata filters (`--type`, `--since`, `--branch`, `--project`, `--provider`, `--tag`, `--off-topic`) and confirm they return without FTS errors. Run one `--search` query and record its (unreliable) behavior as evidence for the documented limitation.
  - Validate A3: check OpenCode skill frontmatter multi-line YAML support by inspecting `~/.config/opencode/skills/opencode-sessions/SKILL.md` and `~/.config/opencode/skills/replay-tester/SKILL.md` frontmatter shape.
  - Produce a short capability-matrix note: `.omo/evidence/task-1-capability-matrix.md` listing per-command: WORKS / OPTIONAL (auth) / AVOID (buggy), with the exact verified flags each downstream skill may use.

  **Must NOT do**:
  - Do NOT fix any bug found (FTS or otherwise) — only DOCUMENT it in the matrix.
  - Do NOT modify aisync Go source or run `aisync update-prices` as a fix; only note if it's required.
  - Do NOT invent flags — record only flags that appear in real `--help` output.

  **Recommended Agent Profile**:
  - **Category**: `unspecified-low`
    - Reason: Mechanical probing + recording; no architectural judgment, but spans many commands so not "quick".
  - **Skills**: none
    - No domain skill needed for running CLI probes.
  - **Skills Evaluated but Omitted**:
    - `customize-opencode`: this task writes an evidence note, not a SKILL.md, so authoring guidance is irrelevant here.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with Task 2)
  - **Blocks**: Tasks 3, 4, 5
  - **Blocked By**: None (can start immediately)

  **References**:

  **Pattern References**:
  - `README.md:Commands table` — canonical list of commands the bundle wraps; cross-check names exist.
  - `~/.config/opencode/skills/opencode-sessions/SKILL.md` — real example of CLI-recipe skill + frontmatter shape (A3 check).

  **API/Type References**:
  - `aisync list --help`, `aisync stats --help`, `aisync analyze --help` (live output) — authoritative flag surface; the ONLY source of truth for recipes.

  **External References**:
  - none (internal CLI only).

  **WHY Each Reference Matters**:
  - The `--help` output is the contract every downstream skill recipe must match — extract exact flag spellings, not remembered ones.
  - The example SKILL.md confirms whether multi-line YAML descriptions parse (A3), which dictates how Tasks 3–5 write frontmatter.

  **Acceptance Criteria**:

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: Every referenced command exists and exposes documented flags
    Tool: Bash
    Preconditions: aisync on PATH; ~/.aisync/sessions.db populated (3682 sessions)
    Steps:
      1. Run: command -v aisync  (assert: prints a path, exit 0)
      2. For each cmd in [list blame inspect show restore resume stats usage tool-usage efficiency analyze diagnose errors validate skill-observe]: run `aisync $cmd --help` and assert exit 0 and no "unknown command"
      3. Capture each help block into .omo/evidence/task-1-capability-matrix.md
    Expected Result: All 15 commands resolve; matrix note lists verified flags per command
    Failure Indicators: any "unknown command" / nonzero exit for a command the bundle plans to use
    Evidence: .omo/evidence/task-1-capability-matrix.md

  Scenario: Cost/efficiency offline behavior + FTS unreliability are recorded
    Tool: Bash
    Preconditions: real session id from `aisync list --quiet | head -1`
    Steps:
      1. Run `aisync efficiency` (or cost variant); record success OR auth/pricing error verbatim
      2. Run `aisync list --search "<two words with implied OR>"`; record whether results are correct/truncated
      3. Run a metadata filter (e.g. `aisync list --provider opencode --limit 5 --json`) and assert clean output
    Expected Result: matrix marks efficiency/cost as WORKS or OPTIONAL(auth); --search marked AVOID with evidence; metadata filters marked WORKS
    Failure Indicators: metadata filters error out (would invalidate the session-finder approach → escalate)
    Evidence: .omo/evidence/task-1-cost-and-search-probe.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-1-capability-matrix.md`
  - [ ] `.omo/evidence/task-1-cost-and-search-probe.txt`

  **Commit**: NO (analysis note only; not committed to repo)

- [x] 2. Repo skills scaffold + frontmatter template

  **What to do**:
  - Create the three skill directories in the aisync repo: `.opencode/skills/aisync-session-finder/`, `.opencode/skills/aisync-stats/`, `.opencode/skills/aisync-analyze/`.
  - In each, create a placeholder `SKILL.md` with valid frontmatter (`name` + `description`) and an empty body section — so directory structure + frontmatter shape is locked before authoring.
  - Mirror the frontmatter style verified in Task 1 (A3): use the same YAML shape as `opencode-sessions`/`replay-tester` skills (single-line or block-scalar `description`, whichever parses).
  - Ensure `name` values exactly equal the directory names (OpenCode convention).

  **Must NOT do**:
  - Do NOT write full recipes yet (that's Tasks 3–5) — scaffold + frontmatter only.
  - Do NOT create a 4th skill directory.
  - Do NOT symlink to global yet (that's Task 6).

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: Trivial directory + 3 stub files, clear and mechanical.
  - **Skills**: none
    - Stub creation needs no domain guidance; full authoring guidance lands in Tasks 3–5.
  - **Skills Evaluated but Omitted**:
    - `customize-opencode`: deferred to authoring tasks; overkill for stubs.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with Task 1)
  - **Blocks**: Tasks 3, 4, 5
  - **Blocked By**: None (can start immediately)

  **References**:

  **Pattern References**:
  - `~/.config/opencode/skills/opencode-sessions/SKILL.md:1-10` — exact frontmatter block to mirror (`name`, `description`).
  - `~/.config/opencode/skills/replay-tester/SKILL.md:1-10` — second frontmatter example to confirm consistent shape.

  **WHY Each Reference Matters**:
  - These two files are the only known-good OpenCode skill frontmatter examples; copying their shape avoids load-time parse failures.

  **Acceptance Criteria**:

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: Three skill dirs exist with parseable frontmatter
    Tool: Bash
    Preconditions: repo root /Users/guardix/dev/aisync
    Steps:
      1. Run: ls -d .opencode/skills/aisync-session-finder .opencode/skills/aisync-stats .opencode/skills/aisync-analyze (assert all 3 exist, exit 0)
      2. For each, run: head -5 .opencode/skills/$name/SKILL.md and assert frontmatter has `name:` matching dir + non-empty `description:`
      3. Validate YAML frontmatter parses (python3 -c with yaml, or a simple awk delimiter check)
    Expected Result: 3 dirs, 3 stub SKILL.md, each with valid name+description frontmatter
    Failure Indicators: missing dir, name≠dirname, empty description, unparseable YAML
    Evidence: .omo/evidence/task-2-scaffold.txt

  Scenario: No 4th skill dir was created
    Tool: Bash
    Preconditions: scaffold complete
    Steps:
      1. Run: ls .opencode/skills/ | grep -c aisync- (assert == 3)
    Expected Result: exactly 3 aisync- skill dirs
    Failure Indicators: count != 3 (scope violation)
    Evidence: .omo/evidence/task-2-count.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-2-scaffold.txt`
  - [ ] `.omo/evidence/task-2-count.txt`

  **Commit**: YES (groups with Task 3, or standalone)
  - Message: `chore(skills): scaffold aisync agent skill bundle`
  - Files: `.opencode/skills/aisync-session-finder/SKILL.md`, `.opencode/skills/aisync-stats/SKILL.md`, `.opencode/skills/aisync-analyze/SKILL.md`
  - Pre-commit: `ls -d .opencode/skills/aisync-*`

- [x] 3. Author `aisync-session-finder/SKILL.md` (list / search / blame / inspect / show / restore / resume)

  **What to do**:
  - Write the full skill body: a routing table mapping user-question phrasings → the right `aisync` command, plus copy-paste recipes for each command verified in Task 1.
  - Frontmatter `description` MUST embed concrete trigger phrasings: "which session worked on X", "find the session that touched this file", "restore/resume a past session", "show me what session <id> did".
  - Recipes to cover: `aisync list` (+ metadata filters `--type/--since/--until/--branch/--project/--provider/--tag/--off-topic/--quiet/--json`), `aisync blame <file>`, `aisync inspect <id>`, `aisync show <id>`, `aisync restore`, `aisync resume <branch>`.
  - Include an explicit **LIMITATION** callout: `--search` (FTS5) is currently unreliable (truncation at 50k, broken OR, no child→parent) → prefer metadata filters + `blame` + `inspect`/`show`. (Source: Task 1 evidence + `.omo/plans/ai5-search-architecture.md`.)
  - Mention MCP `aisync_list`/`aisync_blame`/`aisync_search` as a fallback only when MCP is configured.
  - Keep ≤ ~350 lines.

  **Must NOT do**:
  - Do NOT recommend `--search` as the primary retrieval path.
  - Do NOT document `diff`/`replay`/`rewind` deeply (one-line "see `aisync --help`" at most).
  - Do NOT invent flags absent from Task 1's matrix.

  **Recommended Agent Profile**:
  - **Category**: `writing`
    - Reason: Core output is well-structured technical prose (routing table + recipes + limitation note).
  - **Skills**: `customize-opencode`
    - `customize-opencode`: authoritative on OpenCode SKILL.md authoring (frontmatter, description tuning, body conventions) — directly overlaps.
  - **Skills Evaluated but Omitted**:
    - `opencode-sessions`: it's a reference example to read, not a skill to load for authoring.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 2 (with Tasks 4, 5)
  - **Blocks**: Tasks 6, 7
  - **Blocked By**: Tasks 1, 2

  **References**:

  **Pattern References**:
  - `~/.config/opencode/skills/opencode-sessions/SKILL.md` — closest analog (session-finding via CLI); mirror its routing-table + recipe layout.
  - `.omo/evidence/task-1-capability-matrix.md` — verified flag surface for list/blame/inspect/show/restore/resume.

  **API/Type References**:
  - `aisync list --help`, `aisync blame --help`, `aisync inspect --help`, `aisync show --help` (live) — exact flags.

  **External References**:
  - `.omo/plans/ai5-search-architecture.md` — source/rationale for the `--search` limitation callout.

  **WHY Each Reference Matters**:
  - opencode-sessions shows the proven shape an agent reliably follows; reusing it lowers load-miss + execution error.
  - The Task 1 matrix is the guardrail against invented flags — every recipe must trace to it.

  **Acceptance Criteria**:

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: Routing question → list/filter returns a real session
    Tool: Bash
    Preconditions: skill written; ~/.aisync/sessions.db has 3682 sessions
    Steps:
      1. Take the skill's recipe for "find sessions on a branch": run it (e.g. aisync list --branch main --limit 5 --json)
      2. Assert exit 0 and JSON array with ≥1 element having a session id field
      3. Take the `blame` recipe with a real repo file; assert it returns session ids or a clean "no sessions" message
    Expected Result: both recipes run verbatim and return correct, parseable output
    Failure Indicators: unknown flag, nonzero exit, empty/garbled output where data exists
    Evidence: .omo/evidence/task-3-finder-happy.txt

  Scenario: --search limitation is documented and not the primary path
    Tool: Bash
    Preconditions: skill written
    Steps:
      1. grep -i "search" .opencode/skills/aisync-session-finder/SKILL.md → assert a LIMITATION/unreliable note exists
      2. Assert the primary "find" recipe uses metadata filters or blame, NOT --search (grep ordering / explicit wording)
      3. wc -l SKILL.md → assert ≤ 350
    Expected Result: limitation callout present; primary path is metadata/blame; file ≤350 lines
    Failure Indicators: --search presented as primary; no limitation note; >350 lines
    Evidence: .omo/evidence/task-3-finder-limitation.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-3-finder-happy.txt`
  - [ ] `.omo/evidence/task-3-finder-limitation.txt`

  **Commit**: YES
  - Message: `feat(skills): add aisync-session-finder skill`
  - Files: `.opencode/skills/aisync-session-finder/SKILL.md`
  - Pre-commit: run the two QA recipes above

- [x] 4. Author `aisync-stats/SKILL.md` (stats / cost / tool-usage / efficiency / forecast / usage)

  **What to do**:
  - Write the full skill body: routing table (question → command) + recipes for `aisync stats` (+`--json`), `aisync tool-usage`, `aisync usage`, `aisync forecast`, and `aisync efficiency`/cost.
  - Frontmatter `description` MUST embed trigger phrasings: "how many tokens did this session use", "what did this cost", "which tools were used most", "forecast my usage", "project/branch usage stats".
  - Mark `efficiency`/cost recipes as **OPTIONAL** per Task 1 (A2): note they may require auth/pricing (`aisync update-prices`) and degrade gracefully.
  - Mention MCP `aisync_stats` as fallback when configured.
  - Keep ≤ ~350 lines.

  **Must NOT do**:
  - Do NOT deep-doc `usage compute` internals (mention + defer to `--help`).
  - Do NOT present cost/efficiency as guaranteed-offline if Task 1 found they need pricing data.
  - Do NOT invent flags.

  **Recommended Agent Profile**:
  - **Category**: `writing`
    - Reason: Structured technical prose; recipe + routing authoring.
  - **Skills**: `customize-opencode`
    - `customize-opencode`: SKILL.md authoring conventions + description tuning — direct overlap.
  - **Skills Evaluated but Omitted**:
    - none.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 2 (with Tasks 3, 5)
  - **Blocks**: Tasks 6, 7
  - **Blocked By**: Tasks 1, 2

  **References**:

  **Pattern References**:
  - `~/.config/opencode/skills/opencode-sessions/SKILL.md` — recipe/routing layout to mirror.
  - `.omo/evidence/task-1-capability-matrix.md` — verified flags + which stats commands are OPTIONAL(auth).

  **API/Type References**:
  - `aisync stats --help`, `aisync tool-usage --help`, `aisync usage --help`, `aisync efficiency --help` (live) — exact flags.

  **WHY Each Reference Matters**:
  - The matrix dictates which recipes get the OPTIONAL caveat (cost/efficiency) so the skill never hard-promises an offline-impossible result.

  **Acceptance Criteria**:

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: stats recipe returns parseable token/usage numbers
    Tool: Bash
    Preconditions: skill written; populated DB
    Steps:
      1. Run the skill's primary stats recipe (e.g. aisync stats --json)
      2. Assert exit 0 and JSON containing token/session count fields
      3. Run the tool-usage recipe; assert a ranked tool list or clean empty-state
    Expected Result: both recipes run verbatim and return correct output
    Failure Indicators: unknown flag, nonzero exit, no numeric fields where data exists
    Evidence: .omo/evidence/task-4-stats-happy.txt

  Scenario: cost/efficiency marked OPTIONAL and degrades gracefully
    Tool: Bash
    Preconditions: skill written
    Steps:
      1. grep -i "optional\|pricing\|update-prices" .opencode/skills/aisync-stats/SKILL.md → assert caveat present near cost/efficiency
      2. Run the efficiency/cost recipe; whether it succeeds or errors-for-auth, assert the skill's documented expectation matches reality
      3. wc -l SKILL.md → assert ≤ 350
    Expected Result: cost/efficiency caveat present; behavior matches doc; ≤350 lines
    Failure Indicators: cost presented as guaranteed; behavior contradicts doc; >350 lines
    Evidence: .omo/evidence/task-4-stats-optional.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-4-stats-happy.txt`
  - [ ] `.omo/evidence/task-4-stats-optional.txt`

  **Commit**: YES
  - Message: `feat(skills): add aisync-stats skill`
  - Files: `.opencode/skills/aisync-stats/SKILL.md`
  - Pre-commit: run the two QA recipes above

- [x] 5. Author `aisync-analyze/SKILL.md` (analyze / diagnose / errors / validate / off-topic)

  **What to do**:
  - Write the full skill body: routing table (question → command) + recipes for `aisync analyze <id>`, `aisync diagnose <id>`, `aisync errors`, `aisync validate`, and off-topic detection (`aisync list --off-topic` and/or `analyze` off-topic signal).
  - Frontmatter `description` MUST embed trigger phrasings: "why did this session fail", "diagnose what went wrong", "find sessions with errors", "validate this session/db", "did this session go off-topic".
  - Per Task 1 (A2): if `analyze`/`diagnose` require LLM/auth, mark those recipes **OPTIONAL** and document graceful degradation; keep `errors`/`validate`/`--off-topic` as the offline-safe core.
  - Mention MCP `aisync_explain`/analysis tools as fallback when configured.
  - Keep ≤ ~350 lines.

  **Must NOT do**:
  - Do NOT promise LLM-backed `analyze`/`diagnose` work offline if Task 1 shows they need auth.
  - Do NOT add a 4th concern outside health/debug.
  - Do NOT invent flags.

  **Recommended Agent Profile**:
  - **Category**: `writing`
    - Reason: Structured technical prose; recipe + routing authoring.
  - **Skills**: `customize-opencode`
    - `customize-opencode`: SKILL.md authoring + description tuning — direct overlap.
  - **Skills Evaluated but Omitted**:
    - none.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 2 (with Tasks 3, 4)
  - **Blocks**: Tasks 6, 7
  - **Blocked By**: Tasks 1, 2

  **References**:

  **Pattern References**:
  - `~/.config/opencode/skills/opencode-sessions/SKILL.md` — recipe/routing layout to mirror.
  - `.omo/evidence/task-1-capability-matrix.md` — which analyze/diagnose recipes are OPTIONAL(auth) vs offline-safe.

  **API/Type References**:
  - `aisync analyze --help`, `aisync diagnose --help`, `aisync errors --help`, `aisync validate --help` (live) — exact flags.

  **WHY Each Reference Matters**:
  - The matrix separates LLM-dependent commands (caveat needed) from offline-safe ones (the reliable core), so the skill routes to working commands first.

  **Acceptance Criteria**:

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: offline-safe health recipe returns useful output
    Tool: Bash
    Preconditions: skill written; populated DB; real session id from `aisync list --quiet | head -1`
    Steps:
      1. Run the skill's `errors` recipe (e.g. aisync errors --limit 5) → assert exit 0, parseable list or clean empty-state
      2. Run `aisync validate <id>` (or db validate) → assert exit 0 and a pass/fail report
      3. Run the off-topic recipe (aisync list --off-topic --limit 5) → assert exit 0
    Expected Result: all three offline-safe recipes run verbatim and return correct output
    Failure Indicators: unknown flag, nonzero exit on offline-safe commands
    Evidence: .omo/evidence/task-5-analyze-happy.txt

  Scenario: LLM-dependent analyze/diagnose marked OPTIONAL + matches reality
    Tool: Bash
    Preconditions: skill written
    Steps:
      1. grep -i "optional\|llm\|auth\|api key" .opencode/skills/aisync-analyze/SKILL.md → assert caveat near analyze/diagnose
      2. Run `aisync analyze <id>`; whether it succeeds or errors-for-auth, assert documented expectation matches
      3. wc -l SKILL.md → assert ≤ 350
    Expected Result: analyze/diagnose caveat present; behavior matches doc; ≤350 lines
    Failure Indicators: analyze presented as guaranteed-offline; behavior contradicts doc; >350 lines
    Evidence: .omo/evidence/task-5-analyze-optional.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-5-analyze-happy.txt`
  - [ ] `.omo/evidence/task-5-analyze-optional.txt`

  **Commit**: YES
  - Message: `feat(skills): add aisync-analyze skill`
  - Files: `.opencode/skills/aisync-analyze/SKILL.md`
  - Pre-commit: run the two QA recipes above

- [x] 6. Add `make install-skills` symlink target + deploy to global

  **What to do**:
  - Add a `install-skills` target to the repo `Makefile` that symlinks each `.opencode/skills/aisync-*` directory into `~/.config/opencode/skills/` (use `ln -sfn` with absolute repo paths so re-runs are idempotent).
  - Add a matching `uninstall-skills` target that removes only those symlinks (never `rm -rf` a real dir — guard against removing a non-symlink).
  - Run `make install-skills` to actually deploy; verify all 3 symlinks resolve to the repo dirs.
  - Keep targets POSIX/macOS-safe (darwin); create `~/.config/opencode/skills/` if missing.

  **Must NOT do**:
  - Do NOT copy files (decision = symlink; repo is source of truth).
  - Do NOT delete or overwrite non-symlink entries in the global skills dir.
  - Do NOT touch unrelated Makefile targets.

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: Two small, well-specified Makefile targets + a verification run.
  - **Skills**: none
    - Plain Makefile/shell; no domain skill required.
  - **Skills Evaluated but Omitted**:
    - `customize-opencode`: this configures deployment, not authoring SKILL.md content.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 3 (with Task 7)
  - **Blocks**: F1–F4
  - **Blocked By**: Tasks 3, 4, 5

  **References**:

  **Pattern References**:
  - `Makefile` (repo root) — match existing target style (`install`, `build`, `test`) for naming + phony declarations.

  **WHY Each Reference Matters**:
  - The existing `make install` target shows the project's conventions for install-style targets; the new target should read consistently.

  **Acceptance Criteria**:

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: install-skills creates 3 working symlinks
    Tool: Bash
    Preconditions: 3 skill dirs exist in repo; ~/.config/opencode/skills present or creatable
    Steps:
      1. Run: make install-skills (assert exit 0)
      2. For each of aisync-session-finder/aisync-stats/aisync-analyze: run `readlink ~/.config/opencode/skills/$name` and assert it points into /Users/guardix/dev/aisync/.opencode/skills/$name
      3. Run `head -3 ~/.config/opencode/skills/aisync-stats/SKILL.md` through the symlink; assert frontmatter readable
    Expected Result: 3 symlinks resolve to repo dirs; content readable via symlink
    Failure Indicators: missing symlink, points to wrong path, copy instead of symlink (test -L fails)
    Evidence: .omo/evidence/task-6-symlinks.txt

  Scenario: install-skills is idempotent and uninstall is safe
    Tool: Bash
    Preconditions: install-skills already run once
    Steps:
      1. Run `make install-skills` again → assert exit 0, still exactly 3 symlinks (no error, no duplication)
      2. Run `make uninstall-skills` → assert the 3 symlinks removed AND repo dirs untouched (ls .opencode/skills still shows 3)
      3. Re-run `make install-skills` to restore deployed state
    Expected Result: idempotent re-install; uninstall removes only symlinks; repo intact
    Failure Indicators: second run errors; uninstall deletes repo content; non-symlink removed
    Evidence: .omo/evidence/task-6-idempotent.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-6-symlinks.txt`
  - [ ] `.omo/evidence/task-6-idempotent.txt`

  **Commit**: YES
  - Message: `feat(build): add install-skills target for agent skill bundle`
  - Files: `Makefile`
  - Pre-commit: `make install-skills && test -L ~/.config/opencode/skills/aisync-stats`

- [x] 7. Documentation — `.opencode/skills/README.md` + main `README.md` section

  **What to do**:
  - Write `.opencode/skills/README.md` describing the bundle: the 3 skills, what each does, the trigger phrasings, and the `make install-skills` workflow (symlink model = repo is source of truth, edits propagate live).
  - Add a concise "Agent Skills" section to the main `README.md` linking to the bundle and the install command.
  - Note the `--search` (FTS5) limitation + pointer to `.omo/plans/ai5-search-architecture.md`, and that cost/analyze recipes may be OPTIONAL (auth).
  - Mention CLI-first + MCP fallback in one line each.

  **Must NOT do**:
  - Do NOT duplicate full recipes from the SKILL.md files (link/summarize instead).
  - Do NOT document out-of-scope commands (`diff`/`replay`/`rewind`) beyond a `--help` pointer.

  **Recommended Agent Profile**:
  - **Category**: `writing`
    - Reason: Documentation prose, audience = repo maintainers + future agents.
  - **Skills**: none
    - General docs; `customize-opencode` not needed (not authoring a SKILL.md).
  - **Skills Evaluated but Omitted**:
    - `customize-opencode`: this is README prose, not OpenCode config authoring.

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 3 (with Task 6)
  - **Blocks**: F1–F4
  - **Blocked By**: Tasks 3, 4, 5

  **References**:

  **Pattern References**:
  - `README.md:Commands` + `README.md:MCP Server Integration` — match the existing README tone/heading style for the new section.
  - `.opencode/skills/aisync-*/SKILL.md` (frontmatter) — source of the per-skill one-line summaries + trigger phrasings.

  **WHY Each Reference Matters**:
  - Reusing the README's existing section style keeps the doc consistent; pulling summaries from the actual frontmatter avoids drift between docs and skills.

  **Acceptance Criteria**:

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: Docs list all 3 skills + install command
    Tool: Bash
    Preconditions: SKILL.md files + Makefile target exist
    Steps:
      1. grep -c "aisync-session-finder\|aisync-stats\|aisync-analyze" .opencode/skills/README.md → assert all 3 named
      2. grep -i "install-skills" .opencode/skills/README.md README.md → assert install command documented in both/at least bundle README
      3. grep -i "search\|fts" .opencode/skills/README.md → assert limitation note + pointer to ai5-search-architecture plan
    Expected Result: bundle README names 3 skills, documents install, notes search limitation; main README has an Agent Skills section
    Failure Indicators: a skill unmentioned, no install instructions, no limitation note
    Evidence: .omo/evidence/task-7-docs.txt

  Scenario: Main README links the bundle
    Tool: Bash
    Preconditions: README updated
    Steps:
      1. grep -i "agent skill" README.md → assert section heading present
      2. grep -i ".opencode/skills" README.md → assert link/path to the bundle
    Expected Result: discoverable Agent Skills section pointing to the bundle
    Failure Indicators: no section, no link
    Evidence: .omo/evidence/task-7-readme-link.txt
  ```

  **Evidence to Capture**:
  - [ ] `.omo/evidence/task-7-docs.txt`
  - [ ] `.omo/evidence/task-7-readme-link.txt`

  **Commit**: YES
  - Message: `docs(skills): document agent skill bundle`
  - Files: `.opencode/skills/README.md`, `README.md`
  - Pre-commit: run the two QA recipes above

---

## Final Verification Wave (MANDATORY — after ALL implementation tasks)

> 4 review agents run in PARALLEL. ALL must APPROVE. Present consolidated results to the user and get an explicit "okay" before completing. Do NOT auto-complete. Never check F1–F4 before the user's okay.

- [x] F1. **Plan Compliance Audit** — `oracle`
  Read this plan end-to-end. For each "Must Have": verify it exists (read each SKILL.md, run a sample recipe). For each "Must NOT Have": search for violations (4th skill? invented flags? Go code changed via `git diff --stat`? FTS fix? files >350 lines via `wc -l`). Confirm evidence files exist in `.omo/evidence/`.
  Output: `Must Have [N/N] | Must NOT Have [N/N] | Tasks [N/N] | VERDICT: APPROVE/REJECT`

- [x] F2. **Skill Content Quality + Recipe Validity** — `unspecified-high`
  For EVERY command recipe in all 3 SKILL.md files: extract the command and run it against `~/.aisync/sessions.db` (substituting a real session id). Assert no "unknown command/flag" errors. Verify frontmatter parses (valid YAML, `name`+`description` present). Check descriptions contain concrete trigger phrasings. Flag AI slop (filler prose, invented examples).
  Output: `Recipes [N pass/N fail] | Frontmatter [3/3 valid] | Trigger phrasings [3/3] | VERDICT`

- [x] F3. **Real Agent QA + Trigger Check** — `unspecified-high`
  Simulate the end-user flow: for each skill, take 2 representative user questions, load the skill, follow its routing to pick + run the right `aisync` command, assert a correct answer. Then run `aisync skill-observe` on a representative session (non-blocking coverage signal). Verify `make install-skills` produced working symlinks (`readlink` each). Save evidence to `.omo/evidence/final-qa/`.
  Output: `Skill flows [N/N pass] | Symlinks [3/3] | skill-observe [ran] | VERDICT`

- [x] F4. **Scope Fidelity Check** — `deep`
  For each task: read "What to do", read the actual diff (`git status`/`git diff --stat`). Verify 1:1 — everything specified was built, nothing beyond scope was added (no 4th skill, no Go changes, no unrelated files). Confirm `skillsrouter.md` untouched. Flag any unaccounted changes.
  Output: `Tasks [N/N compliant] | Out-of-scope [CLEAN/N issues] | Unaccounted [CLEAN/N files] | VERDICT`

---

## Commit Strategy

- **Wave 1 (T1–T2)**: no commit (T1 produces an analysis note only; T2 scaffolding commits with first skill or standalone).
- **T3**: `feat(skills): add aisync-session-finder skill` — `.opencode/skills/aisync-session-finder/SKILL.md`
- **T4**: `feat(skills): add aisync-stats skill` — `.opencode/skills/aisync-stats/SKILL.md`
- **T5**: `feat(skills): add aisync-analyze skill` — `.opencode/skills/aisync-analyze/SKILL.md`
- **T6**: `feat(build): add install-skills target` — `Makefile`
- **T7**: `docs(skills): document agent skill bundle` — `.opencode/skills/README.md`, `README.md`
- Pre-commit per task: run the skill's recipes (T3–T5), `make install-skills` dry-run (T6).

---

## Success Criteria

### Verification Commands
```bash
# All 3 skills present with valid frontmatter
for s in aisync-session-finder aisync-stats aisync-analyze; do head -4 .opencode/skills/$s/SKILL.md; done
# Symlinks live in global skills dir
ls -la ~/.config/opencode/skills/ | grep aisync
# A representative recipe runs clean
aisync list --global --limit 5 --json | head -c 200
```

### Final Checklist
- [ ] All "Must Have" present
- [ ] All "Must NOT Have" absent
- [ ] 3 skills run their recipes against the real DB
- [ ] `make install-skills` symlinks verified
- [ ] Docs updated
- [ ] F1–F4 APPROVE + user okay
