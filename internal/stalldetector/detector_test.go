package stalldetector

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/session"

	_ "modernc.org/sqlite"
)

// ── classifyError ────────────────────────────────────────────────────

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   session.StallRootCause
	}{
		{"MessageAbortedError", 0, session.StallRootCauseAborted},
		{"APIError", 429, session.StallRootCauseRateLimit429},
		{"APIError", 500, session.StallRootCauseProviderError},
		{"APIError", 401, session.StallRootCauseProviderError},
		{"APIError", 0, session.StallRootCauseProviderError},
		{"ProviderAuthError", 0, session.StallRootCauseProviderError},
		{"ContextOverflowError", 0, session.StallRootCauseProviderError},
		{"UnknownError", 0, session.StallRootCauseProviderError},
		{"Something else", 0, session.StallRootCauseProviderError},
	}
	for _, c := range cases {
		got := classifyError(c.name, c.status)
		if got != c.want {
			t.Errorf("classifyError(%q, %d) = %q, want %q", c.name, c.status, got, c.want)
		}
	}
}

// ── estimateCost ─────────────────────────────────────────────────────

type fakeCatalog struct {
	entries map[string]pricing.ModelPrice
}

func (f *fakeCatalog) Lookup(model string) (pricing.ModelPrice, bool) {
	p, ok := f.entries[model]
	return p, ok
}

func (f *fakeCatalog) List() []pricing.ModelPrice {
	out := make([]pricing.ModelPrice, 0, len(f.entries))
	for _, p := range f.entries {
		out = append(out, p)
	}
	return out
}

func TestEstimateCost(t *testing.T) {
	cat := &fakeCatalog{
		entries: map[string]pricing.ModelPrice{
			"claude-opus-4-6": {
				Model:           "claude-opus-4-6",
				InputPerMToken:  15.0,
				OutputPerMToken: 75.0,
			},
		},
	}

	t.Run("nil catalog returns 0", func(t *testing.T) {
		if got := estimateCost(nil, "claude-opus-4-6", 1000, 1000, 0, 0); got != 0 {
			t.Errorf("nil catalog: got %v, want 0", got)
		}
	})

	t.Run("unknown model returns 0", func(t *testing.T) {
		if got := estimateCost(cat, "unknown-model", 1000, 1000, 0, 0); got != 0 {
			t.Errorf("unknown model: got %v, want 0", got)
		}
	})

	t.Run("base computation", func(t *testing.T) {
		// 1M input × $15 + 1M output × $75 = $90
		got := estimateCost(cat, "claude-opus-4-6", 1_000_000, 1_000_000, 0, 0)
		if got != 90.0 {
			t.Errorf("base: got %v, want 90", got)
		}
	})

	t.Run("cache tokens added to input", func(t *testing.T) {
		// (1M raw + 500k read + 500k write) × $15 + 0 output = $30
		got := estimateCost(cat, "claude-opus-4-6", 1_000_000, 0, 500_000, 500_000)
		if got != 30.0 {
			t.Errorf("cache: got %v, want 30", got)
		}
	})
}

// ── LiveKey ──────────────────────────────────────────────────────────

func TestLiveKey(t *testing.T) {
	ts := time.UnixMilli(1700000000000).UTC()
	want := "ses_abc|1700000000000"
	if got := LiveKey("ses_abc", ts); got != want {
		t.Errorf("LiveKey: got %q, want %q", got, want)
	}
}

// ── Detect (integration with a hand-built opencode-shaped DB) ────────

// buildFakeOpenCodeDB writes a minimal opencode.db-shaped SQLite file
// into a temp directory. Schema matches the columns the detector
// queries — session(id, parent_id), message(id, session_id, data),
// part(id, message_id, session_id, data). Other columns are omitted.
func buildFakeOpenCodeDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT)`,
		`CREATE TABLE part    (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, data TEXT)`,
	}
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return path
}

func insertSession(t *testing.T, db *sql.DB, id, parentID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO session(id, parent_id) VALUES (?, ?)`, id, parentID); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func insertMessage(t *testing.T, db *sql.DB, id, sessionID string, data map[string]any) {
	t.Helper()
	b, _ := json.Marshal(data)
	if _, err := db.Exec(`INSERT INTO message(id, session_id, data) VALUES (?, ?, ?)`, id, sessionID, string(b)); err != nil {
		t.Fatalf("insert message: %v", err)
	}
}

