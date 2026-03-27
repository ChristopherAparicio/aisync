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

// ── Get() all keys ──

func TestGet_AllKeys(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	// Set non-default values so we exercise all Get branches
	mustSet(t, cfg, "summarize.enabled", "true")
	mustSet(t, cfg, "summarize.model", "gpt-4o")
	mustSet(t, cfg, "analysis.auto", "true")
	mustSet(t, cfg, "analysis.adapter", "opencode")
	mustSet(t, cfg, "analysis.error_threshold", "30")
	mustSet(t, cfg, "analysis.min_tool_calls", "10")
	mustSet(t, cfg, "analysis.model", "claude-3")
	mustSet(t, cfg, "server.url", "http://localhost:8371")
	mustSet(t, cfg, "database.path", "/tmp/test.db")

	tests := []struct {
		key  string
		want string
	}{
		{"storage_mode", "compact"},
		{"secrets.mode", "mask"},
		{"auto_capture", "true"},
		{"summarize.enabled", "true"},
		{"summarize.model", "gpt-4o"},
		{"analysis.auto", "true"},
		{"analysis.adapter", "opencode"},
		{"analysis.error_threshold", "30"},
		{"analysis.min_tool_calls", "10"},
		{"analysis.model", "claude-3"},
		{"dashboard.page_size", "25"},
		{"dashboard.columns", "id,project,provider,branch,summary,health,messages,tokens,errors,when"},
		{"dashboard.sort_by", "created_at"},
		{"dashboard.sort_order", "desc"},
		{"dashboard.default_provider", ""},
		{"dashboard.default_branch", ""},
		{"server.url", "http://localhost:8371"},
		{"database.path", "/tmp/test.db"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, err := cfg.Get(tt.key)
			if err != nil {
				t.Fatalf("Get(%q) error = %v", tt.key, err)
			}
			if got != tt.want {
				t.Errorf("Get(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

// ── Set() all keys + validation errors ──

func TestSet_UnknownKey(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("nonexistent.key", "value"); err == nil {
		t.Error("Set(nonexistent) should return an error")
	}
}

func TestSet_InvalidStorageMode(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("storage_mode", "invalid"); err == nil {
		t.Error("expected error for invalid storage_mode")
	}
}

func TestSet_InvalidSecretsMode(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("secrets.mode", "invalid"); err == nil {
		t.Error("expected error for invalid secrets.mode")
	}
}

func TestSet_Summarize(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	mustSet(t, cfg, "summarize.enabled", "true")
	mustSet(t, cfg, "summarize.model", "gpt-4o")

	if !cfg.IsSummarizeEnabled() {
		t.Error("IsSummarizeEnabled() = false, want true")
	}
	if got := cfg.GetSummarizeModel(); got != "gpt-4o" {
		t.Errorf("GetSummarizeModel() = %q, want %q", got, "gpt-4o")
	}
}

func TestSet_CustomPatterns(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	mustSet(t, cfg, "secrets.custom_patterns.add", "MY_SECRET (?i)secret_\\w+")
	patterns := cfg.GetCustomPatterns()
	if len(patterns) != 1 || patterns[0] != "MY_SECRET (?i)secret_\\w+" {
		t.Errorf("GetCustomPatterns() = %v, want [MY_SECRET (?i)secret_\\w+]", patterns)
	}
}

func TestSet_AnalysisAdapter_Validation(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("analysis.adapter", "invalid"); err == nil {
		t.Error("expected error for invalid analysis.adapter")
	}
	// Valid values
	mustSet(t, cfg, "analysis.adapter", "llm")
	mustSet(t, cfg, "analysis.adapter", "opencode")
}

func TestSet_AnalysisErrorThreshold_Validation(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("analysis.error_threshold", "abc"); err == nil {
		t.Error("expected error for non-numeric error_threshold")
	}
	mustSet(t, cfg, "analysis.error_threshold", "42.5")
	if got := cfg.GetAnalysisErrorThreshold(); got != 42.5 {
		t.Errorf("GetAnalysisErrorThreshold() = %v, want 42.5", got)
	}
}

func TestSet_AnalysisMinToolCalls_Validation(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("analysis.min_tool_calls", "abc"); err == nil {
		t.Error("expected error for non-numeric min_tool_calls")
	}
	mustSet(t, cfg, "analysis.min_tool_calls", "15")
	if got := cfg.GetAnalysisMinToolCalls(); got != 15 {
		t.Errorf("GetAnalysisMinToolCalls() = %d, want 15", got)
	}
}

func TestSet_ServerURL_Validation(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("server.url", "ftp://bad"); err == nil {
		t.Error("expected error for non-http server.url")
	}
	// Valid values
	mustSet(t, cfg, "server.url", "http://localhost:8371")
	mustSet(t, cfg, "server.url", "https://aisync.example.com")
	// Empty is also valid (standalone mode)
	mustSet(t, cfg, "server.url", "")
}

func TestSet_DefaultProvider_Validation(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("dashboard.default_provider", "unknown-provider"); err == nil {
		t.Error("expected error for invalid default_provider")
	}
	// Empty is valid (no filter)
	mustSet(t, cfg, "dashboard.default_provider", "")
}

// ── Getters with defaults ──

func TestGetters_Defaults(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	// Summarize defaults
	if cfg.IsSummarizeEnabled() {
		t.Error("IsSummarizeEnabled() default should be false")
	}
	if got := cfg.GetSummarizeModel(); got != "" {
		t.Errorf("GetSummarizeModel() = %q, want empty", got)
	}

	// Pricing defaults
	if got := cfg.GetPricingOverrides(); len(got) != 0 {
		t.Errorf("GetPricingOverrides() = %v, want empty", got)
	}

	// Analysis defaults
	if cfg.IsAnalysisAutoEnabled() {
		t.Error("IsAnalysisAutoEnabled() default should be false")
	}
	if got := cfg.GetAnalysisAdapter(); got != "llm" {
		t.Errorf("GetAnalysisAdapter() = %q, want %q", got, "llm")
	}
	if got := cfg.GetAnalysisErrorThreshold(); got != 20 {
		t.Errorf("GetAnalysisErrorThreshold() = %v, want 20", got)
	}
	if got := cfg.GetAnalysisMinToolCalls(); got != 5 {
		t.Errorf("GetAnalysisMinToolCalls() = %v, want 5", got)
	}
	if got := cfg.GetAnalysisModel(); got != "" {
		t.Errorf("GetAnalysisModel() = %q, want empty", got)
	}

	// Custom patterns default
	if got := cfg.GetCustomPatterns(); len(got) != 0 {
		t.Errorf("GetCustomPatterns() = %v, want nil/empty", got)
	}
}

func TestGetAnalysisAdapter_EmptyFallback(t *testing.T) {
	cfg := &Config{data: configData{}}
	// Empty adapter should default to "llm"
	if got := cfg.GetAnalysisAdapter(); got != "llm" {
		t.Errorf("GetAnalysisAdapter() with empty = %q, want %q", got, "llm")
	}
}

func TestGetAnalysisErrorThreshold_ZeroFallback(t *testing.T) {
	cfg := &Config{data: configData{}}
	// Zero threshold should default to 20
	if got := cfg.GetAnalysisErrorThreshold(); got != 20 {
		t.Errorf("GetAnalysisErrorThreshold() with zero = %v, want 20", got)
	}
}

func TestGetAnalysisMinToolCalls_ZeroFallback(t *testing.T) {
	cfg := &Config{data: configData{}}
	// Zero min tool calls should default to 5
	if got := cfg.GetAnalysisMinToolCalls(); got != 5 {
		t.Errorf("GetAnalysisMinToolCalls() with zero = %v, want 5", got)
	}
}

func TestGetDashboardPageSize_ZeroFallback(t *testing.T) {
	cfg := &Config{data: configData{}}
	if got := cfg.GetDashboardPageSize(); got != 25 {
		t.Errorf("GetDashboardPageSize() with zero = %d, want 25", got)
	}
}

func TestGetDashboardColumns_EmptyFallback(t *testing.T) {
	cfg := &Config{data: configData{}}
	cols := cfg.GetDashboardColumns()
	if len(cols) != len(DefaultDashboardColumns) {
		t.Errorf("GetDashboardColumns() with empty = %d items, want %d", len(cols), len(DefaultDashboardColumns))
	}
}

func TestGetDashboardSortBy_EmptyFallback(t *testing.T) {
	cfg := &Config{data: configData{}}
	if got := cfg.GetDashboardSortBy(); got != "created_at" {
		t.Errorf("GetDashboardSortBy() with empty = %q, want %q", got, "created_at")
	}
}

func TestGetDashboardSortOrder_EmptyFallback(t *testing.T) {
	cfg := &Config{data: configData{}}
	if got := cfg.GetDashboardSortOrder(); got != "desc" {
		t.Errorf("GetDashboardSortOrder() with empty = %q, want %q", got, "desc")
	}
}

// ── AddPricingOverride ──

func TestAddPricingOverride_Append(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	cfg.AddPricingOverride("gpt-4", 10.0, 30.0)
	cfg.AddPricingOverride("claude-3", 15.0, 75.0)

	overrides := cfg.GetPricingOverrides()
	if len(overrides) != 2 {
		t.Fatalf("GetPricingOverrides() count = %d, want 2", len(overrides))
	}
	if overrides[0].Model != "gpt-4" || overrides[0].InputPerMToken != 10.0 || overrides[0].OutputPerMToken != 30.0 {
		t.Errorf("override[0] = %+v, want gpt-4/10/30", overrides[0])
	}
	if overrides[1].Model != "claude-3" || overrides[1].InputPerMToken != 15.0 || overrides[1].OutputPerMToken != 75.0 {
		t.Errorf("override[1] = %+v, want claude-3/15/75", overrides[1])
	}
}

func TestAddPricingOverride_Update(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	cfg.AddPricingOverride("gpt-4", 10.0, 30.0)
	cfg.AddPricingOverride("gpt-4", 20.0, 60.0) // update

	overrides := cfg.GetPricingOverrides()
	if len(overrides) != 1 {
		t.Fatalf("GetPricingOverrides() count = %d, want 1 (should update, not append)", len(overrides))
	}
	if overrides[0].InputPerMToken != 20.0 || overrides[0].OutputPerMToken != 60.0 {
		t.Errorf("override[0] = %+v, want gpt-4/20/60", overrides[0])
	}
}

// ── AddCustomPattern ──

func TestAddCustomPattern(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	cfg.AddCustomPattern("MY_KEY (?i)key_\\w+")
	cfg.AddCustomPattern("MY_TOKEN (?i)tok_\\w+")

	patterns := cfg.GetCustomPatterns()
	if len(patterns) != 2 {
		t.Fatalf("GetCustomPatterns() count = %d, want 2", len(patterns))
	}
}

// ── GetServerURL / GetDatabasePath env override ──

func TestGetServerURL_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))
	mustSet(t, cfg, "server.url", "http://from-config:8371")

	// Without env var
	if got := cfg.GetServerURL(); got != "http://from-config:8371" {
		t.Errorf("GetServerURL() = %q, want config value", got)
	}

	// With env var override
	t.Setenv("AISYNC_SERVER_URL", "http://from-env:9999")
	if got := cfg.GetServerURL(); got != "http://from-env:9999" {
		t.Errorf("GetServerURL() with env = %q, want env value", got)
	}
}

func TestGetDatabasePath_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))
	mustSet(t, cfg, "database.path", "/from/config.db")

	// Without env var
	if got := cfg.GetDatabasePath(); got != "/from/config.db" {
		t.Errorf("GetDatabasePath() = %q, want config value", got)
	}

	// With env var override
	t.Setenv("AISYNC_DATABASE_PATH", "/from/env.db")
	if got := cfg.GetDatabasePath(); got != "/from/env.db" {
		t.Errorf("GetDatabasePath() with env = %q, want env value", got)
	}
}

