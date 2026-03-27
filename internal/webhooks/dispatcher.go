// Package webhooks implements outbound webhook notifications for aisync events.
// When configured, it fires HTTP POST requests to registered URLs whenever
// sessions are captured, analyzed, or skills are missed.
//
// Dispatching is fire-and-forget with best-effort retry (configurable).
// Failures are logged but never block the main operation.
package webhooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// EventType identifies what happened.
type EventType string

const (
	EventSessionCaptured EventType = "session.captured"
	EventSessionAnalyzed EventType = "session.analyzed"
	EventSessionTagged   EventType = "session.tagged"
	EventSkillMissed     EventType = "skill.missed"
	EventBudgetAlert     EventType = "budget.alert"
)

// Event is the payload sent to webhook endpoints.
type Event struct {
	Type      EventType `json:"type"`
	Timestamp string    `json:"timestamp"`
	Data      any       `json:"data"`
}

// HookConfig describes a single webhook registration.
type HookConfig struct {
	URL    string      `json:"url"`              // target URL for HTTP POST
	Events []EventType `json:"events,omitempty"` // filter: only fire for these events (empty = all)
	Secret string      `json:"secret,omitempty"` // optional shared secret for HMAC signature (future)
}

// Config holds all dispatcher settings.
type Config struct {
	Hooks      []HookConfig
	Logger     *log.Logger
	Timeout    time.Duration // per-request timeout (default: 10s)
	MaxRetries int           // retry count on failure (default: 1)
}

// Dispatcher manages outbound webhook delivery.
type Dispatcher struct {
	hooks      []HookConfig
	client     *http.Client
	logger     *log.Logger
	maxRetries int
}

// New creates a Dispatcher. Returns nil if no hooks are configured.
func New(cfg Config) *Dispatcher {
	if len(cfg.Hooks) == 0 {
		return nil
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}

	return &Dispatcher{
		hooks:      cfg.Hooks,
		client:     &http.Client{Timeout: timeout},
		logger:     logger,
		maxRetries: maxRetries,
	}
}

// Fire sends an event to all matching webhooks asynchronously.
// This method never blocks — each delivery runs in its own goroutine.
func (d *Dispatcher) Fire(eventType EventType, data any) {
	if d == nil {
		return
	}

	event := Event{
		Type:      eventType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      data,
	}

	for _, hook := range d.hooks {
		if !hook.matches(eventType) {
			continue
		}
		go d.deliver(hook, event)
	}
}

// deliver sends a single event to a single webhook with retries.
func (d *Dispatcher) deliver(hook HookConfig, event Event) {
	body, err := json.Marshal(event)
	if err != nil {
		d.logger.Printf("[webhook] marshal error for %s: %v", event.Type, err)
		return
	}

	var lastErr error
	for attempt := 0; attempt <= d.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second) // linear backoff
		}

		req, reqErr := http.NewRequest(http.MethodPost, hook.URL, bytes.NewReader(body))
		if reqErr != nil {
			d.logger.Printf("[webhook] request error: %v", reqErr)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "aisync-webhook/1.0")
		req.Header.Set("X-AiSync-Event", string(event.Type))

		resp, doErr := d.client.Do(req)
		if doErr != nil {
			lastErr = doErr
			continue
		}
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return // success
		}
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	d.logger.Printf("[webhook] delivery failed for %s to %s after %d attempts: %v",
		event.Type, hook.URL, d.maxRetries+1, lastErr)
}

// matches returns true if the hook should receive this event type.
func (h HookConfig) matches(eventType EventType) bool {
	if len(h.Events) == 0 {
		return true // no filter = all events
	}
	for _, e := range h.Events {
		if e == eventType || strings.HasPrefix(string(eventType), string(e)) {
			return true
		}
	}
	return false
}
