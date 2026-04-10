// Package capture implements the session capture workflow.
// It orchestrates provider detection, session export, and storage.
package capture

import (
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/secrets"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// Request contains all inputs for a capture operation.
type Request struct {
	ProjectPath  string
	Branch       string
	Mode         session.StorageMode
	ProviderName session.ProviderName // empty = auto-detect
	Message      string               // optional manual summary override
	OwnerID      session.ID           // optional owner identity
}

// Result contains the output of a capture operation.
type Result struct {
	Session      *session.Session
	Provider     session.ProviderName
	SecretsFound int  // number of secrets detected (0 if no scanner)
	Skipped      bool // true if session was unchanged and export was skipped
}

// Service orchestrates the capture workflow.
type Service struct {
	registry *provider.Registry
	store    storage.Store
	scanner  *secrets.Scanner // optional — nil means no scanning
}

// NewService creates a capture service.
func NewService(registry *provider.Registry, store storage.Store) *Service {
	return &Service{
		registry: registry,
		store:    store,
	}
}

// NewServiceWithScanner creates a capture service with secret scanning.
func NewServiceWithScanner(registry *provider.Registry, store storage.Store, scanner *secrets.Scanner) *Service {
	return &Service{
		registry: registry,
		store:    store,
		scanner:  scanner,
	}
}

// Capture detects the most recent AI session, exports it, and stores it.
func (s *Service) Capture(req Request) (*Result, error) {
	var (
		summary *session.Summary
		prov    provider.Provider
		err     error
	)

	if req.ProviderName != "" {
		prov, err = s.registry.Get(req.ProviderName)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", req.ProviderName, err)
		}
		summaries, detectErr := prov.Detect(req.ProjectPath, req.Branch)
		if detectErr != nil {
			return nil, fmt.Errorf("detecting sessions: %w", detectErr)
		}
		if len(summaries) == 0 {
			return nil, fmt.Errorf("no sessions found for provider %q on branch %q: %w",
				req.ProviderName, req.Branch, session.ErrSessionNotFound)
		}
		summary = &summaries[0]
	} else {
		summary, prov, err = s.registry.DetectBest(req.ProjectPath, req.Branch)
		if err != nil {
			return nil, fmt.Errorf("no active AI session found: %w", err)
		}
	}

	return s.captureOne(prov, summary, req)
}

// CaptureAll detects all sessions for the given project/provider and captures each one.
// Returns results for every successfully captured session. Stops on the first error.
func (s *Service) CaptureAll(req Request) ([]*Result, error) {
	summaries, prov, err := s.detectSessions(req)
	if err != nil {
		return nil, err
	}

	var results []*Result
	for i := range summaries {
		result, captureErr := s.captureOne(prov, &summaries[i], req)
		if captureErr != nil {
			return results, fmt.Errorf("capturing session %s: %w", summaries[i].ID, captureErr)
		}
		results = append(results, result)
	}
	return results, nil
}

// CaptureByID captures a specific session by its provider-native ID.
func (s *Service) CaptureByID(req Request, sessionID session.ID) (*Result, error) {
	_, prov, err := s.detectSessions(req)
	if err != nil {
		return nil, err
	}

	summary := &session.Summary{ID: sessionID}
	return s.captureOne(prov, summary, req)
}

