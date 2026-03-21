package service

import (
	"context"
	"fmt"
	"strconv"

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
	OwnerID     string // filter by session owner (empty = no filter)
	PRNumber    int    // if > 0, list sessions linked to this PR
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
		OwnerID:     session.ID(req.OwnerID),
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

// TagSession sets the session_type classification tag.
func (s *SessionService) TagSession(_ context.Context, id session.ID, sessionType string) error {
	return s.store.UpdateSessionType(id, sessionType)
}
