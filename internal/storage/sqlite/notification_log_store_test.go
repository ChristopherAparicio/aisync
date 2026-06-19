package sqlite

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── InsertNotificationLog + GetNotificationLog ──

func TestInsertNotificationLog_InsertAndGet(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().UTC().Truncate(time.Millisecond)
	entry := &session.NotificationLogEntry{
		EventType:    "stall_spike",
		Severity:     "critical",
		Project:      "aisync",
		Title:        "Stall spike detected",
		Summary:      "5 live stalls",
		PayloadJSON:  `{"live":5}`,
		DispatchedAt: now,
		DedupKey:     "stall_spike:aisync:2026-05-26",
	}

	if err := store.InsertNotificationLog(entry); err != nil {
		t.Fatalf("InsertNotificationLog() error = %v", err)
	}
	if entry.ID == 0 {
		t.Fatal("InsertNotificationLog: expected ID to be set, got 0")
	}

	got, err := store.GetNotificationLog(entry.ID)
	if err != nil {
		t.Fatalf("GetNotificationLog() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetNotificationLog() = nil, want entry")
	}
	if got.EventType != "stall_spike" {
		t.Errorf("EventType = %q, want %q", got.EventType, "stall_spike")
	}
	if got.Severity != "critical" {
		t.Errorf("Severity = %q, want %q", got.Severity, "critical")
	}
	if got.Project != "aisync" {
		t.Errorf("Project = %q, want %q", got.Project, "aisync")
	}
	if got.Title != "Stall spike detected" {
		t.Errorf("Title = %q, want %q", got.Title, "Stall spike detected")
	}
	if got.Summary != "5 live stalls" {
		t.Errorf("Summary = %q, want %q", got.Summary, "5 live stalls")
	}
	if got.PayloadJSON != `{"live":5}` {
		t.Errorf("PayloadJSON = %q, want %q", got.PayloadJSON, `{"live":5}`)
	}
	if got.DedupKey != "stall_spike:aisync:2026-05-26" {
		t.Errorf("DedupKey = %q, want %q", got.DedupKey, "stall_spike:aisync:2026-05-26")
	}
	if got.DispatchedAt.UnixMilli() != now.UnixMilli() {
		t.Errorf("DispatchedAt = %v, want %v", got.DispatchedAt, now)
	}
	if got.IsAcknowledged() {
		t.Error("IsAcknowledged() = true, want false (no ack set)")
	}
}

