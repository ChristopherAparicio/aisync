// Package service implements the core business logic for aisync.
// It provides SessionService (capture, restore, export, import, stats, etc.)
// and SyncService (push/pull via git branch). These services absorb all
// orchestration logic that previously lived in CLI commands, making the
// logic reusable across CLI, HTTP API, and MCP server.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	capturesvc "github.com/ChristopherAparicio/aisync/internal/capture"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/platform"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	restoresvc "github.com/ChristopherAparicio/aisync/internal/restore"
	"github.com/ChristopherAparicio/aisync/internal/secrets"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── SessionService ──

// SessionService orchestrates all session-related business logic.
type SessionService struct {
	store     storage.Store
	registry  *provider.Registry
	scanner   *secrets.Scanner // optional — nil means no scanning
	converter *converter.Converter
	git       *git.Client       // optional — nil when git is unavailable
	platform  platform.Platform // optional — nil when platform is unavailable
	llm       llm.Client        // optional — nil disables AI features (summarize, explain)
}

// SessionServiceConfig holds all dependencies for creating a SessionService.
type SessionServiceConfig struct {
	Store     storage.Store
	Registry  *provider.Registry
	Scanner   *secrets.Scanner // optional
	Converter *converter.Converter
	Git       *git.Client       // optional
	Platform  platform.Platform // optional
	LLM       llm.Client        // optional — nil disables AI features
}

// NewSessionService creates a SessionService with all dependencies.
func NewSessionService(cfg SessionServiceConfig) *SessionService {
	conv := cfg.Converter
	if conv == nil {
		conv = converter.New()
	}
	return &SessionService{
		store:     cfg.Store,
		registry:  cfg.Registry,
		scanner:   cfg.Scanner,
		converter: conv,
		git:       cfg.Git,
		platform:  cfg.Platform,
		llm:       cfg.LLM,
	}
}

// resolveOwner detects the current user from git config and ensures they exist
// in the store. Returns the user ID, or empty if the identity cannot be determined.
func (s *SessionService) resolveOwner() session.ID {
	if s.git == nil {
		return ""
	}

	email := s.git.UserEmail()
	if email == "" {
		return ""
	}

	// Check if the user already exists
	existing, err := s.store.GetUserByEmail(email)
	if err == nil && existing != nil {
		return existing.ID
	}

	// Create a new user
	name := s.git.UserName()
	if name == "" {
		name = email // fallback to email as name
	}

	user := &session.User{
		ID:     session.NewID(),
		Name:   name,
		Email:  email,
		Source: "git",
	}

	if saveErr := s.store.SaveUser(user); saveErr != nil {
		return "" // silently skip — user identity is best-effort
	}

	return user.ID
}

// ── Capture ──

// CaptureRequest contains inputs for a capture operation.
type CaptureRequest struct {
	ProjectPath  string
	Branch       string
	Mode         session.StorageMode
	ProviderName session.ProviderName // empty = auto-detect
	Message      string               // optional manual summary
	Summarize    bool                 // if true, AI-summarize after export
	Model        string               // optional model override for summarization
}

// CaptureResult contains the output of a capture operation.
type CaptureResult struct {
	Session           *session.Session
	Provider          session.ProviderName
	SecretsFound      int
	Summarized        bool                       // true if AI summarization was applied
	StructuredSummary *session.StructuredSummary // non-nil if summarized
}

// Capture detects the active AI session, exports it, and stores it.
// If Summarize is true and an LLM client is available, it generates an AI summary.
// Summarization is non-blocking: if it fails, capture proceeds with the native summary.
func (s *SessionService) Capture(req CaptureRequest) (*CaptureResult, error) {
	var svc *capturesvc.Service
	if s.scanner != nil {
		svc = capturesvc.NewServiceWithScanner(s.registry, s.store, s.scanner)
	} else {
		svc = capturesvc.NewService(s.registry, s.store)
	}

	// Resolve owner identity before capture so it's included in the single Save()
	ownerID := s.resolveOwner()

	result, err := svc.Capture(capturesvc.Request{
		ProjectPath:  req.ProjectPath,
		Branch:       req.Branch,
		Mode:         req.Mode,
		ProviderName: req.ProviderName,
		Message:      req.Message,
		OwnerID:      ownerID,
	})
	if err != nil {
		return nil, err
	}

	captureResult := &CaptureResult{
		Session:      result.Session,
		Provider:     result.Provider,
		SecretsFound: result.SecretsFound,
	}

	// AI summarization: only if requested, no --message override, and LLM is available.
	// Priority: --message > AI summary > provider-native summary.
	if req.Summarize && req.Message == "" && s.llm != nil {
		ctx := context.Background()
		sumResult, sumErr := s.Summarize(ctx, SummarizeRequest{
			Session: result.Session,
			Model:   req.Model,
		})
		if sumErr == nil {
			// Apply the AI-generated summary
			result.Session.Summary = sumResult.OneLine
			captureResult.Summarized = true
			captureResult.StructuredSummary = &sumResult.Summary

			// Re-save with updated summary (session already in store from capture).
			// Log error but don't fail capture — summary loss is acceptable.
			if saveErr := s.store.Save(result.Session); saveErr != nil {
				captureResult.Summarized = false // summary was not persisted
			}
		}
		// On failure: silently keep the provider-native summary (non-blocking).
	}

	return captureResult, nil
}

