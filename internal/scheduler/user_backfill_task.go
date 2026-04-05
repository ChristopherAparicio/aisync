package scheduler

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// UserKindBackfillTask reclassifies existing users based on configured
// machine email patterns. Runs once at startup or on a slow schedule.
type UserKindBackfillTask struct {
	store  storage.Store
	cfg    *config.Config
	logger *log.Logger
}

// UserKindBackfillConfig configures the user kind backfill task.
type UserKindBackfillConfig struct {
	Store  storage.Store
	Config *config.Config
	Logger *log.Logger
}

// NewUserKindBackfillTask creates a new user kind backfill task.
func NewUserKindBackfillTask(cfg UserKindBackfillConfig) *UserKindBackfillTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &UserKindBackfillTask{
		store:  cfg.Store,
		cfg:    cfg.Config,
		logger: logger,
	}
}

// Name returns the task identifier.
func (t *UserKindBackfillTask) Name() string {
	return "user_kind_backfill"
}

// Run classifies all users with "unknown" kind using the configured machine patterns.
func (t *UserKindBackfillTask) Run(_ context.Context) error {
	var patterns []string
	if t.cfg != nil {
		patterns = t.cfg.GetMachinePatterns()
	}

	users, err := t.store.ListUsers()
	if err != nil {
		return err
	}

	var updated int
	for _, u := range users {
		newKind := service.ClassifyUserKind(u.Email, patterns)
		if newKind == u.Kind {
			continue
		}
		if err := t.store.UpdateUserKind(u.ID, string(newKind)); err != nil {
			t.logger.Printf("[user_kind_backfill] WARNING: failed to update %s: %v", u.ID, err)
			continue
		}
		updated++
	}

	t.logger.Printf("[user_kind_backfill] done: %d users checked, %d updated", len(users), updated)
	return nil
}
