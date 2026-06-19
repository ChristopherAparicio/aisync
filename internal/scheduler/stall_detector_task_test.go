package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"

	_ "modernc.org/sqlite"
)

// buildOC writes a minimal opencode-shaped sqlite db into a tempdir
// and seeds it via the supplied callback. Returns the file path.
func buildOC(t *testing.T, seed func(db *sql.DB)) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

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
	if seed != nil {
		seed(db)
	}
	return path
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %s: %v", q, err)
	}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestStallDetectorTask_UpsertAndSeal(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	stuckStart := now.Add(-30 * time.Minute)

	// --- Pass 1: one stuck tool in OpenCode ---
	pathA := buildOC(t, func(db *sql.DB) {
		mustExec(t, db, `INSERT INTO session(id, parent_id) VALUES (?, ?)`, "ses_a", "")
		mustExec(t, db, `INSERT INTO message(id, session_id, data) VALUES (?, ?, ?)`,
			"msg_a", "ses_a", toJSON(t, map[string]any{
				"providerID": "anthropic", "modelID": "claude-opus-4-7", "agent": "build",
			}))
		mustExec(t, db, `INSERT INTO part(id, message_id, session_id, data) VALUES (?, ?, ?, ?)`,
			"part_a", "msg_a", "ses_a", toJSON(t, map[string]any{
				"type": "tool", "tool": "bash",
				"state": map[string]any{
					"status": "running",
					"time":   map[string]any{"start": stuckStart.UnixMilli()},
				},
			}))
	})

	mock := testutil.NewMockStore()
	silentLogger := log.New(io.Discard, "", 0)

	task := NewStallDetectorTask(StallDetectorTaskConfig{
		Store:          mock,
		OpenCodeDBPath: pathA,
		Threshold:      15 * time.Minute,
		Lookback:       24 * time.Hour,
		Logger:         silentLogger,
		Now:            func() time.Time { return now },
	})
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}

	live, _ := mock.ListLiveStalls()
	if len(live) != 1 {
		t.Fatalf("expected 1 live stall after pass 1, got %d", len(live))
	}
	if live[0].ProviderSessionID != "ses_a" || live[0].ToolName != "bash" {
		t.Errorf("unexpected stall: %+v", live[0])
	}

	// --- Pass 2: same DB → idempotent (still 1 row, still live) ---
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	live2, _ := mock.ListLiveStalls()
	if len(live2) != 1 {
		t.Fatalf("expected idempotent (1 live), got %d", len(live2))
	}
	if live2[0].ID != live[0].ID {
		t.Errorf("ID changed on re-upsert: %d → %d", live[0].ID, live2[0].ID)
	}

	// --- Pass 3: OpenCode no longer has the running part → must seal ---
	pathB := buildOC(t, func(db *sql.DB) {
		mustExec(t, db, `INSERT INTO session(id, parent_id) VALUES (?, ?)`, "ses_a", "")
		// no parts → nothing is live anymore
	})
	taskSeal := NewStallDetectorTask(StallDetectorTaskConfig{
		Store:          mock,
		OpenCodeDBPath: pathB,
		Threshold:      15 * time.Minute,
		Lookback:       24 * time.Hour,
		Logger:         silentLogger,
		Now:            func() time.Time { return now.Add(10 * time.Minute) },
	})
	if err := taskSeal.Run(context.Background()); err != nil {
		t.Fatalf("seal run: %v", err)
	}

	live3, _ := mock.ListLiveStalls()
	if len(live3) != 0 {
		t.Fatalf("expected 0 live after seal pass, got %d", len(live3))
	}

	sealed, _ := mock.GetStall(live[0].ID)
	if sealed == nil {
		t.Fatalf("stall vanished after seal")
	}
	if sealed.EndedAt == nil {
		t.Errorf("sealed stall should have EndedAt set")
	}
	if sealed.DurationMs <= 0 {
		t.Errorf("sealed stall should have positive DurationMs, got %d", sealed.DurationMs)
	}
}

func TestStallDetectorTask_AbortedMessage(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	abortStart := now.Add(-1 * time.Hour)
	abortEnd := abortStart.Add(30 * time.Second)

	path := buildOC(t, func(db *sql.DB) {
		mustExec(t, db, `INSERT INTO session(id, parent_id) VALUES (?, ?)`, "ses_x", "")
		mustExec(t, db, `INSERT INTO message(id, session_id, data) VALUES (?, ?, ?)`,
			"msg_x", "ses_x", toJSON(t, map[string]any{
				"providerID": "anthropic", "modelID": "claude-opus-4-7", "agent": "build",
				"cost":   0.15,
				"tokens": map[string]any{"input": 1000, "output": 500},
				"time":   map[string]any{"created": abortStart.UnixMilli(), "completed": abortEnd.UnixMilli()},
				"error":  map[string]any{"name": "MessageAbortedError", "data": map[string]any{"message": "Aborted"}},
			}))
	})

	mock := testutil.NewMockStore()
	silentLogger := log.New(io.Discard, "", 0)

	task := NewStallDetectorTask(StallDetectorTaskConfig{
		Store:          mock,
		OpenCodeDBPath: path,
		Threshold:      15 * time.Minute,
		Lookback:       24 * time.Hour,
		Logger:         silentLogger,
		Now:            func() time.Time { return now },
	})
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	all, _ := mock.ListStalls(session.StallFilter{})
	if len(all) != 1 {
		t.Fatalf("expected 1 stall, got %d", len(all))
	}
	got := all[0]
	if got.RootCause != session.StallRootCauseAborted {
		t.Errorf("RootCause = %q, want %q", got.RootCause, session.StallRootCauseAborted)
	}
	if got.CostLostUSD != 0.15 {
		t.Errorf("CostLostUSD = %v, want 0.15 (from msg.cost)", got.CostLostUSD)
	}
	if got.TokensLost != 1500 {
		t.Errorf("TokensLost = %d, want 1500", got.TokensLost)
	}
	if got.EndedAt == nil {
		t.Errorf("aborted stall must have EndedAt set")
	}
	if got.IsLive() {
		t.Errorf("aborted stall must not be live")
	}
}
