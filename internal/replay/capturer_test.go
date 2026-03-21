package replay_test

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/replay"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

func TestProviderCapturer_UnsupportedProvider(t *testing.T) {
	store := testutil.NewMockStore()
	capturer := replay.NewProviderCapturer(store, nil)

	// Cursor is not supported for replay capture.
	_, err := capturer.CaptureReplay(session.ProviderCursor, "/tmp/worktree", "original-id")
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	t.Logf("got expected error: %v", err)
}

func TestProviderCapturer_NoSessionsFound(t *testing.T) {
	store := testutil.NewMockStore()
	capturer := replay.NewProviderCapturer(store, nil)

	// A worktree path that has no sessions (because no agent ran there).
	_, err := capturer.CaptureReplay(session.ProviderOpenCode, "/tmp/nonexistent-worktree", "original-id")
	if err == nil {
		t.Fatal("expected error when no sessions found")
	}
	t.Logf("got expected error: %v", err)
}
