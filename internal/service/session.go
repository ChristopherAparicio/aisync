// Package service implements the core business logic for aisync.
// It provides SessionService (capture, restore, export, import, stats, etc.)
// and SyncService (push/pull via git branch). These services absorb all
// orchestration logic that previously lived in CLI commands, making the
// logic reusable across CLI, HTTP API, and MCP server.
package service

import (
	"strings"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/platform"
	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/secrets"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── SessionService ──

// SessionService orchestrates all session-related business logic.
// PostCaptureFunc is a callback invoked after a session is successfully captured and saved.
// It receives the captured session. Any error is logged but does NOT fail the capture.
type PostCaptureFunc func(sess *session.Session)

type SessionService struct {
	store     storage.Store
	registry  *provider.Registry
	scanner   *secrets.Scanner // optional — nil means no scanning
	converter *converter.Converter
	pricing   *pricing.Calculator
	git       *git.Client       // optional — nil when git is unavailable
	platform  platform.Platform // optional — nil when platform is unavailable
	llm       llm.Client        // optional — nil disables AI features (summarize, explain)

	// postCapture is an optional callback invoked after successful capture.
	// Used by the analysis subsystem to trigger auto-analysis.
	postCapture PostCaptureFunc
}

// SessionServiceConfig holds all dependencies for creating a SessionService.
type SessionServiceConfig struct {
	Store       storage.Store
	Registry    *provider.Registry
	Scanner     *secrets.Scanner // optional
	Converter   *converter.Converter
	Pricing     *pricing.Calculator // optional — nil uses defaults
	Git         *git.Client         // optional
	Platform    platform.Platform   // optional
	LLM         llm.Client          // optional — nil disables AI features
	PostCapture PostCaptureFunc     // optional — callback after successful capture
}

// NewSessionService creates a SessionService with all dependencies.
func NewSessionService(cfg SessionServiceConfig) *SessionService {
	conv := cfg.Converter
	if conv == nil {
		conv = converter.New()
	}
	calc := cfg.Pricing
	if calc == nil {
		calc = pricing.NewCalculator()
	}
	return &SessionService{
		store:       cfg.Store,
		registry:    cfg.Registry,
		scanner:     cfg.Scanner,
		converter:   conv,
		pricing:     calc,
		git:         cfg.Git,
		platform:    cfg.Platform,
		llm:         cfg.LLM,
		postCapture: cfg.PostCapture,
	}
}

// resolveOwner detects the current user from git config and ensures they exist
// in the store. Returns the user ID, or empty if the identity cannot be determined.
func (s *SessionService) resolveOwner() session.ID {
	if s.git == nil {
		return ""
	}

	email := s.git.UserEmail()
	if email == "" {
		return ""
	}

	// Check if the user already exists
	existing, err := s.store.GetUserByEmail(email)
	if err == nil && existing != nil {
		return existing.ID
	}

	// Create a new user
	name := s.git.UserName()
	if name == "" {
		name = email // fallback to email as name
	}

	user := &session.User{
		ID:     session.NewID(),
		Name:   name,
		Email:  email,
		Source: "git",
	}

	if saveErr := s.store.SaveUser(user); saveErr != nil {
		return "" // silently skip — user identity is best-effort
	}

	return user.ID
}

// ── Helpers ──

// resolveSession resolves a session by ID or by current branch.
func (s *SessionService) resolveSession(id session.ID, projectPath, branch string) (*session.Session, error) {
	if id != "" {
		return s.store.Get(id)
	}
	return s.store.GetLatestByBranch(projectPath, branch)
}

// resolveRemoteURL returns a normalized git remote URL from the git client.
// Returns empty string if git is unavailable or no remote is configured.
func (s *SessionService) resolveRemoteURL() string {
	if s.git == nil {
		return ""
	}
	raw := s.git.RemoteURL("origin")
	return NormalizeRemoteURL(raw)
}

// NormalizeRemoteURL converts any git remote URL format to a canonical form:
// "github.com/org/repo" (no protocol, no .git suffix, no user@).
//
// Examples:
//
//	"https://github.com/org/repo.git"    → "github.com/org/repo"
//	"git@github.com:org/repo.git"        → "github.com/org/repo"
//	"ssh://git@github.com/org/repo.git"  → "github.com/org/repo"
//	""                                   → ""
func NormalizeRemoteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Handle SSH format: git@host:org/repo.git
	if strings.Contains(raw, "@") && strings.Contains(raw, ":") && !strings.Contains(raw, "://") {
		// git@github.com:org/repo.git → github.com/org/repo.git
		parts := strings.SplitN(raw, "@", 2)
		hostPath := parts[1]
		raw = strings.Replace(hostPath, ":", "/", 1)
	} else {
		// Strip protocol: https://, ssh://, git://
		for _, prefix := range []string{"https://", "http://", "ssh://", "git://"} {
			if strings.HasPrefix(raw, prefix) {
				raw = strings.TrimPrefix(raw, prefix)
				break
			}
		}
		// Strip user@ prefix (e.g. git@)
		if idx := strings.Index(raw, "@"); idx != -1 && idx < strings.Index(raw, "/") {
			raw = raw[idx+1:]
		}
	}

	// Strip trailing slash first, then .git suffix
	raw = strings.TrimRight(raw, "/")
	raw = strings.TrimSuffix(raw, ".git")
	raw = strings.TrimRight(raw, "/")

	return raw
}

// looksLikeCommitSHA returns true if s looks like a hex commit SHA (7-40 chars).
func looksLikeCommitSHA(str string) bool {
	if len(str) < 7 || len(str) > 40 {
		return false
	}
	for _, c := range str {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
