package testutil

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// Compile-time check: MockStore must satisfy storage.Store.
var _ storage.Store = (*MockStore)(nil)

// MockStore is a centralized in-memory implementation of storage.Store for tests.
// It replaces the 14 duplicated mockStore definitions across the codebase.
//
// Usage:
//
//	store := testutil.NewMockStore()                  // empty store
//	store := testutil.NewMockStore(sess1, sess2)      // pre-populated
//
// After test:
//
//	store.SavedSessions  → all sessions passed to Save()
//	store.SaveCount      → number of Save() calls
//	store.LastSaved       → most recent session passed to Save()
//	store.Deleted        → IDs passed to Delete()
type MockStore struct {
	// Sessions holds all stored sessions keyed by ID.
	Sessions map[session.ID]*session.Session

	// Summaries can be pre-populated for List() calls.
	// If nil, List() builds summaries from Sessions.
	Summaries []session.Summary

	// ByBranch maps "projectPath:branch" → session for GetLatestByBranch.
	// Populated automatically by Save().
	ByBranch map[string]*session.Session

	// Links maps "linkType:ref" → summaries for GetByLink.
	Links map[string][]session.Summary

	// LinksList stores raw links added via AddLink (for assertion).
	LinksList []session.Link

	// BlameEntries is the return value for GetSessionsByFile.
	BlameEntries []session.BlameEntry

	// Analyses stores session analyses keyed by analysis ID.
	Analyses map[string]*analysis.SessionAnalysis

	// Freshness maps session ID → [messageCount, sourceUpdatedAt].
	Freshness map[session.ID][2]int64

	// SearchFunc allows tests to override Search behavior.
	// If nil, Search returns an empty result.
	SearchFunc func(session.SearchQuery) (*session.SearchResult, error)

	// ── Tracking fields for assertions ──

	// SaveCount tracks the number of Save() calls.
	SaveCount int

	// LastSaved holds the most recent session passed to Save().
	LastSaved *session.Session

	// Deleted collects IDs passed to Delete().
	Deleted []session.ID

	// DeletedByGC tracks sessions removed by DeleteOlderThan.
	DeletedByGC int

	// SessionLinks stores session-to-session links added via LinkSessions.
	SessionLinks []session.SessionLink
}

// NewMockStore creates a MockStore, optionally pre-populated with sessions.
func NewMockStore(sessions ...*session.Session) *MockStore {
	m := &MockStore{
		Sessions: make(map[session.ID]*session.Session),
		ByBranch: make(map[string]*session.Session),
		Links:    make(map[string][]session.Summary),
		Analyses: make(map[string]*analysis.SessionAnalysis),
	}
	for _, s := range sessions {
		m.Sessions[s.ID] = s
		if s.ProjectPath != "" && s.Branch != "" {
			m.ByBranch[s.ProjectPath+":"+s.Branch] = s
		}
	}
	return m
}

// ── Session CRUD ──

func (m *MockStore) Save(s *session.Session) error {
	m.Sessions[s.ID] = s
	m.LastSaved = s
	m.SaveCount++
	if s.ProjectPath != "" || s.Branch != "" {
		m.ByBranch[s.ProjectPath+":"+s.Branch] = s
	}
	return nil
}

func (m *MockStore) Get(id session.ID) (*session.Session, error) {
	s, ok := m.Sessions[id]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return s, nil
}

func (m *MockStore) GetLatestByBranch(projectPath, branch string) (*session.Session, error) {
	s, ok := m.ByBranch[projectPath+":"+branch]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return s, nil
}

func (m *MockStore) CountByBranch(_, _ string) (int, error) {
	return m.SaveCount, nil
}