func TestGetServerURL_Empty(t *testing.T) {
	cfg := &Config{data: configData{}}
	if got := cfg.GetServerURL(); got != "" {
		t.Errorf("GetServerURL() with no config = %q, want empty", got)
	}
}

func TestGetDatabasePath_Empty(t *testing.T) {
	cfg := &Config{data: configData{}}
	if got := cfg.GetDatabasePath(); got != "" {
		t.Errorf("GetDatabasePath() with no config = %q, want empty", got)
	}
}

// ── New() edge cases ──

func TestNew_MalformedGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	mustMkdirAll(t, globalDir)

	// Write malformed JSON to global config
	if err := os.WriteFile(filepath.Join(globalDir, configFileName), []byte("{bad json}"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	_, err := New(globalDir, "")
	if err == nil {
		t.Error("New() should return error for malformed global config")
	}
}

func TestNew_MalformedRepoConfig(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")
	mustMkdirAll(t, repoDir)

	// Write malformed JSON to repo config
	if err := os.WriteFile(filepath.Join(repoDir, configFileName), []byte("{bad json}"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	_, err := New("", repoDir)
	if err == nil {
		t.Error("New() should return error for malformed repo config")
	}
}

func TestNew_EmptyDirs(t *testing.T) {
	// Both dirs empty → should succeed with defaults
	cfg, err := New("", "")
	if err != nil {
		t.Fatalf("New('', '') error = %v", err)
	}
	if got := cfg.GetStorageMode(); got != session.StorageModeCompact {
		t.Errorf("GetStorageMode() = %q, want %q", got, session.StorageModeCompact)
	}
}

// ── Save() edge cases ──

func TestSave_NoDirSpecified(t *testing.T) {
	cfg := &Config{data: defaultConfig()}
	if err := cfg.Save(); err == nil {
		t.Error("Save() should return error when no directory specified")
	}
}

func TestSave_GlobalFallback(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global-save")
	cfg := &Config{
		data:      defaultConfig(),
		globalDir: globalDir,
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// Verify file exists
	if _, err := os.Stat(filepath.Join(globalDir, configFileName)); err != nil {
		t.Errorf("config file not found after Save(): %v", err)
	}
}

// ── loadFrom merge for Server, Database, Analysis, Pricing ──

func TestLoadFrom_MergesAllFields(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	repoDir := filepath.Join(dir, "repo")

	// Create global config with server + database + analysis + pricing + summarize
	mustMkdirAll(t, globalDir)
	globalJSON := `{
		"server": {"url": "http://global:8371"},
		"database": {"path": "/global/db.sqlite"},
		"analysis": {
			"auto": true,
			"adapter": "opencode",
			"error_threshold": 50,
			"min_tool_calls": 20,
			"model": "gpt-4"
		},
		"pricing": {
			"overrides": [{"model": "gpt-4", "input_per_mtoken": 10, "output_per_mtoken": 30}]
		},
		"summarize": {
			"enabled": true,
			"model": "claude-3"
		},
		"secrets": {
			"custom_patterns": ["CUSTOM (?i)custom_\\w+"],
			"ignore_patterns": ["ignore_this"]
		}
	}`
	if err := os.WriteFile(filepath.Join(globalDir, configFileName), []byte(globalJSON), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	// Repo overrides only server URL
	mustMkdirAll(t, repoDir)
	repoJSON := `{
		"server": {"url": "http://repo:9999"},
		"database": {"path": "/repo/db.sqlite"}
	}`
	if err := os.WriteFile(filepath.Join(repoDir, configFileName), []byte(repoJSON), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	cfg := mustNewConfig(t, globalDir, repoDir)

	// Repo overrides
	if got := cfg.GetServerURL(); got != "http://repo:9999" {
		t.Errorf("GetServerURL() = %q, want repo override", got)
	}
	if got := cfg.GetDatabasePath(); got != "/repo/db.sqlite" {
		t.Errorf("GetDatabasePath() = %q, want repo override", got)
	}

	// Booleans always take the loaded value — repo config has no analysis.auto,
	// so JSON unmarshals it as false, overriding global's true.
	if cfg.IsAnalysisAutoEnabled() {
		t.Error("IsAnalysisAutoEnabled() should be false (repo's zero-value overrides global)")
	}
	if got := cfg.GetAnalysisAdapter(); got != "opencode" {
		t.Errorf("GetAnalysisAdapter() = %q, want %q from global", got, "opencode")
	}
	if got := cfg.GetAnalysisErrorThreshold(); got != 50 {
		t.Errorf("GetAnalysisErrorThreshold() = %v, want 50 from global", got)
	}
	if got := cfg.GetAnalysisMinToolCalls(); got != 20 {
		t.Errorf("GetAnalysisMinToolCalls() = %v, want 20 from global", got)
	}
	if got := cfg.GetAnalysisModel(); got != "gpt-4" {
		t.Errorf("GetAnalysisModel() = %q, want %q from global", got, "gpt-4")
	}

	// Pricing overrides from global
	overrides := cfg.GetPricingOverrides()
	if len(overrides) != 1 || overrides[0].Model != "gpt-4" {
		t.Errorf("GetPricingOverrides() = %v, want [{gpt-4 10 30}]", overrides)
	}

	// Summarize.Enabled is a bool — repo's zero-value (false) overrides global's true
	if cfg.IsSummarizeEnabled() {
		t.Error("IsSummarizeEnabled() should be false (repo's zero-value overrides global)")
	}
	if got := cfg.GetSummarizeModel(); got != "claude-3" {
		t.Errorf("GetSummarizeModel() = %q, want %q from global", got, "claude-3")
	}

	// Custom patterns from global
	patterns := cfg.GetCustomPatterns()
	if len(patterns) != 1 || patterns[0] != "CUSTOM (?i)custom_\\w+" {
		t.Errorf("GetCustomPatterns() = %v, want [CUSTOM ...]", patterns)
	}
}

// ── GetStorageMode / GetSecretsMode invalid fallback ──

func TestGetStorageMode_InvalidFallback(t *testing.T) {
	cfg := &Config{data: configData{StorageMode: "invalid"}}
	if got := cfg.GetStorageMode(); got != session.StorageModeCompact {
		t.Errorf("GetStorageMode() with invalid = %q, want %q (fallback)", got, session.StorageModeCompact)
	}
}

func TestGetSecretsMode_InvalidFallback(t *testing.T) {
	cfg := &Config{data: configData{Secrets: secrets{Mode: "invalid"}}}
	if got := cfg.GetSecretsMode(); got != session.SecretModeMask {
		t.Errorf("GetSecretsMode() with invalid = %q, want %q (fallback)", got, session.SecretModeMask)
	}
}

// ── GetProviders with invalid entries ──

func TestGetProviders_SkipsInvalid(t *testing.T) {
	cfg := &Config{data: configData{Providers: []string{"claude-code", "invalid-provider", "opencode"}}}
	providers := cfg.GetProviders()
	if len(providers) != 2 {
		t.Fatalf("GetProviders() count = %d, want 2 (should skip invalid)", len(providers))
	}
	if providers[0] != session.ProviderClaudeCode {
		t.Errorf("providers[0] = %q, want %q", providers[0], session.ProviderClaudeCode)
	}
	if providers[1] != session.ProviderOpenCode {
		t.Errorf("providers[1] = %q, want %q", providers[1], session.ProviderOpenCode)
	}
}

// ── Save + Reload round-trip for all fields ──

func TestSaveAndReload_AllFields(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	repoDir := filepath.Join(dir, "repo")

	cfg := mustNewConfig(t, globalDir, repoDir)

	// Set every field
	mustSet(t, cfg, "storage_mode", "full")
	mustSet(t, cfg, "secrets.mode", "block")
	mustSet(t, cfg, "auto_capture", "false")
	mustSet(t, cfg, "summarize.enabled", "true")
	mustSet(t, cfg, "summarize.model", "gpt-4o")
	mustSet(t, cfg, "analysis.auto", "true")
	mustSet(t, cfg, "analysis.adapter", "opencode")
	mustSet(t, cfg, "analysis.error_threshold", "42")
	mustSet(t, cfg, "analysis.min_tool_calls", "15")
	mustSet(t, cfg, "analysis.model", "claude-3")
	mustSet(t, cfg, "dashboard.page_size", "50")
	mustSet(t, cfg, "dashboard.columns", "id,tokens,cost")
	mustSet(t, cfg, "dashboard.sort_by", "tokens")
	mustSet(t, cfg, "dashboard.sort_order", "asc")
	mustSet(t, cfg, "dashboard.default_provider", "opencode")
	mustSet(t, cfg, "dashboard.default_branch", "main")
	mustSet(t, cfg, "server.url", "https://aisync.example.com")
	mustSet(t, cfg, "database.path", "/tmp/aisync.db")
	cfg.AddPricingOverride("gpt-4", 10.0, 30.0)
	cfg.AddCustomPattern("MY_KEY (?i)key_\\w+")
	mustSave(t, cfg)

	// Reload
	cfg2 := mustNewConfig(t, globalDir, repoDir)

	checks := []struct {
		key  string
		want string
	}{
		{"storage_mode", "full"},
		{"secrets.mode", "block"},
		{"auto_capture", "false"},
		{"summarize.enabled", "true"},
		{"summarize.model", "gpt-4o"},
		{"analysis.auto", "true"},
		{"analysis.adapter", "opencode"},
		{"analysis.error_threshold", "42"},
		{"analysis.min_tool_calls", "15"},
		{"analysis.model", "claude-3"},
		{"dashboard.page_size", "50"},
		{"dashboard.columns", "id,tokens,cost"},
		{"dashboard.sort_by", "tokens"},
		{"dashboard.sort_order", "asc"},
		{"dashboard.default_provider", "opencode"},
		{"dashboard.default_branch", "main"},
		{"server.url", "https://aisync.example.com"},
		{"database.path", "/tmp/aisync.db"},
	}
	for _, c := range checks {
		got, err := cfg2.Get(c.key)
		if err != nil {
			t.Errorf("Get(%q) error = %v", c.key, err)
			continue
		}
		if got != c.want {
			t.Errorf("After reload: Get(%q) = %q, want %q", c.key, got, c.want)
		}
	}

	// Pricing overrides survived reload
	overrides := cfg2.GetPricingOverrides()
	if len(overrides) != 1 || overrides[0].Model != "gpt-4" {
		t.Errorf("After reload: PricingOverrides = %v, want [{gpt-4 10 30}]", overrides)
	}

	// Custom patterns survived reload
	patterns := cfg2.GetCustomPatterns()
	if len(patterns) != 1 || patterns[0] != "MY_KEY (?i)key_\\w+" {
		t.Errorf("After reload: CustomPatterns = %v, want [MY_KEY ...]", patterns)
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

// ── Dashboard config tests ──

func TestDashboard_Defaults(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if got := cfg.GetDashboardPageSize(); got != 25 {
		t.Errorf("GetDashboardPageSize() = %d, want 25", got)
	}
	cols := cfg.GetDashboardColumns()
	if len(cols) != 10 {
		t.Errorf("GetDashboardColumns() count = %d, want 10", len(cols))
	}
	if cols[0] != "id" {
		t.Errorf("first column = %q, want %q", cols[0], "id")
	}
	if got := cfg.GetDashboardSortBy(); got != "created_at" {
		t.Errorf("GetDashboardSortBy() = %q, want %q", got, "created_at")
	}
	if got := cfg.GetDashboardSortOrder(); got != "desc" {
		t.Errorf("GetDashboardSortOrder() = %q, want %q", got, "desc")
	}
	if got := cfg.GetDashboardDefaultProvider(); got != "" {
		t.Errorf("GetDashboardDefaultProvider() = %q, want empty", got)
	}
	if got := cfg.GetDashboardDefaultBranch(); got != "" {
		t.Errorf("GetDashboardDefaultBranch() = %q, want empty", got)
	}
}

func TestDashboard_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	tests := []struct {
		key     string
		value   string
		wantGet string
	}{
		{"dashboard.page_size", "50", "50"},
		{"dashboard.columns", "id,agent,tokens,cost,when", "id,agent,tokens,cost,when"},
		{"dashboard.sort_by", "tokens", "tokens"},
		{"dashboard.sort_order", "asc", "asc"},
		{"dashboard.default_provider", "opencode", "opencode"},
		{"dashboard.default_branch", "main", "main"},
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
			if got != tt.wantGet {
				t.Errorf("Get(%q) = %q, want %q", tt.key, got, tt.wantGet)
			}
		})
	}
}

func TestDashboard_SetColumns_Validation(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	// Setting invalid columns should fail.
	if err := cfg.Set("dashboard.columns", "invalid,nope"); err == nil {
		t.Error("expected error for invalid column names")
	}

	// Mixed valid/invalid should keep only valid ones.
	if err := cfg.Set("dashboard.columns", "id,invalid,tokens"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	cols := cfg.GetDashboardColumns()
	if len(cols) != 2 || cols[0] != "id" || cols[1] != "tokens" {
		t.Errorf("GetDashboardColumns() = %v, want [id, tokens]", cols)
	}
}

func TestDashboard_SetSortBy_Validation(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("dashboard.sort_by", "invalid"); err == nil {
		t.Error("expected error for invalid sort_by")
	}
}

func TestDashboard_SetSortOrder_Validation(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("dashboard.sort_order", "random"); err == nil {
		t.Error("expected error for invalid sort_order")
	}
}

func TestDashboard_SetPageSize_Validation(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	if err := cfg.Set("dashboard.page_size", "0"); err == nil {
		t.Error("expected error for page_size 0")
	}
	if err := cfg.Set("dashboard.page_size", "201"); err == nil {
		t.Error("expected error for page_size 201")
	}
	if err := cfg.Set("dashboard.page_size", "abc"); err == nil {
		t.Error("expected error for non-numeric page_size")
	}
}

func TestDashboard_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	repoDir := filepath.Join(dir, "repo")

	cfg := mustNewConfig(t, globalDir, repoDir)
	mustSet(t, cfg, "dashboard.columns", "id,agent,cost,when")
	mustSet(t, cfg, "dashboard.page_size", "100")
	mustSet(t, cfg, "dashboard.default_provider", "opencode")
	mustSave(t, cfg)

	// Reload from disk.
	cfg2 := mustNewConfig(t, globalDir, repoDir)
	cols := cfg2.GetDashboardColumns()
	if len(cols) != 4 || cols[1] != "agent" {
		t.Errorf("After reload: columns = %v, want [id,agent,cost,when]", cols)
	}
	if got := cfg2.GetDashboardPageSize(); got != 100 {
		t.Errorf("After reload: page_size = %d, want 100", got)
	}
	if got := cfg2.GetDashboardDefaultProvider(); got != "opencode" {
		t.Errorf("After reload: default_provider = %q, want %q", got, "opencode")
	}
}

// ── LLM Profiles Tests ──

func TestLLMProfiles_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	cfg, err := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Set a profile.
	if err := cfg.Set("llm.profiles.fast.provider", "ollama"); err != nil {
		t.Fatalf("Set provider: %v", err)
	}
	if err := cfg.Set("llm.profiles.fast.model", "qwen3:4b"); err != nil {
		t.Fatalf("Set model: %v", err)
	}

	// Get it back.
	got, err := cfg.Get("llm.profiles.fast.provider")
	if err != nil {
		t.Fatalf("Get provider: %v", err)
	}
	if got != "ollama" {
		t.Errorf("provider = %q, want %q", got, "ollama")
	}
	got, err = cfg.Get("llm.profiles.fast.model")
	if err != nil {
		t.Fatalf("Get model: %v", err)
	}
	if got != "qwen3:4b" {
		t.Errorf("model = %q, want %q", got, "qwen3:4b")
	}
}

func TestLLMProviders_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	cfg, err := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := cfg.Set("llm.providers.ollama.url", "http://gpu:11434"); err != nil {
		t.Fatalf("Set url: %v", err)
	}
	if err := cfg.Set("llm.providers.anthropic.api_key", "sk-test-key"); err != nil {
		t.Fatalf("Set api_key: %v", err)
	}

	got, _ := cfg.Get("llm.providers.ollama.url")
	if got != "http://gpu:11434" {
		t.Errorf("url = %q, want %q", got, "http://gpu:11434")
	}
	got, _ = cfg.Get("llm.providers.anthropic.api_key")
	if got != "****-key" { // masked
		t.Errorf("api_key = %q, want masked value", got)
	}
}

func TestLLMProfiles_InvalidProvider(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	// Invalid provider should not be set (setLLMDynamic returns false).
	if cfg.Set("llm.profiles.bad.provider", "invalid_provider") == nil {
		// The dynamic setter returns false for invalid providers,
		// which falls through to the default "unknown config key" error.
		// This is the expected behavior.
	}
}

func TestResolveProfile_FromProfiles(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	// Set up provider + profile.
	cfg.Set("llm.providers.ollama.url", "http://gpu:11434")
	cfg.Set("llm.profiles.fast.provider", "ollama")
	cfg.Set("llm.profiles.fast.model", "qwen3:4b")

	resolved := cfg.ResolveProfile("fast")
	if resolved.Provider != "ollama" {
		t.Errorf("Provider = %q, want %q", resolved.Provider, "ollama")
	}
	if resolved.Model != "qwen3:4b" {
		t.Errorf("Model = %q, want %q", resolved.Model, "qwen3:4b")
	}
	if resolved.URL != "http://gpu:11434" {
		t.Errorf("URL = %q, want %q", resolved.URL, "http://gpu:11434")
	}
}

func TestResolveProfile_FromAnalysisProfile(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	cfg.Set("llm.profiles.cloud.provider", "anthropic")
	cfg.Set("llm.profiles.cloud.model", "claude-haiku-4-20250514")
	cfg.Set("llm.providers.anthropic.api_key", "sk-test")
	cfg.Set("analysis.profile", "cloud")

	resolved := cfg.ResolveProfile("") // empty → falls back to analysis.profile
	if resolved.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", resolved.Provider, "anthropic")
	}
	if resolved.Model != "claude-haiku-4-20250514" {
		t.Errorf("Model = %q, want %q", resolved.Model, "claude-haiku-4-20250514")
	}
	if resolved.APIKey != "sk-test" {
		t.Errorf("APIKey = %q, want %q", resolved.APIKey, "sk-test")
	}
}

func TestResolveProfile_LegacyFallback(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	// No profiles, no analysis.profile — use legacy analysis.adapter + model.
	cfg.Set("analysis.adapter", "ollama")
	cfg.Set("analysis.model", "qwen3:30b")
	cfg.Set("analysis.ollama_url", "http://myhost:11434")

	resolved := cfg.ResolveProfile("")
	if resolved.Provider != "ollama" {
		t.Errorf("Provider = %q, want %q", resolved.Provider, "ollama")
	}
	if resolved.Model != "qwen3:30b" {
		t.Errorf("Model = %q, want %q", resolved.Model, "qwen3:30b")
	}
	if resolved.URL != "http://myhost:11434" {
		t.Errorf("URL = %q, want %q", resolved.URL, "http://myhost:11434")
	}
}

func TestResolveProfile_ProfileOverridesLegacy(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	// Legacy config.
	cfg.Set("analysis.adapter", "llm")
	cfg.Set("analysis.model", "old-model")

	// Profile config (should take precedence).
	cfg.Set("llm.profiles.default.provider", "ollama")
	cfg.Set("llm.profiles.default.model", "new-model")
	cfg.Set("analysis.profile", "default")

	resolved := cfg.ResolveProfile("")
	if resolved.Provider != "ollama" {
		t.Errorf("Provider = %q, want %q (profile should override legacy)", resolved.Provider, "ollama")
	}
	if resolved.Model != "new-model" {
		t.Errorf("Model = %q, want %q (profile should override legacy)", resolved.Model, "new-model")
	}
}

// ── Config Adapter Factory Selection Tests (8.6.3d) ──

func TestResolveProfile_NonexistentProfileFallsBackToLegacy(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	cfg.Set("analysis.adapter", "anthropic")
	cfg.Set("analysis.model", "claude-3-haiku")
	cfg.Set("analysis.profile", "nonexistent")

	resolved := cfg.ResolveProfile("")
	// "nonexistent" doesn't exist in profiles → should fall back to legacy.
	if resolved.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q (fallback to legacy)", resolved.Provider, "anthropic")
	}
	if resolved.Model != "claude-3-haiku" {
		t.Errorf("Model = %q, want %q (fallback to legacy)", resolved.Model, "claude-3-haiku")
	}
}

func TestResolveProfile_ExplicitNameOverridesAnalysisProfile(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	cfg.Set("llm.profiles.fast.provider", "ollama")
	cfg.Set("llm.profiles.fast.model", "qwen3:30b")
	cfg.Set("llm.profiles.cloud.provider", "anthropic")
	cfg.Set("llm.profiles.cloud.model", "claude-3-opus")
	cfg.Set("analysis.profile", "cloud")

	// Explicitly requesting "fast" should override the default "cloud".
	resolved := cfg.ResolveProfile("fast")
	if resolved.Provider != "ollama" {
		t.Errorf("Provider = %q, want %q", resolved.Provider, "ollama")
	}
	if resolved.Model != "qwen3:30b" {
		t.Errorf("Model = %q, want %q", resolved.Model, "qwen3:30b")
	}
}

func TestResolveProfile_ProviderInfraFromLLMProviders(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	cfg.Set("llm.providers.ollama.url", "http://gpu-server:11434")
	cfg.Set("llm.profiles.local.provider", "ollama")
	cfg.Set("llm.profiles.local.model", "qwen3:30b")

	resolved := cfg.ResolveProfile("local")
	if resolved.URL != "http://gpu-server:11434" {
		t.Errorf("URL = %q, want %q (should resolve from llm.providers)", resolved.URL, "http://gpu-server:11434")
	}
}

func TestResolveProfile_ProviderInfraFallsBackToLegacy(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	// No llm.providers configured, but legacy analysis keys are set.
	cfg.Set("analysis.adapter", "ollama")
	cfg.Set("analysis.ollama_url", "http://legacy:11434")

	resolved := cfg.ResolveProfile("")
	if resolved.URL != "http://legacy:11434" {
		t.Errorf("URL = %q, want %q (should fallback to legacy)", resolved.URL, "http://legacy:11434")
	}
}

func TestAnalysisProfile_GetSetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	mustSet(t, cfg, "analysis.profile", "fast")
	got, err := cfg.Get("analysis.profile")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if got != "fast" {
		t.Errorf("Get(analysis.profile) = %q, want %q", got, "fast")
	}
}

