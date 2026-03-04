package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestNew_DefaultValues(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	repoDir := filepath.Join(dir, "repo")

	cfg, err := New(globalDir, repoDir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Check defaults
	if got := cfg.GetStorageMode(); got != session.StorageModeCompact {
		t.Errorf("GetStorageMode() = %q, want %q", got, session.StorageModeCompact)
	}
	if got := cfg.GetSecretsMode(); got != session.SecretModeMask {
		t.Errorf("GetSecretsMode() = %q, want %q", got, session.SecretModeMask)
	}

	providers := cfg.GetProviders()
	if len(providers) != 2 {
		t.Fatalf("GetProviders() count = %d, want 2", len(providers))
	}
	if providers[0] != session.ProviderClaudeCode {
		t.Errorf("GetProviders()[0] = %q, want %q", providers[0], session.ProviderClaudeCode)
	}
	if providers[1] != session.ProviderOpenCode {
		t.Errorf("GetProviders()[1] = %q, want %q", providers[1], session.ProviderOpenCode)
	}
}

func TestSetAndGet(t *testing.T) {
	dir := t.TempDir()
	cfg, err := New(filepath.Join(dir, "global"), filepath.Join(dir, "repo"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		key   string
		value string
	}{
		{key: "storage_mode", value: "full"},
		{key: "secrets.mode", value: "block"},
		{key: "auto_capture", value: "false"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if err := cfg.Set(tt.key, tt.value); err != nil {
				t.Fatalf("Set(%q, %q) error = %v", tt.key, tt.value, err)
			}
			got, err := cfg.Get(tt.key)
			if err != nil {
				t.Fatalf("Get(%q) error = %v", tt.key, err)
			}
			if got != tt.value {
				t.Errorf("Get(%q) = %q, want %q", tt.key, got, tt.value)
			}
		})
	}
}

func TestSetStorageMode_UpdatesGetStorageMode(t *testing.T) {
	dir := t.TempDir()
	cfg, err := New(filepath.Join(dir, "global"), filepath.Join(dir, "repo"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := cfg.Set("storage_mode", "full"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if got := cfg.GetStorageMode(); got != session.StorageModeFull {
		t.Errorf("GetStorageMode() = %q, want %q", got, session.StorageModeFull)
	}
}

func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	repoDir := filepath.Join(dir, "repo")

	// Create, modify, and save config
	cfg := mustNewConfig(t, globalDir, repoDir)
	mustSet(t, cfg, "storage_mode", "summary")
	mustSave(t, cfg)

	// Reload from disk
	cfg2 := mustNewConfig(t, globalDir, repoDir)
	if got := cfg2.GetStorageMode(); got != session.StorageModeSummary {
		t.Errorf("After reload: GetStorageMode() = %q, want %q", got, session.StorageModeSummary)
	}
}

func TestRepoOverridesGlobal(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	repoDir := filepath.Join(dir, "repo")

	// Create global config with storage_mode = full
	globalCfg := mustNewConfig(t, globalDir, "")
	mustSet(t, globalCfg, "storage_mode", "full")
	mustSave(t, globalCfg)

	// Create repo config with storage_mode = summary
	mustMkdirAll(t, repoDir)
	repoCfg := mustNewConfig(t, "", repoDir)
	mustSet(t, repoCfg, "storage_mode", "summary")
	mustSave(t, repoCfg)

	// Load merged: repo should override global
	merged := mustNewConfig(t, globalDir, repoDir)
	if got := merged.GetStorageMode(); got != session.StorageModeSummary {
		t.Errorf("Merged GetStorageMode() = %q, want %q (repo should override global)", got, session.StorageModeSummary)
	}
}

func TestGet_UnknownKey(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	_, err := cfg.Get("nonexistent.key")
	if err == nil {
		t.Error("Get(nonexistent) should return an error")
	}
}

// ── Test helpers ──

func mustNewConfig(t *testing.T, globalDir, repoDir string) *Config {
	t.Helper()
	cfg, err := New(globalDir, repoDir)
	if err != nil {
		t.Fatalf("New(%q, %q) error = %v", globalDir, repoDir, err)
	}
	return cfg
}

func mustSet(t *testing.T, cfg *Config, key, value string) {
	t.Helper()
	if err := cfg.Set(key, value); err != nil {
		t.Fatalf("Set(%q, %q) error = %v", key, value, err)
	}
}

func mustSave(t *testing.T, cfg *Config) {
	t.Helper()
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
}
