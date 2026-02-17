// Package secrets implements secret detection and masking for aisync.
// It provides a built-in regex scanner that loads patterns from an embedded file
// and supports user-defined custom patterns.
package secrets

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

// Scanner detects secrets in text using regex patterns.
// It implements domain.SecretScanner.
type Scanner struct {
	mode     domain.SecretMode
	patterns []Pattern
}

// NewScanner creates a scanner with the given mode and patterns.
// If patterns is nil, the built-in defaults are used.
func NewScanner(mode domain.SecretMode, patterns []Pattern) *Scanner {
	if patterns == nil {
		patterns = DefaultPatterns()
	}
	return &Scanner{
		patterns: patterns,
		mode:     mode,
	}
}

// Scan checks content for secrets and returns all matches.
func (s *Scanner) Scan(content string) []domain.SecretMatch {
	var matches []domain.SecretMatch

	for _, p := range s.patterns {
		locs := p.Regex.FindAllStringIndex(content, -1)
		for _, loc := range locs {
			matches = append(matches, domain.SecretMatch{
				Type:     p.Name,
				Value:    content[loc[0]:loc[1]],
				StartPos: loc[0],
				EndPos:   loc[1],
			})
		}
	}

	// Sort by position for consistent output
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].StartPos < matches[j].StartPos
	})

	return matches
}

// Mask replaces detected secrets with ***REDACTED:TYPE*** placeholders.
func (s *Scanner) Mask(content string) string {
	matches := s.Scan(content)
	if len(matches) == 0 {
		return content
	}

	// Build result by replacing matches from end to start
	// (so byte offsets stay valid).
	result := content
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		replacement := fmt.Sprintf("***REDACTED:%s***", m.Type)
		result = result[:m.StartPos] + replacement + result[m.EndPos:]
	}

	return result
}

// Mode returns the current secret handling mode.
func (s *Scanner) Mode() domain.SecretMode {
	return s.mode
}

// ScanSession checks all text content in a session for secrets.
// This scans message content, tool call outputs, and (optionally) tool call inputs.
func (s *Scanner) ScanSession(session *domain.Session) []domain.SecretMatch {
	var allMatches []domain.SecretMatch

	for _, msg := range session.Messages {
		allMatches = append(allMatches, s.Scan(msg.Content)...)
		for _, tc := range msg.ToolCalls {
			allMatches = append(allMatches, s.Scan(tc.Output)...)
		}
	}

	return allMatches
}

// MaskSession applies masking to all text content in a session.
// Modifies the session in place.
func (s *Scanner) MaskSession(session *domain.Session) {
	for i := range session.Messages {
		session.Messages[i].Content = s.Mask(session.Messages[i].Content)
		for j := range session.Messages[i].ToolCalls {
			session.Messages[i].ToolCalls[j].Output = s.Mask(session.Messages[i].ToolCalls[j].Output)
		}
	}
}

// AddPatterns appends additional patterns to the scanner.
func (s *Scanner) AddPatterns(patterns []Pattern) {
	s.patterns = append(s.patterns, patterns...)
}

// PatternCount returns the number of loaded patterns.
func (s *Scanner) PatternCount() int {
	return len(s.patterns)
}

// FormatMatches returns a human-readable summary of secret matches.
func FormatMatches(matches []domain.SecretMatch) string {
	if len(matches) == 0 {
		return "no secrets found"
	}

	// Group by type
	counts := make(map[string]int)
	for _, m := range matches {
		counts[m.Type]++
	}

	var parts []string
	for typ, count := range counts {
		if count == 1 {
			parts = append(parts, typ)
		} else {
			parts = append(parts, fmt.Sprintf("%s (%d)", typ, count))
		}
	}
	sort.Strings(parts)

	return strings.Join(parts, ", ")
}