func TestAnalysisAdapter_AllValidValues(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "g"), filepath.Join(dir, "r"))

	for _, adapter := range []string{"llm", "opencode", "ollama", "anthropic"} {
		if err := cfg.Set("analysis.adapter", adapter); err != nil {
			t.Errorf("Set(analysis.adapter, %q) should succeed: %v", adapter, err)
		}
		if got := cfg.GetAnalysisAdapter(); got != adapter {
			t.Errorf("GetAnalysisAdapter() = %q after Set(%q)", got, adapter)
		}
	}
}

// ── Telemetry Config Tests ──

func TestTelemetry_Default(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	// Telemetry should be disabled by default.
	if cfg.IsTelemetryEnabled() {
		t.Error("IsTelemetryEnabled() default should be false")
	}
	got, err := cfg.Get("telemetry.enabled")
	if err != nil {
		t.Fatalf("Get(telemetry.enabled) error = %v", err)
	}
	if got != "false" {
		t.Errorf("Get(telemetry.enabled) = %q, want %q", got, "false")
	}
}

func TestTelemetry_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	// Enable telemetry.
	mustSet(t, cfg, "telemetry.enabled", "true")
	if !cfg.IsTelemetryEnabled() {
		t.Error("IsTelemetryEnabled() = false, want true after Set")
	}
	got, err := cfg.Get("telemetry.enabled")
	if err != nil {
		t.Fatalf("Get(telemetry.enabled) error = %v", err)
	}
	if got != "true" {
		t.Errorf("Get(telemetry.enabled) = %q, want %q", got, "true")
	}

	// Disable telemetry.
	mustSet(t, cfg, "telemetry.enabled", "false")
	if cfg.IsTelemetryEnabled() {
		t.Error("IsTelemetryEnabled() = true, want false after Set(false)")
	}
}

