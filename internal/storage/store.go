package storage

import "github.com/ChristopherAparicio/aisync/internal/session"

// Store persists sessions and users in local storage.
// Current implementation: sqlite/.
type Store interface {
	// Save stores a session. If a session with the same ID exists, it is replaced.
	Save(s *session.Session) error

	// Get retrieves a session by its ID.
	// Returns ErrSessionNotFound if the session does not exist.
	Get(id session.ID) (*session.Session, error)

	// GetByBranch retrieves the most recent session for a project and branch.
	// Returns ErrSessionNotFound if no session matches.
	GetByBranch(projectPath string, branch string) (*session.Session, error)

	// List returns session summaries matching the given options.
	List(opts session.ListOptions) ([]session.Summary, error)

	// Delete removes a session by its ID.
	// Returns ErrSessionNotFound if the session does not exist.
	Delete(id session.ID) error

	// AddLink associates a session with a git object (branch, commit, PR).
	AddLink(sessionID session.ID, link session.Link) error

	// GetByLink retrieves sessions linked to a specific ref (PR number, commit SHA, etc.).
	// Returns ErrSessionNotFound if no sessions match.
	GetByLink(linkType session.LinkType, ref string) ([]session.Summary, error)

	// SaveUser creates or updates a user. If a user with the same email exists,
	// it returns the existing user (upsert by email).
	SaveUser(user *session.User) error

	// GetUser retrieves a user by ID.
	GetUser(id session.ID) (*session.User, error)

	// GetUserByEmail retrieves a user by email address.
	// Returns nil, nil if no user matches.
	GetUserByEmail(email string) (*session.User, error)

	// Search returns sessions matching the given query criteria.
	// Supports filtering by branch, provider, owner, time range,
	// and keyword search across summary content.
	Search(query session.SearchQuery) (*session.SearchResult, error)

	// Close releases any resources held by the store.
	Close() error
}
