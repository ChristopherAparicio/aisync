package domain

// Store persists sessions in local storage.
type Store interface {
	// Save stores a session. If a session with the same ID exists, it is replaced.
	Save(session *Session) error

	// Get retrieves a session by its ID.
	// Returns ErrSessionNotFound if the session does not exist.
	Get(id SessionID) (*Session, error)

	// GetByBranch retrieves the most recent session for a project and branch.
	// Returns ErrSessionNotFound if no session matches.
	GetByBranch(projectPath string, branch string) (*Session, error)

	// List returns session summaries matching the given options.
	List(opts ListOptions) ([]SessionSummary, error)

	// Delete removes a session by its ID.
	// Returns ErrSessionNotFound if the session does not exist.
	Delete(id SessionID) error

	// AddLink associates a session with a git object (branch, commit, PR).
	AddLink(sessionID SessionID, link Link) error

	// GetByLink retrieves sessions linked to a specific ref (PR number, commit SHA, etc.).
	// Returns ErrSessionNotFound if no sessions match.
	GetByLink(linkType LinkType, ref string) ([]SessionSummary, error)

	// Close releases any resources held by the store.
	Close() error
}
