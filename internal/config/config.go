// Package config implements the aisync configuration layer.
// Config supports two levels: global (~/.aisync/) and per-repo (.aisync/).
// Per-repo values override global values.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robfig/cron/v3"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

const configFileName = "config.json"

// configData is the JSON-serializable configuration structure.
type configData struct {
	StorageMode string        `json:"storage_mode"`
	Providers   []string      `json:"providers"`
	Secrets     secrets       `json:"secrets"`
	Summarize   summarize     `json:"summarize"`
	Pricing     pricing       `json:"pricing"`
	LLM         llmConf       `json:"llm"`
	Analysis    analysisConf  `json:"analysis"`
	Tagging     taggingConf   `json:"tagging"`
	Project     projectConf   `json:"project"`
	Webhooks    webhooksConf  `json:"webhooks"`
	Dashboard   dashboardConf `json:"dashboard"`
	Server      serverConf    `json:"server"`
	Database    databaseConf  `json:"database"`
	Scheduler   schedulerConf `json:"scheduler"`
	Telemetry   telemetryConf `json:"telemetry"`
	Version     int           `json:"version"`
	AutoCapture bool          `json:"auto_capture"`
}

// PricingOverride is an exported type for pricing overrides, used by Factory.
type PricingOverride struct {
	Model           string  `json:"model"`
	InputPerMToken  float64 `json:"input_per_mtoken"`
	OutputPerMToken float64 `json:"output_per_mtoken"`
}

type secrets struct {
	Mode            string   `json:"mode"`
	CustomPatterns  []string `json:"custom_patterns"`
	IgnorePatterns  []string `json:"ignore_patterns"`
	ScanToolOutputs bool     `json:"scan_tool_outputs"`
}

type summarize struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model"`
}

type pricing struct {
	Overrides []PricingOverride `json:"overrides"`
}

// ── LLM Provider Profiles ──

// llmConf holds provider infrastructure and named profiles.
// Providers define connection details (URLs, API keys).
// Profiles define which provider + model to use for a given purpose.
type llmConf struct {
	Providers map[string]llmProviderConf `json:"providers,omitempty"` // keyed by provider name ("ollama", "anthropic")
	Profiles  map[string]llmProfile      `json:"profiles,omitempty"`  // keyed by profile name ("default", "fast", "cloud")
}

// llmProviderConf holds connection details for an LLM provider.
type llmProviderConf struct {
	URL    string `json:"url,omitempty"`     // base URL (Ollama)
	APIKey string `json:"api_key,omitempty"` // API key (Anthropic)
}

// llmProfile selects a provider + model for a specific purpose.
type llmProfile struct {
	Provider string `json:"provider"` // "ollama", "anthropic", "opencode", "llm"
	Model    string `json:"model"`    // e.g. "qwen3:30b", "claude-haiku-4-20250514"
}

// ResolvedProfile is the fully-resolved LLM configuration ready to create an adapter.
type ResolvedProfile struct {
	Provider string // "ollama", "anthropic", "opencode", "llm"
	Model    string // model name
	URL      string // provider URL (Ollama)
	APIKey   string // provider API key (Anthropic)
}

type analysisConf struct {
	Auto           bool    `json:"auto"`
	Profile        string  `json:"profile,omitempty"` // LLM profile name (e.g. "default", "fast")
	Adapter        string  `json:"adapter"`           // LEGACY: "llm", "opencode", "ollama", "anthropic"
	ErrorThreshold float64 `json:"error_threshold"`   // percent (0-100)
	MinToolCalls   int     `json:"min_tool_calls"`
	Model          string  `json:"model"`      // LEGACY: optional model override
	OllamaURL      string  `json:"ollama_url"` // LEGACY: Ollama API base URL
	APIKey         string  `json:"api_key"`    // LEGACY: Anthropic API key
	Schedule       string  `json:"schedule"`   // cron expression
}

type taggingConf struct {
	Auto    bool     `json:"auto"`              // auto-tag after capture
	Profile string   `json:"profile,omitempty"` // LLM profile for classification (e.g. "fast")
	Tags    []string `json:"tags,omitempty"`    // custom tag list; empty = use defaults
}

// projectConf holds project-level configuration (stored per-repo in .aisync/config.json).
type projectConf struct {
	Category   string   `json:"category,omitempty"`   // project category: backend, frontend, ops, etc.
	Categories []string `json:"categories,omitempty"` // custom category list (extends defaults)
	AutoDetect bool     `json:"auto_detect"`          // auto-detect category from project files
}

type webhookEntry struct {
	URL    string   `json:"url"`
	Events []string `json:"events,omitempty"` // empty = all events
	Secret string   `json:"secret,omitempty"`
}

type webhooksConf struct {
	Hooks []webhookEntry `json:"hooks,omitempty"`
}

type serverConf struct {
	URL  string   `json:"url"`  // e.g. "http://127.0.0.1:8371"; empty = standalone mode
	Auth authConf `json:"auth"` // authentication config
}

type authConf struct {
	Enabled   bool   `json:"enabled"`    // false = no auth required (default for localhost)
	JWTSecret string `json:"jwt_secret"` // HMAC signing key for JWT tokens
}

