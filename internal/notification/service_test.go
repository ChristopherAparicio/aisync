package notification

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// ── Mock implementations ──

type mockChannel struct {
	name    string
	mu      sync.Mutex
	sent    []sentMessage
	sendErr error
}

type sentMessage struct {
	Recipient Recipient
	Message   RenderedMessage
}

func (m *mockChannel) Name() string { return m.name }
func (m *mockChannel) Send(r Recipient, msg RenderedMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return m.sendErr
	}
	m.sent = append(m.sent, sentMessage{r, msg})
	return nil
}

type mockFormatter struct {
	formatErr error
}

func (m *mockFormatter) Format(event Event) (RenderedMessage, error) {
	if m.formatErr != nil {
		return RenderedMessage{}, m.formatErr
	}
	return RenderedMessage{
		Body:         []byte(fmt.Sprintf(`{"event":"%s"}`, event.Type)),
		FallbackText: string(event.Type),
	}, nil
}

type staticRouter struct {
	recipients []Recipient
}

func (r *staticRouter) Route(_ Event) []Recipient { return r.recipients }

// ── Tests ──

func TestNewService_NilWhenNoChannels(t *testing.T) {
	svc := NewService(ServiceConfig{
		Channels: nil,
		Router:   &staticRouter{},
	})
	if svc != nil {
		t.Error("expected nil service when no channels")
	}
}

func TestNewService_NilWhenNoRouter(t *testing.T) {
	ch := &mockChannel{name: "test"}
	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{{Channel: ch, Formatter: &mockFormatter{}}},
		Router:   nil,
	})
	if svc != nil {
		t.Error("expected nil service when no router")
	}
}

func TestService_Notify_NilSafe(t *testing.T) {
	var svc *Service
	svc.Notify(Event{Type: EventBudgetAlert}) // should not panic
}

func TestService_NotifySync_DeliveryToOneChannel(t *testing.T) {
	ch := &mockChannel{name: "test"}
	router := &staticRouter{
		recipients: []Recipient{{Type: RecipientChannel, Target: "#general"}},
	}

	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{
			{Channel: ch, Formatter: &mockFormatter{}},
		},
		Router: router,
	})

	err := svc.NotifySync(Event{
		Type:      EventBudgetAlert,
		Timestamp: time.Now(),
		Project:   "org/repo",
	})
	if err != nil {
		t.Fatalf("NotifySync() error = %v", err)
	}

	if len(ch.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(ch.sent))
	}
	if ch.sent[0].Recipient.Target != "#general" {
		t.Errorf("target = %q, want #general", ch.sent[0].Recipient.Target)
	}
}

func TestService_NotifySync_MultipleRecipients(t *testing.T) {
	ch := &mockChannel{name: "test"}
	router := &staticRouter{
		recipients: []Recipient{
			{Type: RecipientChannel, Target: "#ch1"},
			{Type: RecipientChannel, Target: "#ch2"},
		},
	}

	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{
			{Channel: ch, Formatter: &mockFormatter{}},
		},
		Router: router,
	})

	err := svc.NotifySync(Event{Type: EventDailyDigest})
	if err != nil {
		t.Fatalf("NotifySync() error = %v", err)
	}
	if len(ch.sent) != 2 {
		t.Fatalf("expected 2 sent messages, got %d", len(ch.sent))
	}
}

func TestService_NotifySync_MultipleChannels(t *testing.T) {
	ch1 := &mockChannel{name: "slack"}
	ch2 := &mockChannel{name: "webhook"}
	router := &staticRouter{
		recipients: []Recipient{{Type: RecipientChannel, Target: "#general"}},
	}

	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{
			{Channel: ch1, Formatter: &mockFormatter{}},
			{Channel: ch2, Formatter: &mockFormatter{}},
		},
		Router: router,
	})

	err := svc.NotifySync(Event{Type: EventBudgetAlert})
	if err != nil {
		t.Fatalf("NotifySync() error = %v", err)
	}
	if len(ch1.sent) != 1 {
		t.Errorf("ch1 sent %d, want 1", len(ch1.sent))
	}
	if len(ch2.sent) != 1 {
		t.Errorf("ch2 sent %d, want 1", len(ch2.sent))
	}
}

func TestService_NotifySync_NoRecipients(t *testing.T) {
	ch := &mockChannel{name: "test"}
	router := &staticRouter{recipients: nil}

	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{
			{Channel: ch, Formatter: &mockFormatter{}},
		},
		Router: router,
	})

	err := svc.NotifySync(Event{Type: EventBudgetAlert})
	if err != nil {
		t.Fatalf("NotifySync() error = %v", err)
	}
	if len(ch.sent) != 0 {
		t.Errorf("expected 0 sent messages when no recipients, got %d", len(ch.sent))
	}
}

func TestService_NotifySync_SendError(t *testing.T) {
	ch := &mockChannel{name: "test", sendErr: fmt.Errorf("network error")}
	router := &staticRouter{
		recipients: []Recipient{{Type: RecipientChannel, Target: "#general"}},
	}

	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{
			{Channel: ch, Formatter: &mockFormatter{}},
		},
		Router: router,
	})

	err := svc.NotifySync(Event{Type: EventBudgetAlert})
	if err == nil {
		t.Fatal("expected error from failed send")
	}
}