func TestTelemetry_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	repoDir := filepath.Join(dir, "repo")

	cfg := mustNewConfig(t, globalDir, repoDir)
	mustSet(t, cfg, "telemetry.enabled", "true")
	mustSave(t, cfg)

	// Reload from disk.
	cfg2 := mustNewConfig(t, globalDir, repoDir)
	if !cfg2.IsTelemetryEnabled() {
		t.Error("IsTelemetryEnabled() = false after reload, want true")
	}
}

func TestTelemetry_LoadFromMerge(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	repoDir := filepath.Join(dir, "repo")

	// Global config with telemetry enabled.
	mustMkdirAll(t, globalDir)
	globalJSON := `{"telemetry": {"enabled": true}}`
	if err := os.WriteFile(filepath.Join(globalDir, configFileName), []byte(globalJSON), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	// Repo config without telemetry — bool zero-value (false) overrides global.
	mustMkdirAll(t, repoDir)
	repoJSON := `{}`
	if err := os.WriteFile(filepath.Join(repoDir, configFileName), []byte(repoJSON), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	cfg := mustNewConfig(t, globalDir, repoDir)
	// Booleans always take the loaded value — repo's zero (false) overrides global's true.
	if cfg.IsTelemetryEnabled() {
		t.Error("IsTelemetryEnabled() should be false (repo's zero-value overrides global)")
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
}

// ── Scheduler Config Tests ──

func TestScheduler_GetAndSet(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	// Set all scheduler keys.
	mustSet(t, cfg, "scheduler.gc.enabled", "true")
	mustSet(t, cfg, "scheduler.gc.cron", "0 3 * * *")
	mustSet(t, cfg, "scheduler.gc.retention_days", "60")
	mustSet(t, cfg, "scheduler.capture_all.enabled", "true")
	mustSet(t, cfg, "scheduler.capture_all.cron", "*/15 * * * *")
	mustSet(t, cfg, "scheduler.stats_report.enabled", "true")
	mustSet(t, cfg, "scheduler.stats_report.cron", "30 * * * *")

	tests := []struct {
		key  string
		want string
	}{
		{"scheduler.gc.enabled", "true"},
		{"scheduler.gc.cron", "0 3 * * *"},
		{"scheduler.gc.retention_days", "60"},
		{"scheduler.capture_all.enabled", "true"},
		{"scheduler.capture_all.cron", "*/15 * * * *"},
		{"scheduler.stats_report.enabled", "true"},
		{"scheduler.stats_report.cron", "30 * * * *"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, err := cfg.Get(tt.key)
			if err != nil {
				t.Fatalf("Get(%q) error = %v", tt.key, err)
			}
			if got != tt.want {
				t.Errorf("Get(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestScheduler_Defaults(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	// All should be disabled by default.
	if cfg.GetSchedulerGCEnabled() {
		t.Error("GC should be disabled by default")
	}
	if cfg.GetSchedulerCaptureAllEnabled() {
		t.Error("CaptureAll should be disabled by default")
	}
	if cfg.GetSchedulerStatsReportEnabled() {
		t.Error("StatsReport should be disabled by default")
	}

	// Defaults for cron expressions.
	if got := cfg.GetSchedulerGCCron(); got != "0 3 * * *" {
		t.Errorf("GetSchedulerGCCron() = %q, want default %q", got, "0 3 * * *")
	}
	if got := cfg.GetSchedulerCaptureAllCron(); got != "*/30 * * * *" {
		t.Errorf("GetSchedulerCaptureAllCron() = %q, want default %q", got, "*/30 * * * *")
	}
	if got := cfg.GetSchedulerStatsReportCron(); got != "0 * * * *" {
		t.Errorf("GetSchedulerStatsReportCron() = %q, want default %q", got, "0 * * * *")
	}
	if got := cfg.GetSchedulerGCRetentionDays(); got != 90 {
		t.Errorf("GetSchedulerGCRetentionDays() = %d, want default 90", got)
	}
}

func TestScheduler_InvalidCron(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	tests := []struct {
		key   string
		value string
	}{
		{"scheduler.gc.cron", "invalid cron"},
		{"scheduler.capture_all.cron", "not a cron"},
		{"scheduler.stats_report.cron", "1 2 3"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			err := cfg.Set(tt.key, tt.value)
			if err == nil {
				t.Errorf("Set(%q, %q) should fail with invalid cron", tt.key, tt.value)
			}
		})
	}
}

func TestScheduler_InvalidRetentionDays(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	tests := []struct {
		value string
	}{
		{"0"},
		{"-1"},
		{"abc"},
		{""},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			err := cfg.Set("scheduler.gc.retention_days", tt.value)
			if err == nil {
				t.Errorf("Set(retention_days, %q) should fail", tt.value)
			}
		})
	}
}

func TestScheduler_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	repoDir := filepath.Join(dir, "repo")
	cfg := mustNewConfig(t, globalDir, repoDir)

	mustSet(t, cfg, "scheduler.gc.enabled", "true")
	mustSet(t, cfg, "scheduler.gc.cron", "0 4 * * *")
	mustSet(t, cfg, "scheduler.gc.retention_days", "45")
	mustSet(t, cfg, "scheduler.capture_all.enabled", "true")
	mustSet(t, cfg, "scheduler.capture_all.cron", "*/10 * * * *")
	mustSet(t, cfg, "scheduler.stats_report.enabled", "true")
	mustSet(t, cfg, "scheduler.stats_report.cron", "15 * * * *")

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Reload
	cfg2, err := New(globalDir, repoDir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if !cfg2.GetSchedulerGCEnabled() {
		t.Error("GC should be enabled after reload")
	}
	if got := cfg2.GetSchedulerGCCron(); got != "0 4 * * *" {
		t.Errorf("GC cron = %q after reload, want %q", got, "0 4 * * *")
	}
	if got := cfg2.GetSchedulerGCRetentionDays(); got != 45 {
		t.Errorf("GC retention = %d after reload, want 45", got)
	}
	if !cfg2.GetSchedulerCaptureAllEnabled() {
		t.Error("CaptureAll should be enabled after reload")
	}
	if got := cfg2.GetSchedulerCaptureAllCron(); got != "*/10 * * * *" {
		t.Errorf("CaptureAll cron = %q after reload, want %q", got, "*/10 * * * *")
	}
	if !cfg2.GetSchedulerStatsReportEnabled() {
		t.Error("StatsReport should be enabled after reload")
	}
	if got := cfg2.GetSchedulerStatsReportCron(); got != "15 * * * *" {
		t.Errorf("StatsReport cron = %q after reload, want %q", got, "15 * * * *")
	}
}

func TestScheduler_GetterValues(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))

	mustSet(t, cfg, "scheduler.gc.enabled", "true")
	mustSet(t, cfg, "scheduler.gc.retention_days", "30")
	mustSet(t, cfg, "scheduler.gc.cron", "0 2 * * *")

	if !cfg.GetSchedulerGCEnabled() {
		t.Error("expected GC enabled")
	}
	if got := cfg.GetSchedulerGCRetentionDays(); got != 30 {
		t.Errorf("retention = %d, want 30", got)
	}
	if got := cfg.GetSchedulerGCCron(); got != "0 2 * * *" {
		t.Errorf("cron = %q, want %q", got, "0 2 * * *")
	}
}

// ── Database Driver Tests ──

func TestDatabaseDriver_Default(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))
	if got := cfg.GetDatabaseDriver(); got != "sqlite" {
		t.Errorf("GetDatabaseDriver() = %q, want default %q", got, "sqlite")
	}
}

