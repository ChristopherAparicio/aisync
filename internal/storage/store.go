package storage

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/internal/registry"
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

	// GetBatch retrieves multiple sessions by ID in a minimal number of queries.
	// Returns a map keyed by session ID; missing IDs are simply absent from the map
	// (no error is raised for not-found entries — callers should check map membership).
	//
	// This is the batch-aware counterpart to Get() and is the preferred API for any
	// code path that iterates over a Summary slice and needs the full Session for each
	// element. A single GetBatch call performs 3 SQL queries total regardless of how
	// many IDs are requested, versus 3×N queries for a Get() loop.
	GetBatch(ids []session.ID) (map[session.ID]*session.Session, error)

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

	// ListAllForkRelations returns every fork relation in the database.
	// Used by ComputeTokenBuckets and Forecast to build a dedup map.
	ListAllForkRelations() ([]session.ForkRelation, error)

	// GetForkRelationsForSessions returns fork relations for multiple sessions in a single query.
	// Returns a map of session ID → []ForkRelation. A session appears in the map if it has
	// any fork relations (as original or fork).
	GetForkRelationsForSessions(ids []session.ID) (map[session.ID][]session.ForkRelation, error)

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

	// UpsertToolBucket inserts or updates a per-tool usage bucket.
	UpsertToolBucket(b session.ToolUsageBucket) error

	// QueryToolBuckets retrieves per-tool usage buckets for a time range.
	QueryToolBuckets(granularity string, since, until time.Time, projectPath string) ([]session.ToolUsageBucket, error)

	// UpdateRemoteURL sets the remote_url for a single session by ID.
	UpdateRemoteURL(id session.ID, remoteURL string) error

	// ListSessionsWithEmptyRemoteURL returns session IDs and project paths
	// for sessions that have no remote_url set. Used by the backfill task.
	ListSessionsWithEmptyRemoteURL(limit int) ([]session.BackfillCandidate, error)

	// DeleteOlderThan removes sessions created before the given time.
	// Returns the number of deleted sessions.
	DeleteOlderThan(before time.Time) (int, error)

	// ReplaceSessionFiles atomically replaces all file changes for a session.
	// Used by the file-blame extractor after parsing tool calls.
	ReplaceSessionFiles(sessionID session.ID, records []session.SessionFileRecord) error

	// GetSessionFileChanges returns all file change records for a session.
	GetSessionFileChanges(sessionID session.ID) ([]session.SessionFileRecord, error)

	// CountSessionsWithFiles returns how many sessions have file-blame data.
	CountSessionsWithFiles() (int, error)

	// SearchFacets returns aggregated counts for faceted navigation.
	// Respects the same filters as Search (project, date range, etc.) so that
	// facet counts reflect the current search context.
	SearchFacets(q session.SearchQuery) (*session.SearchFacets, error)

	// UpdateCosts sets the denormalized cost columns for a session.
	// Used by the cost backfill task to populate costs for existing sessions.
	UpdateCosts(id session.ID, estimatedCost, actualCost float64) error

	// ListSessionsWithZeroCosts returns session IDs that have estimated_cost = 0.
	// Used by the cost backfill task to find sessions needing cost computation.
	ListSessionsWithZeroCosts(limit int) ([]session.ID, error)

	// ── Materialized analytics (CQRS read model) ───────────────────
	//
	// The three methods below persist and query the session_analytics table
	// (migration 031), which is the materialized read model that replaces the
	// old stats_cache / dashboard_warm_task layer. Analytics are computed once
	// per session by session.ComputeAnalytics() and stamped via the
	// service-layer stampAnalytics() hook in the same write path as Save() —
	// see the UpdateCosts + cost_backfill_task precedent above for the same
	// pattern applied to cost columns.
	//
	// Hot-path handlers (Forecast, CacheEfficiency, ContextSaturation,
	// AgentROIAnalysis) read rows via QueryAnalytics() and aggregate them in
	// memory, turning a ~12-14s cold path into a ~50-100ms indexed scan.
	// When a handler hits a session whose row is missing or stale (older
	// schema version), it falls back to session.ComputeAnalytics() on the
	// live session payload; the AnalyticsBackfillTask then upserts the fresh
	// row on the next cron tick so the fallback is self-healing.

	// UpsertSessionAnalytics stores or replaces the materialized analytics
	// row for a session, including its sibling session_agent_usage rows.
	// The entire operation runs in a single transaction: the parent row in
	// session_analytics is upserted, all existing session_agent_usage rows
	// for the session are deleted, and the AgentUsage slice from the payload
	// is inserted fresh. This guarantees the per-agent rollup is always
	// consistent with the parent row and never partially updated.
	//
	// Called from the service-layer stampAnalytics() hook, the cost backfill
	// task, and the new AnalyticsBackfillTask.
	UpsertSessionAnalytics(a session.Analytics) error

	// GetSessionAnalytics retrieves the materialized analytics for a single
	// session, with its AgentUsage slice hydrated from session_agent_usage.
	// Returns (nil, nil) — not an error — when no row exists for the session;
	// callers (typically the hot-path handlers) interpret this as "not yet
	// computed" and fall back to session.ComputeAnalytics() on the live
	// payload. This is the intentional CQRS safety net: readers never fail
	// just because the write model hasn't caught up.
	GetSessionAnalytics(id session.ID) (*session.Analytics, error)

	// QueryAnalytics returns all analytics rows matching the filter, joined
	// with the parent sessions table to populate AnalyticsRow.ProjectPath,
	// CreatedAt, and Branch in a single query. AgentUsage is NOT hydrated
	// by this call — the hot paths that use QueryAnalytics aggregate at the
	// parent-row level and read per-agent rollups separately when needed.
	//
	// Results are ordered by sessions.created_at DESC. When filter.Since or
	// filter.Until are zero, the corresponding bound is omitted. When
	// filter.MinSchemaVersion is zero, rows of any schema version are
	// returned (the backfill task relies on this to find stale rows).
	QueryAnalytics(filter session.AnalyticsFilter) ([]session.AnalyticsRow, error)

	// ListSessionsNeedingAnalytics returns session IDs that either have no row
	// in session_analytics at all, or have a row with schema_version < the
	// provided minSchemaVersion. Used by the analytics backfill task to find
	// sessions needing (re)computation. Results are ordered by created_at DESC
	// so that the most recent sessions are processed first.
	ListSessionsNeedingAnalytics(minSchemaVersion int, limit int) ([]session.ID, error)
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

	// ListUsers returns all users ordered by name.
	ListUsers() ([]*session.User, error)

	// ListUsersByKind returns users filtered by kind ("human", "machine", "unknown").
	ListUsersByKind(kind string) ([]*session.User, error)

	// UpdateUserSlack sets the Slack identity fields for a user.
	UpdateUserSlack(id session.ID, slackID, slackName string) error

	// UpdateUserKind sets the kind classification for a user.
	UpdateUserKind(id session.ID, kind string) error

	// UpdateUserRole sets the notification role for a user.
	UpdateUserRole(id session.ID, role string) error

	// OwnerStats returns aggregated session statistics grouped by owner_id
	// for a given time range and optional project filter (empty = all projects).
	OwnerStats(projectPath string, since, until time.Time) ([]session.OwnerStat, error)
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

	// TopFilesForProject returns the most frequently touched files for a project,
	// aggregated from file_changes. Results are sorted by session count descending.
	TopFilesForProject(projectPath string, limit int) ([]session.TopFileEntry, error)

	// FilesForProject returns all files touched in a project with blame summary
	// (session count, last session info). Results are sorted by last_session_time DESC.
	// If dirPrefix is non-empty, only files under that directory are returned.
	FilesForProject(projectPath string, dirPrefix string, limit int) ([]session.ProjectFileEntry, error)
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

	// GetSessionEventsBatch returns events for multiple sessions in a single SQL query,
	// keyed by session ID. This is the batch-aware counterpart to GetSessionEvents and
	// exists to eliminate N+1 patterns in analytics paths (e.g. SkillROIAnalysis) that
	// previously looped over a Summary slice calling GetSessionEvents per session.
	//
	// Missing session IDs are silently omitted from the result map (no empty slice entry).
	// Duplicate IDs are de-duplicated before the IN-clause is built.
	//
	// The variadic types parameter filters events at the SQL level via event_type IN (...).
	// Passing zero types returns all event types for the requested sessions. Callers that
	// only need a subset (e.g. only skill_load events) SHOULD pass explicit types to avoid
	// transferring unrelated event payloads — this can turn a multi-hundred-thousand row
	// scan into a few thousand.
	//
	// Within each returned slice, events are ordered by occurred_at ASC, message_index ASC
	// (same ordering as GetSessionEvents for compatibility).
	GetSessionEventsBatch(ids []session.ID, types ...sessionevent.EventType) (map[session.ID][]sessionevent.Event, error)

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

