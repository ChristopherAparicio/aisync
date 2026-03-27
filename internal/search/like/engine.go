// Package like implements the basic LIKE-based search engine.
// It wraps the existing SQLite store.Search() as a search.Engine.
// This is the fallback engine that always works.
package like

import (
	"context"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Searcher is the subset of storage.Store needed for LIKE search.
type Searcher interface {
	Search(query session.SearchQuery) (*session.SearchResult, error)
}

// Engine wraps the existing SQLite LIKE-based search.
type Engine struct {
	store Searcher
}

// New creates a LIKE search engine backed by the session store.
func New(store Searcher) *Engine {
	return &Engine{store: store}
}

func (e *Engine) Name() string { return "like" }

func (e *Engine) Capabilities() search.Capabilities {
	return search.Capabilities{
		FullText:   false, // only searches summary, not message content
		Semantic:   false,
		Facets:     false,
		Highlights: false,
		FuzzyMatch: false,
		Ranking:    false,
	}
}

func (e *Engine) Search(_ context.Context, query search.Query) (*search.Result, error) {
	start := time.Now()

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	sq := session.SearchQuery{
		Keyword:         query.Text,
		ProjectPath:     query.Filters.ProjectPath,
		RemoteURL:       query.Filters.RemoteURL,
		Branch:          query.Filters.Branch,
		Provider:        session.ProviderName(query.Filters.Provider),
		SessionType:     query.Filters.SessionType,
		ProjectCategory: query.Filters.ProjectCategory,
		Since:           query.Filters.Since,
		Until:           query.Filters.Until,
		HasErrors:       query.Filters.HasErrors,
		Limit:           limit,
		Offset:          query.Offset,
	}

	sr, err := e.store.Search(sq)
	if err != nil {
		return nil, err
	}

	result := &search.Result{
		TotalCount: sr.TotalCount,
		Took:       time.Since(start),
		Engine:     "like",
	}

	for _, s := range sr.Sessions {
		result.Hits = append(result.Hits, search.Hit{
			SessionID:   string(s.ID),
			Summary:     s.Summary,
			ProjectPath: s.ProjectPath,
			RemoteURL:   s.RemoteURL,
			Branch:      s.Branch,
			Agent:       s.Agent,
			Provider:    string(s.Provider),
			CreatedAt:   s.CreatedAt,
			Tokens:      s.TotalTokens,
			Messages:    s.MessageCount,
			Errors:      s.ErrorCount,
		})
	}

	return result, nil
}

func (e *Engine) Index(_ context.Context, _ search.Document) error {
	return nil // LIKE doesn't need indexing — it queries the sessions table directly
}

func (e *Engine) Delete(_ context.Context, _ string) error {
	return nil
}

func (e *Engine) Close() error {
	return nil
}
