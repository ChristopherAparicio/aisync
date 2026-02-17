// Package capture implements the session capture workflow.
// It orchestrates provider detection, session export, and storage.
package capture

import (
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/provider"
)

// Request contains all inputs for a capture operation.
type Request struct {
	ProjectPath  string
	Branch       string
	Mode         domain.StorageMode
	ProviderName domain.ProviderName // empty = auto-detect
	Message      string              // optional manual summary override
}

// Result contains the output of a capture operation.
type Result struct {
	Session      *domain.Session
	Provider     domain.ProviderName
	SecretsFound int // number of secrets detected (0 if no scanner)
}

// Service orchestrates the capture workflow.
type Service struct {
	registry *provider.Registry
	store    domain.Store
	scanner  domain.SecretScanner // optional — nil means no scanning
}

// NewService creates a capture service.
func NewService(registry *provider.Registry, store domain.Store) *Service {
	return &Service{
		registry: registry,
		store:    store,
	}
}

// NewServiceWithScanner creates a capture service with secret scanning.
func NewServiceWithScanner(registry *provider.Registry, store domain.Store, scanner domain.SecretScanner) *Service {
	return &Service{
		registry: registry,
		store:    store,
		scanner:  scanner,
	}
}

// Capture detects the active AI session, exports it, and stores it.
func (s *Service) Capture(req Request) (*Result, error) {
	var (
		summary *domain.SessionSummary
		prov    domain.Provider
		err     error
	)

	if req.ProviderName != "" {
		// Explicit provider selection
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
				req.ProviderName, req.Branch, domain.ErrSessionNotFound)
		}
		summary = &summaries[0]
	} else {
		// Auto-detect: find the best session across all providers
		summary, prov, err = s.registry.DetectBest(req.ProjectPath, req.Branch)
		if err != nil {
			return nil, fmt.Errorf("no active AI session found: %w", err)
		}
	}

	// Export the session from the provider
	session, err := prov.Export(summary.ID, req.Mode)
	if err != nil {
		return nil, fmt.Errorf("exporting session from %s: %w", prov.Name(), err)
	}

	// Override summary if provided
	if req.Message != "" {
		session.Summary = req.Message
	}

	// Ensure branch is set
	if session.Branch == "" {
		session.Branch = req.Branch
	}

	// Ensure project path is set
	if session.ProjectPath == "" {
		session.ProjectPath = req.ProjectPath
	}

	// Ensure storage mode reflects the requested mode
	session.StorageMode = req.Mode

	// Add branch link
	if session.Branch != "" {
		session.Links = append(session.Links, domain.Link{
			LinkType: domain.LinkBranch,
			Ref:      session.Branch,
		})
	}

	// Secret scanning (if scanner is configured)
	var secretsFound int
	if s.scanner != nil {
		matches := s.scanner.Scan(sessionText(session))
		secretsFound = len(matches)

		if secretsFound > 0 {
			switch s.scanner.Mode() {
			case domain.SecretModeBlock:
				return nil, fmt.Errorf("%w: %d secrets detected — capture blocked", domain.ErrSecretDetected, secretsFound)
			case domain.SecretModeMask:
				s.scanner.Mask("") // warm up (no-op)
				maskSession(session, s.scanner)
			case domain.SecretModeWarn:
				// Store as-is, caller should display warning
			}
		}
	}

	// Deduplication: if a session for the same project+branch already exists, update it
	// instead of creating a duplicate. The provider may assign different IDs each time,
	// but logically one branch = one active session.
	if existing, lookupErr := s.store.GetByBranch(session.ProjectPath, session.Branch); lookupErr == nil {
		session.ID = existing.ID
	}

	// Store the session
	if err := s.store.Save(session); err != nil {
		return nil, fmt.Errorf("storing session: %w", err)
	}

	return &Result{
		Session:      session,
		Provider:     prov.Name(),
		SecretsFound: secretsFound,
	}, nil
}

// sessionText concatenates all scannable text from a session.
func sessionText(session *domain.Session) string {
	var text string
	for _, msg := range session.Messages {
		text += msg.Content + "\n"
		for _, tc := range msg.ToolCalls {
			text += tc.Output + "\n"
		}
	}
	return text
}

// maskSession applies masking to all text content in a session.
func maskSession(session *domain.Session, scanner domain.SecretScanner) {
	for i := range session.Messages {
		session.Messages[i].Content = scanner.Mask(session.Messages[i].Content)
		for j := range session.Messages[i].ToolCalls {
			session.Messages[i].ToolCalls[j].Output = scanner.Mask(session.Messages[i].ToolCalls[j].Output)
		}
	}
}
