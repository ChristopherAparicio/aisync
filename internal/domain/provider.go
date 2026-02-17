package domain

// Provider reads and writes sessions from/to an AI coding tool.
type Provider interface {
	// Name returns the provider identifier.
	Name() ProviderName

	// Detect finds sessions matching the given project and branch.
	// Returns summaries of detected sessions, most recent first.
	Detect(projectPath string, branch string) ([]SessionSummary, error)

	// Export reads a session and converts it to the unified format.
	Export(sessionID SessionID, mode StorageMode) (*Session, error)

	// CanImport reports whether this provider supports session import.
	CanImport() bool

	// Import injects a session into the provider's native storage.
	// Returns ErrImportNotSupported if CanImport() is false.
	Import(session *Session) error
}