type databaseConf struct {
	Path   string `json:"path"`   // e.g. "/shared/aisync.db"; empty = default (~/.aisync/sessions.db)
	Driver string `json:"driver"` // storage driver: "sqlite" (default); future: "postgres"
}

// ── Scheduler ──

// schedulerConf holds configuration for all scheduled tasks.
type schedulerConf struct {
	GC          schedulerTaskConf `json:"gc"`
	CaptureAll  schedulerTaskConf `json:"capture_all"`
	StatsReport schedulerTaskConf `json:"stats_report"`
}

// schedulerTaskConf holds configuration for a single scheduled task.
type schedulerTaskConf struct {
	Enabled       bool   `json:"enabled"`
	Cron          string `json:"cron,omitempty"`           // cron expression (5-field)
	RetentionDays int    `json:"retention_days,omitempty"` // only used by gc task
}

// ── Telemetry ──

// telemetryConf holds opt-in anonymous usage statistics configuration.
type telemetryConf struct {
	Enabled bool `json:"enabled"`
}

type dashboardConf struct {
	PageSize        int      `json:"page_size"`        // sessions per page (default 25)
	Columns         []string `json:"columns"`          // visible columns in order
	SortBy          string   `json:"sort_by"`          // default sort field (default "created_at")
	SortOrder       string   `json:"sort_order"`       // "asc" or "desc" (default "desc")
	DefaultProvider string   `json:"default_provider"` // pre-selected provider filter
	DefaultBranch   string   `json:"default_branch"`   // pre-selected branch filter
}

// ValidDashboardColumns is the set of column identifiers the sessions table supports.
var ValidDashboardColumns = map[string]bool{
	"id": true, "provider": true, "agent": true, "branch": true,
	"summary": true, "messages": true, "tokens": true, "cost": true,
	"tools": true, "errors": true, "error_rate": true, "when": true,
}

// DefaultDashboardColumns is the default column set.
var DefaultDashboardColumns = []string{
	"id", "provider", "branch", "summary", "messages", "tokens", "errors", "when",
}

func defaultConfig() configData {
	return configData{
		Version:     1,
		Providers:   []string{"claude-code", "opencode"},
		AutoCapture: true,
		StorageMode: "compact",
		Secrets: secrets{
			Mode:            "mask",
			ScanToolOutputs: true,
		},
		Analysis: analysisConf{
			Auto:           false,
			Adapter:        "llm",
			ErrorThreshold: 20,
			MinToolCalls:   5,
		},
		Dashboard: dashboardConf{
			PageSize:  25,
			Columns:   DefaultDashboardColumns,
			SortBy:    "created_at",
			SortOrder: "desc",
		},
	}
}

// Config implements session.Config using JSON files.
type Config struct {
	globalDir string
	repoDir   string
	data      configData
}

// New creates a Config by loading and merging global + repo config files.
// Directories are created if they don't exist. Missing files use defaults.
func New(globalDir, repoDir string) (*Config, error) {
	cfg := &Config{
		data:      defaultConfig(),
		globalDir: globalDir,
		repoDir:   repoDir,
	}

	// Load global config if it exists
	if globalDir != "" {
		if err := cfg.loadFrom(globalDir); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("loading global config: %w", err)
		}
	}

	// Load repo config (overrides global)
	if repoDir != "" {
		if err := cfg.loadFrom(repoDir); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("loading repo config: %w", err)
		}
	}

	return cfg, nil
}

