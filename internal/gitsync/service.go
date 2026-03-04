// Package gitsync implements session synchronization via a dedicated git branch.
// Sessions are stored as JSON files on the aisync/sessions orphan branch,
// enabling team sharing without polluting the main branch history.
package gitsync

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// IndexEntry is a lightweight entry in the sync index for fast lookup.
type IndexEntry struct {
	CreatedAt    time.Time            `json:"created_at"`
	ID           session.ID           `json:"id"`
	Provider     session.ProviderName `json:"provider"`
	Agent        string               `json:"agent"`
	Branch       string               `json:"branch,omitempty"`
	Summary      string               `json:"summary,omitempty"`
	MessageCount int                  `json:"message_count"`
}

// Index holds the lightweight session index for the sync branch.
type Index struct {
	UpdatedAt time.Time    `json:"updated_at"`
	Entries   []IndexEntry `json:"entries"`
	Version   int          `json:"version"`
}

// PushResult contains the outcome of a push operation.
type PushResult struct {
	Pushed int  // number of sessions written to the sync branch
	Remote bool // whether push to remote was attempted
}

// PullResult contains the outcome of a pull operation.
type PullResult struct {
	Pulled int // number of sessions imported from the sync branch
}

// Service orchestrates session sync via the git branch.
type Service struct {
	gitClient *git.Client
	store     storage.Store
}

// NewService creates a sync service.
func NewService(gitClient *git.Client, store storage.Store) *Service {
	return &Service{
		gitClient: gitClient,
		store:     store,
	}
}

// Push exports sessions from the local store to the aisync/sessions branch.
// If pushRemote is true, also pushes to the remote.
func (s *Service) Push(pushRemote bool) (*PushResult, error) {
	// Ensure sync branch exists
	if !s.gitClient.SyncBranchExists() {
		if err := s.gitClient.InitSyncBranch(); err != nil {
			return nil, fmt.Errorf("initializing sync branch: %w", err)
		}
	}

	// List all sessions
	summaries, err := s.store.List(session.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	if len(summaries) == 0 {
		return &PushResult{Pushed: 0}, nil
	}

	// Read existing index to determine what's already synced
	existingIndex, _ := s.readIndex()
	existingIDs := make(map[session.ID]bool)
	if existingIndex != nil {
		for _, e := range existingIndex.Entries {
			existingIDs[e.ID] = true
		}
	}

	// Collect new sessions to push
	files := make(map[string][]byte)
	var indexEntries []IndexEntry
	var pushed int

	// Keep existing index entries
	if existingIndex != nil {
		indexEntries = existingIndex.Entries
	}

	for _, summary := range summaries {
		// Load the full session
		sess, getErr := s.store.Get(summary.ID)
		if getErr != nil {
			continue
		}

		// Check if already synced
		if existingIDs[sess.ID] {
			continue
		}

		// Serialize to JSON
		data, marshalErr := json.MarshalIndent(sess, "", "  ")
		if marshalErr != nil {
			continue
		}

		fileName := string(sess.ID) + ".json"
		files[fileName] = data
		pushed++

		// Add index entry
		indexEntries = append(indexEntries, IndexEntry{
			ID:           sess.ID,
			Provider:     sess.Provider,
			Agent:        sess.Agent,
			Branch:       sess.Branch,
			Summary:      sess.Summary,
			MessageCount: len(sess.Messages),
			CreatedAt:    sess.CreatedAt,
		})
	}

	if pushed == 0 {
		return &PushResult{Pushed: 0}, nil
	}

	// Sort index by created_at descending
	sort.Slice(indexEntries, func(i, j int) bool {
		return indexEntries[i].CreatedAt.After(indexEntries[j].CreatedAt)
	})

	// Write index
	idx := Index{
		Version:   1,
		UpdatedAt: time.Now(),
		Entries:   indexEntries,
	}
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling index: %w", err)
	}
	files["index.json"] = indexData

	// Write all files to the sync branch
	msg := fmt.Sprintf("aisync: push %d session(s)", pushed)
	if err := s.gitClient.WriteSyncFiles(files, msg); err != nil {
		return nil, fmt.Errorf("writing to sync branch: %w", err)
	}

	result := &PushResult{Pushed: pushed}

	// Push to remote if requested
	if pushRemote && s.gitClient.HasRemote("origin") {
		if pushErr := s.gitClient.PushSyncBranch("origin"); pushErr != nil {
			return nil, fmt.Errorf("pushing to remote: %w", pushErr)
		}
		result.Remote = true
	}

	return result, nil
}

// Pull imports sessions from the aisync/sessions branch into the local store.
// If pullRemote is true, fetches from the remote first.
func (s *Service) Pull(pullRemote bool) (*PullResult, error) {
	// Fetch from remote if requested.
	// PullSyncBranch may fail if the remote branch doesn't exist yet — that's expected.
	if pullRemote && s.gitClient.HasRemote("origin") {
		_ = s.gitClient.PullSyncBranch("origin")
	}

	if !s.gitClient.SyncBranchExists() {
		return &PullResult{Pulled: 0}, nil
	}

	// Read the index
	idx, err := s.readIndex()
	if err != nil || idx == nil {
		return &PullResult{Pulled: 0}, nil
	}

	// Import sessions that we don't already have
	var pulled int
	for _, entry := range idx.Entries {
		// Check if we already have this session
		if _, getErr := s.store.Get(entry.ID); getErr == nil {
			continue // already have it
		}

		// Read the session file from the sync branch
		fileName := string(entry.ID) + ".json"
		data, readErr := s.gitClient.ReadSyncFile(fileName)
		if readErr != nil || len(data) == 0 {
			continue
		}

		var sess session.Session
		if unmarshalErr := json.Unmarshal(data, &sess); unmarshalErr != nil {
			continue
		}

		if saveErr := s.store.Save(&sess); saveErr != nil {
			continue
		}
		pulled++
	}

	return &PullResult{Pulled: pulled}, nil
}

// readIndex reads the index.json from the sync branch.
func (s *Service) readIndex() (*Index, error) {
	data, err := s.gitClient.ReadSyncFile("index.json")
	if err != nil || len(data) == 0 {
		return nil, err
	}

	var idx Index
	if unmarshalErr := json.Unmarshal(data, &idx); unmarshalErr != nil {
		return nil, unmarshalErr
	}
	return &idx, nil
}

// ReadIndex reads the remote index from the sync branch (for listing).
func (s *Service) ReadIndex() (*Index, error) {
	return s.readIndex()
}
