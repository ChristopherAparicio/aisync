package session

import (
	"encoding/json"
	"strings"
)

// ExtractBaseCommand extracts the base command name from a bash/shell tool call.
// For example, "git status --short" → "git", "ls -la /tmp" → "ls".
//
// The tool call input can be:
//   - JSON: {"command": "git status --short"}
//   - Plain string: "git status --short"
//
// Returns "" if the command cannot be extracted.
func ExtractBaseCommand(toolName, toolInput string) string {
	// Only process bash/shell tools.
	lower := strings.ToLower(toolName)
	if lower != "bash" && lower != "shell" && lower != "terminal" && lower != "execute_command" {
		return ""
	}

	cmd := extractCommandString(toolInput)
	if cmd == "" {
		return ""
	}

	// Extract the first word (the base command).
	// Handle common prefixes: sudo, env, time, nice, etc.
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}

	// Skip common prefixes.
	skipPrefixes := map[string]bool{
		"sudo": true, "env": true, "time": true,
		"nice": true, "nohup": true, "timeout": true,
	}

	for i, f := range fields {
		// Skip env-style KEY=VALUE assignments.
		if strings.Contains(f, "=") && !strings.HasPrefix(f, "-") {
			continue
		}
		if skipPrefixes[f] {
			continue
		}
		// This is the actual command.
		base := f
		// Strip path prefix (e.g. /usr/bin/git → git).
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		_ = i // avoid unused
		return base
	}

	return fields[0]
}

// extractCommandString extracts the command string from tool input.
func extractCommandString(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	// Try JSON format: {"command": "..."}.
	if strings.HasPrefix(input, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(input), &obj); err == nil {
			if cmd, ok := obj["command"].(string); ok {
				return strings.TrimSpace(cmd) // may be empty → caller handles
			}
			if cmd, ok := obj["cmd"].(string); ok {
				return strings.TrimSpace(cmd)
			}
			// JSON parsed but no command key found → not a bash input.
			return ""
		}
	}

	// Plain string — treat as command directly.
	return input
}

// ComputeCommandStats analyzes all tool calls in a session and produces
// a CommandStats breakdown.
func ComputeCommandStats(messages []Message) CommandStats {
	stats := CommandStats{
		ByCommand: make(map[string]int),
	}

	for i := range messages {
		for j := range messages[i].ToolCalls {
			tc := &messages[i].ToolCalls[j]
			base := ExtractBaseCommand(tc.Name, tc.Input)
			if base == "" {
				continue
			}
			stats.TotalCommands++
			stats.ByCommand[base]++
			if tc.State == ToolStateError {
				stats.ErrorCommands++
			}
		}
	}

	return stats
}

// ComputeImageStats aggregates image usage across all messages.
func ComputeImageStats(messages []Message) ImageStats {
	var stats ImageStats
	for i := range messages {
		for _, img := range messages[i].Images {
			stats.Count++
			stats.TotalBytes += img.SizeBytes
			stats.TotalTokens += img.TokensEstimate
		}
	}
	return stats
}
