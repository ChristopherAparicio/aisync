package domain

// SecretScanner detects and handles secrets in session content.
type SecretScanner interface {
	// Scan checks content for secrets and returns all matches found.
	Scan(content string) []SecretMatch

	// Mask replaces detected secrets with redacted placeholders.
	// Returns the content with secrets replaced by ***REDACTED:TYPE***.
	Mask(content string) string

	// Mode returns the current secret handling mode.
	Mode() SecretMode
}

// SecretMatch represents a single secret detected in content.
type SecretMatch struct {
	// Type is the category of secret (e.g., "AWS_ACCESS_KEY", "GITHUB_TOKEN").
	Type string `json:"type"`

	// Value is the detected secret value.
	Value string `json:"value"`

	// StartPos is the byte offset where the secret starts in the content.
	StartPos int `json:"start_pos"`

	// EndPos is the byte offset where the secret ends in the content.
	EndPos int `json:"end_pos"`
}
