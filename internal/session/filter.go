// Package session — filter.go defines the SessionFilter port interface.
//
// SessionFilter is a Strategy pattern interface for composable, chainable
// session transformations. Each filter receives a session and returns a
// modified copy. Filters are applied in chain during the restore workflow.
//
// Concrete implementations live in internal/restore/filter/ (infrastructure layer).
// This file only defines the interface and the chain runner.
package session

import "fmt"

// SessionFilter transforms a session before restore.
// Each implementation is a single-responsibility strategy:
//   - ErrorCleanerFilter: replaces tool errors with a summary
//   - MessageExcluderFilter: removes messages by index, role, or content pattern
//   - SecretRedactorFilter: obfuscates secrets as $VARIABLE_NAME
//   - EmptyMessageFilter: strips empty messages
//
// Filters MUST NOT modify the input session; they should return a copy.
// Filters MAY return a FilterResult with metadata about what changed.
type SessionFilter interface {
	// Name returns a short identifier for logging/diagnostics (e.g. "error-cleaner").
	Name() string

	// Apply transforms the session. Returns the modified session and metadata.
	// If the filter has nothing to do, it returns the session unchanged with Applied=false.
	Apply(sess *Session) (*Session, *FilterResult, error)
}

// FilterResult describes what a filter changed.
type FilterResult struct {
	// FilterName identifies which filter produced this result.
	FilterName string `json:"filter_name"`

	// Applied is true if the filter made any changes.
	Applied bool `json:"applied"`

	// Summary is a human-readable description of changes (e.g. "removed 3 empty messages").
	Summary string `json:"summary"`

	// MessagesRemoved is the count of messages removed by this filter.
	MessagesRemoved int `json:"messages_removed,omitempty"`

	// MessagesModified is the count of messages modified (content changed) by this filter.
	MessagesModified int `json:"messages_modified,omitempty"`

	// SecretsFound is the count of secrets detected (for SecretRedactorFilter).
	SecretsFound int `json:"secrets_found,omitempty"`
}

// ApplyFilters runs a chain of filters on a session, returning the final session
// and a list of results from each filter. Stops on the first error.
func ApplyFilters(sess *Session, filters []SessionFilter) (*Session, []FilterResult, error) {
	if len(filters) == 0 {
		return sess, nil, nil
	}

	current := sess
	var results []FilterResult

	for _, f := range filters {
		next, result, err := f.Apply(current)
		if err != nil {
			return nil, results, fmt.Errorf("filter %q: %w", f.Name(), err)
		}
		if result != nil {
			results = append(results, *result)
		}
		current = next
	}

	return current, results, nil
}

// CopySession creates a deep copy of a session for safe mutation by filters.
// Messages and their ToolCalls are copied; other slices are shallow-copied.
func CopySession(sess *Session) *Session {
	cp := *sess

	// Deep-copy messages (the main thing filters mutate).
	if len(sess.Messages) > 0 {
		cp.Messages = make([]Message, len(sess.Messages))
		for i, msg := range sess.Messages {
			cp.Messages[i] = msg
			// Deep-copy ToolCalls within each message.
			if len(msg.ToolCalls) > 0 {
				cp.Messages[i].ToolCalls = make([]ToolCall, len(msg.ToolCalls))
				copy(cp.Messages[i].ToolCalls, msg.ToolCalls)
			}
			// Deep-copy ContentBlocks.
			if len(msg.ContentBlocks) > 0 {
				cp.Messages[i].ContentBlocks = make([]ContentBlock, len(msg.ContentBlocks))
				copy(cp.Messages[i].ContentBlocks, msg.ContentBlocks)
			}
			// Deep-copy Images.
			if len(msg.Images) > 0 {
				cp.Messages[i].Images = make([]ImageMeta, len(msg.Images))
				copy(cp.Messages[i].Images, msg.Images)
			}
		}
	}

	// Shallow-copy other slices to avoid aliasing.
	if len(sess.Children) > 0 {
		cp.Children = make([]Session, len(sess.Children))
		copy(cp.Children, sess.Children)
	}
	if len(sess.Links) > 0 {
		cp.Links = make([]Link, len(sess.Links))
		copy(cp.Links, sess.Links)
	}
	if len(sess.FileChanges) > 0 {
		cp.FileChanges = make([]FileChange, len(sess.FileChanges))
		copy(cp.FileChanges, sess.FileChanges)
	}

	return &cp
}
