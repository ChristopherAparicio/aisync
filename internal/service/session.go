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
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	capturesvc "github.com/ChristopherAparicio/aisync/internal/capture"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/platform"
	"github.com/ChristopherAparicio/aisync/internal/pricing"
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
	pricing   *pricing.Calculator
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
	Pricing   *pricing.Calculator // optional — nil uses defaults
	Git       *git.Client         // optional
	Platform  platform.Platform   // optional
	LLM       llm.Client          // optional — nil disables AI features
}

// NewSessionService creates a SessionService with all dependencies.
func NewSessionService(cfg SessionServiceConfig) *SessionService {
	conv := cfg.Converter
	if conv == nil {
		conv = converter.New()
	}
	calc := cfg.Pricing
	if calc == nil {
		calc = pricing.NewCalculator()
	}
	return &SessionService{
		store:     cfg.Store,
		registry:  cfg.Registry,
		scanner:   cfg.Scanner,
		converter: conv,
		pricing:   calc,
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

// CaptureAll detects all sessions for the given project/provider and captures each one.
// Requires --provider to be set (CLI enforces this).
func (s *SessionService) CaptureAll(req CaptureRequest) ([]*CaptureResult, error) {
	var svc *capturesvc.Service
	if s.scanner != nil {
		svc = capturesvc.NewServiceWithScanner(s.registry, s.store, s.scanner)
	} else {
		svc = capturesvc.NewService(s.registry, s.store)
	}

	ownerID := s.resolveOwner()

	results, err := svc.CaptureAll(capturesvc.Request{
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

	var captureResults []*CaptureResult
	for _, r := range results {
		captureResults = append(captureResults, &CaptureResult{
			Session:      r.Session,
			Provider:     r.Provider,
			SecretsFound: r.SecretsFound,
		})
	}
	return captureResults, nil
}

// CaptureByID captures a specific session by its provider-native ID.
func (s *SessionService) CaptureByID(req CaptureRequest, sessionID session.ID) (*CaptureResult, error) {
	var svc *capturesvc.Service
	if s.scanner != nil {
		svc = capturesvc.NewServiceWithScanner(s.registry, s.store, s.scanner)
	} else {
		svc = capturesvc.NewService(s.registry, s.store)
	}

	ownerID := s.resolveOwner()

	result, err := svc.CaptureByID(capturesvc.Request{
		ProjectPath:  req.ProjectPath,
		Branch:       req.Branch,
		Mode:         req.Mode,
		ProviderName: req.ProviderName,
		Message:      req.Message,
		OwnerID:      ownerID,
	}, sessionID)
	if err != nil {
		return nil, err
	}

	return &CaptureResult{
		Session:      result.Session,
		Provider:     result.Provider,
		SecretsFound: result.SecretsFound,
	}, nil
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

// ListTree builds a hierarchical tree of sessions using ParentID relationships.
// Sessions without a parent become root nodes. Fork detection compares user messages
// across siblings to identify retries.
func (s *SessionService) ListTree(ctx context.Context, req ListRequest) ([]session.SessionTreeNode, error) {
	summaries, err := s.List(req)
	if err != nil {
		return nil, err
	}

	return buildTree(summaries), nil
}

// buildTree constructs a tree from a flat list of summaries using ParentID.
// The algorithm processes nodes in two passes:
//  1. Create all nodes indexed by ID.
//  2. Link children to parents. Nodes whose parent is not in the set become roots.
//
// Children are linked via pointers first, then flattened to values on output,
// ensuring grandchildren are correctly included.
func buildTree(summaries []session.Summary) []session.SessionTreeNode {
	if len(summaries) == 0 {
		return nil
	}

	// Index by ID for quick lookup.
	type treeNode struct {
		summary  session.Summary
		children []*treeNode
		isFork   bool
	}

	byID := make(map[session.ID]*treeNode, len(summaries))
	for _, sm := range summaries {
		byID[sm.ID] = &treeNode{summary: sm}
	}

	// Build parent → children relationships.
	var roots []*treeNode
	for _, sm := range summaries {
		node := byID[sm.ID]
		if sm.ParentID != "" {
			parent, ok := byID[sm.ParentID]
			if ok {
				node.isFork = true
				parent.children = append(parent.children, node)
				continue
			}
		}
		roots = append(roots, node)
	}

	// Recursively convert to the public type.
	var convert func(n *treeNode) session.SessionTreeNode
	convert = func(n *treeNode) session.SessionTreeNode {
		out := session.SessionTreeNode{
			Summary: n.summary,
			IsFork:  n.isFork,
		}
		for _, child := range n.children {
			out.Children = append(out.Children, convert(child))
		}
		return out
	}

	result := make([]session.SessionTreeNode, 0, len(roots))
	for _, r := range roots {
		result = append(result, convert(r))
	}

	return result
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
	ProjectPath  string
	Branch       string
	Provider     session.ProviderName
	All          bool
	IncludeTools bool // if true, aggregate tool usage across sessions
}

// BranchStats holds aggregated stats per branch.
type BranchStats struct {
	Branch       string
	TotalTokens  int
	TotalCost    float64 // estimated cost in USD
	SessionCount int
}

// StatsResult contains aggregated statistics.
type StatsResult struct {
	TotalSessions int
	TotalMessages int
	TotalTokens   int
	TotalCost     float64 // estimated cost in USD
	PerBranch     []*BranchStats
	PerProvider   map[session.ProviderName]int
	TopFiles      []FileEntry          // sorted by count descending, max 10
	ToolStats     *AggregatedToolStats `json:"tool_stats,omitempty"` // populated when IncludeTools is true
}

// AggregatedToolStats holds tool usage aggregated across multiple sessions.
type AggregatedToolStats struct {
	Tools      []session.ToolUsageEntry `json:"tools"`
	TotalCalls int                      `json:"total_calls"`
	TotalCost  session.Cost             `json:"total_cost,omitempty"`
	Warning    string                   `json:"warning,omitempty"` // set if any session used compact/summary mode
}

// FileEntry is a file path with its touch count.
type FileEntry struct {
	Path  string
	Count int
}

// EstimateCost computes the cost breakdown for a session.
func (s *SessionService) EstimateCost(ctx context.Context, idOrSHA string) (*session.CostEstimate, error) {
	sess, err := s.Get(idOrSHA)
	if err != nil {
		return nil, err
	}
	return s.pricing.SessionCost(sess), nil
}

// ToolUsage computes per-tool token usage breakdown for a session.
// If tool calls don't have token data, it estimates from content size (~4 chars/token).
func (s *SessionService) ToolUsage(ctx context.Context, idOrSHA string) (*session.ToolUsageStats, error) {
	sess, err := s.Get(idOrSHA)
	if err != nil {
		return nil, err
	}

	type toolAgg struct {
		calls        int
		inputTokens  int
		outputTokens int
		totalDur     int
		errorCount   int
	}

	perTool := make(map[string]*toolAgg)
	totalCalls := 0

	for i := range sess.Messages {
		msg := &sess.Messages[i]
		for j := range msg.ToolCalls {
			tc := &msg.ToolCalls[j]
			totalCalls++

			agg, ok := perTool[tc.Name]
			if !ok {
				agg = &toolAgg{}
				perTool[tc.Name] = agg
			}
			agg.calls++

			// Use explicit token data if available, otherwise estimate from content size.
			inTok := tc.InputTokens
			outTok := tc.OutputTokens
			if inTok == 0 && len(tc.Input) > 0 {
				inTok = estimateTokens(tc.Input)
			}
			if outTok == 0 && len(tc.Output) > 0 {
				outTok = estimateTokens(tc.Output)
			}
			agg.inputTokens += inTok
			agg.outputTokens += outTok

			if tc.DurationMs > 0 {
				agg.totalDur += tc.DurationMs
			}
			if tc.State == session.ToolStateError {
				agg.errorCount++
			}
		}
	}

	// Build sorted result.
	names := make([]string, 0, len(perTool))
	for name := range perTool {
		names = append(names, name)
	}
	sort.Strings(names)

	var grandTotal int
	entries := make([]session.ToolUsageEntry, 0, len(names))
	for _, name := range names {
		agg := perTool[name]
		total := agg.inputTokens + agg.outputTokens
		grandTotal += total

		entry := session.ToolUsageEntry{
			Name:         name,
			Calls:        agg.calls,
			InputTokens:  agg.inputTokens,
			OutputTokens: agg.outputTokens,
			TotalTokens:  total,
			ErrorCount:   agg.errorCount,
		}
		if agg.calls > 0 && agg.totalDur > 0 {
			entry.AvgDuration = agg.totalDur / agg.calls
		}
		entries = append(entries, entry)
	}

	// Compute percentages.
	for i := range entries {
		if grandTotal > 0 {
			entries[i].Percentage = float64(entries[i].TotalTokens) / float64(grandTotal) * 100
		}
	}

	// Sort by total tokens descending (most expensive first).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TotalTokens > entries[j].TotalTokens
	})

	// Optionally compute cost per tool.
	var totalCost session.Cost
	if s.pricing != nil {
		for i := range entries {
			e := &entries[i]
			// Use session's primary model for tool cost estimation.
			model := primaryModel(sess)
			if model != "" {
				e.Cost = s.pricing.MessageCost(model, e.InputTokens, e.OutputTokens)
				totalCost.InputCost += e.Cost.InputCost
				totalCost.OutputCost += e.Cost.OutputCost
				totalCost.TotalCost += e.Cost.TotalCost
			}
		}
		totalCost.Currency = "USD"
	}

	result := &session.ToolUsageStats{
		Tools:      entries,
		TotalCalls: totalCalls,
		TotalCost:  totalCost,
	}

	// Warn when storage mode limits tool call data fidelity.
	if sess.StorageMode == session.StorageModeCompact || sess.StorageMode == session.StorageModeSummary {
		result.Warning = fmt.Sprintf("session was captured in %q mode — tool call data may be incomplete; use --mode full for accurate tool accounting", sess.StorageMode)
	}

	return result, nil
}

// estimateTokens roughly estimates token count from text length.
// Uses the common heuristic of ~4 characters per token for English/JSON text.
func estimateTokens(text string) int {
	n := len(text) / 4
	if n == 0 && len(text) > 0 {
		n = 1
	}
	return n
}

// primaryModel returns the most-used model in a session (by message count).
func primaryModel(sess *session.Session) string {
	counts := make(map[string]int)
	for i := range sess.Messages {
		if m := sess.Messages[i].Model; m != "" {
			counts[m]++
		}
	}
	var best string
	var bestCount int
	for m, c := range counts {
		if c > bestCount {
			best = m
			bestCount = c
		}
	}
	return best
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

	// Tool aggregation state (used when IncludeTools is true).
	type toolAgg struct {
		calls, errors, inputTok, outputTok, totalDur int
	}
	var perTool map[string]*toolAgg
	var hasCompactSessions bool
	if req.IncludeTools {
		perTool = make(map[string]*toolAgg)
	}

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

		// File changes + cost + tool usage (requires loading full session)
		full, getErr := s.store.Get(sm.ID)
		if getErr == nil {
			for _, fc := range full.FileChanges {
				fileCounts[fc.FilePath]++
			}

			// Cost estimation
			est := s.pricing.SessionCost(full)
			sessionCost := est.TotalCost.TotalCost
			result.TotalCost += sessionCost
			bs.TotalCost += sessionCost

			// Tool aggregation
			if req.IncludeTools {
				if full.StorageMode == session.StorageModeCompact || full.StorageMode == session.StorageModeSummary {
					hasCompactSessions = true
				}
				for i := range full.Messages {
					for j := range full.Messages[i].ToolCalls {
						tc := &full.Messages[i].ToolCalls[j]
						agg, exists := perTool[tc.Name]
						if !exists {
							agg = &toolAgg{}
							perTool[tc.Name] = agg
						}
						agg.calls++
						inTok, outTok := tc.InputTokens, tc.OutputTokens
						if inTok == 0 && len(tc.Input) > 0 {
							inTok = estimateTokens(tc.Input)
						}
						if outTok == 0 && len(tc.Output) > 0 {
							outTok = estimateTokens(tc.Output)
						}
						agg.inputTok += inTok
						agg.outputTok += outTok
						if tc.DurationMs > 0 {
							agg.totalDur += tc.DurationMs
						}
						if tc.State == session.ToolStateError {
							agg.errors++
						}
					}
				}
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

	// Build aggregated tool stats.
	if req.IncludeTools && len(perTool) > 0 {
		names := make([]string, 0, len(perTool))
		for name := range perTool {
			names = append(names, name)
		}
		sort.Strings(names)

		var grandTotal, totalCalls int
		entries := make([]session.ToolUsageEntry, 0, len(names))
		for _, name := range names {
			agg := perTool[name]
			total := agg.inputTok + agg.outputTok
			grandTotal += total
			totalCalls += agg.calls

			entry := session.ToolUsageEntry{
				Name:         name,
				Calls:        agg.calls,
				InputTokens:  agg.inputTok,
				OutputTokens: agg.outputTok,
				TotalTokens:  total,
				ErrorCount:   agg.errors,
			}
			if agg.calls > 0 && agg.totalDur > 0 {
				entry.AvgDuration = agg.totalDur / agg.calls
			}
			entries = append(entries, entry)
		}

		// Compute percentages.
		for i := range entries {
			if grandTotal > 0 {
				entries[i].Percentage = float64(entries[i].TotalTokens) / float64(grandTotal) * 100
			}
		}

		// Sort by total tokens descending.
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].TotalTokens > entries[j].TotalTokens
		})

		aggStats := &AggregatedToolStats{
			Tools:      entries,
			TotalCalls: totalCalls,
		}
		if hasCompactSessions {
			aggStats.Warning = "some sessions were captured in compact/summary mode — tool data may be incomplete"
		}
		result.ToolStats = aggStats
	}

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

// ── AnalyzeEfficiency ──

// efficiencySystemPrompt is the system instruction for AI efficiency analysis.
const efficiencySystemPrompt = `You are a senior AI coding efficiency analyst. Given statistics about an AI coding session
(token usage, tool call breakdown, message patterns, timing), produce a structured JSON efficiency report with these fields:
- score: integer 0-100 (100 = perfectly efficient, 0 = extremely wasteful)
- summary: one-paragraph assessment of the session's efficiency
- strengths: array of short strings describing what went well
- issues: array of short strings describing inefficiencies found
- suggestions: array of actionable improvement recommendations
- patterns: array of detected anti-patterns (e.g., "retry loops", "over-reading", "redundant writes", "large context")

Scoring guidelines:
- 80-100: Excellent. Minimal wasted tokens, focused tool usage, clean conversation flow.
- 60-79: Good. Some minor inefficiencies but generally well-structured.
- 40-59: Fair. Noticeable waste — retry loops, excessive reads, or bloated contexts.
- 20-39: Poor. Significant token waste from retries, hallucination recovery, or unfocused exploration.
- 0-19: Very poor. Most tokens wasted on failed attempts or circular conversation.

Respond ONLY with valid JSON, no markdown fences, no explanation.`

// EfficiencyRequest contains inputs for analyzing session efficiency.
type EfficiencyRequest struct {
	SessionID session.ID // the session to analyze
	Model     string     // optional — override default model
}

// EfficiencyResult contains the AI-generated efficiency report.
type EfficiencyResult struct {
	Report     session.EfficiencyReport
	SessionID  session.ID
	Model      string
	TokensUsed int
}

// AnalyzeEfficiency generates an LLM-powered efficiency analysis for a session.
// It computes tool usage stats, token distribution, and message patterns,
// then asks the LLM to evaluate overall efficiency.
func (s *SessionService) AnalyzeEfficiency(ctx context.Context, req EfficiencyRequest) (*EfficiencyResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("efficiency analysis requires an LLM client")
	}

	sess, err := s.store.Get(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}

	if len(sess.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages to analyze")
	}

	prompt := buildEfficiencyPrompt(sess, s.pricing)

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: efficiencySystemPrompt,
		UserPrompt:   prompt,
		Model:        req.Model,
		MaxTokens:    2048,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM efficiency analysis: %w", err)
	}

	var report session.EfficiencyReport
	if jsonErr := json.Unmarshal([]byte(resp.Content), &report); jsonErr != nil {
		return nil, fmt.Errorf("parsing LLM efficiency response: %w (raw: %s)", jsonErr, truncate(resp.Content, 200))
	}

	// Clamp score to valid range.
	if report.Score < 0 {
		report.Score = 0
	}
	if report.Score > 100 {
		report.Score = 100
	}

	return &EfficiencyResult{
		Report:     report,
		SessionID:  sess.ID,
		Model:      resp.Model,
		TokensUsed: resp.InputTokens + resp.OutputTokens,
	}, nil
}

// buildEfficiencyPrompt constructs a data-rich prompt for the LLM efficiency analysis.
// It includes token breakdown, tool call statistics, message patterns, and timing.
func buildEfficiencyPrompt(sess *session.Session, calc *pricing.Calculator) string {
	var b strings.Builder

	// Header
	b.WriteString(fmt.Sprintf("Session: %s\n", sess.ID))
	b.WriteString(fmt.Sprintf("Provider: %s\n", sess.Provider))
	if sess.Branch != "" {
		b.WriteString(fmt.Sprintf("Branch: %s\n", sess.Branch))
	}
	b.WriteString(fmt.Sprintf("Messages: %d\n", len(sess.Messages)))
	b.WriteString(fmt.Sprintf("Tokens: input=%d output=%d total=%d\n",
		sess.TokenUsage.InputTokens, sess.TokenUsage.OutputTokens, sess.TokenUsage.TotalTokens))
	b.WriteString("\n")

	// Cost estimate
	if calc != nil {
		est := calc.SessionCost(sess)
		if est.TotalCost.TotalCost > 0 {
			b.WriteString(fmt.Sprintf("Estimated cost: $%.4f (input=$%.4f output=$%.4f)\n\n",
				est.TotalCost.TotalCost, est.TotalCost.InputCost, est.TotalCost.OutputCost))
		}
	}

	// Message role distribution
	roleCounts := make(map[session.MessageRole]int)
	var totalToolCalls, errorToolCalls int
	for i := range sess.Messages {
		msg := &sess.Messages[i]
		roleCounts[msg.Role]++
		for j := range msg.ToolCalls {
			totalToolCalls++
			if msg.ToolCalls[j].State == session.ToolStateError {
				errorToolCalls++
			}
		}
	}
	b.WriteString("Message distribution:\n")
	for role, count := range roleCounts {
		b.WriteString(fmt.Sprintf("  %s: %d\n", role, count))
	}
	b.WriteString("\n")

	// Tool call breakdown
	if totalToolCalls > 0 {
		b.WriteString(fmt.Sprintf("Tool calls: %d total, %d errors (%.0f%% error rate)\n",
			totalToolCalls, errorToolCalls,
			safePercent(errorToolCalls, totalToolCalls)))

		// Per-tool summary
		type toolAgg struct {
			calls, errors, inputTok, outputTok, totalDur int
		}
		perTool := make(map[string]*toolAgg)
		for i := range sess.Messages {
			for j := range sess.Messages[i].ToolCalls {
				tc := &sess.Messages[i].ToolCalls[j]
				agg, ok := perTool[tc.Name]
				if !ok {
					agg = &toolAgg{}
					perTool[tc.Name] = agg
				}
				agg.calls++
				inTok, outTok := tc.InputTokens, tc.OutputTokens
				if inTok == 0 && len(tc.Input) > 0 {
					inTok = estimateTokens(tc.Input)
				}
				if outTok == 0 && len(tc.Output) > 0 {
					outTok = estimateTokens(tc.Output)
				}
				agg.inputTok += inTok
				agg.outputTok += outTok
				agg.totalDur += tc.DurationMs
				if tc.State == session.ToolStateError {
					agg.errors++
				}
			}
		}

		b.WriteString("\nPer-tool breakdown:\n")
		for name, agg := range perTool {
			b.WriteString(fmt.Sprintf("  %s: calls=%d tokens=%d errors=%d",
				name, agg.calls, agg.inputTok+agg.outputTok, agg.errors))
			if agg.calls > 0 && agg.totalDur > 0 {
				b.WriteString(fmt.Sprintf(" avg_duration=%dms", agg.totalDur/agg.calls))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Conversation flow patterns (detect potential retries)
	b.WriteString("Conversation flow (last 20 messages):\n")
	start := 0
	if len(sess.Messages) > 20 {
		start = len(sess.Messages) - 20
	}
	for i := start; i < len(sess.Messages); i++ {
		msg := &sess.Messages[i]
		toolCount := len(msg.ToolCalls)
		tokens := msg.InputTokens + msg.OutputTokens
		b.WriteString(fmt.Sprintf("  [%d] %s tokens=%d tools=%d content_len=%d\n",
			i+1, msg.Role, tokens, toolCount, len(msg.Content)))
	}

	// File changes
	if len(sess.FileChanges) > 0 {
		b.WriteString(fmt.Sprintf("\nFiles changed: %d\n", len(sess.FileChanges)))
		limit := len(sess.FileChanges)
		if limit > 15 {
			limit = 15
		}
		for _, fc := range sess.FileChanges[:limit] {
			b.WriteString(fmt.Sprintf("  %s (%s)\n", fc.FilePath, fc.ChangeType))
		}
		if len(sess.FileChanges) > 15 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(sess.FileChanges)-15))
		}
	}

	return b.String()
}

// safePercent computes (part/total)*100, returning 0 if total is 0.
func safePercent(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
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
		ID:              session.NewID(),
		Provider:        original.Provider,
		Agent:           original.Agent,
		Branch:          original.Branch,
		CommitSHA:       original.CommitSHA,
		ProjectPath:     original.ProjectPath,
		StorageMode:     original.StorageMode,
		OwnerID:         original.OwnerID,
		ParentID:        original.ID,
		ForkedAtMessage: req.AtMessage,
		Messages:        make([]session.Message, req.AtMessage),
		FileChanges:     append([]session.FileChange(nil), original.FileChanges...), // deep copy
		Links:           append([]session.Link(nil), original.Links...),             // deep copy
		Summary:         fmt.Sprintf("Rewind of %s at message %d", original.ID, req.AtMessage),
	}
	copy(rewound.Messages, original.Messages[:req.AtMessage])

	// Recalculate token usage from truncated messages
	var inputTokens, outputTokens int
	for _, msg := range rewound.Messages {
		inputTokens += msg.InputTokens
		outputTokens += msg.OutputTokens
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

// ── Garbage Collection ──

// GCRequest contains inputs for garbage collection.
type GCRequest struct {
	OlderThan  string // duration string like "30d", "24h", "7d"
	KeepLatest int    // keep the N most recent sessions per branch (0 = no per-branch limit)
	DryRun     bool   // if true, count but don't delete
}

// GCResult contains the outcome of a garbage collection operation.
type GCResult struct {
	Deleted int  `json:"deleted"` // number of sessions deleted (0 if DryRun)
	Would   int  `json:"would"`   // number of sessions that would be deleted (only in DryRun)
	DryRun  bool `json:"dry_run"`
}

// GarbageCollect removes old sessions based on age and count policies.
// Age-based: deletes sessions older than OlderThan duration.
// Count-based: keeps only KeepLatest sessions per branch (deletes oldest first).
// Both policies can be combined — the union of sessions matching either policy is deleted.
func (s *SessionService) GarbageCollect(ctx context.Context, req GCRequest) (*GCResult, error) {
	if req.OlderThan == "" && req.KeepLatest <= 0 {
		return nil, fmt.Errorf("specify --older-than and/or --keep-latest")
	}

	result := &GCResult{DryRun: req.DryRun}

	// Age-based cleanup
	if req.OlderThan != "" {
		dur, err := parseDuration(req.OlderThan)
		if err != nil {
			return nil, fmt.Errorf("invalid duration %q: %w", req.OlderThan, err)
		}
		cutoff := time.Now().UTC().Add(-dur)

		if req.DryRun {
			// Count sessions that would be deleted
			summaries, listErr := s.store.List(session.ListOptions{All: true})
			if listErr != nil {
				return nil, fmt.Errorf("listing sessions: %w", listErr)
			}
			for _, sm := range summaries {
				full, getErr := s.store.Get(sm.ID)
				if getErr != nil {
					continue
				}
				if full.CreatedAt.Before(cutoff) {
					result.Would++
				}
			}
		} else {
			count, delErr := s.store.DeleteOlderThan(cutoff)
			if delErr != nil {
				return nil, fmt.Errorf("deleting old sessions: %w", delErr)
			}
			result.Deleted += count
		}
	}

	// Count-based cleanup (per branch)
	if req.KeepLatest > 0 {
		summaries, listErr := s.store.List(session.ListOptions{All: true})
		if listErr != nil {
			return nil, fmt.Errorf("listing sessions: %w", listErr)
		}

		// Group by branch
		perBranch := make(map[string][]session.Summary)
		for _, sm := range summaries {
			perBranch[sm.Branch] = append(perBranch[sm.Branch], sm)
		}

		// For each branch, keep only the most recent KeepLatest.
		// List() returns sessions ordered by created_at DESC, so we skip the first N.
		for _, sessions := range perBranch {
			if len(sessions) <= req.KeepLatest {
				continue
			}
			toDelete := sessions[req.KeepLatest:]
			for _, sm := range toDelete {
				if req.DryRun {
					result.Would++
				} else {
					if delErr := s.store.Delete(sm.ID); delErr == nil {
						result.Deleted++
					}
				}
			}
		}
	}

	return result, nil
}

// parseDuration parses a human-friendly duration string.
// Supports: "30d" (days), "24h" (hours), "7d12h" (days+hours).
// Falls back to time.ParseDuration for standard Go durations.
func parseDuration(s string) (time.Duration, error) {
	// Check for day notation: "Nd" or "NdMh"
	if strings.ContainsAny(s, "d") {
		var days, hours int
		parts := strings.Split(s, "d")
		if len(parts) >= 1 && parts[0] != "" {
			d, err := strconv.Atoi(parts[0])
			if err != nil {
				return 0, fmt.Errorf("invalid day count: %w", err)
			}
			days = d
		}
		if len(parts) >= 2 && parts[1] != "" {
			// Remaining part after "d", e.g. "12h"
			rem, err := time.ParseDuration(parts[1])
			if err != nil {
				return 0, fmt.Errorf("invalid remaining duration: %w", err)
			}
			hours = int(rem.Hours())
		}
		return time.Duration(days)*24*time.Hour + time.Duration(hours)*time.Hour, nil
	}

	// Fallback to standard Go duration
	return time.ParseDuration(s)
}

// ── Diff ──

// DiffRequest contains inputs for comparing two sessions.
type DiffRequest struct {
	LeftID  string // session ID or commit SHA
	RightID string // session ID or commit SHA
}

// Diff compares two sessions side-by-side and returns a structured diff.
// It computes token deltas, cost deltas, file overlap, tool usage comparison,
// and identifies where the message sequences diverge.
func (s *SessionService) Diff(ctx context.Context, req DiffRequest) (*session.DiffResult, error) {
	if req.LeftID == "" || req.RightID == "" {
		return nil, fmt.Errorf("both left and right session IDs are required")
	}

	left, err := s.Get(req.LeftID)
	if err != nil {
		return nil, fmt.Errorf("left session: %w", err)
	}

	right, err := s.Get(req.RightID)
	if err != nil {
		return nil, fmt.Errorf("right session: %w", err)
	}

	result := &session.DiffResult{
		Left:         buildDiffSide(left),
		Right:        buildDiffSide(right),
		TokenDelta:   computeTokenDelta(left, right),
		FileDiff:     computeFileDiff(left, right),
		ToolDiff:     computeToolDiff(left, right),
		MessageDelta: computeMessageDelta(left, right),
	}

	// Cost delta (uses pricing calculator).
	leftCost := s.pricing.SessionCost(left)
	rightCost := s.pricing.SessionCost(right)
	result.CostDelta = session.CostDelta{
		LeftCost:  leftCost.TotalCost.TotalCost,
		RightCost: rightCost.TotalCost.TotalCost,
		Delta:     rightCost.TotalCost.TotalCost - leftCost.TotalCost.TotalCost,
		Currency:  "USD",
	}

	return result, nil
}

// buildDiffSide extracts summary metadata from a session for one side of a diff.
func buildDiffSide(sess *session.Session) session.DiffSide {
	return session.DiffSide{
		ID:           sess.ID,
		Provider:     sess.Provider,
		Branch:       sess.Branch,
		Summary:      sess.Summary,
		MessageCount: len(sess.Messages),
		TotalTokens:  sess.TokenUsage.TotalTokens,
		StorageMode:  sess.StorageMode,
	}
}

// computeTokenDelta calculates the difference in token usage between two sessions.
func computeTokenDelta(left, right *session.Session) session.TokenDelta {
	return session.TokenDelta{
		InputDelta:  right.TokenUsage.InputTokens - left.TokenUsage.InputTokens,
		OutputDelta: right.TokenUsage.OutputTokens - left.TokenUsage.OutputTokens,
		TotalDelta:  right.TokenUsage.TotalTokens - left.TokenUsage.TotalTokens,
	}
}

// computeFileDiff groups file changes into shared, left-only, and right-only.
func computeFileDiff(left, right *session.Session) session.FileDiff {
	leftFiles := make(map[string]struct{}, len(left.FileChanges))
	for _, fc := range left.FileChanges {
		leftFiles[fc.FilePath] = struct{}{}
	}

	rightFiles := make(map[string]struct{}, len(right.FileChanges))
	for _, fc := range right.FileChanges {
		rightFiles[fc.FilePath] = struct{}{}
	}

	var shared, leftOnly, rightOnly []string

	for f := range leftFiles {
		if _, ok := rightFiles[f]; ok {
			shared = append(shared, f)
		} else {
			leftOnly = append(leftOnly, f)
		}
	}
	for f := range rightFiles {
		if _, ok := leftFiles[f]; !ok {
			rightOnly = append(rightOnly, f)
		}
	}

	// Sort for deterministic output.
	sort.Strings(shared)
	sort.Strings(leftOnly)
	sort.Strings(rightOnly)

	return session.FileDiff{
		Shared:    shared,
		LeftOnly:  leftOnly,
		RightOnly: rightOnly,
	}
}

// computeToolDiff compares tool usage between two sessions.
func computeToolDiff(left, right *session.Session) session.ToolDiff {
	leftTools := countToolCalls(left)
	rightTools := countToolCalls(right)

	// Collect all tool names.
	allTools := make(map[string]struct{})
	for name := range leftTools {
		allTools[name] = struct{}{}
	}
	for name := range rightTools {
		allTools[name] = struct{}{}
	}

	// Sort tool names for deterministic output.
	names := make([]string, 0, len(allTools))
	for name := range allTools {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]session.ToolDiffEntry, 0, len(names))
	for _, name := range names {
		lc := leftTools[name]
		rc := rightTools[name]
		entries = append(entries, session.ToolDiffEntry{
			Name:       name,
			LeftCalls:  lc,
			RightCalls: rc,
			CallsDelta: rc - lc,
		})
	}

	return session.ToolDiff{Entries: entries}
}

// countToolCalls returns a map of tool name → call count for a session.
func countToolCalls(sess *session.Session) map[string]int {
	counts := make(map[string]int)
	for i := range sess.Messages {
		for j := range sess.Messages[i].ToolCalls {
			counts[sess.Messages[i].ToolCalls[j].Name]++
		}
	}
	return counts
}

// computeMessageDelta finds the common prefix length and counts remaining messages.
// Two messages are considered identical when they share the same role and content.
func computeMessageDelta(left, right *session.Session) session.MessageDelta {
	minLen := len(left.Messages)
	if len(right.Messages) < minLen {
		minLen = len(right.Messages)
	}

	commonPrefix := 0
	for i := 0; i < minLen; i++ {
		lm := &left.Messages[i]
		rm := &right.Messages[i]
		if lm.Role != rm.Role || lm.Content != rm.Content {
			break
		}
		commonPrefix++
	}

	return session.MessageDelta{
		CommonPrefix: commonPrefix,
		LeftAfter:    len(left.Messages) - commonPrefix,
		RightAfter:   len(right.Messages) - commonPrefix,
	}
}

// ── Off-Topic Detection ──

// OffTopicRequest contains inputs for off-topic detection.
type OffTopicRequest struct {
	ProjectPath string  // required — limits to this project
	Branch      string  // required — the branch to analyze
	Threshold   float64 // 0.0–1.0 overlap threshold; below = off-topic (default 0.2)
}

// DetectOffTopic compares file changes across all sessions on a branch
// and flags sessions whose files don't overlap with the branch's dominant topic.
// A session is "off-topic" when the fraction of its files shared with at least
// one other session on the branch falls below the threshold.
func (s *SessionService) DetectOffTopic(ctx context.Context, req OffTopicRequest) (*session.OffTopicResult, error) {
	if req.Branch == "" {
		return nil, fmt.Errorf("branch is required for off-topic detection")
	}

	threshold := req.Threshold
	if threshold <= 0 {
		threshold = 0.2 // default: 20% overlap minimum
	}

	summaries, err := s.store.List(session.ListOptions{
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
	})
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	if len(summaries) < 2 {
		// With 0 or 1 sessions, off-topic detection is meaningless.
		entries := make([]session.OffTopicEntry, 0, len(summaries))
		for _, sm := range summaries {
			full, getErr := s.store.Get(sm.ID)
			if getErr != nil {
				continue
			}
			files := uniqueFiles(full)
			entries = append(entries, session.OffTopicEntry{
				ID:        sm.ID,
				Provider:  sm.Provider,
				Summary:   sm.Summary,
				Files:     files,
				Overlap:   1.0,
				CreatedAt: sm.CreatedAt,
			})
		}
		return &session.OffTopicResult{
			Branch:   req.Branch,
			Sessions: entries,
			Total:    len(entries),
		}, nil
	}

	// Load full sessions to access file changes.
	type sessionFiles struct {
		summary session.Summary
		files   map[string]struct{}
	}
	loaded := make([]sessionFiles, 0, len(summaries))
	for _, sm := range summaries {
		full, getErr := s.store.Get(sm.ID)
		if getErr != nil {
			continue // skip sessions that can't be loaded
		}
		fileSet := make(map[string]struct{}, len(full.FileChanges))
		for _, fc := range full.FileChanges {
			fileSet[fc.FilePath] = struct{}{}
		}
		loaded = append(loaded, sessionFiles{summary: sm, files: fileSet})
	}

	// Build global file frequency: how many sessions touch each file.
	fileFreq := make(map[string]int)
	for _, sf := range loaded {
		for f := range sf.files {
			fileFreq[f]++
		}
	}

	// Score each session: overlap = fraction of its files that appear in ≥2 sessions.
	entries := make([]session.OffTopicEntry, 0, len(loaded))
	offTopicCount := 0
	for _, sf := range loaded {
		files := sortedKeys(sf.files)
		overlap := computeOverlap(sf.files, fileFreq)
		isOff := overlap < threshold && len(sf.files) > 0
		if isOff {
			offTopicCount++
		}
		entries = append(entries, session.OffTopicEntry{
			ID:         sf.summary.ID,
			Provider:   sf.summary.Provider,
			Summary:    sf.summary.Summary,
			Files:      files,
			Overlap:    overlap,
			IsOffTopic: isOff,
			CreatedAt:  sf.summary.CreatedAt,
		})
	}

	// Top files: sorted by frequency descending, capped at 10.
	topFiles := topFilesByFrequency(fileFreq, 10)

	return &session.OffTopicResult{
		Branch:   req.Branch,
		Sessions: entries,
		TopFiles: topFiles,
		Total:    len(entries),
		OffTopic: offTopicCount,
	}, nil
}

// computeOverlap returns the fraction of files in fileSet that appear in ≥2 sessions.
// Returns 1.0 for empty file sets (sessions without files aren't off-topic).
func computeOverlap(fileSet map[string]struct{}, fileFreq map[string]int) float64 {
	if len(fileSet) == 0 {
		return 1.0
	}
	shared := 0
	for f := range fileSet {
		if fileFreq[f] >= 2 {
			shared++
		}
	}
	return float64(shared) / float64(len(fileSet))
}

// uniqueFiles returns a sorted, deduplicated list of file paths from a session.
func uniqueFiles(sess *session.Session) []string {
	seen := make(map[string]struct{}, len(sess.FileChanges))
	for _, fc := range sess.FileChanges {
		seen[fc.FilePath] = struct{}{}
	}
	return sortedKeys(seen)
}

// sortedKeys returns the sorted keys of a string set.
func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// topFilesByFrequency returns the top N files by how many sessions touch them.
func topFilesByFrequency(freq map[string]int, n int) []string {
	type entry struct {
		file  string
		count int
	}
	entries := make([]entry, 0, len(freq))
	for f, c := range freq {
		entries = append(entries, entry{f, c})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].file < entries[j].file
	})
	if len(entries) > n {
		entries = entries[:n]
	}
	result := make([]string, len(entries))
	for i, e := range entries {
		result[i] = e.file
	}
	return result
}