func insertPart(t *testing.T, db *sql.DB, id, messageID, sessionID string, data map[string]any) {
	t.Helper()
	b, _ := json.Marshal(data)
	if _, err := db.Exec(`INSERT INTO part(id, message_id, session_id, data) VALUES (?, ?, ?, ?)`, id, messageID, sessionID, string(b)); err != nil {
		t.Fatalf("insert part: %v", err)
	}
}

func TestDetect_LiveStall(t *testing.T) {
	path := buildFakeOpenCodeDB(t)

	// Open writer to seed data, then close.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	stuckStart := now.Add(-30 * time.Minute) // 30min ago → stuck (> 15min threshold)
	freshStart := now.Add(-5 * time.Minute)  // 5min ago → NOT stuck

	insertSession(t, db, "ses_stuck", "ses_parent")
	insertSession(t, db, "ses_fresh", "")

	insertMessage(t, db, "msg_stuck", "ses_stuck", map[string]any{
		"providerID": "anthropic",
		"modelID":    "claude-opus-4-6",
		"agent":      "build",
	})
	insertMessage(t, db, "msg_fresh", "ses_fresh", map[string]any{
		"providerID": "anthropic",
		"modelID":    "claude-opus-4-6",
		"agent":      "build",
	})

	insertPart(t, db, "part_stuck", "msg_stuck", "ses_stuck", map[string]any{
		"type": "tool",
		"tool": "bash",
		"state": map[string]any{
			"status": "running",
			"time":   map[string]any{"start": stuckStart.UnixMilli(), "end": 0},
		},
	})
	insertPart(t, db, "part_fresh", "msg_fresh", "ses_fresh", map[string]any{
		"type": "tool",
		"tool": "read",
		"state": map[string]any{
			"status": "running",
			"time":   map[string]any{"start": freshStart.UnixMilli(), "end": 0},
		},
	})
	// Completed part should NOT show up.
	insertPart(t, db, "part_done", "msg_stuck", "ses_stuck", map[string]any{
		"type": "tool",
		"tool": "grep",
		"state": map[string]any{
			"status": "completed",
			"time":   map[string]any{"start": stuckStart.UnixMilli(), "end": now.UnixMilli()},
		},
	})
	_ = db.Close()

	d := New(Config{
		OpenCodeDBPath: path,
		Threshold:      15 * time.Minute,
		Lookback:       24 * time.Hour,
		Now:            func() time.Time { return now },
	})
	res, err := d.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if len(res.Stalls) != 1 {
		t.Fatalf("expected 1 stall, got %d: %+v", len(res.Stalls), res.Stalls)
	}
	s := res.Stalls[0]
	if s.ProviderSessionID != "ses_stuck" {
		t.Errorf("ProviderSessionID = %q, want ses_stuck", s.ProviderSessionID)
	}
	if s.RootCause != session.StallRootCauseStreamStall {
		t.Errorf("RootCause = %q, want %q", s.RootCause, session.StallRootCauseStreamStall)
	}
	if s.ToolName != "bash" {
		t.Errorf("ToolName = %q, want bash", s.ToolName)
	}
	if s.Provider != "anthropic" || s.Model != "claude-opus-4-6" || s.Agent != "build" {
		t.Errorf("enrichment wrong: provider=%q model=%q agent=%q", s.Provider, s.Model, s.Agent)
	}
	if s.ParentSessionID != "ses_parent" {
		t.Errorf("ParentSessionID = %q, want ses_parent", s.ParentSessionID)
	}
	if s.EndedAt != nil {
		t.Errorf("live stall should have EndedAt == nil, got %v", s.EndedAt)
	}
	if s.DurationMs <= 0 {
		t.Errorf("DurationMs should be > 0, got %d", s.DurationMs)
	}

	wantKey := LiveKey("ses_stuck", stuckStart)
	if _, ok := res.LiveKeys[wantKey]; !ok {
		t.Errorf("LiveKeys missing %q; got %v", wantKey, res.LiveKeys)
	}
}

