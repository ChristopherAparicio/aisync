// Package remote implements a SessionServicer adapter that delegates
// all operations to a running aisync server via HTTP.
//
// This is the "remote adapter" in hexagonal architecture terms.
// The CLI Factory chooses between this and the local *service.SessionService
// based on the server.url configuration.
package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/client"
	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// SessionService implements service.SessionServicer by delegating to the HTTP API.
type SessionService struct {
	c *client.Client
}

// Compile-time check.
var _ service.SessionServicer = (*SessionService)(nil)

// New creates a remote SessionService targeting the given aisync server.
func New(c *client.Client) *SessionService {
	return &SessionService{c: c}
}

// ── Capture / Restore ──

func (r *SessionService) Capture(req service.CaptureRequest) (*service.CaptureResult, error) {
	res, err := r.c.Capture(client.CaptureRequest{
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		Mode:        string(req.Mode),
		Provider:    string(req.ProviderName),
		Message:     req.Message,
	})
	if err != nil {
		return nil, err
	}
	var sess session.Session
	if res.Session != nil {
		sess = clientSessionToDomain(*res.Session)
	}
	return &service.CaptureResult{
		Session:      &sess,
		Provider:     session.ProviderName(res.Provider),
		SecretsFound: res.SecretsFound,
	}, nil
}

func (r *SessionService) CaptureAll(req service.CaptureRequest) ([]*service.CaptureResult, error) {
	res, err := r.Capture(req)
	if err != nil {
		return nil, err
	}
	return []*service.CaptureResult{res}, nil
}

func (r *SessionService) CaptureByID(req service.CaptureRequest, _ session.ID) (*service.CaptureResult, error) {
	return r.Capture(req)
}

func (r *SessionService) Restore(req service.RestoreRequest) (*service.RestoreResult, error) {
	res, err := r.c.Restore(client.RestoreRequest{
		SessionID:   string(req.SessionID),
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		Agent:       req.Agent,
		Provider:    string(req.ProviderName),
		AsContext:   req.AsContext,
		PRNumber:    req.PRNumber,
	})
	if err != nil {
		return nil, err
	}
	var sess *session.Session
	if res.Session != nil {
		s := clientSessionToDomain(*res.Session)
		sess = &s
	}
	return &service.RestoreResult{
		Session:     sess,
		Method:      res.Method,
		ContextPath: res.ContextPath,
	}, nil
}

// ── CRUD ──

func (r *SessionService) Get(idOrSHA string) (*session.Session, error) {
	raw, err := r.c.Get(idOrSHA)
	if err != nil {
		return nil, err
	}
	sess := clientSessionToDomain(*raw)
	return &sess, nil
}

func (r *SessionService) List(req service.ListRequest) ([]session.Summary, error) {
	summaries, err := r.c.List(client.ListOptions{
		Branch:      req.Branch,
		Provider:    string(req.Provider),
		ProjectPath: req.ProjectPath,
		OwnerID:     req.OwnerID,
		All:         req.All,
	})
	if err != nil {
		return nil, err
	}
	result := make([]session.Summary, len(summaries))
	for i, s := range summaries {
		result[i] = clientSummaryToDomain(s)
	}
	return result, nil
}

func (r *SessionService) ListTree(_ context.Context, req service.ListRequest) ([]session.SessionTreeNode, error) {
	summaries, err := r.List(req)
	if err != nil {
		return nil, err
	}
	nodes := make([]session.SessionTreeNode, len(summaries))
	for i, s := range summaries {
		nodes[i] = session.SessionTreeNode{Summary: s}
	}
	return nodes, nil
}

func (r *SessionService) Delete(id session.ID) error {
	return r.c.Delete(string(id))
}

func (r *SessionService) TagSession(_ context.Context, id session.ID, sessionType string) error {
	return r.c.PatchSession(string(id), sessionType)
}

// ── Export / Import ──

func (r *SessionService) Export(req service.ExportRequest) (*service.ExportResult, error) {
	res, err := r.c.Export(client.ExportRequest{
		SessionID: string(req.SessionID),
		Branch:    req.Branch,
		Format:    req.Format,
	})
	if err != nil {
		return nil, err
	}
	return &service.ExportResult{
		Data:      res.Data,
		Format:    res.Format,
		SessionID: session.ID(res.SessionID),
	}, nil
}

