// Package opencode implements the OpenCode provider for aisync.
package opencode

// reader is the internal interface for reading OpenCode sessions.
// Two implementations exist: dbReader (SQLite) and fileReader (JSON files).
// The Provider tries dbReader first, falling back to fileReader when the DB
// is unavailable.
type reader interface {
	// findProjectID returns the project ID for a given worktree path.
	findProjectID(worktreePath string) (string, error)

	// listSessions returns all non-child sessions for a project.
	listSessions(projectID string) ([]ocSession, error)

	// readSession reads a single session by its ID.
	readSession(sessionID string) (*ocSession, error)

	// loadMessages returns all messages for a session, sorted by creation time.
	loadMessages(sessionID string) ([]ocMessage, error)

	// loadParts returns all parts for a message.
	loadParts(messageID string) ([]ocPart, error)

	// countMessages returns the number of messages for a session.
	countMessages(sessionID string) int

	// findChildSessions returns session metadata for all children of a parent.
	findChildSessions(parentID string) ([]ocSession, error)

	// sessionUpdatedAt returns the session's last-updated timestamp (epoch ms).
	// Returns 0 if unknown.
	sessionUpdatedAt(sessionID string) int64
}
