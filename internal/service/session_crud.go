package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

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
					sess, getErr := s.store.Get(sid)
					if getErr == nil {
						s.touchSessionAccessed(sess.ID)
					}
					return sess, getErr
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
	sess, getErr := s.store.Get(sid)
	if getErr == nil {
		s.touchSessionAccessed(sess.ID)
	}
	return sess, getErr
}

// ── List ──

// ListRequest contains inputs for listing sessions.
//
// Scope rules (applied in order):
//   - PRNumber > 0          → sessions linked to this PR (overrides everything else)
//   - Global == true        → cross-project, ignores ProjectPath and Branch
//   - All == true           → all sessions in ProjectPath, ignores Branch
//   - default               → ProjectPath + Branch
//
// Filters (combinable with any scope): Keyword, Provider, OwnerID,
// SessionType, Since, Until, Limit.
type ListRequest struct {
	ProjectPath string
	Branch      string
	Provider    session.ProviderName
	OwnerID     string // filter by session owner (empty = no filter)
	PRNumber    int    // if > 0, list sessions linked to this PR
	All         bool   // if true, ignore Branch and return all project sessions
	Global      bool   // if true, ignore ProjectPath/Branch and search across all projects

	// Filters (applied via FTS5/search engine when Keyword is set, otherwise via store).
	Keyword       string   // free-text keyword (FTS5 search on summary/content when non-empty)
	SessionType   string   // filter by session_type (feature, bug, refactor, …)
	Tags          []string // filter by manual tags; AND semantics (session must have ALL listed tags)
	RemoteURL     string   // filter by normalized git remote URL (substring match, case-insensitive)
	ProjectFilter string   // additional client-side substring filter on Summary.ProjectPath
	Since         string   // RFC3339, "2006-01-02", or relative duration ("7d", "24h", "1w", "2mo")
	Until         string   // same formats as Since
	Limit         int      // max results (0 = no limit for store; defaults to 50 when keyword used)
}