// ── Cost Forecasting ──

// ForecastRequest contains inputs for cost forecasting.
type ForecastRequest struct {
	ProjectPath string // optional — limit to this project
	Branch      string // optional — limit to this branch
	Period      string // "daily" or "weekly" (default: "weekly")
	Days        int    // look-back window in days (default: 90)
}

// Forecast analyzes historical session costs and projects future spending.
// It buckets sessions by time period, applies linear regression for trend,
// and recommends cheaper model alternatives.
func (s *SessionService) Forecast(ctx context.Context, req ForecastRequest) (*session.ForecastResult, error) {
	period := req.Period
	if period == "" {
		period = "weekly"
	}
	if period != "daily" && period != "weekly" {
		return nil, fmt.Errorf("period must be 'daily' or 'weekly', got %q", period)
	}

	lookbackDays := req.Days
	if lookbackDays <= 0 {
		lookbackDays = 90
	}

	now := time.Now().UTC()
	since := now.AddDate(0, 0, -lookbackDays)

	// Query all sessions in the time window.
	summaries, err := s.store.List(session.ListOptions{
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		All:         req.Branch == "" && req.ProjectPath == "",
	})
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	// Filter to time window.
	var filtered []session.Summary
	for _, sm := range summaries {
		if !sm.CreatedAt.Before(since) {
			filtered = append(filtered, sm)
		}
	}

	if len(filtered) == 0 {
		return &session.ForecastResult{
			Period:   period,
			TrendDir: "stable",
		}, nil
	}

	// Load full sessions for cost calculation + model breakdown.
	var loaded []sessionCostEntry
	globalModels := make(map[string]*forecastModelAgg)

	for _, sm := range filtered {
		full, getErr := s.store.Get(sm.ID)
		if getErr != nil {
			continue
		}
		estimate := s.pricing.SessionCost(full)
		loaded = append(loaded, sessionCostEntry{
			createdAt: full.CreatedAt,
			cost:      estimate.TotalCost.TotalCost,
			tokens:    full.TokenUsage.TotalTokens,
		})
		for _, mc := range estimate.PerModel {
			g, ok := globalModels[mc.Model]
			if !ok {
				g = &forecastModelAgg{}
				globalModels[mc.Model] = g
			}
			g.cost += mc.Cost.TotalCost
			g.tokens += mc.InputTokens + mc.OutputTokens
			g.count++
		}
	}

	// Build time buckets.
	bucketDuration := 7 * 24 * time.Hour // weekly
	if period == "daily" {
		bucketDuration = 24 * time.Hour
	}

	buckets := buildCostBuckets(loaded, since, now, bucketDuration)

	// Compute totals.
	var totalCost float64
	for _, b := range buckets {
		totalCost += b.Cost
	}

	avgPerBucket := 0.0
	if len(buckets) > 0 {
		avgPerBucket = totalCost / float64(len(buckets))
	}

	// Linear regression on bucket costs for trend.
	trendPerDay, trendDir := computeTrend(buckets, bucketDuration)

	// Project forward.
	projected30d := math.Max(0, avgPerBucket*30.0/bucketDuration.Hours()*24+trendPerDay*15) // avg + mid-point trend
	projected90d := math.Max(0, avgPerBucket*90.0/bucketDuration.Hours()*24+trendPerDay*45)

	// Model breakdown with recommendations.
	modelBreakdown := buildModelBreakdown(globalModels, totalCost, s.pricing)

	return &session.ForecastResult{
		Period:         period,
		Buckets:        buckets,
		TotalCost:      totalCost,
		AvgPerBucket:   avgPerBucket,
		SessionCount:   len(loaded),
		Projected30d:   math.Round(projected30d*10000) / 10000, // round to 4 decimals
		Projected90d:   math.Round(projected90d*10000) / 10000,
		TrendPerDay:    math.Round(trendPerDay*10000) / 10000,
		TrendDir:       trendDir,
		ModelBreakdown: modelBreakdown,
	}, nil
}