func (r *SessionService) Import(req service.ImportRequest) (*service.ImportResult, error) {
	res, err := r.c.Import(client.ImportRequest{
		Data:         req.Data,
		SourceFormat: req.SourceFormat,
		IntoTarget:   req.IntoTarget,
	})
	if err != nil {
		return nil, err
	}
	return &service.ImportResult{
		SessionID:    session.ID(res.SessionID),
		SourceFormat: res.SourceFormat,
		Target:       res.Target,
	}, nil
}

// ── Git Integration ──

func (r *SessionService) Link(req service.LinkRequest) (*service.LinkResult, error) {
	res, err := r.c.Link(client.LinkRequest{
		SessionID:   string(req.SessionID),
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		PRNumber:    req.PRNumber,
		CommitSHA:   req.CommitSHA,
		AutoDetect:  req.AutoDetect,
	})
	if err != nil {
		return nil, err
	}
	return &service.LinkResult{
		SessionID: session.ID(res.SessionID),
		PRNumber:  res.PRNumber,
		CommitSHA: res.CommitSHA,
	}, nil
}

func (r *SessionService) Comment(req service.CommentRequest) (*service.CommentResult, error) {
	res, err := r.c.Comment(client.CommentRequest{
		SessionID:   string(req.SessionID),
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		PRNumber:    req.PRNumber,
	})
	if err != nil {
		return nil, err
	}
	return &service.CommentResult{
		PRNumber: res.PRNumber,
		Updated:  res.Updated,
	}, nil
}

// ── Analytics ──

func (r *SessionService) Stats(req service.StatsRequest) (*service.StatsResult, error) {
	raw, err := r.c.Stats(client.StatsOptions{
		Branch:       req.Branch,
		Provider:     string(req.Provider),
		ProjectPath:  req.ProjectPath,
		OwnerID:      req.OwnerID,
		All:          req.All,
		IncludeTools: req.IncludeTools,
	})
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(raw)
	var result service.StatsResult
	if jsonErr := json.Unmarshal(data, &result); jsonErr != nil {
		return nil, fmt.Errorf("decoding stats: %w", jsonErr)
	}
	return &result, nil
}

func (r *SessionService) Search(req service.SearchRequest) (*session.SearchResult, error) {
	raw, err := r.c.Search(client.SearchOptions{
		Keyword:     req.Keyword,
		Branch:      req.Branch,
		Provider:    string(req.Provider),
		ProjectPath: req.ProjectPath,
		Since:       req.Since,
		Until:       req.Until,
		Limit:       req.Limit,
		Offset:      req.Offset,
		Voice:       req.Voice,
	})
	if err != nil {
		return nil, err
	}
	summaries := make([]session.Summary, len(raw.Sessions))
	for i, s := range raw.Sessions {
		summaries[i] = clientSummaryToDomain(s)
	}

	result := &session.SearchResult{
		Sessions:   summaries,
		TotalCount: raw.TotalCount,
		Limit:      raw.Limit,
		Offset:     raw.Offset,
	}

	// Propagate voice results from server response.
	if len(raw.VoiceResults) > 0 {
		voice := make([]session.VoiceSummary, len(raw.VoiceResults))
		for i, v := range raw.VoiceResults {
			voice[i] = session.VoiceSummary{
				ID:      session.ID(v.ID),
				Summary: v.Summary,
				TimeAgo: v.TimeAgo,
				Agent:   v.Agent,
				Branch:  v.Branch,
			}
		}
		result.VoiceResults = voice
	}

	return result, nil
}

func (r *SessionService) Blame(_ context.Context, req service.BlameRequest) (*service.BlameResult, error) {
	raw, err := r.c.Blame(client.BlameOptions{
		File:     req.FilePath,
		Branch:   req.Branch,
		Provider: string(req.Provider),
		All:      req.All,
	})
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(raw)
	var result service.BlameResult
	if jsonErr := json.Unmarshal(data, &result); jsonErr != nil {
		return nil, fmt.Errorf("decoding blame: %w", jsonErr)
	}
	return &result, nil
}

