package skillresolver

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Service Port Interface ──

// ResolverServicer is the Use Case Port for skill resolution.
// Driving adapters (CLI, API) depend on this interface.
type ResolverServicer interface {
	// Resolve analyzes missed skills for a session and proposes/applies improvements.
	Resolve(ctx context.Context, req ResolveRequest) (*ResolveResult, error)
}

// ── Dependencies (injected via config) ──

// SessionGetter is the minimal interface needed to retrieve a session.
// Satisfied by service.SessionServicer and service.SessionCRUD.
type SessionGetter interface {
	Get(idOrSHA string) (*session.Session, error)
}

// AnalysisGetter is the minimal interface needed to retrieve analysis results.
// Satisfied by service.AnalysisServicer.
type AnalysisGetter interface {
	GetLatestAnalysis(sessionID string) (*analysis.SessionAnalysis, error)
}

// ── Service Implementation ──

// Service orchestrates skill resolution: observer → analyze → propose → apply.
type Service struct {
	sessions SessionGetter
	analyses AnalysisGetter
	analyzer SkillAnalyzer
	logger   *log.Logger

	// maxMessages controls how many user messages to send to the LLM.
	maxMessages int
}

// ServiceConfig holds dependencies for creating a Service.
type ServiceConfig struct {
	// Sessions provides session lookup (required).
	Sessions SessionGetter

	// Analyses provides analysis/observation lookup (required).
	Analyses AnalysisGetter

	// Analyzer is the LLM-based skill analyzer (required).
	Analyzer SkillAnalyzer

	// Logger for diagnostics (optional, defaults to log.Default()).
	Logger *log.Logger

	// MaxMessages limits user messages sent to the analyzer (default: 10).
	MaxMessages int
}

// NewService creates a new skill resolver service.
func NewService(cfg ServiceConfig) *Service {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	maxMessages := cfg.MaxMessages
	if maxMessages <= 0 {
		maxMessages = 10
	}
	return &Service{
		sessions:    cfg.Sessions,
		analyses:    cfg.Analyses,
		analyzer:    cfg.Analyzer,
		logger:      logger,
		maxMessages: maxMessages,
	}
}

// Resolve implements ResolverServicer.
func (s *Service) Resolve(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	start := time.Now()

	result := &ResolveResult{
		SessionID: req.SessionID,
		Verdict:   VerdictNoChange,
	}

	// 1. Load the session.
	sess, err := s.sessions.Get(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session %s: %w", req.SessionID, err)
	}

	// 2. Get the skill observation from the latest analysis.
	observation, err := s.getSkillObservation(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("getting skill observation for %s: %w", req.SessionID, err)
	}
	if observation == nil || len(observation.Missed) == 0 {
		s.logger.Printf("[skillresolver] no missed skills for session %s", req.SessionID)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 3. Determine which missed skills to analyze.
	missed := observation.Missed
	if len(req.SkillNames) > 0 {
		missed = filterSkillNames(missed, req.SkillNames)
	}
	if len(missed) == 0 {
		s.logger.Printf("[skillresolver] no matching missed skills after filtering")
		result.Duration = time.Since(start)
		return result, nil
	}

	// 4. Extract user messages.
	userMessages := extractUserMessages(sess.Messages, s.maxMessages)

	// 5. For each missed skill, try to read its SKILL.md and analyze.
	for _, skillName := range missed {
		select {
		case <-ctx.Done():
			result.Error = ctx.Err().Error()
			result.Duration = time.Since(start)
			return result, ctx.Err()
		default:
		}

		improvements, analyzeErr := s.analyzeSkill(ctx, skillName, sess, userMessages, observation)
		if analyzeErr != nil {
			s.logger.Printf("[skillresolver] error analyzing skill %q: %v", skillName, analyzeErr)
			continue
		}
		result.Improvements = append(result.Improvements, improvements...)
	}

	// 6. Apply improvements if not dry-run.
	if !req.DryRun && len(result.Improvements) > 0 {
		applied := s.applyImprovements(result.Improvements)
		result.Applied = applied
	}

	// 7. Determine verdict.
	result.Verdict = s.computeVerdict(result)
	result.Duration = time.Since(start)

	s.logger.Printf("[skillresolver] resolved %d improvements for session %s (applied=%d, verdict=%s, duration=%s)",
		len(result.Improvements), req.SessionID, result.Applied, result.Verdict, result.Duration.Round(time.Millisecond))

	return result, nil
}

// ── Internal Methods ──

// getSkillObservation retrieves the SkillObservation from the latest analysis.
func (s *Service) getSkillObservation(sessionID string) (*analysis.SkillObservation, error) {
	sa, err := s.analyses.GetLatestAnalysis(sessionID)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return nil, nil
	}
	return sa.Report.SkillObservation, nil
}