// ── Restore ──

// RestoreRequest contains inputs for a restore operation.
type RestoreRequest struct {
	ProjectPath  string
	Branch       string
	Agent        string
	SessionID    session.ID
	ProviderName session.ProviderName
	AsContext    bool
	PRNumber     int // if > 0, look up session linked to this PR
}

// RestoreResult contains the output of a restore operation.
type RestoreResult struct {
	Session     *session.Session
	Method      string // "native", "converted", or "context"
	ContextPath string
}

// Restore looks up a session and imports it into a target provider.
func (s *SessionService) Restore(req RestoreRequest) (*RestoreResult, error) {
	sessionID := req.SessionID

	// If --pr is set and no explicit session, look up by PR link
	if req.PRNumber > 0 && sessionID == "" {
		summaries, err := s.store.GetByLink(session.LinkPR, strconv.Itoa(req.PRNumber))
		if err != nil {
			return nil, fmt.Errorf("no session linked to PR #%d: %w", req.PRNumber, err)
		}
		if len(summaries) == 0 {
			return nil, fmt.Errorf("no session linked to PR #%d", req.PRNumber)
		}
		sessionID = summaries[0].ID
	}

	svc := restoresvc.NewServiceWithConverter(s.registry, s.store, s.converter)

	result, err := svc.Restore(restoresvc.Request{
		ProjectPath:  req.ProjectPath,
		Branch:       req.Branch,
		SessionID:    sessionID,
		ProviderName: req.ProviderName,
		Agent:        req.Agent,
		AsContext:    req.AsContext,
	})
	if err != nil {
		return nil, err
	}

	return &RestoreResult{
		Session:     result.Session,
		Method:      result.Method,
		ContextPath: result.ContextPath,
	}, nil
}

// ── Get ──

// Get retrieves a session by ID or commit SHA.
// If the argument looks like a commit SHA, it parses the AI-Session trailer.
func (s *SessionService) Get(idOrSHA string) (*session.Session, error) {
	// Try as a git commit SHA first
	if s.git != nil && looksLikeCommitSHA(idOrSHA) && s.git.IsValidCommit(idOrSHA) {
		commitMsg, err := s.git.CommitMessage(idOrSHA)
		if err == nil {
			trailerID := git.ParseSessionTrailer(commitMsg)
			if trailerID != "" {
				sid, parseErr := session.ParseID(trailerID)
				if parseErr == nil {
					return s.store.Get(sid)
				}
			}
		}
		return nil, fmt.Errorf("commit %s has no AI-Session trailer; use a session ID instead", idOrSHA)
	}

	// Fall back to session ID
	sid, err := session.ParseID(idOrSHA)
	if err != nil {
		return nil, err
	}
	return s.store.Get(sid)
}

// ── List ──

// ListRequest contains inputs for listing sessions.
type ListRequest struct {
	ProjectPath string
	Branch      string
	Provider    session.ProviderName
	PRNumber    int // if > 0, list sessions linked to this PR
	All         bool
}

// List returns session summaries matching the given criteria.
func (s *SessionService) List(req ListRequest) ([]session.Summary, error) {
	if req.PRNumber > 0 {
		return s.store.GetByLink(session.LinkPR, strconv.Itoa(req.PRNumber))
	}

	listOpts := session.ListOptions{
		ProjectPath: req.ProjectPath,
		All:         req.All,
		Provider:    req.Provider,
	}
	if !req.All {
		listOpts.Branch = req.Branch
	}

	return s.store.List(listOpts)
}

// ── Delete ──

// Delete removes a session by ID.
func (s *SessionService) Delete(id session.ID) error {
	return s.store.Delete(id)
}

// ── Export ──

// ExportRequest contains inputs for exporting a session.
type ExportRequest struct {
	SessionID   session.ID // empty = use current branch
	ProjectPath string     // used if SessionID is empty
	Branch      string     // used if SessionID is empty
	Format      string     // "aisync", "claude", "opencode", "context"
}

