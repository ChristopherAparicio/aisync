package service

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// WorkItemRequest filters work items when listing them for a project.
type WorkItemRequest struct {
	ProjectPath string
	RemoteURL   string
	Kind        string
}

// WorkItems lists work items (external ticket references) with aggregated cost,
// sorted by estimated cost descending. An empty Kind returns every kind.
func (s *SessionService) WorkItems(_ context.Context, req WorkItemRequest) (*session.WorkItemList, error) {
	refs, err := s.store.DistinctLinkRefs(session.LinkTicket)
	if err != nil {
		return nil, err
	}

	list := &session.WorkItemList{}
	for _, ref := range refs {
		item, buildErr := s.buildWorkItem(ref, req.ProjectPath, req.RemoteURL, false)
		if buildErr != nil || item == nil {
			continue
		}
		if req.Kind != "" && item.Kind != req.Kind {
			continue
		}
		list.Items = append(list.Items, *item)
		list.TotalCost += item.EstimatedCost
		list.TotalSessions += item.SessionCount
	}

	sort.SliceStable(list.Items, func(i, j int) bool {
		return list.Items[i].EstimatedCost > list.Items[j].EstimatedCost
	})
	return list, nil
}

// WorkItem returns a single work item by ticket ref, including its linked
// sessions. Returns ErrSessionNotFound when no session is linked to the ref.
func (s *SessionService) WorkItem(_ context.Context, ref string) (*session.WorkItem, error) {
	item, err := s.buildWorkItem(ref, "", "", true)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, session.ErrSessionNotFound
	}
	return item, nil
}

// buildWorkItem aggregates cost, tokens and activity for one ticket ref. When
// projectPath or remoteURL is set, only matching sessions are counted. Returns
// (nil, nil) when no session remains after filtering.
func (s *SessionService) buildWorkItem(ref, projectPath, remoteURL string, withSessions bool) (*session.WorkItem, error) {
	summaries, err := s.store.GetByLink(session.LinkTicket, ref)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []*session.Session
	for i := range summaries {
		sess, getErr := s.store.Get(summaries[i].ID)
		if getErr != nil {
			continue
		}
		if projectPath != "" && sess.ProjectPath != projectPath {
			continue
		}
		if remoteURL != "" && sess.RemoteURL != remoteURL {
			continue
		}
		sessions = append(sessions, sess)
	}
	if len(sessions) == 0 {
		return nil, nil
	}

	item := &session.WorkItem{Ref: ref}
	for _, sess := range sessions {
		item.SessionCount++
		item.TotalTokens += sess.TokenUsage.TotalTokens
		if est := s.pricing.SessionCost(sess); est != nil {
			item.EstimatedCost += est.TotalCost.TotalCost
		}
		if item.FirstActivity.IsZero() || sess.CreatedAt.Before(item.FirstActivity) {
			item.FirstActivity = sess.CreatedAt
		}
		if sess.CreatedAt.After(item.LastActivity) {
			item.LastActivity = sess.CreatedAt
		}
		if withSessions {
			item.Sessions = append(item.Sessions, sessionToSummary(sess))
		}
	}

	pc := s.projectClassifier(sessions[0])
	item.Kind = deriveKind(pc, ref, sessions)
	if pc != nil {
		item.Source = pc.TicketSource
		if pc.TicketURL != "" {
			item.URL = strings.ReplaceAll(pc.TicketURL, "{id}", ref)
		}
	}
	return item, nil
}

func (s *SessionService) projectClassifier(sess *session.Session) *config.ProjectClassifierConf {
	if s.cfg == nil {
		return nil
	}
	return s.cfg.GetProjectClassifier(sess.RemoteURL, sess.ProjectPath)
}

// deriveKind resolves a work item's kind from project config: by ticket-ref
// prefix when kind_from="prefix", otherwise by the most frequent session type.
func deriveKind(pc *config.ProjectClassifierConf, ref string, sessions []*session.Session) string {
	if pc != nil && pc.KindFrom == "prefix" {
		if k := kindFromPrefix(ref, config.ResolveKinds(pc)); k != "" {
			return k
		}
	}
	return dominantSessionType(sessions)
}

// kindFromPrefix extracts the leading alphabetic segment of a ticket ref and
// returns it (lowercased) when it is a configured kind. "BUG-12" yields "bug".
func kindFromPrefix(ref string, kinds []string) string {
	var b strings.Builder
	for _, r := range ref {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
			continue
		}
		break
	}
	prefix := strings.ToLower(b.String())
	for _, k := range kinds {
		if strings.EqualFold(k, prefix) {
			return k
		}
	}
	return ""
}

// dominantSessionType returns the most frequent non-empty session type, breaking
// ties by first occurrence. Returns "other" when no session type is set.
func dominantSessionType(sessions []*session.Session) string {
	counts := make(map[string]int)
	var order []string
	for _, sess := range sessions {
		t := sess.SessionType
		if t == "" {
			continue
		}
		if counts[t] == 0 {
			order = append(order, t)
		}
		counts[t]++
	}

	best, bestCount := "", 0
	for _, t := range order {
		if counts[t] > bestCount {
			best, bestCount = t, counts[t]
		}
	}
	if best == "" {
		return "other"
	}
	return best
}
