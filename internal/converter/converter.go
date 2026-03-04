// Package converter transforms sessions between the unified aisync format
// and provider-native formats (Claude Code JSONL, OpenCode JSON).
//
// The converter delegates actual marshaling/unmarshaling to each provider's
// canonical Marshal/Unmarshal functions. This package provides the orchestration
// layer (ToNative, FromNative) and universal outputs (ToContextMD, DetectFormat).
package converter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/provider/claude"
	"github.com/ChristopherAparicio/aisync/internal/provider/opencode"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Converter implements session.Converter for cross-provider format conversion.
type Converter struct{}

// New creates a new Converter.
func New() *Converter {
	return &Converter{}
}

// SupportedFormats returns which providers this converter supports.
func (c *Converter) SupportedFormats() []session.ProviderName {
	return []session.ProviderName{
		session.ProviderClaudeCode,
		session.ProviderOpenCode,
	}
}

// ToNative converts a unified Session to the native format of the target provider.
// It delegates to the provider's canonical Marshal function.
func (c *Converter) ToNative(sess *session.Session, target session.ProviderName) ([]byte, error) {
	switch target {
	case session.ProviderClaudeCode:
		return claude.MarshalJSONL(sess)
	case session.ProviderOpenCode:
		return opencode.MarshalJSON(sess)
	default:
		return nil, fmt.Errorf("unsupported target format %q", target)
	}
}

// FromNative parses raw provider-native data into a unified Session.
// It delegates to the provider's canonical Unmarshal function.
func (c *Converter) FromNative(data []byte, source session.ProviderName) (*session.Session, error) {
	switch source {
	case session.ProviderClaudeCode:
		return claude.UnmarshalJSONL(data, session.StorageModeFull)
	case session.ProviderOpenCode:
		return opencode.UnmarshalJSON(data)
	default:
		return nil, fmt.Errorf("unsupported source format %q", source)
	}
}

// ToContextMD generates a CONTEXT.md fallback from a session.
// This is a universal output that any AI tool can read as a prompt.
func ToContextMD(sess *session.Session) []byte {
	var b strings.Builder

	b.WriteString("# AI Session Context\n\n")
	b.WriteString(fmt.Sprintf("- **Provider:** %s\n", sess.Provider))
	b.WriteString(fmt.Sprintf("- **Agent:** %s\n", sess.Agent))
	if sess.Branch != "" {
		b.WriteString(fmt.Sprintf("- **Branch:** %s\n", sess.Branch))
	}
	if !sess.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("- **Created:** %s\n", sess.CreatedAt.Format(time.RFC3339)))
	}
	if sess.Summary != "" {
		b.WriteString(fmt.Sprintf("- **Summary:** %s\n", sess.Summary))
	}
	b.WriteString("\n---\n\n")

	// File changes
	if len(sess.FileChanges) > 0 {
		b.WriteString("## Files Changed\n\n")
		for _, fc := range sess.FileChanges {
			b.WriteString(fmt.Sprintf("- `%s` (%s)\n", fc.FilePath, fc.ChangeType))
		}
		b.WriteString("\n---\n\n")
	}

	// Conversation
	b.WriteString("## Conversation\n\n")
	for _, msg := range sess.Messages {
		switch msg.Role {
		case session.RoleUser:
			b.WriteString("### User\n\n")
		case session.RoleAssistant:
			b.WriteString("### Assistant\n\n")
		case session.RoleSystem:
			b.WriteString("### System\n\n")
		}

		if msg.Content != "" {
			b.WriteString(msg.Content)
			b.WriteString("\n\n")
		}

		// Tool calls
		for _, tc := range msg.ToolCalls {
			b.WriteString(fmt.Sprintf("**Tool: %s** (state: %s)\n", tc.Name, tc.State))
			if tc.Input != "" {
				b.WriteString("```json\n")
				b.WriteString(tc.Input)
				b.WriteString("\n```\n")
			}
			if tc.Output != "" {
				b.WriteString("\nOutput:\n```\n")
				b.WriteString(tc.Output)
				b.WriteString("\n```\n")
			}
			b.WriteString("\n")
		}
	}

	// Children
	for _, child := range sess.Children {
		b.WriteString(fmt.Sprintf("\n---\n\n## Sub-agent: %s\n\n", child.Agent))
		for _, msg := range child.Messages {
			switch msg.Role {
			case session.RoleUser:
				b.WriteString("### User\n\n")
			case session.RoleAssistant:
				b.WriteString("### Assistant\n\n")
			default:
				b.WriteString("### System\n\n")
			}
			if msg.Content != "" {
				b.WriteString(msg.Content)
				b.WriteString("\n\n")
			}
		}
	}

	return []byte(b.String())
}

// DetectFormat tries to identify the format of raw data.
// Returns a ProviderName or empty string if it looks like unified aisync JSON.
func DetectFormat(data []byte) session.ProviderName {
	trimmed := strings.TrimSpace(string(data))

	// JSONL: multiple lines of JSON (Claude Code)
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 1 {
		// Check if first line is valid JSON object
		var first map[string]interface{}
		if err := json.Unmarshal([]byte(lines[0]), &first); err == nil {
			// Claude JSONL typically has "type" field
			if _, hasType := first["type"]; hasType {
				return session.ProviderClaudeCode
			}
		}
	}

	// Single JSON object — could be aisync unified or OpenCode
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal(data, &obj); err == nil {
			// aisync unified has "provider" and "messages" fields
			if _, hasProvider := obj["provider"]; hasProvider {
				if _, hasMsgs := obj["messages"]; hasMsgs {
					return "" // unified aisync format
				}
			}
			// OpenCode has "projectID" and "directory"
			if _, hasProjID := obj["projectID"]; hasProjID {
				return session.ProviderOpenCode
			}
		}
	}

	return "" // default: assume unified aisync
}
