// Package replay implements session replay — re-running captured AI coding sessions
// against the same (or different) agent in an isolated git worktree to validate
// improvements (skill changes, agent config) or compare providers.
//
// Architecture:
//   - Engine: orchestrates the full replay flow (worktree → run → capture → compare)
//   - Runner: port interface for executing prompts via an AI agent (OpenCode, Claude Code)
//   - Worktree: creates/removes temporary git worktrees for isolation
//   - Compare: computes diff between original and replay sessions
package replay

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Request specifies what to replay and how.
type Request struct {
	// SourceSessionID is the session to replay.
	SourceSessionID session.ID

	// Provider overrides the provider to use (empty = same as original).
	Provider session.ProviderName

	// Agent overrides the agent name (empty = same as original).
	Agent string

	// Model overrides the model (empty = same as original/default).
	Model string

	// CommitSHA overrides the git commit to checkout (empty = use original session's commit).
	CommitSHA string

	// MaxMessages limits the number of user messages to replay (0 = all).
	MaxMessages int
}

// Result contains the outcome of a replay.
type Result struct {
	// OriginalSession is the source session that was replayed.
	OriginalSession *session.Session `json:"original_session"`

	// ReplaySession is the new session created by the replay (nil if replay failed before capture).
	ReplaySession *session.Session `json:"replay_session,omitempty"`

	// WorktreePath is where the replay ran (cleaned up after).
	WorktreePath string `json:"worktree_path"`

	// Duration is how long the replay took.
	Duration time.Duration `json:"duration"`

	// Comparison is the diff between original and replay.
	Comparison *Comparison `json:"comparison,omitempty"`

	// Error is set if the replay failed.
	Error string `json:"error,omitempty"`
}

// Comparison compares metrics between original and replay sessions.
type Comparison struct {
	// Token usage
	OriginalTokens int `json:"original_tokens"`
	ReplayTokens   int `json:"replay_tokens"`
	TokenDelta     int `json:"token_delta"` // replay - original (negative = improvement)

	// Error counts
	OriginalErrors int `json:"original_errors"`
	ReplayErrors   int `json:"replay_errors"`
	ErrorDelta     int `json:"error_delta"`

	// Message counts
	OriginalMessages int `json:"original_messages"`
	ReplayMessages   int `json:"replay_messages"`

	// Skills
	OriginalSkills  []string `json:"original_skills,omitempty"`   // skills loaded in original
	ReplaySkills    []string `json:"replay_skills,omitempty"`     // skills loaded in replay
	NewSkillsLoaded []string `json:"new_skills_loaded,omitempty"` // in replay but not original
	SkillsLost      []string `json:"skills_lost,omitempty"`       // in original but not replay

	// Tool usage
	OriginalToolCalls int `json:"original_tool_calls"`
	ReplayToolCalls   int `json:"replay_tool_calls"`

	// Verdict
	Verdict string `json:"verdict"` // "improved", "same", "degraded"
}
