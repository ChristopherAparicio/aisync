package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
)

func mustOpenSQLiteStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.New(filepath.Join(t.TempDir(), "blame.db"))
	if err != nil {
		t.Fatalf("sqlite.New error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func blameSession(id, projectPath, filePath string, at time.Time) *session.Session {
	return &session.Session{
		ID:          session.ID(id),
		Version:     1,
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "main",
		ProjectPath: projectPath,
		CreatedAt:   at,
		ExportedAt:  at,
		StorageMode: session.StorageModeCompact,
		FileChanges: []session.FileChange{
			{FilePath: filePath, ChangeType: session.ChangeModified},
		},
	}
}

// TestBlame_MatchesLegacyAbsoluteAndRelative proves the P1 dual-format lookup:
// a relative target (as produced by `git status`) must find sessions whose
// file_changes were stored as a legacy absolute path AND sessions stored with
// the normalized relative path. The legacy-absolute row is created by leaving
// ProjectPath empty so the path is stored verbatim — this stays stable whether
// or not write-time normalization (P2) is active.
func TestBlame_MatchesLegacyAbsoluteAndRelative(t *testing.T) {
	const root = "/home/chris/my-app"
	const rel = "internal/mcp/tools.go"
	abs := root + "/" + rel

	store := mustOpenSQLiteStore(t)

	legacy := blameSession("blame-abs", "", abs, time.Date(2026, 2, 16, 10, 0, 0, 0, time.UTC))
	modern := blameSession("blame-rel", root, rel, time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC))
	if err := store.Save(legacy); err != nil {
		t.Fatalf("Save(legacy) error = %v", err)
	}
	if err := store.Save(modern); err != nil {
		t.Fatalf("Save(modern) error = %v", err)
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Blame(context.Background(), BlameRequest{
		FilePath:    rel,
		ProjectPath: root,
		All:         true,
	})
	if err != nil {
		t.Fatalf("Blame() error = %v", err)
	}

	found := make(map[session.ID]bool)
	for _, e := range result.Entries {
		found[e.SessionID] = true
	}
	if !found["blame-abs"] {
		t.Errorf("relative target did not match legacy absolute row; entries=%+v", result.Entries)
	}
	if !found["blame-rel"] {
		t.Errorf("relative target did not match relative row; entries=%+v", result.Entries)
	}
}

// TestBlame_AbsoluteInputMatchesRelativeRow is the mirror case: an absolute
// target must still find a session stored with the normalized relative path.
func TestBlame_AbsoluteInputMatchesRelativeRow(t *testing.T) {
	const root = "/home/chris/my-app"
	const rel = "internal/mcp/tools.go"
	abs := root + "/" + rel

	store := mustOpenSQLiteStore(t)
	modern := blameSession("blame-rel", root, rel, time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC))
	if err := store.Save(modern); err != nil {
		t.Fatalf("Save(modern) error = %v", err)
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})
	result, err := svc.Blame(context.Background(), BlameRequest{
		FilePath:    abs,
		ProjectPath: root,
		All:         true,
	})
	if err != nil {
		t.Fatalf("Blame() error = %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].SessionID != "blame-rel" {
		t.Errorf("absolute target did not match relative row; entries=%+v", result.Entries)
	}
}