// PullRequestStore manages pull request persistence and session-PR associations.
type PullRequestStore interface {
	// SavePullRequest creates or updates a pull request (upsert by repo_owner/repo_name/number).
	SavePullRequest(pr *session.PullRequest) error

	// GetPullRequest retrieves a pull request by owner, repo and number.
	GetPullRequest(repoOwner, repoName string, number int) (*session.PullRequest, error)

	// ListPullRequests returns pull requests matching optional filters.
	// If repoOwner+repoName are empty, returns all PRs. State "" means all states.
	ListPullRequests(repoOwner, repoName, state string, limit int) ([]session.PullRequest, error)

	// LinkSessionPR links a session to a pull request.
	// Idempotent — duplicate links are silently ignored.
	LinkSessionPR(sessionID session.ID, repoOwner, repoName string, prNumber int) error

	// GetSessionsForPR returns all sessions linked to a specific PR.
	GetSessionsForPR(repoOwner, repoName string, prNumber int) ([]session.Summary, error)

	// GetPRsForSession returns all PRs linked to a specific session.
	GetPRsForSession(sessionID session.ID) ([]session.PullRequest, error)

	// ListPRsWithSessions returns PRs enriched with linked session data.
	// This is the main query for the /pulls page.
	ListPRsWithSessions(repoOwner, repoName, state string, limit int) ([]session.PRWithSessions, error)

	// GetPRByBranch finds a pull request by its source branch name.
	// Returns the most recently updated PR matching the branch.
	// Returns session.ErrPRNotFound if no PR matches.
	GetPRByBranch(branch string) (*session.PullRequest, error)
}