func (c *Config) loadFrom(dir string) error {
	path := filepath.Join(dir, configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var loaded configData
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	// Merge: loaded values override defaults only if set (non-zero)
	if loaded.StorageMode != "" {
		c.data.StorageMode = loaded.StorageMode
	}
	if loaded.Secrets.Mode != "" {
		c.data.Secrets.Mode = loaded.Secrets.Mode
	}
	if len(loaded.Providers) > 0 {
		c.data.Providers = loaded.Providers
	}
	// AutoCapture is a bool — always take the loaded value
	c.data.AutoCapture = loaded.AutoCapture
	c.data.Secrets.ScanToolOutputs = loaded.Secrets.ScanToolOutputs
	if len(loaded.Secrets.CustomPatterns) > 0 {
		c.data.Secrets.CustomPatterns = loaded.Secrets.CustomPatterns
	}
	if len(loaded.Secrets.IgnorePatterns) > 0 {
		c.data.Secrets.IgnorePatterns = loaded.Secrets.IgnorePatterns
	}

	// Summarize — bools always take the loaded value
	c.data.Summarize.Enabled = loaded.Summarize.Enabled
	if loaded.Summarize.Model != "" {
		c.data.Summarize.Model = loaded.Summarize.Model
	}

	// Pricing overrides
	if len(loaded.Pricing.Overrides) > 0 {
		c.data.Pricing.Overrides = loaded.Pricing.Overrides
	}

	// Analysis — bools always take the loaded value
	c.data.Analysis.Auto = loaded.Analysis.Auto
	if loaded.Analysis.Adapter != "" {
		c.data.Analysis.Adapter = loaded.Analysis.Adapter
	}
	if loaded.Analysis.ErrorThreshold > 0 {
		c.data.Analysis.ErrorThreshold = loaded.Analysis.ErrorThreshold
	}
	if loaded.Analysis.MinToolCalls > 0 {
		c.data.Analysis.MinToolCalls = loaded.Analysis.MinToolCalls
	}
	if loaded.Analysis.Model != "" {
		c.data.Analysis.Model = loaded.Analysis.Model
	}

	// Dashboard
	if loaded.Dashboard.PageSize > 0 {
		c.data.Dashboard.PageSize = loaded.Dashboard.PageSize
	}
	if len(loaded.Dashboard.Columns) > 0 {
		c.data.Dashboard.Columns = loaded.Dashboard.Columns
	}
	if loaded.Dashboard.SortBy != "" {
		c.data.Dashboard.SortBy = loaded.Dashboard.SortBy
	}
	if loaded.Dashboard.SortOrder != "" {
		c.data.Dashboard.SortOrder = loaded.Dashboard.SortOrder
	}
	if loaded.Dashboard.DefaultProvider != "" {
		c.data.Dashboard.DefaultProvider = loaded.Dashboard.DefaultProvider
	}
	if loaded.Dashboard.DefaultBranch != "" {
		c.data.Dashboard.DefaultBranch = loaded.Dashboard.DefaultBranch
	}

	// Server
	if loaded.Server.URL != "" {
		c.data.Server.URL = loaded.Server.URL
	}

	// Database
	if loaded.Database.Path != "" {
		c.data.Database.Path = loaded.Database.Path
	}
	if loaded.Database.Driver != "" {
		c.data.Database.Driver = loaded.Database.Driver
	}

	// Project
	if loaded.Project.Category != "" {
		c.data.Project.Category = loaded.Project.Category
	}
	if len(loaded.Project.Categories) > 0 {
		c.data.Project.Categories = loaded.Project.Categories
	}
	// AutoDetect bool — take loaded value only if explicitly true (false is zero-value ambiguous)
	if loaded.Project.AutoDetect {
		c.data.Project.AutoDetect = true
	}

	// Scheduler — bools always take the loaded value
	c.data.Scheduler.GC.Enabled = loaded.Scheduler.GC.Enabled
	if loaded.Scheduler.GC.Cron != "" {
		c.data.Scheduler.GC.Cron = loaded.Scheduler.GC.Cron
	}
	if loaded.Scheduler.GC.RetentionDays > 0 {
		c.data.Scheduler.GC.RetentionDays = loaded.Scheduler.GC.RetentionDays
	}
	c.data.Scheduler.CaptureAll.Enabled = loaded.Scheduler.CaptureAll.Enabled
	if loaded.Scheduler.CaptureAll.Cron != "" {
		c.data.Scheduler.CaptureAll.Cron = loaded.Scheduler.CaptureAll.Cron
	}
	c.data.Scheduler.StatsReport.Enabled = loaded.Scheduler.StatsReport.Enabled
	if loaded.Scheduler.StatsReport.Cron != "" {
		c.data.Scheduler.StatsReport.Cron = loaded.Scheduler.StatsReport.Cron
	}

	// Telemetry — bools always take the loaded value
	c.data.Telemetry.Enabled = loaded.Telemetry.Enabled

	return nil
}

// Get retrieves a configuration value by key.
func (c *Config) Get(key string) (string, error) {
	switch key {
	case "storage_mode":
		return c.data.StorageMode, nil
	case "secrets.mode":
		return c.data.Secrets.Mode, nil
	case "auto_capture":
		if c.data.AutoCapture {
			return "true", nil
		}
		return "false", nil
	case "summarize.enabled":
		if c.data.Summarize.Enabled {
			return "true", nil
		}
		return "false", nil
	case "summarize.model":
		return c.data.Summarize.Model, nil
	case "analysis.profile":
		return c.data.Analysis.Profile, nil
	case "tagging.auto":
		if c.data.Tagging.Auto {
			return "true", nil
		}
		return "false", nil
	case "tagging.profile":
		return c.data.Tagging.Profile, nil
	case "tagging.tags":
		return strings.Join(c.GetTaggingTags(), ","), nil

	// Project
	case "project.category":
		return c.data.Project.Category, nil
	case "project.categories":
		return strings.Join(c.GetProjectCategories(), ","), nil
	case "project.auto_detect":
		return fmt.Sprintf("%v", c.data.Project.AutoDetect), nil

	case "analysis.auto":
		if c.data.Analysis.Auto {
			return "true", nil
		}
		return "false", nil
	case "analysis.adapter":
		return c.data.Analysis.Adapter, nil
	case "analysis.error_threshold":
		return fmt.Sprintf("%.0f", c.data.Analysis.ErrorThreshold), nil
	case "analysis.min_tool_calls":
		return fmt.Sprintf("%d", c.data.Analysis.MinToolCalls), nil
	case "analysis.model":
		return c.data.Analysis.Model, nil
	case "analysis.ollama_url":
		return c.GetAnalysisOllamaURL(), nil
	case "analysis.api_key":
		if c.data.Analysis.APIKey != "" {
			return "****" + c.data.Analysis.APIKey[max(0, len(c.data.Analysis.APIKey)-4):], nil // mask for display
		}
		return "", nil
	case "analysis.schedule":
		return c.data.Analysis.Schedule, nil
	case "dashboard.page_size":
		return strconv.Itoa(c.data.Dashboard.PageSize), nil
	case "dashboard.columns":
		return strings.Join(c.data.Dashboard.Columns, ","), nil
	case "dashboard.sort_by":
		return c.data.Dashboard.SortBy, nil
	case "dashboard.sort_order":
		return c.data.Dashboard.SortOrder, nil
	case "dashboard.default_provider":
		return c.data.Dashboard.DefaultProvider, nil
	case "dashboard.default_branch":
		return c.data.Dashboard.DefaultBranch, nil

	// Server
	case "server.url":
		return c.data.Server.URL, nil
	case "server.auth.enabled":
		return fmt.Sprintf("%v", c.data.Server.Auth.Enabled), nil
	case "server.auth.jwt_secret":
		if s := c.data.Server.Auth.JWTSecret; s != "" {
			return "****" + s[max(0, len(s)-4):], nil // mask the secret
		}
		return "", nil

	// Database
	case "database.path":
		return c.data.Database.Path, nil
	case "database.driver":
		return c.GetDatabaseDriver(), nil

	// Scheduler
	case "scheduler.gc.enabled":
		return fmt.Sprintf("%v", c.data.Scheduler.GC.Enabled), nil
	case "scheduler.gc.cron":
		return c.data.Scheduler.GC.Cron, nil
	case "scheduler.gc.retention_days":
		return strconv.Itoa(c.data.Scheduler.GC.RetentionDays), nil
	case "scheduler.capture_all.enabled":
		return fmt.Sprintf("%v", c.data.Scheduler.CaptureAll.Enabled), nil
	case "scheduler.capture_all.cron":
		return c.data.Scheduler.CaptureAll.Cron, nil
	case "scheduler.stats_report.enabled":
		return fmt.Sprintf("%v", c.data.Scheduler.StatsReport.Enabled), nil
	case "scheduler.stats_report.cron":
		return c.data.Scheduler.StatsReport.Cron, nil

	// Telemetry
	case "telemetry.enabled":
		if c.data.Telemetry.Enabled {
			return "true", nil
		}
		return "false", nil

	default:
		// Dynamic keys: llm.providers.<name>.<field>, llm.profiles.<name>.<field>
		if v, ok := c.getLLMDynamic(key); ok {
			return v, nil
		}
		return "", fmt.Errorf("unknown config key %q", key)
	}
}

// getLLMDynamic handles dynamic dot-notation keys for LLM providers and profiles.
func (c *Config) getLLMDynamic(key string) (string, bool) {
	parts := strings.Split(key, ".")
	if len(parts) != 4 || parts[0] != "llm" {
		return "", false
	}

	section, name, field := parts[1], parts[2], parts[3]
	switch section {
	case "providers":
		if c.data.LLM.Providers == nil {
			return "", true
		}
		prov, ok := c.data.LLM.Providers[name]
		if !ok {
			return "", true
		}
		switch field {
		case "url":
			return prov.URL, true
		case "api_key":
			if prov.APIKey != "" {
				return "****" + prov.APIKey[max(0, len(prov.APIKey)-4):], true
			}
			return "", true
		}
	case "profiles":
		if c.data.LLM.Profiles == nil {
			return "", true
		}
		prof, ok := c.data.LLM.Profiles[name]
		if !ok {
			return "", true
		}
		switch field {
		case "provider":
			return prof.Provider, true
		case "model":
			return prof.Model, true
		}
	}
	return "", false
}

// Set updates a configuration value.
func (c *Config) Set(key string, value string) error {
	switch key {
	case "storage_mode":
		if _, err := session.ParseStorageMode(value); err != nil {
			return err
		}
		c.data.StorageMode = value
	case "secrets.mode":
		if _, err := session.ParseSecretMode(value); err != nil {
			return err
		}
		c.data.Secrets.Mode = value
	case "secrets.custom_patterns.add":
		c.data.Secrets.CustomPatterns = append(c.data.Secrets.CustomPatterns, value)
	case "auto_capture":
		c.data.AutoCapture = value == "true"
	case "summarize.enabled":
		c.data.Summarize.Enabled = value == "true"
	case "summarize.model":
		c.data.Summarize.Model = value
	case "analysis.auto":
		c.data.Analysis.Auto = value == "true"
	case "analysis.profile":
		c.data.Analysis.Profile = value
	case "analysis.adapter":
		if value != "llm" && value != "opencode" && value != "ollama" && value != "anthropic" {
			return fmt.Errorf("invalid analysis adapter %q: must be \"llm\", \"opencode\", \"ollama\", or \"anthropic\"", value)
		}
		c.data.Analysis.Adapter = value
	case "analysis.error_threshold":
		var v float64
		if _, err := fmt.Sscanf(value, "%f", &v); err != nil {
			return fmt.Errorf("invalid error_threshold %q: %w", value, err)
		}
		c.data.Analysis.ErrorThreshold = v
	case "analysis.min_tool_calls":
		var v int
		if _, err := fmt.Sscanf(value, "%d", &v); err != nil {
			return fmt.Errorf("invalid min_tool_calls %q: %w", value, err)
		}
		c.data.Analysis.MinToolCalls = v
	case "analysis.model":
		c.data.Analysis.Model = value
	case "analysis.ollama_url":
		c.data.Analysis.OllamaURL = value
	case "analysis.api_key":
		c.data.Analysis.APIKey = value
	case "analysis.schedule":
		c.data.Analysis.Schedule = value

	// Project
	case "project.category":
		c.data.Project.Category = value
	case "project.categories":
		cats := strings.Split(value, ",")
		var valid []string
		for _, cat := range cats {
			cat = strings.TrimSpace(cat)
			if cat != "" {
				valid = append(valid, cat)
			}
		}
		c.data.Project.Categories = valid
	case "project.auto_detect":
		c.data.Project.AutoDetect = value == "true"

	// Tagging
	case "tagging.auto":
		c.data.Tagging.Auto = value == "true"
	case "tagging.profile":
		c.data.Tagging.Profile = value
	case "tagging.tags":
		c.data.Tagging.Tags = strings.Split(value, ",")

	case "dashboard.page_size":
		v, err := strconv.Atoi(value)
		if err != nil || v < 1 || v > 200 {
			return fmt.Errorf("invalid page_size %q: must be 1-200", value)
		}
		c.data.Dashboard.PageSize = v
	case "dashboard.columns":
		cols := strings.Split(value, ",")
		var valid []string
		for _, col := range cols {
			col = strings.TrimSpace(col)
			if col != "" && ValidDashboardColumns[col] {
				valid = append(valid, col)
			}
		}
		if len(valid) == 0 {
			return fmt.Errorf("no valid columns in %q; valid: id,provider,agent,branch,summary,messages,tokens,cost,error_rate,when", value)
		}
		c.data.Dashboard.Columns = valid
	case "dashboard.sort_by":
		allowed := map[string]bool{"created_at": true, "provider": true, "branch": true, "tokens": true, "messages": true}
		if !allowed[value] {
			return fmt.Errorf("invalid sort_by %q: allowed: created_at,provider,branch,tokens,messages", value)
		}
		c.data.Dashboard.SortBy = value
	case "dashboard.sort_order":
		if value != "asc" && value != "desc" {
			return fmt.Errorf("invalid sort_order %q: must be \"asc\" or \"desc\"", value)
		}
		c.data.Dashboard.SortOrder = value
	case "dashboard.default_provider":
		if value != "" {
			if _, err := session.ParseProviderName(value); err != nil {
				return fmt.Errorf("invalid default_provider %q: %w", value, err)
			}
		}
		c.data.Dashboard.DefaultProvider = value
	case "dashboard.default_branch":
		c.data.Dashboard.DefaultBranch = value

	// Server
	case "server.url":
		if value != "" && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return fmt.Errorf("server.url must start with http:// or https://, got %q", value)
		}
		c.data.Server.URL = value
	case "server.auth.enabled":
		c.data.Server.Auth.Enabled = value == "true"
	case "server.auth.jwt_secret":
		if len(value) < 32 {
			return fmt.Errorf("server.auth.jwt_secret must be at least 32 characters")
		}
		c.data.Server.Auth.JWTSecret = value

	// Database
	case "database.path":
		c.data.Database.Path = value
	case "database.driver":
		if value != "sqlite" {
			return fmt.Errorf("invalid database.driver %q: only \"sqlite\" is currently supported", value)
		}
		c.data.Database.Driver = value

	// Scheduler
	case "scheduler.gc.enabled":
		c.data.Scheduler.GC.Enabled = value == "true"
	case "scheduler.gc.cron":
		if err := validateCronExpr(value); err != nil {
			return fmt.Errorf("invalid scheduler.gc.cron %q: %w", value, err)
		}
		c.data.Scheduler.GC.Cron = value
	case "scheduler.gc.retention_days":
		v, err := strconv.Atoi(value)
		if err != nil || v < 1 {
			return fmt.Errorf("invalid scheduler.gc.retention_days %q: must be a positive integer", value)
		}
		c.data.Scheduler.GC.RetentionDays = v
	case "scheduler.capture_all.enabled":
		c.data.Scheduler.CaptureAll.Enabled = value == "true"
	case "scheduler.capture_all.cron":
		if err := validateCronExpr(value); err != nil {
			return fmt.Errorf("invalid scheduler.capture_all.cron %q: %w", value, err)
		}
		c.data.Scheduler.CaptureAll.Cron = value
	case "scheduler.stats_report.enabled":
		c.data.Scheduler.StatsReport.Enabled = value == "true"
	case "scheduler.stats_report.cron":
		if err := validateCronExpr(value); err != nil {
			return fmt.Errorf("invalid scheduler.stats_report.cron %q: %w", value, err)
		}
		c.data.Scheduler.StatsReport.Cron = value

	// Telemetry
	case "telemetry.enabled":
		c.data.Telemetry.Enabled = value == "true"

	default:
		// Dynamic keys: llm.providers.<name>.<field>, llm.profiles.<name>.<field>
		if c.setLLMDynamic(key, value) {
			return nil
		}
		return fmt.Errorf("unknown config key %q", key)
	}
	return nil
}

