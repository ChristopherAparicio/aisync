package notification

import (
	"sync"
	"time"
)

// ── Alert Deduplication ──

// Deduplicator suppresses duplicate notifications for the same event type
// and project within a configurable cooldown window. This prevents alert
// fatigue when conditions persist across multiple scheduler runs.
//
// The dedup key is: EventType + Project (or OwnerID for personal events).
// Digests (daily, weekly, personal) are never deduplicated — they are
// scheduled infrequently and always expected to deliver.
//
// Thread-safe: safe for concurrent use by multiple goroutines.
type Deduplicator struct {
	mu       sync.Mutex
	seen     map[string]time.Time // dedupKey → last sent timestamp
	cooldown time.Duration        // minimum interval between duplicate alerts
}

// DeduplicatorConfig configures the deduplication behavior.
type DeduplicatorConfig struct {
	// Cooldown is the minimum interval between duplicate alerts for the same
	// event type + project combination. Default: 30 minutes.
	Cooldown time.Duration
}

// NewDeduplicator creates a new deduplicator with the given configuration.
func NewDeduplicator(cfg DeduplicatorConfig) *Deduplicator {
	cooldown := cfg.Cooldown
	if cooldown <= 0 {
		cooldown = 30 * time.Minute
	}
	return &Deduplicator{
		seen:     make(map[string]time.Time),
		cooldown: cooldown,
	}
}

// ShouldSend checks whether the given event should be dispatched or suppressed.
// Returns true if the event should be sent (not a duplicate or cooldown expired).
// Returns false if the event was sent recently and should be suppressed.
//
// This method has a side effect: if it returns true, the event's timestamp is
// recorded so subsequent identical events within the cooldown window are suppressed.
func (d *Deduplicator) ShouldSend(event Event) bool {
	if d == nil {
		return true // no dedup configured — always send
	}

	// Never deduplicate digests — they run on a schedule and always deliver.
	if isDigestEvent(event.Type) {
		return true
	}

	key := dedupKey(event)

	d.mu.Lock()
	defer d.mu.Unlock()

	lastSent, exists := d.seen[key]
	now := event.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	if exists && now.Sub(lastSent) < d.cooldown {
		return false // suppress — too soon
	}

	d.seen[key] = now
	return true
}

// Prune removes expired entries from the seen map to prevent unbounded growth.
// Should be called periodically (e.g. once per hour from a scheduler tick).
func (d *Deduplicator) Prune() {
	if d == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	for key, lastSent := range d.seen {
		if now.Sub(lastSent) >= d.cooldown*2 {
			delete(d.seen, key)
		}
	}
}

// Len returns the number of tracked dedup entries (for testing/monitoring).
func (d *Deduplicator) Len() int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}

// ── Helpers ──

// dedupKey builds a composite key from event type + project (or owner for DMs).
func dedupKey(event Event) string {
	scope := event.Project
	if scope == "" {
		scope = event.ProjectPath
	}
	if scope == "" {
		scope = event.OwnerID
	}
	if scope == "" {
		scope = "_global_"
	}
	return string(event.Type) + ":" + scope
}

// isDigestEvent returns true for event types that should never be deduplicated.
func isDigestEvent(t EventType) bool {
	switch t {
	case EventDailyDigest, EventWeeklyReport, EventPersonalDaily, EventRecommendation:
		return true
	default:
		return false
	}
}

// dedupScope returns a human-readable scope string for log messages.
func dedupScope(event Event) string {
	if event.Project != "" {
		return event.Project
	}
	if event.ProjectPath != "" {
		return event.ProjectPath
	}
	if event.OwnerID != "" {
		return event.OwnerID
	}
	return "global"
}
