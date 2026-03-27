package scheduler

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/webhooks"
)

// BudgetCheckTask checks project budgets and fires webhook alerts.
type BudgetCheckTask struct {
	sessionSvc service.SessionServicer
	dispatcher *webhooks.Dispatcher
	logger     *log.Logger
}

// BudgetCheckConfig holds the configuration for creating a BudgetCheckTask.
type BudgetCheckConfig struct {
	SessionService service.SessionServicer
	Dispatcher     *webhooks.Dispatcher
	Logger         *log.Logger
}

// NewBudgetCheckTask creates a new budget check task.
func NewBudgetCheckTask(cfg BudgetCheckConfig) *BudgetCheckTask {
	return &BudgetCheckTask{
		sessionSvc: cfg.SessionService,
		dispatcher: cfg.Dispatcher,
		logger:     cfg.Logger,
	}
}

func (t *BudgetCheckTask) Name() string { return "budget_check" }

func (t *BudgetCheckTask) Run(ctx context.Context) error {
	statuses, err := t.sessionSvc.BudgetStatus(ctx)
	if err != nil {
		return err
	}

	for _, bs := range statuses {
		// Fire webhook for monthly alerts.
		if bs.MonthlyAlert != "" {
			t.logger.Printf("[budget] %s: monthly %s (%.0f%% of $%.0f)",
				bs.ProjectName, bs.MonthlyAlert, bs.MonthlyPercent, bs.MonthlyLimit)

			if t.dispatcher != nil {
				t.dispatcher.Fire(webhooks.EventBudgetAlert, map[string]any{
					"project":        bs.ProjectName,
					"project_path":   bs.ProjectPath,
					"alert_type":     "monthly",
					"alert_level":    bs.MonthlyAlert,
					"spent":          bs.MonthlySpent,
					"limit":          bs.MonthlyLimit,
					"percent":        bs.MonthlyPercent,
					"projected":      bs.ProjectedMonth,
					"days_remaining": bs.DaysRemaining,
				})
			}
		}

		// Fire webhook for daily alerts.
		if bs.DailyAlert != "" {
			t.logger.Printf("[budget] %s: daily %s (%.0f%% of $%.0f)",
				bs.ProjectName, bs.DailyAlert, bs.DailyPercent, bs.DailyLimit)

			if t.dispatcher != nil {
				t.dispatcher.Fire(webhooks.EventBudgetAlert, map[string]any{
					"project":     bs.ProjectName,
					"alert_type":  "daily",
					"alert_level": bs.DailyAlert,
					"spent":       bs.DailySpent,
					"limit":       bs.DailyLimit,
					"percent":     bs.DailyPercent,
				})
			}
		}
	}

	return nil
}
