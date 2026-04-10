// Package diagnostic — script module support.
//
// ScriptModule wraps an external executable (any language) as an AnalysisModule.
// The contract is simple:
//   - aisync passes the full session JSON on stdin
//   - the script writes a JSON array of problems to stdout
//   - exit 0 = success, non-zero = skip (treated as "no problems")
//   - scripts have a timeout (default 30s)
//
// Discovery: DiscoverScriptModules scans two directories for executable files:
//   - .aisync/modules/  (per-project, relative to cwd)
//   - ~/.aisync/modules/ (global)
//
// Files ending with ".disabled" are skipped. Files must be executable (chmod +x).
package diagnostic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// DefaultScriptTimeout is the max execution time for a script module.
const DefaultScriptTimeout = 30 * time.Second

// ScriptProblem is the JSON structure a script writes to stdout.
// It's a simplified version of Problem — scripts don't need to know about
// Go-specific types like Severity or Category enums.
type ScriptProblem struct {
	ID          string  `json:"id"`
	Severity    string  `json:"severity"`           // "high", "medium", "low"
	Category    string  `json:"category,omitempty"` // optional, defaults to "patterns"
	Title       string  `json:"title"`
	Observation string  `json:"observation"`
	Impact      string  `json:"impact"`
	Metric      float64 `json:"metric,omitempty"`
	MetricUnit  string  `json:"metric_unit,omitempty"`
}

// ScriptModule wraps an external script as an AnalysisModule.
type ScriptModule struct {
	// path is the absolute path to the script executable.
	path string
	// name is derived from the filename (without extension).
	name string
	// timeout controls how long the script can run.
	timeout time.Duration
	// source indicates where the script was discovered ("project" or "global").
	source string
}

// NewScriptModule creates a ScriptModule from a file path.
func NewScriptModule(path string, source string) *ScriptModule {
	name := filepath.Base(path)
	// Strip extension for the module name
	if ext := filepath.Ext(name); ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	return &ScriptModule{
		path:    path,
		name:    "script:" + name,
		timeout: DefaultScriptTimeout,
		source:  source,
	}
}

func (m *ScriptModule) Name() string { return m.name }

// ShouldActivate always returns true — script modules are always active
// when present. Scripts can do their own activation check internally and
// simply return an empty array if they don't apply.
func (m *ScriptModule) ShouldActivate(_ *session.Session) bool {
	return true
}

// Detect runs the script with the session JSON on stdin and parses problems from stdout.
func (m *ScriptModule) Detect(_ *InspectReport, sess *session.Session) []Problem {
	// Serialize the session to JSON for stdin
	sessionJSON, err := json.Marshal(sess)
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, m.path)
	cmd.Stdin = bytes.NewReader(sessionJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Non-zero exit or timeout — script chose not to report problems.
		// This is normal (e.g. script decided session doesn't apply).
		return nil
	}

	// Parse stdout as JSON array of ScriptProblem
	output := bytes.TrimSpace(stdout.Bytes())
	if len(output) == 0 {
		return nil
	}

	var scriptProblems []ScriptProblem
	if err := json.Unmarshal(output, &scriptProblems); err != nil {
		// Bad JSON — skip silently. The script is buggy but we don't crash.
		return nil
	}

	// Convert ScriptProblem → Problem
	problems := make([]Problem, 0, len(scriptProblems))
	for _, sp := range scriptProblems {
		if sp.ID == "" || sp.Title == "" {
			continue // skip malformed entries
		}
		p := Problem{
			ID:          ProblemID(sp.ID),
			Severity:    parseSeverity(sp.Severity),
			Category:    parseCategory(sp.Category),
			Title:       sp.Title,
			Observation: sp.Observation,
			Impact:      sp.Impact,
			Metric:      sp.Metric,
			MetricUnit:  sp.MetricUnit,
		}
		problems = append(problems, p)
	}

	return problems
}

// Path returns the absolute path to the script.
func (m *ScriptModule) Path() string { return m.path }

// Source returns where the script was discovered ("project" or "global").
func (m *ScriptModule) Source() string { return m.source }

// ── Discovery ───────────────────────────────────────────────────────────────

// ScriptDirs represents the directories to scan for script modules.
type ScriptDirs struct {
	// ProjectDir is the per-project modules directory (e.g. .aisync/modules/).
	// Empty string means no project directory.
	ProjectDir string
	// GlobalDir is the global modules directory (e.g. ~/.aisync/modules/).
	// Empty string means no global directory.
	GlobalDir string
}

