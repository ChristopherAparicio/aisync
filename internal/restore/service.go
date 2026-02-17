// Package restore implements the session restore workflow.
// It looks up stored sessions and imports them into a target provider.
// When the source provider differs from the target, the converter transforms
// the session into the target's native format before importing.
package restore

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/provider"
)

// Request contains all inputs for a restore operation.
type Request struct {
	ProjectPath  string
	Branch       string
	Agent        string              // target agent name (e.g., for OpenCode multi-agent)
	SessionID    domain.SessionID    // empty = lookup by branch
	ProviderName domain.ProviderName // empty = use source provider
	AsContext    bool                // generate CONTEXT.md instead of native import
}

// Result contains the output of a restore operation.
type Result struct {
	Session     *domain.Session
	Method      string // "native", "converted", or "context"
	ContextPath string // only set if Method == "context"
}

// Service orchestrates the restore workflow.
type Service struct {
	converter domain.Converter
	registry  *provider.Registry
	store     domain.Store
}

// NewService creates a restore service.
func NewService(registry *provider.Registry, store domain.Store) *Service {
	return &Service{
		registry: registry,
		store:    store,
	}
}

// NewServiceWithConverter creates a restore service with a cross-provider converter.
func NewServiceWithConverter(registry *provider.Registry, store domain.Store, conv domain.Converter) *Service {
	return &Service{
		registry:  registry,
		store:     store,
		converter: conv,
	}
}

// Restore looks up a session and imports it into a target provider.
func (s *Service) Restore(req Request) (*Result, error) {
	// Step 1: Find the session
	session, err := s.findSession(req)
	if err != nil {
		return nil, err
	}

	// Apply agent override if specified
	if req.Agent != "" {
		session.Agent = req.Agent
	}

	// Step 2: If --as-context, generate CONTEXT.md
	if req.AsContext {
		return s.restoreAsContext(session, req.ProjectPath)
	}

	// Step 3: Determine target provider
	targetName := req.ProviderName
	if targetName == "" {
		targetName = session.Provider
	}

	target, targetErr := s.registry.Get(targetName)
	if targetErr != nil {
		// Fall back to CONTEXT.md if target provider not available
		return s.restoreAsContext(session, req.ProjectPath)
	}

	// Step 4: Check if import is supported
	if !target.CanImport() {
		return s.restoreAsContext(session, req.ProjectPath)
	}

	// Step 5: Cross-provider conversion if source != target
	if session.Provider != targetName && s.converter != nil {
		return s.restoreWithConversion(session, target, targetName, req.ProjectPath)
	}

	// Step 6: Direct import (same provider)
	if importErr := target.Import(session); importErr != nil {
		// Fall back to CONTEXT.md on import failure
		return s.restoreAsContext(session, req.ProjectPath)
	}

	return &Result{
		Session: session,
		Method:  "native",
	}, nil
}

// restoreWithConversion converts the session to the target format and imports it.
func (s *Service) restoreWithConversion(session *domain.Session, target domain.Provider, targetName domain.ProviderName, projectPath string) (*Result, error) {
	// Convert session to target native format, then back to unified
	// This round-trips through the native format to apply format-specific transformations
	nativeData, convErr := s.converter.ToNative(session, targetName)
	if convErr != nil {
		// Conversion not supported for this pair — fall back to CONTEXT.md
		return s.restoreAsContext(session, projectPath)
	}

	converted, parseErr := s.converter.FromNative(nativeData, targetName)
	if parseErr != nil {
		return s.restoreAsContext(session, projectPath)
	}

	// Preserve original metadata
	converted.ID = session.ID
	converted.Provider = targetName

	if importErr := target.Import(converted); importErr != nil {
		return s.restoreAsContext(session, projectPath)
	}

	return &Result{
		Session: session,
		Method:  "converted",
	}, nil
}

// restoreAsContext generates a CONTEXT.md fallback.
func (s *Service) restoreAsContext(session *domain.Session, projectPath string) (*Result, error) {
	contextPath, genErr := generateContextFile(session, projectPath)
	if genErr != nil {
		return nil, fmt.Errorf("generating CONTEXT.md: %w", genErr)
	}
	return &Result{
		Session:     session,
		Method:      "context",
		ContextPath: contextPath,
	}, nil
}

func (s *Service) findSession(req Request) (*domain.Session, error) {
	if req.SessionID != "" {
		session, err := s.store.Get(req.SessionID)
		if err != nil {
			return nil, fmt.Errorf("session %q not found: %w", req.SessionID, err)
		}
		return session, nil
	}

	session, err := s.store.GetByBranch(req.ProjectPath, req.Branch)
	if err != nil {
		return nil, fmt.Errorf("no session found for branch %q: %w", req.Branch, err)
	}
	return session, nil
}

// generateContextFile creates a CONTEXT.md file using the shared converter.
func generateContextFile(session *domain.Session, projectPath string) (string, error) {
	content := converter.ToContextMD(session)
	contextPath := filepath.Join(projectPath, "CONTEXT.md")
	if err := os.WriteFile(contextPath, content, 0o644); err != nil {
		return "", err
	}
	return contextPath, nil
}