// ExportResult contains the exported data.
type ExportResult struct {
	Data      []byte
	Format    string // normalized format label
	SessionID session.ID
}

// Export converts a session to the requested format.
func (s *SessionService) Export(req ExportRequest) (*ExportResult, error) {
	sess, err := s.resolveSession(req.SessionID, req.ProjectPath, req.Branch)
	if err != nil {
		return nil, err
	}

	var output []byte
	formatLabel := req.Format

	switch req.Format {
	case "aisync", "":
		output, err = json.MarshalIndent(sess, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshaling session: %w", err)
		}
		output = append(output, '\n')
		formatLabel = "aisync"

	case "claude", "claude-code":
		output, err = s.converter.ToNative(sess, session.ProviderClaudeCode)
		if err != nil {
			return nil, fmt.Errorf("converting to Claude format: %w", err)
		}
		formatLabel = "claude"

	case "opencode":
		output, err = s.converter.ToNative(sess, session.ProviderOpenCode)
		if err != nil {
			return nil, fmt.Errorf("converting to OpenCode format: %w", err)
		}

	case "context", "context.md":
		output = converter.ToContextMD(sess)
		formatLabel = "context.md"

	default:
		return nil, fmt.Errorf("unknown format %q: valid values are [aisync, claude, opencode, context]", req.Format)
	}

	return &ExportResult{
		Data:      output,
		Format:    formatLabel,
		SessionID: sess.ID,
	}, nil
}

// ── Import ──

// ImportRequest contains inputs for importing a session.
type ImportRequest struct {
	Data         []byte // raw file contents
	SourceFormat string // "aisync", "claude", "opencode", or "" (auto-detect)
	IntoTarget   string // "aisync", "claude-code", "opencode"
}

// ImportResult contains the outcome of an import.
type ImportResult struct {
	SessionID    session.ID
	SourceFormat string
	Target       string
}

// Import parses raw data, optionally scans for secrets, and stores or injects the session.
func (s *SessionService) Import(req ImportRequest) (*ImportResult, error) {
	if len(req.Data) == 0 {
		return nil, fmt.Errorf("import data is empty")
	}

	// Determine source format
	var sourceFormat session.ProviderName
	if req.SourceFormat != "" {
		switch req.SourceFormat {
		case "aisync":
			sourceFormat = "" // unified
		case "claude", "claude-code":
			sourceFormat = session.ProviderClaudeCode
		case "opencode":
			sourceFormat = session.ProviderOpenCode
		default:
			return nil, fmt.Errorf("unknown format %q: valid values are [aisync, claude, opencode]", req.SourceFormat)
		}
	} else {
		sourceFormat = converter.DetectFormat(req.Data)
	}

	// Parse into unified session
	var sess *session.Session
	var err error

	if sourceFormat == "" {
		// Unified aisync JSON
		sess = &session.Session{}
		if jsonErr := json.Unmarshal(req.Data, sess); jsonErr != nil {
			return nil, fmt.Errorf("parsing aisync JSON: %w", jsonErr)
		}
	} else {
		sess, err = s.converter.FromNative(req.Data, sourceFormat)
		if err != nil {
			return nil, fmt.Errorf("parsing %s format: %w", sourceFormat, err)
		}
	}

	// Scan for secrets
	if s.scanner != nil && s.scanner.Mode() == session.SecretModeMask {
		matches := s.scanner.ScanSession(sess)
		if len(matches) > 0 {
			s.scanner.MaskSession(sess)
		}
	}

	// Determine format label for result
	detectedLabel := "aisync"
	if sourceFormat != "" {
		detectedLabel = string(sourceFormat)
	}

	// Determine target
	target := req.IntoTarget
	if target == "" {
		target = "aisync"
	}

	switch target {
	case "aisync":
		if sess.ID == "" {
			sess.ID = session.NewID()
		}
		// Attach owner identity if not already set
		if sess.OwnerID == "" {
			sess.OwnerID = s.resolveOwner()
		}
		if err := s.store.Save(sess); err != nil {
			return nil, fmt.Errorf("storing session: %w", err)
		}

	case "claude", "claude-code":
		prov, provErr := s.registry.Get(session.ProviderClaudeCode)
		if provErr != nil {
			return nil, fmt.Errorf("claude-code provider: %w", provErr)
		}
		if !prov.CanImport() {
			return nil, fmt.Errorf("claude-code provider does not support import")
		}
		if importErr := prov.Import(sess); importErr != nil {
			return nil, fmt.Errorf("importing into claude-code: %w", importErr)
		}

	case "opencode":
		prov, provErr := s.registry.Get(session.ProviderOpenCode)
		if provErr != nil {
			return nil, fmt.Errorf("opencode provider: %w", provErr)
		}
		if !prov.CanImport() {
			return nil, fmt.Errorf("opencode provider does not support import")
		}
		if importErr := prov.Import(sess); importErr != nil {
			return nil, fmt.Errorf("importing into opencode: %w", importErr)
		}

	default:
		return nil, fmt.Errorf("unknown target %q: valid values are [aisync, claude-code, opencode]", target)
	}

	return &ImportResult{
		SessionID:    sess.ID,
		SourceFormat: detectedLabel,
		Target:       target,
	}, nil
}

