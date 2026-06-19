package retentioncmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func TestRetentionCompactDryRunCommandReportsWithoutMutation(t *testing.T) {
	store, err := sqlite.New(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("sqlite.New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC().Add(-72 * time.Hour)
	sess := retentionCmdTestSession("cmd-dry-run", now)
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	ios := iostreams.Test()
	cfg, err := config.New(filepath.Join(t.TempDir(), "global"), filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatalf("config.New() error = %v", err)
	}
	mustSetRetention(t, cfg, "retention.enabled", "true")
	mustSetRetention(t, cfg, "retention.compact_after_idle", "48h")
	f := &cmdutil.Factory{
		IOStreams: ios,
		ConfigFunc: func() (*config.Config, error) {
			return cfg, nil
		},
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{Store: store, Config: cfg}), nil
		},
	}

	cmd := NewCmdRetention(f)
	cmd.SetArgs([]string{"compact", "--dry-run"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "dry-run") || !strings.Contains(output, "Candidates: 1") {
		t.Fatalf("output = %q, want dry-run candidates", output)
	}
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.RetentionTier != session.RetentionTierHot {
		t.Fatalf("RetentionTier = %s, want hot after dry-run", got.RetentionTier)
	}
}

func TestRetentionStatsCommandReportsTiers(t *testing.T) {
	store, err := sqlite.New(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("sqlite.New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sess := retentionCmdTestSession("stats-warm", time.Now().UTC())
	sess.RetentionTier = session.RetentionTierWarm
	sess.RetentionFidelity = session.RetentionFidelityWindowed
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{Store: store}), nil
		},
	}
	cmd := NewCmdRetention(f)
	cmd.SetArgs([]string{"stats"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "warm") || !strings.Contains(output, "1") {
		t.Fatalf("output = %q, want warm tier count", output)
	}
}

func retentionCmdTestSession(id string, createdAt time.Time) *session.Session {
	return &session.Session{
		ID:              session.ID(id),
		Provider:        session.ProviderClaudeCode,
		Agent:           "claude",
		ProjectPath:     "/tmp/project",
		Branch:          "main",
		Summary:         "retention command fixture",
		CreatedAt:       createdAt,
		ExportedAt:      createdAt,
		SourceUpdatedAt: createdAt.UnixMilli(),
		LastAccessedAt:  createdAt,
		StorageMode:     session.StorageModeFull,
		TokenUsage:      session.TokenUsage{TotalTokens: 1000},
		Messages: []session.Message{
			{ID: "user", Role: session.RoleUser, Content: "question"},
			{ID: "assistant", Role: session.RoleAssistant, Content: "answer", InputTokens: 100},
		},
	}
}

func mustSetRetention(t *testing.T, cfg *config.Config, key, value string) {
	t.Helper()
	if err := cfg.Set(key, value); err != nil {
		t.Fatalf("Set(%q, %q) error = %v", key, value, err)
	}
}
