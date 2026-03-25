package storage

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// ── Role Interfaces (Interface Segregation Principle) ──
//
// Each interface below groups methods by domain responsibility.
// Consumers SHOULD depend on the smallest interface they need.
// The Store interface composes all of them for DI convenience.
//
// Example: a service that only reads sessions can accept SessionReader
// instead of the full Store, making its contract explicit and testable.

// SessionReader provides read access to sessions.
type SessionReader interface {
	// Get retrieves a session by its ID.
	// Returns ErrSessionNotFound if the session does not exist.
	Get(id session.ID) (*session.Session, error)

	// GetLatestByBranch retrieves the most recent session for a project and branch.
	// Returns ErrSessionNotFound if no session matches.
	GetLatestByBranch(projectPath string, branch string) (*session.Session, error)

	// CountByBranch returns the number of sessions for a project and branch.
	CountByBranch(projectPath string, branch string) (int, error)

	// List returns session summaries matching the given options.
	List(opts session.ListOptions) ([]session.Summary, error)

	// GetFreshness returns the stored message count and source-updated-at timestamp
	// for a session. Used by the skip-if-unchanged optimization to avoid re-exporting
	// sessions that haven't changed in the source provider.
	// Returns (0, 0, nil) if the session doesn't exist (first capture).
	GetFreshness(id session.ID) (messageCount int, sourceUpdatedAt int64, err error)

	// ListProjects returns all distinct projects, grouped by remote_url (for git repos)
	// or by project_path (for non-git projects). Results are sorted by session count desc.
	ListProjects() ([]session.ProjectGroup, error)
}

// SessionWriter provides write access to sessions.
type SessionWriter interface {
	// Save stores a session. If a session with the same ID exists, it is replaced.
	Save(s *session.Session) error

	// Delete removes a session by its ID.
	// Returns ErrSessionNotFound if the session does not exist.
	Delete(id session.ID) error

	// UpdateSummary updates only the summary column (avoids full payload re-write).
	UpdateSummary(id session.ID, summary string) error

	// UpdateSessionType sets the session_type tag for a session.
	UpdateSessionType(id session.ID, sessionType string) error

	// UpdateProjectCategory bulk-updates project_category for all sessions with the given project path.
	// Returns the number of updated sessions.
	UpdateProjectCategory(projectPath, category string) (int, error)

	// SetProjectCategory sets the project_category for a single session by ID.
	SetProjectCategory(id session.ID, category string) error

	// SaveForkRelation persists a detected fork relationship (upsert).
	SaveForkRelation(rel session.ForkRelation) error

	// GetForkRelations retrieves all fork relations for a session (as original or fork).
	GetForkRelations(sessionID session.ID) ([]session.ForkRelation, error)

	// GetTotalDeduplication returns the total shared tokens across all detected forks.
	GetTotalDeduplication() (sharedInput, sharedOutput int, err error)

	// SaveObjective persists a session work objective (upsert).
	SaveObjective(obj session.SessionObjective) error

	// GetObjective retrieves the objective for a session. Returns nil if not computed yet.
	GetObjective(sessionID session.ID) (*session.SessionObjective, error)

	// ListObjectives retrieves objectives for multiple sessions.
	ListObjectives(sessionIDs []session.ID) (map[session.ID]*session.SessionObjective, error)

	// UpsertTokenBucket inserts or updates a pre-computed token usage bucket.
	UpsertTokenBucket(b session.TokenUsageBucket) error

	// QueryTokenBuckets retrieves buckets for a time range and granularity.
	QueryTokenBuckets(granularity string, since, until time.Time, projectPath string) ([]session.TokenUsageBucket, error)

	// GetLastBucketComputeTime returns the most recent compute timestamp.
	GetLastBucketComputeTime(granularity string) (time.Time, error)

	// UpdateRemoteURL sets the remote_url for a single session by ID.
	UpdateRemoteURL(id session.ID, remoteURL string) error

	// ListSessionsWithEmptyRemoteURL returns session IDs and project paths
	// for sessions that have no remote_url set. Used by the backfill task.
	ListSessionsWithEmptyRemoteURL(limit int) ([]session.BackfillCandidate, error)

	// DeleteOlderThan removes sessions created before the given time.
	// Returns the number of deleted sessions.
	DeleteOlderThan(before time.Time) (int, error)
}