// ── Link ──

// LinkRequest contains inputs for linking a session.
type LinkRequest struct {
	SessionID   session.ID // empty = resolve from branch
	ProjectPath string
	Branch      string
	PRNumber    int
	CommitSHA   string
	AutoDetect  bool // auto-detect PR from branch
}

// LinkResult contains the outcome of a link operation.
type LinkResult struct {
	SessionID session.ID
	PRNumber  int    // only if a PR was linked
	CommitSHA string // only if a commit was linked
}

// Link associates a session with a PR, commit, or other git object.
func (s *SessionService) Link(req LinkRequest) (*LinkResult, error) {
	if req.PRNumber == 0 && req.CommitSHA == "" && !req.AutoDetect {
		return nil, fmt.Errorf("specify a PR number, commit SHA, or auto-detect")
	}

	// Resolve session ID
	sessionID := req.SessionID
	if sessionID == "" {
		sess, err := s.store.GetLatestByBranch(req.ProjectPath, req.Branch)
		if err != nil {
			return nil, fmt.Errorf("no session found for branch %q: %w", req.Branch, err)
		}
		sessionID = sess.ID
	}

	// Auto-detect PR from branch
	prNumber := req.PRNumber
	if req.AutoDetect && prNumber == 0 {
		if s.platform == nil {
			return nil, fmt.Errorf("platform not available for PR auto-detection")
		}
		pr, err := s.platform.GetPRForBranch(req.Branch)
		if err != nil {
			return nil, fmt.Errorf("no open PR found for branch %q: %w", req.Branch, err)
		}
		prNumber = pr.Number
	}

	result := &LinkResult{SessionID: sessionID}

	// Add PR link
	if prNumber > 0 {
		link := session.Link{
			LinkType: session.LinkPR,
			Ref:      strconv.Itoa(prNumber),
		}
		if err := s.store.AddLink(sessionID, link); err != nil {
			return nil, fmt.Errorf("linking to PR #%d: %w", prNumber, err)
		}
		result.PRNumber = prNumber
	}

	// Add commit link
	if req.CommitSHA != "" {
		link := session.Link{
			LinkType: session.LinkCommit,
			Ref:      req.CommitSHA,
		}
		if err := s.store.AddLink(sessionID, link); err != nil {
			return nil, fmt.Errorf("linking to commit %s: %w", req.CommitSHA, err)
		}
		result.CommitSHA = req.CommitSHA
	}

	if result.PRNumber == 0 && result.CommitSHA == "" {
		return nil, fmt.Errorf("no links were added")
	}

	return result, nil
}

// ── Comment ──

// AisyncMarker is the HTML comment used to identify aisync PR comments for idempotent updates.
const AisyncMarker = "<!-- aisync -->"

// CommentRequest contains inputs for posting a PR comment.
type CommentRequest struct {
	SessionID   session.ID // empty = resolve from branch or PR
	ProjectPath string
	Branch      string
	PRNumber    int // 0 = auto-detect
}

// CommentResult contains the outcome of a comment operation.
type CommentResult struct {
	PRNumber int
	Updated  bool // true if an existing comment was updated, false if new
}

