// Package factory wires the default dependencies for the aisync CLI.
// This is the composition root — the only place that knows about concrete types.
package factory

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/platform"
	ghplatform "github.com/ChristopherAparicio/aisync/internal/platform/github"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/provider/claude"
	cursorprov "github.com/ChristopherAparicio/aisync/internal/provider/cursor"
	"github.com/ChristopherAparicio/aisync/internal/provider/opencode"
	"github.com/ChristopherAparicio/aisync/internal/secrets"
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
		cachedConfig domain.Config
		configOnce   sync.Once
		configErr    error

		cachedStore domain.Store
		storeOnce   sync.Once
		storeErr    error

		cachedGit *git.Client
		gitOnce   sync.Once
		gitErr    error

		cachedRegistry *provider.Registry
		registryOnce   sync.Once

		cachedScanner domain.SecretScanner
		scannerOnce   sync.Once

		cachedPlatform domain.Platform
		platformOnce   sync.Once
		platformErr    error
	)

	f.ConfigFunc = func() (domain.Config, error) {
		configOnce.Do(func() {
			globalDir := globalConfigDir()
			repoDir := repoConfigDir()
			cachedConfig, configErr = config.New(globalDir, repoDir)
		})
		return cachedConfig, configErr
	}

	f.StoreFunc = func() (domain.Store, error) {
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
				gitErr = domain.ErrConfigNotFound
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

	f.ScannerFunc = func() domain.SecretScanner {
		scannerOnce.Do(func() {
			// Determine secret mode from config
			mode := domain.SecretModeMask
			cfg, cfgErr := f.Config()
			if cfgErr == nil {
				mode = cfg.GetSecretsMode()
			}
			cachedScanner = secrets.NewScanner(mode, nil)
		})
		return cachedScanner
	}

	f.PlatformFunc = func() (domain.Platform, error) {
		platformOnce.Do(func() {
			gitClient, gitClientErr := f.Git()
			if gitClientErr != nil {
				platformErr = domain.ErrPlatformNotDetected
				return
			}
			remoteURL := gitClient.RemoteURL("origin")
			if remoteURL == "" {
				platformErr = domain.ErrPlatformNotDetected
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
			case domain.PlatformGitHub:
				cachedPlatform = ghplatform.New(topLevel)
			default:
				platformErr = fmt.Errorf("platform %q detected but not yet supported", platformName)
			}
		})
		return cachedPlatform, platformErr
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
