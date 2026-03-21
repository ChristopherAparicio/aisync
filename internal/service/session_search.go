package service

import (
	"fmt"
	"time"

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
		Limit:           req.Limit,
		Offset:          req.Offset,
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

	result, err := s.store.Search(query)
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