// Comment posts or updates a PR comment with an AI session summary.
func (s *SessionService) Comment(req CommentRequest) (*CommentResult, error) {
	if s.platform == nil {
		return nil, fmt.Errorf("platform not available: cannot post PR comments")
	}

	// Determine PR number
	prNumber := req.PRNumber
	if prNumber == 0 {
		pr, err := s.platform.GetPRForBranch(req.Branch)
		if err != nil {
			return nil, fmt.Errorf("no open PR found for branch %q (use --pr to specify): %w", req.Branch, err)
		}
		prNumber = pr.Number
	}

	// Find session
	sess, err := s.resolveSessionForComment(req, prNumber)
	if err != nil {
		return nil, err
	}

	// Build comment body
	body := BuildCommentBody(sess)

	// Check for existing aisync comment (idempotent update)
	comments, err := s.platform.ListComments(prNumber)
	if err != nil {
		return nil, fmt.Errorf("listing PR comments: %w", err)
	}

	var existingID int64
	for _, c := range comments {
		if strings.Contains(c.Body, AisyncMarker) {
			existingID = c.ID
			break
		}
	}

	updated := false
	if existingID > 0 {
		if updateErr := s.platform.UpdateComment(existingID, body); updateErr != nil {
			return nil, fmt.Errorf("updating comment: %w", updateErr)
		}
		updated = true
	} else {
		if addErr := s.platform.AddComment(prNumber, body); addErr != nil {
			return nil, fmt.Errorf("adding comment: %w", addErr)
		}
	}

	return &CommentResult{
		PRNumber: prNumber,
		Updated:  updated,
	}, nil
}

func (s *SessionService) resolveSessionForComment(req CommentRequest, prNumber int) (*session.Session, error) {
	if req.SessionID != "" {
		return s.store.Get(req.SessionID)
	}

	// Try PR link first
	summaries, lookupErr := s.store.GetByLink(session.LinkPR, strconv.Itoa(prNumber))
	if lookupErr == nil && len(summaries) > 0 {
		return s.store.Get(summaries[0].ID)
	}

	// Fall back to branch
	return s.store.GetLatestByBranch(req.ProjectPath, req.Branch)
}

// BuildCommentBody creates the Markdown comment body from a session.
// Exported so it can be used by the CLI for display purposes.
func BuildCommentBody(sess *session.Session) string {
	var b strings.Builder

	b.WriteString(AisyncMarker)
	b.WriteString("\n## AI Session Summary\n\n")
	b.WriteString(fmt.Sprintf("**Session:** `%s`\n", sess.ID))
	b.WriteString(fmt.Sprintf("**Provider:** %s\n", sess.Provider))
	b.WriteString(fmt.Sprintf("**Branch:** %s\n", sess.Branch))

	if sess.Summary != "" {
		b.WriteString(fmt.Sprintf("\n### Summary\n\n%s\n", sess.Summary))
	}

	if sess.TokenUsage.TotalTokens > 0 {
		b.WriteString("\n### Token Usage\n\n")
		b.WriteString("| Metric | Count |\n")
		b.WriteString("|--------|-------|\n")
		b.WriteString(fmt.Sprintf("| Input  | %d |\n", sess.TokenUsage.InputTokens))
		b.WriteString(fmt.Sprintf("| Output | %d |\n", sess.TokenUsage.OutputTokens))
		b.WriteString(fmt.Sprintf("| Total  | %d |\n", sess.TokenUsage.TotalTokens))
	}

	b.WriteString(fmt.Sprintf("\n**Messages:** %d\n", len(sess.Messages)))

	if len(sess.FileChanges) > 0 {
		b.WriteString("\n### Files Changed\n\n")
		for _, fc := range sess.FileChanges {
			b.WriteString(fmt.Sprintf("- `%s` (%s)\n", fc.FilePath, fc.ChangeType))
		}
	}

	b.WriteString("\n---\n*Posted by [aisync](https://github.com/ChristopherAparicio/aisync)*\n")

	return b.String()
}

// ── Stats ──

// StatsRequest contains inputs for computing statistics.
type StatsRequest struct {
	ProjectPath string
	Branch      string
	Provider    session.ProviderName
	All         bool
}

// BranchStats holds aggregated stats per branch.
type BranchStats struct {
	Branch       string
	TotalTokens  int
	SessionCount int
}

// StatsResult contains aggregated statistics.
type StatsResult struct {
	TotalSessions int
	TotalMessages int
	TotalTokens   int
	PerBranch     []*BranchStats
	PerProvider   map[session.ProviderName]int
	TopFiles      []FileEntry // sorted by count descending, max 10
}

// FileEntry is a file path with its touch count.
type FileEntry struct {
	Path  string
	Count int
}

