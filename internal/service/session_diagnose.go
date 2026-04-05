// Package service — session_diagnose.go provides the Diagnose use case.
//
// Diagnose produces a unified DiagnosisReport for a single session.
// The "quick scan" (default) is pure domain logic — no LLM, no I/O.
// The "deep analysis" (--deep) adds LLM-powered root cause analysis
// and suggestions by reusing AnalyzeEfficiency.
package service

import (
	"context"
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// DiagnoseRequest contains inputs for the diagnose use case.
type DiagnoseRequest struct {
	SessionID session.ID // the session to diagnose
	Deep      bool       // if true, include LLM-powered deep analysis
	Model     string     // optional — override default model for deep analysis
}

// Diagnose produces a unified DiagnosisReport for a session.
//
// Quick scan (always):
//   - HealthScore, OverloadAnalysis, ErrorTimeline, ErrorSummary
//   - ToolReport, PhaseAnalysis, Verdict, RestoreAdvice
//
// Deep analysis (when req.Deep == true):
//   - LLM-powered efficiency analysis (root cause, suggestions, patterns)
func (s *SessionService) Diagnose(ctx context.Context, req DiagnoseRequest) (*session.DiagnosisReport, error) {
	sess, err := s.store.Get(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}

	if len(sess.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages to diagnose")
	}

	// ── Quick Scan (pure domain, no I/O) ──

	// Build a lightweight Summary for health score computation.
	summary := sessionToSummary(sess)
	healthScore := session.ComputeHealthScore(summary)

	// Overload detection from messages.
	overload := session.DetectOverload(sess.Messages)

	// Error timeline + summary.
	errorTimeline := session.BuildErrorTimeline(sess)
	errorSummary := session.NewSessionErrorSummary(sess.ID, sess.Errors)

	// Tool report.
	toolReport := session.BuildToolReport(sess)

	// Phase analysis.
	phases := session.AnalyzePhases(sess)

	// Verdict.
	verdict := session.ComputeVerdict(healthScore, overload, phases)

	report := &session.DiagnosisReport{
		HealthScore:   healthScore,
		Overload:      overload,
		ErrorTimeline: errorTimeline,
		ErrorSummary:  errorSummary,
		ToolReport:    toolReport,
		Phases:        phases,
		Verdict:       verdict,
	}

	// Restore advice.
	report.RestoreAdvice = session.ComputeRestoreAdvice(report)

	// ── Deep Analysis (optional, LLM-powered) ──

	if req.Deep {
		if s.llm == nil {
			return report, nil // no LLM → return quick scan only
		}

		effResult, effErr := s.AnalyzeEfficiency(ctx, EfficiencyRequest{
			SessionID: req.SessionID,
			Model:     req.Model,
		})
		if effErr == nil && effResult != nil {
			report.Efficiency = &effResult.Report
			report.RootCause = effResult.Report.Summary
			report.Suggestions = effResult.Report.Suggestions
		}
		// Deep analysis errors are non-fatal — quick scan is always returned.
	}

	return report, nil
}

// sessionToSummary builds a lightweight Summary from a full Session
// for use with ComputeHealthScore.
func sessionToSummary(sess *session.Session) session.Summary {
	toolCalls, errorCalls := 0, 0
	for i := range sess.Messages {
		for j := range sess.Messages[i].ToolCalls {
			toolCalls++
			if sess.Messages[i].ToolCalls[j].State == session.ToolStateError {
				errorCalls++
			}
		}
	}

	return session.Summary{
		ID:            sess.ID,
		Provider:      sess.Provider,
		Agent:         sess.Agent,
		Branch:        sess.Branch,
		ProjectPath:   sess.ProjectPath,
		Summary:       sess.Summary,
		MessageCount:  len(sess.Messages),
		TotalTokens:   sess.TokenUsage.TotalTokens,
		ToolCallCount: toolCalls,
		ErrorCount:    errorCalls,
		Status:        session.DetectSessionStatus(0, sess.CreatedAt),
	}
}