// analyzeSkill loads a skill's SKILL.md and asks the analyzer to propose improvements.
func (s *Service) analyzeSkill(
	ctx context.Context,
	skillName string,
	sess *session.Session,
	userMessages []string,
	observation *analysis.SkillObservation,
) ([]SkillImprovement, error) {
	// Find the skill path from the registry (available skills).
	skillPath := s.findSkillPath(skillName, observation.Available)
	if skillPath == "" {
		return nil, fmt.Errorf("skill %q has no known file path", skillName)
	}

	// Read the SKILL.md file.
	sf, err := ReadSkillFile(skillPath)
	if err != nil {
		return nil, fmt.Errorf("reading SKILL.md for %q: %w", skillName, err)
	}

	// Build the analysis input.
	input := AnalyzeInput{
		SkillName:          skillName,
		SkillPath:          skillPath,
		CurrentContent:     sf.Raw,
		CurrentDescription: sf.Description,
		CurrentKeywords:    extractKeywordsFromDescription(sf.Description),
		UserMessages:       userMessages,
		SessionSummary:     sess.Summary,
		SessionID:          string(sess.ID),
	}

	output, err := s.analyzer.Analyze(ctx, input)
	if err != nil {
		return nil, err
	}

	return output.Improvements, nil
}

// findSkillPath looks up the file path for a skill name.
// For now, this is a simple heuristic — the skill observer stores names but not paths.
// We check standard locations: ~/.config/opencode/skills/<name>/SKILL.md
// and project-level .opencode/skills/<name>/SKILL.md.
func (s *Service) findSkillPath(skillName string, _ []string) string {
	// Check standard global skill location.
	homeDir, err := homeDir()
	if err == nil {
		path := fmt.Sprintf("%s/.config/opencode/skills/%s/SKILL.md", homeDir, skillName)
		if fileExists(path) {
			return path
		}
	}

	return ""
}

// applyImprovements writes improvements to disk. Returns count of applied changes.
func (s *Service) applyImprovements(improvements []SkillImprovement) int {
	applied := 0

	// Group improvements by skill path.
	byPath := make(map[string][]SkillImprovement)
	for _, imp := range improvements {
		if imp.SkillPath != "" {
			byPath[imp.SkillPath] = append(byPath[imp.SkillPath], imp)
		}
	}

	for path, imps := range byPath {
		sf, err := ReadSkillFile(path)
		if err != nil {
			s.logger.Printf("[skillresolver] cannot read %s for apply: %v", path, err)
			continue
		}

		changed := false
		for _, imp := range imps {
			if ApplyImprovement(sf, imp) {
				changed = true
			}
		}

		if changed {
			if err := WriteSkillFile(sf); err != nil {
				s.logger.Printf("[skillresolver] cannot write %s: %v", path, err)
				continue
			}
			applied += len(imps)
			s.logger.Printf("[skillresolver] applied %d improvements to %s", len(imps), path)
		}
	}

	return applied
}

// computeVerdict determines the overall resolution verdict.
func (s *Service) computeVerdict(result *ResolveResult) Verdict {
	if len(result.Improvements) == 0 {
		return VerdictNoChange
	}
	if result.Applied == 0 {
		return VerdictPending // improvements proposed but not applied (dry-run)
	}
	if result.Applied == len(result.Improvements) {
		return VerdictFixed
	}
	return VerdictPartial
}

// ── Helpers ──

// extractUserMessages returns the content of user messages from a session.
func extractUserMessages(messages []session.Message, maxMessages int) []string {
	var result []string
	for i := range messages {
		if messages[i].Role != session.RoleUser {
			continue
		}
		content := messages[i].Content
		if content == "" {
			continue
		}
		result = append(result, content)
		if maxMessages > 0 && len(result) >= maxMessages {
			break
		}
	}
	return result
}

// extractKeywordsFromDescription splits a description into keyword tokens.
func extractKeywordsFromDescription(desc string) []string {
	words := splitWords(desc)
	var keywords []string
	for _, w := range words {
		w = cleanWord(w)
		if len(w) >= 3 {
			keywords = append(keywords, w)
		}
	}
	return keywords
}

// filterSkillNames returns only the skills that are in the filter list.
func filterSkillNames(missed []string, filter []string) []string {
	filterSet := make(map[string]bool, len(filter))
	for _, f := range filter {
		filterSet[f] = true
	}
	var result []string
	for _, m := range missed {
		if filterSet[m] {
			result = append(result, m)
		}
	}
	return result
}

// splitWords splits text on whitespace.
func splitWords(s string) []string {
	var words []string
	word := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if word != "" {
				words = append(words, word)
				word = ""
			}
		} else {
			word += string(r)
		}
	}
	if word != "" {
		words = append(words, word)
	}
	return words
}

// cleanWord removes common punctuation and lowercases.
func cleanWord(w string) string {
	// Strip leading/trailing punctuation.
	for len(w) > 0 && isPunct(w[0]) {
		w = w[1:]
	}
	for len(w) > 0 && isPunct(w[len(w)-1]) {
		w = w[:len(w)-1]
	}
	result := make([]byte, len(w))
	for i := 0; i < len(w); i++ {
		c := w[i]
		if c >= 'A' && c <= 'Z' {
			c = c + 32
		}
		result[i] = c
	}
	return string(result)
}

func isPunct(c byte) bool {
	return c == ',' || c == '.' || c == '!' || c == '?' || c == ';' || c == ':' || c == '(' || c == ')' || c == '"' || c == '\''
}

// homeDir returns the user's home directory.
func homeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home, nil
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