func (r *SessionService) EstimateCost(_ context.Context, idOrSHA string) (*session.CostEstimate, error) {
	raw, err := r.c.EstimateCost(idOrSHA)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(raw)
	var result session.CostEstimate
	if jsonErr := json.Unmarshal(data, &result); jsonErr != nil {
		return nil, fmt.Errorf("decoding cost: %w", jsonErr)
	}
	return &result, nil
}

func (r *SessionService) ToolUsage(_ context.Context, idOrSHA string) (*session.ToolUsageStats, error) {
	raw, err := r.c.ToolUsage(idOrSHA)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(raw)
	var result session.ToolUsageStats
	if jsonErr := json.Unmarshal(data, &result); jsonErr != nil {
		return nil, fmt.Errorf("decoding tool usage: %w", jsonErr)
	}
	return &result, nil
}

func (r *SessionService) Forecast(_ context.Context, req service.ForecastRequest) (*session.ForecastResult, error) {
	raw, err := r.c.Forecast(client.ForecastRequest{
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		Period:      req.Period,
		Days:        req.Days,
	})
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(raw)
	var result session.ForecastResult
	if jsonErr := json.Unmarshal(data, &result); jsonErr != nil {
		return nil, fmt.Errorf("decoding forecast: %w", jsonErr)
	}
	return &result, nil
}

func (r *SessionService) Trends(_ context.Context, _ service.TrendRequest) (*service.TrendResult, error) {
	return nil, fmt.Errorf("trends not supported in remote mode")
}

func (r *SessionService) ListProjects(_ context.Context) ([]session.ProjectGroup, error) {
	clientGroups, err := r.c.ListProjects()
	if err != nil {
		return nil, err
	}
	groups := make([]session.ProjectGroup, len(clientGroups))
	for i, cg := range clientGroups {
		groups[i] = session.ProjectGroup{
			RemoteURL:    cg.RemoteURL,
			ProjectPath:  cg.ProjectPath,
			Provider:     session.ProviderName(cg.Provider),
			SessionCount: cg.SessionCount,
			TotalTokens:  cg.TotalTokens,
			DisplayName:  cg.DisplayName,
		}
	}
	return groups, nil
}

// ── AI-Powered ──

func (r *SessionService) Summarize(_ context.Context, _ service.SummarizeRequest) (*service.SummarizeResult, error) {
	return nil, fmt.Errorf("summarize is not available in remote mode (runs at capture time on server)")
}

func (r *SessionService) Explain(_ context.Context, req service.ExplainRequest) (*service.ExplainResult, error) {
	raw, err := r.c.Explain(client.ExplainRequest{
		SessionID: string(req.SessionID),
		Short:     req.Short,
		Model:     req.Model,
	})
	if err != nil {
		return nil, err
	}
	return &service.ExplainResult{
		Explanation: raw.Explanation,
		SessionID:   session.ID(raw.SessionID),
		Model:       raw.Model,
		TokensUsed:  raw.TokensUsed,
	}, nil
}

func (r *SessionService) BranchTimeline(_ context.Context, _ service.TimelineRequest) ([]service.TimelineEntry, error) {
	return nil, fmt.Errorf("BranchTimeline not supported in remote mode yet")
}

func (r *SessionService) ComputeTokenBuckets(_ context.Context, _ service.ComputeTokenBucketsRequest) (*service.ComputeTokenBucketsResult, error) {
	return nil, fmt.Errorf("ComputeTokenBuckets not supported in remote mode")
}

func (r *SessionService) QueryTokenUsage(_ context.Context, _ service.QueryTokenUsageRequest) ([]session.TokenUsageBucket, error) {
	return nil, fmt.Errorf("QueryTokenUsage not supported in remote mode yet")
}

func (r *SessionService) ToolCostSummary(_ context.Context, _ string, _, _ time.Time) (*session.ToolCostSummary, error) {
	return nil, fmt.Errorf("ToolCostSummary not supported in remote mode yet")
}

func (r *SessionService) AgentCostSummary(_ context.Context, _ string, _, _ time.Time) ([]session.AgentCostEntry, error) {
	return nil, fmt.Errorf("AgentCostSummary not supported in remote mode yet")
}