// List returns session summaries matching the given criteria.
//
// When req.Keyword is non-empty, the request is routed through the search engine
// (FTS5 if available, LIKE fallback otherwise) so callers benefit from ranking
// and relevance scoring. Structured-only filters bypass the engine and hit the
// store directly.
func (s *SessionService) List(req ListRequest) ([]session.Summary, error) {
	if req.PRNumber > 0 {
		return s.store.GetByLink(session.LinkPR, strconv.Itoa(req.PRNumber))
	}

	// Scope resolution: Global > All > default (Branch+ProjectPath).
	projectPath := req.ProjectPath
	branch := req.Branch
	if req.Global {
		projectPath = ""
		branch = ""
	} else if req.All {
		branch = ""
	}

	// Route via search engine when a keyword is provided.
	var summaries []session.Summary
	if req.Keyword != "" {
		searchReq := SearchRequest{
			Keyword:     req.Keyword,
			ProjectPath: projectPath,
			Branch:      branch,
			Provider:    req.Provider,
			OwnerID:     session.ID(req.OwnerID),
			SessionType: req.SessionType,
			Since:       req.Since,
			Until:       req.Until,
			Limit:       req.Limit,
		}
		result, err := s.Search(searchReq)
		if err != nil {
			return nil, err
		}
		summaries = result.Sessions
	} else {
		// No keyword: structured filters only via the store.
		listOpts := session.ListOptions{
			ProjectPath: projectPath,
			Branch:      branch,
			All:         req.All || req.Global,
			Provider:    req.Provider,
			OwnerID:     session.ID(req.OwnerID),
		}

		var err error
		summaries, err = s.store.List(listOpts)
		if err != nil {
			return nil, err
		}

		// Apply Since/Until/SessionType filtering in-memory when no keyword
		// (store.List doesn't filter on these fields directly).
		if req.Since != "" || req.Until != "" || req.SessionType != "" {
			summaries, err = filterSummaries(summaries, req)
			if err != nil {
				return nil, err
			}
		}
	}

	// Remote URL filter (substring, case-insensitive). Applied in-memory so
	// it works uniformly across both the FTS5 search path and the store-only
	// path, and tolerates repository renames (e.g. github.com/old-org/repo vs
	// github.com/new-org/repo) by matching just a fragment.
	// We allocate a fresh slice (not summaries[:0]) so we never mutate any
	// backing array potentially shared with the store (in-memory mocks, caches).
	if req.RemoteURL != "" && len(summaries) > 0 {
		needle := strings.ToLower(req.RemoteURL)
		filtered := make([]session.Summary, 0, len(summaries))
		for _, sm := range summaries {
			if strings.Contains(strings.ToLower(sm.RemoteURL), needle) {
				filtered = append(filtered, sm)
			}
		}
		summaries = filtered
	}

	// Project path substring filter (case-insensitive). Useful for narrowing
	// to a worktree subset (e.g. --project "opencode/worktree" matches all
	// OpenCode worktree-based sessions across repos).
	if req.ProjectFilter != "" && len(summaries) > 0 {
		needle := strings.ToLower(req.ProjectFilter)
		filtered := make([]session.Summary, 0, len(summaries))
		for _, sm := range summaries {
			if strings.Contains(strings.ToLower(sm.ProjectPath), needle) {
				filtered = append(filtered, sm)
			}
		}
		summaries = filtered
	}

	// Tag filtering (AND): keep only sessions carrying ALL the requested tags.
	// Always applied last so it composes with all scopes (Global/All/Branch)
	// and with both keyword search and structured-only listing.
	if len(req.Tags) > 0 && len(summaries) > 0 {
		ids := make([]session.ID, 0, len(summaries))
		for _, sm := range summaries {
			ids = append(ids, sm.ID)
		}
		matched, err := s.store.FilterSessionIDsByTags(ids, req.Tags)
		if err != nil {
			return nil, fmt.Errorf("filter by tags: %w", err)
		}
		keep := make(map[session.ID]struct{}, len(matched))
		for _, id := range matched {
			keep[id] = struct{}{}
		}
		filtered := make([]session.Summary, 0, len(summaries))
		for _, sm := range summaries {
			if _, ok := keep[sm.ID]; ok {
				filtered = append(filtered, sm)
			}
		}
		summaries = filtered
	}

	// Decorate summaries with their tags so callers (CLI, web UI, MCP)
	// can display them without an extra round-trip.
	if len(summaries) > 0 {
		ids := make([]session.ID, 0, len(summaries))
		for _, sm := range summaries {
			ids = append(ids, sm.ID)
		}
		if tagMap, tagErr := s.store.GetTagsBatch(ids); tagErr == nil {
			for i := range summaries {
				if tags, ok := tagMap[summaries[i].ID]; ok {
					summaries[i].Tags = tags
				}
			}
		}
	}

	if req.Limit > 0 && len(summaries) > req.Limit {
		summaries = summaries[:req.Limit]
	}
	return summaries, nil
}

// filterSummaries applies in-memory filtering for criteria that the store's
// ListOptions doesn't support natively (Since, Until, SessionType).
func filterSummaries(in []session.Summary, req ListRequest) ([]session.Summary, error) {
	var since, until time.Time
	var err error
	if req.Since != "" {
		since, err = parseFlexibleTime(req.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid 'since' value %q: %w", req.Since, err)
		}
	}
	if req.Until != "" {
		until, err = parseFlexibleTime(req.Until)
		if err != nil {
			return nil, fmt.Errorf("invalid 'until' value %q: %w", req.Until, err)
		}
	}
	out := in[:0]
	for _, sm := range in {
		if !since.IsZero() && sm.CreatedAt.Before(since) {
			continue
		}
		if !until.IsZero() && sm.CreatedAt.After(until) {
			continue
		}
		if req.SessionType != "" && sm.SessionType != req.SessionType {
			continue
		}
		out = append(out, sm)
	}
	return out, nil
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

// TagSession sets the session_type classification tag.
func (s *SessionService) TagSession(_ context.Context, id session.ID, sessionType string) error {
	return s.store.UpdateSessionType(id, sessionType)
}
