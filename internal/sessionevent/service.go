package sessionevent

import (
	"fmt"
	"log/slog"

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

	// 4. Aggregate into buckets and batch-upsert.
	// Combine hourly + daily into a single batch for fewer transactions.
	hourlyBuckets := s.aggregator.AggregateForSession(sess, events, "1h")
	dailyBuckets := s.aggregator.AggregateForSession(sess, events, "1d")
	allBuckets := append(hourlyBuckets, dailyBuckets...)

	if err := s.store.UpsertEventBuckets(allBuckets); err != nil {
		s.logger.Warn("failed to upsert event buckets",
			"session_id", sess.ID,
			"bucket_count", len(allBuckets),
			"error", err,
		)
	}

	return nil
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
