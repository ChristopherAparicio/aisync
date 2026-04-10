package session

import (
	"regexp"
	"sort"
	"strings"
)

// CommandPattern represents a normalized command pattern seen across sessions.
type CommandPattern struct {
	Normalized   string `json:"normalized"`    // Normalized form (paths→/PATH, digits→N, etc.)
	Occurrences  int    `json:"occurrences"`   // Total invocations across all sessions
	SessionCount int    `json:"session_count"` // Number of unique sessions containing this pattern
	ProjectCount int    `json:"project_count"` // Number of unique projects containing this pattern
	AvgLength    int    `json:"avg_length"`    // Average character length of raw commands
	TotalChars   int    `json:"total_chars"`   // Sum of all raw command lengths
	MaxLength    int    `json:"max_length"`    // Longest raw invocation
	TotalOutput  int    `json:"total_output"`  // Total output bytes across all invocations
	AvgOutput    int    `json:"avg_output"`    // Average output per invocation
}

// CommandPatternInput is a single command invocation fed to FindCommandPatterns.
type CommandPatternInput struct {
	FullCommand string // raw command text
	SessionID   ID     // which session this came from
	ProjectPath string // which project
	OutputBytes int    // output size (0 if unknown)
}

// FindCommandPatterns groups command invocations by normalized form
// and returns patterns matching the filter criteria.
func FindCommandPatterns(inputs []CommandPatternInput, minLength int, minCount int) []CommandPattern {
	if minLength <= 0 {
		minLength = 100
	}
	if minCount <= 0 {
		minCount = 3
	}

	type agg struct {
		normalized string
		count      int
		sessions   map[string]bool
		projects   map[string]bool
		totalLen   int
		maxLen     int
		totalOut   int
	}
	patternMap := make(map[string]*agg)

	for _, inp := range inputs {
		if len(inp.FullCommand) < minLength {
			continue
		}

		norm := NormalizeCommand(inp.FullCommand)
		a, ok := patternMap[norm]
		if !ok {
			a = &agg{
				normalized: norm,
				sessions:   make(map[string]bool),
				projects:   make(map[string]bool),
			}
			patternMap[norm] = a
		}
		a.count++
		a.totalLen += len(inp.FullCommand)
		if len(inp.FullCommand) > a.maxLen {
			a.maxLen = len(inp.FullCommand)
		}
		a.totalOut += inp.OutputBytes
		if string(inp.SessionID) != "" {
			a.sessions[string(inp.SessionID)] = true
		}
		if inp.ProjectPath != "" {
			a.projects[inp.ProjectPath] = true
		}
	}

	var patterns []CommandPattern
	for _, a := range patternMap {
		if a.count < minCount {
			continue
		}
		avg := 0
		if a.count > 0 {
			avg = a.totalLen / a.count
		}
		avgOut := 0
		if a.count > 0 {
			avgOut = a.totalOut / a.count
		}
		patterns = append(patterns, CommandPattern{
			Normalized:   a.normalized,
			Occurrences:  a.count,
			SessionCount: len(a.sessions),
			ProjectCount: len(a.projects),
			AvgLength:    avg,
			TotalChars:   a.totalLen,
			MaxLength:    a.maxLen,
			TotalOutput:  a.totalOut,
			AvgOutput:    avgOut,
		})
	}

	// Sort by total chars descending (highest "retransmission" cost first).
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].TotalChars > patterns[j].TotalChars
	})

	return patterns
}

// NormalizeCommand transforms a raw shell command into a normalized form
// for pattern matching across sessions. Transformations:
//   - Absolute paths → /PATH
//   - Runs of digits → N
//   - Quoted string literals → "..."
//   - UUIDs → UUID
//   - SHA hashes (7-40 hex chars at word boundary) → SHA
//   - Multiple spaces → single space
//   - Trim whitespace
func NormalizeCommand(cmd string) string {
	s := cmd

	// 1. Replace quoted strings first (before other transformations alter content).
	s = reDoubleQuoted.ReplaceAllString(s, `"..."`)
	s = reSingleQuoted.ReplaceAllString(s, `'...'`)

	// 2. Replace UUIDs (before generic hex/digit rules).
	s = reUUID.ReplaceAllString(s, "UUID")

	// 3. Replace SHA-like hex strings (7-40 hex chars at word boundaries).
	s = reSHA.ReplaceAllString(s, "SHA")

	// 4. Replace absolute paths (but keep the base command and flags).
	s = reAbsPath.ReplaceAllString(s, "/PATH")

	// 5. Replace runs of digits.
	s = reDigits.ReplaceAllString(s, "N")

	// 6. Collapse whitespace.
	s = reSpaces.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	return s
}

var (
	reDoubleQuoted = regexp.MustCompile(`"[^"]*"`)
	reSingleQuoted = regexp.MustCompile(`'[^']*'`)
	reUUID         = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reSHA          = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	reAbsPath      = regexp.MustCompile(`/[\w./-]{3,}`)
	reDigits       = regexp.MustCompile(`\d+`)
	reSpaces       = regexp.MustCompile(`\s+`)
)