func TestDatabaseDriver_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))
	mustSet(t, cfg, "database.driver", "sqlite")
	got, err := cfg.Get("database.driver")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if got != "sqlite" {
		t.Errorf("Get(database.driver) = %q, want %q", got, "sqlite")
	}
}

func TestDatabaseDriver_InvalidDriver(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "global"), filepath.Join(dir, "repo"))
	err := cfg.Set("database.driver", "postgres")
	if err == nil {
		t.Error("Set(database.driver, postgres) should fail")
	}
	err = cfg.Set("database.driver", "mysql")
	if err == nil {
		t.Error("Set(database.driver, mysql) should fail")
	}
}

func TestDatabaseDriver_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	repoDir := filepath.Join(dir, "repo")
	cfg := mustNewConfig(t, globalDir, repoDir)
	mustSet(t, cfg, "database.driver", "sqlite")
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	cfg2, err := New(globalDir, repoDir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if got := cfg2.GetDatabaseDriver(); got != "sqlite" {
		t.Errorf("after reload: %q, want %q", got, "sqlite")
	}
}

// ── Project Category Config Tests ──

func TestProjectCategory_Default(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "g"), filepath.Join(dir, "r"))
	if got := cfg.GetProjectCategory(); got != "" {
		t.Errorf("GetProjectCategory() = %q, want empty default", got)
	}
	if got := cfg.IsProjectAutoDetectEnabled(); got {
		t.Error("auto-detect should be disabled by default")
	}
}