// buildCostBuckets groups session costs into time buckets.
func buildCostBuckets(sessions []sessionCostEntry, start, end time.Time, bucketDur time.Duration) []session.CostBucket {
	if len(sessions) == 0 {
		return nil
	}

	// Determine bucket boundaries.
	var buckets []session.CostBucket
	for t := start; t.Before(end); t = t.Add(bucketDur) {
		bucketEnd := t.Add(bucketDur)
		if bucketEnd.After(end) {
			bucketEnd = end
		}
		buckets = append(buckets, session.CostBucket{
			Start: t,
			End:   bucketEnd,
		})
	}

	// Assign sessions to buckets.
	for _, sc := range sessions {
		for i := range buckets {
			if !sc.createdAt.Before(buckets[i].Start) && sc.createdAt.Before(buckets[i].End) {
				buckets[i].Cost += sc.cost
				buckets[i].Tokens += sc.tokens
				buckets[i].SessionCount++
				break
			}
		}
	}

	return buckets
}

// sessionCostEntry is a lightweight struct for bucket building.
type sessionCostEntry struct {
	createdAt time.Time
	cost      float64
	tokens    int
}

// forecastModelAgg accumulates per-model cost data for forecasting.
type forecastModelAgg struct {
	cost   float64
	tokens int
	count  int
}

