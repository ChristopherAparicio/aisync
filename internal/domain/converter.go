package domain

// Converter transforms sessions between the unified format and provider-native formats.
type Converter interface {
	// ToNative converts a unified Session to the native format of a target provider.
	// Returns the raw bytes (JSONL for Claude Code, JSON for OpenCode, etc.).
	ToNative(session *Session, target ProviderName) ([]byte, error)

	// FromNative parses raw provider-native data into a unified Session.
	FromNative(data []byte, source ProviderName) (*Session, error)

	// SupportedFormats returns which provider conversions are available.
	SupportedFormats() []ProviderName
}
