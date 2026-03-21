package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// AnalyzeDailyTask analyzes all sessions from the last N hours
// that haven't been analyzed yet.
type AnalyzeDailyTask struct {
	analysisSvc    service.AnalysisServicer
	store          storage.SessionReader
	logger         *log.Logger
	lookbackHours  int     // how far back to look (default: 24)
	errorThreshold float64 // min error rate to trigger analysis (0 = analyze all)
	minToolCalls   int     // min tool calls to consider (0 = analyze all)
}

// AnalyzeDailyConfig configures the daily analysis task.
type AnalyzeDailyConfig struct {
	AnalysisService service.AnalysisServicer
	Store           storage.SessionReader
	Logger          *log.Logger
	LookbackHours   int     // default: 24
	ErrorThreshold  float64 // default: 0 (analyze all sessions)
	MinToolCalls    int     // default: 0 (analyze all sessions)
}

// NewAnalyzeDailyTask creates a new daily analysis task.
func NewAnalyzeDailyTask(cfg AnalyzeDailyConfig) *AnalyzeDailyTask {
	lookback := cfg.LookbackHours
	if lookback <= 0 {
		lookback = 24
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &AnalyzeDailyTask{
		analysisSvc:    cfg.AnalysisService,
		store:          cfg.Store,
		logger:         logger,
		lookbackHours:  lookback,
		errorThreshold: cfg.ErrorThreshold,
		minToolCalls:   cfg.MinToolCalls,
	}
}

// Name returns the task identifier.
func (t *AnalyzeDailyTask) Name() string {
	return "analyze_daily"
}

// Run lists recent sessions and analyzes those without existing analysis.
func (t *AnalyzeDailyTask) Run(ctx context.Context) error {
	since := time.Now().Add(-time.Duration(t.lookbackHours) * time.Hour)

	// List all sessions (we filter by time manually since ListOptions doesn't have Since).
	summaries, err := t.store.List(session.ListOptions{All: true})
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	// Filter to sessions in the lookback window.
	var candidates []session.Summary
	for _, s := range summaries {
		if s.CreatedAt.After(since) {
			candidates = append(candidates, s)
		}
	}

	if len(candidates) == 0 {
		t.logger.Printf("[analyze_daily] no sessions found in the last %dh", t.lookbackHours)
		return nil
	}

	var analyzed, skipped, failed int

	for _, s := range candidates {
		// Check context cancellation.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Skip if already analyzed.
		existing, _ := t.analysisSvc.GetLatestAnalysis(string(s.ID))
		if existing != nil {
			skipped++
			continue
		}

		// Optionally apply thresholds (0 = analyze all).
		if t.minToolCalls > 0 && s.ToolCallCount < t.minToolCalls {
			skipped++
			continue
		}
		if t.errorThreshold > 0 && s.ToolCallCount > 0 {
			errorRate := float64(s.ErrorCount) / float64(s.ToolCallCount) * 100
			if errorRate < t.errorThreshold {
				skipped++
				continue
			}
		}

		// Run analysis.
		_, analyzeErr := t.analysisSvc.Analyze(ctx, service.AnalysisRequest{
			SessionID:      s.ID,
			Trigger:        analysis.TriggerAuto,
			ErrorThreshold: t.errorThreshold,
			MinToolCalls:   t.minToolCalls,
		})
		if analyzeErr != nil {
			t.logger.Printf("[analyze_daily] session %s analysis failed: %v", s.ID, analyzeErr)
			failed++
			continue
		}
		analyzed++
	}

	t.logger.Printf("[analyze_daily] done: %d analyzed, %d skipped, %d failed (of %d candidates)",
		analyzed, skipped, failed, len(candidates))
	return nil
}
