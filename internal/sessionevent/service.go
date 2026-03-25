package sessionevent

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Service orchestrates event extraction, persistence, and querying.
// It provides both the micro view (single session) and macro view (project-wide buckets).
type Service struct {
	store      Store
	processor  *Processor
	aggregator *BucketAggregator
	logger     *slog.Logger
}

// NewService creates a new event service.
func NewService(store Store, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:      store,
		processor:  NewProcessor(),
		aggregator: NewBucketAggregator(),
		logger:     logger,
	}
}

// ── Capture-time entry point ──

// ProcessSession extracts events from a session, persists them, and updates buckets.
// This should be called as part of the post-capture pipeline (similar to ErrorService.ProcessSession).
//
// It is idempotent: if called again for the same session, it deletes old events first.
func (s *Service) ProcessSession(sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("sessionevent: nil session")
	}

	s.logger.Info("processing session events",
		"session_id", sess.ID,
		"provider", sess.Provider,
		"message_count", len(sess.Messages),
	)

	// 1. Extract events.
	events, summary := s.processor.ExtractAll(sess)

	s.logger.Info("extracted session events",
		"session_id", sess.ID,
		"total_events", summary.TotalEvents,
		"tool_calls", summary.ToolCallCount,
		"skills_loaded", summary.SkillLoadCount,
		"commands", summary.CommandCount,
		"errors", summary.ErrorCount,
		"images", summary.ImageCount,
		"unique_tools", summary.UniqueToolCount,
	)

	if len(events) == 0 {
		return nil
	}

	// 2. Delete previous events for this session (idempotent re-capture).
	if err := s.store.DeleteSessionEvents(sess.ID); err != nil {
		s.logger.Warn("failed to delete old events, proceeding with save",
			"session_id", sess.ID,
			"error", err,
		)
	}

	// 3. Persist events.
	if err := s.store.SaveEvents(events); err != nil {
		return fmt.Errorf("sessionevent: save events: %w", err)
	}

	// 4. Recompute buckets for affected time windows.
	// Instead of additive upsert (which double-counts on re-capture), we:
	//   a) Query ALL events in the affected time windows (from all sessions)
	//   b) Aggregate them into fresh buckets
	//   c) Replace the old buckets entirely
	// This makes ProcessSession fully idempotent.
	if err := s.recomputeAffectedBuckets(events); err != nil {
		s.logger.Warn("failed to recompute event buckets",
			"session_id", sess.ID,
			"error", err,
		)
	}

	return nil
}

// recomputeAffectedBuckets rebuilds buckets for the time windows touched by the given events.
// It queries ALL events in those windows (not just this session's) and replaces the buckets.
func (s *Service) recomputeAffectedBuckets(events []Event) error {
	if len(events) == 0 {
		return nil
	}

	// Find the time range covered by these events.
	var earliest, latest time.Time
	for _, e := range events {
		if e.OccurredAt.IsZero() {
			continue
		}
		if earliest.IsZero() || e.OccurredAt.Before(earliest) {
			earliest = e.OccurredAt
		}
		if e.OccurredAt.After(latest) {
			latest = e.OccurredAt
		}
	}
	if earliest.IsZero() {
		return nil
	}

	// Recompute for both granularities.
	for _, gran := range []string{"1h", "1d"} {
		since := truncateTime(earliest, gran)
		until := advanceTime(truncateTime(latest, gran), gran)

		// Query ALL events in this time window (from all sessions).
		allEvents, err := s.store.QueryEvents(EventQuery{
			Since: since,
			Until: until,
		})
		if err != nil {
			return fmt.Errorf("query events for recompute (%s): %w", gran, err)
		}

		// Aggregate into fresh buckets.
		freshBuckets := s.aggregator.Aggregate(allEvents, gran)

		// Count distinct sessions per bucket.
		for i := range freshBuckets {
			freshBuckets[i].SessionCount = countDistinctSessions(allEvents, freshBuckets[i].BucketStart, freshBuckets[i].BucketEnd)
		}

		// Delete old buckets in this range and insert fresh ones.
		if err := s.store.ReplaceEventBuckets(freshBuckets); err != nil {
			return fmt.Errorf("replace buckets (%s): %w", gran, err)
		}
	}

	return nil
}

