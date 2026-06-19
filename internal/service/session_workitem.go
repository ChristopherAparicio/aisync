package service

import (
	"context"
	"errors"
	"regexp"
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
	Source      string
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
		items, buildErr := s.buildWorkItems(ref, req.ProjectPath, req.RemoteURL, req.Source, false)
		if buildErr != nil {
			continue
		}
		for _, item := range items {
			if req.Kind != "" && item.Kind != req.Kind {
				continue
			}
			list.Items = append(list.Items, *item)
			list.TotalCost += item.EstimatedCost
			list.TotalSessions += item.SessionCount
		}
	}

	sort.SliceStable(list.Items, func(i, j int) bool {
		return list.Items[i].EstimatedCost > list.Items[j].EstimatedCost
	})
	return list, nil
}

// WorkItem returns a single work item by ticket ref, including its linked
// sessions. Returns ErrSessionNotFound when no session is linked to the ref.
func (s *SessionService) WorkItem(_ context.Context, ref string, filters ...WorkItemRequest) (*session.WorkItem, error) {
	req := WorkItemRequest{}
	if len(filters) > 0 {
		req = filters[0]
	}
	items, err := s.buildWorkItems(ref, req.ProjectPath, req.RemoteURL, req.Source, true)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, session.ErrSessionNotFound
	}
	if len(items) == 1 {
		return items[0], nil
	}
	return mergeWorkItems(ref, items), nil
}

func (s *SessionService) buildWorkItems(ref, projectPath, remoteURL, source string, withSessions bool) ([]*session.WorkItem, error) {
	summaries, err := s.store.GetByLink(session.LinkTicket, ref)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return nil, nil
		}
		return nil, err
	}

	type sourcedSession struct {
		session *session.Session
		source  string
		url     string
	}
	var sessions []sourcedSession
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
		itemSource, itemURL := resolveSourceAndURL(s.projectClassifier(sess), ref)
		if source != "" && itemSource != source {
			continue
		}
		sessions = append(sessions, sourcedSession{session: sess, source: itemSource, url: itemURL})
	}
	if len(sessions) == 0 {
		return nil, nil
	}

	groups := make(map[string][]*session.Session)
	metadata := make(map[string]struct{ source, url string })
	var keys []string
	for _, entry := range sessions {
		key := entry.source + "\x00" + entry.url
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
			metadata[key] = struct{ source, url string }{source: entry.source, url: entry.url}
		}
		groups[key] = append(groups[key], entry.session)
	}
	sort.Strings(keys)

	items := make([]*session.WorkItem, 0, len(keys))
	for _, key := range keys {
		meta := metadata[key]
		items = append(items, s.aggregateWorkItem(ref, meta.source, meta.url, groups[key], withSessions))
	}
	return items, nil
}

func (s *SessionService) aggregateWorkItem(ref, sourceName, url string, sessions []*session.Session, withSessions bool) *session.WorkItem {
	item := &session.WorkItem{Ref: ref, Source: sourceName, URL: url}
	for _, sess := range sessions {
		item.SessionCount++

		netTokens := sess.TokenUsage.TotalTokens
		var netCost float64
		if est := s.pricing.SessionCost(sess); est != nil {
			netCost = est.TotalCost.TotalCost
		}

		// Fork-aware deduction: subtract shared-prefix cost from fork sessions
		// to avoid double-counting the shared context.
		rels, _ := s.store.GetForkRelations(sess.ID)
		for _, rel := range rels {
			if rel.ForkID == sess.ID && rel.SharedInputTokens+rel.SharedOutputTokens > 0 && sess.TokenUsage.TotalTokens > 0 {
				sharedTokens := rel.SharedInputTokens + rel.SharedOutputTokens
				ratio := float64(sharedTokens) / float64(sess.TokenUsage.TotalTokens)
				netCost -= netCost * ratio
				netTokens -= sharedTokens
				break
			}
		}
		if netTokens < 0 {
			netTokens = 0
		}
		if netCost < 0 {
			netCost = 0
		}

		item.TotalTokens += netTokens
		item.EstimatedCost += netCost

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
	return item
}

func mergeWorkItems(ref string, items []*session.WorkItem) *session.WorkItem {
	merged := &session.WorkItem{Ref: ref, Source: items[0].Source, URL: items[0].URL}
	mixedSource := false
	for _, item := range items {
		merged.SessionCount += item.SessionCount
		merged.TotalTokens += item.TotalTokens
		merged.EstimatedCost += item.EstimatedCost
		if merged.FirstActivity.IsZero() || item.FirstActivity.Before(merged.FirstActivity) {
			merged.FirstActivity = item.FirstActivity
		}
		if item.LastActivity.After(merged.LastActivity) {
			merged.LastActivity = item.LastActivity
		}
		merged.Sessions = append(merged.Sessions, item.Sessions...)
		if item.Source != merged.Source {
			mixedSource = true
		}
		if merged.Kind == "" {
			merged.Kind = item.Kind
		}
	}
	if mixedSource {
		merged.Source = "mixed"
		merged.URL = ""
	}
	return merged
}

// resolveSourceAndURL returns the tracker source name and resolved URL for a
// ticket ref. It checks ticket_sources entries in order (first pattern matching
// the ref wins), then falls back to the legacy single-source fields.
func resolveSourceAndURL(pc *config.ProjectClassifierConf, ref string) (source, ticketURL string) {
	if pc == nil {
		return "", ""
	}
	for _, ts := range pc.TicketSources {
		if ts.TicketPattern == "" {
			continue
		}
		re, err := regexp.Compile(ts.TicketPattern)
		if err != nil {
			continue
		}
		if re.MatchString(ref) {
			u := ""
			if ts.TicketURL != "" {
				u = strings.ReplaceAll(ts.TicketURL, "{id}", ref)
			}
			return ts.Name, u
		}
	}
	u := ""
	if pc.TicketURL != "" {
		u = strings.ReplaceAll(pc.TicketURL, "{id}", ref)
	}
	return pc.TicketSource, u
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
