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
	ExportedAt      time.Time    `json:"exported_at"`
	CreatedAt       time.Time    `json:"created_at"`
	ProjectPath     string       `json:"project_path"`
	ExportedBy      string       `json:"exported_by,omitempty"`
	ParentID        string       `json:"parent_id,omitempty"`
	OwnerID         string       `json:"owner_id,omitempty"`
	StorageMode     string       `json:"storage_mode"`
	Summary         string       `json:"summary,omitempty"`
	ID              string       `json:"id"`
	Provider        string       `json:"provider"`
	Agent           string       `json:"agent"`
	Branch          string       `json:"branch,omitempty"`
	CommitSHA       string       `json:"commit_sha,omitempty"`
	Messages        []Message    `json:"messages,omitempty"`
	Links           []Link       `json:"links,omitempty"`
	FileChanges     []FileChange `json:"file_changes,omitempty"`
	TokenUsage      TokenUsage   `json:"token_usage"`
	ForkedAtMessage int          `json:"forked_at_message,omitempty"`
	Version         int          `json:"version"`
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

// ── Ingest ──

// IngestRequest contains inputs for a session ingest (push) operation.
type IngestRequest struct {
	Provider    string          `json:"provider"`
	Messages    []IngestMessage `json:"messages"`
	Agent       string          `json:"agent,omitempty"`
	ProjectPath string          `json:"project_path,omitempty"`
	Branch      string          `json:"branch,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
}

// IngestMessage is a lightweight message for the ingest endpoint.
type IngestMessage struct {
	Role         string           `json:"role"`
	Content      string           `json:"content"`
	Model        string           `json:"model,omitempty"`
	Thinking     string           `json:"thinking,omitempty"`
	ToolCalls    []IngestToolCall `json:"tool_calls,omitempty"`
	InputTokens  int              `json:"input_tokens,omitempty"`
	OutputTokens int              `json:"output_tokens,omitempty"`
}

// IngestToolCall is a lightweight tool call for the ingest endpoint.
type IngestToolCall struct {
	Name       string `json:"name"`
	Input      string `json:"input"`
	Output     string `json:"output,omitempty"`
	State      string `json:"state,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
}

// IngestResult contains the output of an ingest operation.
type IngestResult struct {
	SessionID string `json:"session_id"`
	Provider  string `json:"provider"`
}

// Ingest pushes a session to the aisync server without provider detection.
func (c *Client) Ingest(req IngestRequest) (*IngestResult, error) {
	data, err := c.doPost("/api/v1/ingest", req)
	if err != nil {
		return nil, err
	}
	var result IngestResult
	return &result, decode(data, &result)
}

// ── Ingest Ollama ──

