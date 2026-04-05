package scheduler

import (
	"context"
	"fmt"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── Slack User Resolve Task ──

// SlackUserResolver is a port for looking up Slack users by email.
// The Slack adapter implements this interface via its Client.LookupByEmail method.
type SlackUserResolver interface {
	// LookupByEmail resolves a Slack user by email address.
	// Returns (nil, nil) if the user is not found in Slack.
	LookupByEmail(email string) (*SlackUserInfo, error)
}

// SlackUserInfo holds the resolved Slack identity for a user.
type SlackUserInfo struct {
	ID       string // Slack user ID (e.g. "U0123ABCDEF")
	Name     string // Slack display name
	RealName string // Slack full name
}

// SlackResolveTask iterates human users without a slack_id and attempts
// to resolve their Slack identity via the users.lookupByEmail API.
// Users whose email matches a Slack account get their slack_id and slack_name
// updated automatically.
type SlackResolveTask struct {
	store    storage.Store
	resolver SlackUserResolver
	logger   *log.Logger
}

// SlackResolveConfig holds the configuration for creating a SlackResolveTask.
type SlackResolveConfig struct {
	Store    storage.Store
	Resolver SlackUserResolver
	Logger   *log.Logger
}

// NewSlackResolveTask creates a Slack user resolve task.
func NewSlackResolveTask(cfg SlackResolveConfig) *SlackResolveTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &SlackResolveTask{
		store:    cfg.Store,
		resolver: cfg.Resolver,
		logger:   logger,
	}
}

func (t *SlackResolveTask) Name() string { return "slack_resolve" }

func (t *SlackResolveTask) Run(ctx context.Context) error {
	if t.store == nil || t.resolver == nil {
		return nil
	}

	// Get all users — we filter to those without slack_id.
	users, err := t.store.ListUsers()
	if err != nil {
		return fmt.Errorf("slack_resolve: list users: %w", err)
	}

	var resolved, notFound, skipped, errCount int
	for _, u := range users {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Skip users who already have a slack_id.
		if u.SlackID != "" {
			skipped++
			continue
		}

		// Skip users without an email (can't look up).
		if u.Email == "" {
			skipped++
			continue
		}

		// Skip machine accounts — they typically don't have Slack accounts.
		if u.Kind == session.UserKindMachine {
			skipped++
			continue
		}

		info, lookupErr := t.resolver.LookupByEmail(u.Email)
		if lookupErr != nil {
			t.logger.Printf("[slack_resolve] lookup error for %s (%s): %v", u.Name, u.Email, lookupErr)
			errCount++
			continue
		}

		if info == nil {
			notFound++
			continue
		}

		// Determine the display name: prefer Slack display_name, fall back to real_name.
		slackName := info.Name
		if slackName == "" {
			slackName = info.RealName
		}

		if updateErr := t.store.UpdateUserSlack(u.ID, info.ID, slackName); updateErr != nil {
			t.logger.Printf("[slack_resolve] update error for %s: %v", u.Name, updateErr)
			errCount++
			continue
		}

		resolved++
		t.logger.Printf("[slack_resolve] resolved %s → %s (%s)", u.Email, info.ID, slackName)
	}

	t.logger.Printf("[slack_resolve] done: %d resolved, %d not found, %d skipped, %d errors",
		resolved, notFound, skipped, errCount)
	return nil
}
