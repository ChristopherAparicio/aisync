// Package skillobs implements skill observation — detecting which skills
// were loaded vs should have been loaded during an AI coding session.
//
// Two components work together:
//   - Detector: scans tool calls and message text for skill-loading patterns
//   - Recommender: matches user messages against skill keywords to suggest relevant skills
//
// The Observer combines both to produce a SkillObservation with
// recommended / loaded / missed / discovered lists.
package skillobs

import (
	"encoding/json"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// skillToolNames is the set of tool call names that indicate a skill was loaded.
var skillToolNames = map[string]bool{
	"load_skill": true,
	"mcp_skill":  true,
	"skill":      true,
	"read_skill": true,
	"load-skill": true,
}

// DetectLoadedSkills scans a session's messages for tool calls and text patterns
// that indicate a skill was loaded. Returns the list of detected skill names.
func DetectLoadedSkills(messages []session.Message) []string {
	seen := make(map[string]bool)
	var loaded []string

	for i := range messages {
		msg := &messages[i]

		// 1. Check tool calls.
		for j := range msg.ToolCalls {
			tc := &msg.ToolCalls[j]
			if !skillToolNames[strings.ToLower(tc.Name)] {
				continue
			}
			name := extractSkillName(tc.Input)
			if name == "" {
				name = extractSkillName(tc.Output)
			}
			if name != "" && !seen[name] {
				seen[name] = true
				loaded = append(loaded, name)
			}
		}

		// 2. Check text patterns in assistant messages.
		// OpenCode/Claude emit `<skill_content name="xxx">` when a skill is loaded.
		if msg.Role == session.RoleAssistant {
			for _, name := range extractSkillContentTags(msg.Content) {
				if !seen[name] {
					seen[name] = true
					loaded = append(loaded, name)
				}
			}
		}
	}

	return loaded
}

// extractSkillName tries to find a skill name in a JSON string.
// Looks for common field names: "name", "skill_name", "skill".
func extractSkillName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || (raw[0] != '{' && raw[0] != '"') {
		// Not JSON — might be a plain skill name.
		if raw != "" && !strings.ContainsAny(raw, " \t\n{}[]") {
			return raw
		}
		return ""
	}

	// Try as a plain quoted string first.
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal([]byte(raw), &s); err == nil && s != "" {
			return s
		}
	}

	// Try as a JSON object with known fields.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ""
	}

	for _, key := range []string{"name", "skill_name", "skill"} {
		if v, ok := obj[key]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err == nil && s != "" {
				return s
			}
		}
	}

	return ""
}

// extractSkillContentTags finds `<skill_content name="xxx">` patterns in text.
// Returns all skill names found.
func extractSkillContentTags(text string) []string {
	var names []string
	const prefix = `<skill_content name="`

	remaining := text
	for {
		idx := strings.Index(remaining, prefix)
		if idx < 0 {
			break
		}
		remaining = remaining[idx+len(prefix):]
		end := strings.IndexByte(remaining, '"')
		if end < 0 {
			break
		}
		name := remaining[:end]
		if name != "" {
			names = append(names, name)
		}
		remaining = remaining[end+1:]
	}

	return names
}