func TestService_NotifySync_FormatError(t *testing.T) {
	ch := &mockChannel{name: "test"}
	router := &staticRouter{
		recipients: []Recipient{{Type: RecipientChannel, Target: "#general"}},
	}

	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{
			{Channel: ch, Formatter: &mockFormatter{formatErr: fmt.Errorf("format failed")}},
		},
		Router: router,
	})

	err := svc.NotifySync(Event{Type: EventBudgetAlert})
	if err == nil {
		t.Fatal("expected error from failed format")
	}
}

// ── Dedup integration tests ──

func TestService_NotifySync_DedupSuppressesDuplicate(t *testing.T) {
	ch := &mockChannel{name: "test"}
	router := &staticRouter{
		recipients: []Recipient{{Type: RecipientChannel, Target: "#general"}},
	}
	dedup := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})

	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{
			{Channel: ch, Formatter: &mockFormatter{}},
		},
		Router:       router,
		Deduplicator: dedup,
	})

	now := time.Now()

	// First alert — should be sent.
	err := svc.NotifySync(Event{
		Type:      EventErrorSpike,
		Project:   "org/backend",
		Timestamp: now,
	})
	if err != nil {
		t.Fatalf("first NotifySync() error = %v", err)
	}
	if len(ch.sent) != 1 {
		t.Fatalf("expected 1 sent after first alert, got %d", len(ch.sent))
	}

	// Second alert within cooldown — should be suppressed.
	err = svc.NotifySync(Event{
		Type:      EventErrorSpike,
		Project:   "org/backend",
		Timestamp: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("second NotifySync() error = %v", err)
	}
	if len(ch.sent) != 1 {
		t.Errorf("expected still 1 sent after duplicate, got %d", len(ch.sent))
	}
}

func TestService_Notify_DedupSuppressesDuplicate(t *testing.T) {
	ch := &mockChannel{name: "test"}
	router := &staticRouter{
		recipients: []Recipient{{Type: RecipientChannel, Target: "#general"}},
	}
	dedup := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})

	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{
			{Channel: ch, Formatter: &mockFormatter{}},
		},
		Router:       router,
		Deduplicator: dedup,
	})

	now := time.Now()

	// First alert — should be sent (async).
	svc.Notify(Event{
		Type:      EventBudgetAlert,
		Project:   "org/repo",
		Timestamp: now,
	})
	time.Sleep(50 * time.Millisecond)

	ch.mu.Lock()
	firstCount := len(ch.sent)
	ch.mu.Unlock()
	if firstCount != 1 {
		t.Fatalf("expected 1 sent after first Notify, got %d", firstCount)
	}

	// Second alert within cooldown — should be suppressed.
	svc.Notify(Event{
		Type:      EventBudgetAlert,
		Project:   "org/repo",
		Timestamp: now.Add(5 * time.Minute),
	})
	time.Sleep(50 * time.Millisecond)

	ch.mu.Lock()
	secondCount := len(ch.sent)
	ch.mu.Unlock()
	if secondCount != 1 {
		t.Errorf("expected still 1 sent after duplicate Notify, got %d", secondCount)
	}
}

func TestService_NotifySync_DedupAllowsDigests(t *testing.T) {
	ch := &mockChannel{name: "test"}
	router := &staticRouter{
		recipients: []Recipient{{Type: RecipientChannel, Target: "#general"}},
	}
	dedup := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})

	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{
			{Channel: ch, Formatter: &mockFormatter{}},
		},
		Router:       router,
		Deduplicator: dedup,
	})

	now := time.Now()

	// Send digest twice — both should go through (digests are never deduplicated).
	for i := 0; i < 2; i++ {
		err := svc.NotifySync(Event{
			Type:      EventDailyDigest,
			Timestamp: now.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("digest %d NotifySync() error = %v", i, err)
		}
	}

	if len(ch.sent) != 2 {
		t.Errorf("expected 2 digest sends (no dedup), got %d", len(ch.sent))
	}
}

func TestService_NotifySync_NoDedupWithoutDeduplicator(t *testing.T) {
	ch := &mockChannel{name: "test"}
	router := &staticRouter{
		recipients: []Recipient{{Type: RecipientChannel, Target: "#general"}},
	}

	// No deduplicator configured — duplicates should be sent.
	svc := NewService(ServiceConfig{
		Channels: []ChannelWithFormatter{
			{Channel: ch, Formatter: &mockFormatter{}},
		},
		Router: router,
	})

	now := time.Now()

	for i := 0; i < 3; i++ {
		err := svc.NotifySync(Event{
			Type:      EventErrorSpike,
			Project:   "org/backend",
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("NotifySync(%d) error = %v", i, err)
		}
	}

	if len(ch.sent) != 3 {
		t.Errorf("expected 3 sends without deduplicator, got %d", len(ch.sent))
	}
}
