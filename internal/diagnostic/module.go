package diagnostic

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// AnalysisModule is a group of related detectors that activates conditionally
// based on session content. Each module has a deterministic activation check
// (no LLM, no config) and produces 0..N problems when active.
type AnalysisModule interface {
	// Name returns a short identifier, e.g. "core", "rtk", "images", "api".
	Name() string

	// ShouldActivate inspects session data and returns true if this module's
	// detectors are relevant. Activation must be deterministic and fast.
	ShouldActivate(sess *session.Session) bool

	// Detect runs all detectors in this module against the report and session.
	// The session is passed so modules can build their own pre-computed stats
	// without requiring BuildReport to know about every module.
	// It returns 0..N problems found.
	Detect(r *InspectReport, sess *session.Session) []Problem
}

// ModuleResult records which modules were activated and which were skipped.
type ModuleResult struct {
	Name      string `json:"name"`
	Activated bool   `json:"activated"`
	Problems  int    `json:"problems_found"`
}

// ── Module Registry ─────────────────────────────────────────────────────────

// moduleReg is the global module registry. Modules register themselves via
// init() functions so that DefaultModules() automatically includes them.
var moduleReg = &moduleRegistry{}

type moduleRegistry struct {
	mu      sync.Mutex
	modules []AnalysisModule
	names   map[string]bool
}

// RegisterModule adds a module to the global registry. It is safe for
// concurrent use (e.g. from init() functions across packages).
// Panics if a module with the same Name() is already registered — this is
// intentional: duplicate module names are a programming error, not a runtime
// condition to handle gracefully.
func RegisterModule(mod AnalysisModule) {
	moduleReg.mu.Lock()
	defer moduleReg.mu.Unlock()

	name := mod.Name()
	if moduleReg.names == nil {
		moduleReg.names = make(map[string]bool)
	}
	if moduleReg.names[name] {
		panic(fmt.Sprintf("diagnostic: duplicate module name %q", name))
	}
	moduleReg.names[name] = true
	moduleReg.modules = append(moduleReg.modules, mod)
}

// DefaultModules returns a copy of all registered analysis modules.
// The returned slice is a shallow copy — callers can reorder or filter it
// without affecting the registry.
func DefaultModules() []AnalysisModule {
	moduleReg.mu.Lock()
	defer moduleReg.mu.Unlock()

	out := make([]AnalysisModule, len(moduleReg.modules))
	copy(out, moduleReg.modules)
	return out
}

// resetRegistry clears the global registry. Test-only — not exported.
func resetRegistry() {
	moduleReg.mu.Lock()
	defer moduleReg.mu.Unlock()

	moduleReg.modules = nil
	moduleReg.names = nil
}

// registeredModuleNames returns the names of all registered modules. Test-only.
func registeredModuleNames() []string {
	moduleReg.mu.Lock()
	defer moduleReg.mu.Unlock()

	names := make([]string, len(moduleReg.modules))
	for i, m := range moduleReg.modules {
		names[i] = m.Name()
	}
	return names
}

// RunModules activates each module against the session, then runs its detectors
// on the report. Returns all problems found plus activation metadata.
func RunModules(modules []AnalysisModule, sess *session.Session, r *InspectReport) ([]Problem, []ModuleResult) {
	var allProblems []Problem
	var results []ModuleResult

	for _, mod := range modules {
		active := mod.ShouldActivate(sess)
		mr := ModuleResult{Name: mod.Name(), Activated: active}

		if active {
			problems := mod.Detect(r, sess)
			mr.Problems = len(problems)
			allProblems = append(allProblems, problems...)
		}

		results = append(results, mr)
	}

	return allProblems, results
}

// ── Activation helpers ──────────────────────────────────────────────────────

// SessionHasRTK returns true if any bash tool call in the session uses RTK.
func SessionHasRTK(sess *session.Session) bool {
	for _, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			name := strings.ToLower(tc.Name)
			if name == "bash" || name == "mcp_bash" || name == "execute_command" {
				if strings.Contains(tc.Input, "rtk ") || strings.Contains(tc.Input, "/rtk ") {
					return true
				}
			}
		}
	}
	return false
}

// SessionHasImages returns true if the session contains inline images or
// image-related tool calls (simctl screenshots, sips resizes, or image file reads).
func SessionHasImages(sess *session.Session) bool {
	// Check inline images on messages
	for _, msg := range sess.Messages {
		if len(msg.Images) > 0 {
			return true
		}
		for _, cb := range msg.ContentBlocks {
			if cb.Type == session.ContentBlockImage && cb.Image != nil {
				return true
			}
		}
	}

	// Check tool calls for image operations
	for _, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			name := strings.ToLower(tc.Name)
			input := strings.ToLower(tc.Input)

			// Image file reads
			if name == "read" || name == "mcp_read" {
				if strings.Contains(input, ".png") || strings.Contains(input, ".jpg") || strings.Contains(input, ".jpeg") {
					return true
				}
			}
			// Simulator screenshots or sips resizes
			if name == "bash" || name == "mcp_bash" {
				if strings.Contains(input, "simctl") && strings.Contains(input, "screenshot") {
					return true
				}
				if strings.Contains(input, "sips") {
					return true
				}
			}
		}
	}
	return false
}

// SessionHasAPICalls returns true if the session contains curl, httpie, or wget commands.
func SessionHasAPICalls(sess *session.Session) bool {
	for _, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			name := strings.ToLower(tc.Name)
			if name == "bash" || name == "mcp_bash" || name == "execute_command" {
				input := tc.Input
				if strings.Contains(input, "curl ") || strings.Contains(input, "curl\n") ||
					strings.Contains(input, "httpie") || strings.Contains(input, "http ") ||
					strings.Contains(input, "wget ") {
					return true
				}
			}
		}
	}
	return false
}
