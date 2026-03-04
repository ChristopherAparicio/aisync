package client

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// ── Domain types ──
// These are client-side copies of the domain types. They mirror the JSON
// shapes returned by the API, decoupling the client from internal packages.
// This lets the client/ package be imported without pulling in internal/.

// Session represents a captured AI coding session.
type Session struct {
	ExportedAt  time.Time    `json:"exported_at"`
	CreatedAt   time.Time    `json:"created_at"`
	ProjectPath string       `json:"project_path"`
	ExportedBy  string       `json:"exported_by,omitempty"`
	ParentID    string       `json:"parent_id,omitempty"`
	OwnerID     string       `json:"owner_id,omitempty"`
	StorageMode string       `json:"storage_mode"`
	Summary     string       `json:"summary,omitempty"`
	ID          string       `json:"id"`
	Provider    string       `json:"provider"`
	Agent       string       `json:"agent"`
	Branch      string       `json:"branch,omitempty"`
	CommitSHA   string       `json:"commit_sha,omitempty"`
	Messages    []Message    `json:"messages,omitempty"`
	Links       []Link       `json:"links,omitempty"`
	FileChanges []FileChange `json:"file_changes,omitempty"`
	TokenUsage  TokenUsage   `json:"token_usage"`
	Version     int          `json:"version"`
}

// Summary is a lightweight session representation for listings.
type Summary struct {
	CreatedAt    time.Time `json:"created_at"`
	ID           string    `json:"id"`
	OwnerID      string    `json:"owner_id,omitempty"`
	Provider     string    `json:"provider"`
	Agent        string    `json:"agent"`
	Branch       string    `json:"branch,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	MessageCount int       `json:"message_count"`
	TotalTokens  int       `json:"total_tokens"`
}

// Message represents a single message in an AI conversation.
type Message struct {
	Timestamp time.Time  `json:"timestamp"`
	ID        string     `json:"id"`
	Content   string     `json:"content"`
	Model     string     `json:"model,omitempty"`
	Thinking  string     `json:"thinking,omitempty"`
	Role      string     `json:"role"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Tokens    int        `json:"tokens,omitempty"`
}

// ToolCall represents a tool invocation.
type ToolCall struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Input      string `json:"input"`
	Output     string `json:"output,omitempty"`
	State      string `json:"state"`
	DurationMs int    `json:"duration_ms,omitempty"`
}

// FileChange records a file touched during a session.
type FileChange struct {
	FilePath   string `json:"file_path"`
	ChangeType string `json:"change_type"`
}

// Link connects a session to a git object.
type Link struct {
	LinkType string `json:"link_type"`
	Ref      string `json:"ref"`
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ── Capture ──

// CaptureRequest contains inputs for a capture operation.
type CaptureRequest struct {
	ProjectPath string `json:"project_path"`
	Branch      string `json:"branch"`
	Mode        string `json:"mode,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Message     string `json:"message,omitempty"`
}

// CaptureResult contains the output of a capture operation.
type CaptureResult struct {
	Session      *Session `json:"Session"`
	Provider     string   `json:"Provider"`
	SecretsFound int      `json:"SecretsFound"`
}

// Capture detects the active AI session, exports it, and stores it.
func (c *Client) Capture(req CaptureRequest) (*CaptureResult, error) {
	data, err := c.doPost("/api/v1/sessions/capture", req)
	if err != nil {
		return nil, err
	}
	var result CaptureResult
	return &result, decode(data, &result)
}

// ── Restore ──

// RestoreRequest contains inputs for a restore operation.
type RestoreRequest struct {
	ProjectPath string `json:"project_path"`
	Branch      string `json:"branch"`
	Agent       string `json:"agent,omitempty"`
	SessionID   string `json:"session_id"`
	Provider    string `json:"provider,omitempty"`
	AsContext   bool   `json:"as_context,omitempty"`
	PRNumber    int    `json:"pr_number,omitempty"`
}

// RestoreResult contains the output of a restore operation.
type RestoreResult struct {
	Session     *Session `json:"Session"`
	Method      string   `json:"Method"`
	ContextPath string   `json:"ContextPath"`
}

// Restore looks up a session and imports it into a target provider.
func (c *Client) Restore(req RestoreRequest) (*RestoreResult, error) {
	data, err := c.doPost("/api/v1/sessions/restore", req)
	if err != nil {
		return nil, err
	}
	var result RestoreResult
	return &result, decode(data, &result)
}

// ── Get ──

// Get retrieves a session by ID or commit SHA.
func (c *Client) Get(idOrSHA string) (*Session, error) {
	data, err := c.doGet("/api/v1/sessions/" + url.PathEscape(idOrSHA))
	if err != nil {
		return nil, err
	}
	var sess Session
	return &sess, decode(data, &sess)
}

// ── List ──

// ListOptions controls session listing queries.
type ListOptions struct {
	ProjectPath string
	Branch      string
	Provider    string
	PRNumber    int
	All         bool
}

// List returns session summaries matching the given criteria.
func (c *Client) List(opts ListOptions) ([]Summary, error) {
	q := url.Values{}
	if opts.ProjectPath != "" {
		q.Set("project_path", opts.ProjectPath)
	}
	if opts.Branch != "" {
		q.Set("branch", opts.Branch)
	}
	if opts.Provider != "" {
		q.Set("provider", opts.Provider)
	}
	if opts.PRNumber > 0 {
		q.Set("pr", strconv.Itoa(opts.PRNumber))
	}
	if opts.All {
		q.Set("all", "true")
	}

	path := "/api/v1/sessions"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	data, err := c.doGet(path)
	if err != nil {
		return nil, err
	}
	var summaries []Summary
	return summaries, decode(data, &summaries)
}

// ── Delete ──

// Delete removes a session by ID.
func (c *Client) Delete(id string) error {
	_, err := c.doDelete("/api/v1/sessions/" + url.PathEscape(id))
	return err
}

// ── Export ──

// ExportRequest contains inputs for exporting a session.
type ExportRequest struct {
	SessionID   string `json:"session_id,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Format      string `json:"format,omitempty"`
}

// ExportResult contains the exported data.
type ExportResult struct {
	Data      []byte // decoded from base64
	Format    string
	SessionID string
}

// Export converts a session to the requested format.
func (c *Client) Export(req ExportRequest) (*ExportResult, error) {
	data, err := c.doPost("/api/v1/sessions/export", req)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Data      string `json:"data"`
		Format    string `json:"format"`
		SessionID string `json:"session_id"`
	}
	if err := decode(data, &raw); err != nil {
		return nil, err
	}

	decoded, err := base64.StdEncoding.DecodeString(raw.Data)
	if err != nil {
		return nil, fmt.Errorf("decode export data: %w", err)
	}

	return &ExportResult{
		Data:      decoded,
		Format:    raw.Format,
		SessionID: raw.SessionID,
	}, nil
}

