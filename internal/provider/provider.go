package provider

import (
	"errors"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ErrIncrementalNotPossible is returned by IncrementalExporter when
// the provider cannot perform an incremental export (e.g., messages
// were deleted/reordered, or the offset is invalid).
// The capture service falls back to full Export() on this error.
var ErrIncrementalNotPossible = errors.New("incremental export not possible")

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

// ProjectInfo holds metadata about a project discovered in a provider.
type ProjectInfo struct {
	ID           string // provider-native project ID
	Path         string // worktree / project path on disk
	SessionCount int    // number of sessions for this project
}

// ProjectDiscoverer is an optional interface that providers can implement
// to support bulk discovery of all projects and their session counts.
// Used by `aisync import --discover` to list everything available.
type ProjectDiscoverer interface {
	// ListAllProjects returns all projects known to this provider.
	ListAllProjects() ([]ProjectInfo, error)
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

// IncrementalResult holds the output of an incremental export.
// It contains only the NEW messages since the last capture, plus
// updated session-level metadata (token totals, updated-at, errors).
type IncrementalResult struct {
	// NewMessages contains only messages added since messageOffset.
	NewMessages []session.Message

	// UpdatedAt is the source session's latest updated-at timestamp.
	UpdatedAt int64

	// TokenUsage is the full session's token usage (recomputed by the provider).
	TokenUsage session.TokenUsage

	// Errors are the full session's structured errors (recomputed).
	Errors []session.SessionError

	// Children are the full child sessions (always fully re-exported).
	// Incremental child export is not supported — children are typically small.
	Children []session.Session
}

// IncrementalExporter is an optional interface that providers can implement
// to support incremental captures. Instead of re-reading all messages,
// the provider returns only messages added since the given offset.
//
// This is a performance optimization for large sessions: a 500-message
// session that gained 5 new messages only reads those 5 instead of all 500.
//
// The capture service checks for this via type assertion. If unavailable
// or if the incremental export fails, it falls back to full Export().
type IncrementalExporter interface {
	// ExportIncremental reads only messages added after messageOffset.
	// messageOffset is the number of messages already captured (0-based count).
	// mode controls the storage fidelity (compact/full/summary).
	//
	// Returns an IncrementalResult with only the new messages.
	// Returns ErrIncrementalNotPossible if the provider cannot perform
	// an incremental export (e.g., messages were deleted/reordered).
	ExportIncremental(sessionID session.ID, messageOffset int, mode session.StorageMode) (*IncrementalResult, error)
}
