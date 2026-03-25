package service

import (
	"fmt"

	"github.com/ChristopherAparicio/aisync/git"
	syncsvc "github.com/ChristopherAparicio/aisync/internal/gitsync"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── SyncService ──

// SyncService orchestrates session synchronization via the git sync branch.
// It wraps the gitsync.Service and provides Push/Pull/Sync methods.
type SyncService struct {
	inner    *syncsvc.Service
	store    storage.Store
	postPull PostCaptureFunc // optional: called for each newly-pulled session
}

// NewSyncService creates a SyncService.
func NewSyncService(gitClient *git.Client, store storage.Store) *SyncService {
	return &SyncService{
		inner: syncsvc.NewService(gitClient, store),
		store: store,
	}
}

// SetPostPull sets a callback that runs for each session imported during Pull.
// This enables event extraction, error classification, etc. for synced sessions.
func (s *SyncService) SetPostPull(fn PostCaptureFunc) {
	s.postPull = fn
}

// PushResult contains the outcome of a push operation.
type PushResult struct {
	Pushed int
	Remote bool
}

// PullResult contains the outcome of a pull operation.
type PullResult struct {
	Pulled    int
	Processed int // sessions that had post-pull processing applied
}

// SyncResult contains the outcome of a sync (pull+push) operation.
type SyncResult struct {
	Pulled int
	Pushed int
	Remote bool
}

// Push exports sessions to the sync branch and optionally pushes to remote.
func (s *SyncService) Push(pushRemote bool) (*PushResult, error) {
	result, err := s.inner.Push(pushRemote)
	if err != nil {
		return nil, err
	}
	return &PushResult{
		Pushed: result.Pushed,
		Remote: result.Remote,
	}, nil
}

// Pull imports sessions from the sync branch.
// If a PostPull callback is set, it runs for each newly-pulled session
// to trigger event extraction, error classification, etc.
func (s *SyncService) Pull(pullRemote bool) (*PullResult, error) {
	result, err := s.inner.Pull(pullRemote)
	if err != nil {
		return nil, err
	}

	pr := &PullResult{Pulled: result.Pulled}

	// Run post-pull processing for newly-pulled sessions.
	if s.postPull != nil && result.Pulled > 0 {
		pr.Processed = s.postPullProcess(result.PulledIDs)
	}

	return pr, nil
}

// Sync performs pull then push.
func (s *SyncService) Sync(remote bool) (*SyncResult, error) {
	pullResult, err := s.inner.Pull(remote)
	if err != nil {
		return nil, fmt.Errorf("pull: %w", err)
	}

	// Post-pull processing for newly-pulled sessions.
	if s.postPull != nil && pullResult.Pulled > 0 {
		s.postPullProcess(pullResult.PulledIDs)
	}

	pushResult, err := s.inner.Push(remote)
	if err != nil {
		return nil, fmt.Errorf("push: %w", err)
	}

	return &SyncResult{
		Pulled: pullResult.Pulled,
		Pushed: pushResult.Pushed,
		Remote: pushResult.Remote,
	}, nil
}

// postPullProcess loads each pulled session and runs the PostPull callback.
func (s *SyncService) postPullProcess(ids []session.ID) int {
	processed := 0
	for _, id := range ids {
		sess, err := s.store.Get(id)
		if err != nil {
			continue
		}
		s.postPull(sess)
		processed++
	}
	return processed
}

// ReadIndex reads the sync branch index for listing.
func (s *SyncService) ReadIndex() (*syncsvc.Index, error) {
	return s.inner.ReadIndex()
}
