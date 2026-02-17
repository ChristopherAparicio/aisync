// Package cmdutil provides shared utilities for CLI commands.
package cmdutil

import (
	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Factory provides lazy access to shared dependencies for CLI commands.
// Dependencies are initialized on first access so that commands that don't
// need them (e.g., "version") don't pay the cost.
type Factory struct {
	IOStreams *iostreams.IOStreams

	// Lazy initializers — set by the composition root (default.go).
	ConfigFunc   func() (domain.Config, error)
	StoreFunc    func() (domain.Store, error)
	GitFunc      func() (*git.Client, error)
	RegistryFunc func() *provider.Registry
	ScannerFunc  func() domain.SecretScanner
	PlatformFunc func() (domain.Platform, error)

	// CloseFunc releases resources (e.g., database connections).
	// Set by the composition root, called by main on exit.
	CloseFunc func() error
}

// Config returns the Config instance, initializing it on first call.
func (f *Factory) Config() (domain.Config, error) {
	if f.ConfigFunc == nil {
		return nil, domain.ErrConfigNotFound
	}
	return f.ConfigFunc()
}

// Store returns the Store instance, initializing it on first call.
func (f *Factory) Store() (domain.Store, error) {
	if f.StoreFunc == nil {
		return nil, domain.ErrConfigNotFound
	}
	return f.StoreFunc()
}

// Git returns the Git client, initializing it on first call.
func (f *Factory) Git() (*git.Client, error) {
	if f.GitFunc == nil {
		return nil, domain.ErrConfigNotFound
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
func (f *Factory) Scanner() domain.SecretScanner {
	if f.ScannerFunc == nil {
		return nil
	}
	return f.ScannerFunc()
}

// Platform returns the code hosting platform client (GitHub, GitLab, etc.).
// Returns ErrPlatformNotDetected if the platform cannot be determined.
func (f *Factory) Platform() (domain.Platform, error) {
	if f.PlatformFunc == nil {
		return nil, domain.ErrPlatformNotDetected
	}
	return f.PlatformFunc()
}

// Close releases all resources held by lazy-initialized dependencies.
func (f *Factory) Close() error {
	if f.CloseFunc != nil {
		return f.CloseFunc()
	}
	return nil
}
