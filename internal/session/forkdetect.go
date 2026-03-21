package session

import (
	"crypto/sha256"
	"fmt"
)

// ForkRelation describes the relationship between two sessions
// where one is a fork (copy) of the other.
type ForkRelation struct {
	OriginalID     ID      `json:"original_id"`     // the older/shorter session
	ForkID         ID      `json:"fork_id"`         // the newer session (forked from original)
	ForkPoint      int     `json:"fork_point"`      // message index where they diverge (0-based)
	SharedMessages int     `json:"shared_messages"` // number of identical messages before fork point
	OverlapRatio   float64 `json:"overlap_ratio"`   // 0.0-1.0, shared/min(len(a),len(b))

	// Reason: why the fork was created (summarized from post-fork messages).
	Reason      string `json:"reason,omitempty"`       // LLM-generated summary (e.g. "Switched to API documentation")
	ForkContext string `json:"fork_context,omitempty"` // raw: first user message after fork point (truncated)

	// Token deduplication.
	SharedInputTokens  int `json:"shared_input_tokens"`
	SharedOutputTokens int `json:"shared_output_tokens"`
}

// ForkTree represents a session and all its forks as a tree structure.
type ForkTree struct {
	RootID     ID            `json:"root_id"`
	Root       *ForkTreeNode `json:"root"`
	TotalForks int           `json:"total_forks"`
}

// ForkTreeNode is a node in the fork tree.
type ForkTreeNode struct {
	SessionID ID              `json:"session_id"`
	Summary   string          `json:"summary"`
	Messages  int             `json:"messages"`
	ForkPoint int             `json:"fork_point,omitempty"` // where this fork diverged from parent (0 for root)
	Reason    string          `json:"reason,omitempty"`     // why this fork was created
	Children  []*ForkTreeNode `json:"children,omitempty"`
}

// sessionFingerprint holds content hashes for a session's messages.
type sessionFingerprint struct {
	sessionID ID
	hashes    []string
	msgCount  int
}

// DetectForks analyzes a set of sessions and finds fork relationships
// by comparing message content hashes.
func DetectForks(sessions []*Session) []ForkRelation {
	if len(sessions) < 2 {
		return nil
	}

	const sampleSize = 200

	fps := make([]sessionFingerprint, 0, len(sessions))
	for _, s := range sessions {
		if len(s.Messages) < 3 {
			continue
		}
		limit := len(s.Messages)
		if limit > sampleSize {
			limit = sampleSize
		}
		hashes := make([]string, limit)
		for i := 0; i < limit; i++ {
			hashes[i] = messageContentHash(&s.Messages[i])
		}
		fps = append(fps, sessionFingerprint{
			sessionID: s.ID,
			hashes:    hashes,
			msgCount:  len(s.Messages),
		})
	}

	isFork := make(map[ID]bool)
	var relations []ForkRelation

	for i := 0; i < len(fps); i++ {
		if isFork[fps[i].sessionID] {
			continue
		}
		for j := i + 1; j < len(fps); j++ {
			if isFork[fps[j].sessionID] {
				continue
			}
			rel := compareByContentHash(fps[i], fps[j], sessions)
			if rel != nil {
				relations = append(relations, *rel)
				isFork[rel.ForkID] = true
			}
		}
	}

	return relations
}

// BuildForkTree constructs a tree structure from fork relations.
// Groups all forks that share the same root session.
func BuildForkTree(relations []ForkRelation, sessionLookup map[ID]*Session) []ForkTree {
	if len(relations) == 0 {
		return nil
	}

	// Find all roots: sessions that appear as OriginalID but never as ForkID.
	forkIDs := make(map[ID]bool)
	for _, rel := range relations {
		forkIDs[rel.ForkID] = true
	}

	roots := make(map[ID]bool)
	for _, rel := range relations {
		if !forkIDs[rel.OriginalID] {
			roots[rel.OriginalID] = true
		}
	}

	// Build tree for each root.
	var trees []ForkTree
	for rootID := range roots {
		root := buildNode(rootID, relations, sessionLookup)
		tree := ForkTree{
			RootID: rootID,
			Root:   root,
		}
		tree.TotalForks = countNodes(root) - 1 // exclude root itself
		trees = append(trees, tree)
	}

	return trees
}

func buildNode(id ID, relations []ForkRelation, lookup map[ID]*Session) *ForkTreeNode {
	node := &ForkTreeNode{SessionID: id}
	if s, ok := lookup[id]; ok {
		node.Summary = s.Summary
		node.Messages = len(s.Messages)
	}

	for _, rel := range relations {
		if rel.OriginalID == id {
			child := buildNode(rel.ForkID, relations, lookup)
			child.ForkPoint = rel.ForkPoint
			child.Reason = rel.Reason
			if child.Reason == "" {
				child.Reason = rel.ForkContext
			}
			node.Children = append(node.Children, child)
		}
	}

	return node
}

func countNodes(node *ForkTreeNode) int {
	if node == nil {
		return 0
	}
	count := 1
	for _, child := range node.Children {
		count += countNodes(child)
	}
	return count
}

// messageContentHash produces a short hash of a message's content.
func messageContentHash(msg *Message) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s", msg.Role, msg.Content)))
	return fmt.Sprintf("%x", h[:8])
}

// compareByContentHash checks if two sessions share a common message prefix.
func compareByContentHash(a, b sessionFingerprint, sessions []*Session) *ForkRelation {
	prefix := commonPrefixLen(a.hashes, b.hashes)
	if prefix < 3 {
		return nil
	}

	minLen := a.msgCount
	if b.msgCount < minLen {
		minLen = b.msgCount
	}
	ratio := float64(prefix) / float64(minLen)

	if ratio < 0.5 {
		return nil
	}

	originalID, forkID := a.sessionID, b.sessionID
	if b.msgCount < a.msgCount {
		originalID, forkID = b.sessionID, a.sessionID
	}

	var sharedInput, sharedOutput int
	var forkContext string

	for _, s := range sessions {
		if s.ID == originalID {
			for k := 0; k < prefix && k < len(s.Messages); k++ {
				sharedInput += s.Messages[k].InputTokens
				sharedOutput += s.Messages[k].OutputTokens
			}
		}
		// Extract the first user message after fork point from the FORK session.
		if s.ID == forkID {
			for k := prefix; k < len(s.Messages); k++ {
				if s.Messages[k].Role == RoleUser && s.Messages[k].Content != "" {
					forkContext = s.Messages[k].Content
					if len(forkContext) > 500 {
						forkContext = forkContext[:497] + "..."
					}
					break
				}
			}
		}
	}

	return &ForkRelation{
		OriginalID:         originalID,
		ForkID:             forkID,
		ForkPoint:          prefix,
		SharedMessages:     prefix,
		OverlapRatio:       ratio,
		ForkContext:        forkContext,
		SharedInputTokens:  sharedInput,
		SharedOutputTokens: sharedOutput,
	}
}

// commonPrefixLen returns the length of the longest common prefix.
func commonPrefixLen(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
