package notification

import (
	"testing"
	"time"
)

func TestDeduplicator_NilSafe(t *testing.T) {
	var d *Deduplicator
	if !d.ShouldSend(Event{Type: EventBudgetAlert}) {
		t.Error("nil deduplicator should always allow send")
	}
	if d.Len() != 0 {
		t.Error("nil deduplicator Len should be 0")
	}
	d.Prune() // should not panic
}

func TestDeduplicator_DefaultCooldown(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{})
	if d.cooldown != 30*time.Minute {
		t.Errorf("default cooldown = %v, want 30m", d.cooldown)
	}
}

func TestDeduplicator_CustomCooldown(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 10 * time.Minute})
	if d.cooldown != 10*time.Minute {
		t.Errorf("cooldown = %v, want 10m", d.cooldown)
	}
}

func TestDeduplicator_FirstAlertAllowed(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})

	event := Event{
		Type:      EventErrorSpike,
		Project:   "org/backend",
		Timestamp: time.Now(),
	}

	if !d.ShouldSend(event) {
		t.Error("first alert should be allowed")
	}
	if d.Len() != 1 {
		t.Errorf("Len() = %d, want 1", d.Len())
	}
}

func TestDeduplicator_DuplicateWithinCooldown_Suppressed(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})
	now := time.Now()

	event := Event{
		Type:      EventErrorSpike,
		Project:   "org/backend",
		Timestamp: now,
	}

	// First: allowed
	if !d.ShouldSend(event) {
		t.Fatal("first alert should be allowed")
	}

	// Second within cooldown: suppressed
	event.Timestamp = now.Add(15 * time.Minute)
	if d.ShouldSend(event) {
		t.Error("duplicate within cooldown should be suppressed")
	}

	// Len should still be 1 (same key)
	if d.Len() != 1 {
		t.Errorf("Len() = %d, want 1", d.Len())
	}
}

func TestDeduplicator_AfterCooldown_Allowed(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})
	now := time.Now()

	event := Event{
		Type:      EventErrorSpike,
		Project:   "org/backend",
		Timestamp: now,
	}

	// First: allowed
	if !d.ShouldSend(event) {
		t.Fatal("first alert should be allowed")
	}

	// After cooldown: allowed again
	event.Timestamp = now.Add(31 * time.Minute)
	if !d.ShouldSend(event) {
		t.Error("alert after cooldown should be allowed")
	}
}

func TestDeduplicator_DifferentProjects_Independent(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})
	now := time.Now()

	eventA := Event{
		Type:      EventErrorSpike,
		Project:   "org/backend",
		Timestamp: now,
	}
	eventB := Event{
		Type:      EventErrorSpike,
		Project:   "org/frontend",
		Timestamp: now,
	}

	if !d.ShouldSend(eventA) {
		t.Error("first alert for backend should be allowed")
	}
	if !d.ShouldSend(eventB) {
		t.Error("first alert for frontend should be allowed (different project)")
	}

	// Duplicate for backend: suppressed
	eventA.Timestamp = now.Add(5 * time.Minute)
	if d.ShouldSend(eventA) {
		t.Error("duplicate backend alert should be suppressed")
	}

	// Duplicate for frontend: also suppressed
	eventB.Timestamp = now.Add(5 * time.Minute)
	if d.ShouldSend(eventB) {
		t.Error("duplicate frontend alert should be suppressed")
	}

	if d.Len() != 2 {
		t.Errorf("Len() = %d, want 2", d.Len())
	}
}

func TestDeduplicator_DifferentEventTypes_Independent(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})
	now := time.Now()

	errorEvent := Event{
		Type:      EventErrorSpike,
		Project:   "org/backend",
		Timestamp: now,
	}
	budgetEvent := Event{
		Type:      EventBudgetAlert,
		Project:   "org/backend",
		Timestamp: now,
	}

	if !d.ShouldSend(errorEvent) {
		t.Error("error spike should be allowed")
	}
	if !d.ShouldSend(budgetEvent) {
		t.Error("budget alert should be allowed (different event type)")
	}
}

func TestDeduplicator_DigestsNeverDeduplicated(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})
	now := time.Now()

	digestTypes := []EventType{
		EventDailyDigest,
		EventWeeklyReport,
		EventPersonalDaily,
		EventRecommendation,
	}

	for _, dt := range digestTypes {
		event := Event{Type: dt, Timestamp: now}

		// First send
		if !d.ShouldSend(event) {
			t.Errorf("digest %s first send should be allowed", dt)
		}

		// Immediate second send — should still be allowed (no dedup for digests)
		event.Timestamp = now.Add(1 * time.Second)
		if !d.ShouldSend(event) {
			t.Errorf("digest %s second send should be allowed (no dedup)", dt)
		}
	}

	// Digests should NOT be tracked
	if d.Len() != 0 {
		t.Errorf("Len() = %d, want 0 (digests should not be tracked)", d.Len())
	}
}