// Stats computes aggregated statistics across sessions.
func (s *SessionService) Stats(req StatsRequest) (*StatsResult, error) {
	listOpts := session.ListOptions{
		ProjectPath: req.ProjectPath,
		All:         true,
	}

	if req.Branch != "" {
		listOpts.Branch = req.Branch
		listOpts.All = false
	}
	if req.Provider != "" {
		listOpts.Provider = req.Provider
	}

	summaries, err := s.store.List(listOpts)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	result := &StatsResult{
		PerProvider: make(map[session.ProviderName]int),
	}

	perBranch := make(map[string]*BranchStats)
	fileCounts := make(map[string]int)

	for _, sm := range summaries {
		result.TotalSessions++
		result.TotalTokens += sm.TotalTokens
		result.TotalMessages += sm.MessageCount

		// Per-branch
		bs, ok := perBranch[sm.Branch]
		if !ok {
			bs = &BranchStats{Branch: sm.Branch}
			perBranch[sm.Branch] = bs
		}
		bs.SessionCount++
		bs.TotalTokens += sm.TotalTokens

		// Per-provider
		result.PerProvider[sm.Provider]++

		// File changes (requires loading full session)
		full, getErr := s.store.Get(sm.ID)
		if getErr == nil {
			for _, fc := range full.FileChanges {
				fileCounts[fc.FilePath]++
			}
		}
	}

	// Sort branches by token count descending
	branchList := make([]*BranchStats, 0, len(perBranch))
	for _, bs := range perBranch {
		branchList = append(branchList, bs)
	}
	sort.Slice(branchList, func(i, j int) bool {
		return branchList[i].TotalTokens > branchList[j].TotalTokens
	})
	result.PerBranch = branchList

	// Top files (up to 10)
	files := make([]FileEntry, 0, len(fileCounts))
	for path, count := range fileCounts {
		files = append(files, FileEntry{Path: path, Count: count})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Count > files[j].Count
	})
	if len(files) > 10 {
		files = files[:10]
	}
	result.TopFiles = files

	return result, nil
}

// ── Search ──

// SearchRequest contains inputs for a search operation.
type SearchRequest struct {
	Keyword     string
	ProjectPath string
	Branch      string
	Provider    session.ProviderName
	OwnerID     session.ID
	Since       string // RFC3339 or "2006-01-02" format
	Until       string // RFC3339 or "2006-01-02" format
	Limit       int
	Offset      int
}

// Search finds sessions matching the given query criteria.
func (s *SessionService) Search(req SearchRequest) (*session.SearchResult, error) {
	query := session.SearchQuery{
		Keyword:     req.Keyword,
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		Provider:    req.Provider,
		OwnerID:     req.OwnerID,
		Limit:       req.Limit,
		Offset:      req.Offset,
	}

	if req.Since != "" {
		t, err := parseFlexibleTime(req.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid 'since' value %q: %w", req.Since, err)
		}
		query.Since = t
	}
	if req.Until != "" {
		t, err := parseFlexibleTime(req.Until)
		if err != nil {
			return nil, fmt.Errorf("invalid 'until' value %q: %w", req.Until, err)
		}
		query.Until = t
	}

	return s.store.Search(query)
}

// parseFlexibleTime parses a time string in RFC3339 or date-only format.
func parseFlexibleTime(s string) (time.Time, error) {
	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try date-only
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("expected RFC3339 (2006-01-02T15:04:05Z) or date (2006-01-02)")
}

// ── Blame ──

// BlameRequest contains inputs for a blame lookup.
type BlameRequest struct {
	FilePath    string               // required — file path relative to project root
	ProjectPath string               // used for Restore shortcut
	Branch      string               // optional filter
	Provider    session.ProviderName // optional filter
	All         bool                 // true = all sessions; false = most recent only
	Restore     bool                 // if true, restore the most recent session that touched the file
}

// BlameResult contains the outcome of a blame operation.
type BlameResult struct {
	Entries  []session.BlameEntry
	Restored *RestoreResult // non-nil only when Restore=true
	FilePath string
}

// Blame finds AI sessions that touched the given file.
// If Restore is set, it restores the most recent matching session.
func (s *SessionService) Blame(ctx context.Context, req BlameRequest) (*BlameResult, error) {
	if req.FilePath == "" {
		return nil, fmt.Errorf("file path is required")
	}

	query := session.BlameQuery{
		FilePath: req.FilePath,
		Branch:   req.Branch,
		Provider: req.Provider,
	}
	if !req.All {
		query.Limit = 1
	}

	entries, err := s.store.GetSessionsByFile(query)
	if err != nil {
		return nil, fmt.Errorf("blame lookup: %w", err)
	}

	result := &BlameResult{
		Entries:  entries,
		FilePath: req.FilePath,
	}

	// Restore shortcut: restore the most recent session that touched this file.
	if req.Restore && len(entries) > 0 {
		restoreResult, restoreErr := s.Restore(RestoreRequest{
			SessionID:   entries[0].SessionID,
			ProjectPath: req.ProjectPath,
		})
		if restoreErr != nil {
			return nil, fmt.Errorf("blame restore: %w", restoreErr)
		}
		result.Restored = restoreResult
	}

	return result, nil
}