func (m *MockStore) List(opts session.ListOptions) ([]session.Summary, error) {
	// If Summaries is pre-populated, return it directly.
	if m.Summaries != nil {
		return m.Summaries, nil
	}
	// Otherwise build from Sessions map.
	var result []session.Summary
	for _, s := range m.Sessions {
		if opts.Branch != "" && s.Branch != opts.Branch {
			continue
		}
		result = append(result, session.Summary{
			ID:           s.ID,
			Provider:     s.Provider,
			Branch:       s.Branch,
			Summary:      s.Summary,
			MessageCount: len(s.Messages),
			TotalTokens:  s.TokenUsage.TotalTokens,
			CreatedAt:    s.CreatedAt,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (m *MockStore) UpdateSummary(id session.ID, summary string) error {
	s, ok := m.Sessions[id]
	if !ok {
		return session.ErrSessionNotFound
	}
	s.Summary = summary
	return nil
}

func (m *MockStore) UpdateSessionType(id session.ID, sessionType string) error {
	s, ok := m.Sessions[id]
	if !ok {
		return session.ErrSessionNotFound
	}
	s.SessionType = sessionType
	return nil
}

func (m *MockStore) UpdateProjectCategory(projectPath, category string) (int, error) {
	var count int
	for _, s := range m.Sessions {
		if s.ProjectPath == projectPath && s.ProjectCategory == "" {
			s.ProjectCategory = category
			count++
		}
	}
	return count, nil
}

func (m *MockStore) SetProjectCategory(id session.ID, category string) error {
	s, ok := m.Sessions[id]
	if !ok {
		return session.ErrSessionNotFound
	}
	s.ProjectCategory = category
	return nil
}

func (m *MockStore) UpdateRemoteURL(id session.ID, remoteURL string) error {
	s, ok := m.Sessions[id]
	if !ok {
		return session.ErrSessionNotFound
	}
	s.RemoteURL = remoteURL
	return nil
}

func (m *MockStore) ListSessionsWithEmptyRemoteURL(limit int) ([]session.BackfillCandidate, error) {
	var candidates []session.BackfillCandidate
	for _, s := range m.Sessions {
		if s.RemoteURL == "" && s.ProjectPath != "" {
			candidates = append(candidates, session.BackfillCandidate{
				ID:          s.ID,
				ProjectPath: s.ProjectPath,
			})
		}
	}
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

func (m *MockStore) SaveForkRelation(_ session.ForkRelation) error { return nil }
func (m *MockStore) GetForkRelations(_ session.ID) ([]session.ForkRelation, error) {
	return nil, nil
}
func (m *MockStore) ListAllForkRelations() ([]session.ForkRelation, error) { return nil, nil }
func (m *MockStore) GetTotalDeduplication() (int, int, error)              { return 0, 0, nil }

func (m *MockStore) SaveObjective(_ session.SessionObjective) error { return nil }
func (m *MockStore) GetObjective(_ session.ID) (*session.SessionObjective, error) {
	return nil, nil
}
func (m *MockStore) ListObjectives(_ []session.ID) (map[session.ID]*session.SessionObjective, error) {
	return nil, nil
}

func (m *MockStore) UpsertTokenBucket(_ session.TokenUsageBucket) error { return nil }
func (m *MockStore) QueryTokenBuckets(_ string, _, _ time.Time, _ string) ([]session.TokenUsageBucket, error) {
	return nil, nil
}
func (m *MockStore) GetLastBucketComputeTime(_ string) (time.Time, error) {
	return time.Time{}, nil
}

func (m *MockStore) UpsertToolBucket(_ session.ToolUsageBucket) error { return nil }
func (m *MockStore) QueryToolBuckets(_ string, _, _ time.Time, _ string) ([]session.ToolUsageBucket, error) {
	return nil, nil
}

func (m *MockStore) Delete(id session.ID) error {
	m.Deleted = append(m.Deleted, id)
	delete(m.Sessions, id)
	return nil
}

// ── Links ──

func (m *MockStore) AddLink(sessionID session.ID, link session.Link) error {
	m.LinksList = append(m.LinksList, link)
	key := fmt.Sprintf("%s:%s", link.LinkType, link.Ref)
	// Build summary from session if available.
	if s, ok := m.Sessions[sessionID]; ok {
		summary := session.Summary{
			ID:       s.ID,
			Provider: s.Provider,
			Branch:   s.Branch,
			Summary:  s.Summary,
		}
		m.Links[key] = append(m.Links[key], summary)
	}
	return nil
}

func (m *MockStore) GetByLink(linkType session.LinkType, ref string) ([]session.Summary, error) {
	key := fmt.Sprintf("%s:%s", linkType, ref)
	summaries, ok := m.Links[key]
	if !ok || len(summaries) == 0 {
		return nil, session.ErrSessionNotFound
	}
	return summaries, nil
}

// ── Session Links ──

func (m *MockStore) LinkSessions(link session.SessionLink) error {
	m.SessionLinks = append(m.SessionLinks, link)
	return nil
}

func (m *MockStore) GetLinkedSessions(sessionID session.ID) ([]session.SessionLink, error) {
	var result []session.SessionLink
	for _, l := range m.SessionLinks {
		if l.SourceSessionID == sessionID || l.TargetSessionID == sessionID {
			result = append(result, l)
		}
	}
	return result, nil
}

func (m *MockStore) DeleteSessionLink(id session.ID) error {
	for i, l := range m.SessionLinks {
		if l.ID == id {
			m.SessionLinks = append(m.SessionLinks[:i], m.SessionLinks[i+1:]...)
			return nil
		}
	}
	return nil
}

// ── Users ──

func (m *MockStore) SaveUser(_ *session.User) error                 { return nil }
func (m *MockStore) GetUser(_ session.ID) (*session.User, error)    { return nil, nil }
func (m *MockStore) GetUserByEmail(_ string) (*session.User, error) { return nil, nil }

// ── Search & Blame ──

func (m *MockStore) Search(q session.SearchQuery) (*session.SearchResult, error) {
	if m.SearchFunc != nil {
		return m.SearchFunc(q)
	}
	return &session.SearchResult{}, nil
}

func (m *MockStore) GetSessionsByFile(_ session.BlameQuery) ([]session.BlameEntry, error) {
	return m.BlameEntries, nil
}

func (m *MockStore) TopFilesForProject(_ string, _ int) ([]session.TopFileEntry, error) {
	return nil, nil
}

// ── Lifecycle ──

func (m *MockStore) ReplaceSessionFiles(_ session.ID, _ []session.SessionFileRecord) error {
	return nil
}
func (m *MockStore) GetSessionFileChanges(_ session.ID) ([]session.SessionFileRecord, error) {
	return nil, nil
}
func (m *MockStore) CountSessionsWithFiles() (int, error) { return 0, nil }

func (m *MockStore) DeleteOlderThan(before time.Time) (int, error) {
	count := 0
	for _, s := range m.Sessions {
		if !s.CreatedAt.IsZero() && s.CreatedAt.Before(before) {
			count++
		}
	}
	m.DeletedByGC = count
	return count, nil
}

func (m *MockStore) GetFreshness(id session.ID) (int, int64, error) {
	if m.Freshness != nil {
		if f, ok := m.Freshness[id]; ok {
			return int(f[0]), f[1], nil
		}
	}
	return 0, 0, nil
}

func (m *MockStore) ListProjects() ([]session.ProjectGroup, error) { return nil, nil }

func (m *MockStore) Close() error { return nil }

// ── RegistryStore ──

func (m *MockStore) SaveProjectSnapshot(_ *registry.ProjectSnapshot) error {
	return nil
}

func (m *MockStore) GetLatestSnapshot(_ string) (*registry.ProjectSnapshot, error) {
	return nil, nil
}

func (m *MockStore) ListSnapshots(_ string, _ int) ([]registry.ProjectSnapshot, error) {
	return nil, nil
}

// ── Analysis ──

func (m *MockStore) SaveAnalysis(a *analysis.SessionAnalysis) error {
	m.Analyses[a.ID] = a
	return nil
}

func (m *MockStore) GetAnalysis(id string) (*analysis.SessionAnalysis, error) {
	if a, ok := m.Analyses[id]; ok {
		return a, nil
	}
	return nil, analysis.ErrAnalysisNotFound
}

func (m *MockStore) GetAnalysisBySession(sessionID string) (*analysis.SessionAnalysis, error) {
	for _, a := range m.Analyses {
		if a.SessionID == sessionID {
			return a, nil
		}
	}
	return nil, analysis.ErrAnalysisNotFound
}

func (m *MockStore) ListAnalyses(sessionID string) ([]*analysis.SessionAnalysis, error) {
	var results []*analysis.SessionAnalysis
	for _, a := range m.Analyses {
		if a.SessionID == sessionID {
			results = append(results, a)
		}
	}
	return results, nil
}

// ── Cache ──

func (m *MockStore) GetCache(_ string, _ time.Duration) ([]byte, error) { return nil, nil }
func (m *MockStore) SetCache(_ string, _ []byte) error                  { return nil }
func (m *MockStore) InvalidateCache(_ string) error                     { return nil }

// ── User Preferences ──

func (m *MockStore) GetPreferences(_ session.ID) (*session.UserPreferences, error) {
	return nil, nil
}
func (m *MockStore) SavePreferences(_ *session.UserPreferences) error { return nil }

// ── Error Store (stubs) ──

func (m *MockStore) SaveErrors(_ []session.SessionError) error { return nil }
func (m *MockStore) GetErrors(_ session.ID) ([]session.SessionError, error) {
	return nil, nil
}
func (m *MockStore) GetErrorSummary(_ session.ID) (*session.SessionErrorSummary, error) {
	return nil, nil
}
func (m *MockStore) ListRecentErrors(_ int, _ session.ErrorCategory) ([]session.SessionError, error) {
	return nil, nil
}

// ── Auth Store (stubs) ──

func (m *MockStore) CreateAuthUser(_ *auth.User) error        { return nil }
func (m *MockStore) GetAuthUser(_ string) (*auth.User, error) { return nil, auth.ErrUserNotFound }
func (m *MockStore) GetAuthUserByUsername(_ string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (m *MockStore) UpdateAuthUser(_ *auth.User) error    { return nil }
func (m *MockStore) ListAuthUsers() ([]*auth.User, error) { return nil, nil }
func (m *MockStore) CreateAPIKey(_ *auth.APIKey) error    { return nil }
func (m *MockStore) GetAPIKeyByHash(_ string) (*auth.APIKey, error) {
	return nil, auth.ErrAPIKeyNotFound
}
func (m *MockStore) ListAPIKeysByUser(_ string) ([]*auth.APIKey, error) { return nil, nil }
func (m *MockStore) UpdateAPIKey(_ *auth.APIKey) error                  { return nil }
func (m *MockStore) DeleteAPIKey(_ string) error                        { return nil }
func (m *MockStore) CountAuthUsers() (int, error)                       { return 0, nil }

// ── Session Event Store (stubs) ──

func (m *MockStore) SaveEvents(_ []sessionevent.Event) error { return nil }
func (m *MockStore) GetSessionEvents(_ session.ID) ([]sessionevent.Event, error) {
	return nil, nil
}
func (m *MockStore) QueryEvents(_ sessionevent.EventQuery) ([]sessionevent.Event, error) {
	return nil, nil
}
func (m *MockStore) DeleteSessionEvents(_ session.ID) error { return nil }
func (m *MockStore) UpsertEventBucket(_ sessionevent.EventBucket) error {
	return nil
}
func (m *MockStore) UpsertEventBuckets(_ []sessionevent.EventBucket) error {
	return nil
}
func (m *MockStore) ReplaceEventBuckets(_ []sessionevent.EventBucket) error {
	return nil
}
func (m *MockStore) DeleteEventBuckets(_ sessionevent.BucketQuery) error {
	return nil
}
func (m *MockStore) QueryEventBuckets(_ sessionevent.BucketQuery) ([]sessionevent.EventBucket, error) {
	return nil, nil
}

// ── Search Helper ──

// DefaultSearchFunc returns a SearchFunc that filters Summaries by keyword, branch,
// and provider. Useful for tests that need realistic Search behavior.
func DefaultSearchFunc(m *MockStore) func(session.SearchQuery) (*session.SearchResult, error) {
	return func(q session.SearchQuery) (*session.SearchResult, error) {
		summaries, _ := m.List(session.ListOptions{})
		var filtered []session.Summary
		for _, s := range summaries {
			if q.Keyword != "" && !strings.Contains(strings.ToLower(s.Summary), strings.ToLower(q.Keyword)) {
				continue
			}
			if q.Branch != "" && s.Branch != q.Branch {
				continue
			}
			if q.Provider != "" && s.Provider != q.Provider {
				continue
			}
			filtered = append(filtered, s)
		}
		total := len(filtered)
		if q.Offset > 0 && q.Offset < len(filtered) {
			filtered = filtered[q.Offset:]
		}
		if q.Limit > 0 && q.Limit < len(filtered) {
			filtered = filtered[:q.Limit]
		}
		return &session.SearchResult{
			Sessions:   filtered,
			TotalCount: total,
		}, nil
	}
}
