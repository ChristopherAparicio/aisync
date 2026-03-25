// Package restore implements the session restore workflow.
// It looks up stored sessions and imports them into a target provider.
// Each provider's Import() handles internal conversion from the unified
// session format to its native format, so cross-provider restore is simply
// calling target.Import(sess) directly.
package restore

import (
	"fmt"
	"os"
	"path/filepath"

	convpkg "github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// SessionConverter converts sessions between provider formats.
// This interface decouples the restore service from the concrete converter.
type SessionConverter interface {
	SupportedFormats() []session.ProviderName
	ToNative(sess *session.Session, target session.ProviderName) ([]byte, error)
	FromNative(data []byte, source session.ProviderName) (*session.Session, error)
}

// Request contains all inputs for a restore operation.
type Request struct {
	ProjectPath  string
	Branch       string
	Agent        string               // target agent name (e.g., for OpenCode multi-agent)
	SessionID    session.ID           // empty = lookup by branch
	ProviderName session.ProviderName // empty = use source provider
	AsContext    bool                 // generate CONTEXT.md instead of native import

	// Filters is a chain of SessionFilter strategies applied before restore.
	// Each filter transforms the session (e.g. clean errors, redact secrets).
	// Filters are applied in order; an empty slice means no filtering.
	Filters []session.SessionFilter
}

// Result contains the output of a restore operation.
type Result struct {
	Session       *session.Session
	Method        string                 // "native", "converted", or "context"
	ContextPath   string                 // only set if Method == "context"
	FilterResults []session.FilterResult // results from each applied filter (empty if no filters)
}

// Service orchestrates the restore workflow.
type Service struct {
	converter SessionConverter
	registry  *provider.Registry
	store     storage.Store
}

// NewService creates a restore service.
func NewService(registry *provider.Registry, store storage.Store) *Service {
	return &Service{
		registry: registry,
		store:    store,
	}
}

// NewServiceWithConverter creates a restore service with a cross-provider converter.
func NewServiceWithConverter(registry *provider.Registry, store storage.Store, conv SessionConverter) *Service {
	return &Service{
		registry:  registry,
		store:     store,
		converter: conv,
	}
}

// Restore looks up a session and imports it into a target provider.
func (s *Service) Restore(req Request) (*Result, error) {
	// Step 1: Find the session
	sess, err := s.findSession(req)
	if err != nil {
		return nil, err
	}

	// Apply agent override if specified
	if req.Agent != "" {
		sess.Agent = req.Agent
	}

	// Step 2: Apply filter chain (if any)
	var filterResults []session.FilterResult
	if len(req.Filters) > 0 {
		filtered, results, filterErr := session.ApplyFilters(sess, req.Filters)
		if filterErr != nil {
			return nil, fmt.Errorf("applying filters: %w", filterErr)
		}
		sess = filtered
		filterResults = results
	}

	// Step 3: If --as-context, generate CONTEXT.md
	if req.AsContext {
		result, contextErr := s.restoreAsContext(sess, req.ProjectPath)
		if contextErr != nil {
			return nil, contextErr
		}
		result.FilterResults = filterResults
		return result, nil
	}

	// Step 4: Determine target provider
	targetName := req.ProviderName
	if targetName == "" {
		targetName = sess.Provider
	}

	target, targetErr := s.registry.Get(targetName)
	if targetErr != nil {
		// Fall back to CONTEXT.md if target provider not available
		result, contextErr := s.restoreAsContext(sess, req.ProjectPath)
		if contextErr != nil {
			return nil, contextErr
		}
		result.FilterResults = filterResults
		return result, nil
	}

	// Step 5: Check if import is supported
	if !target.CanImport() {
		result, contextErr := s.restoreAsContext(sess, req.ProjectPath)
		if contextErr != nil {
			return nil, contextErr
		}
		result.FilterResults = filterResults
		return result, nil
	}

	// Step 6: Import directly — each provider's Import() handles
	// conversion from the unified session format to its native format.
	// Cross-provider conversion is handled transparently by Import().
	method := "native"
	if sess.Provider != targetName {
		method = "converted"
	}

	if importErr := target.Import(sess); importErr != nil {
		// Fall back to CONTEXT.md on import failure
		result, contextErr := s.restoreAsContext(sess, req.ProjectPath)
		if contextErr != nil {
			return nil, contextErr
		}
		result.FilterResults = filterResults
		return result, nil
	}

	return &Result{
		Session:       sess,
		Method:        method,
		FilterResults: filterResults,
	}, nil
}

// restoreAsContext generates a CONTEXT.md fallback.
func (s *Service) restoreAsContext(sess *session.Session, projectPath string) (*Result, error) {
	contextPath, genErr := generateContextFile(sess, projectPath)
	if genErr != nil {
		return nil, fmt.Errorf("generating CONTEXT.md: %w", genErr)
	}
	return &Result{
		Session:     sess,
		Method:      "context",
		ContextPath: contextPath,
	}, nil
}

func (s *Service) findSession(req Request) (*session.Session, error) {
	if req.SessionID != "" {
		sess, err := s.store.Get(req.SessionID)
		if err != nil {
			return nil, fmt.Errorf("session %q not found: %w", req.SessionID, err)
		}
		return sess, nil
	}

	sess, err := s.store.GetLatestByBranch(req.ProjectPath, req.Branch)
	if err != nil {
		return nil, fmt.Errorf("no session found for branch %q: %w", req.Branch, err)
	}
	return sess, nil
}

// generateContextFile creates a CONTEXT.md file using the shared converter.
func generateContextFile(sess *session.Session, projectPath string) (string, error) {
	content := convpkg.ToContextMD(sess)
	contextPath := filepath.Join(projectPath, "CONTEXT.md")
	if err := os.WriteFile(contextPath, content, 0o644); err != nil {
		return "", err
	}
	return contextPath, nil
}