// countDistinctSessions counts unique session IDs for events within a time window.
func countDistinctSessions(events []Event, start, end time.Time) int {
	seen := make(map[session.ID]bool)
	for _, e := range events {
		if !e.OccurredAt.Before(start) && e.OccurredAt.Before(end) {
			seen[e.SessionID] = true
		}
	}
	return len(seen)
}

// ── Micro view: single session ──

// GetSessionEvents returns all events for a given session, ordered chronologically.
func (s *Service) GetSessionEvents(sessionID session.ID) ([]Event, error) {
	return s.store.GetSessionEvents(sessionID)
}

// GetSessionSummary returns a computed summary of events for a single session.
// This is the micro view — everything about one session at a glance.
func (s *Service) GetSessionSummary(sessionID session.ID) (*SessionEventSummary, error) {
	events, err := s.store.GetSessionEvents(sessionID)
	if err != nil {
		return nil, fmt.Errorf("sessionevent: get events: %w", err)
	}

	summary := NewSessionEventSummary(sessionID, events)
	return &summary, nil
}

// GetSessionEventsByType returns events for a session filtered by event type.
func (s *Service) GetSessionEventsByType(sessionID session.ID, eventType EventType) ([]Event, error) {
	return s.store.QueryEvents(EventQuery{
		SessionID: sessionID,
		Type:      eventType,
	})
}

// ── Macro view: project-wide buckets ──

// QueryBuckets returns aggregated event buckets for a project over a time range.
// This is the macro view — hourly or daily aggregations across all sessions.
func (s *Service) QueryBuckets(query BucketQuery) ([]EventBucket, error) {
	return s.store.QueryEventBuckets(query)
}

// ── Re-process (backfill) ──

// ReprocessSession re-extracts events for a session that was already captured.
// Useful for backfilling after code changes to the processor.
func (s *Service) ReprocessSession(sess *session.Session) error {
	return s.ProcessSession(sess) // ProcessSession is already idempotent
}

// RecomputeAllBuckets deletes all event buckets and recomputes them from scratch
// using the events stored in session_events. This fixes any staleness from
// deleted sessions (GC) or any accumulated inaccuracies.
func (s *Service) RecomputeAllBuckets() error {
	s.logger.Info("recomputing all event buckets from events table")

	// 1. Delete all existing buckets.
	if err := s.store.DeleteEventBuckets(BucketQuery{Granularity: "1h"}); err != nil {
		return fmt.Errorf("delete hourly buckets: %w", err)
	}
	if err := s.store.DeleteEventBuckets(BucketQuery{Granularity: "1d"}); err != nil {
		return fmt.Errorf("delete daily buckets: %w", err)
	}

	// 2. Query ALL events.
	allEvents, err := s.store.QueryEvents(EventQuery{})
	if err != nil {
		return fmt.Errorf("query all events: %w", err)
	}

	if len(allEvents) == 0 {
		s.logger.Info("no events found, buckets cleared")
		return nil
	}

	// 3. Aggregate into fresh buckets.
	for _, gran := range []string{"1h", "1d"} {
		buckets := s.aggregator.Aggregate(allEvents, gran)
		// Count distinct sessions per bucket.
		for i := range buckets {
			buckets[i].SessionCount = countDistinctSessions(allEvents, buckets[i].BucketStart, buckets[i].BucketEnd)
		}
		if err := s.store.ReplaceEventBuckets(buckets); err != nil {
			return fmt.Errorf("replace buckets (%s): %w", gran, err)
		}
	}

	s.logger.Info("recomputed all event buckets",
		"total_events", len(allEvents),
	)
	return nil
}
