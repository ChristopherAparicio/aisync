package cmdutil

import (
	"errors"
	"testing"

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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var errTest = errors.New("test error")

// stubFactory returns a Factory with all Func fields set to return errors,
// so we can override individual ones per test.
func stubFactory() *Factory {
	return &Factory{
		IOStreams: iostreams.System(),
	}
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

func TestConfig_NilFunc(t *testing.T) {
	f := stubFactory()
	_, err := f.Config()
	if !errors.Is(err, session.ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestConfig_Success(t *testing.T) {
	cfg, cfgErr := config.New("", "")
	if cfgErr != nil {
		t.Fatalf("config.New: %v", cfgErr)
	}
	f := stubFactory()
	f.ConfigFunc = func() (*config.Config, error) { return cfg, nil }

	got, err := f.Config()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cfg {
		t.Fatal("returned config does not match")
	}
}

func TestConfig_Error(t *testing.T) {
	f := stubFactory()
	f.ConfigFunc = func() (*config.Config, error) { return nil, errTest }

	_, err := f.Config()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

func TestStore_NilFunc(t *testing.T) {
	f := stubFactory()
	_, err := f.Store()
	if !errors.Is(err, session.ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestStore_Success(t *testing.T) {
	var called bool
	f := stubFactory()
	f.StoreFunc = func() (storage.Store, error) {
		called = true
		return nil, nil // nil store is fine for this test
	}

	_, err := f.Store()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("StoreFunc was not called")
	}
}

func TestStore_Error(t *testing.T) {
	f := stubFactory()
	f.StoreFunc = func() (storage.Store, error) { return nil, errTest }

	_, err := f.Store()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Git
// ---------------------------------------------------------------------------

func TestGit_NilFunc(t *testing.T) {
	f := stubFactory()
	_, err := f.Git()
	if !errors.Is(err, session.ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestGit_Success(t *testing.T) {
	client := &git.Client{}
	f := stubFactory()
	f.GitFunc = func() (*git.Client, error) { return client, nil }

	got, err := f.Git()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != client {
		t.Fatal("returned git client does not match")
	}
}

func TestGit_Error(t *testing.T) {
	f := stubFactory()
	f.GitFunc = func() (*git.Client, error) { return nil, errTest }

	_, err := f.Git()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

func TestRegistry_NilFunc(t *testing.T) {
	f := stubFactory()
	got := f.Registry()
	if got == nil {
		t.Fatal("expected fallback Registry, got nil")
	}
}

func TestRegistry_WithFunc(t *testing.T) {
	reg := provider.NewRegistry()
	f := stubFactory()
	f.RegistryFunc = func() *provider.Registry { return reg }

	got := f.Registry()
	if got != reg {
		t.Fatal("returned registry does not match")
	}
}

// ---------------------------------------------------------------------------
// Scanner
// ---------------------------------------------------------------------------

func TestScanner_NilFunc(t *testing.T) {
	f := stubFactory()
	got := f.Scanner()
	if got != nil {
		t.Fatalf("expected nil scanner, got %v", got)
	}
}

func TestScanner_WithFunc(t *testing.T) {
	sc := &secrets.Scanner{}
	f := stubFactory()
	f.ScannerFunc = func() *secrets.Scanner { return sc }

	got := f.Scanner()
	if got != sc {
		t.Fatal("returned scanner does not match")
	}
}

// ---------------------------------------------------------------------------
// Platform
// ---------------------------------------------------------------------------

func TestPlatform_NilFunc(t *testing.T) {
	f := stubFactory()
	_, err := f.Platform()
	if !errors.Is(err, session.ErrPlatformNotDetected) {
		t.Fatalf("expected ErrPlatformNotDetected, got %v", err)
	}
}

func TestPlatform_Success(t *testing.T) {
	var called bool
	f := stubFactory()
	f.PlatformFunc = func() (platform.Platform, error) {
		called = true
		return nil, nil
	}

	_, err := f.Platform()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("PlatformFunc was not called")
	}
}

func TestPlatform_Error(t *testing.T) {
	f := stubFactory()
	f.PlatformFunc = func() (platform.Platform, error) { return nil, errTest }

	_, err := f.Platform()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Converter
// ---------------------------------------------------------------------------

func TestConverter_NilFunc(t *testing.T) {
	f := stubFactory()
	got := f.Converter()
	if got == nil {
		t.Fatal("expected fallback Converter, got nil")
	}
}

func TestConverter_WithFunc(t *testing.T) {
	c := converter.New()
	f := stubFactory()
	f.ConverterFunc = func() *converter.Converter { return c }

	got := f.Converter()
	if got != c {
		t.Fatal("returned converter does not match")
	}
}

// ---------------------------------------------------------------------------
// HooksManager
// ---------------------------------------------------------------------------

func TestHooksManager_NilFunc(t *testing.T) {
	f := stubFactory()
	_, err := f.HooksManager()
	if !errors.Is(err, session.ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestHooksManager_Success(t *testing.T) {
	hm := &hooks.Manager{}
	f := stubFactory()
	f.HooksManagerFunc = func() (*hooks.Manager, error) { return hm, nil }

	got, err := f.HooksManager()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != hm {
		t.Fatal("returned hooks manager does not match")
	}
}

func TestHooksManager_Error(t *testing.T) {
	f := stubFactory()
	f.HooksManagerFunc = func() (*hooks.Manager, error) { return nil, errTest }

	_, err := f.HooksManager()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// SessionService
// ---------------------------------------------------------------------------

func TestSessionService_NilFunc(t *testing.T) {
	f := stubFactory()
	_, err := f.SessionService()
	if !errors.Is(err, session.ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestSessionService_Success(t *testing.T) {
	var called bool
	f := stubFactory()
	f.SessionServiceFunc = func() (service.SessionServicer, error) {
		called = true
		return nil, nil
	}

	_, err := f.SessionService()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("SessionServiceFunc was not called")
	}
}

func TestSessionService_Error(t *testing.T) {
	f := stubFactory()
	f.SessionServiceFunc = func() (service.SessionServicer, error) { return nil, errTest }

	_, err := f.SessionService()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// SyncService
// ---------------------------------------------------------------------------

func TestSyncService_NilFunc(t *testing.T) {
	f := stubFactory()
	_, err := f.SyncService()
	if !errors.Is(err, session.ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestSyncService_Success(t *testing.T) {
	ss := &service.SyncService{}
	f := stubFactory()
	f.SyncServiceFunc = func() (*service.SyncService, error) { return ss, nil }

	got, err := f.SyncService()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ss {
		t.Fatal("returned sync service does not match")
	}
}

func TestSyncService_Error(t *testing.T) {
	f := stubFactory()
	f.SyncServiceFunc = func() (*service.SyncService, error) { return nil, errTest }

	_, err := f.SyncService()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// RegistryService
// ---------------------------------------------------------------------------

func TestRegistryService_NilFunc(t *testing.T) {
	f := stubFactory()
	_, err := f.RegistryService()
	if !errors.Is(err, session.ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestRegistryService_Success(t *testing.T) {
	rs := &service.RegistryService{}
	f := stubFactory()
	f.RegistryServiceFunc = func() (*service.RegistryService, error) { return rs, nil }

	got, err := f.RegistryService()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != rs {
		t.Fatal("returned registry service does not match")
	}
}

func TestRegistryService_Error(t *testing.T) {
	f := stubFactory()
	f.RegistryServiceFunc = func() (*service.RegistryService, error) { return nil, errTest }

	_, err := f.RegistryService()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// AnalysisService
// ---------------------------------------------------------------------------

func TestAnalysisService_NilFunc(t *testing.T) {
	f := stubFactory()
	_, err := f.AnalysisService()
	if !errors.Is(err, session.ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestAnalysisService_Success(t *testing.T) {
	as := service.NewAnalysisService(service.AnalysisServiceConfig{})
	f := stubFactory()
	f.AnalysisServiceFunc = func() (service.AnalysisServicer, error) { return as, nil }

	got, err := f.AnalysisService()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != as {
		t.Fatal("returned analysis service does not match")
	}
}

func TestAnalysisService_Error(t *testing.T) {
	f := stubFactory()
	f.AnalysisServiceFunc = func() (service.AnalysisServicer, error) { return nil, errTest }

	_, err := f.AnalysisService()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestClose_NilFunc(t *testing.T) {
	f := stubFactory()
	err := f.Close()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestClose_Success(t *testing.T) {
	var called bool
	f := stubFactory()
	f.CloseFunc = func() error {
		called = true
		return nil
	}

	err := f.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("CloseFunc was not called")
	}
}

func TestClose_Error(t *testing.T) {
	f := stubFactory()
	f.CloseFunc = func() error { return errTest }

	err := f.Close()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Zero-value Factory — verify no panics
// ---------------------------------------------------------------------------

func TestZeroValueFactory_NoPanics(t *testing.T) {
	f := &Factory{}

	// All error-returning methods should return an error, not panic.
	if _, err := f.Config(); err == nil {
		t.Error("Config: expected error")
	}
	if _, err := f.Store(); err == nil {
		t.Error("Store: expected error")
	}
	if _, err := f.Git(); err == nil {
		t.Error("Git: expected error")
	}
	if _, err := f.Platform(); err == nil {
		t.Error("Platform: expected error")
	}
	if _, err := f.HooksManager(); err == nil {
		t.Error("HooksManager: expected error")
	}
	if _, err := f.SessionService(); err == nil {
		t.Error("SessionService: expected error")
	}
	if _, err := f.SyncService(); err == nil {
		t.Error("SyncService: expected error")
	}
	if _, err := f.RegistryService(); err == nil {
		t.Error("RegistryService: expected error")
	}
	if _, err := f.AnalysisService(); err == nil {
		t.Error("AnalysisService: expected error")
	}

	// Nil-safe methods should return defaults, not panic.
	if got := f.Registry(); got == nil {
		t.Error("Registry: expected fallback, got nil")
	}
	if got := f.Scanner(); got != nil {
		t.Error("Scanner: expected nil")
	}
	if got := f.Converter(); got == nil {
		t.Error("Converter: expected fallback, got nil")
	}

	// Close should be safe on zero-value.
	if err := f.Close(); err != nil {
		t.Errorf("Close: expected nil, got %v", err)
	}
}