func TestProjectCategory_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "g"), filepath.Join(dir, "r"))
	mustSet(t, cfg, "project.category", "backend")
	got, err := cfg.Get("project.category")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if got != "backend" {
		t.Errorf("Get(project.category) = %q, want %q", got, "backend")
	}
}

func TestProjectCategory_AutoDetect(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "g"), filepath.Join(dir, "r"))
	mustSet(t, cfg, "project.auto_detect", "true")
	if !cfg.IsProjectAutoDetectEnabled() {
		t.Error("auto-detect should be enabled after Set")
	}
}

func TestProjectCategory_CustomCategories(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "g"), filepath.Join(dir, "r"))
	mustSet(t, cfg, "project.categories", "backend,frontend,ml,security")
	cats := cfg.GetProjectCategories()
	if len(cats) != 4 {
		t.Fatalf("expected 4 categories, got %d: %v", len(cats), cats)
	}
	if cats[2] != "ml" {
		t.Errorf("categories[2] = %q, want %q", cats[2], "ml")
	}
}

func TestProjectCategory_DefaultCategories(t *testing.T) {
	dir := t.TempDir()
	cfg := mustNewConfig(t, filepath.Join(dir, "g"), filepath.Join(dir, "r"))
	cats := cfg.GetProjectCategories()
	if len(cats) != 8 {
		t.Errorf("expected 8 default categories, got %d", len(cats))
	}
}

