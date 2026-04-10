package service

import (
	"context"
	"fmt"

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
	// Fallback: try resolving from the session's own project path.
	// This handles OpenCode worktrees where the server CWD differs from the session directory.
	if result.Session.RemoteURL == "" && result.Session.ProjectPath != "" {
		result.Session.RemoteURL = resolveRemoteURLForPath(result.Session.ProjectPath)
	}

	captureResult := &CaptureResult{
		Session:      result.Session,
		Provider:     result.Provider,
		SecretsFound: result.SecretsFound,
		Skipped:      result.Skipped,
	}

	// If skipped (unchanged), return immediately — no save, summarization, or hooks needed.
	if result.Skipped {
		return captureResult, nil
	}

	// AI summarization: only if requested, no --message override, and LLM is available.
	// Priority: --message > AI summary > provider-native summary.
	// Done BEFORE Save() so the summary is included in the single write.
	if req.Summarize && req.Message == "" && s.llm != nil {
		ctx := context.Background()
		sumResult, sumErr := s.Summarize(ctx, SummarizeRequest{
			Session: result.Session,
			Model:   req.Model,
		})
		if sumErr == nil {
			result.Session.Summary = sumResult.OneLine
			captureResult.Summarized = true
			captureResult.StructuredSummary = &sumResult.Summary
		}
		// On failure: silently keep the provider-native summary (non-blocking).
	}

	// Stamp denormalized costs, then persist in a single Save().
	// Previously this was 2-3 Save() calls: capture → stampCosts → summarize.
	// Now: enrich in-memory → single write → analytics sidecar.
	s.stampCosts(result.Session)
	if err := s.store.Save(result.Session); err != nil {
		return nil, fmt.Errorf("storing session: %w", err)
	}
	s.stampAnalytics(result.Session)

	// If summarization was attempted but Save failed above, we already returned the error.
	// If summarization succeeded but we get here, it was persisted in the single Save.

	// Save child sessions (sub-agents) as separate rows in the store.
	// This makes them searchable, listable, and visible in the dashboard.
	// Each child gets a single Save() with costs already stamped.
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
			s.stampCosts(child)
			_ = s.store.Save(child)
			s.stampAnalytics(child)
			// Post-capture hook for child: extract events, classify errors, etc.
			if s.postCapture != nil {
				s.postCapture(child)
			}
		}
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
		// Fallback: try resolving from the session's own project path.
		if r.Session.RemoteURL == "" && r.Session.ProjectPath != "" {
			r.Session.RemoteURL = resolveRemoteURLForPath(r.Session.ProjectPath)
		}

		// Stamp costs and persist in a single Save() (no redundant writes).
		if !r.Skipped {
			s.stampCosts(r.Session)
			_ = s.store.Save(r.Session)
			s.stampAnalytics(r.Session)
		}

		// Save child sessions (sub-agents) as separate rows.
		// Each child gets a single Save() with costs already stamped.
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
				s.stampCosts(child)
				_ = s.store.Save(child)
				s.stampAnalytics(child)
				// Post-capture hook for child: extract events, classify errors, etc.
				if s.postCapture != nil {
					s.postCapture(child)
				}
			}
		}

		captureResults = append(captureResults, &CaptureResult{
			Session:      r.Session,
			Provider:     r.Provider,
			SecretsFound: r.SecretsFound,
			Skipped:      r.Skipped,
		})

		// Post-capture hook: fire for each non-skipped session so that error
		// classification, event extraction, auto-tagging, etc. run for batch
		// captures the same way they run for single captures.
		if !r.Skipped && s.postCapture != nil {
			s.postCapture(r.Session)
		}
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
	// Fallback: try resolving from the session's own project path.
	if result.Session.RemoteURL == "" && result.Session.ProjectPath != "" {
		result.Session.RemoteURL = resolveRemoteURLForPath(result.Session.ProjectPath)
	}

	captureResult := &CaptureResult{
		Session:      result.Session,
		Provider:     result.Provider,
		SecretsFound: result.SecretsFound,
		Skipped:      result.Skipped,
	}

	// Stamp costs and persist in a single Save() (capture service no longer saves).
	if !result.Skipped {
		s.stampCosts(result.Session)
		if err := s.store.Save(result.Session); err != nil {
			return nil, fmt.Errorf("storing session: %w", err)
		}
		s.stampAnalytics(result.Session)
	}

	// Post-capture hook: fire for non-skipped sessions (same as Capture).
	if !result.Skipped && s.postCapture != nil {
		s.postCapture(result.Session)
	}

	return captureResult, nil
}
