package service

import (
	"log"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ClassifySession applies per-project classifier rules to a session:
//   - Extracts ticket IDs from branch name and summary using configured patterns
//   - Creates ticket links for extracted IDs
//   - Infers SessionType from branch name using configured branch rules
//
// Returns the number of changes applied (ticket links created + session type set).
func (s *SessionService) ClassifySession(sess *session.Session) int {
	if s.cfg == nil {
		return 0
	}

	// Find the classifier config for this project.
	pc := s.cfg.GetProjectClassifier(sess.RemoteURL, sess.ProjectPath)
	if pc == nil {
		return 0
	}

	changes := 0

	// 1. Extract ticket IDs and create links.
	if pc.TicketPattern != "" {
		re, err := regexp.Compile(pc.TicketPattern)
		if err != nil {
			log.Printf("[classifier] invalid ticket_pattern %q for project: %v", pc.TicketPattern, err)
		} else {
			tickets := extractTickets(re, sess.Branch, sess.Summary)
			for _, ticket := range tickets {
				if err := s.store.AddLink(sess.ID, session.Link{
					LinkType: session.LinkTicket,
					Ref:      ticket,
				}); err != nil {
					// Ignore duplicate link errors (idempotent).
					if !strings.Contains(err.Error(), "UNIQUE") {
						log.Printf("[classifier] error adding ticket link %s to %s: %v", ticket, sess.ID, err)
					}
				} else {
					changes++
				}
			}
		}
	}

	// 2. Infer SessionType from branch rules.
	if sess.SessionType == "" && len(pc.BranchRules) > 0 && sess.Branch != "" {
		if sessionType := matchBranchRule(pc.BranchRules, sess.Branch); sessionType != "" {
			if err := s.store.UpdateSessionType(sess.ID, sessionType); err == nil {
				sess.SessionType = sessionType
				changes++
			}
		}
	}

	return changes
}

// ClassifyProjectSessions applies classifiers to all sessions for a project.
// Returns (classified, total, error).
func (s *SessionService) ClassifyProjectSessions(remoteURL, projectPath string) (int, int, error) {
	if s.cfg == nil {
		return 0, 0, nil
	}
	pc := s.cfg.GetProjectClassifier(remoteURL, projectPath)
	if pc == nil {
		return 0, 0, nil
	}

	opts := session.ListOptions{}
	if remoteURL != "" {
		opts.RemoteURL = remoteURL // prefer remote URL — matches across worktrees
	} else if projectPath != "" {
		opts.ProjectPath = projectPath
	} else {
		opts.All = true
	}
	summaries, err := s.store.List(opts)
	if err != nil {
		return 0, 0, err
	}

	classified := 0
	for _, sm := range summaries {
		sess, getErr := s.store.Get(sm.ID)
		if getErr != nil {
			continue
		}
		if n := s.ClassifySession(sess); n > 0 {
			classified++
		}
	}
	return classified, len(summaries), nil
}

// extractTickets finds all unique ticket IDs matching the pattern in the given texts.
func extractTickets(re *regexp.Regexp, texts ...string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, text := range texts {
		matches := re.FindAllString(text, -1)
		for _, m := range matches {
			upper := strings.ToUpper(m)
			if !seen[upper] {
				seen[upper] = true
				result = append(result, upper)
			}
		}
	}
	return result
}

// matchBranchRule checks if a branch name matches any configured rule.
// Rules use glob-like patterns: "feature/*" matches "feature/add-login".
func matchBranchRule(rules map[string]string, branch string) string {
	for pattern, sessionType := range rules {
		matched, err := filepath.Match(pattern, branch)
		if err != nil {
			continue
		}
		if matched {
			return sessionType
		}
		// Also try matching just the first segment for patterns like "feature/*"
		// against branches like "feature/deep/nested/path".
		if strings.Contains(pattern, "*") {
			prefix := strings.Split(pattern, "*")[0]
			if strings.HasPrefix(branch, prefix) {
				return sessionType
			}
		}
	}
	return ""
}