// OllamaIngestRequest wraps an Ollama-native conversation with optional metadata.
type OllamaIngestRequest struct {
	// Metadata.
	ProjectPath string `json:"project_path,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Summary     string `json:"summary,omitempty"`
	SessionID   string `json:"session_id,omitempty"`

	// Ollama fields.
	Model           string          `json:"model"`
	Conversation    []OllamaMessage `json:"conversation"`
	PromptEvalCount int             `json:"prompt_eval_count,omitempty"`
	EvalCount       int             `json:"eval_count,omitempty"`
	TotalDuration   int64           `json:"total_duration,omitempty"`
	EvalDuration    int64           `json:"eval_duration,omitempty"`
}

// OllamaMessage is a single message in an Ollama conversation.
type OllamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []OllamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
}

// OllamaToolCall is an Ollama tool call within an assistant message.
type OllamaToolCall struct {
	Type     string             `json:"type,omitempty"`
	Function OllamaFunctionCall `json:"function"`
}

// OllamaFunctionCall is the function details within an Ollama tool call.
type OllamaFunctionCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// IngestOllama pushes an Ollama-native conversation to aisync.
// The server handles format conversion automatically.
func (c *Client) IngestOllama(req OllamaIngestRequest) (*IngestResult, error) {
	data, err := c.doPost("/api/v1/ingest/ollama", req)
	if err != nil {
		return nil, err
	}
	var result IngestResult
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
	FilePath    string `json:"file_path,omitempty"`
	Worktree    bool   `json:"worktree,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
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
	OwnerID     string
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
	if opts.OwnerID != "" {
		q.Set("owner_id", opts.OwnerID)
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

// PatchSession updates mutable session fields (currently only session_type).
func (c *Client) PatchSession(id string, sessionType string) error {
	body := map[string]string{"session_type": sessionType}
	_, err := c.doPatch("/api/v1/sessions/"+url.PathEscape(id), body)
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
	Voice       bool // voice mode: compact results optimized for TTS
}

// SearchResult contains paginated search results.
type SearchResult struct {
	Sessions     []Summary      `json:"sessions"`
	VoiceResults []VoiceSummary `json:"voice_results,omitempty"`
	TotalCount   int            `json:"total_count"`
	Limit        int            `json:"limit"`
	Offset       int            `json:"offset"`
}

// VoiceSummary is a compact, TTS-optimized session representation.
type VoiceSummary struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
	TimeAgo string `json:"time_ago"`
	Agent   string `json:"agent,omitempty"`
	Branch  string `json:"branch,omitempty"`
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
	if opts.Voice {
		q.Set("voice", "true")
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

// ── Explain ──

// ExplainRequest contains inputs for explaining a session.
type ExplainRequest struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model,omitempty"`
	Short     bool   `json:"short,omitempty"`
}

// ExplainResult contains the AI-generated explanation.
type ExplainResult struct {
	Explanation string `json:"Explanation"`
	SessionID   string `json:"SessionID"`
	Model       string `json:"Model"`
	TokensUsed  int    `json:"TokensUsed"`
}

// Explain generates an AI-powered explanation of a session.
func (c *Client) Explain(req ExplainRequest) (*ExplainResult, error) {
	data, err := c.doPost("/api/v1/sessions/explain", req)
	if err != nil {
		return nil, err
	}
	var result ExplainResult
	return &result, decode(data, &result)
}

// ── Rewind ──

// RewindRequest contains inputs for rewinding a session.
type RewindRequest struct {
	SessionID string `json:"session_id"`
	AtMessage int    `json:"at_message"`
}

// RewindResult contains the outcome of a rewind operation.
type RewindResult struct {
	NewSession      *Session `json:"NewSession"`
	OriginalID      string   `json:"OriginalID"`
	TruncatedAt     int      `json:"TruncatedAt"`
	MessagesRemoved int      `json:"MessagesRemoved"`
}

// Rewind creates a fork of a session truncated at the given message index.
func (c *Client) Rewind(req RewindRequest) (*RewindResult, error) {
	data, err := c.doPost("/api/v1/sessions/rewind", req)
	if err != nil {
		return nil, err
	}
	var result RewindResult
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
	ProjectPath  string
	Branch       string
	Provider     string
	OwnerID      string
	All          bool
	IncludeTools bool // if true, include aggregated tool usage
}

// BranchStats holds aggregated stats per branch.
type BranchStats struct {
	Branch       string  `json:"Branch"`
	TotalTokens  int     `json:"TotalTokens"`
	TotalCost    float64 `json:"TotalCost"`
	SessionCount int     `json:"SessionCount"`
	LastActivity string  `json:"LastActivity,omitempty"`
}

// FileEntry is a file path with its touch count.
type FileEntry struct {
	Path  string `json:"Path"`
	Count int    `json:"Count"`
}

// AggregatedToolStats holds tool usage aggregated across multiple sessions.
type AggregatedToolStats struct {
	Tools      []ToolUsageEntry `json:"tools"`
	TotalCalls int              `json:"total_calls"`
	TotalCost  Cost             `json:"total_cost,omitempty"`
	Warning    string           `json:"warning,omitempty"`
}

// StatsResult contains aggregated statistics.
type StatsResult struct {
	TotalSessions int                  `json:"TotalSessions"`
	TotalMessages int                  `json:"TotalMessages"`
	TotalTokens   int                  `json:"TotalTokens"`
	TotalCost     float64              `json:"TotalCost"`
	PerBranch     []*BranchStats       `json:"PerBranch"`
	PerProvider   map[string]int       `json:"PerProvider"`
	TopFiles      []FileEntry          `json:"TopFiles"`
	ToolStats     *AggregatedToolStats `json:"tool_stats,omitempty"`

	// Error / activity aggregates (populated by the server's Stats() loop).
	// Tag naming matches service.StatsResult for remote round-trip compatibility.
	TotalErrors        int `json:"total_errors,omitempty"`
	TotalToolCalls     int `json:"total_tool_calls,omitempty"`
	SessionsWithErrors int `json:"sessions_with_errors,omitempty"`

	// Recent sessions (top 10 by last activity, sorted desc).
	RecentSessions []Summary `json:"recent_sessions,omitempty"`
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
	if opts.OwnerID != "" {
		q.Set("owner_id", opts.OwnerID)
	}
	if opts.All {
		q.Set("all", "true")
	}
	if opts.IncludeTools {
		q.Set("tools", "true")
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

// ── Cost ──

// Cost represents a monetary amount.
type Cost struct {
	InputCost  float64 `json:"input_cost"`
	OutputCost float64 `json:"output_cost"`
	TotalCost  float64 `json:"total_cost"`
	Currency   string  `json:"currency"`
}

// ModelCost groups cost by model within a session.
type ModelCost struct {
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Cost         Cost   `json:"cost"`
	MessageCount int    `json:"message_count"`
}

// CostEstimate is the full cost breakdown for a session.
type CostEstimate struct {
	TotalCost     Cost        `json:"total_cost"`
	PerModel      []ModelCost `json:"per_model"`
	UnknownModels []string    `json:"unknown_models,omitempty"`
}

// EstimateCost retrieves the cost breakdown for a session.
func (c *Client) EstimateCost(idOrSHA string) (*CostEstimate, error) {
	data, err := c.doGet("/api/v1/sessions/" + url.PathEscape(idOrSHA) + "/cost")
	if err != nil {
		return nil, err
	}
	var result CostEstimate
	return &result, decode(data, &result)
}

// ── Tool Usage ──

// ToolUsageEntry aggregates token usage for a single tool name.
type ToolUsageEntry struct {
	Name         string  `json:"name"`
	Calls        int     `json:"calls"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	AvgDuration  int     `json:"avg_duration_ms,omitempty"`
	ErrorCount   int     `json:"error_count"`
	Cost         Cost    `json:"cost,omitempty"`
	Percentage   float64 `json:"percentage"`
}

// ToolUsageStats is the aggregated tool usage breakdown for a session.
type ToolUsageStats struct {
	Tools      []ToolUsageEntry `json:"tools"`
	TotalCalls int              `json:"total_calls"`
	TotalCost  Cost             `json:"total_cost,omitempty"`
	Warning    string           `json:"warning,omitempty"` // non-empty when data may be incomplete
}

// ToolUsage retrieves per-tool token usage breakdown for a session.
func (c *Client) ToolUsage(idOrSHA string) (*ToolUsageStats, error) {
	data, err := c.doGet("/api/v1/sessions/" + url.PathEscape(idOrSHA) + "/tool-usage")
	if err != nil {
		return nil, err
	}
	var result ToolUsageStats
	return &result, decode(data, &result)
}

// ── Garbage Collection ──

// GCRequest contains inputs for garbage collection.
type GCRequest struct {
	OlderThan  string `json:"older_than,omitempty"`
	KeepLatest int    `json:"keep_latest,omitempty"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

// GCResult contains the outcome of garbage collection.
type GCResult struct {
	Deleted int  `json:"deleted"`
	Would   int  `json:"would"`
	DryRun  bool `json:"dry_run"`
}

// GarbageCollect removes old sessions based on age and count policies.
func (c *Client) GarbageCollect(req GCRequest) (*GCResult, error) {
	data, err := c.doPost("/api/v1/gc", req)
	if err != nil {
		return nil, err
	}
	var result GCResult
	return &result, decode(data, &result)
}

// ── Efficiency ──

// EfficiencyRequest contains inputs for analyzing session efficiency.
type EfficiencyRequest struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model,omitempty"`
}

// EfficiencyReport is an LLM-generated efficiency analysis.
type EfficiencyReport struct {
	Score       int      `json:"score"`
	Summary     string   `json:"summary"`
	Strengths   []string `json:"strengths"`
	Issues      []string `json:"issues"`
	Suggestions []string `json:"suggestions"`
	Patterns    []string `json:"patterns"`
}

// EfficiencyResult contains the AI-generated efficiency report.
type EfficiencyResult struct {
	Report     EfficiencyReport `json:"Report"`
	SessionID  string           `json:"SessionID"`
	Model      string           `json:"Model"`
	TokensUsed int              `json:"TokensUsed"`
}

// AnalyzeEfficiency generates an LLM-powered efficiency analysis of a session.
func (c *Client) AnalyzeEfficiency(req EfficiencyRequest) (*EfficiencyResult, error) {
	data, err := c.doPost("/api/v1/sessions/efficiency", req)
	if err != nil {
		return nil, err
	}
	var result EfficiencyResult
	return &result, decode(data, &result)
}

// ── Diff ──

// DiffRequest contains inputs for comparing two sessions.
type DiffRequest struct {
	LeftID  string
	RightID string
}

// DiffResult holds a side-by-side comparison between two sessions.
type DiffResult struct {
	Left         DiffSide     `json:"left"`
	Right        DiffSide     `json:"right"`
	TokenDelta   TokenDelta   `json:"token_delta"`
	CostDelta    CostDelta    `json:"cost_delta,omitempty"`
	FileDiff     FileDiff     `json:"file_diff"`
	ToolDiff     ToolDiff     `json:"tool_diff,omitempty"`
	MessageDelta MessageDelta `json:"message_delta"`
}

// DiffSide holds summary metadata for one side of a diff.
type DiffSide struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	Branch       string `json:"branch,omitempty"`
	Summary      string `json:"summary,omitempty"`
	MessageCount int    `json:"message_count"`
	TotalTokens  int    `json:"total_tokens"`
	StorageMode  string `json:"storage_mode"`
}

// TokenDelta shows the difference in token usage.
type TokenDelta struct {
	InputDelta  int `json:"input_delta"`
	OutputDelta int `json:"output_delta"`
	TotalDelta  int `json:"total_delta"`
}

// CostDelta shows the difference in estimated cost.
type CostDelta struct {
	LeftCost  float64 `json:"left_cost"`
	RightCost float64 `json:"right_cost"`
	Delta     float64 `json:"delta"`
	Currency  string  `json:"currency"`
}

// FileDiff groups files into shared, left-only, and right-only.
type FileDiff struct {
	Shared    []string `json:"shared"`
	LeftOnly  []string `json:"left_only,omitempty"`
	RightOnly []string `json:"right_only,omitempty"`
}

// ToolDiff compares tool usage between two sessions.
type ToolDiff struct {
	Entries []ToolDiffEntry `json:"entries"`
}

// ToolDiffEntry compares a single tool across two sessions.
type ToolDiffEntry struct {
	Name       string `json:"name"`
	LeftCalls  int    `json:"left_calls"`
	RightCalls int    `json:"right_calls"`
	CallsDelta int    `json:"calls_delta"`
}

// MessageDelta shows where two sessions diverge in their message sequence.
type MessageDelta struct {
	CommonPrefix int `json:"common_prefix"`
	LeftAfter    int `json:"left_after"`
	RightAfter   int `json:"right_after"`
}

// Diff compares two sessions side-by-side.
func (c *Client) Diff(req DiffRequest) (*DiffResult, error) {
	q := url.Values{}
	q.Set("left", req.LeftID)
	q.Set("right", req.RightID)

	data, err := c.doGet("/api/v1/sessions/diff?" + q.Encode())
	if err != nil {
		return nil, err
	}
	var result DiffResult
	return &result, decode(data, &result)
}

// ── Off-Topic Detection ──

// OffTopicRequest contains inputs for off-topic detection.
type OffTopicRequest struct {
	ProjectPath string  // filter by project
	Branch      string  // required — the branch to analyze
	Threshold   float64 // 0.0–1.0 overlap threshold (default: 0.2)
}

// OffTopicResult holds the analysis of sessions on a branch.
type OffTopicResult struct {
	Branch   string          `json:"branch"`
	Sessions []OffTopicEntry `json:"sessions"`
	TopFiles []string        `json:"top_files"`
	Total    int             `json:"total"`
	OffTopic int             `json:"off_topic"`
}

// OffTopicEntry scores a single session's relevance to the branch topic.
type OffTopicEntry struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	Summary    string    `json:"summary,omitempty"`
	Files      []string  `json:"files"`
	Overlap    float64   `json:"overlap"`
	IsOffTopic bool      `json:"is_off_topic"`
	CreatedAt  time.Time `json:"created_at"`
}

// DetectOffTopic analyzes sessions on a branch and flags those with low file overlap.
func (c *Client) DetectOffTopic(req OffTopicRequest) (*OffTopicResult, error) {
	q := url.Values{}
	q.Set("branch", req.Branch)
	if req.ProjectPath != "" {
		q.Set("project_path", req.ProjectPath)
	}
	if req.Threshold > 0 {
		q.Set("threshold", strconv.FormatFloat(req.Threshold, 'f', -1, 64))
	}

	data, err := c.doGet("/api/v1/sessions/off-topic?" + q.Encode())
	if err != nil {
		return nil, err
	}
	var result OffTopicResult
	return &result, decode(data, &result)
}

// ── Cost Forecast ──

// ForecastRequest contains inputs for cost forecasting.
type ForecastRequest struct {
	ProjectPath string // filter by project
	Branch      string // filter by branch
	Period      string // "daily" or "weekly" (default: "weekly")
	Days        int    // look-back window in days (default: 90)
}

// ForecastResult holds cost forecast data and model recommendations.
type ForecastResult struct {
	Period         string          `json:"period"`
	Buckets        []CostBucket    `json:"buckets"`
	TotalCost      float64         `json:"total_cost"`
	AvgPerBucket   float64         `json:"avg_per_bucket"`
	SessionCount   int             `json:"session_count"`
	Projected30d   float64         `json:"projected_30d"`
	Projected90d   float64         `json:"projected_90d"`
	TrendPerDay    float64         `json:"trend_per_day"`
	TrendDir       string          `json:"trend_dir"`
	ModelBreakdown []ModelForecast `json:"model_breakdown"`
}

// CostBucket holds cost data for a time period.
type CostBucket struct {
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Cost         float64   `json:"cost"`
	Tokens       int       `json:"tokens"`
	SessionCount int       `json:"session_count"`
}

// ModelForecast holds per-model cost data and a savings recommendation.
type ModelForecast struct {
	Model          string  `json:"model"`
	Cost           float64 `json:"cost"`
	Tokens         int     `json:"tokens"`
	SessionCount   int     `json:"session_count"`
	Share          float64 `json:"share"`
	Recommendation string  `json:"recommendation,omitempty"`
}

// Forecast analyzes historical session costs and projects future spending.
func (c *Client) Forecast(req ForecastRequest) (*ForecastResult, error) {
	q := url.Values{}
	if req.ProjectPath != "" {
		q.Set("project_path", req.ProjectPath)
	}
	if req.Branch != "" {
		q.Set("branch", req.Branch)
	}
	if req.Period != "" {
		q.Set("period", req.Period)
	}
	if req.Days > 0 {
		q.Set("days", strconv.Itoa(req.Days))
	}

	data, err := c.doGet("/api/v1/stats/forecast?" + q.Encode())
	if err != nil {
		return nil, err
	}
	var result ForecastResult
	return &result, decode(data, &result)
}

// ── Session-to-Session Links ──

// SessionLinkRequest contains inputs for creating a session-to-session link.
type SessionLinkRequest struct {
	SourceSessionID string `json:"source_session_id"`
	TargetSessionID string `json:"target_session_id"`
	LinkType        string `json:"link_type"`
	Description     string `json:"description,omitempty"`
}

// SessionLink represents a link between two sessions.
// ProjectGroup represents a project (grouping key for sessions).
type ProjectGroup struct {
	RemoteURL    string `json:"remote_url,omitempty"`
	ProjectPath  string `json:"project_path"`
	Provider     string `json:"provider"`
	SessionCount int    `json:"session_count"`
	TotalTokens  int    `json:"total_tokens"`
	DisplayName  string `json:"display_name"`
}

// ListProjects returns all distinct projects with aggregated stats.
func (c *Client) ListProjects() ([]ProjectGroup, error) {
	data, err := c.doGet("/api/v1/projects")
	if err != nil {
		return nil, err
	}
	var groups []ProjectGroup
	return groups, decode(data, &groups)
}

type SessionLink struct {
	CreatedAt       time.Time `json:"created_at"`
	ID              string    `json:"id"`
	SourceSessionID string    `json:"source_session_id"`
	TargetSessionID string    `json:"target_session_id"`
	LinkType        string    `json:"link_type"`
	Description     string    `json:"description,omitempty"`
}

// LinkSessions creates a bidirectional link between two sessions.
func (c *Client) LinkSessions(req SessionLinkRequest) (*SessionLink, error) {
	data, err := c.doPost("/api/v1/session-links", req)
	if err != nil {
		return nil, err
	}
	var result SessionLink
	return &result, decode(data, &result)
}

// GetLinkedSessions retrieves all session-to-session links for a given session.
func (c *Client) GetLinkedSessions(sessionID string) ([]SessionLink, error) {
	data, err := c.doGet("/api/v1/session-links?session_id=" + url.QueryEscape(sessionID))
	if err != nil {
		return nil, err
	}
	var result []SessionLink
	return result, decode(data, &result)
}

// DeleteSessionLink removes a session-to-session link by its ID.
func (c *Client) DeleteSessionLink(id string) error {
	_, err := c.doDelete("/api/v1/session-links/" + url.PathEscape(id))
	return err
}

// ── Analysis ──

// AnalyzeRequest is the request body for POST /api/v1/sessions/{id}/analyze.
type AnalyzeRequest struct {
	Trigger string `json:"trigger,omitempty"` // "manual" (default) or "auto"
}

// AnalysisReport mirrors analysis.AnalysisReport.
type AnalysisReport struct {
	Score            int                 `json:"score"`
	Summary          string              `json:"summary"`
	Problems         []AnalysisProblem   `json:"problems,omitempty"`
	Recommendations  []AnalysisRecommend `json:"recommendations,omitempty"`
	SkillSuggestions []AnalysisSkill     `json:"skill_suggestions,omitempty"`
}

// AnalysisProblem mirrors analysis.Problem.
type AnalysisProblem struct {
	Severity     string `json:"severity"`
	Description  string `json:"description"`
	MessageStart int    `json:"message_start,omitempty"`
	MessageEnd   int    `json:"message_end,omitempty"`
	ToolName     string `json:"tool_name,omitempty"`
}

// AnalysisRecommend mirrors analysis.Recommendation.
type AnalysisRecommend struct {
	Category    string `json:"category"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    int    `json:"priority"`
}

// AnalysisSkill mirrors analysis.SkillSuggestion.
type AnalysisSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Trigger     string `json:"trigger,omitempty"`
	Content     string `json:"content,omitempty"`
}

// SessionAnalysis mirrors analysis.SessionAnalysis.
type SessionAnalysis struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id"`
	CreatedAt  string         `json:"created_at"`
	Trigger    string         `json:"trigger"`
	Report     AnalysisReport `json:"report"`
	Adapter    string         `json:"adapter"`
	Model      string         `json:"model,omitempty"`
	TokensUsed int            `json:"tokens_used,omitempty"`
	DurationMs int            `json:"duration_ms,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// AnalyzeSession triggers an analysis for a session and returns the result.
func (c *Client) AnalyzeSession(sessionID string, req AnalyzeRequest) (*SessionAnalysis, error) {
	data, err := c.doPost("/api/v1/sessions/"+url.PathEscape(sessionID)+"/analyze", req)
	if err != nil {
		return nil, err
	}
	var result SessionAnalysis
	return &result, decode(data, &result)
}

// GetAnalysis retrieves the latest analysis for a session.
func (c *Client) GetAnalysis(sessionID string) (*SessionAnalysis, error) {
	data, err := c.doGet("/api/v1/sessions/" + url.PathEscape(sessionID) + "/analysis")
	if err != nil {
		return nil, err
	}
	var result SessionAnalysis
	return &result, decode(data, &result)
}

// ListAnalyses returns all analyses for a session.
func (c *Client) ListAnalyses(sessionID string) ([]SessionAnalysis, error) {
	data, err := c.doGet("/api/v1/sessions/" + url.PathEscape(sessionID) + "/analyses")
	if err != nil {
		return nil, err
	}
	var result []SessionAnalysis
	return result, decode(data, &result)
}

// ── Replay ──

// ReplayRequest specifies options for replaying a session.
type ReplayRequest struct {
	Provider    string `json:"provider,omitempty"`     // override provider
	Agent       string `json:"agent,omitempty"`        // override agent
	Model       string `json:"model,omitempty"`        // override model
	CommitSHA   string `json:"commit_sha,omitempty"`   // override commit
	MaxMessages int    `json:"max_messages,omitempty"` // limit user messages (0 = all)
}

// ReplayResult contains the outcome of a session replay.
type ReplayResult struct {
	OriginalSession *Session          `json:"original_session"`
	ReplaySession   *Session          `json:"replay_session,omitempty"`
	WorktreePath    string            `json:"worktree_path"`
	Duration        string            `json:"duration"` // Go duration string
	Comparison      *ReplayComparison `json:"comparison,omitempty"`
	Error           string            `json:"error,omitempty"`
}

// ReplayComparison compares metrics between original and replay sessions.
type ReplayComparison struct {
	OriginalTokens    int      `json:"original_tokens"`
	ReplayTokens      int      `json:"replay_tokens"`
	TokenDelta        int      `json:"token_delta"`
	OriginalErrors    int      `json:"original_errors"`
	ReplayErrors      int      `json:"replay_errors"`
	ErrorDelta        int      `json:"error_delta"`
	OriginalMessages  int      `json:"original_messages"`
	ReplayMessages    int      `json:"replay_messages"`
	OriginalSkills    []string `json:"original_skills,omitempty"`
	ReplaySkills      []string `json:"replay_skills,omitempty"`
	NewSkillsLoaded   []string `json:"new_skills_loaded,omitempty"`
	SkillsLost        []string `json:"skills_lost,omitempty"`
	OriginalToolCalls int      `json:"original_tool_calls"`
	ReplayToolCalls   int      `json:"replay_tool_calls"`
	Verdict           string   `json:"verdict"`
}

// ReplaySession triggers a replay of the given session and returns the result.
func (c *Client) ReplaySession(sessionID string, req ReplayRequest) (*ReplayResult, error) {
	data, err := c.doPost("/api/v1/sessions/"+url.PathEscape(sessionID)+"/replay", req)
	if err != nil {
		return nil, err
	}
	var result ReplayResult
	return &result, decode(data, &result)
}

// ── Skill Resolver ──

// SkillResolveRequest specifies options for resolving missed skills.
type SkillResolveRequest struct {
	SkillNames []string `json:"skill_names,omitempty"` // optional filter
	DryRun     bool     `json:"dry_run"`               // true = suggest only, false = apply
}

// SkillImprovement proposes a specific change to a SKILL.md file.
type SkillImprovement struct {
	SkillName           string   `json:"skill_name"`
	SkillPath           string   `json:"skill_path"`
	Kind                string   `json:"kind"`
	CurrentDescription  string   `json:"current_description,omitempty"`
	ProposedDescription string   `json:"proposed_description,omitempty"`
	AddKeywords         []string `json:"add_keywords,omitempty"`
	AddTriggerPatterns  []string `json:"add_trigger_patterns,omitempty"`
	ProposedContent     string   `json:"proposed_content,omitempty"`
	Reasoning           string   `json:"reasoning"`
	Confidence          float64  `json:"confidence"`
	SourceSessionID     string   `json:"source_session_id"`
}

// SkillResolveResult contains the outcome of a skill resolution.
type SkillResolveResult struct {
	SessionID    string             `json:"session_id"`
	Improvements []SkillImprovement `json:"improvements"`
	Applied      int                `json:"applied"`
	Validated    bool               `json:"validated"`
	Verdict      string             `json:"verdict"`
	Duration     string             `json:"duration"` // Go duration string
	Error        string             `json:"error,omitempty"`
}

// ResolveSkills analyzes missed skills for a session and proposes/applies improvements.
func (c *Client) ResolveSkills(sessionID string, req SkillResolveRequest) (*SkillResolveResult, error) {
	data, err := c.doPost("/api/v1/sessions/"+url.PathEscape(sessionID)+"/skills/resolve", req)
	if err != nil {
		return nil, err
	}
	var result SkillResolveResult
	return &result, decode(data, &result)
}

// ── Authentication ──

// AuthRegisterRequest is the input for user registration.
type AuthRegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthLoginRequest is the input for login.
type AuthLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthResponse is returned by Register and Login.
type AuthResponse struct {
	Token string   `json:"token"`
	User  AuthUser `json:"user"`
}

// AuthUser is the API view of a user (separate from session domain).
type AuthUser struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
}

// AuthAPIKey is the API view of an API key.
type AuthAPIKey struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	KeyPrefix string  `json:"key_prefix"`
	Active    bool    `json:"active"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// AuthCreateKeyResponse is returned when creating an API key.
type AuthCreateKeyResponse struct {
	RawKey string     `json:"raw_key"`
	APIKey AuthAPIKey `json:"api_key"`
}

// Register creates a new user account.
func (c *Client) Register(req AuthRegisterRequest) (*AuthResponse, error) {
	data, err := c.doPost("/api/v1/auth/register", req)
	if err != nil {
		return nil, err
	}
	var result AuthResponse
	return &result, decode(data, &result)
}

// Login authenticates with username+password.
func (c *Client) Login(req AuthLoginRequest) (*AuthResponse, error) {
	data, err := c.doPost("/api/v1/auth/login", req)
	if err != nil {
		return nil, err
	}
	var result AuthResponse
	return &result, decode(data, &result)
}

// AuthMe returns the current user info from the JWT/API key.
func (c *Client) AuthMe() (*AuthUser, error) {
	data, err := c.doGet("/api/v1/auth/me")
	if err != nil {
		return nil, err
	}
	var result AuthUser
	return &result, decode(data, &result)
}

// CreateAPIKeyRequest is the input for creating an API key.
type CreateAPIKeyRequest struct {
	Name string `json:"name"`
}

// AuthCreateAPIKey creates a new API key for the authenticated user.
func (c *Client) AuthCreateAPIKey(req CreateAPIKeyRequest) (*AuthCreateKeyResponse, error) {
	data, err := c.doPost("/api/v1/auth/keys", req)
	if err != nil {
		return nil, err
	}
	var result AuthCreateKeyResponse
	return &result, decode(data, &result)
}

// AuthListAPIKeys returns all API keys for the authenticated user.
func (c *Client) AuthListAPIKeys() ([]AuthAPIKey, error) {
	data, err := c.doGet("/api/v1/auth/keys")
	if err != nil {
		return nil, err
	}
	var result []AuthAPIKey
	return result, decode(data, &result)
}

// AuthRevokeAPIKey deactivates an API key.
func (c *Client) AuthRevokeAPIKey(keyID string) error {
	_, err := c.doPost("/api/v1/auth/keys/"+url.PathEscape(keyID)+"/revoke", nil)
	return err
}

// AuthDeleteAPIKey permanently removes an API key.
func (c *Client) AuthDeleteAPIKey(keyID string) error {
	_, err := c.doDelete("/api/v1/auth/keys/" + url.PathEscape(keyID))
	return err
}
