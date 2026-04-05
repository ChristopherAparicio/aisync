package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Garbage Collection ──

// GCRequest contains inputs for garbage collection.
type GCRequest struct {
	OlderThan  string // duration string like "30d", "24h", "7d"
	KeepLatest int    // keep the N most recent sessions per branch (0 = no per-branch limit)
	DryRun     bool   // if true, count but don't delete
}

// GCResult contains the outcome of a garbage collection operation.
type GCResult struct {
	Deleted int  `json:"deleted"` // number of sessions deleted (0 if DryRun)
	Would   int  `json:"would"`   // number of sessions that would be deleted (only in DryRun)
	DryRun  bool `json:"dry_run"`
}

// GarbageCollect removes old sessions based on age and count policies.
// Age-based: deletes sessions older than OlderThan duration.
// Count-based: keeps only KeepLatest sessions per branch (deletes oldest first).
// Both policies can be combined — the union of sessions matching either policy is deleted.
func (s *SessionService) GarbageCollect(ctx context.Context, req GCRequest) (*GCResult, error) {
	if req.OlderThan == "" && req.KeepLatest <= 0 {
		return nil, fmt.Errorf("specify --older-than and/or --keep-latest")
	}

	result := &GCResult{DryRun: req.DryRun}

	// Age-based cleanup
	if req.OlderThan != "" {
		dur, err := parseDuration(req.OlderThan)
		if err != nil {
			return nil, fmt.Errorf("invalid duration %q: %w", req.OlderThan, err)
		}
		cutoff := time.Now().UTC().Add(-dur)

		if req.DryRun {
			// Count sessions that would be deleted (using Summary.CreatedAt to avoid N+1 Get calls).
			summaries, listErr := s.store.List(session.ListOptions{All: true})
			if listErr != nil {
				return nil, fmt.Errorf("listing sessions: %w", listErr)
			}
			for _, sm := range summaries {
				if sm.CreatedAt.Before(cutoff) {
					result.Would++
				}
			}
		} else {
			count, delErr := s.store.DeleteOlderThan(cutoff)
			if delErr != nil {
				return nil, fmt.Errorf("deleting old sessions: %w", delErr)
			}
			result.Deleted += count
		}
	}

	// Count-based cleanup (per branch)
	if req.KeepLatest > 0 {
		summaries, listErr := s.store.List(session.ListOptions{All: true})
		if listErr != nil {
			return nil, fmt.Errorf("listing sessions: %w", listErr)
		}

		// Group by branch
		perBranch := make(map[string][]session.Summary)
		for _, sm := range summaries {
			perBranch[sm.Branch] = append(perBranch[sm.Branch], sm)
		}

		// For each branch, keep only the most recent KeepLatest.
		// List() returns sessions ordered by created_at DESC, so we skip the first N.
		for _, sessions := range perBranch {
			if len(sessions) <= req.KeepLatest {
				continue
			}
			toDelete := sessions[req.KeepLatest:]
			for _, sm := range toDelete {
				if req.DryRun {
					result.Would++
				} else {
					if delErr := s.store.Delete(sm.ID); delErr == nil {
						result.Deleted++
					}
				}
			}
		}
	}

	return result, nil
}

// parseDuration parses a human-friendly duration string.
// Supports: "30d" (days), "24h" (hours), "7d12h" (days+hours).
// Falls back to time.ParseDuration for standard Go durations.
func parseDuration(s string) (time.Duration, error) {
	// Check for day notation: "Nd" or "NdMh"
	if strings.ContainsAny(s, "d") {
		var days, hours int
		parts := strings.Split(s, "d")
		if len(parts) >= 1 && parts[0] != "" {
			d, err := strconv.Atoi(parts[0])
			if err != nil {
				return 0, fmt.Errorf("invalid day count: %w", err)
			}
			days = d
		}
		if len(parts) >= 2 && parts[1] != "" {
			// Remaining part after "d", e.g. "12h"
			rem, err := time.ParseDuration(parts[1])
			if err != nil {
				return 0, fmt.Errorf("invalid remaining duration: %w", err)
			}
			hours = int(rem.Hours())
		}
		return time.Duration(days)*24*time.Hour + time.Duration(hours)*time.Hour, nil
	}

	// Fallback to standard Go duration
	return time.ParseDuration(s)
}
