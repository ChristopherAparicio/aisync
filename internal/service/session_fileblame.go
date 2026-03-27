package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ExtractAndSaveFiles parses a session's tool calls to extract file operations,
// then persists them in the store's file_changes table for blame queries.
// Returns the number of unique files extracted.
func (s *SessionService) ExtractAndSaveFiles(sess *session.Session) (int, error) {
	if sess == nil || len(sess.Messages) == 0 {
		return 0, nil
	}

	ops := session.ExtractFileOperations(sess.Messages)
	if len(ops) == 0 {
		return 0, nil
	}

	// Convert to records with session context.
	records := make([]session.SessionFileRecord, len(ops))
	for i, op := range ops {
		records[i] = session.SessionFileRecord{
			SessionID:  sess.ID,
			FilePath:   op.FilePath,
			ChangeType: op.ChangeType,
			ToolName:   op.ToolName,
			CreatedAt:  sess.CreatedAt,
		}
	}

	if err := s.store.ReplaceSessionFiles(sess.ID, records); err != nil {
		return 0, fmt.Errorf("saving file records: %w", err)
	}

	return len(records), nil
}

// BackfillFileBlame runs file extraction on all sessions that don't yet have
// file_changes data. It loads each session's full payload, parses tool calls,
// and stores the results.
//
// This is expensive — each session's payload must be decompressed and parsed.
// Intended for one-time migration or manual re-indexing via CLI.
func (s *SessionService) BackfillFileBlame(ctx context.Context) (processed, filesExtracted int, err error) {
	// List all sessions.
	summaries, err := s.store.List(session.ListOptions{
		All: true,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("listing sessions: %w", err)
	}

	// Check which sessions already have file data.
	for _, sum := range summaries {
		select {
		case <-ctx.Done():
			return processed, filesExtracted, ctx.Err()
		default:
		}

		// Check if this session already has files.
		existing, getErr := s.store.GetSessionFileChanges(sum.ID)
		if getErr != nil {
			slog.Warn("skipping session for file blame", "id", sum.ID, "error", getErr)
			continue
		}
		if len(existing) > 0 {
			// Already has file data, skip.
			continue
		}

		// Load full session.
		sess, loadErr := s.store.Get(sum.ID)
		if loadErr != nil {
			slog.Warn("skipping session for file blame: load failed", "id", sum.ID, "error", loadErr)
			continue
		}

		count, extractErr := s.ExtractAndSaveFiles(sess)
		if extractErr != nil {
			slog.Warn("file extraction failed", "id", sum.ID, "error", extractErr)
			continue
		}

		processed++
		filesExtracted += count

		// Log progress every 100 sessions.
		if processed%100 == 0 {
			slog.Info("file blame backfill progress", "processed", processed, "files", filesExtracted)
		}
	}

	return processed, filesExtracted, nil
}

// GetSessionFiles returns file changes extracted from a session's tool calls.
func (s *SessionService) GetSessionFiles(ctx context.Context, sessID session.ID) ([]session.SessionFileRecord, error) {
	return s.store.GetSessionFileChanges(sessID)
}