func (r *SessionService) CacheEfficiency(_ context.Context, _ string, _ time.Time) (*session.CacheEfficiency, error) {
	return nil, fmt.Errorf("CacheEfficiency not supported in remote mode yet")
}

func (r *SessionService) MCPCostMatrix(_ context.Context, _, _ time.Time) (*session.MCPProjectMatrix, error) {
	return nil, fmt.Errorf("MCPCostMatrix not supported in remote mode yet")
}

func (r *SessionService) ContextSaturation(_ context.Context, _ string, _ time.Time) (*session.ContextSaturation, error) {
	return nil, fmt.Errorf("ContextSaturation not supported in remote mode yet")
}

func (r *SessionService) ClassifySession(_ *session.Session) int {
	return 0
}

func (r *SessionService) ClassifyProjectSessions(_, _ string) (int, int, error) {
	return 0, 0, fmt.Errorf("ClassifyProjectSessions not supported in remote mode")
}

func (r *SessionService) BudgetStatus(_ context.Context) ([]session.BudgetStatus, error) {
	return nil, fmt.Errorf("BudgetStatus not supported in remote mode")
}

func (r *SessionService) SearchCapabilities() search.Capabilities {
	return search.Capabilities{}
}

func (r *SessionService) IndexAllSessions(_ context.Context) (int, int, error) {
	return 0, 0, fmt.Errorf("IndexAllSessions not supported in remote mode")
}

func (r *SessionService) SessionSaturationCurve(_ context.Context, _ session.ID) (*session.SaturationCurve, error) {
	return nil, fmt.Errorf("SessionSaturationCurve not supported in remote mode")
}

func (r *SessionService) AgentROIAnalysis(_ context.Context, _ string, _ time.Time) (*session.AgentROI, error) {
	return nil, fmt.Errorf("AgentROIAnalysis not supported in remote mode")
}

func (r *SessionService) SkillROIAnalysis(_ context.Context, _ string, _ time.Time) (*session.SkillROI, error) {
	return nil, fmt.Errorf("SkillROIAnalysis not supported in remote mode")
}

func (r *SessionService) GenerateRecommendations(_ context.Context, _ string) ([]session.Recommendation, error) {
	return nil, fmt.Errorf("GenerateRecommendations not supported in remote mode")
}

func (r *SessionService) ExtractAndSaveFiles(_ *session.Session) (int, error) {
	return 0, fmt.Errorf("ExtractAndSaveFiles not supported in remote mode")
}

func (r *SessionService) BackfillFileBlame(_ context.Context) (int, int, error) {
	return 0, 0, fmt.Errorf("BackfillFileBlame not supported in remote mode")
}

func (r *SessionService) GetSessionFiles(_ context.Context, _ session.ID) ([]session.SessionFileRecord, error) {
	return nil, fmt.Errorf("GetSessionFiles not supported in remote mode")
}

func (r *SessionService) ComputeObjective(_ context.Context, _ service.ComputeObjectiveRequest) (*session.SessionObjective, error) {
	return nil, fmt.Errorf("ComputeObjective not supported in remote mode")
}

func (r *SessionService) GetObjective(_ context.Context, _ string) (*session.SessionObjective, error) {
	return nil, fmt.Errorf("GetObjective not supported in remote mode yet")
}

func (r *SessionService) AnalyzeEfficiency(_ context.Context, req service.EfficiencyRequest) (*service.EfficiencyResult, error) {
	raw, err := r.c.AnalyzeEfficiency(client.EfficiencyRequest{
		SessionID: string(req.SessionID),
		Model:     req.Model,
	})
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(raw)
	var result service.EfficiencyResult
	if jsonErr := json.Unmarshal(data, &result); jsonErr != nil {
		return nil, fmt.Errorf("decoding efficiency: %w", jsonErr)
	}
	return &result, nil
}

// ── Session Management ──

func (r *SessionService) Rewind(_ context.Context, req service.RewindRequest) (*service.RewindResult, error) {
	raw, err := r.c.Rewind(client.RewindRequest{
		SessionID: string(req.SessionID),
		AtMessage: req.AtMessage,
	})
	if err != nil {
		return nil, err
	}
	var newSess *session.Session
	if raw.NewSession != nil {
		s := clientSessionToDomain(*raw.NewSession)
		newSess = &s
	}
	return &service.RewindResult{
		NewSession:      newSess,
		OriginalID:      session.ID(raw.OriginalID),
		TruncatedAt:     raw.TruncatedAt,
		MessagesRemoved: raw.MessagesRemoved,
	}, nil
}

