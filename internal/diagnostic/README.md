# diagnostic — Session Analysis & Problem Detection

The `diagnostic` package implements `aisync inspect`, a CLI command that produces
comprehensive session diagnostics: token usage, image costs, compaction patterns,
command analysis, tool errors, behavioral patterns, and detected problems.

## Architecture

```
internal/diagnostic/
├── module.go          # AnalysisModule interface + dynamic registry (RegisterModule)
├── modtypes.go        # Exported types shared between modules and fixgen
├── report.go          # InspectReport struct + BuildReport()
├── scriptmod.go       # ScriptModule — external script runner + discovery
├── detector.go        # 17 detector functions (used by core + images modules)
├── problem.go         # Problem struct, ProblemID constants, Severity, Category
├── fix.go             # FixSet, Artefact types
├── fixgen.go          # Fix generator registry (17 generators)
├── helpers.go         # Exported utilities: command extraction, formatting
├── trend.go           # Historical trend comparison
│
├── module_core.go     # CoreModule — always active, 14 detectors
├── module_rtk.go      # RTKModule — RTK-specific, 3 detectors + stats builder
├── module_images.go   # ImagesModule — image-specific, 3 detectors
├── module_api.go      # APIModule — HTTP client-specific, 2 detectors + stats builder
│
└── modules/           # Sub-packages for custom/community Go modules
    ├── MODULE_TEMPLATE.md   # Instructions for writing new Go modules
    └── example/             # Working example Go module with tests
```

## Two Ways to Extend

### 1. Script Modules (External — recommended for users)

Script modules are **external executables** (any language) that receive session
JSON on stdin and output problems as JSON on stdout. No Go, no compilation.

```
.aisync/modules/          ← per-project scripts
~/.aisync/modules/        ← global scripts
```

**Contract:**
- **stdin**: full session JSON (same structure as `aisync show --json`)
- **stdout**: JSON array of `[{id, severity, title, observation, impact, metric, metric_unit}]`
- **exit 0**: success (even if no problems — output `[]`)
- **exit non-zero**: skip (the script decided this session doesn't apply)
- **timeout**: 30 seconds max

**Create a module:**
```bash
aisync module init detect-my-pattern        # Python template
aisync module init detect-my-pattern --sh   # Shell template
aisync module init detect-my-pattern --global  # In ~/.aisync/modules/
```

**Test it:**
```bash
aisync show --json ses_abc123 | .aisync/modules/detect-my-pattern.py
```

**It runs automatically** on every `aisync inspect` — no configuration needed.
To disable, rename with `.disabled` suffix or delete.

**List discovered modules:**
```bash
aisync module list
```

### 2. Go Modules (Internal — for aisync developers)

Built-in modules live in `internal/diagnostic/` (deeply coupled to report).
Custom Go modules go in `internal/diagnostic/modules/<name>/` sub-packages.

See [`modules/MODULE_TEMPLATE.md`](modules/MODULE_TEMPLATE.md) for complete instructions.

Quick summary:
1. Create `internal/diagnostic/modules/<name>/<name>.go`
2. Implement `diagnostic.AnalysisModule` interface
3. Call `diagnostic.RegisterModule()` in `init()`
4. Add blank import to wire it
5. Write tests

See [`modules/example/`](modules/example/) for a working reference.

## How It Works

### 1. Report Building
`BuildReport(session, events, extraModules...)` creates an `InspectReport` with sections:
- **Tokens** — input/output/cache/image token counts, cost estimate
- **Images** — screenshot counts, sizes, resize rates, estimated image cost
- **Compaction** — context window resets, acceleration patterns
- **Commands** — tool calls by type, verbose output, durations
- **Tool Errors** — error loops, abandoned calls
- **Patterns** — conversation drift, yolo editing, glob storms

### 2. Modular Detection
Modules implement `AnalysisModule` (Name, ShouldActivate, Detect).
`RunModules()` iterates all registered modules, activates those relevant
to the session, and collects detected problems.

Built-in Go modules auto-register via `init()` → `RegisterModule()`.
Script modules are discovered from `.aisync/modules/` directories.
Both types produce `[]Problem` and appear in the same report.

### 3. Fix Generation (opt-in)
`GenerateFixes()` maps detected problems to provider-specific artefacts
(AGENTS.md patches, .cursorrules patches, etc.). Only runs with `--generate-fix`.

## Built-in Modules

| Module | Activation | Detectors |
|--------|-----------|-----------|
| **core** | Always | 14: compaction (3), commands (3), tokens (3), tool errors (2), patterns (3) |
| **rtk** | Session uses RTK commands | 3: curl-conflict, secret-redaction, identical-retry |
| **images** | Session has screenshots/images | 3: expensive, oversized, unresized |
| **api** | Session uses curl/httpie/wget | 2: retry-loop, identical-command-burst |

## Key Design Decisions

- **Detection is observational** — facts, counts, ratios. No prescriptions.
- **Activation is deterministic** — no LLM, no config, no network calls.
- **Modules own their stats** — each module builds its own pre-computed data
  via `SetModuleData()`, the report doesn't know about module-specific types.
- **Script modules are always active** — they do their own activation check
  internally and return `[]` if irrelevant.
- **Built-in modules stay in `diagnostic/`** — they're deeply coupled to the
  report structure. Custom modules go in `modules/` sub-packages or as scripts.
- **Fix generation is separate** — `fixgen.go` has its own registry, opt-in only.
- **Project scripts override global** — if the same script name exists in both
  `.aisync/modules/` and `~/.aisync/modules/`, the project version wins.
