// Package identity implements identity matching between Git users and external
// platforms (Slack, GitHub). It provides fuzzy name matching with confidence
// scoring and manual override support.
//
// Architecture: This is a bounded context following hexagonal architecture.
// Domain types are pure value objects. The Service orchestrates matching
// via ports (SlackClient interface) and the store (UserStore).
package identity

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// MatchConfidence represents the confidence level of an identity match.
type MatchConfidence string

const (
	// ConfidenceExact means email or name matched exactly.
	ConfidenceExact MatchConfidence = "exact"
	// ConfidenceHigh means strong fuzzy match (score >= 0.8).
	ConfidenceHigh MatchConfidence = "high"
	// ConfidenceMedium means moderate fuzzy match (score >= 0.6).
	ConfidenceMedium MatchConfidence = "medium"
	// ConfidenceLow means weak fuzzy match (score >= 0.4).
	ConfidenceLow MatchConfidence = "low"
	// ConfidenceNone means no reasonable match found.
	ConfidenceNone MatchConfidence = "none"
)

// SuggestionStatus represents the state of an identity suggestion.
type SuggestionStatus string

const (
	StatusPending  SuggestionStatus = "pending"
	StatusApproved SuggestionStatus = "approved"
	StatusRejected SuggestionStatus = "rejected"
)

// SlackMember represents a member fetched from the Slack users.list API.
type SlackMember struct {
	// ID is the Slack user ID (e.g., "U0123ABCDEF").
	ID string `json:"id"`

	// RealName is the user's full real name from their Slack profile.
	RealName string `json:"real_name"`

	// DisplayName is the user's chosen display name in Slack.
	DisplayName string `json:"display_name"`

	// Email is the user's email from their Slack profile (may be empty if
	// the bot doesn't have users:read.email scope).
	Email string `json:"email"`

	// IsBot indicates if this is a Slack bot user.
	IsBot bool `json:"is_bot"`

	// Deleted indicates if the user has been deactivated.
	Deleted bool `json:"deleted"`

	// TeamID is the workspace ID this member belongs to.
	TeamID string `json:"team_id"`
}

// Suggestion represents a proposed link between a Git user and a Slack member.
type Suggestion struct {
	// GitUser is the aisync user (from the users table).
	GitUserID   session.ID `json:"git_user_id"`
	GitUserName string     `json:"git_user_name"`
	GitEmail    string     `json:"git_email"`

	// SlackMember is the proposed Slack match.
	SlackID          string `json:"slack_id"`
	SlackRealName    string `json:"slack_real_name"`
	SlackDisplayName string `json:"slack_display_name"`
	SlackEmail       string `json:"slack_email"`

	// Score is the raw match score (0.0 to 1.0).
	Score float64 `json:"score"`

	// Confidence is the categorized confidence level.
	Confidence MatchConfidence `json:"confidence"`

	// MatchReason describes why this match was suggested.
	MatchReason string `json:"match_reason"`

	// Status is the current state of this suggestion.
	Status SuggestionStatus `json:"status"`

	// CreatedAt is when this suggestion was generated.
	CreatedAt time.Time `json:"created_at"`
}

// SyncResult summarizes the outcome of a Slack sync operation.
type SyncResult struct {
	// SlackMembersFound is the total number of active (non-bot, non-deleted)
	// Slack members found.
	SlackMembersFound int `json:"slack_members_found"`

	// GitUsersTotal is the total number of Git users in the store.
	GitUsersTotal int `json:"git_users_total"`

	// AlreadyLinked is the number of Git users that already have a Slack ID.
	AlreadyLinked int `json:"already_linked"`

	// NewSuggestions is the list of new match suggestions generated.
	NewSuggestions []Suggestion `json:"new_suggestions"`

	// AutoLinked is the number of exact matches that were automatically linked
	// (when auto-link mode is enabled).
	AutoLinked int `json:"auto_linked"`

	// Unmatched is the number of Git users with no reasonable match.
	Unmatched int `json:"unmatched"`
}

// ScoreToConfidence converts a raw match score to a confidence level.
func ScoreToConfidence(score float64) MatchConfidence {
	switch {
	case score >= 1.0:
		return ConfidenceExact
	case score >= 0.8:
		return ConfidenceHigh
	case score >= 0.6:
		return ConfidenceMedium
	case score >= 0.4:
		return ConfidenceLow
	default:
		return ConfidenceNone
	}
}
