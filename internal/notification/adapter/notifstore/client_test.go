package notifstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// stubStore is a minimal in-memory storage.NotificationLogStore for tests.
type stubStore struct {
	inserted []*session.NotificationLogEntry
	insertErr error
}

func (s *stubStore) InsertNotificationLog(entry *session.NotificationLogEntry) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	entry.ID = int64(len(s.inserted) + 1)
	s.inserted = append(s.inserted, entry)
	return nil
}

func (s *stubStore) GetNotificationLog(id int64) (*session.NotificationLogEntry, error) {
	for _, e := range s.inserted {
		if e.ID == id {
			return e, nil
		}
	}
	return nil, nil
}

func (s *stubStore) ListNotificationLogs(_ session.NotificationLogFilter) ([]session.NotificationLogEntry, error) {
	out := make([]session.NotificationLogEntry, 0, len(s.inserted))
	for _, e := range s.inserted {
		out = append(out, *e)
	}
	return out, nil
}

func (s *stubStore) AcknowledgeNotification(_ int64, _ string, _ time.Time) error { return nil }
func (s *stubStore) AcknowledgeAllNotifications(_ string, _ time.Time) (int, error) {
	return 0, nil
}
func (s *stubStore) UnacknowledgedNotificationCount() (int, error) { return 0, nil }

// ── Formatter ──

func TestFormatter_StallSpike(t *testing.T) {
	f := NewFormatter()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	event := notification.Event{
		Type:      notification.EventStallSpike,
		Severity:  notification.SeverityCritical,
		Project:   "aisync",
		Timestamp: now,
		Data: notification.StallSpikeData{
			LiveCount:    5,
			NewStalls24h: 12,
			CostLost24h:  1.50,
			TopRootCause: "provider_429",
		},
	}

	msg, err := f.Format(event)
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	if !strings.Contains(msg.FallbackText, "Stall spike") {
		t.Errorf("FallbackText = %q, want contains 'Stall spike'", msg.FallbackText)
	}

	var env envelope
	if err := json.Unmarshal(msg.Body, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.EventType != string(notification.EventStallSpike) {
		t.Errorf("EventType = %q, want %q", env.EventType, notification.EventStallSpike)
	}
	if env.Severity != string(notification.SeverityCritical) {
		t.Errorf("Severity = %q, want %q", env.Severity, notification.SeverityCritical)
	}
	if env.Project != "aisync" {
		t.Errorf("Project = %q, want aisync", env.Project)
	}
	if !strings.Contains(env.Title, "5 live") {
		t.Errorf("Title = %q, want contains '5 live'", env.Title)
	}
	if !strings.Contains(env.Summary, "12 new") {
		t.Errorf("Summary = %q, want contains '12 new'", env.Summary)
	}
	if !strings.Contains(env.Summary, "$1.50") {
		t.Errorf("Summary = %q, want contains '$1.50'", env.Summary)
	}
	if !strings.Contains(env.Summary, "provider_429") {
		t.Errorf("Summary = %q, want contains 'provider_429'", env.Summary)
	}
	wantDedup := "stall.spike:aisync:2026-05-26"
	if env.DedupKey != wantDedup {
		t.Errorf("DedupKey = %q, want %q", env.DedupKey, wantDedup)
	}
}

func TestFormatter_ErrorSpike(t *testing.T) {
	f := NewFormatter()
	event := notification.Event{
		Type:      notification.EventErrorSpike,
		Severity:  notification.SeverityWarning,
		Project:   "omogen",
		Timestamp: time.Now().UTC(),
		Data: notification.ErrorSpikeData{
			ErrorCount:    25,
			WindowMinutes: 15,
			Sessions:      []string{"s1", "s2", "s3"},
		},
	}
	msg, err := f.Format(event)
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	var env envelope
	_ = json.Unmarshal(msg.Body, &env)
	if !strings.Contains(env.Title, "25 errors") {
		t.Errorf("Title = %q, want contains '25 errors'", env.Title)
	}
	if !strings.Contains(env.Summary, "3 sessions") {
		t.Errorf("Summary = %q, want contains '3 sessions'", env.Summary)
	}
}

func TestFormatter_BudgetAlert(t *testing.T) {
	f := NewFormatter()
	event := notification.Event{
		Type:      notification.EventBudgetAlert,
		Severity:  notification.SeverityWarning,
		Project:   "aisync",
		Timestamp: time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC),
		Data: notification.BudgetAlertData{
			AlertType:     "monthly",
			AlertLevel:    "warning",
			Spent:         85.00,
			Limit:         100.00,
			Percent:       85,
			Projected:     120.00,
			DaysRemaining: 5,
		},
	}
	msg, err := f.Format(event)
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	var env envelope
	_ = json.Unmarshal(msg.Body, &env)
	if !strings.Contains(env.Title, "$85.00") || !strings.Contains(env.Title, "$100.00") {
		t.Errorf("Title = %q, want contains both spent and limit", env.Title)
	}
	wantDedup := "budget.alert:aisync:monthly:warning"
	if env.DedupKey != wantDedup {
		t.Errorf("DedupKey = %q, want %q", env.DedupKey, wantDedup)
	}
}

func TestFormatter_DefaultSeverity(t *testing.T) {
	f := NewFormatter()
	event := notification.Event{
		Type:      notification.EventStallSpike,
		Timestamp: time.Now().UTC(),
		Data:      notification.StallSpikeData{LiveCount: 1},
	}
	msg, _ := f.Format(event)
	var env envelope
	_ = json.Unmarshal(msg.Body, &env)
	if env.Severity != "info" {
		t.Errorf("Severity = %q, want info (default)", env.Severity)
	}
}

