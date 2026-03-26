package session

import "errors"

// Sentinel errors for expected failures.
var (
	// ErrSessionNotFound is returned when a session lookup yields no results.
	ErrSessionNotFound = errors.New("session not found")

	// ErrProviderNotDetected is returned when no AI provider is found for the project.
	ErrProviderNotDetected = errors.New("no AI provider detected")

	// ErrImportNotSupported is returned when a provider does not support session import.
	ErrImportNotSupported = errors.New("provider does not support session import")

	// ErrSecretDetected is returned in block mode when secrets are found.
	ErrSecretDetected = errors.New("secrets detected in session content")

	// ErrConfigNotFound is returned when no config file exists at the expected path.
	ErrConfigNotFound = errors.New("config file not found")

	// ErrPRNotFound is returned when a pull request cannot be found.
	ErrPRNotFound = errors.New("pull request not found")

	// ErrPlatformNotDetected is returned when the code hosting platform cannot be determined.
	ErrPlatformNotDetected = errors.New("code platform not detected")

	// ErrContextTooLarge is returned when a generated CONTEXT.md would exceed
	// the safe size limit for AI context windows.
	ErrContextTooLarge = errors.New("CONTEXT.md too large for AI context window")
)
