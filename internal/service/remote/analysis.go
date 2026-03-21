package remote

import (
	"context"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/client"
	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/service"
)

// AnalysisService implements service.AnalysisServicer by delegating
// all operations to a running aisync server via HTTP.
type AnalysisService struct {
	c *client.Client
}

// Compile-time check.
var _ service.AnalysisServicer = (*AnalysisService)(nil)

// NewAnalysisService creates a remote AnalysisService targeting the given aisync server.
func NewAnalysisService(c *client.Client) *AnalysisService {
	return &AnalysisService{c: c}
}

// Analyze triggers a session analysis on the remote server.
func (r *AnalysisService) Analyze(_ context.Context, req service.AnalysisRequest) (*service.AnalysisResult, error) {
	trigger := "manual"
	if req.Trigger == analysis.TriggerAuto {
		trigger = "auto"
	}

	sa, err := r.c.AnalyzeSession(string(req.SessionID), client.AnalyzeRequest{
		Trigger: trigger,
	})
	if err != nil {
		return nil, fmt.Errorf("remote analyze: %w", err)
	}

	domainSA, convErr := toAnalysisDomain(sa)
	if convErr != nil {
		return nil, convErr
	}

	return &service.AnalysisResult{Analysis: domainSA}, nil
}

// GetLatestAnalysis retrieves the most recent analysis for a session from the remote server.
func (r *AnalysisService) GetLatestAnalysis(sessionID string) (*analysis.SessionAnalysis, error) {
	sa, err := r.c.GetAnalysis(sessionID)
	if err != nil {
		return nil, fmt.Errorf("remote get analysis: %w", err)
	}
	return toAnalysisDomain(sa)
}

// ListAnalyses returns all analyses for a session from the remote server.
func (r *AnalysisService) ListAnalyses(sessionID string) ([]*analysis.SessionAnalysis, error) {
	clientList, err := r.c.ListAnalyses(sessionID)
	if err != nil {
		return nil, fmt.Errorf("remote list analyses: %w", err)
	}
	result := make([]*analysis.SessionAnalysis, len(clientList))
	for i := range clientList {
		sa, convErr := toAnalysisDomain(&clientList[i])
		if convErr != nil {
			return nil, convErr
		}
		result[i] = sa
	}
	return result, nil
}

// toAnalysisDomain converts a client.SessionAnalysis to the domain analysis.SessionAnalysis.
func toAnalysisDomain(sa *client.SessionAnalysis) (*analysis.SessionAnalysis, error) {
	createdAt, _ := time.Parse(time.RFC3339, sa.CreatedAt)

	// Convert problems.
	problems := make([]analysis.Problem, len(sa.Report.Problems))
	for i, p := range sa.Report.Problems {
		problems[i] = analysis.Problem{
			Severity:     analysis.Severity(p.Severity),
			Description:  p.Description,
			MessageStart: p.MessageStart,
			MessageEnd:   p.MessageEnd,
			ToolName:     p.ToolName,
		}
	}

	// Convert recommendations.
	recs := make([]analysis.Recommendation, len(sa.Report.Recommendations))
	for i, r := range sa.Report.Recommendations {
		recs[i] = analysis.Recommendation{
			Category:    analysis.RecommendationCategory(r.Category),
			Title:       r.Title,
			Description: r.Description,
			Priority:    r.Priority,
		}
	}

	// Convert skill suggestions.
	skills := make([]analysis.SkillSuggestion, len(sa.Report.SkillSuggestions))
	for i, s := range sa.Report.SkillSuggestions {
		skills[i] = analysis.SkillSuggestion{
			Name:        s.Name,
			Description: s.Description,
			Trigger:     s.Trigger,
			Content:     s.Content,
		}
	}

	return &analysis.SessionAnalysis{
		ID:        sa.ID,
		SessionID: sa.SessionID,
		CreatedAt: createdAt,
		Trigger:   analysis.Trigger(sa.Trigger),
		Report: analysis.AnalysisReport{
			Score:            sa.Report.Score,
			Summary:          sa.Report.Summary,
			Problems:         problems,
			Recommendations:  recs,
			SkillSuggestions: skills,
		},
		Adapter:    analysis.AdapterName(sa.Adapter),
		Model:      sa.Model,
		TokensUsed: sa.TokensUsed,
		DurationMs: sa.DurationMs,
		Error:      sa.Error,
	}, nil
}
