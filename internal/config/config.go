// Package config implements the aisync configuration layer.
// Config supports two levels: global (~/.aisync/) and per-repo (.aisync/).
// Per-repo values override global values.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

const configFileName = "config.json"

// configData is the JSON-serializable configuration structure.
type configData struct {
	StorageMode string   `json:"storage_mode"`
	Providers   []string `json:"providers"`
	Secrets     secrets  `json:"secrets"`
	Version     int      `json:"version"`
	AutoCapture bool     `json:"auto_capture"`
}

type secrets struct {
	Mode            string   `json:"mode"`
	CustomPatterns  []string `json:"custom_patterns"`
	IgnorePatterns  []string `json:"ignore_patterns"`
	ScanToolOutputs bool     `json:"scan_tool_outputs"`
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
	}
}

// Config implements domain.Config using JSON files.
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
	default:
		return "", fmt.Errorf("unknown config key %q", key)
	}
}

// Set updates a configuration value.
func (c *Config) Set(key string, value string) error {
	switch key {
	case "storage_mode":
		if _, err := domain.ParseStorageMode(value); err != nil {
			return err
		}
		c.data.StorageMode = value
	case "secrets.mode":
		if _, err := domain.ParseSecretMode(value); err != nil {
			return err
		}
		c.data.Secrets.Mode = value
	case "secrets.custom_patterns.add":
		c.data.Secrets.CustomPatterns = append(c.data.Secrets.CustomPatterns, value)
	case "auto_capture":
		c.data.AutoCapture = value == "true"
	default:
		return fmt.Errorf("unknown config key %q", key)
	}
	return nil
}

// GetProviders returns the list of enabled provider names.
func (c *Config) GetProviders() []domain.ProviderName {
	result := make([]domain.ProviderName, 0, len(c.data.Providers))
	for _, p := range c.data.Providers {
		name, err := domain.ParseProviderName(p)
		if err == nil {
			result = append(result, name)
		}
	}
	return result
}

// GetStorageMode returns the default storage mode.
func (c *Config) GetStorageMode() domain.StorageMode {
	mode, err := domain.ParseStorageMode(c.data.StorageMode)
	if err != nil {
		return domain.StorageModeCompact // safe default
	}
	return mode
}

// GetSecretsMode returns the secret detection mode.
func (c *Config) GetSecretsMode() domain.SecretMode {
	mode, err := domain.ParseSecretMode(c.data.Secrets.Mode)
	if err != nil {
		return domain.SecretModeMask // safe default
	}
	return mode
}

// GetCustomPatterns returns the list of custom secret patterns (NAME REGEX format).
func (c *Config) GetCustomPatterns() []string {
	return c.data.Secrets.CustomPatterns
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