// setLLMDynamic handles dynamic dot-notation Set for LLM providers and profiles.
func (c *Config) setLLMDynamic(key, value string) bool {
	parts := strings.Split(key, ".")
	if len(parts) != 4 || parts[0] != "llm" {
		return false
	}

	section, name, field := parts[1], parts[2], parts[3]
	switch section {
	case "providers":
		if c.data.LLM.Providers == nil {
			c.data.LLM.Providers = make(map[string]llmProviderConf)
		}
		prov := c.data.LLM.Providers[name]
		switch field {
		case "url":
			prov.URL = value
		case "api_key":
			prov.APIKey = value
		default:
			return false
		}
		c.data.LLM.Providers[name] = prov
		return true

	case "profiles":
		if c.data.LLM.Profiles == nil {
			c.data.LLM.Profiles = make(map[string]llmProfile)
		}
		prof := c.data.LLM.Profiles[name]
		switch field {
		case "provider":
			validProviders := map[string]bool{"ollama": true, "anthropic": true, "opencode": true, "llm": true}
			if !validProviders[value] {
				return false
			}
			prof.Provider = value
		case "model":
			prof.Model = value
		default:
			return false
		}
		c.data.LLM.Profiles[name] = prof
		return true
	}
	return false
}

// GetProviders returns the list of enabled provider names.
func (c *Config) GetProviders() []session.ProviderName {
	result := make([]session.ProviderName, 0, len(c.data.Providers))
	for _, p := range c.data.Providers {
		name, err := session.ParseProviderName(p)
		if err == nil {
			result = append(result, name)
		}
	}
	return result
}

