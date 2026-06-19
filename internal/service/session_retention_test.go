package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
)

func TestCompactIdleSessionsCompactsOnlyInactiveHotSessions(t *testing.T) {
	store, err := sqlite.New(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("sqlite.New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	old := retentionTestSession("old", now.Add(-72*time.Hour), now.Add(-72*time.Hour))
	recentAccess := retentionTestSession("recent", now.Add(-72*time.Hour), now.Add(-time.Hour))
	for _, sess := range []*session.Session{old, recentAccess} {
		if saveErr := store.Save(sess); saveErr != nil {
			t.Fatalf("Save(%s) error = %v", sess.ID, saveErr)
		}
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})
	result, err := svc.CompactIdleSessions(context.Background(), RetentionRequest{
		Now: now,
		Policy: config.RetentionPolicy{
			Enabled:          true,
			CompactAfterIdle: "48h",
			MaxTokens:        150,
			KeepUserMessages: true,
			KeepCompactions:  true,
		},
	})
	if err != nil {
		t.Fatalf("CompactIdleSessions() error = %v", err)
	}
	if result.Compacted != 1 || result.Skipped != 1 {
		t.Fatalf("result = %+v, want compacted=1 skipped=1", result)
	}

	gotOld, err := store.Get(old.ID)
	if err != nil {
		t.Fatalf("Get(old) error = %v", err)
	}
	if gotOld.RetentionTier != session.RetentionTierWarm || gotOld.RetentionFidelity != session.RetentionFidelityWindowed {
		t.Fatalf("old retention = %s/%s, want warm/windowed", gotOld.RetentionTier, gotOld.RetentionFidelity)
	}
	if len(gotOld.Messages) != 3 {
		t.Fatalf("old retained messages = %d, want 3", len(gotOld.Messages))
	}

	gotRecent, err := store.Get(recentAccess.ID)
	if err != nil {
		t.Fatalf("Get(recent) error = %v", err)
	}
	if gotRecent.RetentionTier != session.RetentionTierHot {
		t.Fatalf("recent tier = %s, want hot", gotRecent.RetentionTier)
	}
}

func TestCompactIdleSessionsDryRunDoesNotMutateCandidates(t *testing.T) {
	store, err := sqlite.New(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("sqlite.New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	sess := retentionTestSession("dry-run", now.Add(-72*time.Hour), now.Add(-72*time.Hour))
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})
	result, err := svc.CompactIdleSessions(context.Background(), RetentionRequest{
		Now:    now,
		DryRun: true,
		Policy: config.RetentionPolicy{
			Enabled:          true,
			CompactAfterIdle: "48h",
			MaxTokens:        150,
			KeepUserMessages: true,
			KeepCompactions:  true,
		},
	})
	if err != nil {
		t.Fatalf("CompactIdleSessions() error = %v", err)
	}
	if result.Candidates != 1 || result.Compacted != 0 || !result.DryRun {
		t.Fatalf("result = %+v, want dry-run candidates=1 compacted=0", result)
	}

	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.RetentionTier != session.RetentionTierHot || got.RetentionFidelity != session.RetentionFidelityFull {
		t.Fatalf("retention = %s/%s, want unchanged hot/full", got.RetentionTier, got.RetentionFidelity)
	}
}

func TestCompactIdleSessionsArchivesWarmSessionsToColdWhenEnabled(t *testing.T) {
	store, err := sqlite.New(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("sqlite.New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	sess := retentionTestSession("warm-old", now.Add(-60*24*time.Hour), now.Add(-60*24*time.Hour))
	sess.Summary = "audit summary"
	sess.RetentionTier = session.RetentionTierWarm
	sess.RetentionFidelity = session.RetentionFidelityWindowed
	sess.CompactedAt = now.Add(-45 * 24 * time.Hour)
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})
	result, err := svc.CompactIdleSessions(context.Background(), RetentionRequest{
		Now: now,
		Policy: config.RetentionPolicy{
			Enabled:          true,
			CompactAfterIdle: "48h",
			MaxTokens:        150,
			KeepUserMessages: true,
			KeepCompactions:  true,
			ColdEnabled:      true,
			ArchiveAfterIdle: "30d",
		},
	})
	if err != nil {
		t.Fatalf("CompactIdleSessions() error = %v", err)
	}
	if result.Archived != 1 || result.ColdCandidates != 1 {
		t.Fatalf("result = %+v, want archived=1 cold_candidates=1", result)
	}

	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.RetentionTier != session.RetentionTierCold || got.RetentionFidelity != session.RetentionFidelitySummary || got.StorageMode != session.StorageModeSummary {
		t.Fatalf("retention = %s/%s/%s, want cold/summary/summary", got.RetentionTier, got.RetentionFidelity, got.StorageMode)
	}
	if len(got.Messages) != 0 {
		t.Fatalf("cold messages = %d, want 0", len(got.Messages))
	}
	if got.Summary != "audit summary" || got.TokenUsage.TotalTokens != 1000 {
		t.Fatalf("cold audit fields not preserved: summary=%q tokens=%d", got.Summary, got.TokenUsage.TotalTokens)
	}
}

func TestRetentionStatsReportsPayloadBytesByTier(t *testing.T) {
	store, err := sqlite.New(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("sqlite.New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	hot := retentionTestSession("hot", now, now)
	warm := retentionTestSession("warm", now, now)
	warm.RetentionTier = session.RetentionTierWarm
	warm.RetentionFidelity = session.RetentionFidelityWindowed
	for _, sess := range []*session.Session{hot, warm} {
		if err := store.Save(sess); err != nil {
			t.Fatalf("Save(%s) error = %v", sess.ID, err)
		}
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})
	stats, err := svc.RetentionStats(context.Background())
	if err != nil {
		t.Fatalf("RetentionStats() error = %v", err)
	}
	if stats.TotalSessions != 2 || stats.TotalBytes <= 0 {
		t.Fatalf("stats = %+v, want two sessions and payload bytes", stats)
	}
	if stats.ByTier[session.RetentionTierHot].Sessions != 1 || stats.ByTier[session.RetentionTierWarm].Sessions != 1 {
		t.Fatalf("stats by tier = %+v, want hot=1 warm=1", stats.ByTier)
	}
}

func retentionTestSession(id string, createdAt time.Time, lastAccessedAt time.Time) *session.Session {
	return &session.Session{
		ID:              session.ID(id),
		Provider:        session.ProviderClaudeCode,
		Agent:           "claude",
		ProjectPath:     "/tmp/project",
		Branch:          "main",
		CreatedAt:       createdAt,
		ExportedAt:      createdAt,
		SourceUpdatedAt: createdAt.UnixMilli(),
		LastAccessedAt:  lastAccessedAt,
		StorageMode:     session.StorageModeFull,
		TokenUsage:      session.TokenUsage{TotalTokens: 1000},
		Messages: []session.Message{
			{ID: "user-old", Role: session.RoleUser, Content: "old ask"},
			{ID: "assistant-old", Role: session.RoleAssistant, Content: "old answer", InputTokens: 500},
			{ID: "compact", Role: session.RoleAssistant, Content: "summary", IsCompactionSummary: true},
			{ID: "assistant-tail", Role: session.RoleAssistant, Content: "recent answer", InputTokens: 120},
		},
	}
}
