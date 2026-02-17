package secrets

import (
	"bufio"
	"embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed patterns.txt
var defaultPatternsFile embed.FS

// Pattern is a named regex that detects a specific type of secret.
type Pattern struct {
	Regex *regexp.Regexp
	Name  string
}

// DefaultPatterns returns the built-in secret detection patterns
// loaded from the embedded patterns.txt file.
func DefaultPatterns() []Pattern {
	data, err := defaultPatternsFile.ReadFile("patterns.txt")
	if err != nil {
		// Embedded file missing is a build-time error; return empty as fallback.
		return nil
	}
	patterns, _ := ParsePatterns(string(data))
	return patterns
}

// ParsePatterns parses a patterns file into compiled Pattern values.
//
// File format (one pattern per line):
//
//	NAME  REGEX
//
// Lines starting with # are comments. Empty lines are ignored.
// NAME and REGEX are separated by whitespace.
func ParsePatterns(content string) ([]Pattern, error) {
	var patterns []Pattern
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split into NAME and REGEX (first whitespace separates them)
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			parts = strings.SplitN(line, "\t", 2)
		}
		if len(parts) < 2 {
			return nil, fmt.Errorf("line %d: expected NAME REGEX, got %q", lineNum, line)
		}

		name := strings.TrimSpace(parts[0])
		pattern := strings.TrimSpace(parts[1])

		re, compileErr := regexp.Compile(pattern)
		if compileErr != nil {
			return nil, fmt.Errorf("line %d: invalid regex for %s: %w", lineNum, name, compileErr)
		}

		patterns = append(patterns, Pattern{
			Name:  name,
			Regex: re,
		})
	}

	return patterns, nil
}

// LoadPatternsFromFile reads a patterns file from disk and parses it.
// This is used for user-defined custom pattern files.
func LoadPatternsFromFile(content string) ([]Pattern, error) {
	return ParsePatterns(content)
}