// ── Summarize ──

// summarizeSystemPrompt is the system instruction for AI session summarization.
const summarizeSystemPrompt = `You are a technical session analyzer. Given an AI coding session transcript,
produce a structured JSON summary with these fields:
- intent: What the user was trying to accomplish (1 sentence)
- outcome: What was actually achieved (1 sentence)
- decisions: Key technical decisions made (array of short strings)
- friction: Problems or difficulties encountered (array of short strings)
- open_items: Things left unfinished or needing follow-up (array of short strings)

Respond ONLY with valid JSON, no markdown fences, no explanation.`

// SummarizeRequest contains inputs for summarizing a session.
type SummarizeRequest struct {
	Session *session.Session // the session to summarize
	Model   string           // optional — override default model
}

// SummarizeResult contains the AI-generated summary.
type SummarizeResult struct {
	Summary    session.StructuredSummary
	OneLine    string // compact "Intent: Outcome" string
	Model      string // model that produced the summary
	TokensUsed int    // total tokens consumed
}

// Summarize generates an AI-powered structured summary for a session.
// Returns an error if LLM is not configured or the LLM call fails.
func (s *SessionService) Summarize(ctx context.Context, req SummarizeRequest) (*SummarizeResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("AI summarization requires an LLM client (set summarize.enabled or use --summarize)")
	}
	if req.Session == nil {
		return nil, fmt.Errorf("session is required for summarization")
	}

	userPrompt := buildSessionTranscript(req.Session)
	if userPrompt == "" {
		return nil, fmt.Errorf("session has no messages to summarize")
	}

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: summarizeSystemPrompt,
		UserPrompt:   userPrompt,
		Model:        req.Model,
		MaxTokens:    1024,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM summarize: %w", err)
	}

	var summary session.StructuredSummary
	if jsonErr := json.Unmarshal([]byte(resp.Content), &summary); jsonErr != nil {
		return nil, fmt.Errorf("parsing LLM summary response: %w (raw: %s)", jsonErr, truncate(resp.Content, 200))
	}

	return &SummarizeResult{
		Summary:    summary,
		OneLine:    summary.OneLine(),
		Model:      resp.Model,
		TokensUsed: resp.InputTokens + resp.OutputTokens,
	}, nil
}

// ── Explain ──

// explainSystemPrompt is the system instruction for AI session explanation.
const explainSystemPrompt = `You are a technical analyst. Given an AI coding session transcript,
write a clear explanation of what happened during this session.
Cover: what was the goal, what approach was taken, what files were changed,
what decisions were made and why, and what the outcome was.
Write for a developer who is taking over this branch.`

// explainShortSystemPrompt produces a brief explanation.
const explainShortSystemPrompt = `You are a technical analyst. Given an AI coding session transcript,
write a brief 2-3 sentence summary of what happened. Focus on the goal and outcome.`

// ExplainRequest contains inputs for explaining a session.
type ExplainRequest struct {
	SessionID session.ID
	Model     string // optional — override default model
	Short     bool   // if true, produce a brief explanation
}

// ExplainResult contains the AI-generated explanation.
type ExplainResult struct {
	Explanation string
	SessionID   session.ID
	Model       string
	TokensUsed  int
}

// Explain generates an AI-powered natural language explanation of a session.
// The result is ephemeral — it is NOT stored.
func (s *SessionService) Explain(ctx context.Context, req ExplainRequest) (*ExplainResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("AI explanation requires an LLM client")
	}

	sess, err := s.store.Get(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}

	userPrompt := buildSessionTranscript(sess)
	if userPrompt == "" {
		return nil, fmt.Errorf("session has no messages to explain")
	}

	systemPrompt := explainSystemPrompt
	if req.Short {
		systemPrompt = explainShortSystemPrompt
	}

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		Model:        req.Model,
		MaxTokens:    4096,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM explain: %w", err)
	}

	return &ExplainResult{
		Explanation: resp.Content,
		SessionID:   sess.ID,
		Model:       resp.Model,
		TokensUsed:  resp.InputTokens + resp.OutputTokens,
	}, nil
}

// ── Rewind ──

// RewindRequest contains inputs for rewinding a session.
type RewindRequest struct {
	SessionID session.ID // the session to rewind
	AtMessage int        // truncate at this message index (1-based, inclusive)
}

// RewindResult contains the outcome of a rewind operation.
type RewindResult struct {
	NewSession      *session.Session
	OriginalID      session.ID
	TruncatedAt     int // message index where the session was truncated
	MessagesRemoved int // number of messages discarded
}