// GetStorageMode returns the default storage mode.
func (c *Config) GetStorageMode() session.StorageMode {
	mode, err := session.ParseStorageMode(c.data.StorageMode)
	if err != nil {
		return session.StorageModeCompact // safe default
	}
	return mode
}

// GetSecretsMode returns the secret detection mode.
func (c *Config) GetSecretsMode() session.SecretMode {
	mode, err := session.ParseSecretMode(c.data.Secrets.Mode)
	if err != nil {
		return session.SecretModeMask // safe default
	}
	return mode
}

// GetCustomPatterns returns the list of custom secret patterns (NAME REGEX format).
func (c *Config) GetCustomPatterns() []string {
	return c.data.Secrets.CustomPatterns
}

// IsSummarizeEnabled returns whether AI summarization is enabled by default.
func (c *Config) IsSummarizeEnabled() bool {
	return c.data.Summarize.Enabled
}

// GetSummarizeModel returns the model to use for AI summarization.
// Empty string means let the adapter pick the default.
func (c *Config) GetSummarizeModel() string {
	return c.data.Summarize.Model
}

// GetPricingOverrides returns the list of user-configured pricing overrides.
func (c *Config) GetPricingOverrides() []PricingOverride {
	return c.data.Pricing.Overrides
}

// AddPricingOverride adds or updates a model price override.
func (c *Config) AddPricingOverride(model string, inputPerMToken, outputPerMToken float64) {
	// Update existing or append new
	for i, o := range c.data.Pricing.Overrides {
		if o.Model == model {
			c.data.Pricing.Overrides[i].InputPerMToken = inputPerMToken
			c.data.Pricing.Overrides[i].OutputPerMToken = outputPerMToken
			return
		}
	}
	c.data.Pricing.Overrides = append(c.data.Pricing.Overrides, PricingOverride{
		Model:           model,
		InputPerMToken:  inputPerMToken,
		OutputPerMToken: outputPerMToken,
	})
}