func TestDeduplicator_SessionCaptured_Deduplicated(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})
	now := time.Now()

	event := Event{
		Type:      EventSessionCaptured,
		Project:   "org/backend",
		Timestamp: now,
	}

	if !d.ShouldSend(event) {
		t.Error("first session.captured should be allowed")
	}

	event.Timestamp = now.Add(5 * time.Minute)
	if d.ShouldSend(event) {
		t.Error("duplicate session.captured should be suppressed")
	}
}

func TestDeduplicator_GlobalScope(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})
	now := time.Now()

	// Event with no project or owner — uses _global_ scope
	event := Event{
		Type:      EventErrorSpike,
		Timestamp: now,
	}

	if !d.ShouldSend(event) {
		t.Error("first global alert should be allowed")
	}

	event.Timestamp = now.Add(5 * time.Minute)
	if d.ShouldSend(event) {
		t.Error("duplicate global alert should be suppressed")
	}
}

func TestDeduplicator_OwnerIDScope(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})
	now := time.Now()

	// Alert event with OwnerID but no project — uses OwnerID as scope.
	// (Unusual for alerts, but tests the fallback chain.)
	event := Event{
		Type:      EventErrorSpike,
		OwnerID:   "U001",
		Timestamp: now,
	}

	if !d.ShouldSend(event) {
		t.Error("first owner-scoped alert should be allowed")
	}

	event.Timestamp = now.Add(5 * time.Minute)
	if d.ShouldSend(event) {
		t.Error("duplicate owner-scoped alert should be suppressed")
	}
}

func TestDeduplicator_Prune(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})

	// Add entries with timestamps far in the past (beyond 2x cooldown)
	d.mu.Lock()
	d.seen["error.spike:old-project"] = time.Now().Add(-2 * time.Hour)
	d.seen["budget.alert:another"] = time.Now().Add(-3 * time.Hour)
	d.seen["error.spike:recent"] = time.Now().Add(-10 * time.Minute)
	d.mu.Unlock()

	if d.Len() != 3 {
		t.Fatalf("Len() = %d before prune, want 3", d.Len())
	}

	d.Prune()

	if d.Len() != 1 {
		t.Errorf("Len() = %d after prune, want 1 (only 'recent' should remain)", d.Len())
	}
}

func TestDeduplicator_ZeroTimestamp_UsesNow(t *testing.T) {
	d := NewDeduplicator(DeduplicatorConfig{Cooldown: 30 * time.Minute})

	event := Event{
		Type:    EventErrorSpike,
		Project: "org/backend",
		// Timestamp is zero
	}

	if !d.ShouldSend(event) {
		t.Error("first alert with zero timestamp should be allowed")
	}

	// Second call: should be suppressed (zero timestamp resolves to now)
	if d.ShouldSend(event) {
		t.Error("duplicate with zero timestamp should be suppressed")
	}
}

// ── Helpers ──

func TestDedupKey(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{
			name:  "project scoped",
			event: Event{Type: EventErrorSpike, Project: "org/backend"},
			want:  "error.spike:org/backend",
		},
		{
			name:  "project path fallback",
			event: Event{Type: EventBudgetAlert, ProjectPath: "/home/user/project"},
			want:  "budget.alert:/home/user/project",
		},
		{
			name:  "owner fallback",
			event: Event{Type: EventErrorSpike, OwnerID: "U001"},
			want:  "error.spike:U001",
		},
		{
			name:  "global fallback",
			event: Event{Type: EventErrorSpike},
			want:  "error.spike:_global_",
		},
		{
			name:  "project takes precedence over path",
			event: Event{Type: EventErrorSpike, Project: "org/repo", ProjectPath: "/path/to/repo"},
			want:  "error.spike:org/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupKey(tt.event)
			if got != tt.want {
				t.Errorf("dedupKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsDigestEvent(t *testing.T) {
	digests := []EventType{EventDailyDigest, EventWeeklyReport, EventPersonalDaily, EventRecommendation}
	for _, dt := range digests {
		if !isDigestEvent(dt) {
			t.Errorf("isDigestEvent(%s) = false, want true", dt)
		}
	}

	alerts := []EventType{EventBudgetAlert, EventErrorSpike, EventSessionCaptured}
	for _, at := range alerts {
		if isDigestEvent(at) {
			t.Errorf("isDigestEvent(%s) = true, want false", at)
		}
	}
}
