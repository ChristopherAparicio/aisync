// Package notifstore implements an always-on internal notification channel
// that persists every dispatched notification.Event into the NotificationLogStore.
//
// This adapter is the backbone of the in-app /alerts page and the
// `aisync alerts` CLI: every alert routed by the notification service is
// captured here, regardless of whether Slack or webhook adapters are
// configured. It runs unconditionally as long as a storage backend exists.
package notifstore

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// Client implements notification.Channel by persisting every event into
// storage.NotificationLogStore. It ignores the Recipient field — the channel
// is always-on and global.
type Client struct {
	store storage.NotificationLogStore
}

// NewClient creates a notification.Channel that persists into the given store.
// Returns nil when store is nil so callers can short-circuit registration.
func NewClient(store storage.NotificationLogStore) *Client {
	if store == nil {
		return nil
	}
	return &Client{store: store}
}

// Name returns the adapter identifier.
func (c *Client) Name() string { return "notifstore" }

// Send decodes the rendered envelope and persists it as a NotificationLogEntry.
// Recipient is ignored — every event lands in the shared in-app log.
func (c *Client) Send(_ notification.Recipient, msg notification.RenderedMessage) error {
	if c == nil {
		return nil
	}

	var env envelope
	if err := json.Unmarshal(msg.Body, &env); err != nil {
		return fmt.Errorf("notifstore: decode envelope: %w", err)
	}

	entry := &session.NotificationLogEntry{
		EventType:    env.EventType,
		Severity:     env.Severity,
		Project:      env.Project,
		Title:        env.Title,
		Summary:      env.Summary,
		PayloadJSON:  string(env.Payload),
		DispatchedAt: env.DispatchedAt,
		DedupKey:     env.DedupKey,
	}
	if err := c.store.InsertNotificationLog(entry); err != nil {
		return fmt.Errorf("notifstore: persist: %w", err)
	}
	return nil
}

// Formatter renders notification events into the internal envelope expected by Client.
// Each EventType has a dedicated title/summary/dedup_key derivation so the in-app
// /alerts page can display compact, scannable rows.
type Formatter struct{}

// NewFormatter creates a notifstore formatter.
func NewFormatter() *Formatter { return &Formatter{} }

// Format renders an event into a JSON envelope.
func (f *Formatter) Format(event notification.Event) (notification.RenderedMessage, error) {
	title, summary := renderTitleAndSummary(event)
	dedup := deriveDedupKey(event)

	payload, err := json.Marshal(event.Data)
	if err != nil {
		return notification.RenderedMessage{}, fmt.Errorf("notifstore: marshal payload: %w", err)
	}

	dispatchedAt := event.Timestamp
	if dispatchedAt.IsZero() {
		dispatchedAt = time.Now().UTC()
	}
	severity := string(event.Severity)
	if severity == "" {
		severity = string(notification.SeverityInfo)
	}

	env := envelope{
		EventType:    string(event.Type),
		Severity:     severity,
		Project:      event.Project,
		Title:        title,
		Summary:      summary,
		DedupKey:     dedup,
		DispatchedAt: dispatchedAt,
		Payload:      payload,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return notification.RenderedMessage{}, fmt.Errorf("notifstore: marshal envelope: %w", err)
	}
	return notification.RenderedMessage{
		Body:         body,
		FallbackText: title,
	}, nil
}

// envelope is the internal serialized payload exchanged between Formatter and Client.
type envelope struct {
	EventType    string          `json:"event_type"`
	Severity     string          `json:"severity"`
	Project      string          `json:"project,omitempty"`
	Title        string          `json:"title"`
	Summary      string          `json:"summary"`
	DedupKey     string          `json:"dedup_key,omitempty"`
	DispatchedAt time.Time       `json:"dispatched_at"`
	Payload      json.RawMessage `json:"payload"`
}