// ── Import ──

// ImportRequest contains inputs for importing a session.
type ImportRequest struct {
	Data         []byte // raw file contents (will be base64-encoded for transport)
	SourceFormat string
	IntoTarget   string
}

// ImportResult contains the outcome of an import.
type ImportResult struct {
	SessionID    string `json:"SessionID"`
	SourceFormat string `json:"SourceFormat"`
	Target       string `json:"Target"`
}

// Import parses raw data and stores or injects the session.
func (c *Client) Import(req ImportRequest) (*ImportResult, error) {
	body := struct {
		Data         string `json:"data"`
		SourceFormat string `json:"source_format,omitempty"`
		IntoTarget   string `json:"into_target,omitempty"`
	}{
		Data:         base64.StdEncoding.EncodeToString(req.Data),
		SourceFormat: req.SourceFormat,
		IntoTarget:   req.IntoTarget,
	}

	data, err := c.doPost("/api/v1/sessions/import", body)
	if err != nil {
		return nil, err
	}
	var result ImportResult
	return &result, decode(data, &result)
}

// ── Link ──

// LinkRequest contains inputs for linking a session.
type LinkRequest struct {
	SessionID   string `json:"session_id,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PRNumber    int    `json:"pr_number,omitempty"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	AutoDetect  bool   `json:"auto_detect,omitempty"`
}

// LinkResult contains the outcome of a link operation.
type LinkResult struct {
	SessionID string `json:"SessionID"`
	PRNumber  int    `json:"PRNumber"`
	CommitSHA string `json:"CommitSHA"`
}

// Link associates a session with a PR, commit, or other git object.
func (c *Client) Link(req LinkRequest) (*LinkResult, error) {
	data, err := c.doPost("/api/v1/sessions/link", req)
	if err != nil {
		return nil, err
	}
	var result LinkResult
	return &result, decode(data, &result)
}

// ── Comment ──

