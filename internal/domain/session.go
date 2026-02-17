// Package domain contains the core entities and interfaces for aisync.
// This package has zero external dependencies (only stdlib + domain types).
package domain

import "time"

// Session represents a captured AI coding session.
type Session struct {
	ExportedAt  time.Time    `json:"exported_at"`
	CreatedAt   time.Time    `json:"created_at"`
	ProjectPath string       `json:"project_path"`
	ExportedBy  string       `json:"exported_by,omitempty"`
	ParentID    SessionID    `json:"parent_id,omitempty"`
	StorageMode StorageMode  `json:"storage_mode"`
	Summary     string       `json:"summary,omitempty"`
	ID          SessionID    `json:"id"`
	Provider    ProviderName `json:"provider"`
	Agent       string       `json:"agent"`
	Branch      string       `json:"branch,omitempty"`
	CommitSHA   string       `json:"commit_sha,omitempty"`
	Messages    []Message    `json:"messages,omitempty"`
	Children    []Session    `json:"children,omitempty"`
	Links       []Link       `json:"links,omitempty"`
	FileChanges []FileChange `json:"file_changes,omitempty"`
	TokenUsage  TokenUsage   `json:"token_usage"`
	Version     int          `json:"version"`
}

// SessionSummary is a lightweight representation of a session for listings.
type SessionSummary struct {
	CreatedAt    time.Time    `json:"created_at"`
	ID           SessionID    `json:"id"`
	Provider     ProviderName `json:"provider"`
	Agent        string       `json:"agent"`
	Branch       string       `json:"branch,omitempty"`
	Summary      string       `json:"summary,omitempty"`
	MessageCount int          `json:"message_count"`
	TotalTokens  int          `json:"total_tokens"`
}

// Message represents a single message in an AI conversation.
type Message struct {
	Timestamp time.Time   `json:"timestamp"`
	ID        string      `json:"id"`
	Content   string      `json:"content"`
	Model     string      `json:"model,omitempty"`
	Thinking  string      `json:"thinking,omitempty"`
	Role      MessageRole `json:"role"`
	ToolCalls []ToolCall  `json:"tool_calls,omitempty"`
	Tokens    int         `json:"tokens,omitempty"`
}

// ToolCall represents a tool invocation with its lifecycle.
type ToolCall struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Input      string    `json:"input"`
	Output     string    `json:"output,omitempty"`
	State      ToolState `json:"state"`
	DurationMs int       `json:"duration_ms,omitempty"`
}

// FileChange records a file touched during a session.
type FileChange struct {
	FilePath   string     `json:"file_path"`
	ChangeType ChangeType `json:"change_type"`
}

// Link connects a session to a git object (branch, commit, PR).
type Link struct {
	LinkType LinkType `json:"link_type"`
	Ref      string   `json:"ref"`
}

// TokenUsage tracks token consumption for a session.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ListOptions controls session listing queries.
type ListOptions struct {
	ProjectPath string
	Branch      string
	Provider    ProviderName
	All         bool
}
