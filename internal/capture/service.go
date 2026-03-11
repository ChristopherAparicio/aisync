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
	SecretsFound int // number of secrets detected (0 if no scanner)
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
	if err != nil || len(all) == 0 {
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

// captureOne exports, processes, and stores a single session.
func (s *Service) captureOne(prov provider.Provider, summary *session.Summary, req Request) (*Result, error) {
	sess, err := prov.Export(summary.ID, req.Mode)
	if err != nil {
		return nil, fmt.Errorf("exporting session from %s: %w", prov.Name(), err)
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

	if err := s.store.Save(sess); err != nil {
		return nil, fmt.Errorf("storing session: %w", err)
	}

	return &Result{
		Session:      sess,
		Provider:     prov.Name(),
		SecretsFound: secretsFound,
	}, nil
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