// RecommendationStore persists and retrieves actionable recommendations.
type RecommendationStore interface {
	// UpsertRecommendation creates or updates a recommendation by fingerprint.
	// If a recommendation with the same fingerprint exists and is active,
	// its title/message/impact/priority are updated. Dismissed/snoozed recs
	// are NOT overwritten (user intent is preserved).
	UpsertRecommendation(rec *session.RecommendationRecord) error

	// ListRecommendations returns recommendations matching the filter.
	// Results are ordered by priority (high > medium > low), then created_at DESC.
	ListRecommendations(filter session.RecommendationFilter) ([]session.RecommendationRecord, error)

	// DismissRecommendation marks a recommendation as dismissed by ID.
	DismissRecommendation(id string) error

	// SnoozeRecommendation marks a recommendation as snoozed until the given time.
	SnoozeRecommendation(id string, until time.Time) error

	// ExpireRecommendations marks active recommendations older than maxAge as expired.
	// Returns the count of expired recommendations.
	ExpireRecommendations(maxAge time.Duration) (int, error)

	// ReactivateSnoozed reactivates snoozed recommendations whose snooze period has passed.
	// Returns the count of reactivated recommendations.
	ReactivateSnoozed() (int, error)

	// RecommendationStats returns aggregate counts by status for a project (empty = all).
	RecommendationStats(projectPath string) (session.RecommendationStats, error)

	// DeleteRecommendationsByProject removes all recommendations for a project.
	DeleteRecommendationsByProject(projectPath string) (int, error)
}

// HotspotStore persists pre-computed session hot-spots (Section 8.3).
// Hot-spots are computed by the nightly HotspotsTask and read by the
// session detail "Hot Spots" tab and investigation API.
type HotspotStore interface {
	// GetHotspots retrieves pre-computed hot-spots for a session.
	// Returns (nil, nil) when no row exists (not yet computed).
	GetHotspots(id session.ID) (*session.SessionHotspots, error)

	// SetHotspots persists hot-spots for a session (upsert).
	// The payload is compressed (gzip) before storage.
	SetHotspots(id session.ID, h session.SessionHotspots, schemaVersion int) error

	// ListSessionsNeedingHotspots returns session IDs that either have no
	// row in session_hotspots or have schema_version < minSchemaVersion.
	// Results are ordered by created_at DESC. Used by the backfill task.
	ListSessionsNeedingHotspots(minSchemaVersion int, limit int) ([]session.ID, error)
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
	RegistryStore
	PullRequestStore
	RecommendationStore
	HotspotStore

	// Close releases any resources held by the store.
	Close() error
}

// RegistryStore persists project capability snapshots and flat capability records.
type RegistryStore interface {
	// SaveProjectSnapshot persists a capability snapshot for a project.
	SaveProjectSnapshot(snapshot *registry.ProjectSnapshot) error

	// GetLatestSnapshot returns the most recent snapshot for a project.
	// Returns nil, nil if no snapshots exist.
	GetLatestSnapshot(projectPath string) (*registry.ProjectSnapshot, error)

	// ListSnapshots returns all snapshots for a project, newest first.
	ListSnapshots(projectPath string, limit int) ([]registry.ProjectSnapshot, error)

	// UpsertCapabilities inserts or updates flat capability records for a project.
	// Capabilities present in the list are marked active with updated last_seen.
	// Capabilities NOT in the list but previously active are marked inactive.
	UpsertCapabilities(projectPath string, caps []registry.PersistedCapability) error

	// ListCapabilities returns persisted capabilities matching the filter.
	ListCapabilities(filter registry.CapabilityFilter) ([]registry.PersistedCapability, error)

	// ListCapabilityProjects returns distinct project paths that have at least
	// one persisted capability.
	ListCapabilityProjects() ([]string, error)
}

// ErrAnalysisNotFound is returned when an analysis lookup yields no results.
var ErrAnalysisNotFound = analysis.ErrAnalysisNotFound