func TestDetect_ErroredMessages(t *testing.T) {
	path := buildFakeOpenCodeDB(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	abortedCreated := now.Add(-2 * time.Hour)
	abortedCompleted := abortedCreated.Add(45 * time.Second)
	rate429Created := now.Add(-1 * time.Hour)
	tooOldCreated := now.Add(-48 * time.Hour) // outside 24h lookback

	insertSession(t, db, "ses_aborted", "")
	insertSession(t, db, "ses_429", "")
	insertSession(t, db, "ses_old", "")

	insertMessage(t, db, "msg_aborted", "ses_aborted", map[string]any{
		"providerID": "anthropic",
		"modelID":    "claude-opus-4-6",
		"agent":      "build",
		"cost":       0.42,
		"tokens": map[string]any{
			"input":  10_000,
			"output": 5_000,
			"cache":  map[string]any{"read": 100_000, "write": 0},
		},
		"time": map[string]any{
			"created":   abortedCreated.UnixMilli(),
			"completed": abortedCompleted.UnixMilli(),
		},
		"error": map[string]any{
			"name": "MessageAbortedError",
			"data": map[string]any{"message": "The operation was aborted."},
		},
	})

	insertMessage(t, db, "msg_429", "ses_429", map[string]any{
		"providerID": "anthropic",
		"modelID":    "claude-opus-4-6",
		"agent":      "build",
		"tokens": map[string]any{
			"input": 50_000,
		},
		"time": map[string]any{"created": rate429Created.UnixMilli()},
		"error": map[string]any{
			"name": "APIError",
			"data": map[string]any{"message": "rate limited", "statusCode": 429},
		},
	})

	insertMessage(t, db, "msg_old", "ses_old", map[string]any{
		"providerID": "anthropic",
		"time":       map[string]any{"created": tooOldCreated.UnixMilli()},
		"error":      map[string]any{"name": "MessageAbortedError"},
	})
	// Clean message with no error — should NOT show up.
	insertMessage(t, db, "msg_clean", "ses_aborted", map[string]any{
		"providerID": "anthropic",
		"time":       map[string]any{"created": now.UnixMilli()},
	})
	_ = db.Close()

	d := New(Config{
		OpenCodeDBPath: path,
		Threshold:      15 * time.Minute,
		Lookback:       24 * time.Hour,
		Now:            func() time.Time { return now },
	})
	res, err := d.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if len(res.Stalls) != 2 {
		t.Fatalf("expected 2 errored stalls (aborted + 429), got %d: %+v", len(res.Stalls), res.Stalls)
	}

	var aborted, rate session.SessionStall
	for _, s := range res.Stalls {
		switch s.RootCause {
		case session.StallRootCauseAborted:
			aborted = s
		case session.StallRootCauseRateLimit429:
			rate = s
		}
	}
	if aborted.ProviderSessionID != "ses_aborted" {
		t.Errorf("aborted: ProviderSessionID = %q, want ses_aborted", aborted.ProviderSessionID)
	}
	if aborted.CostLostUSD != 0.42 {
		t.Errorf("aborted: CostLostUSD = %v, want 0.42 (from msg.cost)", aborted.CostLostUSD)
	}
	// 10k + 5k + 100k + 0 + 0 = 115_000
	if aborted.TokensLost != 115_000 {
		t.Errorf("aborted: TokensLost = %d, want 115000", aborted.TokensLost)
	}
	if aborted.EndedAt == nil {
		t.Errorf("aborted: EndedAt should be set")
	}
	if aborted.DurationMs != 45_000 {
		t.Errorf("aborted: DurationMs = %d, want 45000", aborted.DurationMs)
	}
	if aborted.ErrorMessage != "The operation was aborted." {
		t.Errorf("aborted: ErrorMessage = %q", aborted.ErrorMessage)
	}

	if rate.ProviderSessionID != "ses_429" {
		t.Errorf("429: ProviderSessionID = %q, want ses_429", rate.ProviderSessionID)
	}
	if rate.TokensLost != 50_000 {
		t.Errorf("429: TokensLost = %d, want 50000", rate.TokensLost)
	}
}