// detectSessions resolves the provider and returns all available session summaries.
func (s *Service) detectSessions(req Request) ([]session.Summary, provider.Provider, error) {
	if req.ProviderName != "" {
		prov, err := s.registry.Get(req.ProviderName)
		if err != nil {
			return nil, nil, fmt.Errorf("provider %q: %w", req.ProviderName, err)
		}
		summaries, err := prov.Detect(req.ProjectPath, req.Branch)
		if err != nil {
			return nil, nil, fmt.Errorf("detecting sessions: %w", err)
		}
		if len(summaries) == 0 {
			return nil, nil, fmt.Errorf("no sessions found for provider %q: %w",
				req.ProviderName, session.ErrSessionNotFound)
		}
		return summaries, prov, nil
	}

	// Auto-detect: use DetectAll to get all sessions across providers
	all, err := s.registry.DetectAll(req.ProjectPath, req.Branch)
	if err != nil {
		return nil, nil, fmt.Errorf("detecting sessions: %w", err)
	}
	if len(all) == 0 {
		return nil, nil, fmt.Errorf("no active AI sessions found: %w", session.ErrProviderNotDetected)
	}

	// All sessions from DetectAll come from potentially different providers.
	// For CaptureAll without explicit provider, we need a provider per session.
	// Simplification: require --provider with --all. This is enforced at CLI level.
	// For now, return the first provider's sessions via DetectBest fallback.
	best, prov, detectErr := s.registry.DetectBest(req.ProjectPath, req.Branch)
	if detectErr != nil {
		return nil, nil, fmt.Errorf("no active AI sessions found: %w", detectErr)
	}
	return []session.Summary{*best}, prov, nil
}

// captureOne exports and processes a single session without persisting it.
// The caller (service layer) is responsible for calling store.Save() after
// enriching the session with costs and analytics — this avoids redundant
// marshal → compress → UPSERT cycles.
//
// If the provider supports FreshnessChecker, it compares the source's
// message count + updated-at with the stored values to skip unchanged sessions.
func (s *Service) captureOne(prov provider.Provider, summary *session.Summary, req Request) (*Result, error) {
	// Skip-if-unchanged optimization: avoid expensive Export for unmodified sessions.
	if checker, ok := prov.(provider.FreshnessChecker); ok {
		if skipped := s.skipIfUnchanged(checker, summary.ID, prov.Name()); skipped {
			return &Result{
				Session:  &session.Session{ID: summary.ID},
				Provider: prov.Name(),
				Skipped:  true,
			}, nil
		}
	}

	// Try incremental export first — reads only new messages.
	// Falls back to full Export() if the provider doesn't support it or if it fails.
	sess, err := s.exportSession(prov, summary.ID, req.Mode)
	if err != nil {
		return nil, fmt.Errorf("exporting session from %s: %w", prov.Name(), err)
	}

	// Safety net: if the provider's Export() forgot to set SourceUpdatedAt,
	// fetch it from the freshness checker so that skip-if-unchanged and
	// DetectSessionStatus work correctly on subsequent captures.
	if sess.SourceUpdatedAt == 0 {
		if checker, ok := prov.(provider.FreshnessChecker); ok {
			if fresh, freshErr := checker.SessionFreshness(summary.ID); freshErr == nil {
				sess.SourceUpdatedAt = fresh.UpdatedAt
			}
		}
	}

	if req.Message != "" {
		sess.Summary = req.Message
	}
	if sess.Branch == "" {
		sess.Branch = req.Branch
	}
	if sess.ProjectPath == "" {
		sess.ProjectPath = req.ProjectPath
	}
	sess.StorageMode = req.Mode

	// Auto-detect session lifecycle status from source freshness.
	if sess.Status == "" {
		sess.Status = session.DetectSessionStatus(sess.SourceUpdatedAt, sess.CreatedAt)
	}

	if sess.Branch != "" {
		sess.Links = append(sess.Links, session.Link{
			LinkType: session.LinkBranch,
			Ref:      sess.Branch,
		})
	}

	var secretsFound int
	if s.scanner != nil {
		matches := s.scanner.Scan(sessionText(sess))
		secretsFound = len(matches)

		if secretsFound > 0 {
			switch s.scanner.Mode() {
			case session.SecretModeBlock:
				return nil, fmt.Errorf("%w: %d secrets detected — capture blocked", session.ErrSecretDetected, secretsFound)
			case session.SecretModeMask:
				s.scanner.MaskSession(sess)
			case session.SecretModeWarn:
				// Store as-is, caller should display warning
			}
		}
	}

	if req.OwnerID != "" {
		sess.OwnerID = req.OwnerID
	}

	// NOTE: No store.Save() here — the caller (session service) handles
	// persistence after stampCosts + stampAnalytics to avoid redundant writes.

	return &Result{
		Session:      sess,
		Provider:     prov.Name(),
		SecretsFound: secretsFound,
	}, nil
}