// computeTrend applies simple linear regression on bucket costs to determine the trend.
// Returns the daily cost change and a direction string.
func computeTrend(buckets []session.CostBucket, bucketDur time.Duration) (float64, string) {
	n := len(buckets)
	if n < 2 {
		return 0, "stable"
	}

	// Linear regression: y = a + b*x, where x is bucket index, y is cost.
	var sumX, sumY, sumXY, sumX2 float64
	for i, b := range buckets {
		x := float64(i)
		y := b.Cost
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	nf := float64(n)
	denom := nf*sumX2 - sumX*sumX
	if denom == 0 {
		return 0, "stable"
	}

	slope := (nf*sumXY - sumX*sumY) / denom // cost change per bucket
	daysPerBucket := bucketDur.Hours() / 24.0
	trendPerDay := slope / daysPerBucket

	dir := "stable"
	// Only flag as increasing/decreasing if the change is >5% of the average.
	avg := sumY / nf
	if avg > 0 && math.Abs(slope)/avg > 0.05 {
		if trendPerDay > 0 {
			dir = "increasing"
		} else {
			dir = "decreasing"
		}
	}

	return trendPerDay, dir
}

// buildModelBreakdown creates per-model cost data with savings recommendations.
func buildModelBreakdown(models map[string]*forecastModelAgg, totalCost float64, calc *pricing.Calculator) []session.ModelForecast {
	entries := make([]session.ModelForecast, 0, len(models))

	for model, agg := range models {
		share := 0.0
		if totalCost > 0 {
			share = (agg.cost / totalCost) * 100
		}

		var rec string
		if altModel, savings, ok := calc.CheaperAlternative(model); ok && savings > 0.1 {
			rec = fmt.Sprintf("Switch to %s to save ~%.0f%%", altModel, savings*100)
		}

		entries = append(entries, session.ModelForecast{
			Model:          model,
			Cost:           agg.cost,
			Tokens:         agg.tokens,
			SessionCount:   agg.count,
			Share:          math.Round(share*10) / 10,
			Recommendation: rec,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Cost > entries[j].Cost // most expensive first
	})

	return entries
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
