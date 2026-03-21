package telemetry

import (
	"path/filepath"
	"testing"
	"time"
)

// ── Event creation ──

func TestNewEvent_SetsNameAndTimestamp(t *testing.T) {
	before := time.Now()
	e := NewEvent(EventSessionCaptured, map[string]string{"provider": "claude-code"})
	after := time.Now()

	if e.Name != EventSessionCaptured {
		t.Errorf("Name = %q, want %q", e.Name, EventSessionCaptured)
	}
	if e.Timestamp.Before(before) || e.Timestamp.After(after) {
		t.Errorf("Timestamp = %v, want between %v and %v", e.Timestamp, before, after)
	}
	if e.Properties["provider"] != "claude-code" {
		t.Errorf("Properties[provider] = %q, want %q", e.Properties["provider"], "claude-code")
	}
}

func TestNewEvent_NilProperties(t *testing.T) {
	e := NewEvent(EventCommandExecuted, nil)
	if e.Name != EventCommandExecuted {
		t.Errorf("Name = %q, want %q", e.Name, EventCommandExecuted)
	}
	if e.Properties != nil {
		t.Errorf("Properties = %v, want nil", e.Properties)
	}
}

// ── Event name constants ──

func TestEventConstants_NotEmpty(t *testing.T) {
	constants := []string{
		EventSessionCaptured,
		EventSessionAnalyzed,
		EventCommandExecuted,
		EventServerStarted,
	}
	for _, c := range constants {
		if c == "" {
			t.Error("event constant should not be empty")
		}
	}
}

// ── NoopCollector ──

func TestNoopCollector_ImplementsCollector(t *testing.T) {
	var c Collector = NoopCollector{}
	_ = c // compile-time interface check
}

func TestNoopCollector_Enabled(t *testing.T) {
	c := NoopCollector{}
	if c.Enabled() {
		t.Error("NoopCollector.Enabled() = true, want false")
	}
}

func TestNoopCollector_TrackDoesNotPanic(t *testing.T) {
	c := NoopCollector{}
	// Should not panic.
	c.Track(NewEvent(EventSessionCaptured, nil))
}

func TestNoopCollector_FlushReturnsNil(t *testing.T) {
	c := NoopCollector{}
	if err := c.Flush(); err != nil {
		t.Errorf("NoopCollector.Flush() = %v, want nil", err)
	}
}

// ── LocalCollector ──

func TestLocalCollector_ImplementsCollector(t *testing.T) {
	var c Collector = NewLocalCollector(t.TempDir())
	_ = c // compile-time interface check
}

func TestLocalCollector_Enabled(t *testing.T) {
	c := NewLocalCollector(t.TempDir())
	if !c.Enabled() {
		t.Error("LocalCollector.Enabled() = false, want true")
	}
}

func TestLocalCollector_FlushReturnsNil(t *testing.T) {
	c := NewLocalCollector(t.TempDir())
	if err := c.Flush(); err != nil {
		t.Errorf("LocalCollector.Flush() = %v, want nil", err)
	}
}

func TestLocalCollector_TrackAndReadBack(t *testing.T) {
	dir := t.TempDir()
	c := NewLocalCollector(dir)

	// Track two events.
	e1 := NewEvent(EventSessionCaptured, map[string]string{"provider": "claude-code"})
	e2 := NewEvent(EventCommandExecuted, map[string]string{"command": "capture"})
	c.Track(e1)
	c.Track(e2)

	// Read back from file.
	events, err := ReadEvents(c.Path())
	if err != nil {
		t.Fatalf("ReadEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("ReadEvents() count = %d, want 2", len(events))
	}

	// Verify first event.
	if events[0].Name != EventSessionCaptured {
		t.Errorf("events[0].Name = %q, want %q", events[0].Name, EventSessionCaptured)
	}
	if events[0].Properties["provider"] != "claude-code" {
		t.Errorf("events[0].Properties[provider] = %q, want %q", events[0].Properties["provider"], "claude-code")
	}

	// Verify second event.
	if events[1].Name != EventCommandExecuted {
		t.Errorf("events[1].Name = %q, want %q", events[1].Name, EventCommandExecuted)
	}
	if events[1].Properties["command"] != "capture" {
		t.Errorf("events[1].Properties[command] = %q, want %q", events[1].Properties["command"], "capture")
	}

	// Timestamps should be non-zero.
	if events[0].Timestamp.IsZero() {
		t.Error("events[0].Timestamp should not be zero")
	}
	if events[1].Timestamp.IsZero() {
		t.Error("events[1].Timestamp should not be zero")
	}
}

func TestLocalCollector_TrackWithNoProperties(t *testing.T) {
	dir := t.TempDir()
	c := NewLocalCollector(dir)

	c.Track(NewEvent(EventServerStarted, nil))

	events, err := ReadEvents(c.Path())
	if err != nil {
		t.Fatalf("ReadEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ReadEvents() count = %d, want 1", len(events))
	}
	if events[0].Name != EventServerStarted {
		t.Errorf("Name = %q, want %q", events[0].Name, EventServerStarted)
	}
}

func TestLocalCollector_CreatesDirectoryIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "config")
	c := NewLocalCollector(dir)

	c.Track(NewEvent(EventSessionAnalyzed, nil))

	events, err := ReadEvents(c.Path())
	if err != nil {
		t.Fatalf("ReadEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ReadEvents() count = %d, want 1", len(events))
	}
}

func TestLocalCollector_Path(t *testing.T) {
	dir := t.TempDir()
	c := NewLocalCollector(dir)

	want := filepath.Join(dir, "telemetry.jsonl")
	if got := c.Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

// ── ReadEvents edge cases ──

func TestReadEvents_NonExistentFile(t *testing.T) {
	_, err := ReadEvents("/nonexistent/path/telemetry.jsonl")
	if err == nil {
		t.Error("ReadEvents() should return error for non-existent file")
	}
}
