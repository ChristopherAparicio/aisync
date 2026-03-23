package service

import (
	"context"

	capturesvc "github.com/ChristopherAparicio/aisync/internal/capture"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Capture ──

// CaptureRequest contains inputs for a capture operation.
type CaptureRequest struct {
	ProjectPath  string
	Branch       string
	Mode         session.StorageMode
	ProviderName session.ProviderName // empty = auto-detect
	Message      string               // optional manual summary
	Summarize    bool                 // if true, AI-summarize after export
	Model        string               // optional model override for summarization
}

// CaptureResult contains the output of a capture operation.
type CaptureResult struct {
	Session           *session.Session
	Provider          session.ProviderName
	SecretsFound      int
	Summarized        bool                       // true if AI summarization was applied
	Skipped           bool                       // true if session was unchanged and export was skipped
	StructuredSummary *session.StructuredSummary // non-nil if summarized
}

// Capture detects the active AI session, exports it, and stores it.
// If Summarize is true and an LLM client is available, it generates an AI summary.
// Summarization is non-blocking: if it fails, capture proceeds with the native summary.
func (s *SessionService) Capture(req CaptureRequest) (*CaptureResult, error) {
	var svc *capturesvc.Service
	if s.scanner != nil {
		svc = capturesvc.NewServiceWithScanner(s.registry, s.store, s.scanner)
	} else {
		svc = capturesvc.NewService(s.registry, s.store)
	}

	// Resolve owner identity before capture so it's included in the single Save()
	ownerID := s.resolveOwner()

	result, err := svc.Capture(capturesvc.Request{
		ProjectPath:  req.ProjectPath,
		Branch:       req.Branch,
		Mode:         req.Mode,
		ProviderName: req.ProviderName,
		Message:      req.Message,
		OwnerID:      ownerID,
	})
	if err != nil {
		return nil, err
	}

	// Enrich with git remote URL if not already set.
	if result.Session.RemoteURL == "" {
		result.Session.RemoteURL = s.resolveRemoteURL()
	}

	captureResult := &CaptureResult{
		Session:      result.Session,
		Provider:     result.Provider,
		SecretsFound: result.SecretsFound,
		Skipped:      result.Skipped,
	}

	// Save child sessions (sub-agents) as separate rows in the store.
	// This makes them searchable, listable, and visible in the dashboard.
	if len(result.Session.Children) > 0 {
		for i := range result.Session.Children {
			child := &result.Session.Children[i]
			child.ParentID = result.Session.ID
			// Inherit project metadata from parent if not set.
			if child.ProjectPath == "" {
				child.ProjectPath = result.Session.ProjectPath
			}
			if child.RemoteURL == "" {
				child.RemoteURL = result.Session.RemoteURL
			}
			if child.Branch == "" {
				child.Branch = result.Session.Branch
			}
			_ = s.store.Save(child)
		}
	}

	// If skipped (unchanged), return immediately — no summarization or hooks needed.
	if result.Skipped {
		return captureResult, nil
	}

	// AI summarization: only if requested, no --message override, and LLM is available.
	// Priority: --message > AI summary > provider-native summary.
	if req.Summarize && req.Message == "" && s.llm != nil {
		ctx := context.Background()
		sumResult, sumErr := s.Summarize(ctx, SummarizeRequest{
			Session: result.Session,
			Model:   req.Model,
		})
		if sumErr == nil {
			// Apply the AI-generated summary
			result.Session.Summary = sumResult.OneLine
			captureResult.Summarized = true
			captureResult.StructuredSummary = &sumResult.Summary

			// Re-save with updated summary (session already in store from capture).
			// Log error but don't fail capture — summary loss is acceptable.
			if saveErr := s.store.Save(result.Session); saveErr != nil {
				captureResult.Summarized = false // summary was not persisted
			}
		}
		// On failure: silently keep the provider-native summary (non-blocking).
	}

	// Post-capture hook (e.g., auto-analysis). Non-blocking: errors are swallowed.
	if s.postCapture != nil {
		s.postCapture(result.Session)
	}

	return captureResult, nil
}

// CaptureAll detects all sessions for the given project/provider and captures each one.
// Requires --provider to be set (CLI enforces this).
func (s *SessionService) CaptureAll(req CaptureRequest) ([]*CaptureResult, error) {
	var svc *capturesvc.Service
	if s.scanner != nil {
		svc = capturesvc.NewServiceWithScanner(s.registry, s.store, s.scanner)
	} else {
		svc = capturesvc.NewService(s.registry, s.store)
	}

	ownerID := s.resolveOwner()

	results, err := svc.CaptureAll(capturesvc.Request{
		ProjectPath:  req.ProjectPath,
		Branch:       req.Branch,
		Mode:         req.Mode,
		ProviderName: req.ProviderName,
		Message:      req.Message,
		OwnerID:      ownerID,
	})
	if err != nil {
		return nil, err
	}

	remoteURL := s.resolveRemoteURL()
	var captureResults []*CaptureResult
	for _, r := range results {
		if r.Session.RemoteURL == "" {
			r.Session.RemoteURL = remoteURL
		}

		// Save child sessions (sub-agents) as separate rows.
		if len(r.Session.Children) > 0 {
			for i := range r.Session.Children {
				child := &r.Session.Children[i]
				child.ParentID = r.Session.ID
				if child.ProjectPath == "" {
					child.ProjectPath = r.Session.ProjectPath
				}
				if child.RemoteURL == "" {
					child.RemoteURL = r.Session.RemoteURL
				}
				if child.Branch == "" {
					child.Branch = r.Session.Branch
				}
				_ = s.store.Save(child)
			}
		}

		captureResults = append(captureResults, &CaptureResult{
			Session:      r.Session,
			Provider:     r.Provider,
			SecretsFound: r.SecretsFound,
			Skipped:      r.Skipped,
		})
	}
	return captureResults, nil
}

// CaptureByID captures a specific session by its provider-native ID.
func (s *SessionService) CaptureByID(req CaptureRequest, sessionID session.ID) (*CaptureResult, error) {
	var svc *capturesvc.Service
	if s.scanner != nil {
		svc = capturesvc.NewServiceWithScanner(s.registry, s.store, s.scanner)
	} else {
		svc = capturesvc.NewService(s.registry, s.store)
	}

	ownerID := s.resolveOwner()

	result, err := svc.CaptureByID(capturesvc.Request{
		ProjectPath:  req.ProjectPath,
		Branch:       req.Branch,
		Mode:         req.Mode,
		ProviderName: req.ProviderName,
		Message:      req.Message,
		OwnerID:      ownerID,
	}, sessionID)
	if err != nil {
		return nil, err
	}

	if result.Session.RemoteURL == "" {
		result.Session.RemoteURL = s.resolveRemoteURL()
	}

	return &CaptureResult{
		Session:      result.Session,
		Provider:     result.Provider,
		SecretsFound: result.SecretsFound,
		Skipped:      result.Skipped,
	}, nil
}
