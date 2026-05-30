// Package hermes implements the Hermes provider for aisync.
package hermes

import "database/sql"

// reader is the internal interface for reading raw Hermes session data.
// Only one implementation exists: dbReader (SQLite against ~/.hermes/state.db).
// All methods return raw Hermes row structs — no domain mapping occurs here.
type reader interface {
	// listSessions returns all non-deleted sessions ordered by started_at DESC.
	listSessions() ([]hermesSession, error)

	// readSession returns a single session by its ID.
	readSession(id string) (*hermesSession, error)

	// listMessages returns all messages for a session, ordered by rowid ASC.
	listMessages(sessionID string) ([]hermesMessage, error)

	// findChildSessions returns all sessions whose parent_session_id equals parentID.
	findChildSessions(parentID string) ([]hermesSession, error)

	// close releases the underlying database connection.
	close() error
}

// hermesSession mirrors one row from the Hermes `sessions` table (schema v14).
type hermesSession struct {
	ID               string
	Source           string
	UserID           string
	Model            string
	ParentSessionID  sql.NullString
	StartedAt        float64
	EndedAt          sql.NullFloat64
	EndReason        sql.NullString
	MessageCount     int
	ToolCallCount    int
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	ReasoningTokens  int
	BillingProvider  sql.NullString
	BillingBaseURL   sql.NullString
	BillingMode      sql.NullString
	EstimatedCostUSD sql.NullFloat64
	ActualCostUSD    sql.NullFloat64
	CostStatus       sql.NullString
	Title            sql.NullString
}

// hermesMessage mirrors one row from the Hermes `messages` table (schema v14).
// Content may carry a sentinel prefix used by Hermes internally; callers are
// responsible for stripping it during domain mapping (not here).
// ToolCalls holds a JSON array when present.
type hermesMessage struct {
	ID               string
	SessionID        string
	Role             string
	Content          sql.NullString
	ToolCallID       sql.NullString
	ToolCalls        sql.NullString // JSON array
	ToolName         sql.NullString
	Timestamp        float64
	TokenCount       int
	FinishReason     sql.NullString
	Reasoning        sql.NullString
	ReasoningContent sql.NullString
}