// IsAnalysisAutoEnabled returns whether automatic analysis after capture is enabled.
func (c *Config) IsAnalysisAutoEnabled() bool {
	return c.data.Analysis.Auto
}

// GetAnalysisAdapter returns the configured analysis adapter name ("llm" or "opencode").
func (c *Config) GetAnalysisAdapter() string {
	if c.data.Analysis.Adapter == "" {
		return "llm"
	}
	return c.data.Analysis.Adapter
}

// GetAnalysisErrorThreshold returns the error rate threshold (percent) for auto-analysis.
func (c *Config) GetAnalysisErrorThreshold() float64 {
	if c.data.Analysis.ErrorThreshold <= 0 {
		return 20 // default
	}
	return c.data.Analysis.ErrorThreshold
}

// GetAnalysisMinToolCalls returns the minimum number of tool calls required for auto-analysis.
func (c *Config) GetAnalysisMinToolCalls() int {
	if c.data.Analysis.MinToolCalls <= 0 {
		return 5 // default
	}
	return c.data.Analysis.MinToolCalls
}

// GetAnalysisModel returns the optional model override for analysis.
func (c *Config) GetAnalysisModel() string {
	return c.data.Analysis.Model
}

// GetAnalysisOllamaURL returns the Ollama API base URL.
func (c *Config) GetAnalysisOllamaURL() string {
	if c.data.Analysis.OllamaURL == "" {
		return "http://localhost:11434"
	}
	return c.data.Analysis.OllamaURL
}

// GetAnalysisAPIKey returns the API key for Anthropic adapter.
func (c *Config) GetAnalysisAPIKey() string {
	return c.data.Analysis.APIKey
}