func TestInsertNotificationLog_DefaultDispatchedAt(t *testing.T) {
	store := mustOpenStore(t)

	before := time.Now().UTC().Add(-time.Second)
	entry := &session.NotificationLogEntry{
		EventType: "error_spike",
		Title:     "Errors increasing",
	}
	if err := store.InsertNotificationLog(entry); err != nil {
		t.Fatalf("InsertNotificationLog() error = %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	got, err := store.GetNotificationLog(entry.ID)
	if err != nil {
		t.Fatalf("GetNotificationLog() error = %v", err)
	}
	if got.DispatchedAt.Before(before) || got.DispatchedAt.After(after) {
		t.Errorf("DispatchedAt = %v, want between %v and %v", got.DispatchedAt, before, after)
	}
	if got.Severity != "info" {
		t.Errorf("Severity default = %q, want %q", got.Severity, "info")
	}
}

func TestInsertNotificationLog_NilEntry(t *testing.T) {
	store := mustOpenStore(t)
	if err := store.InsertNotificationLog(nil); err == nil {
		t.Error("InsertNotificationLog(nil) error = nil, want non-nil")
	}
}

func TestInsertNotificationLog_EmptyEventType(t *testing.T) {
	store := mustOpenStore(t)
	if err := store.InsertNotificationLog(&session.NotificationLogEntry{}); err == nil {
		t.Error("InsertNotificationLog(empty event_type) error = nil, want non-nil")
	}
}

func TestGetNotificationLog_NotFound(t *testing.T) {
	store := mustOpenStore(t)
	got, err := store.GetNotificationLog(99999)
	if err != nil {
		t.Fatalf("GetNotificationLog() error = %v", err)
	}
	if got != nil {
		t.Errorf("GetNotificationLog(99999) = %v, want nil", got)
	}
}

// ── ListNotificationLogs ──

func seedNotifications(t *testing.T, store *Store) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	entries := []*session.NotificationLogEntry{
		{EventType: "stall_spike", Severity: "critical", Project: "aisync", Title: "A", DispatchedAt: now.Add(-3 * time.Hour)},
		{EventType: "stall_spike", Severity: "warning", Project: "omogen", Title: "B", DispatchedAt: now.Add(-2 * time.Hour)},
		{EventType: "error_spike", Severity: "warning", Project: "aisync", Title: "C", DispatchedAt: now.Add(-time.Hour)},
		{EventType: "budget_alert", Severity: "info", Project: "aisync", Title: "D", DispatchedAt: now},
	}
	for _, e := range entries {
		if err := store.InsertNotificationLog(e); err != nil {
			t.Fatalf("InsertNotificationLog(%s) error = %v", e.Title, err)
		}
	}
}

func TestListNotificationLogs_All(t *testing.T) {
	store := mustOpenStore(t)
	seedNotifications(t, store)

	got, err := store.ListNotificationLogs(session.NotificationLogFilter{})
	if err != nil {
		t.Fatalf("ListNotificationLogs() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	// Ordered by dispatched_at DESC.
	if got[0].Title != "D" {
		t.Errorf("got[0].Title = %q, want D (most recent)", got[0].Title)
	}
	if got[3].Title != "A" {
		t.Errorf("got[3].Title = %q, want A (oldest)", got[3].Title)
	}
}

func TestListNotificationLogs_FilterByEventType(t *testing.T) {
	store := mustOpenStore(t)
	seedNotifications(t, store)

	got, err := store.ListNotificationLogs(session.NotificationLogFilter{
		EventTypes: []string{"stall_spike"},
	})
	if err != nil {
		t.Fatalf("ListNotificationLogs() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, e := range got {
		if e.EventType != "stall_spike" {
			t.Errorf("EventType = %q, want stall_spike", e.EventType)
		}
	}
}

func TestListNotificationLogs_FilterBySeverity(t *testing.T) {
	store := mustOpenStore(t)
	seedNotifications(t, store)

	got, err := store.ListNotificationLogs(session.NotificationLogFilter{
		Severities: []string{"critical", "warning"},
	})
	if err != nil {
		t.Fatalf("ListNotificationLogs() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
}

func TestListNotificationLogs_FilterByProject(t *testing.T) {
	store := mustOpenStore(t)
	seedNotifications(t, store)

	got, err := store.ListNotificationLogs(session.NotificationLogFilter{
		Projects: []string{"aisync"},
	})
	if err != nil {
		t.Fatalf("ListNotificationLogs() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for _, e := range got {
		if e.Project != "aisync" {
			t.Errorf("Project = %q, want aisync", e.Project)
		}
	}
}

func TestListNotificationLogs_OnlyUnack(t *testing.T) {
	store := mustOpenStore(t)
	seedNotifications(t, store)

	// Ack the most recent.
	all, _ := store.ListNotificationLogs(session.NotificationLogFilter{})
	if err := store.AcknowledgeNotification(all[0].ID, "tester", time.Time{}); err != nil {
		t.Fatalf("AcknowledgeNotification() error = %v", err)
	}

	got, err := store.ListNotificationLogs(session.NotificationLogFilter{OnlyUnack: true})
	if err != nil {
		t.Fatalf("ListNotificationLogs() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (one acked)", len(got))
	}
}

func TestListNotificationLogs_SinceUntil(t *testing.T) {
	store := mustOpenStore(t)
	seedNotifications(t, store)

	now := time.Now().UTC()
	got, err := store.ListNotificationLogs(session.NotificationLogFilter{
		Since: now.Add(-90 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ListNotificationLogs() error = %v", err)
	}
	// Only C (-1h) and D (~now) qualify.
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (Since filter)", len(got))
	}

	got, err = store.ListNotificationLogs(session.NotificationLogFilter{
		Until: now.Add(-90 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ListNotificationLogs(Until) error = %v", err)
	}
	// Only A (-3h) and B (-2h) qualify.
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (Until filter)", len(got))
	}
}

func TestListNotificationLogs_LimitOffset(t *testing.T) {
	store := mustOpenStore(t)
	seedNotifications(t, store)

	got, err := store.ListNotificationLogs(session.NotificationLogFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListNotificationLogs(Limit) error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (limit)", len(got))
	}
	if got[0].Title != "D" || got[1].Title != "C" {
		t.Errorf("Limit results = [%s,%s], want [D,C]", got[0].Title, got[1].Title)
	}

	got, err = store.ListNotificationLogs(session.NotificationLogFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListNotificationLogs(Offset) error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (offset)", len(got))
	}
	if got[0].Title != "B" || got[1].Title != "A" {
		t.Errorf("Offset results = [%s,%s], want [B,A]", got[0].Title, got[1].Title)
	}
}

// ── AcknowledgeNotification ──

func TestAcknowledgeNotification_SetsAckFields(t *testing.T) {
	store := mustOpenStore(t)

	entry := &session.NotificationLogEntry{
		EventType: "stall_spike",
		Title:     "Test",
	}
	if err := store.InsertNotificationLog(entry); err != nil {
		t.Fatalf("InsertNotificationLog() error = %v", err)
	}

	ackTime := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.AcknowledgeNotification(entry.ID, "cli", ackTime); err != nil {
		t.Fatalf("AcknowledgeNotification() error = %v", err)
	}

	got, err := store.GetNotificationLog(entry.ID)
	if err != nil {
		t.Fatalf("GetNotificationLog() error = %v", err)
	}
	if !got.IsAcknowledged() {
		t.Fatal("IsAcknowledged() = false, want true")
	}
	if got.AcknowledgedBy != "cli" {
		t.Errorf("AcknowledgedBy = %q, want cli", got.AcknowledgedBy)
	}
	if got.AcknowledgedAt == nil || got.AcknowledgedAt.UnixMilli() != ackTime.UnixMilli() {
		t.Errorf("AcknowledgedAt = %v, want %v", got.AcknowledgedAt, ackTime)
	}
}

func TestAcknowledgeNotification_IdempotentNoOp(t *testing.T) {
	store := mustOpenStore(t)

	entry := &session.NotificationLogEntry{EventType: "stall_spike", Title: "T"}
	if err := store.InsertNotificationLog(entry); err != nil {
		t.Fatalf("InsertNotificationLog() error = %v", err)
	}

	firstAck := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	if err := store.AcknowledgeNotification(entry.ID, "cli", firstAck); err != nil {
		t.Fatalf("first AcknowledgeNotification() error = %v", err)
	}

	// Second ack must NOT overwrite (UPDATE has WHERE acknowledged_at IS NULL).
	if err := store.AcknowledgeNotification(entry.ID, "web", time.Now().UTC()); err != nil {
		t.Fatalf("second AcknowledgeNotification() error = %v", err)
	}

	got, err := store.GetNotificationLog(entry.ID)
	if err != nil {
		t.Fatalf("GetNotificationLog() error = %v", err)
	}
	if got.AcknowledgedBy != "cli" {
		t.Errorf("AcknowledgedBy = %q, want cli (no overwrite)", got.AcknowledgedBy)
	}
	if got.AcknowledgedAt == nil || got.AcknowledgedAt.UnixMilli() != firstAck.UnixMilli() {
		t.Errorf("AcknowledgedAt = %v, want %v (no overwrite)", got.AcknowledgedAt, firstAck)
	}
}

func TestAcknowledgeNotification_InvalidID(t *testing.T) {
	store := mustOpenStore(t)
	if err := store.AcknowledgeNotification(0, "cli", time.Time{}); err == nil {
		t.Error("AcknowledgeNotification(0) error = nil, want non-nil")
	}
}

// ── AcknowledgeAllNotifications ──

func TestAcknowledgeAllNotifications_AcksAllUnacked(t *testing.T) {
	store := mustOpenStore(t)
	seedNotifications(t, store)

	// Pre-ack one.
	all, _ := store.ListNotificationLogs(session.NotificationLogFilter{})
	if err := store.AcknowledgeNotification(all[0].ID, "cli", time.Time{}); err != nil {
		t.Fatalf("AcknowledgeNotification() error = %v", err)
	}

	n, err := store.AcknowledgeAllNotifications("web", time.Time{})
	if err != nil {
		t.Fatalf("AcknowledgeAllNotifications() error = %v", err)
	}
	if n != 3 {
		t.Errorf("AcknowledgeAllNotifications() = %d, want 3 (4 total - 1 already ack)", n)
	}

	unack, err := store.UnacknowledgedNotificationCount()
	if err != nil {
		t.Fatalf("UnacknowledgedNotificationCount() error = %v", err)
	}
	if unack != 0 {
		t.Errorf("UnacknowledgedNotificationCount() = %d, want 0", unack)
	}
}

func TestAcknowledgeAllNotifications_Empty(t *testing.T) {
	store := mustOpenStore(t)
	n, err := store.AcknowledgeAllNotifications("cli", time.Time{})
	if err != nil {
		t.Fatalf("AcknowledgeAllNotifications() error = %v", err)
	}
	if n != 0 {
		t.Errorf("AcknowledgeAllNotifications() = %d, want 0 (empty table)", n)
	}
}

// ── UnacknowledgedNotificationCount ──

func TestUnacknowledgedNotificationCount(t *testing.T) {
	store := mustOpenStore(t)

	n, err := store.UnacknowledgedNotificationCount()
	if err != nil {
		t.Fatalf("UnacknowledgedNotificationCount() error = %v", err)
	}
	if n != 0 {
		t.Errorf("initial count = %d, want 0", n)
	}

	seedNotifications(t, store)

	n, err = store.UnacknowledgedNotificationCount()
	if err != nil {
		t.Fatalf("UnacknowledgedNotificationCount() error = %v", err)
	}
	if n != 4 {
		t.Errorf("count after seed = %d, want 4", n)
	}

	// Ack one.
	all, _ := store.ListNotificationLogs(session.NotificationLogFilter{})
	_ = store.AcknowledgeNotification(all[0].ID, "cli", time.Time{})

	n, err = store.UnacknowledgedNotificationCount()
	if err != nil {
		t.Fatalf("UnacknowledgedNotificationCount() error = %v", err)
	}
	if n != 3 {
		t.Errorf("count after one ack = %d, want 3", n)
	}
}
