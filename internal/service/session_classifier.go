package service

import (
	"log"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ClassifySession applies per-project classifier rules to a session using a
// priority cascade: commit message > summary prefix > branch rules > agent rules.
//
// It also extracts ticket IDs and updates session status from summary prefixes.
//
// Returns the number of changes applied.
func (s *SessionService) ClassifySession(sess *session.Session) int {
	if s.cfg == nil {
		return 0
	}

	// Find the classifier config for this project.
	pc := s.cfg.GetProjectClassifier(sess.RemoteURL, sess.ProjectPath)

	changes := 0

	// ── 1. Extract ticket IDs and create links ──
	if pc != nil && pc.TicketPattern != "" {
		re, err := regexp.Compile(pc.TicketPattern)
		if err != nil {
			log.Printf("[classifier] invalid ticket_pattern %q: %v", pc.TicketPattern, err)
		} else {
			tickets := extractTickets(re, sess.Branch, sess.Summary)
			for _, ticket := range tickets {
				if err := s.store.AddLink(sess.ID, session.Link{
					LinkType: session.LinkTicket,
					Ref:      ticket,
				}); err != nil {
					if !strings.Contains(err.Error(), "UNIQUE") {
						log.Printf("[classifier] error adding ticket link %s to %s: %v", ticket, sess.ID, err)
					}
				} else {
					changes++
				}
			}
		}
	}

	// ── 2. Classify session type (priority cascade) ──
	// Only classify if not already typed.
	if sess.SessionType == "" {
		sessionType := s.classifyType(sess, pc)
		if sessionType != "" {
			if err := s.store.UpdateSessionType(sess.ID, sessionType); err == nil {
				sess.SessionType = sessionType
				changes++
			}
		}
	}

	// ── 3. Update session status from summary prefix ──
	statusRules := config.DefaultStatusRules
	if pc != nil && len(pc.StatusRules) > 0 {
		statusRules = pc.StatusRules
	}
	if newStatus := matchSummaryPrefix(statusRules, sess.Summary); newStatus != "" {
		if string(sess.Status) != newStatus {
			sess.Status = session.SessionStatus(newStatus)
			changes++
		}
	}

	return changes
}

// classifyType determines session type using a priority cascade:
//
//  1. Conventional Commit prefix in summary (highest priority)
//  2. Branch name rules
//  3. Agent name rules (lowest priority, excluding generic agents)
//
// Returns empty string if no rule matches (the LLM tagger handles those).
func (s *SessionService) classifyType(sess *session.Session, pc *config.ProjectClassifierConf) string {
	// Priority 1: Conventional Commit prefix in summary.
	// Matches patterns like "[COMMIT] fix: ...", "[COMMIT] 🔀 Fix ...", "feat: ...", "fix(auth): ..."
	commitRules := config.DefaultCommitRules
	if pc != nil && len(pc.CommitRules) > 0 {
		commitRules = pc.CommitRules
	}
	if t := matchConventionalCommit(commitRules, sess.Summary); t != "" {
		return t
	}

	// Priority 2: Branch name rules.
	branchRules := map[string]string{}
	if pc != nil {
		branchRules = pc.BranchRules
	}
	if len(branchRules) > 0 && sess.Branch != "" {
		if t := matchBranchRule(branchRules, sess.Branch); t != "" {
			return t
		}
	}

	// Priority 3: Agent name rules.
	agentRules := config.DefaultAgentRules
	if pc != nil && len(pc.AgentRules) > 0 {
		agentRules = pc.AgentRules
	}
	if sess.Agent != "" {
		// Only apply agent rules for non-generic agents.
		// "build" and "coder" are too generic — they build anything.
		if t, ok := agentRules[sess.Agent]; ok {
			return t
		}
	}

	return ""
}

// ClassifyProjectSessions applies classifiers to all sessions for a project.
// Also classifies sessions without a project-specific config using defaults.
// Returns (classified, total, error).
func (s *SessionService) ClassifyProjectSessions(remoteURL, projectPath string) (int, int, error) {
	if s.cfg == nil {
		return 0, 0, nil
	}

	opts := session.ListOptions{}
	if remoteURL != "" {
		opts.RemoteURL = remoteURL
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

// ── Helper functions ──

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
func matchBranchRule(rules map[string]string, branch string) string {
	for pattern, sessionType := range rules {
		matched, err := filepath.Match(pattern, branch)
		if err != nil {
			continue
		}
		if matched {
			return sessionType
		}
		// Also try prefix matching for "feature/*" against "feature/deep/nested".
		if strings.Contains(pattern, "*") {
			prefix := strings.Split(pattern, "*")[0]
			if strings.HasPrefix(branch, prefix) {
				return sessionType
			}
		}
	}
	return ""
}

// matchConventionalCommit extracts a Conventional Commit prefix from a summary.
// Handles formats like:
//   - "fix: description"
//   - "feat(scope): description"
//   - "[COMMIT] fix: ..."
//   - "[COMMIT] 🔀 Fix ..."
//   - "[PR] feat: ..."
func matchConventionalCommit(rules map[string]string, summary string) string {
	// Strip common prefixes: [COMMIT], [PR], [WIP], [DONE], emoji.
	text := summary
	for _, prefix := range []string{"[COMMIT]", "[PR]", "[WIP]", "[DONE]"} {
		text = strings.TrimPrefix(text, prefix)
	}
	text = strings.TrimSpace(text)
	// Strip leading emoji (🔀, ✅, etc.).
	for len(text) > 0 {
		r := []rune(text)
		if r[0] > 127 && r[0] != '(' { // non-ASCII, likely emoji
			text = strings.TrimSpace(string(r[1:]))
		} else {
			break
		}
	}

	// Try to match "prefix:" or "prefix(scope):" at the start.
	lower := strings.ToLower(text)
	for prefix, sessionType := range rules {
		if strings.HasPrefix(lower, prefix+":") || strings.HasPrefix(lower, prefix+"(") {
			return sessionType
		}
	}

	// Also try case-insensitive word match at start: "Fix ...", "Refactor ..."
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	firstWord := strings.ToLower(words[0])
	wordMap := map[string]string{
		"fix":      "bug",
		"fixes":    "bug",
		"fixed":    "bug",
		"bugfix":   "bug",
		"hotfix":   "bug",
		"add":      "feature",
		"feature":  "feature",
		"refactor": "refactor",
		"cleanup":  "refactor",
		"migrate":  "refactor",
	}
	if t, ok := wordMap[firstWord]; ok {
		// Only apply if it matches configured rules too.
		if _, configured := rules[t]; configured || len(rules) == 0 {
			return t
		}
		return t
	}

	return ""
}

// matchSummaryPrefix checks if a session summary starts with a known prefix.
func matchSummaryPrefix(rules map[string]string, summary string) string {
	for prefix, status := range rules {
		if strings.HasPrefix(summary, prefix) {
			return status
		}
	}
	return ""
}