// LinkStore manages associations between sessions and git objects (branches, commits, PRs)
// as well as session-to-session links.
type LinkStore interface {
	// AddLink associates a session with a git object (branch, commit, PR).
	AddLink(sessionID session.ID, link session.Link) error

	// GetByLink retrieves sessions linked to a specific ref (PR number, commit SHA, etc.).
	// Returns ErrSessionNotFound if no sessions match.
	GetByLink(linkType session.LinkType, ref string) ([]session.Summary, error)

	// LinkSessions creates a bidirectional link between two sessions.
	LinkSessions(link session.SessionLink) error

	// GetLinkedSessions retrieves all session links where the given session is either source or target.
	GetLinkedSessions(sessionID session.ID) ([]session.SessionLink, error)

	// DeleteSessionLink removes a session-to-session link by its ID.
	DeleteSessionLink(id session.ID) error
}

// UserStore manages user identities.
type UserStore interface {
	// SaveUser creates or updates a user. If a user with the same email exists,
	// it returns the existing user (upsert by email).
	SaveUser(user *session.User) error

	// GetUser retrieves a user by ID.
	GetUser(id session.ID) (*session.User, error)

	// GetUserByEmail retrieves a user by email address.
	// Returns nil, nil if no user matches.
	GetUserByEmail(email string) (*session.User, error)
}

// SearchStore provides full-text search and file-level blame queries.
type SearchStore interface {
	// Search returns sessions matching the given query criteria.
	// Supports filtering by branch, provider, owner, time range,
	// and keyword search across summary content.
	Search(query session.SearchQuery) (*session.SearchResult, error)

	// GetSessionsByFile returns sessions that touched the given file path.
	// Results are ordered by created_at DESC. Optional filters narrow by branch/provider.
	// Limit 0 means return all matching sessions.
	GetSessionsByFile(query session.BlameQuery) ([]session.BlameEntry, error)
}

// AnalysisStore persists and retrieves session analyses.
type AnalysisStore interface {
	// SaveAnalysis persists a session analysis. If an analysis with the same ID
	// exists, it is replaced (upsert).
	SaveAnalysis(a *analysis.SessionAnalysis) error

	// GetAnalysis retrieves a session analysis by its ID.
	// Returns ErrAnalysisNotFound if the analysis does not exist.
	GetAnalysis(id string) (*analysis.SessionAnalysis, error)

	// GetAnalysisBySession retrieves the most recent analysis for a session.
	// Returns ErrAnalysisNotFound if no analysis exists for the session.
	GetAnalysisBySession(sessionID string) (*analysis.SessionAnalysis, error)

	// ListAnalyses returns all analyses for a session, ordered by created_at DESC.
	ListAnalyses(sessionID string) ([]*analysis.SessionAnalysis, error)
}

// CacheStore provides key-value caching and user preference storage.
type CacheStore interface {
	// GetCache retrieves a cached value by key.
	// Returns nil, nil if the key is not found or has expired (older than maxAge).
	GetCache(key string, maxAge time.Duration) ([]byte, error)

	// SetCache stores a value with the given key, replacing any existing value.
	SetCache(key string, value []byte) error

	// InvalidateCache removes cache entries matching the prefix.
	// Pass empty prefix to invalidate ALL cache entries.
	InvalidateCache(prefix string) error

	// GetPreferences retrieves preferences for a user.
	// Pass empty userID for global defaults.
	// Returns nil, nil if no preferences are stored (callers should use system defaults).
	GetPreferences(userID session.ID) (*session.UserPreferences, error)

	// SavePreferences creates or updates preferences for a user.
	// Pass empty userID to set global defaults.
	SavePreferences(prefs *session.UserPreferences) error
}

