package provider

import "github.com/ChristopherAparicio/aisync/internal/session"

// Provider reads and writes sessions from/to an AI coding tool.
// Three implementations exist: claude/, opencode/, cursor/.
type Provider interface {
	// Name returns the provider identifier.
	Name() session.ProviderName

	// Detect finds sessions matching the given project and branch.
	// Returns summaries of detected sessions, most recent first.
	Detect(projectPath string, branch string) ([]session.Summary, error)

	// Export reads a session and converts it to the unified format.
	Export(sessionID session.ID, mode session.StorageMode) (*session.Session, error)

	// CanImport reports whether this provider supports session import.
	CanImport() bool

	// Import injects a session into the provider's native storage.
	// Returns ErrImportNotSupported if CanImport() is false.
	Import(s *session.Session) error
}
