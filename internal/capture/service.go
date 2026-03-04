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

// Capture detects the active AI session, exports it, and stores it.
func (s *Service) Capture(req Request) (*Result, error) {
	var (
		summary *session.Summary
		prov    provider.Provider
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
				req.ProviderName, req.Branch, session.ErrSessionNotFound)
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
	sess, err := prov.Export(summary.ID, req.Mode)
	if err != nil {
		return nil, fmt.Errorf("exporting session from %s: %w", prov.Name(), err)
	}

	// Override summary if provided
	if req.Message != "" {
		sess.Summary = req.Message
	}

	// Ensure branch is set
	if sess.Branch == "" {
		sess.Branch = req.Branch
	}

	// Ensure project path is set
	if sess.ProjectPath == "" {
		sess.ProjectPath = req.ProjectPath
	}

	// Ensure storage mode reflects the requested mode
	sess.StorageMode = req.Mode

	// Add branch link
	if sess.Branch != "" {
		sess.Links = append(sess.Links, session.Link{
			LinkType: session.LinkBranch,
			Ref:      sess.Branch,
		})
	}

	// Secret scanning (if scanner is configured)
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

	// Deduplication: if a session for the same project+branch already exists, update it
	// instead of creating a duplicate. The provider may assign different IDs each time,
	// but logically one branch = one active session.
	if existing, lookupErr := s.store.GetByBranch(sess.ProjectPath, sess.Branch); lookupErr == nil {
		sess.ID = existing.ID
	}

	// Attach owner identity if provided
	if req.OwnerID != "" {
		sess.OwnerID = req.OwnerID
	}

	// Store the session
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