func TestFormatter_DefaultDispatchedAt(t *testing.T) {
	f := NewFormatter()
	before := time.Now().UTC().Add(-time.Second)
	event := notification.Event{
		Type: notification.EventStallSpike,
		Data: notification.StallSpikeData{LiveCount: 1},
	}
	msg, _ := f.Format(event)
	after := time.Now().UTC().Add(time.Second)

	var env envelope
	_ = json.Unmarshal(msg.Body, &env)
	if env.DispatchedAt.Before(before) || env.DispatchedAt.After(after) {
		t.Errorf("DispatchedAt = %v, want between %v and %v", env.DispatchedAt, before, after)
	}
}

func TestFormatter_GlobalScopeFallback(t *testing.T) {
	f := NewFormatter()
	event := notification.Event{
		Type:      notification.EventStallSpike,
		Timestamp: time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC),
		Data:      notification.StallSpikeData{LiveCount: 1},
	}
	msg, _ := f.Format(event)
	var env envelope
	_ = json.Unmarshal(msg.Body, &env)
	if !strings.Contains(env.DedupKey, ":global:") {
		t.Errorf("DedupKey = %q, want contains ':global:'", env.DedupKey)
	}
}

func TestFormatter_UnknownEventType(t *testing.T) {
	f := NewFormatter()
	event := notification.Event{
		Type:      notification.EventType("custom.event"),
		Timestamp: time.Now().UTC(),
		Data:      map[string]any{"foo": "bar"},
	}
	msg, err := f.Format(event)
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	var env envelope
	_ = json.Unmarshal(msg.Body, &env)
	if env.Title != "custom.event" {
		t.Errorf("Title = %q, want 'custom.event' fallback", env.Title)
	}
}

// ── Client ──

func TestClient_NameAndSend(t *testing.T) {
	store := &stubStore{}
	c := NewClient(store)
	if c == nil {
		t.Fatal("NewClient() = nil, want non-nil")
	}
	if c.Name() != "notifstore" {
		t.Errorf("Name() = %q, want notifstore", c.Name())
	}

	f := NewFormatter()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	event := notification.Event{
		Type:      notification.EventStallSpike,
		Severity:  notification.SeverityCritical,
		Project:   "aisync",
		Timestamp: now,
		Data:      notification.StallSpikeData{LiveCount: 7, NewStalls24h: 30, CostLost24h: 2.34},
	}
	msg, err := f.Format(event)
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	if err := c.Send(notification.Recipient{}, msg); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("inserted = %d, want 1", len(store.inserted))
	}

	got := store.inserted[0]
	if got.EventType != string(notification.EventStallSpike) {
		t.Errorf("EventType = %q, want %q", got.EventType, notification.EventStallSpike)
	}
	if got.Severity != string(notification.SeverityCritical) {
		t.Errorf("Severity = %q, want %q", got.Severity, notification.SeverityCritical)
	}
	if got.Project != "aisync" {
		t.Errorf("Project = %q, want aisync", got.Project)
	}
	if !strings.Contains(got.Title, "Stall spike") {
		t.Errorf("Title = %q, want contains 'Stall spike'", got.Title)
	}
	if !strings.Contains(got.Summary, "7 live") {
		t.Errorf("Summary = %q, want contains '7 live'", got.Summary)
	}
	if got.PayloadJSON == "" {
		t.Error("PayloadJSON is empty, want non-empty")
	}
	if got.DedupKey != "stall.spike:aisync:2026-05-26" {
		t.Errorf("DedupKey = %q, want %q", got.DedupKey, "stall.spike:aisync:2026-05-26")
	}
	if got.DispatchedAt.UnixMilli() != now.UnixMilli() {
		t.Errorf("DispatchedAt = %v, want %v", got.DispatchedAt, now)
	}

	// PayloadJSON should be valid JSON.
	var payload map[string]any
	if err := json.Unmarshal([]byte(got.PayloadJSON), &payload); err != nil {
		t.Errorf("PayloadJSON unmarshal: %v", err)
	}
	if v, ok := payload["live_count"]; !ok || int(v.(float64)) != 7 {
		t.Errorf("payload.live_count = %v, want 7", v)
	}
}

func TestClient_SendInvalidBody(t *testing.T) {
	store := &stubStore{}
	c := NewClient(store)
	err := c.Send(notification.Recipient{}, notification.RenderedMessage{Body: []byte("not json")})
	if err == nil {
		t.Error("Send() with bad body error = nil, want non-nil")
	}
}

func TestClient_NilStoreReturnsNil(t *testing.T) {
	if NewClient(nil) != nil {
		t.Error("NewClient(nil) != nil, want nil")
	}
}

func TestClient_PropagatesStoreError(t *testing.T) {
	store := &stubStore{insertErr: errStoreBoom}
	c := NewClient(store)

	f := NewFormatter()
	event := notification.Event{
		Type:      notification.EventStallSpike,
		Timestamp: time.Now().UTC(),
		Data:      notification.StallSpikeData{LiveCount: 1},
	}
	msg, _ := f.Format(event)
	if err := c.Send(notification.Recipient{}, msg); err == nil {
		t.Error("Send() error = nil, want store error propagation")
	}
}

// errStoreBoom is a sentinel error used in TestClient_PropagatesStoreError.
var errStoreBoom = stubError("boom")

type stubError string

func (e stubError) Error() string { return string(e) }
