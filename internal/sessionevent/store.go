package sessionevent

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// EventStore is the persistence interface for session events.
// Implementations live in infrastructure (e.g. internal/storage/sqlite).
type EventStore interface {
	// SaveEvents persists a batch of events (INSERT OR REPLACE).
	SaveEvents(events []Event) error

	// GetSessionEvents returns all events for a given session, ordered by occurred_at.
	GetSessionEvents(sessionID session.ID) ([]Event, error)

	// QueryEvents returns events matching the given filters.
	QueryEvents(query EventQuery) ([]Event, error)

	// DeleteSessionEvents removes all events for a session (used during re-capture).
	DeleteSessionEvents(sessionID session.ID) error
}

// BucketStore is the persistence interface for event buckets.
// Implementations live in infrastructure (e.g. internal/storage/sqlite).
type BucketStore interface {
	// UpsertEventBucket inserts or merges a single event bucket.
	// If a bucket with the same (start, granularity, project_path, provider) exists,
	// the counters are merged (additive).
	UpsertEventBucket(bucket EventBucket) error

	// UpsertEventBuckets inserts or merges multiple event buckets in a single transaction.
	// Preferred for batch operations to avoid per-bucket transaction overhead.
	UpsertEventBuckets(buckets []EventBucket) error

	// ReplaceEventBuckets deletes all buckets matching the given keys and inserts new ones.
	// This is the idempotent alternative to additive upsert — it fully replaces
	// bucket contents instead of accumulating, solving the double-count problem.
	ReplaceEventBuckets(buckets []EventBucket) error

	// DeleteEventBuckets removes buckets matching the query criteria.
	// Used to clean up stale buckets after session GC.
	DeleteEventBuckets(query BucketQuery) error

	// QueryEventBuckets returns buckets matching the given filters.
	QueryEventBuckets(query BucketQuery) ([]EventBucket, error)
}

// Store combines both event and bucket persistence.
type Store interface {
	EventStore
	BucketStore
}

// RecomputeQuery defines the scope for bucket recomputation.
type RecomputeQuery struct {
	ProjectPath string
	RemoteURL   string
	Provider    session.ProviderName
	Granularity string    // "1h" or "1d"
	Since       time.Time // recompute from this time
	Until       time.Time // recompute until this time
}