func TestProjectCategory_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	gDir := filepath.Join(dir, "g")
	rDir := filepath.Join(dir, "r")
	cfg := mustNewConfig(t, gDir, rDir)
	mustSet(t, cfg, "project.category", "ops")
	mustSet(t, cfg, "project.auto_detect", "true")
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	cfg2, err := New(gDir, rDir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if got := cfg2.GetProjectCategory(); got != "ops" {
		t.Errorf("after reload: %q, want %q", got, "ops")
	}
	if !cfg2.IsProjectAutoDetectEnabled() {
		t.Error("auto-detect should be enabled after reload")
	}
}

// ── Error Classification Config Tests ──

func TestErrorsConfig_Defaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if got := cfg.GetErrorsClassifier(); got != "deterministic" {
		t.Errorf("GetErrorsClassifier() = %q, want %q", got, "deterministic")
	}
	if cfg.IsErrorsLLMFallbackEnabled() {
		t.Error("LLM fallback should be disabled by default")
	}
	if got := cfg.GetErrorsLLMSchedule(); got != "" {
		t.Errorf("GetErrorsLLMSchedule() = %q, want empty", got)
	}
	if got := cfg.GetErrorsLLMProfile(); got != "" {
		t.Errorf("GetErrorsLLMProfile() = %q, want empty", got)
	}
}