// GetAnalysisSchedule returns the cron expression for scheduled analysis.
func (c *Config) GetAnalysisSchedule() string {
	return c.data.Analysis.Schedule
}

// ── LLM Profile Resolution ──

// ResolveProfile resolves a named LLM profile to a fully-configured ResolvedProfile.
// Resolution order:
//  1. If profileName is non-empty and exists in llm.profiles → use it
//  2. If profileName is empty, check analysis.profile → use it
//  3. Fallback: build from legacy analysis.adapter + analysis.model keys
//
// Provider config (URL, API key) is resolved from llm.providers, falling back
// to legacy analysis.ollama_url / analysis.api_key.
func (c *Config) ResolveProfile(profileName string) ResolvedProfile {
	// Step 1: Try named profile.
	if profileName == "" {
		profileName = c.data.Analysis.Profile
	}
	if profileName != "" {
		if profile, ok := c.data.LLM.Profiles[profileName]; ok {
			resolved := ResolvedProfile{
				Provider: profile.Provider,
				Model:    profile.Model,
			}
			// Resolve provider infra.
			c.resolveProviderInfra(&resolved)
			return resolved
		}
	}

	// Step 2: Fallback to legacy flat config.
	resolved := ResolvedProfile{
		Provider: c.GetAnalysisAdapter(),
		Model:    c.GetAnalysisModel(),
	}
	c.resolveProviderInfra(&resolved)
	return resolved
}

// resolveProviderInfra fills URL and APIKey from llm.providers config,
// falling back to legacy analysis.* keys.
func (c *Config) resolveProviderInfra(r *ResolvedProfile) {
	// Try llm.providers first.
	if c.data.LLM.Providers != nil {
		if prov, ok := c.data.LLM.Providers[r.Provider]; ok {
			r.URL = prov.URL
			r.APIKey = prov.APIKey
			return
		}
	}

	// Fallback to legacy keys.
	switch r.Provider {
	case "ollama":
		r.URL = c.GetAnalysisOllamaURL()
	case "anthropic":
		r.APIKey = c.GetAnalysisAPIKey()
	}
}

// GetLLMProfiles returns all configured LLM profile names.
func (c *Config) GetLLMProfiles() map[string]llmProfile {
	if c.data.LLM.Profiles == nil {
		return nil
	}
	return c.data.LLM.Profiles
}

// GetLLMProviders returns all configured LLM provider names.
func (c *Config) GetLLMProviders() map[string]llmProviderConf {
	if c.data.LLM.Providers == nil {
		return nil
	}
	return c.data.LLM.Providers
}

// ── Tagging ──

// IsTaggingAutoEnabled returns whether auto-tagging after capture is enabled.
func (c *Config) IsTaggingAutoEnabled() bool {
	return c.data.Tagging.Auto
}

// GetTaggingProfile returns the LLM profile name for tagging.
func (c *Config) GetTaggingProfile() string {
	return c.data.Tagging.Profile
}

// GetTaggingTags returns the configured tag list, or defaults.
func (c *Config) GetTaggingTags() []string {
	if len(c.data.Tagging.Tags) == 0 {
		return session.DefaultSessionTypes
	}
	return c.data.Tagging.Tags
}

// ── Webhooks ──

// GetWebhookEntries returns the raw webhook configurations.
func (c *Config) GetWebhookEntries() []webhookEntry {
	return c.data.Webhooks.Hooks
}

// GetDashboardPageSize returns the number of sessions per page in the dashboard.
func (c *Config) GetDashboardPageSize() int {
	if c.data.Dashboard.PageSize <= 0 {
		return 25
	}
	return c.data.Dashboard.PageSize
}

// GetDashboardColumns returns the ordered list of visible column identifiers.
func (c *Config) GetDashboardColumns() []string {
	if len(c.data.Dashboard.Columns) == 0 {
		return DefaultDashboardColumns
	}
	return c.data.Dashboard.Columns
}

// GetDashboardSortBy returns the default sort field.
func (c *Config) GetDashboardSortBy() string {
	if c.data.Dashboard.SortBy == "" {
		return "created_at"
	}
	return c.data.Dashboard.SortBy
}

// GetDashboardSortOrder returns "asc" or "desc".
func (c *Config) GetDashboardSortOrder() string {
	if c.data.Dashboard.SortOrder == "" {
		return "desc"
	}
	return c.data.Dashboard.SortOrder
}

// GetDashboardDefaultProvider returns the pre-selected provider filter (empty = all).
func (c *Config) GetDashboardDefaultProvider() string {
	return c.data.Dashboard.DefaultProvider
}

// GetDashboardDefaultBranch returns the pre-selected branch filter (empty = all).
func (c *Config) GetDashboardDefaultBranch() string {
	return c.data.Dashboard.DefaultBranch
}

// ── Scheduler Getters ──

// GetSchedulerGCEnabled returns whether the GC scheduled task is enabled.
func (c *Config) GetSchedulerGCEnabled() bool {
	return c.data.Scheduler.GC.Enabled
}

// GetSchedulerGCCron returns the cron expression for the GC task.
// Returns the default "0 3 * * *" (3 AM daily) if not configured.
func (c *Config) GetSchedulerGCCron() string {
	if c.data.Scheduler.GC.Cron == "" {
		return "0 3 * * *"
	}
	return c.data.Scheduler.GC.Cron
}

