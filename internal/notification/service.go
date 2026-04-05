package notification

import (
	"fmt"
	"log"
	"sync"
)

// ── NotificationService ──

// ServiceConfig holds all dependencies for creating a NotificationService.
type ServiceConfig struct {
	// Channels is the list of registered notification channels (Slack, webhook, etc.).
	// Each channel has its own Formatter.
	Channels []ChannelWithFormatter

	// Router determines recipients for each event.
	Router Router

	// Deduplicator suppresses duplicate alerts within a cooldown window.
	// Optional — if nil, no dedup is applied.
	Deduplicator *Deduplicator

	// Logger for dispatch diagnostics.
	Logger *log.Logger
}

// ChannelWithFormatter pairs a Channel adapter with its Formatter.
// Each adapter knows how to both format and deliver messages.
type ChannelWithFormatter struct {
	Channel   Channel
	Formatter Formatter
}

// Service orchestrates notification dispatch.
// It is the single entry point for all notification events:
//
//  1. Deduplicator.ShouldSend(event) — suppress if duplicate (optional)
//  2. Router.Route(event) → list of recipients
//  3. For each channel: Formatter.Format(event) → RenderedMessage
//  4. Channel.Send(recipient, message) for each recipient
//
// Dispatch is fire-and-forget — errors are logged but never block the caller.
// The service is safe for concurrent use.
type Service struct {
	channels []ChannelWithFormatter
	router   Router
	dedup    *Deduplicator
	logger   *log.Logger
}

// NewService creates a NotificationService.
// Returns nil if no channels are configured (nil-safe: Notify() is a no-op).
func NewService(cfg ServiceConfig) *Service {
	if len(cfg.Channels) == 0 || cfg.Router == nil {
		return nil
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Service{
		channels: cfg.Channels,
		router:   cfg.Router,
		dedup:    cfg.Deduplicator,
		logger:   logger,
	}
}

// Notify dispatches an event to all appropriate recipients across all channels.
// This method never blocks — each delivery runs in its own goroutine.
// It is nil-safe: calling Notify on a nil *Service is a no-op.
// Duplicate alerts (same event type + project within cooldown) are suppressed.
func (s *Service) Notify(event Event) {
	if s == nil {
		return
	}

	// Dedup check — suppress duplicate alerts within cooldown window.
	if s.dedup != nil && !s.dedup.ShouldSend(event) {
		s.logger.Printf("[notification] suppressed duplicate %s for %s",
			event.Type, dedupScope(event))
		return
	}

	recipients := s.router.Route(event)
	if len(recipients) == 0 {
		return
	}

	var wg sync.WaitGroup

	for _, cwf := range s.channels {
		msg, err := cwf.Formatter.Format(event)
		if err != nil {
			s.logger.Printf("[notification] %s format error for %s: %v",
				cwf.Channel.Name(), event.Type, err)
			continue
		}

		for _, r := range recipients {
			wg.Add(1)
			go func(ch Channel, recipient Recipient, message RenderedMessage) {
				defer wg.Done()
				if sendErr := ch.Send(recipient, message); sendErr != nil {
					s.logger.Printf("[notification] %s send error to %s (%s): %v",
						ch.Name(), recipient.Target, event.Type, sendErr)
				}
			}(cwf.Channel, r, msg)
		}
	}

	// Don't block the caller — goroutines will finish asynchronously.
	// We use a WaitGroup only so tests can verify delivery.
	// In production, this returns immediately.
}

// NotifySync dispatches an event synchronously — waits for all deliveries to complete.
// Useful for testing and for scheduler tasks that want confirmation before continuing.
// Returns the first error encountered, or nil if all deliveries succeeded.
// Duplicate alerts (same event type + project within cooldown) are suppressed.
func (s *Service) NotifySync(event Event) error {
	if s == nil {
		return nil
	}

	// Dedup check — suppress duplicate alerts within cooldown window.
	if s.dedup != nil && !s.dedup.ShouldSend(event) {
		s.logger.Printf("[notification] suppressed duplicate %s for %s",
			event.Type, dedupScope(event))
		return nil
	}

	recipients := s.router.Route(event)
	if len(recipients) == 0 {
		return nil
	}

	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)

	for _, cwf := range s.channels {
		msg, err := cwf.Formatter.Format(event)
		if err != nil {
			return fmt.Errorf("%s format error: %w", cwf.Channel.Name(), err)
		}

		for _, r := range recipients {
			wg.Add(1)
			go func(ch Channel, recipient Recipient, message RenderedMessage) {
				defer wg.Done()
				if sendErr := ch.Send(recipient, message); sendErr != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("%s send to %s: %w", ch.Name(), recipient.Target, sendErr)
					}
					mu.Unlock()
				}
			}(cwf.Channel, r, msg)
		}
	}

	wg.Wait()
	return firstErr
}