// renderTitleAndSummary derives a compact human-readable title + summary
// from the event payload. Unknown event types fall back to the EventType.
func renderTitleAndSummary(event notification.Event) (string, string) {
	switch event.Type {
	case notification.EventStallSpike:
		d, ok := event.Data.(notification.StallSpikeData)
		if !ok {
			return "Stall spike", ""
		}
		title := fmt.Sprintf("Stall spike: %d live", d.LiveCount)
		summary := fmt.Sprintf("%d live · %d new 24h · $%.2f lost", d.LiveCount, d.NewStalls24h, d.CostLost24h)
		if d.TopRootCause != "" {
			summary += " · top: " + d.TopRootCause
		}
		return title, summary

	case notification.EventErrorSpike:
		d, ok := event.Data.(notification.ErrorSpikeData)
		if !ok {
			return "Error spike", ""
		}
		title := fmt.Sprintf("Error spike: %d errors", d.ErrorCount)
		summary := fmt.Sprintf("%d errors in %d min · %d sessions", d.ErrorCount, d.WindowMinutes, len(d.Sessions))
		return title, summary

	case notification.EventBudgetAlert:
		d, ok := event.Data.(notification.BudgetAlertData)
		if !ok {
			return "Budget alert", ""
		}
		title := fmt.Sprintf("Budget %s: $%.2f / $%.2f", d.AlertLevel, d.Spent, d.Limit)
		summary := fmt.Sprintf("%s · %.0f%% used · projected $%.2f · %d days left",
			d.AlertType, d.Percent, d.Projected, d.DaysRemaining)
		return title, summary

	case notification.EventSessionCaptured:
		d, ok := event.Data.(notification.SessionCapturedData)
		if !ok {
			return "Session captured", ""
		}
		title := fmt.Sprintf("Session captured: %s/%s", d.Provider, d.Branch)
		summary := d.Summary
		if summary == "" {
			summary = fmt.Sprintf("%d tokens · %s", d.Tokens, d.Agent)
		}
		return title, summary

	case notification.EventDailyDigest, notification.EventWeeklyReport, notification.EventPersonalDaily:
		// Digests carry a Period field; we use it as the title suffix.
		period := extractPeriod(event.Data)
		title := fmt.Sprintf("%s: %s", event.Type, period)
		summary := summarizeDigest(event.Data)
		return title, summary

	case notification.EventRecommendation:
		d, ok := event.Data.(notification.RecommendationData)
		if !ok {
			return "Recommendations", ""
		}
		title := fmt.Sprintf("Recommendations: %d new", d.TotalCount)
		summary := fmt.Sprintf("%d total · %d high-priority", d.TotalCount, d.HighCount)
		return title, summary
	}

	// Fallback: use the event type as title.
	return string(event.Type), ""
}

// deriveDedupKey produces a stable key per (event_type, scope, period) tuple.
// The notification.Deduplicator already suppresses in-memory; this key gives us
// a persistent fingerprint for future analytics and CLI/web filtering.
func deriveDedupKey(event notification.Event) string {
	day := event.Timestamp
	if day.IsZero() {
		day = time.Now().UTC()
	}
	stamp := day.UTC().Format("2006-01-02")
	scope := event.Project
	if scope == "" {
		scope = "global"
	}

	switch event.Type {
	case notification.EventBudgetAlert:
		if d, ok := event.Data.(notification.BudgetAlertData); ok {
			return fmt.Sprintf("%s:%s:%s:%s", event.Type, scope, d.AlertType, d.AlertLevel)
		}
	case notification.EventSessionCaptured:
		if d, ok := event.Data.(notification.SessionCapturedData); ok && d.SessionID != "" {
			return fmt.Sprintf("%s:%s", event.Type, d.SessionID)
		}
	case notification.EventDailyDigest, notification.EventWeeklyReport, notification.EventPersonalDaily:
		return fmt.Sprintf("%s:%s:%s", event.Type, scope, extractPeriod(event.Data))
	}
	return fmt.Sprintf("%s:%s:%s", event.Type, scope, stamp)
}

// extractPeriod pulls the Period string out of any digest payload.
func extractPeriod(data any) string {
	switch d := data.(type) {
	case notification.DigestData:
		return d.Period
	case notification.PersonalDigestData:
		return d.Period
	}
	return ""
}

// summarizeDigest produces a one-line summary for any digest payload.
func summarizeDigest(data any) string {
	switch d := data.(type) {
	case notification.DigestData:
		return fmt.Sprintf("%d sessions · %d tokens · $%.2f · %d errors",
			d.SessionCount, d.TotalTokens, d.TotalCost, d.ErrorCount)
	case notification.PersonalDigestData:
		return fmt.Sprintf("%s: %d sessions · $%.2f (team avg $%.2f)",
			d.OwnerName, d.SessionCount, d.TotalCost, d.TeamAvgCost)
	}
	return ""
}
