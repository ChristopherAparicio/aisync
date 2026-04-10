package diagnostic

import (
	"fmt"
	"strings"
)

// ── Format helpers ──────────────────────────────────────────────────────────

func fmtInt(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func fmtK(n int) string {
	return fmt.Sprintf("%.0f", float64(n)/1_000)
}

func fmtTok(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func fmtBytes(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1f MB", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1f KB", float64(n)/1_000)
	}
	return fmt.Sprintf("%d B", n)
}

// ── Aggregation helpers ─────────────────────────────────────────────────────

func countAbove(events []CompactionView, threshold int) int {
	count := 0
	for _, e := range events {
		if e.BeforeTokens > threshold {
			count++
		}
	}
	return count
}

func JoinMax(items []string, max int) string {
	if len(items) <= max {
		return joinStrings(items)
	}
	return joinStrings(items[:max]) + fmt.Sprintf(" (+%d more)", len(items)-max)
}

func joinStrings(items []string) string {
	result := ""
	for i, s := range items {
		if i > 0 {
			result += "; "
		}
		result += s
	}
	return result
}

// ── Command extraction helpers ──────────────────────────────────────────────

// extractPath extracts a file path from tool call input JSON (looks for "filePath" key).
func extractPath(input string) string {
	if idx := strings.Index(input, "filePath"); idx >= 0 {
		rest := input[idx:]
		if q := strings.Index(rest, ":"); q >= 0 {
			rest = rest[q+1:]
			rest = strings.TrimSpace(rest)
			rest = strings.Trim(rest, "\"")
			if end := strings.IndexAny(rest, "\",}"); end >= 0 {
				rest = rest[:end]
			}
			return rest
		}
	}
	if len(input) > 80 {
		return input[:80]
	}
	return input
}

// ExtractCommandFull extracts the full command string from tool call input JSON.
func ExtractCommandFull(input string) string {
	lower := strings.ToLower(input)
	if idx := strings.Index(lower, "command"); idx >= 0 {
		rest := input[idx:]
		if q := strings.Index(rest, ":"); q >= 0 {
			rest = rest[q+1:]
			rest = strings.TrimSpace(rest)
			rest = strings.Trim(rest, "\"")
			if end := strings.IndexAny(rest, "\"}"); end >= 0 {
				rest = rest[:end]
			}
			return strings.TrimSpace(rest)
		}
	}
	return input
}

// ExtractCommandBase extracts the base command name (first word) from tool call input JSON.
func ExtractCommandBase(input string) string {
	full := ExtractCommandFull(input)
	if full == input {
		// ExtractCommandFull didn't find "command" key — try raw parsing
		return "(unknown)"
	}
	fields := strings.Fields(full)
	if len(fields) == 0 {
		return "(unknown)"
	}
	cmd := fields[0]
	if lastSlash := strings.LastIndex(cmd, "/"); lastSlash >= 0 {
		cmd = cmd[lastSlash+1:]
	}
	return cmd
}

// ExtractCurlURL extracts the target URL from a curl command string.
func ExtractCurlURL(input string) string {
	idx := strings.Index(input, "http://")
	if idx < 0 {
		idx = strings.Index(input, "https://")
	}
	if idx < 0 {
		return ""
	}
	rest := input[idx:]
	// URL ends at whitespace, quote, or end-of-string
	end := strings.IndexAny(rest, " \t\n\"'}")
	if end > 0 {
		rest = rest[:end]
	}
	// Strip query params for grouping
	if qIdx := strings.Index(rest, "?"); qIdx > 0 {
		rest = rest[:qIdx]
	}
	return rest
}

// TruncateStr truncates a string to maxLen characters, appending "..." if truncated.
func TruncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