// DefaultScriptDirs returns the standard script directories.
func DefaultScriptDirs() ScriptDirs {
	home, err := os.UserHomeDir()
	globalDir := ""
	if err == nil {
		globalDir = filepath.Join(home, ".aisync", "modules")
	}

	projectDir := filepath.Join(".aisync", "modules")

	return ScriptDirs{
		ProjectDir: projectDir,
		GlobalDir:  globalDir,
	}
}

// DiscoverScriptModules scans the project and global directories for executable
// script files and returns them as ScriptModule instances.
//
// Files are skipped if:
//   - they end with ".disabled"
//   - they are not executable (no +x permission)
//   - they are directories
//   - they start with "." (hidden files)
//   - they are named "README", "README.md", etc.
//
// Project scripts take precedence: if a script with the same base name exists
// in both project and global dirs, only the project version is included.
func DiscoverScriptModules(dirs ScriptDirs) []*ScriptModule {
	seen := make(map[string]bool) // base name → already included
	var modules []*ScriptModule

	// Project dir first (higher priority)
	if dirs.ProjectDir != "" {
		for _, m := range scanDir(dirs.ProjectDir, "project") {
			seen[m.name] = true
			modules = append(modules, m)
		}
	}

	// Global dir second (lower priority, skip duplicates)
	if dirs.GlobalDir != "" {
		for _, m := range scanDir(dirs.GlobalDir, "global") {
			if !seen[m.name] {
				modules = append(modules, m)
			}
		}
	}

	return modules
}

// scanDir reads a directory and returns ScriptModule instances for valid scripts.
func scanDir(dir string, source string) []*ScriptModule {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // directory doesn't exist — that's fine
	}

	var modules []*ScriptModule
	for _, entry := range entries {
		if !isValidScript(entry) {
			continue
		}

		absPath, err := filepath.Abs(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		// Check executable permission
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue // not executable
		}

		modules = append(modules, NewScriptModule(absPath, source))
	}

	return modules
}

// isValidScript checks if a directory entry is a valid script file.
func isValidScript(entry fs.DirEntry) bool {
	if entry.IsDir() {
		return false
	}
	name := entry.Name()
	if strings.HasPrefix(name, ".") {
		return false
	}
	if strings.HasSuffix(name, ".disabled") {
		return false
	}
	// Skip documentation files
	lower := strings.ToLower(name)
	if lower == "readme" || lower == "readme.md" || lower == "readme.txt" {
		return false
	}
	return true
}

// ── Severity / Category parsing ─────────────────────────────────────────────

func parseSeverity(s string) Severity {
	switch strings.ToLower(s) {
	case "high":
		return SeverityHigh
	case "medium":
		return SeverityMedium
	case "low":
		return SeverityLow
	default:
		return SeverityLow
	}
}

func parseCategory(s string) Category {
	switch strings.ToLower(s) {
	case "images":
		return CategoryImages
	case "compaction":
		return CategoryCompaction
	case "commands":
		return CategoryCommands
	case "tokens":
		return CategoryTokens
	case "tool_errors":
		return CategoryToolErrors
	case "patterns":
		return CategoryPatterns
	default:
		return CategoryPatterns // default for custom modules
	}
}

// ── Helpers for inspectcmd ──────────────────────────────────────────────────

// ScriptModuleInfo holds metadata about a discovered script module for display.
type ScriptModuleInfo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Source string `json:"source"` // "project" or "global"
}

// ScriptModulesInfo returns metadata about all script modules for display/JSON output.
func ScriptModulesInfo(modules []*ScriptModule) []ScriptModuleInfo {
	infos := make([]ScriptModuleInfo, len(modules))
	for i, m := range modules {
		infos[i] = ScriptModuleInfo{
			Name:   m.name,
			Path:   m.path,
			Source: m.source,
		}
	}
	return infos
}

// AsAnalysisModules converts a slice of ScriptModule pointers to AnalysisModule interfaces.
func AsAnalysisModules(scripts []*ScriptModule) []AnalysisModule {
	modules := make([]AnalysisModule, len(scripts))
	for i, s := range scripts {
		modules[i] = s
	}
	return modules
}

// FormatScriptModuleError returns a user-friendly error string for a script failure.
// Used in verbose/debug output.
func FormatScriptModuleError(name string, err error, stderr string) string {
	msg := fmt.Sprintf("script module %q failed: %v", name, err)
	if stderr != "" {
		msg += "\n  stderr: " + TruncateStr(stderr, 200)
	}
	return msg
}
