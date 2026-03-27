package service

import (
	"context"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Search ──

// SearchRequest contains inputs for a search operation.
type SearchRequest struct {
	Keyword         string
	ProjectPath     string
	Branch          string
	Provider        session.ProviderName
	OwnerID         session.ID
	SessionType     string // filter by session type (feature, bug, etc.)
	ProjectCategory string // filter by project category (backend, frontend, etc.)
	Status          string // filter by lifecycle status: "active", "idle", "archived"
	HasErrors       string // "true" = has errors, "false" = no errors, "" = no filter
	Since           string // RFC3339 or "2006-01-02" format
	Until           string // RFC3339 or "2006-01-02" format
	Limit           int
	Offset          int
	Voice           bool // voice mode: compact results optimized for TTS
}

// Search finds sessions matching the given query criteria.
func (s *SessionService) Search(req SearchRequest) (*session.SearchResult, error) {
	// Voice mode defaults to limit=5 if not explicitly set.
	if req.Voice && req.Limit == 0 {
		req.Limit = 5
	}

	query := session.SearchQuery{
		Keyword:         req.Keyword,
		ProjectPath:     req.ProjectPath,
		Branch:          req.Branch,
		Provider:        req.Provider,
		OwnerID:         req.OwnerID,
		SessionType:     req.SessionType,
		ProjectCategory: req.ProjectCategory,
		Status:          session.SessionStatus(req.Status),
		Limit:           req.Limit,
		Offset:          req.Offset,
	}

	// Parse HasErrors filter: "true" → &true, "false" → &false, "" → nil.
	if req.HasErrors == "true" {
		v := true
		query.HasErrors = &v
	} else if req.HasErrors == "false" {
		v := false
		query.HasErrors = &v
	}

	if req.Since != "" {
		t, err := parseFlexibleTime(req.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid 'since' value %q: %w", req.Since, err)
		}
		query.Since = t
	}
	if req.Until != "" {
		t, err := parseFlexibleTime(req.Until)
		if err != nil {
			return nil, fmt.Errorf("invalid 'until' value %q: %w", req.Until, err)
		}
		query.Until = t
	}

	result, err := s.searchViaEngine(query)
	if err != nil {
		return nil, err
	}

	// Voice mode: build compact, TTS-friendly voice results.
	if req.Voice {
		now := time.Now().UTC()
		voice := make([]session.VoiceSummary, len(result.Sessions))
		for i, sum := range result.Sessions {
			voice[i] = session.VoiceSummary{
				ID:      sum.ID,
				Summary: sanitizeForVoice(sum.Summary),
				TimeAgo: humanTimeAgo(now, sum.CreatedAt),
				Agent:   sum.Agent,
				Branch:  sum.Branch,
			}
		}
		result.VoiceResults = voice
	}

	return result, nil
}

// parseFlexibleTime parses a time string in RFC3339 or date-only format.
func parseFlexibleTime(s string) (time.Time, error) {
	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try date-only
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("expected RFC3339 (2006-01-02T15:04:05Z) or date (2006-01-02)")
}

// searchViaEngine routes the search through the configured search engine,
// falling back to the store's basic LIKE search when no engine is set.
func (s *SessionService) searchViaEngine(query session.SearchQuery) (*session.SearchResult, error) {
	// If no search engine or no keyword, use the store directly (structured filters only).
	if s.searchEngine == nil || query.Keyword == "" {
		return s.store.Search(query)
	}

	// Build a search.Query from the session.SearchQuery.
	sq := search.Query{
		Text:   query.Keyword,
		Mode:   search.ModeAuto,
		Limit:  query.Limit,
		Offset: query.Offset,
		Filters: search.Filters{
			ProjectPath:     query.ProjectPath,
			RemoteURL:       query.RemoteURL,
			Branch:          query.Branch,
			Provider:        string(query.Provider),
			SessionType:     query.SessionType,
			ProjectCategory: query.ProjectCategory,
			Since:           query.Since,
			Until:           query.Until,
			HasErrors:       query.HasErrors,
		},
	}

	result, err := s.searchEngine.Search(context.Background(), sq)
	if err != nil {
		// Fallback to store on engine error.
		return s.store.Search(query)
	}

	// Convert search.Result back to session.SearchResult.
	sr := &session.SearchResult{
		TotalCount: result.TotalCount,
		Limit:      query.Limit,
		Offset:     query.Offset,
	}
	for _, hit := range result.Hits {
		sr.Sessions = append(sr.Sessions, session.Summary{
			ID:           session.ID(hit.SessionID),
			Summary:      hit.Summary,
			ProjectPath:  hit.ProjectPath,
			RemoteURL:    hit.RemoteURL,
			Branch:       hit.Branch,
			Agent:        hit.Agent,
			Provider:     session.ProviderName(hit.Provider),
			CreatedAt:    hit.CreatedAt,
			TotalTokens:  hit.Tokens,
			MessageCount: hit.Messages,
			ErrorCount:   hit.Errors,
		})
	}
	return sr, nil
}

// SearchCapabilities returns what the configured search engine supports.
func (s *SessionService) SearchCapabilities() search.Capabilities {
	if s.searchEngine == nil {
		return search.Capabilities{} // basic LIKE only
	}
	return s.searchEngine.Capabilities()
}
