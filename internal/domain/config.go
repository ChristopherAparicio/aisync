package domain

// Config provides access to aisync configuration values.
// Configuration is two-level: global (~/.aisync/) + per-repo (.aisync/).
// Per-repo values override global values.
type Config interface {
	// Get retrieves a configuration value by key.
	Get(key string) (string, error)

	// Set updates a configuration value.
	Set(key string, value string) error

	// GetProviders returns the list of enabled provider names.
	GetProviders() []ProviderName

	// GetStorageMode returns the default storage mode.
	GetStorageMode() StorageMode

	// GetSecretsMode returns the secret detection mode.
	GetSecretsMode() SecretMode

	// Save persists the current configuration to disk.
	Save() error
}