// exportSession tries incremental export first, then falls back to full Export().
// Incremental export reads only new messages since the last capture, avoiding
// re-reading hundreds of already-captured messages from the provider's storage.
func (s *Service) exportSession(prov provider.Provider, sessionID session.ID, mode session.StorageMode) (*session.Session, error) {
	inc, ok := prov.(provider.IncrementalExporter)
	if !ok {
		return prov.Export(sessionID, mode) // provider doesn't support incremental
	}

	// Check what we already have stored.
	storedCount, _, storeErr := s.store.GetFreshness(sessionID)
	if storeErr != nil || storedCount == 0 {
		return prov.Export(sessionID, mode) // first capture or store error
	}

	// Try incremental: read only messages after storedCount.
	incResult, incErr := inc.ExportIncremental(sessionID, storedCount, mode)
	if incErr != nil {
		// Incremental failed — fall back to full export.
		return prov.Export(sessionID, mode)
	}

	// Load the existing session from store and merge new messages.
	existing, getErr := s.store.Get(sessionID)
	if getErr != nil {
		// Can't load existing session — fall back to full export.
		return prov.Export(sessionID, mode)
	}

	// Merge: append new messages to existing session.
	existing.Messages = append(existing.Messages, incResult.NewMessages...)
	existing.TokenUsage = incResult.TokenUsage
	existing.SourceUpdatedAt = incResult.UpdatedAt
	existing.StorageMode = mode

	// Merge errors: combine existing errors with new ones (dedup by message ID).
	if len(incResult.Errors) > 0 {
		existingErrMsgIDs := make(map[string]bool)
		for _, e := range existing.Errors {
			existingErrMsgIDs[e.MessageID] = true
		}
		for _, e := range incResult.Errors {
			if !existingErrMsgIDs[e.MessageID] {
				existing.Errors = append(existing.Errors, e)
			}
		}
	}

	// Replace children entirely (incremental child export is not supported).
	if len(incResult.Children) > 0 {
		existing.Children = incResult.Children
	}

	// Re-detect lifecycle status with updated timestamp.
	existing.Status = session.DetectSessionStatus(existing.SourceUpdatedAt, existing.CreatedAt)

	return existing, nil
}

// skipIfUnchanged checks if a session has changed since the last capture
// by comparing the source's message count and updated-at timestamp with
// the values stored in aisync's database.
//
// Returns true (skip) when both match exactly — the session is unchanged.
// Returns false (re-capture) when:
//   - First capture (session not in store yet)
//   - Message count differs (new messages, or rewind deleted some)
//   - Source updated-at differs (catches rewind+re-add edge case where count stays same)
//   - Any error during freshness check (fail-open: re-capture is safe)
func (s *Service) skipIfUnchanged(checker provider.FreshnessChecker, sessionID session.ID, provName session.ProviderName) bool {
	source, err := checker.SessionFreshness(sessionID)
	if err != nil {
		return false // can't determine freshness → re-capture
	}

	storedCount, storedUpdatedAt, err := s.store.GetFreshness(sessionID)
	if err != nil {
		return false // store error → re-capture
	}
	if storedCount == 0 && storedUpdatedAt == 0 {
		return false // first capture — session not in store yet
	}

	return storedCount == source.MessageCount && storedUpdatedAt == source.UpdatedAt
}

// sessionText concatenates all scannable text from a session.
func sessionText(sess *session.Session) string {
	var text string
	for _, msg := range sess.Messages {
		text += msg.Content + "\n"
		for _, tc := range msg.ToolCalls {
			text += tc.Output + "\n"
		}
	}
	return text
}
