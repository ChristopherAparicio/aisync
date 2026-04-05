// Package service — owner.go implements owner identity classification and statistics.
// This is part of Slack Integration Phase 1 (Foundation).
package service

import (
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ClassifyUserKind determines whether an email belongs to a human, machine (bot/CI),
// or unknown entity based on configurable glob patterns.
//
// Pattern matching rules:
//   - "*" matches any sequence of characters
//   - Patterns are case-insensitive
//   - If any pattern matches, the user is classified as "machine"
//   - Emails containing "noreply" (without "[bot]") are classified as "unknown"
//   - Everything else is classified as "human"
//
// This is a pure function — exported for use in resolveOwner() and backfill tasks.
func ClassifyUserKind(email string, machinePatterns []string) session.UserKind {
	if email == "" {
		return session.UserKindUnknown
	}

	lower := strings.ToLower(email)

	// Check machine patterns first
	for _, pattern := range machinePatterns {
		if globMatch(strings.ToLower(pattern), lower) {
			return session.UserKindMachine
		}
	}

	// Check for noreply emails (GitHub-style) that don't match bot patterns
	if strings.Contains(lower, "noreply") {
		return session.UserKindUnknown
	}

	return session.UserKindHuman
}

// globMatch performs simple glob matching where "*" matches any sequence of characters.
// Both pattern and text should already be lowercased.
func globMatch(pattern, text string) bool {
	// Fast paths
	if pattern == "*" {
		return true
	}
	if pattern == "" {
		return text == ""
	}

	// Split pattern by "*" and match segments in order
	parts := strings.Split(pattern, "*")

	// No wildcard — exact match
	if len(parts) == 1 {
		return pattern == text
	}

	pos := 0

	for i, part := range parts {
		if part == "" {
			continue // leading/trailing/consecutive wildcards
		}

		idx := strings.Index(text[pos:], part)
		if idx < 0 {
			return false
		}

		// First segment must match at the start if pattern doesn't start with "*"
		if i == 0 && !strings.HasPrefix(pattern, "*") && idx != 0 {
			return false
		}

		pos += idx + len(part)
	}

	// Last segment must match at the end if pattern doesn't end with "*"
	if !strings.HasSuffix(pattern, "*") {
		lastPart := parts[len(parts)-1]
		if lastPart != "" && !strings.HasSuffix(text, lastPart) {
			return false
		}
	}

	return true
}
