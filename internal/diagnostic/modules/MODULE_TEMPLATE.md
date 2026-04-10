# How to Write a Custom Analysis Module

This template describes how to create a new analysis module for `aisync inspect`.
An LLM or human can follow these instructions to produce a working module.

## Architecture

```
internal/diagnostic/modules/<your-module>/
├── <your-module>.go       # Module implementation
└── <your-module>_test.go  # Tests
```

## Step 1: Create the Module File

```go
package yourmodule

import (
    "fmt"

    "github.com/ChristopherAparicio/aisync/internal/diagnostic"
    "github.com/ChristopherAparicio/aisync/internal/session"
)

// Auto-register on import.
func init() { diagnostic.RegisterModule(&Module{}) }

// Define problem IDs as constants.
const ProblemYourIssue diagnostic.ProblemID = "your-issue-name"

// Module implements diagnostic.AnalysisModule.
type Module struct{}

func (m *Module) Name() string { return "your-module" }

// ShouldActivate — deterministic check on session data.
// RULES:
//   - No LLM calls, no config, no network
//   - Must be fast (O(n) over messages is fine)
//   - Return false to skip all detectors
func (m *Module) ShouldActivate(sess *session.Session) bool {
    // Example: activate if session has Docker commands
    for _, msg := range sess.Messages {
        for _, tc := range msg.ToolCalls {
            if strings.Contains(tc.Input, "docker ") {
                return true
            }
        }
    }
    return false
}

// Detect runs detectors and returns 0..N problems.
func (m *Module) Detect(r *diagnostic.InspectReport, sess *session.Session) []diagnostic.Problem {
    var problems []diagnostic.Problem
    problems = append(problems, m.detectYourIssue(r, sess)...)
    return problems
}
```

## Step 2: Write Detectors

Each detector is a method that returns `[]diagnostic.Problem`:

```go
func (m *Module) detectYourIssue(r *diagnostic.InspectReport, sess *session.Session) []diagnostic.Problem {
    // 1. Compute your metric from session data
    count := 0
    for _, msg := range sess.Messages {
        // ... analyze messages/tool calls
    }

    // 2. Check threshold — return nil if below
    if count < 3 {
        return nil
    }

    // 3. Determine severity
    sev := diagnostic.SeverityLow
    if count > 10 {
        sev = diagnostic.SeverityHigh
    } else if count > 5 {
        sev = diagnostic.SeverityMedium
    }

    // 4. Return the problem with factual observation
    return []diagnostic.Problem{{
        ID:       ProblemYourIssue,
        Severity: sev,
        Category: diagnostic.CategoryCommands, // or CategoryTokens, CategoryPatterns, etc.
        Title:    "Short factual title",
        Observation: fmt.Sprintf("%d occurrences of X detected.", count),
        Impact: fmt.Sprintf("%d wasted operations.", count),
        Metric:     float64(count),
        MetricUnit: "count", // or "tokens", "USD", "ratio"
    }}
}
```

## Step 3: Write Tests

```go
package yourmodule

import (
    "testing"

    "github.com/ChristopherAparicio/aisync/internal/diagnostic"
    "github.com/ChristopherAparicio/aisync/internal/session"
)

func TestModule_Name(t *testing.T) {
    mod := &Module{}
    if mod.Name() != "your-module" {
        t.Errorf("expected 'your-module', got %q", mod.Name())
    }
}

func TestModule_ShouldActivate(t *testing.T) {
    // Test with matching session
    // Test with non-matching session
}

func TestDetect_triggersAboveThreshold(t *testing.T) {
    // Create session with data above threshold
    // Assert exactly 1 problem returned
    // Assert correct ProblemID, Severity, Category
}

func TestDetect_belowThreshold(t *testing.T) {
    // Create session with data below threshold
    // Assert 0 problems returned
}
```

## Step 4: Wire the Module

Add a blank import in the file that initializes modules. For built-in modules,
add to `internal/diagnostic/modules.go` or wherever the wiring happens:

```go
import _ "github.com/ChristopherAparicio/aisync/internal/diagnostic/modules/yourmodule"
```

## Conventions

### Observation language
- **Factual only** — counts, ratios, measurements
- **Forbidden words**: "should", "consider", "recommend", "suggest", "try",
  "fix", "bloated", "efficient", "better", "worse"
- Use: "detected", "observed", "measured", "found", "counted"

### Problem IDs
- Lowercase kebab-case: `"your-issue-name"`
- Prefix with module name for clarity: `"docker-orphan-containers"`

### Categories
Available: `CategoryImages`, `CategoryCompaction`, `CategoryCommands`,
`CategoryTokens`, `CategoryToolErrors`, `CategoryPatterns`

### Severity levels
- `SeverityHigh` — significant token waste or clear failure pattern
- `SeverityMedium` — notable but not critical
- `SeverityLow` — informational

### Module data (optional)
If your module needs pre-computed stats shared across detectors:

```go
type myStats struct {
    DockerCmds int
    FailedBuilds int
}

func (m *Module) Detect(r *diagnostic.InspectReport, sess *session.Session) []diagnostic.Problem {
    // Build stats once, store in report
    if r.GetModuleData("your-module") == nil {
        r.SetModuleData("your-module", buildStats(sess))
    }
    stats := r.GetModuleData("your-module").(*myStats)
    // ... use stats in detectors
}
```

### Available helpers
The `diagnostic` package exports these helpers for module use:

- `diagnostic.ExtractCommandFull(input) string` — extract command from tool call JSON
- `diagnostic.ExtractCommandBase(input) string` — extract base command name (first word)
- `diagnostic.ExtractCurlURL(input) string` — extract URL from curl command
- `diagnostic.TruncateStr(s, maxLen) string` — truncate with "..."
- `diagnostic.JoinMax(items, max) string` — join strings, cap at max with "(+N more)"
- `diagnostic.SessionHasRTK(sess) bool` — check for RTK usage
- `diagnostic.SessionHasImages(sess) bool` — check for image content
- `diagnostic.SessionHasAPICalls(sess) bool` — check for curl/httpie/wget

## Reference

See `internal/diagnostic/modules/example/example.go` for a complete working example.
