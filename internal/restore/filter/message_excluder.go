package filter

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// MessageExcluder removes messages by index, role, or content pattern.
// All criteria are combined with OR logic — a message is excluded if it
// matches ANY of the specified criteria.
type MessageExcluder struct {
	// Indices is a set of 0-based message indices to exclude.
	Indices map[int]bool

	// Roles lists roles to exclude (e.g. "system").
	Roles []session.MessageRole

	// ContentPattern is a regex pattern; messages whose content matches are excluded.
	ContentPattern *regexp.Regexp
}

// MessageExcluderConfig holds the raw inputs for constructing a MessageExcluder.
type MessageExcluderConfig struct {
	Indices        []int    // 0-based message indices to exclude
	Roles          []string // role names to exclude (e.g. "system")
	ContentPattern string   // regex pattern to match against content
}

// NewMessageExcluder creates a MessageExcluder from a config.
func NewMessageExcluder(cfg MessageExcluderConfig) (*MessageExcluder, error) {
	f := &MessageExcluder{
		Indices: make(map[int]bool),
	}

	for _, idx := range cfg.Indices {
		f.Indices[idx] = true
	}

	for _, r := range cfg.Roles {
		role, err := session.ParseMessageRole(r)
		if err != nil {
			return nil, fmt.Errorf("invalid role %q: %w", r, err)
		}
		f.Roles = append(f.Roles, role)
	}

	if cfg.ContentPattern != "" {
		re, err := regexp.Compile(cfg.ContentPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid content pattern %q: %w", cfg.ContentPattern, err)
		}
		f.ContentPattern = re
	}

	return f, nil
}

// Name returns the filter identifier.
func (f *MessageExcluder) Name() string { return "message-excluder" }

// Apply removes matching messages from the session.
func (f *MessageExcluder) Apply(sess *session.Session) (*session.Session, *session.FilterResult, error) {
	cp := session.CopySession(sess)

	// Build the role set for fast lookup.
	roleSet := make(map[session.MessageRole]bool, len(f.Roles))
	for _, r := range f.Roles {
		roleSet[r] = true
	}

	var kept []session.Message
	removed := 0

	for i, msg := range cp.Messages {
		if f.shouldExclude(i, msg, roleSet) {
			removed++
			continue
		}
		kept = append(kept, msg)
	}

	if removed == 0 {
		return cp, &session.FilterResult{
			FilterName: f.Name(),
			Applied:    false,
			Summary:    "no messages matched exclusion criteria",
		}, nil
	}

	cp.Messages = kept

	// Build human-readable summary.
	var parts []string
	if len(f.Indices) > 0 {
		parts = append(parts, fmt.Sprintf("by index: %d", len(f.Indices)))
	}
	if len(f.Roles) > 0 {
		roleStrs := make([]string, len(f.Roles))
		for i, r := range f.Roles {
			roleStrs[i] = string(r)
		}
		parts = append(parts, fmt.Sprintf("by role: %s", strings.Join(roleStrs, ",")))
	}
	if f.ContentPattern != nil {
		parts = append(parts, fmt.Sprintf("by pattern: %s", f.ContentPattern.String()))
	}

	summary := fmt.Sprintf("removed %d message(s)", removed)
	if len(parts) > 0 {
		summary += " (" + strings.Join(parts, "; ") + ")"
	}

	return cp, &session.FilterResult{
		FilterName:      f.Name(),
		Applied:         true,
		Summary:         summary,
		MessagesRemoved: removed,
	}, nil
}

// shouldExclude checks if a message matches any exclusion criterion.
func (f *MessageExcluder) shouldExclude(idx int, msg session.Message, roleSet map[session.MessageRole]bool) bool {
	// Check index.
	if f.Indices[idx] {
		return true
	}

	// Check role.
	if roleSet[msg.Role] {
		return true
	}

	// Check content pattern.
	if f.ContentPattern != nil && f.ContentPattern.MatchString(msg.Content) {
		return true
	}

	return false
}
