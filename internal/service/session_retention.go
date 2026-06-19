package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// RetentionRequest configures a warm-tier compaction run.
type RetentionRequest struct {
	Policy config.RetentionPolicy
	Now    time.Time
	Limit  int
	DryRun bool
}

// RetentionResult reports a warm-tier compaction run outcome.
type RetentionResult struct {
	Cutoff           time.Time `json:"cutoff"`
	ColdCutoff       time.Time `json:"cold_cutoff,omitempty"`
	Scanned          int       `json:"scanned"`
	Candidates       int       `json:"candidates"`
	ColdCandidates   int       `json:"cold_candidates"`
	Compacted        int       `json:"compacted"`
	Archived         int       `json:"archived"`
	Skipped          int       `json:"skipped"`
	Errors           int       `json:"errors"`
	BytesBefore      int       `json:"bytes_before"`
	BytesAfter       int       `json:"bytes_after"`
	RetainedMessages int       `json:"retained_messages"`
	OriginalMessages int       `json:"original_messages"`
	MaxTokens        int       `json:"max_tokens"`
	DryRun           bool      `json:"dry_run"`
}

type retentionStore interface {
	ListWarmCompactionCandidates(cutoff time.Time, limit int) ([]session.Summary, error)
	ListColdCompactionCandidates(cutoff time.Time, limit int) ([]session.Summary, error)
	UpdateSessionRetention(sess *session.Session) error
}

type retentionStatsStore interface {
	RetentionStorageStats() ([]session.RetentionTierStorageStats, error)
}

// RetentionStats reports session payload storage grouped by retention tier.
type RetentionStats struct {
	ByTier        map[session.RetentionTier]session.RetentionTierStorageStats `json:"by_tier"`
	TotalSessions int                                                         `json:"total_sessions"`
	TotalBytes    int                                                         `json:"total_bytes"`
}

type accessStore interface {
	TouchSessionAccessed(id session.ID, at time.Time) error
}

// CompactIdleSessions applies destructive warm-tier retention to idle sessions.
func (s *SessionService) CompactIdleSessions(ctx context.Context, req RetentionRequest) (*RetentionResult, error) {
	policy := s.resolveRetentionPolicy(req.Policy)
	result := &RetentionResult{DryRun: req.DryRun, MaxTokens: policy.MaxTokens}
	if !policy.Enabled {
		return result, nil
	}

	store, ok := s.store.(retentionStore)
	if !ok {
		return nil, fmt.Errorf("store does not support retention compaction")
	}

	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	idleFor, err := parseDuration(policy.CompactAfterIdle)
	if err != nil {
		return nil, fmt.Errorf("invalid retention compact_after_idle %q: %w", policy.CompactAfterIdle, err)
	}
	cutoff := now.Add(-idleFor)
	result.Cutoff = cutoff

	candidates, err := store.ListWarmCompactionCandidates(cutoff, req.Limit)
	if err != nil {
		return nil, err
	}

	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Scanned++
		if !retentionInactive(candidate, cutoff) {
			result.Skipped++
			continue
		}
		result.Candidates++
		if req.DryRun {
			continue
		}

		sess, getErr := s.store.Get(candidate.ID)
		if getErr != nil {
			result.Errors++
			continue
		}
		if session.NormalizeRetentionTier(sess.RetentionTier) != session.RetentionTierHot || !sess.CompactedAt.IsZero() {
			result.Skipped++
			continue
		}

		beforeBytes := jsonSize(sess)
		stats := session.ApplyWindowRetention(sess, session.WindowRetentionPolicy{
			MaxTokens:        policy.MaxTokens,
			KeepUserMessages: policy.KeepUserMessages,
			KeepCompactions:  policy.KeepCompactions,
			KeepToolOutputs:  policy.KeepToolOutputs,
			KeepThinking:     policy.KeepThinking,
		}, now)
		afterBytes := jsonSize(sess)

		if updateErr := store.UpdateSessionRetention(sess); updateErr != nil {
			result.Errors++
			continue
		}
		result.Compacted++
		result.BytesBefore += beforeBytes
		result.BytesAfter += afterBytes
		result.OriginalMessages += stats.OriginalMessages
		result.RetainedMessages += stats.RetainedMessages
	}

	if policy.ColdEnabled {
		if coldErr := s.archiveColdSessions(ctx, store, policy, req, now, result); coldErr != nil {
			return result, coldErr
		}
	}

	return result, nil
}

