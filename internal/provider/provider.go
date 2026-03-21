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

// Freshness holds the minimal data needed to determine if a session
// has changed since the last capture. Used by the skip-if-unchanged
// optimization to avoid re-exporting unmodified sessions.
type Freshness struct {
	MessageCount int   // number of messages in the source session
	UpdatedAt    int64 // source session's last update timestamp (epoch ms)
}

// FreshnessChecker is an optional interface that providers can implement
// to support the skip-if-unchanged optimization. The capture service
// checks for this via type assertion before calling Export.
type FreshnessChecker interface {
	// SessionFreshness returns the message count and last-updated timestamp
	// for a session directly from the source, without performing a full export.
	// Returns an error if the session doesn't exist or can't be queried.
	SessionFreshness(sessionID session.ID) (*Freshness, error)
}