func (r *SessionService) GarbageCollect(_ context.Context, req service.GCRequest) (*service.GCResult, error) {
	raw, err := r.c.GarbageCollect(client.GCRequest{
		OlderThan:  req.OlderThan,
		KeepLatest: req.KeepLatest,
		DryRun:     req.DryRun,
	})
	if err != nil {
		return nil, err
	}
	return &service.GCResult{
		Deleted: raw.Deleted,
		Would:   raw.Would,
		DryRun:  raw.DryRun,
	}, nil
}

func (r *SessionService) Diff(_ context.Context, req service.DiffRequest) (*session.DiffResult, error) {
	raw, err := r.c.Diff(client.DiffRequest{
		LeftID:  string(req.LeftID),
		RightID: string(req.RightID),
	})
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(raw)
	var result session.DiffResult
	if jsonErr := json.Unmarshal(data, &result); jsonErr != nil {
		return nil, fmt.Errorf("decoding diff: %w", jsonErr)
	}
	return &result, nil
}

func (r *SessionService) DetectOffTopic(_ context.Context, req service.OffTopicRequest) (*session.OffTopicResult, error) {
	raw, err := r.c.DetectOffTopic(client.OffTopicRequest{
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		Threshold:   req.Threshold,
	})
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(raw)
	var result session.OffTopicResult
	if jsonErr := json.Unmarshal(data, &result); jsonErr != nil {
		return nil, fmt.Errorf("decoding off-topic: %w", jsonErr)
	}
	return &result, nil
}

// BackfillRemoteURLs is not supported in remote mode — backfill must run locally.
func (r *SessionService) BackfillRemoteURLs(_ context.Context) (*service.BackfillResult, error) {
	return nil, fmt.Errorf("backfill remote URLs is only available in local mode")
}

// DetectForksBatch is not supported in remote mode — fork detection must run locally.
func (r *SessionService) DetectForksBatch(_ context.Context) (*service.ForkDetectionResult, error) {
	return nil, fmt.Errorf("fork detection batch is only available in local mode")
}

// ── Ingest ──

func (r *SessionService) Ingest(_ context.Context, req service.IngestRequest) (*service.IngestResult, error) {
	res, err := r.c.Ingest(client.IngestRequest{
		Provider:    req.Provider,
		Agent:       req.Agent,
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		Summary:     req.Summary,
		SessionID:   req.SessionID,
		Messages:    convertIngestMessages(req.Messages),
	})
	if err != nil {
		return nil, err
	}
	return &service.IngestResult{
		SessionID: session.ID(res.SessionID),
		Provider:  session.ProviderName(res.Provider),
	}, nil
}

func convertIngestMessages(msgs []service.IngestMessage) []client.IngestMessage {
	result := make([]client.IngestMessage, len(msgs))
	for i, m := range msgs {
		tcs := make([]client.IngestToolCall, len(m.ToolCalls))
		for j, tc := range m.ToolCalls {
			tcs[j] = client.IngestToolCall{
				Name:       tc.Name,
				Input:      tc.Input,
				Output:     tc.Output,
				State:      tc.State,
				DurationMs: tc.DurationMs,
			}
		}
		result[i] = client.IngestMessage{
			Role:         m.Role,
			Content:      m.Content,
			Model:        m.Model,
			Thinking:     m.Thinking,
			ToolCalls:    tcs,
			InputTokens:  m.InputTokens,
			OutputTokens: m.OutputTokens,
		}
	}
	return result
}

// ── Type conversion helpers ──

