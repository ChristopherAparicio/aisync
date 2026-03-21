package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Off-Topic Detection ──

// OffTopicRequest contains inputs for off-topic detection.
type OffTopicRequest struct {
	ProjectPath string  // required — limits to this project
	Branch      string  // required — the branch to analyze
	Threshold   float64 // 0.0–1.0 overlap threshold; below = off-topic (default 0.2)
}

// DetectOffTopic compares file changes across all sessions on a branch
// and flags sessions whose files don't overlap with the branch's dominant topic.
// A session is "off-topic" when the fraction of its files shared with at least
// one other session on the branch falls below the threshold.
func (s *SessionService) DetectOffTopic(ctx context.Context, req OffTopicRequest) (*session.OffTopicResult, error) {
	if req.Branch == "" {
		return nil, fmt.Errorf("branch is required for off-topic detection")
	}

	threshold := req.Threshold
	if threshold <= 0 {
		threshold = 0.2 // default: 20% overlap minimum
	}

	summaries, err := s.store.List(session.ListOptions{
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
	})
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	if len(summaries) < 2 {
		// With 0 or 1 sessions, off-topic detection is meaningless.
		entries := make([]session.OffTopicEntry, 0, len(summaries))
		for _, sm := range summaries {
			full, getErr := s.store.Get(sm.ID)
			if getErr != nil {
				continue
			}
			files := uniqueFiles(full)
			entries = append(entries, session.OffTopicEntry{
				ID:        sm.ID,
				Provider:  sm.Provider,
				Summary:   sm.Summary,
				Files:     files,
				Overlap:   1.0,
				CreatedAt: sm.CreatedAt,
			})
		}
		return &session.OffTopicResult{
			Branch:   req.Branch,
			Sessions: entries,
			Total:    len(entries),
		}, nil
	}

	// Load full sessions to access file changes.
	type sessionFiles struct {
		summary session.Summary
		files   map[string]struct{}
	}
	loaded := make([]sessionFiles, 0, len(summaries))
	for _, sm := range summaries {
		full, getErr := s.store.Get(sm.ID)
		if getErr != nil {
			continue // skip sessions that can't be loaded
		}
		fileSet := make(map[string]struct{}, len(full.FileChanges))
		for _, fc := range full.FileChanges {
			fileSet[fc.FilePath] = struct{}{}
		}
		loaded = append(loaded, sessionFiles{summary: sm, files: fileSet})
	}

	// Build global file frequency: how many sessions touch each file.
	fileFreq := make(map[string]int)
	for _, sf := range loaded {
		for f := range sf.files {
			fileFreq[f]++
		}
	}

	// Score each session: overlap = fraction of its files that appear in ≥2 sessions.
	entries := make([]session.OffTopicEntry, 0, len(loaded))
	offTopicCount := 0
	for _, sf := range loaded {
		files := sortedKeys(sf.files)
		overlap := computeOverlap(sf.files, fileFreq)
		isOff := overlap < threshold && len(sf.files) > 0
		if isOff {
			offTopicCount++
		}
		entries = append(entries, session.OffTopicEntry{
			ID:         sf.summary.ID,
			Provider:   sf.summary.Provider,
			Summary:    sf.summary.Summary,
			Files:      files,
			Overlap:    overlap,
			IsOffTopic: isOff,
			CreatedAt:  sf.summary.CreatedAt,
		})
	}

	// Top files: sorted by frequency descending, capped at 10.
	topFiles := topFilesByFrequency(fileFreq, 10)

	return &session.OffTopicResult{
		Branch:   req.Branch,
		Sessions: entries,
		TopFiles: topFiles,
		Total:    len(entries),
		OffTopic: offTopicCount,
	}, nil
}

// computeOverlap returns the fraction of files in fileSet that appear in ≥2 sessions.
// Returns 1.0 for empty file sets (sessions without files aren't off-topic).
func computeOverlap(fileSet map[string]struct{}, fileFreq map[string]int) float64 {
	if len(fileSet) == 0 {
		return 1.0
	}
	shared := 0
	for f := range fileSet {
		if fileFreq[f] >= 2 {
			shared++
		}
	}
	return float64(shared) / float64(len(fileSet))
}

// uniqueFiles returns a sorted, deduplicated list of file paths from a session.
func uniqueFiles(sess *session.Session) []string {
	seen := make(map[string]struct{}, len(sess.FileChanges))
	for _, fc := range sess.FileChanges {
		seen[fc.FilePath] = struct{}{}
	}
	return sortedKeys(seen)
}

// sortedKeys returns the sorted keys of a string set.
func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// topFilesByFrequency returns the top N files by how many sessions touch them.
func topFilesByFrequency(freq map[string]int, n int) []string {
	type entry struct {
		file  string
		count int
	}
	entries := make([]entry, 0, len(freq))
	for f, c := range freq {
		entries = append(entries, entry{f, c})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].file < entries[j].file
	})
	if len(entries) > n {
		entries = entries[:n]
	}
	result := make([]string, len(entries))
	for i, e := range entries {
		result[i] = e.file
	}
	return result
}
