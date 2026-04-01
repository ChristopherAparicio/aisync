// Package search defines the port (interface) for pluggable search engines.
//
// The search system supports multiple backends with automatic fallback:
//
//	External (ES/PG/Typesense) → FTS5 (SQLite builtin) → LIKE (basic)
//
// Each engine reports its Capabilities so the UI can adapt (show highlights,
// facets, semantic results, etc.) depending on what's available.
package search

import (
	"context"
	"time"
)

// Engine is the port interface for search backends.
// Implementations live in sub-packages (like/, fts5/, elastic/, etc.).
type Engine interface {
	// Search executes a query and returns ranked results.
	Search(ctx context.Context, query Query) (*Result, error)

	// Index adds or updates a document in the search index.
	Index(ctx context.Context, doc Document) error

	// Delete removes a document from the index.
	Delete(ctx context.Context, id string) error

	// Capabilities reports what this engine supports.
	// The UI adapts based on these capabilities.
	Capabilities() Capabilities

	// Name returns the engine identifier (e.g. "fts5", "elasticsearch", "like").
	Name() string

	// Close releases resources.
	Close() error
}

// IncrementalIndexer is an optional interface that engines can implement
// to support incremental indexing. When available, IndexAllSessions skips
// sessions already present in the index.
type IncrementalIndexer interface {
	// IndexedSessionIDs returns the set of session IDs already in the index.
	IndexedSessionIDs() (map[string]bool, error)
}

// Capabilities describes what a search engine supports.
type Capabilities struct {
	FullText   bool `json:"full_text"`   // searches inside message content
	Semantic   bool `json:"semantic"`    // embedding-based similarity search
	Facets     bool `json:"facets"`      // can group results by field
	Highlights bool `json:"highlights"`  // returns highlighted snippets
	FuzzyMatch bool `json:"fuzzy_match"` // tolerates typos
	Ranking    bool `json:"ranking"`     // relevance-based ordering (BM25, etc.)
}

// SearchMode controls how the query is interpreted.
type SearchMode string

const (
	ModeKeyword  SearchMode = "keyword"  // exact keyword match (LIKE)
	ModeFullText SearchMode = "fulltext" // full-text search with ranking
	ModeSemantic SearchMode = "semantic" // embedding similarity search
	ModeAuto     SearchMode = "auto"     // engine picks best mode
)

// Query describes what to search for.
type Query struct {
	// Text is the user's search input (keywords or natural language).
	Text string `json:"text"`

	// Mode controls how Text is interpreted. Default: ModeAuto.
	Mode SearchMode `json:"mode,omitempty"`

	// Filters restrict results to matching sessions.
	Filters Filters `json:"filters,omitempty"`

	// FacetFields requests aggregation on these fields (e.g. "project", "branch").
	FacetFields []string `json:"facet_fields,omitempty"`

	// Limit and Offset for pagination.
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// Filters for narrowing search results.
type Filters struct {
	ProjectPath     string    `json:"project_path,omitempty"`
	RemoteURL       string    `json:"remote_url,omitempty"`
	Branch          string    `json:"branch,omitempty"`
	Provider        string    `json:"provider,omitempty"`
	Agent           string    `json:"agent,omitempty"`
	SessionType     string    `json:"session_type,omitempty"`
	ProjectCategory string    `json:"project_category,omitempty"`
	Since           time.Time `json:"since,omitempty"`
	Until           time.Time `json:"until,omitempty"`
	HasErrors       *bool     `json:"has_errors,omitempty"`
}

// Result is the response from a search query.
type Result struct {
	Hits       []Hit              `json:"hits"`
	TotalCount int                `json:"total_count"`
	Facets     map[string][]Facet `json:"facets,omitempty"`
	Took       time.Duration      `json:"took"`
	Engine     string             `json:"engine"` // which engine produced this result
}

// Hit is a single search result.
type Hit struct {
	SessionID   string            `json:"session_id"`
	Score       float64           `json:"score"`                // relevance score (0 = no ranking)
	Highlights  map[string]string `json:"highlights,omitempty"` // field → highlighted snippet
	Summary     string            `json:"summary"`
	ProjectPath string            `json:"project_path"`
	RemoteURL   string            `json:"remote_url,omitempty"`
	Branch      string            `json:"branch,omitempty"`
	Agent       string            `json:"agent,omitempty"`
	Provider    string            `json:"provider,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	Tokens      int               `json:"tokens,omitempty"`
	Messages    int               `json:"messages,omitempty"`
	Errors      int               `json:"errors,omitempty"`
}

// Facet is a single bucket in a faceted aggregation.
type Facet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Document is the indexable representation of a session.
type Document struct {
	ID              string    `json:"id"`
	Summary         string    `json:"summary"`
	Content         string    `json:"content"`    // concatenated message content (truncated)
	ToolNames       string    `json:"tool_names"` // space-separated tool names used
	ProjectPath     string    `json:"project_path"`
	RemoteURL       string    `json:"remote_url"`
	Branch          string    `json:"branch"`
	Agent           string    `json:"agent"`
	Provider        string    `json:"provider"`
	SessionType     string    `json:"session_type"`
	ProjectCategory string    `json:"project_category"`
	CreatedAt       time.Time `json:"created_at"`
	TotalTokens     int       `json:"total_tokens"`
	MessageCount    int       `json:"message_count"`
	ErrorCount      int       `json:"error_count"`
}
