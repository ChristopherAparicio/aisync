// Package factory wires the default dependencies for the aisync CLI.
// This is the composition root — the only place that knows about concrete types.
package factory

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/hooks"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	claudellm "github.com/ChristopherAparicio/aisync/internal/llm/claude"
	"github.com/ChristopherAparicio/aisync/internal/platform"
	ghplatform "github.com/ChristopherAparicio/aisync/internal/platform/github"
	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/provider/claude"
	cursorprov "github.com/ChristopherAparicio/aisync/internal/provider/cursor"
	"github.com/ChristopherAparicio/aisync/internal/provider/opencode"
	"github.com/ChristopherAparicio/aisync/internal/secrets"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

const (
	globalDirName = ".aisync"
	dbFileName    = "sessions.db"
)

// New creates a Factory with default production dependencies.
// Dependencies are lazily initialized on first access.
func New() *cmdutil.Factory {
	f := &cmdutil.Factory{
		IOStreams: iostreams.System(),
	}

	// Lazy singletons
	var (
		cachedConfig *config.Config
		configOnce   sync.Once
		configErr    error

		cachedStore storage.Store
		storeOnce   sync.Once
		storeErr    error

		cachedGit *git.Client
		gitOnce   sync.Once
		gitErr    error

		cachedRegistry *provider.Registry
		registryOnce   sync.Once

		cachedScanner *secrets.Scanner
		scannerOnce   sync.Once

		cachedPlatform platform.Platform
		platformOnce   sync.Once
		platformErr    error

		cachedConverter *converter.Converter
		converterOnce   sync.Once

		cachedHooksMgr *hooks.Manager
		hooksMgrOnce   sync.Once
		hooksMgrErr    error

		cachedSessionSvc *service.SessionService
		sessionSvcOnce   sync.Once
		sessionSvcErr    error

		cachedSyncSvc *service.SyncService
		syncSvcOnce   sync.Once
		syncSvcErr    error
	)

	f.ConfigFunc = func() (*config.Config, error) {
		configOnce.Do(func() {
			globalDir := globalConfigDir()
			repoDir := repoConfigDir()
			cachedConfig, configErr = config.New(globalDir, repoDir)
		})
		return cachedConfig, configErr
	}

	f.StoreFunc = func() (storage.Store, error) {
		storeOnce.Do(func() {
			dir := globalConfigDir()
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				storeErr = mkErr
				return
			}
			dbPath := filepath.Join(dir, dbFileName)
			cachedStore, storeErr = sqlite.New(dbPath)
		})
		return cachedStore, storeErr
	}

	f.GitFunc = func() (*git.Client, error) {
		gitOnce.Do(func() {
			wd, wdErr := os.Getwd()
			if wdErr != nil {
				gitErr = wdErr
				return
			}
			cachedGit = git.NewClient(wd)
			if !cachedGit.IsRepo() {
				gitErr = session.ErrConfigNotFound
				cachedGit = nil
			}
		})
		return cachedGit, gitErr
	}

	f.RegistryFunc = func() *provider.Registry {
		registryOnce.Do(func() {
			cachedRegistry = provider.NewRegistry(
				claude.New(""),     // default ~/.claude
				opencode.New(""),   // default XDG data dir
				cursorprov.New(""), // default platform Cursor dir
			)
		})
		return cachedRegistry
	}

	f.ScannerFunc = func() *secrets.Scanner {
		scannerOnce.Do(func() {
			// Determine secret mode from config
			mode := session.SecretModeMask // default to mask mode
			cfg, cfgErr := f.Config()
			if cfgErr == nil {
				mode = cfg.GetSecretsMode()
			}
			cachedScanner = secrets.NewScanner(mode, nil)
		})
		return cachedScanner
	}

	f.PlatformFunc = func() (platform.Platform, error) {
		platformOnce.Do(func() {
			gitClient, gitClientErr := f.Git()
			if gitClientErr != nil {
				platformErr = session.ErrPlatformNotDetected
				return
			}
			remoteURL := gitClient.RemoteURL("origin")
			if remoteURL == "" {
				platformErr = session.ErrPlatformNotDetected
				return
			}
			platformName, detectErr := platform.DetectFromRemoteURL(remoteURL)
			if detectErr != nil {
				platformErr = detectErr
				return
			}
			topLevel, topErr := gitClient.TopLevel()
			if topErr != nil {
				platformErr = topErr
				return
			}
			switch platformName {
			case session.PlatformGitHub:
				cachedPlatform = ghplatform.New(topLevel)
			default:
				platformErr = fmt.Errorf("platform %q detected but not yet supported", platformName)
			}
		})
		return cachedPlatform, platformErr
	}

	f.ConverterFunc = func() *converter.Converter {
		converterOnce.Do(func() {
			cachedConverter = converter.New()
		})
		return cachedConverter
	}

	f.HooksManagerFunc = func() (*hooks.Manager, error) {
		hooksMgrOnce.Do(func() {
			gitClient, gitClientErr := f.Git()
			if gitClientErr != nil {
				hooksMgrErr = gitClientErr
				return
			}
			hooksDir, hooksErr := gitClient.HooksPath()
			if hooksErr != nil {
				hooksMgrErr = hooksErr
				return
			}
			cachedHooksMgr = hooks.NewManager(hooksDir)
		})
		return cachedHooksMgr, hooksMgrErr
	}

	f.SessionServiceFunc = func() (*service.SessionService, error) {
		sessionSvcOnce.Do(func() {
			store, stErr := f.Store()
			if stErr != nil {
				sessionSvcErr = stErr
				return
			}

			registry := f.Registry()
			scanner := f.Scanner()
			conv := f.Converter()

			// Git and platform are optional — service works without them
			gitClient, _ := f.Git()
			plat, _ := f.Platform()

			// LLM client is optional — nil disables AI features (summarize, explain).
			// Only created if the claude binary is available in PATH.
			var llmClient llm.Client
			if _, lookupErr := exec.LookPath("claude"); lookupErr == nil {
				llmClient = claudellm.New()
			}

			// Pricing calculator — apply user overrides from config if available.
			var calc *pricing.Calculator
			cfg, cfgErr := f.Config()
			if cfgErr == nil {
				overrides := cfg.GetPricingOverrides()
				if len(overrides) > 0 {
					modelPrices := make([]pricing.ModelPrice, len(overrides))
					for i, o := range overrides {
						modelPrices[i] = pricing.ModelPrice{
							Model:           o.Model,
							InputPerMToken:  o.InputPerMToken,
							OutputPerMToken: o.OutputPerMToken,
						}
					}
					calc = pricing.NewCalculator().WithOverrides(modelPrices)
				}
			}

			cachedSessionSvc = service.NewSessionService(service.SessionServiceConfig{
				Store:     store,
				Registry:  registry,
				Scanner:   scanner,
				Converter: conv,
				Pricing:   calc,
				Git:       gitClient,
				Platform:  plat,
				LLM:       llmClient,
			})
		})
		return cachedSessionSvc, sessionSvcErr
	}

	f.SyncServiceFunc = func() (*service.SyncService, error) {
		syncSvcOnce.Do(func() {
			gitClient, gitClientErr := f.Git()
			if gitClientErr != nil {
				syncSvcErr = gitClientErr
				return
			}
			store, stErr := f.Store()
			if stErr != nil {
				syncSvcErr = stErr
				return
			}
			cachedSyncSvc = service.NewSyncService(gitClient, store)
		})
		return cachedSyncSvc, syncSvcErr
	}

	f.CloseFunc = func() error {
		if cachedStore != nil {
			return cachedStore.Close()
		}
		return nil
	}

	return f
}

// globalConfigDir returns the global aisync config directory (~/.aisync/).
func globalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, globalDirName)
}

// repoConfigDir returns the per-repo config directory (.aisync/ in repo root).
// Returns empty string if not in a git repo.
func repoConfigDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}

	client := git.NewClient(wd)
	topLevel, err := client.TopLevel()
	if err != nil {
		return ""
	}

	return filepath.Join(topLevel, globalDirName)
}
