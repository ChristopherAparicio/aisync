// Package cmdutil provides shared utilities for CLI commands.
package cmdutil

import (
	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/hooks"
	"github.com/ChristopherAparicio/aisync/internal/platform"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/secrets"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Factory provides lazy access to shared dependencies for CLI commands.
// Dependencies are initialized on first access so that commands that don't
// need them (e.g., "version") don't pay the cost.
type Factory struct {
	IOStreams *iostreams.IOStreams

	// Lazy initializers — set by the composition root (default.go).
	ConfigFunc          func() (*config.Config, error)
	StoreFunc           func() (storage.Store, error)
	GitFunc             func() (*git.Client, error)
	RegistryFunc        func() *provider.Registry
	ScannerFunc         func() *secrets.Scanner
	PlatformFunc        func() (platform.Platform, error)
	ConverterFunc       func() *converter.Converter
	HooksManagerFunc    func() (*hooks.Manager, error)
	SessionServiceFunc  func() (service.SessionServicer, error)
	SyncServiceFunc     func() (*service.SyncService, error)
	RegistryServiceFunc func() (*service.RegistryService, error)
	AnalysisServiceFunc func() (service.AnalysisServicer, error)

	// CloseFunc releases resources (e.g., database connections).
	// Set by the composition root, called by main on exit.
	CloseFunc func() error
}

// Config returns the Config instance, initializing it on first call.
func (f *Factory) Config() (*config.Config, error) {
	if f.ConfigFunc == nil {
		return nil, session.ErrConfigNotFound
	}
	return f.ConfigFunc()
}

// Store returns the Store instance, initializing it on first call.
func (f *Factory) Store() (storage.Store, error) {
	if f.StoreFunc == nil {
		return nil, session.ErrConfigNotFound
	}
	return f.StoreFunc()
}

// Git returns the Git client, initializing it on first call.
func (f *Factory) Git() (*git.Client, error) {
	if f.GitFunc == nil {
		return nil, session.ErrConfigNotFound
	}
	return f.GitFunc()
}

// Registry returns the provider Registry.
func (f *Factory) Registry() *provider.Registry {
	if f.RegistryFunc == nil {
		return provider.NewRegistry()
	}
	return f.RegistryFunc()
}

// Scanner returns the SecretScanner, or nil if not configured.
func (f *Factory) Scanner() *secrets.Scanner {
	if f.ScannerFunc == nil {
		return nil
	}
	return f.ScannerFunc()
}

// Platform returns the code hosting platform client (GitHub, GitLab, etc.).
// Returns ErrPlatformNotDetected if the platform cannot be determined.
func (f *Factory) Platform() (platform.Platform, error) {
	if f.PlatformFunc == nil {
		return nil, session.ErrPlatformNotDetected
	}
	return f.PlatformFunc()
}

// Converter returns the session format converter.
func (f *Factory) Converter() *converter.Converter {
	if f.ConverterFunc == nil {
		return converter.New()
	}
	return f.ConverterFunc()
}

// HooksManager returns the git hooks manager.
// Returns an error if the git hooks directory cannot be determined.
func (f *Factory) HooksManager() (*hooks.Manager, error) {
	if f.HooksManagerFunc == nil {
		return nil, session.ErrConfigNotFound
	}
	return f.HooksManagerFunc()
}

// SessionService returns the session service for business logic operations.
// Returns a SessionServicer interface — either a local (*SessionService) or
// remote (*remote.SessionService) implementation depending on configuration.
func (f *Factory) SessionService() (service.SessionServicer, error) {
	if f.SessionServiceFunc == nil {
		return nil, session.ErrConfigNotFound
	}
	return f.SessionServiceFunc()
}

// SyncService returns the sync service for push/pull operations.
func (f *Factory) SyncService() (*service.SyncService, error) {
	if f.SyncServiceFunc == nil {
		return nil, session.ErrConfigNotFound
	}
	return f.SyncServiceFunc()
}

// RegistryService returns the registry service for agent capability discovery.
func (f *Factory) RegistryService() (*service.RegistryService, error) {
	if f.RegistryServiceFunc == nil {
		return nil, session.ErrConfigNotFound
	}
	return f.RegistryServiceFunc()
}

// AnalysisService returns the analysis service for session analysis operations.
func (f *Factory) AnalysisService() (service.AnalysisServicer, error) {
	if f.AnalysisServiceFunc == nil {
		return nil, session.ErrConfigNotFound
	}
	return f.AnalysisServiceFunc()
}

// Close releases all resources held by lazy-initialized dependencies.
func (f *Factory) Close() error {
	if f.CloseFunc != nil {
		return f.CloseFunc()
	}
	return nil
}
