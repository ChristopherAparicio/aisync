package webhooks

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestDispatcher_Fire_Success(t *testing.T) {
	var mu sync.Mutex
	var received []Event

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("expected Content-Type: application/json")
		}
		if r.Header.Get("X-AiSync-Event") == "" {
			t.Error("expected X-AiSync-Event header")
		}

		var event Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("decode error: %v", err)
			return
		}
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := New(Config{
		Hooks: []HookConfig{
			{URL: srv.URL, Events: []EventType{EventSessionCaptured}},
		},
		Logger: log.Default(),
	})

	d.Fire(EventSessionCaptured, map[string]string{"session_id": "test-123"})

	// Wait for async delivery.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].Type != EventSessionCaptured {
		t.Errorf("type = %q, want %q", received[0].Type, EventSessionCaptured)
	}
}

func TestDispatcher_Fire_EventFilter(t *testing.T) {
	var mu sync.Mutex
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := New(Config{
		Hooks: []HookConfig{
			{URL: srv.URL, Events: []EventType{EventSessionAnalyzed}}, // only analyzed
		},
		Logger: log.Default(),
	})

	d.Fire(EventSessionCaptured, nil) // should be filtered out
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 0 {
		t.Errorf("expected 0 calls (filtered), got %d", callCount)
	}
}

func TestDispatcher_Fire_AllEvents(t *testing.T) {
	var mu sync.Mutex
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := New(Config{
		Hooks: []HookConfig{
			{URL: srv.URL}, // no events filter = all events
		},
		Logger: log.Default(),
	})

	d.Fire(EventSessionCaptured, nil)
	d.Fire(EventSessionAnalyzed, nil)
	d.Fire(EventSkillMissed, nil)
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 3 {
		t.Errorf("expected 3 calls (all events), got %d", callCount)
	}
}

func TestDispatcher_Fire_RetryOnFailure(t *testing.T) {
	var mu sync.Mutex
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		c := callCount
		mu.Unlock()
		if c <= 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := New(Config{
		Hooks: []HookConfig{
			{URL: srv.URL},
		},
		MaxRetries: 2,
		Timeout:    5 * time.Second,
		Logger:     log.Default(),
	})

	d.Fire(EventSessionCaptured, nil)
	time.Sleep(5 * time.Second) // allow time for retries

	mu.Lock()
	defer mu.Unlock()
	if callCount < 2 {
		t.Errorf("expected at least 2 attempts (1 fail + 1 retry), got %d", callCount)
	}
}

func TestDispatcher_Nil_Safe(t *testing.T) {
	var d *Dispatcher
	d.Fire(EventSessionCaptured, nil) // should not panic
}

func TestNew_NoHooks_ReturnsNil(t *testing.T) {
	d := New(Config{})
	if d != nil {
		t.Error("expected nil dispatcher when no hooks configured")
	}
}

func TestHookConfig_Matches(t *testing.T) {
	cases := []struct {
		events []EventType
		event  EventType
		want   bool
	}{
		{nil, EventSessionCaptured, true},                                             // empty = all
		{[]EventType{EventSessionCaptured}, EventSessionCaptured, true},               // exact match
		{[]EventType{EventSessionAnalyzed}, EventSessionCaptured, false},              // no match
		{[]EventType{EventSessionCaptured, EventSkillMissed}, EventSkillMissed, true}, // multi
	}

	for _, tc := range cases {
		hook := HookConfig{Events: tc.events}
		if got := hook.matches(tc.event); got != tc.want {
			t.Errorf("matches(%v, %v) = %v, want %v", tc.events, tc.event, got, tc.want)
		}
	}
}
