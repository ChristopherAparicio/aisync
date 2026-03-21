package secrets

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func TestSecretsScan_cleanSession(t *testing.T) {
	ios := iostreams.Test()

	store := testutil.NewMockStore(&session.Session{
		ID:          "sess-1",
		Provider:    session.ProviderClaudeCode,
		CreatedAt:   time.Now(),
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Hello, help me with Go code"},
			{Role: session.RoleAssistant, Content: "Sure, I can help!"},
		},
	})

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (storage.Store, error) {
			return store, nil
		},
	}

	opts := &ScanOptions{
		IO:      ios,
		Factory: f,
	}

	err := runScan(opts)
	if err != nil {
		t.Fatalf("runScan() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "clean") {
		t.Errorf("expected 'clean' in output, got: %s", output)
	}
	if !strings.Contains(output, "No secrets detected") {
		t.Errorf("expected 'No secrets detected' in output, got: %s", output)
	}
}

func TestSecretsScan_sessionWithSecrets(t *testing.T) {
	ios := iostreams.Test()

	store := testutil.NewMockStore(&session.Session{
		ID:          "sess-2",
		Provider:    session.ProviderClaudeCode,
		CreatedAt:   time.Now(),
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Use this key AKIAIOSFODNN7EXAMPLE"},
			{Role: session.RoleAssistant, Content: "I'll use the ghp_ABCDEFghijklmnop1234567890abcdef token"},
		},
	})

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (storage.Store, error) {
			return store, nil
		},
	}

	opts := &ScanOptions{
		IO:      ios,
		Factory: f,
	}

	err := runScan(opts)
	if err != nil {
		t.Fatalf("runScan() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "secret(s) found") {
		t.Errorf("expected 'secret(s) found' in output, got: %s", output)
	}
}

func TestSecretsScan_specificSession(t *testing.T) {
	ios := iostreams.Test()

	store := testutil.NewMockStore(&session.Session{
		ID:          "sess-3",
		Provider:    session.ProviderOpenCode,
		CreatedAt:   time.Now(),
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Just regular text"},
		},
	})

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (storage.Store, error) {
			return store, nil
		},
	}

	opts := &ScanOptions{
		IO:          ios,
		Factory:     f,
		SessionFlag: "sess-3",
	}

	err := runScan(opts)
	if err != nil {
		t.Fatalf("runScan() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Scanning 1 session(s)") {
		t.Errorf("expected 'Scanning 1 session(s)' in output, got: %s", output)
	}
	if !strings.Contains(output, "clean") {
		t.Errorf("expected 'clean' in output, got: %s", output)
	}
}

func TestSecretsScan_noSessions(t *testing.T) {
	ios := iostreams.Test()

	store := testutil.NewMockStore()

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (storage.Store, error) {
			return store, nil
		},
	}

	opts := &ScanOptions{
		IO:      ios,
		Factory: f,
	}

	err := runScan(opts)
	if err != nil {
		t.Fatalf("runScan() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No sessions found") {
		t.Errorf("expected 'No sessions found' in output, got: %s", output)
	}
}

func TestSecretsScan_sessionNotFound(t *testing.T) {
	ios := iostreams.Test()

	store := testutil.NewMockStore()

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (storage.Store, error) {
			return store, nil
		},
	}

	opts := &ScanOptions{
		IO:          ios,
		Factory:     f,
		SessionFlag: "nonexistent",
	}

	err := runScan(opts)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}