func clientSessionToDomain(cs client.Session) session.Session {
	msgs := make([]session.Message, len(cs.Messages))
	for i, m := range cs.Messages {
		tcs := make([]session.ToolCall, len(m.ToolCalls))
		for j, tc := range m.ToolCalls {
			tcs[j] = session.ToolCall{
				ID:         tc.ID,
				Name:       tc.Name,
				Input:      tc.Input,
				Output:     tc.Output,
				State:      session.ToolState(tc.State),
				DurationMs: tc.DurationMs,
			}
		}
		msgs[i] = session.Message{
			ID:        m.ID,
			Role:      session.MessageRole(m.Role),
			Content:   m.Content,
			Model:     m.Model,
			Thinking:  m.Thinking,
			Timestamp: m.Timestamp,
			ToolCalls: tcs,
		}
	}

	links := make([]session.Link, len(cs.Links))
	for i, l := range cs.Links {
		links[i] = session.Link{
			LinkType: session.LinkType(l.LinkType),
			Ref:      l.Ref,
		}
	}

	fcs := make([]session.FileChange, len(cs.FileChanges))
	for i, fc := range cs.FileChanges {
		fcs[i] = session.FileChange{
			FilePath:   fc.FilePath,
			ChangeType: session.ChangeType(fc.ChangeType),
		}
	}

	return session.Session{
		ID:          session.ID(cs.ID),
		Provider:    session.ProviderName(cs.Provider),
		Agent:       cs.Agent,
		Branch:      cs.Branch,
		CommitSHA:   cs.CommitSHA,
		ProjectPath: cs.ProjectPath,
		ParentID:    session.ID(cs.ParentID),
		OwnerID:     session.ID(cs.OwnerID),
		StorageMode: session.StorageMode(cs.StorageMode),
		Summary:     cs.Summary,
		Messages:    msgs,
		Links:       links,
		FileChanges: fcs,
		TokenUsage: session.TokenUsage{
			InputTokens:  cs.TokenUsage.InputTokens,
			OutputTokens: cs.TokenUsage.OutputTokens,
			TotalTokens:  cs.TokenUsage.TotalTokens,
		},
		ForkedAtMessage: cs.ForkedAtMessage,
		Version:         cs.Version,
		CreatedAt:       cs.CreatedAt,
		ExportedAt:      cs.ExportedAt,
		ExportedBy:      cs.ExportedBy,
	}
}

func clientSummaryToDomain(cs client.Summary) session.Summary {
	return session.Summary{
		ID:           session.ID(cs.ID),
		OwnerID:      session.ID(cs.OwnerID),
		Provider:     session.ProviderName(cs.Provider),
		Agent:        cs.Agent,
		Branch:       cs.Branch,
		Summary:      cs.Summary,
		MessageCount: cs.MessageCount,
		TotalTokens:  cs.TotalTokens,
		CreatedAt:    cs.CreatedAt,
	}
}

// ── Session Links ──

func (r *SessionService) LinkSessions(_ context.Context, req service.SessionLinkRequest) (*session.SessionLink, error) {
	res, err := r.c.LinkSessions(client.SessionLinkRequest{
		SourceSessionID: req.SourceSessionID,
		TargetSessionID: req.TargetSessionID,
		LinkType:        req.LinkType,
		Description:     req.Description,
	})
	if err != nil {
		return nil, err
	}
	return &session.SessionLink{
		ID:              session.ID(res.ID),
		SourceSessionID: session.ID(res.SourceSessionID),
		TargetSessionID: session.ID(res.TargetSessionID),
		LinkType:        session.SessionLinkType(res.LinkType),
		Description:     res.Description,
		CreatedAt:       res.CreatedAt,
	}, nil
}

func (r *SessionService) GetLinkedSessions(_ context.Context, sessionID session.ID) ([]session.SessionLink, error) {
	res, err := r.c.GetLinkedSessions(string(sessionID))
	if err != nil {
		return nil, err
	}
	links := make([]session.SessionLink, len(res))
	for i, l := range res {
		links[i] = session.SessionLink{
			ID:              session.ID(l.ID),
			SourceSessionID: session.ID(l.SourceSessionID),
			TargetSessionID: session.ID(l.TargetSessionID),
			LinkType:        session.SessionLinkType(l.LinkType),
			Description:     l.Description,
			CreatedAt:       l.CreatedAt,
		}
	}
	return links, nil
}

func (r *SessionService) DeleteSessionLink(_ context.Context, id session.ID) error {
	return r.c.DeleteSessionLink(string(id))
}