// AuthStore manages authentication entities (auth users and API keys).
// This is part of the auth bounded context — separate from the identity-only UserStore.
type AuthStore interface {
	// CreateAuthUser persists a new auth user.
	// Returns auth.ErrUserExists if a user with the same username already exists.
	CreateAuthUser(user *auth.User) error

	// GetAuthUser retrieves an auth user by ID.
	// Returns auth.ErrUserNotFound if the user does not exist.
	GetAuthUser(id string) (*auth.User, error)

	// GetAuthUserByUsername retrieves an auth user by username.
	// Returns auth.ErrUserNotFound if the user does not exist.
	GetAuthUserByUsername(username string) (*auth.User, error)

	// UpdateAuthUser updates an existing auth user's mutable fields
	// (role, active, password_hash, updated_at).
	UpdateAuthUser(user *auth.User) error

	// ListAuthUsers returns all auth users, ordered by created_at ASC.
	ListAuthUsers() ([]*auth.User, error)

	// CreateAPIKey persists a new API key.
	CreateAPIKey(key *auth.APIKey) error

	// GetAPIKeyByHash retrieves an API key by its SHA-256 hash.
	// Returns auth.ErrAPIKeyNotFound if the key does not exist.
	GetAPIKeyByHash(keyHash string) (*auth.APIKey, error)

	// ListAPIKeysByUser returns all API keys for a user, newest first.
	ListAPIKeysByUser(userID string) ([]*auth.APIKey, error)

	// UpdateAPIKey updates an API key's mutable fields (active, last_used_at).
	UpdateAPIKey(key *auth.APIKey) error

	// DeleteAPIKey removes an API key by ID.
	DeleteAPIKey(id string) error

	// CountAuthUsers returns the total number of auth users.
	// Used to detect first-user bootstrap (first user becomes admin).
	CountAuthUsers() (int, error)
}

// ErrorStore manages structured session errors.
type ErrorStore interface {
	// SaveErrors persists a batch of session errors (upsert by error ID).
	SaveErrors(errors []session.SessionError) error

	// GetErrors retrieves all errors for a session, ordered by occurred_at ASC.
	GetErrors(sessionID session.ID) ([]session.SessionError, error)

	// GetErrorSummary computes aggregated error statistics for a session.
	GetErrorSummary(sessionID session.ID) (*session.SessionErrorSummary, error)

	// ListRecentErrors returns recent errors across all sessions, ordered by occurred_at DESC.
	// Limit 0 means use default (50).
	ListRecentErrors(limit int, category session.ErrorCategory) ([]session.SessionError, error)
}

// SessionEventStore manages structured session events and aggregated buckets.
// Events are extracted at capture time and provide both a micro view (per session)
// and a macro view (per project via pre-computed buckets).
type SessionEventStore interface {
	// SaveEvents persists a batch of session events (upsert by event ID).
	SaveEvents(events []sessionevent.Event) error

	// GetSessionEvents returns all events for a session, ordered by occurred_at ASC.
	GetSessionEvents(sessionID session.ID) ([]sessionevent.Event, error)

	// QueryEvents returns events matching the given filters.
	QueryEvents(query sessionevent.EventQuery) ([]sessionevent.Event, error)

	// DeleteSessionEvents removes all events for a session (used during re-capture).
	DeleteSessionEvents(sessionID session.ID) error

	// UpsertEventBucket inserts or merges an event bucket.
	UpsertEventBucket(bucket sessionevent.EventBucket) error

	// UpsertEventBuckets inserts or merges multiple event buckets in a single transaction.
	// Preferred for batch operations.
	UpsertEventBuckets(buckets []sessionevent.EventBucket) error

	// ReplaceEventBuckets deletes matching buckets and inserts new ones atomically.
	// Solves double-counting on re-capture by fully replacing bucket contents.
	ReplaceEventBuckets(buckets []sessionevent.EventBucket) error

	// DeleteEventBuckets removes buckets matching the query criteria.
	// Used to clean up stale buckets after session GC.
	DeleteEventBuckets(query sessionevent.BucketQuery) error

	// QueryEventBuckets returns aggregated buckets matching the given filters.
	QueryEventBuckets(query sessionevent.BucketQuery) ([]sessionevent.EventBucket, error)
}

// ── Composed Interface ──

// Store composes all role interfaces into a single persistence contract.
// The composition root (factory) creates a Store and injects it into services.
// Services that need the full Store can accept Store; services that need
// only a subset should accept the narrower role interface instead.
//
// Current implementation: sqlite/.
type Store interface {
	SessionReader
	SessionWriter
	LinkStore
	UserStore
	SearchStore
	AnalysisStore
	CacheStore
	AuthStore
	ErrorStore
	SessionEventStore

	// Close releases any resources held by the store.
	Close() error
}

// ErrAnalysisNotFound is returned when an analysis lookup yields no results.
var ErrAnalysisNotFound = analysis.ErrAnalysisNotFound
