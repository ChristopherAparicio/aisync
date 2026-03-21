package service

import (
	"context"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// TimelineEntry represents one entry in a branch's work timeline.
type TimelineEntry struct {
	Type      string    `json:"type"` // "session" or "commit"
	Timestamp time.Time `json:"timestamp"`

	// Session fields (when Type == "session").
	Session   *session.Summary          `json:"session,omitempty"`
	Objective *session.SessionObjective `json:"objective,omitempty"`
	ForkCount int                       `json:"fork_count,omitempty"`

	// Commit fields (when Type == "commit").
	Commit          *CommitEntry `json:"commit,omitempty"`
	LinkedSessionID session.ID   `json:"linked_session_id,omitempty"`
}

// CommitEntry is a git commit in the timeline.
type CommitEntry struct {
	SHA       string    `json:"sha"`
	ShortSHA  string    `json:"short_sha"`
	Message   string    `json:"message"`
	Author    string    `json:"author"`
	Timestamp time.Time `json:"timestamp"`
}

// TimelineRequest specifies what to include in the timeline.
type TimelineRequest struct {
	Branch      string
	ProjectPath string
	Since       time.Time
	Limit       int
}

// BranchTimeline builds an interleaved timeline of sessions and git commits.
func (s *SessionService) BranchTimeline(ctx context.Context, req TimelineRequest) ([]TimelineEntry, error) {
	if req.Limit == 0 {
		req.Limit = 50
	}
	since := req.Since
	if since.IsZero() {
		since = time.Now().AddDate(0, 0, -30)
	}

	var entries []TimelineEntry

	// 1. Get sessions on this branch.
	summaries, err := s.store.List(session.ListOptions{
		Branch:      req.Branch,
		ProjectPath: req.ProjectPath,
		Since:       since,
		All:         req.Branch == "",
	})
	if err != nil {
		return nil, err
	}

	// Bulk-load objectives.
	sessionIDs := make([]session.ID, len(summaries))
	for i := range summaries {
		sessionIDs[i] = summaries[i].ID
	}
	objectives, _ := s.store.ListObjectives(sessionIDs)

	// Build session windows for commit correlation.
	type sessionWindow struct {
		id    session.ID
		start time.Time
		end   time.Time
	}
	var windows []sessionWindow

	for i := range summaries {
		sm := &summaries[i]
		entry := TimelineEntry{
			Type:      "session",
			Timestamp: sm.CreatedAt,
			Session:   sm,
		}
		if objectives != nil {
			entry.Objective = objectives[sm.ID]
		}

		// Fork count.
		forkRels, fErr := s.store.GetForkRelations(sm.ID)
		if fErr == nil {
			for _, rel := range forkRels {
				if rel.OriginalID == sm.ID {
					entry.ForkCount++
				}
			}
		}

		entries = append(entries, entry)
		windows = append(windows, sessionWindow{
			id:    sm.ID,
			start: sm.CreatedAt,
			end:   sm.CreatedAt.Add(4 * time.Hour),
		})
	}

	// 2. Get git commits (if git client is available).
	if s.git != nil && req.Branch != "" {
		sinceStr := since.Format(time.RFC3339)
		commits, gitErr := s.git.ListCommits(req.Branch, sinceStr, "", req.Limit)
		if gitErr == nil {
			for _, c := range commits {
				ts, _ := time.Parse(time.RFC3339, c.Timestamp)
				if ts.IsZero() {
					continue
				}

				entry := TimelineEntry{
					Type:      "commit",
					Timestamp: ts,
					Commit: &CommitEntry{
						SHA:       c.SHA,
						ShortSHA:  shortSHA(c.SHA),
						Message:   c.Message,
						Author:    c.Author,
						Timestamp: ts,
					},
				}

				// Correlate: find session whose window contains this commit.
				for _, sw := range windows {
					if ts.After(sw.start) && ts.Before(sw.end) {
						entry.LinkedSessionID = sw.id
						_ = s.store.AddLink(sw.id, session.Link{
							LinkType: session.LinkCommit,
							Ref:      c.SHA,
						})
						break
					}
				}

				entries = append(entries, entry)
			}
		}
	}

	// 3. Sort newest first.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	if len(entries) > req.Limit {
		entries = entries[:req.Limit]
	}

	return entries, nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
