package service

import (
	"fmt"

	"github.com/ChristopherAparicio/aisync/git"
	syncsvc "github.com/ChristopherAparicio/aisync/internal/gitsync"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── SyncService ──

// SyncService orchestrates session synchronization via the git sync branch.
// It wraps the gitsync.Service and provides Push/Pull/Sync methods.
type SyncService struct {
	inner *syncsvc.Service
}

// NewSyncService creates a SyncService.
func NewSyncService(gitClient *git.Client, store storage.Store) *SyncService {
	return &SyncService{
		inner: syncsvc.NewService(gitClient, store),
	}
}

// PushResult contains the outcome of a push operation.
type PushResult struct {
	Pushed int
	Remote bool
}

// PullResult contains the outcome of a pull operation.
type PullResult struct {
	Pulled int
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
func (s *SyncService) Pull(pullRemote bool) (*PullResult, error) {
	result, err := s.inner.Pull(pullRemote)
	if err != nil {
		return nil, err
	}
	return &PullResult{
		Pulled: result.Pulled,
	}, nil
}

// Sync performs pull then push.
func (s *SyncService) Sync(remote bool) (*SyncResult, error) {
	pullResult, err := s.inner.Pull(remote)
	if err != nil {
		return nil, fmt.Errorf("pull: %w", err)
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

// ReadIndex reads the sync branch index for listing.
func (s *SyncService) ReadIndex() (*syncsvc.Index, error) {
	return s.inner.ReadIndex()
}