// RetentionStats returns compressed payload bytes grouped by retention tier.
func (s *SessionService) RetentionStats(_ context.Context) (*RetentionStats, error) {
	store, ok := s.store.(retentionStatsStore)
	if !ok {
		return nil, fmt.Errorf("store does not support retention stats")
	}
	entries, err := store.RetentionStorageStats()
	if err != nil {
		return nil, err
	}
	stats := &RetentionStats{ByTier: make(map[session.RetentionTier]session.RetentionTierStorageStats)}
	for _, entry := range entries {
		entry.Tier = session.NormalizeRetentionTier(entry.Tier)
		stats.ByTier[entry.Tier] = entry
		stats.TotalSessions += entry.Sessions
		stats.TotalBytes += entry.Bytes
	}
	return stats, nil
}

func (s *SessionService) archiveColdSessions(ctx context.Context, store retentionStore, policy config.RetentionPolicy, req RetentionRequest, now time.Time, result *RetentionResult) error {
	idleFor, err := parseDuration(policy.ArchiveAfterIdle)
	if err != nil {
		return fmt.Errorf("invalid retention archive_after_idle %q: %w", policy.ArchiveAfterIdle, err)
	}
	cutoff := now.Add(-idleFor)
	result.ColdCutoff = cutoff

	candidates, err := store.ListColdCompactionCandidates(cutoff, req.Limit)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result.Scanned++
		if !retentionInactive(candidate, cutoff) {
			result.Skipped++
			continue
		}
		result.ColdCandidates++
		if req.DryRun {
			continue
		}
		sess, getErr := s.store.Get(candidate.ID)
		if getErr != nil {
			result.Errors++
			continue
		}
		if !coldArchivable(sess) {
			result.Skipped++
			continue
		}
		beforeBytes := jsonSize(sess)
		sess.Messages = nil
		sess.StorageMode = session.StorageModeSummary
		sess.RetentionTier = session.RetentionTierCold
		sess.RetentionFidelity = session.RetentionFidelitySummary
		sess.CompactedAt = now
		afterBytes := jsonSize(sess)
		if updateErr := store.UpdateSessionRetention(sess); updateErr != nil {
			result.Errors++
			continue
		}
		result.Archived++
		result.BytesBefore += beforeBytes
		result.BytesAfter += afterBytes
	}
	return nil
}

func (s *SessionService) resolveRetentionPolicy(policy config.RetentionPolicy) config.RetentionPolicy {
	if policy.CompactAfterIdle == "" && policy.MaxTokens == 0 && s.cfg != nil {
		policy = s.cfg.GetRetentionPolicy()
	}
	if policy.CompactAfterIdle == "" {
		policy.CompactAfterIdle = "48h"
	}
	if policy.MaxTokens <= 0 {
		policy.MaxTokens = 300000
	}
	if policy.ArchiveAfterIdle == "" {
		policy.ArchiveAfterIdle = "720h"
	}
	if !policy.KeepUserMessages && !policy.KeepCompactions && !policy.KeepToolOutputs && !policy.KeepThinking {
		policy.KeepUserMessages = true
		policy.KeepCompactions = true
	}
	return policy
}

func coldArchivable(sess *session.Session) bool {
	if sess == nil {
		return false
	}
	if session.NormalizeRetentionTier(sess.RetentionTier) != session.RetentionTierWarm {
		return false
	}
	if sess.Summary == "" || sess.TokenUsage.TotalTokens == 0 {
		return false
	}
	return true
}

func (s *SessionService) touchSessionAccessed(id session.ID) {
	store, ok := s.store.(accessStore)
	if !ok || id == "" {
		return
	}
	_ = store.TouchSessionAccessed(id, time.Now().UTC())
}

func retentionInactive(sm session.Summary, cutoff time.Time) bool {
	lastActivity := sm.CreatedAt
	if sm.UpdatedAt.After(lastActivity) {
		lastActivity = sm.UpdatedAt
	}
	if sm.LastAccessedAt.After(lastActivity) {
		lastActivity = sm.LastAccessedAt
	}
	return !lastActivity.IsZero() && !lastActivity.After(cutoff)
}

func jsonSize(sess *session.Session) int {
	data, err := json.Marshal(sess)
	if err != nil {
		return 0
	}
	return len(data)
}

func retentionFidelity(sess *session.Session) session.RetentionFidelity {
	if sess == nil {
		return ""
	}
	return session.NormalizeRetentionFidelity(sess.RetentionFidelity, sess.StorageMode)
}

func retentionWarning(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	switch session.NormalizeRetentionTier(sess.RetentionTier) {
	case session.RetentionTierWarm:
		return "session compacted to warm windowed tier; restore uses retained context only"
	case session.RetentionTierCold:
		return "session compacted to cold tier; restore fidelity is reduced"
	default:
		return ""
	}
}
