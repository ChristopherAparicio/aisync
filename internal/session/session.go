// Package session contains the shared types for aisync.
// This package has zero business logic — it is a shared vocabulary that every
// other package imports. Think of it as Vacuum's model/ or gh CLI's shared types.
//
// No interfaces live here. Interfaces are defined by the packages that own the
// abstraction (provider.Provider, storage.Store).
package session

import "time"

// Session represents a captured AI coding session.
type Session struct {
	ExportedAt  time.Time    `json:"exported_at"`
	CreatedAt   time.Time    `json:"created_at"`
	ProjectPath string       `json:"project_path"`
	ExportedBy  string       `json:"exported_by,omitempty"`
	ParentID    ID           `json:"parent_id,omitempty"`
	OwnerID     ID           `json:"owner_id,omitempty"`
	StorageMode StorageMode  `json:"storage_mode"`
	Summary     string       `json:"summary,omitempty"`
	ID          ID           `json:"id"`
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

// Summary is a lightweight representation of a session for listings.
type Summary struct {
	CreatedAt    time.Time    `json:"created_at"`
	ID           ID           `json:"id"`
	OwnerID      ID           `json:"owner_id,omitempty"`
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

// Cost represents a monetary amount in a given currency.
type Cost struct {
	InputCost  float64 `json:"input_cost"`
	OutputCost float64 `json:"output_cost"`
	TotalCost  float64 `json:"total_cost"`
	Currency   string  `json:"currency"` // always "USD"
}

// CostEstimate is the full cost breakdown for a session.
type CostEstimate struct {
	TotalCost     Cost        `json:"total_cost"`
	PerModel      []ModelCost `json:"per_model"`
	UnknownModels []string    `json:"unknown_models,omitempty"` // models without pricing data
}

// ModelCost groups cost by model within a session.
type ModelCost struct {
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Cost         Cost   `json:"cost"`
	MessageCount int    `json:"message_count"`
}

// ListOptions controls session listing queries.
type ListOptions struct {
	ProjectPath string
	Branch      string
	Provider    ProviderName
	All         bool
}

// SearchQuery defines criteria for searching sessions.
// All fields are optional — an empty query returns all sessions (paginated).
// Filters are combined with AND logic.
type SearchQuery struct {
	// Keyword performs a case-insensitive text search across summary and message content.
	// In "contain" mode this uses SQL LIKE; in "fulltext" mode it uses SQLite FTS5.
	Keyword string

	// Filters narrow results by exact match on structured fields.
	ProjectPath string
	Branch      string
	Provider    ProviderName
	OwnerID     ID

	// Time range filters (inclusive). Zero values are ignored.
	Since time.Time
	Until time.Time

	// Pagination
	Limit  int // 0 means use default (50)
	Offset int
}

// SearchResult holds a page of search results with metadata.
type SearchResult struct {
	Sessions   []Summary `json:"sessions"`
	TotalCount int       `json:"total_count"`
	Limit      int       `json:"limit"`
	Offset     int       `json:"offset"`
}

// BlameEntry represents one session that touched a file.
type BlameEntry struct {
	CreatedAt  time.Time    `json:"created_at"`
	SessionID  ID           `json:"session_id"`
	OwnerID    ID           `json:"owner_id,omitempty"`
	Provider   ProviderName `json:"provider"`
	Branch     string       `json:"branch"`
	Summary    string       `json:"summary,omitempty"`
	ChangeType ChangeType   `json:"change_type"`
}

// BlameQuery contains parameters for a blame lookup.
type BlameQuery struct {
	FilePath string       // required — relative to project root
	Branch   string       // optional filter
	Provider ProviderName // optional filter
	Limit    int          // 0 = no limit (all sessions); >0 = cap results
}

// SecretMatch represents a single secret detected in content.
type SecretMatch struct {
	// Type is the category of secret (e.g., "AWS_ACCESS_KEY", "GITHUB_TOKEN").
	Type string `json:"type"`

	// Value is the detected secret value.
	Value string `json:"value"`

	// StartPos is the byte offset where the secret starts in the content.
	StartPos int `json:"start_pos"`

	// EndPos is the byte offset where the secret ends in the content.
	EndPos int `json:"end_pos"`
}

// PullRequest represents a PR/MR on a code hosting platform.
type PullRequest struct {
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	Branch     string    `json:"branch"`
	BaseBranch string    `json:"base_branch"`
	State      string    `json:"state"` // "open", "closed", "merged"
	Author     string    `json:"author"`
	Number     int       `json:"number"`
}

// PRComment represents a comment on a pull request.
type PRComment struct {
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	ID        int64     `json:"id"`
}

// StructuredSummary is an AI-generated structured analysis of a session.
// It breaks down a session into intent, outcome, decisions, friction, and open items
// for richer understanding than a one-line summary.
type StructuredSummary struct {
	Intent    string   `json:"intent"`     // what the user was trying to do
	Outcome   string   `json:"outcome"`    // what was achieved
	Decisions []string `json:"decisions"`  // key technical decisions made
	Friction  []string `json:"friction"`   // problems or difficulties encountered
	OpenItems []string `json:"open_items"` // things left unfinished or needing follow-up
}

// OneLine returns a compact one-line summary in the form "Intent: Outcome".
func (s StructuredSummary) OneLine() string {
	if s.Intent == "" && s.Outcome == "" {
		return ""
	}
	if s.Outcome == "" {
		return s.Intent
	}
	if s.Intent == "" {
		return s.Outcome
	}
	return s.Intent + ": " + s.Outcome
}

// User represents an aisync user, identified by their git identity.
type User struct {
	CreatedAt time.Time `json:"created_at"`
	ID        ID        `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Source    string    `json:"source"` // "git", "config", "api"
}
