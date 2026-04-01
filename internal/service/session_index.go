package service

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// IndexAllSessions indexes sessions into the search engine.
// It supports incremental indexing: if the engine implements search.IncrementalIndexer,
// only sessions NOT already in the index are processed. This reduces a full re-index
// of 1,300+ sessions to indexing only the delta (typically 0-10 new sessions).
// Returns (indexed, total, error).
func (s *SessionService) IndexAllSessions(ctx context.Context) (int, int, error) {
	if s.searchEngine == nil {
		return 0, 0, nil
	}

	summaries, err := s.store.List(session.ListOptions{All: true})
	if err != nil {
		return 0, 0, err
	}

	maxContentLen := 50000
	if s.cfg != nil {
		maxContentLen = s.cfg.GetSearchMaxContentLength()
	}

	// Check if engine supports incremental indexing.
	var alreadyIndexed map[string]bool
	if inc, ok := s.searchEngine.(search.IncrementalIndexer); ok {
		alreadyIndexed, err = inc.IndexedSessionIDs()
		if err != nil {
			log.Printf("[search] incremental index lookup failed, falling back to full re-index: %v", err)
			alreadyIndexed = nil
		}
	}

	indexed := 0
	skipped := 0
	for _, sm := range summaries {
		// Skip sessions already in the index (incremental mode).
		if alreadyIndexed != nil && alreadyIndexed[string(sm.ID)] {
			skipped++
			continue
		}

		sess, getErr := s.store.Get(sm.ID)
		if getErr != nil {
			continue
		}
		doc := search.DocumentFromSession(sess, maxContentLen)
		if err := s.searchEngine.Index(ctx, doc); err != nil {
			log.Printf("[search] index error for %s: %v", sess.ID, err)
			continue
		}
		indexed++
	}

	if skipped > 0 {
		log.Printf("[search] incremental: indexed %d new, skipped %d already indexed", indexed, skipped)
	}

	return indexed, len(summaries), nil
}

// IndexSession indexes a single session into the search engine.
// Called from post-capture hook.
func (s *SessionService) IndexSession(sess *session.Session) {
	if s.searchEngine == nil || sess == nil {
		return
	}
	maxContentLen := 50000
	if s.cfg != nil {
		maxContentLen = s.cfg.GetSearchMaxContentLength()
	}
	doc := search.DocumentFromSession(sess, maxContentLen)
	if err := s.searchEngine.Index(context.Background(), doc); err != nil {
		log.Printf("[search] index error for %s: %v", sess.ID, err)
	}
}