// GetSchedulerGCRetentionDays returns the number of days to retain sessions.
// Returns the default 90 if not configured.
func (c *Config) GetSchedulerGCRetentionDays() int {
	if c.data.Scheduler.GC.RetentionDays <= 0 {
		return 90
	}
	return c.data.Scheduler.GC.RetentionDays
}

// GetSchedulerCaptureAllEnabled returns whether the capture-all scheduled task is enabled.
func (c *Config) GetSchedulerCaptureAllEnabled() bool {
	return c.data.Scheduler.CaptureAll.Enabled
}

// GetSchedulerCaptureAllCron returns the cron expression for the capture-all task.
// Returns the default "*/30 * * * *" (every 30 minutes) if not configured.
func (c *Config) GetSchedulerCaptureAllCron() string {
	if c.data.Scheduler.CaptureAll.Cron == "" {
		return "*/30 * * * *"
	}
	return c.data.Scheduler.CaptureAll.Cron
}

// GetSchedulerStatsReportEnabled returns whether the stats-report scheduled task is enabled.
func (c *Config) GetSchedulerStatsReportEnabled() bool {
	return c.data.Scheduler.StatsReport.Enabled
}

// GetSchedulerStatsReportCron returns the cron expression for the stats-report task.
// Returns the default "0 * * * *" (hourly) if not configured.
func (c *Config) GetSchedulerStatsReportCron() string {
	if c.data.Scheduler.StatsReport.Cron == "" {
		return "0 * * * *"
	}
	return c.data.Scheduler.StatsReport.Cron
}

// validateCronExpr validates a 5-field cron expression using robfig/cron/v3 parser.
func validateCronExpr(expr string) error {
	if expr == "" {
		return nil // empty is valid (means "use default")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	_, err := parser.Parse(expr)
	return err
}

// GetServerURL returns the aisync server URL for remote mode.
// Empty string means standalone/local mode.
// The AISYNC_SERVER_URL env var takes precedence over the config file.
func (c *Config) GetServerURL() string {
	if env := os.Getenv("AISYNC_SERVER_URL"); env != "" {
		return env
	}
	return c.data.Server.URL
}

// GetDatabasePath returns the path to the SQLite database file.
// Empty string means use the default path (~/.aisync/sessions.db).
// The AISYNC_DATABASE_PATH env var takes precedence over the config file.
func (c *Config) GetDatabasePath() string {
	if env := os.Getenv("AISYNC_DATABASE_PATH"); env != "" {
		return env
	}
	return c.data.Database.Path
}

// GetDatabaseDriver returns the configured database driver.
// Returns "sqlite" as the default if not configured.
func (c *Config) GetDatabaseDriver() string {
	if c.data.Database.Driver == "" {
		return "sqlite"
	}
	return c.data.Database.Driver
}

// ── Project Getters ──

// GetProjectCategory returns the configured project category (e.g. "backend", "frontend").
// Returns "" if not configured.
func (c *Config) GetProjectCategory() string {
	return c.data.Project.Category
}

// GetProjectCategories returns the list of valid project categories.
// Returns DefaultProjectCategories if no custom list is configured.
func (c *Config) GetProjectCategories() []string {
	if len(c.data.Project.Categories) > 0 {
		return c.data.Project.Categories
	}
	return session.DefaultProjectCategories
}

// IsProjectAutoDetectEnabled returns whether auto-detection of project category is enabled.
func (c *Config) IsProjectAutoDetectEnabled() bool {
	return c.data.Project.AutoDetect
}

// IsAuthEnabled returns true if authentication is enabled on the server.
// The AISYNC_AUTH_ENABLED env var takes precedence over the config file.
func (c *Config) IsAuthEnabled() bool {
	if env := os.Getenv("AISYNC_AUTH_ENABLED"); env != "" {
		return env == "true" || env == "1"
	}
	return c.data.Server.Auth.Enabled
}

// GetJWTSecret returns the JWT signing secret.
// The AISYNC_JWT_SECRET env var takes precedence over the config file.
func (c *Config) GetJWTSecret() string {
	if env := os.Getenv("AISYNC_JWT_SECRET"); env != "" {
		return env
	}
	return c.data.Server.Auth.JWTSecret
}

// ── Telemetry ──

// IsTelemetryEnabled returns whether opt-in anonymous usage statistics are enabled.
// Telemetry is disabled by default.
func (c *Config) IsTelemetryEnabled() bool {
	return c.data.Telemetry.Enabled
}

// AddCustomPattern adds a custom secret pattern in "NAME REGEX" format.
func (c *Config) AddCustomPattern(pattern string) {
	c.data.Secrets.CustomPatterns = append(c.data.Secrets.CustomPatterns, pattern)
}

// Save persists the configuration to disk.
// If repoDir is set, saves there. Otherwise saves to globalDir.
func (c *Config) Save() error {
	dir := c.repoDir
	if dir == "" {
		dir = c.globalDir
	}
	if dir == "" {
		return fmt.Errorf("no config directory specified")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := json.MarshalIndent(c.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