// Rewind creates a new session that is a fork of an existing session,
// truncated at the given message index. The original session is never modified.
func (s *SessionService) Rewind(ctx context.Context, req RewindRequest) (*RewindResult, error) {
	original, err := s.store.Get(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}

	if len(original.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages to rewind")
	}

	if req.AtMessage < 1 || req.AtMessage > len(original.Messages) {
		return nil, fmt.Errorf("message index %d out of range [1, %d]", req.AtMessage, len(original.Messages))
	}

	// Create the rewound session (fork)
	rewound := &session.Session{
		ID:          session.NewID(),
		Provider:    original.Provider,
		Agent:       original.Agent,
		Branch:      original.Branch,
		CommitSHA:   original.CommitSHA,
		ProjectPath: original.ProjectPath,
		StorageMode: original.StorageMode,
		OwnerID:     original.OwnerID,
		ParentID:    original.ID,
		Messages:    make([]session.Message, req.AtMessage),
		FileChanges: append([]session.FileChange(nil), original.FileChanges...), // deep copy
		Links:       append([]session.Link(nil), original.Links...),             // deep copy
		Summary:     fmt.Sprintf("Rewind of %s at message %d", original.ID, req.AtMessage),
	}
	copy(rewound.Messages, original.Messages[:req.AtMessage])

	// Recalculate token usage from truncated messages
	var inputTokens, outputTokens int
	for _, msg := range rewound.Messages {
		if msg.Role == session.RoleUser {
			inputTokens += msg.Tokens
		} else {
			outputTokens += msg.Tokens
		}
	}
	rewound.TokenUsage = session.TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}

	if err := s.store.Save(rewound); err != nil {
		return nil, fmt.Errorf("saving rewound session: %w", err)
	}

	return &RewindResult{
		NewSession:      rewound,
		OriginalID:      original.ID,
		TruncatedAt:     req.AtMessage,
		MessagesRemoved: len(original.Messages) - req.AtMessage,
	}, nil
}

// ── LLM helpers ──

// buildSessionTranscript builds a prompt-friendly transcript from session messages.
// It includes the first 3 and last 5 user messages plus file changes to fit context.
func buildSessionTranscript(sess *session.Session) string {
	if len(sess.Messages) == 0 {
		return ""
	}

	var b strings.Builder

	// Header
	b.WriteString(fmt.Sprintf("Session: %s\n", sess.ID))
	b.WriteString(fmt.Sprintf("Provider: %s\n", sess.Provider))
	if sess.Branch != "" {
		b.WriteString(fmt.Sprintf("Branch: %s\n", sess.Branch))
	}
	if sess.Summary != "" {
		b.WriteString(fmt.Sprintf("Summary: %s\n", sess.Summary))
	}
	b.WriteString("\n")

	// File changes
	if len(sess.FileChanges) > 0 {
		b.WriteString("Files changed:\n")
		for _, fc := range sess.FileChanges {
			b.WriteString(fmt.Sprintf("  - %s (%s)\n", fc.FilePath, fc.ChangeType))
		}
		b.WriteString("\n")
	}

	// Messages — truncation strategy:
	// If ≤ 20 messages, include all. Otherwise first 3 + last 5 user/assistant messages.
	messages := sess.Messages
	if len(messages) > 20 {
		var selected []session.Message
		selected = append(selected, messages[:3]...)
		// Last 5 messages
		start := len(messages) - 5
		if start < 3 {
			start = 3
		}
		selected = append(selected, messages[start:]...)
		messages = selected
		b.WriteString(fmt.Sprintf("(showing %d of %d messages)\n\n", len(messages), len(sess.Messages)))
	}

	b.WriteString("Conversation:\n")
	for _, msg := range messages {
		b.WriteString(fmt.Sprintf("[%s] %s\n", msg.Role, truncate(msg.Content, 2000)))
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				b.WriteString(fmt.Sprintf("  tool:%s → %s\n", tc.Name, truncate(tc.Output, 500)))
			}
		}
	}

	return b.String()
}

// truncate shortens a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ── Helpers ──

// resolveSession resolves a session by ID or by current branch.
func (s *SessionService) resolveSession(id session.ID, projectPath, branch string) (*session.Session, error) {
	if id != "" {
		return s.store.Get(id)
	}
	return s.store.GetLatestByBranch(projectPath, branch)
}

// looksLikeCommitSHA returns true if s looks like a hex commit SHA (7-40 chars).
func looksLikeCommitSHA(str string) bool {
	if len(str) < 7 || len(str) > 40 {
		return false
	}
	for _, c := range str {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
