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
	All          bool
	IncludeTools bool // if true, include aggregated tool usage
}

// BranchStats holds aggregated stats per branch.
type BranchStats struct {
	Branch       string  `json:"Branch"`
	TotalTokens  int     `json:"TotalTokens"`
	TotalCost    float64 `json:"TotalCost"`
	SessionCount int     `json:"SessionCount"`
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