// CommentRequest contains inputs for posting a PR comment.
type CommentRequest struct {
	SessionID   string `json:"session_id,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PRNumber    int    `json:"pr_number,omitempty"`
}

// CommentResult contains the outcome of a comment operation.
type CommentResult struct {
	PRNumber int  `json:"PRNumber"`
	Updated  bool `json:"Updated"`
}

// Comment posts or updates a PR comment with an AI session summary.
func (c *Client) Comment(req CommentRequest) (*CommentResult, error) {
	data, err := c.doPost("/api/v1/sessions/comment", req)
	if err != nil {
		return nil, err
	}
	var result CommentResult
	return &result, decode(data, &result)
}

// ── Search ──

// SearchOptions contains inputs for searching sessions.
type SearchOptions struct {
	Keyword     string
	ProjectPath string
	Branch      string
	Provider    string
	OwnerID     string
	Since       string // RFC3339 or "2006-01-02"
	Until       string // RFC3339 or "2006-01-02"
	Limit       int
	Offset      int
}

// SearchResult contains paginated search results.
type SearchResult struct {
	Sessions   []Summary `json:"sessions"`
	TotalCount int       `json:"total_count"`
	Limit      int       `json:"limit"`
	Offset     int       `json:"offset"`
}

// Search finds sessions matching the given criteria.
func (c *Client) Search(opts SearchOptions) (*SearchResult, error) {
	q := url.Values{}
	if opts.Keyword != "" {
		q.Set("keyword", opts.Keyword)
	}
	if opts.ProjectPath != "" {
		q.Set("project_path", opts.ProjectPath)
	}
	if opts.Branch != "" {
		q.Set("branch", opts.Branch)
	}
	if opts.Provider != "" {
		q.Set("provider", opts.Provider)
	}
	if opts.OwnerID != "" {
		q.Set("owner_id", opts.OwnerID)
	}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Until != "" {
		q.Set("until", opts.Until)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Offset > 0 {
		q.Set("offset", strconv.Itoa(opts.Offset))
	}

	path := "/api/v1/sessions/search"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	data, err := c.doGet(path)
	if err != nil {
		return nil, err
	}
	var result SearchResult
	return &result, decode(data, &result)
}

// ── Blame ──

// BlameOptions controls blame lookup queries.
type BlameOptions struct {
	File     string // required — file path
	Branch   string // optional filter
	Provider string // optional filter
	All      bool   // true = all sessions; false = most recent only
}

// BlameEntry represents one session that touched a file.
type BlameEntry struct {
	CreatedAt  time.Time `json:"created_at"`
	SessionID  string    `json:"session_id"`
	OwnerID    string    `json:"owner_id,omitempty"`
	Provider   string    `json:"provider"`
	Branch     string    `json:"branch"`
	Summary    string    `json:"summary,omitempty"`
	ChangeType string    `json:"change_type"`
}

// BlameResult contains the outcome of a blame lookup.
type BlameResult struct {
	Entries  []BlameEntry   `json:"Entries"`
	Restored *RestoreResult `json:"Restored,omitempty"`
	FilePath string         `json:"FilePath"`
}

// Blame finds AI sessions that touched the given file.
func (c *Client) Blame(opts BlameOptions) (*BlameResult, error) {
	if opts.File == "" {
		return nil, fmt.Errorf("file parameter is required")
	}

	q := url.Values{}
	q.Set("file", opts.File)
	if opts.Branch != "" {
		q.Set("branch", opts.Branch)
	}
	if opts.Provider != "" {
		q.Set("provider", opts.Provider)
	}
	if opts.All {
		q.Set("all", "true")
	}

	path := "/api/v1/blame?" + q.Encode()

	data, err := c.doGet(path)
	if err != nil {
		return nil, err
	}
	var result BlameResult
	return &result, decode(data, &result)
}

// ── Stats ──

// StatsOptions controls statistics queries.
type StatsOptions struct {
	ProjectPath string
	Branch      string
	Provider    string
	All         bool
}

// BranchStats holds aggregated stats per branch.
type BranchStats struct {
	Branch       string `json:"Branch"`
	TotalTokens  int    `json:"TotalTokens"`
	SessionCount int    `json:"SessionCount"`
}

// FileEntry is a file path with its touch count.
type FileEntry struct {
	Path  string `json:"Path"`
	Count int    `json:"Count"`
}

// StatsResult contains aggregated statistics.
type StatsResult struct {
	TotalSessions int            `json:"TotalSessions"`
	TotalMessages int            `json:"TotalMessages"`
	TotalTokens   int            `json:"TotalTokens"`
	PerBranch     []*BranchStats `json:"PerBranch"`
	PerProvider   map[string]int `json:"PerProvider"`
	TopFiles      []FileEntry    `json:"TopFiles"`
}

// Stats computes aggregated statistics across sessions.
func (c *Client) Stats(opts StatsOptions) (*StatsResult, error) {
	q := url.Values{}
	if opts.ProjectPath != "" {
		q.Set("project_path", opts.ProjectPath)
	}
	if opts.Branch != "" {
		q.Set("branch", opts.Branch)
	}
	if opts.Provider != "" {
		q.Set("provider", opts.Provider)
	}
	if opts.All {
		q.Set("all", "true")
	}

	path := "/api/v1/stats"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	data, err := c.doGet(path)
	if err != nil {
		return nil, err
	}
	var result StatsResult
	return &result, decode(data, &result)
}