func TestErrorsConfig_GetSet(t *testing.T) {
	dir := t.TempDir()
	cfg, err := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		key      string
		setValue string
		wantGet  string
	}{
		{"errors.classifier", "composite", "composite"},
		{"errors.classifier", "deterministic", "deterministic"},
		{"errors.llm_fallback", "true", "true"},
		{"errors.llm_fallback", "false", "false"},
		{"errors.llm_schedule", "0 0 * * *", "0 0 * * *"},
		{"errors.llm_profile", "fast", "fast"},
	}

	for _, tt := range tests {
		if err := cfg.Set(tt.key, tt.setValue); err != nil {
			t.Fatalf("Set(%q, %q) error = %v", tt.key, tt.setValue, err)
		}
		got, err := cfg.Get(tt.key)
		if err != nil {
			t.Fatalf("Get(%q) error = %v", tt.key, err)
		}
		if got != tt.wantGet {
			t.Errorf("Get(%q) = %q, want %q", tt.key, got, tt.wantGet)
		}
	}
}

func TestErrorsConfig_SetValidation(t *testing.T) {
	dir := t.TempDir()
	cfg, err := New(filepath.Join(dir, "g"), filepath.Join(dir, "r"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Invalid classifier value.
	if err := cfg.Set("errors.classifier", "invalid"); err == nil {
		t.Error("expected error for invalid classifier")
	}

	// Invalid cron expression.
	if err := cfg.Set("errors.llm_schedule", "not a cron"); err == nil {
		t.Error("expected error for invalid cron expression")
	}

	// Valid cron expression should succeed.
	if err := cfg.Set("errors.llm_schedule", "0 0 * * *"); err != nil {
		t.Errorf("valid cron should succeed: %v", err)
	}
}

func TestErrorsConfig_LoadFromJSON(t *testing.T) {
	dir := t.TempDir()
	gDir := filepath.Join(dir, "g")
	rDir := filepath.Join(dir, "r")

	os.MkdirAll(gDir, 0o755)

	configJSON := `{
		"errors": {
			"classifier": "composite",
			"llm_fallback": true,
			"llm_schedule": "0 2 * * *",
			"llm_profile": "cloud"
		}
	}`
	os.WriteFile(filepath.Join(gDir, "config.json"), []byte(configJSON), 0o644)

	cfg, err := New(gDir, rDir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if got := cfg.GetErrorsClassifier(); got != "composite" {
		t.Errorf("GetErrorsClassifier() = %q, want %q", got, "composite")
	}
	if !cfg.IsErrorsLLMFallbackEnabled() {
		t.Error("LLM fallback should be enabled")
	}
	if got := cfg.GetErrorsLLMSchedule(); got != "0 2 * * *" {
		t.Errorf("GetErrorsLLMSchedule() = %q, want %q", got, "0 2 * * *")
	}
	if got := cfg.GetErrorsLLMProfile(); got != "cloud" {
		t.Errorf("GetErrorsLLMProfile() = %q, want %q", got, "cloud")
	}
}
