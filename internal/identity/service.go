package identity

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ServiceConfig configures the identity matching service.
type ServiceConfig struct {
	// MinConfidence is the minimum confidence level to include in suggestions.
	// Matches below this threshold are discarded. Default: ConfidenceLow (0.4).
	MinConfidence MatchConfidence

	// AutoLinkExact controls whether exact matches (email or name) are
	// automatically linked without requiring admin approval. Default: false.
	AutoLinkExact bool

	// Logger for the service. If nil, a no-op logger is used.
	Logger *slog.Logger
}

// Service orchestrates identity matching between Git users and Slack members.
type Service struct {
	slackClient   SlackClient
	store         storage.UserStore
	minScore      float64
	autoLinkExact bool
	logger        *slog.Logger
}

// NewService creates an identity matching service.
// Returns nil if slackClient is nil.
func NewService(client SlackClient, store storage.UserStore, cfg ServiceConfig) *Service {
	if client == nil || store == nil {
		return nil
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	minScore := confidenceToMinScore(cfg.MinConfidence)
	if minScore == 0 {
		minScore = 0.4 // default: ConfidenceLow
	}

	return &Service{
		slackClient:   client,
		store:         store,
		minScore:      minScore,
		autoLinkExact: cfg.AutoLinkExact,
		logger:        logger,
	}
}

// SyncSlackMembers fetches Slack workspace members, matches them against
// Git users, and returns suggestions for unlinked users.
//
// If AutoLinkExact is true, exact matches (email or name) are automatically
// linked and the user's slack_id/slack_name are updated in the store.
func (s *Service) SyncSlackMembers() (*SyncResult, error) {
	if s == nil {
		return nil, fmt.Errorf("identity service not configured")
	}

	// Step 1: Fetch Slack members
	slackMembers, err := s.slackClient.ListMembers()
	if err != nil {
		return nil, fmt.Errorf("fetching slack members: %w", err)
	}

	// Filter to active, non-bot members
	var activeMembers []SlackMember
	for _, m := range slackMembers {
		if !m.IsBot && !m.Deleted {
			activeMembers = append(activeMembers, m)
		}
	}

	s.logger.Info("fetched slack members",
		"total", len(slackMembers),
		"active", len(activeMembers))

	// Step 2: Fetch all Git users
	gitUsers, err := s.store.ListUsers()
	if err != nil {
		return nil, fmt.Errorf("listing git users: %w", err)
	}

	s.logger.Info("loaded git users", "total", len(gitUsers))

	// Step 3: Match each unlinked Git user against Slack members
	result := &SyncResult{
		SlackMembersFound: len(activeMembers),
		GitUsersTotal:     len(gitUsers),
	}

	// Build a set of already-used Slack IDs to avoid double-linking
	usedSlackIDs := make(map[string]bool)
	for _, u := range gitUsers {
		if u.SlackID != "" {
			result.AlreadyLinked++
			usedSlackIDs[u.SlackID] = true
		}
	}

	for _, gitUser := range gitUsers {
		// Skip already linked users
		if gitUser.SlackID != "" {
			continue
		}
		// Skip machine accounts — they don't have Slack identities
		if gitUser.Kind == session.UserKindMachine {
			result.Unmatched++
			continue
		}

		suggestion := s.findBestMatch(gitUser, activeMembers, usedSlackIDs)
		if suggestion == nil {
			result.Unmatched++
			continue
		}

		// Auto-link exact matches if enabled
		if s.autoLinkExact && suggestion.Confidence == ConfidenceExact {
			if err := s.store.UpdateUserSlack(gitUser.ID, suggestion.SlackID, suggestion.SlackRealName); err != nil {
				s.logger.Error("auto-linking user",
					"user_id", gitUser.ID,
					"slack_id", suggestion.SlackID,
					"error", err)
			} else {
				suggestion.Status = StatusApproved
				result.AutoLinked++
				usedSlackIDs[suggestion.SlackID] = true
				s.logger.Info("auto-linked user",
					"user", gitUser.Name,
					"slack", suggestion.SlackRealName,
					"reason", suggestion.MatchReason)
			}
		}

		result.NewSuggestions = append(result.NewSuggestions, *suggestion)
	}

	// Sort suggestions by score descending
	sort.Slice(result.NewSuggestions, func(i, j int) bool {
		return result.NewSuggestions[i].Score > result.NewSuggestions[j].Score
	})

	return result, nil
}

// LinkUser manually links a Git user to a Slack member.
// This updates the user's slack_id and slack_name in the store.
func (s *Service) LinkUser(userID session.ID, slackID, slackName string) error {
	if s == nil {
		return fmt.Errorf("identity service not configured")
	}
	return s.store.UpdateUserSlack(userID, slackID, slackName)
}

// findBestMatch finds the best Slack member match for a Git user.
// Returns nil if no match meets the minimum confidence threshold.
func (s *Service) findBestMatch(gitUser *session.User, slackMembers []SlackMember, usedSlackIDs map[string]bool) *Suggestion {
	var bestResult MatchResult
	var bestMember *SlackMember

	for i := range slackMembers {
		m := &slackMembers[i]
		// Skip already-linked Slack members
		if usedSlackIDs[m.ID] {
			continue
		}

		result := MatchNames(gitUser.Name, gitUser.Email, m.RealName, m.DisplayName, m.Email)
		if result.Score > bestResult.Score {
			bestResult = result
			bestMember = m
		}
	}

	if bestMember == nil || bestResult.Score < s.minScore {
		return nil
	}

	return &Suggestion{
		GitUserID:        gitUser.ID,
		GitUserName:      gitUser.Name,
		GitEmail:         gitUser.Email,
		SlackID:          bestMember.ID,
		SlackRealName:    bestMember.RealName,
		SlackDisplayName: bestMember.DisplayName,
		SlackEmail:       bestMember.Email,
		Score:            bestResult.Score,
		Confidence:       ScoreToConfidence(bestResult.Score),
		MatchReason:      bestResult.Reason,
		Status:           StatusPending,
		CreatedAt:        time.Now(),
	}
}

// confidenceToMinScore converts a MatchConfidence to its minimum score threshold.
func confidenceToMinScore(c MatchConfidence) float64 {
	switch c {
	case ConfidenceExact:
		return 1.0
	case ConfidenceHigh:
		return 0.8
	case ConfidenceMedium:
		return 0.6
	case ConfidenceLow:
		return 0.4
	default:
		return 0
	}
}
