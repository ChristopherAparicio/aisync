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

// DefaultMaxContextBytes is the maximum size (in bytes) for generated CONTEXT.md files.
// 400 KB ≈ 100K tokens (at ~4 bytes/token). This leaves headroom for the system prompt
// and assistant response within typical AI context windows (128K–200K tokens).
const DefaultMaxContextBytes = 400 * 1024

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
//
// It returns ErrContextTooLarge if the estimated output would exceed
// DefaultMaxContextBytes. Callers should suggest using Smart Restore
// filters (--clean-errors, --strip-empty, --exclude) to reduce session size.
func ToContextMD(sess *session.Session) ([]byte, error) {
	// Pre-flight size check: estimate the output size before building it.
	estimatedBytes := estimateContextSize(sess)
	if estimatedBytes > DefaultMaxContextBytes {
		return nil, &ContextTooLargeError{
			EstimatedBytes: estimatedBytes,
			MaxBytes:       DefaultMaxContextBytes,
			MessageCount:   len(sess.Messages),
			ChildCount:     len(sess.Children),
		}
	}

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

	return []byte(b.String()), nil
}

// ContextTooLargeError provides actionable details when CONTEXT.md would be too large.
type ContextTooLargeError struct {
	EstimatedBytes int
	MaxBytes       int
	MessageCount   int
	ChildCount     int
}

func (e *ContextTooLargeError) Error() string {
	estKB := e.EstimatedBytes / 1024
	maxKB := e.MaxBytes / 1024
	estTokens := e.EstimatedBytes / 4 // rough: ~4 bytes per token

	msg := fmt.Sprintf(
		"session too large for CONTEXT.md: estimated %dKB (~%dK tokens) exceeds %dKB limit (%d messages",
		estKB, estTokens/1000, maxKB, e.MessageCount,
	)
	if e.ChildCount > 0 {
		msg += fmt.Sprintf(", %d sub-agents", e.ChildCount)
	}
	msg += ")\n\nReduce session size with Smart Restore filters:\n"
	msg += "  --clean-errors     Replace verbose error outputs with compact summaries\n"
	msg += "  --strip-empty      Remove empty messages\n"
	msg += "  --exclude \"system\" Remove system messages\n"
	msg += "  --exclude \"0,1,2\"  Remove specific messages by index\n"
	msg += "\nExample: aisync restore --session <id> --as-context --clean-errors --strip-empty"
	return msg
}

// Unwrap returns the sentinel ErrContextTooLarge for errors.Is() matching.
func (e *ContextTooLargeError) Unwrap() error {
	return session.ErrContextTooLarge
}

// estimateContextSize computes a rough byte estimate for the CONTEXT.md output
// without actually building the string. This is O(n) over messages but avoids
// allocating the full output buffer just to check if it's too big.
func estimateContextSize(sess *session.Session) int {
	size := 200 // header (metadata, separators)

	// File changes
	for _, fc := range sess.FileChanges {
		size += len(fc.FilePath) + 20 // "- `path` (type)\n"
	}

	// Messages
	for _, msg := range sess.Messages {
		size += 20 // "### Role\n\n"
		size += len(msg.Content) + 2

		for _, tc := range msg.ToolCalls {
			size += len(tc.Name) + 40   // "**Tool: name** (state: ...)\n"
			size += len(tc.Input) + 20  // json code block wrapper
			size += len(tc.Output) + 20 // output code block wrapper
		}
	}

	// Children
	for _, child := range sess.Children {
		size += len(child.Agent) + 30 // "## Sub-agent: name\n"
		for _, msg := range child.Messages {
			size += 20 // "### Role\n\n"
			size += len(msg.Content) + 2
		}
	}

	return size
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
