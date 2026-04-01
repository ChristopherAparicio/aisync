// Package factory wires the default dependencies for the aisync CLI.
// This is the composition root — the only place that knows about concrete types.
package factory

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/ChristopherAparicio/aisync/client"
	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/internal/benchmark"
	"github.com/ChristopherAparicio/aisync/internal/categorizer"
	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/errorclass"
	"github.com/ChristopherAparicio/aisync/internal/hooks"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	claudellm "github.com/ChristopherAparicio/aisync/internal/llm/claude"
	ollamallm "github.com/ChristopherAparicio/aisync/internal/llm/ollama"
	"github.com/ChristopherAparicio/aisync/internal/llmfactory"
	"github.com/ChristopherAparicio/aisync/internal/platform"
	ghplatform "github.com/ChristopherAparicio/aisync/internal/platform/github"
	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/provider/claude"
	cursorprov "github.com/ChristopherAparicio/aisync/internal/provider/cursor"
	"github.com/ChristopherAparicio/aisync/internal/provider/opencode"
	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/search/fts5"
	"github.com/ChristopherAparicio/aisync/internal/search/like"
	"github.com/ChristopherAparicio/aisync/internal/secrets"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/service/remote"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
	"github.com/ChristopherAparicio/aisync/internal/tagger"
	webhookspkg "github.com/ChristopherAparicio/aisync/internal/webhooks"
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
		cachedRemoteSvc  *remote.SessionService
		sessionSvcOnce   sync.Once
		sessionSvcErr    error

		cachedSyncSvc *service.SyncService
		syncSvcOnce   sync.Once
		syncSvcErr    error

		cachedRegistrySvc *service.RegistryService
		registrySvcOnce   sync.Once
		registrySvcErr    error

		cachedCalc *pricing.Calculator
		calcOnce   sync.Once

		cachedAnalysisSvc service.AnalysisServicer
		analysisSvcOnce   sync.Once
		analysisSvcErr    error

		cachedErrorSvc service.ErrorServicer
		errorSvcOnce   sync.Once
		errorSvcErr    error

		cachedSessionEventSvc *sessionevent.Service
		sessionEventSvcOnce   sync.Once
		sessionEventSvcErr    error
	)

	// initCalc initializes the shared pricing calculator.
	// Catalog chain: LiteLLM cache (2500+ models) → Embedded YAML (fallback) → User overrides (top).
	initCalc := func() *pricing.Calculator {
		calcOnce.Do(func() {
			// Step 1: Build the base catalog (LiteLLM → Embedded fallback).
			baseCatalog := pricing.DefaultCatalog()

			liteLLMCat, liteLLMErr := pricing.NewLiteLLMCatalog(pricing.LiteLLMCatalogConfig{
				CacheDir: globalConfigDir(),
			})
			if liteLLMErr == nil {
				// LiteLLM cache available → use it as primary with embedded as fallback.
				baseCatalog = pricing.NewFallbackCatalog(liteLLMCat, pricing.DefaultCatalog())
			}
			// If LiteLLM cache not available, silently fall back to embedded YAML only.

			// Step 2: Create calculator with the base catalog.
			cachedCalc = pricing.NewCalculatorWithCatalog(baseCatalog)

			// Step 3: Layer user-configured pricing overrides on top.
			cfg, cfgErr := f.Config()
			if cfgErr != nil {
				return
			}
			overrides := cfg.GetPricingOverrides()
			if len(overrides) == 0 {
				return
			}
			modelPrices := make([]pricing.ModelPrice, len(overrides))
			for i, o := range overrides {
				mp := pricing.ModelPrice{
					Model:               o.Model,
					InputPerMToken:      o.InputPerMToken,
					OutputPerMToken:     o.OutputPerMToken,
					CacheReadPerMToken:  o.CacheReadPerMToken,
					CacheWritePerMToken: o.CacheWritePerMToken,
				}
				for _, t := range o.Tiers {
					mp.Tiers = append(mp.Tiers, pricing.PricingTier{
						ThresholdTokens:  t.ThresholdTokens,
						InputMultiplier:  t.InputMultiplier,
						OutputMultiplier: t.OutputMultiplier,
					})
				}
				modelPrices[i] = mp
			}
			cachedCalc = cachedCalc.WithOverrides(modelPrices)
		})
		return cachedCalc
	}

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
			// Check if database.path is configured (config or env var).
			var dbPath string
			if cfg, cfgErr := f.Config(); cfgErr == nil {
				dbPath = cfg.GetDatabasePath()
			}

			if dbPath == "" {
				// Default: ~/.aisync/sessions.db
				dir := globalConfigDir()
				if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
					storeErr = mkErr
					return
				}
				dbPath = filepath.Join(dir, dbFileName)
			} else {
				// Custom path: ensure parent directory exists.
				dir := filepath.Dir(dbPath)
				if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
					storeErr = mkErr
					return
				}
			}

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

	f.SessionServiceFunc = func() (service.SessionServicer, error) {
		sessionSvcOnce.Do(func() {
			// ── Dual-mode detection ──
			// If server.url is configured and the server is reachable, use the remote adapter.
			if cfg, cfgErr := f.Config(); cfgErr == nil {
				if serverURL := cfg.GetServerURL(); serverURL != "" {
					c := newAuthenticatedClient(serverURL)
					if c.IsAvailable() {
						cachedRemoteSvc = remote.New(c)
						fmt.Fprintf(os.Stderr, "[aisync] connected to server at %s\n", serverURL)
						return
					}
					fmt.Fprintf(os.Stderr, "[aisync] server at %s unavailable, falling back to local mode\n", serverURL)
				}
			}

			// ── Local mode (default) ──
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
			var llmClient llm.Client
			cfg0, cfg0Err := f.Config()
			if cfg0Err == nil && cfg0.GetAnalysisAdapter() == "ollama" {
				llmClient = ollamallm.New(ollamallm.Config{
					BaseURL: cfg0.GetAnalysisOllamaURL(),
					Model:   cfg0.GetAnalysisModel(),
				})
			} else if _, lookupErr := exec.LookPath("claude"); lookupErr == nil {
				llmClient = claudellm.New()
			}

			// Pricing calculator — shared across services (respects user overrides).
			calc := initCalc()
			cfg, cfgErr := f.Config()

			// ── Webhook dispatcher (optional) ──
			var whDispatcher *webhookspkg.Dispatcher
			if cfgErr == nil {
				entries := cfg.GetWebhookEntries()
				if len(entries) > 0 {
					var hooks []webhookspkg.HookConfig
					for _, e := range entries {
						evts := make([]webhookspkg.EventType, len(e.Events))
						for i, ev := range e.Events {
							evts[i] = webhookspkg.EventType(ev)
						}
						hooks = append(hooks, webhookspkg.HookConfig{
							URL:    e.URL,
							Events: evts,
							Secret: e.Secret,
						})
					}
					whDispatcher = webhookspkg.New(webhookspkg.Config{Hooks: hooks})
				}
			}

			// Post-capture hooks: auto-analysis + auto-tagging + webhooks.
			var postCapture service.PostCaptureFunc

			wantAnalysis := cfgErr == nil && cfg.IsAnalysisAutoEnabled()
			wantTagging := cfgErr == nil && cfg.IsTaggingAutoEnabled()
			wantCategoryDetect := cfgErr == nil && cfg.IsProjectAutoDetectEnabled()
			wantFileBlame := cfgErr == nil && cfg.IsFileBlameEnabled()
			wantWebhooks := whDispatcher != nil

			// Error classification always runs (it's deterministic and fast).
			wantErrorProcessing := true

			if wantAnalysis || wantTagging || wantCategoryDetect || wantWebhooks || wantErrorProcessing {
				errorThreshold := float64(0)
				minToolCalls := 0
				if wantAnalysis {
					errorThreshold = cfg.GetAnalysisErrorThreshold()
					minToolCalls = cfg.GetAnalysisMinToolCalls()
				}

				postCapture = func(sess *session.Session) {
					// Error classification: classify and persist any errors extracted during capture.
					// This is deterministic (no LLM), fast, and always runs.
					if wantErrorProcessing && len(sess.Errors) > 0 {
						errSvc, errSvcErr := f.ErrorService()
						if errSvcErr == nil {
							_, _ = errSvc.ProcessSession(sess)
						}
					}

					// Session event extraction: extract tool calls, skills, agents, commands
					// into events and aggregate into hourly/daily buckets.
					// This is deterministic (no LLM), fast, and always runs.
					{
						sessionEventSvc, svcErr := f.SessionEventService()
						if svcErr == nil {
							_ = sessionEventSvc.ProcessSession(sess)
						}
					}

					// Webhook: session.captured
					if wantWebhooks {
						whDispatcher.Fire(webhookspkg.EventSessionCaptured, map[string]any{
							"session_id": string(sess.ID),
							"provider":   string(sess.Provider),
							"agent":      sess.Agent,
							"branch":     sess.Branch,
							"summary":    sess.Summary,
							"tokens":     sess.TokenUsage.TotalTokens,
						})
					}

					// Auto-tagging: classify session type if not already tagged.
					if wantTagging && sess.SessionType == "" {
						analyzer, analyzerErr := llmfactory.NewAnalyzerFromConfig(cfg, cfg.GetTaggingProfile())
						if analyzerErr == nil {
							result := tagger.Classify(context.Background(), analyzer, sess, cfg.GetTaggingTags(), tagger.DefaultMaxMessages)
							if result.Tag != "" && result.Tag != "other" {
								sess.SessionType = result.Tag
								_ = store.UpdateSessionType(sess.ID, result.Tag)

								// Webhook: session.tagged
								if wantWebhooks {
									whDispatcher.Fire(webhookspkg.EventSessionTagged, map[string]any{
										"session_id":   string(sess.ID),
										"session_type": result.Tag,
										"confidence":   result.Confidence,
									})
								}
							}
						}
					}

					// Project category auto-detection (cascade: config → heuristic → LLM).
					if wantCategoryDetect && sess.ProjectCategory == "" {
						cat := cfg.GetProjectCategory() // 1. Manual config (priority)
						if cat == "" && sess.ProjectPath != "" {
							cat = categorizer.DetectProjectCategory(sess.ProjectPath) // 2. File heuristic (free)
						}
						if cat == "" {
							// 3. LLM fallback (costs tokens)
							catAnalyzer, catErr := llmfactory.NewAnalyzerFromConfig(cfg, cfg.GetTaggingProfile())
							if catErr == nil {
								catResult := categorizer.ClassifyProject(
									context.Background(), catAnalyzer, sess, cfg.GetProjectCategories(),
								)
								if catResult.Category != "" {
									cat = catResult.Category
								}
							}
						}
						if cat != "" {
							sess.ProjectCategory = cat
							_, _ = store.UpdateProjectCategory(sess.ProjectPath, cat)
						}
					}

					// Per-project classifier rules (ticket extraction, branch-to-type mapping).
					if classSvc, classSvcErr := f.SessionService(); classSvcErr == nil {
						classSvc.ClassifySession(sess)
						// Index into search engine (FTS5, etc.) — uses concrete type.
						if indexer, ok := classSvc.(*service.SessionService); ok {
							indexer.IndexSession(sess)
						}

						// File blame extraction: parse tool calls to find file operations.
						if wantFileBlame {
							if _, err := classSvc.ExtractAndSaveFiles(sess); err != nil {
								slog.Warn("file blame extraction failed", "session", sess.ID, "error", err)
							}
						}
					}

					// Auto-analysis: only if error rate exceeds threshold.
					if wantAnalysis && service.ShouldAutoAnalyze(sess, errorThreshold, minToolCalls) {
						analysisSvc, analysisErr := f.AnalysisService()
						if analysisErr != nil {
							return
						}
						analysisResult, _ := analysisSvc.Analyze(context.Background(), service.AnalysisRequest{
							SessionID:      sess.ID,
							Trigger:        analysis.TriggerAuto,
							ErrorThreshold: errorThreshold,
							MinToolCalls:   minToolCalls,
						})

						// Auto-objective: compute intent/outcome for the session.
						// Reuse the same LLM call context.
						if sess != nil {
							sessionSvcLocal, svcErr := f.SessionService()
							if svcErr == nil {
								_, _ = sessionSvcLocal.ComputeObjective(context.Background(), service.ComputeObjectiveRequest{
									SessionID: string(sess.ID),
									Session:   sess,
								})
							}
						}

						// Webhook: session.analyzed
						if wantWebhooks && analysisResult != nil {
							whDispatcher.Fire(webhookspkg.EventSessionAnalyzed, map[string]any{
								"session_id": string(sess.ID),
								"score":      analysisResult.Analysis.Report.Score,
								"summary":    analysisResult.Analysis.Report.Summary,
								"problems":   len(analysisResult.Analysis.Report.Problems),
							})
						}
					}
				}
			}

			cachedSessionSvc = service.NewSessionService(service.SessionServiceConfig{
				Store:        store,
				Registry:     registry,
				Scanner:      scanner,
				Converter:    conv,
				Pricing:      calc,
				Config:       cfg,
				Git:          gitClient,
				Platform:     plat,
				LLM:          llmClient,
				SearchEngine: buildSearchEngine(cfg, store),
				PostCapture:  postCapture,
			})
		})
		if cachedRemoteSvc != nil {
			return cachedRemoteSvc, nil
		}
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

			// Wire PostPull so that synced sessions get event extraction,
			// error classification, etc. — reuses the same PostCaptureFunc
			// from SessionService (resolved lazily to avoid circular init).
			cachedSyncSvc.SetPostPull(func(sess *session.Session) {
				// Resolve SessionService lazily (it may not be initialized yet).
				sessionSvc, svcErr := f.SessionService()
				if svcErr != nil {
					return
				}
				if local, ok := sessionSvc.(*service.SessionService); ok {
					local.RunPostCapture(sess)
				}
			})
		})
		return cachedSyncSvc, syncSvcErr
	}

	f.RegistryServiceFunc = func() (*service.RegistryService, error) {
		registrySvcOnce.Do(func() {
			scannerReg := provider.NewScannerRegistry(
				opencode.NewScanner(""),
				claude.NewScanner(""),
				cursorprov.NewScanner(),
			)

			// Store is optional for cost enrichment
			store, _ := f.Store()

			cachedRegistrySvc = service.NewRegistryService(service.RegistryServiceConfig{
				Scanners: scannerReg,
				Store:    store,
				Pricing:  initCalc(),
			})
		})
		return cachedRegistrySvc, registrySvcErr
	}

	f.AnalysisServiceFunc = func() (service.AnalysisServicer, error) {
		analysisSvcOnce.Do(func() {
			// ── Remote mode: if a remote session service is active, use it for analysis too. ──
			if cachedRemoteSvc != nil {
				// The remote client is already connected — reuse it for analysis.
				cfg, cfgErr := f.Config()
				if cfgErr == nil {
					if serverURL := cfg.GetServerURL(); serverURL != "" {
						c := newAuthenticatedClient(serverURL)
						cachedAnalysisSvc = remote.NewAnalysisService(c)
						return
					}
				}
			}

			// ── Local mode ──
			store, stErr := f.Store()
			if stErr != nil {
				analysisSvcErr = stErr
				return
			}

			cfg, cfgErr := f.Config()
			if cfgErr != nil {
				analysisSvcErr = cfgErr
				return
			}

			// Create analyzer from LLM profile (resolves provider + model + infra).
			analyzer, analyzerErr := llmfactory.NewAnalyzerFromConfig(cfg, "")
			if analyzerErr != nil {
				analysisSvcErr = analyzerErr
				return
			}

			cachedAnalysisSvc = service.NewAnalysisService(service.AnalysisServiceConfig{
				Store:    store,
				Analyzer: analyzer,
			})
		})
		return cachedAnalysisSvc, analysisSvcErr
	}

	f.ErrorServiceFunc = func() (service.ErrorServicer, error) {
		errorSvcOnce.Do(func() {
			store, stErr := f.Store()
			if stErr != nil {
				errorSvcErr = stErr
				return
			}

			cachedErrorSvc = service.NewErrorService(service.ErrorServiceConfig{
				Store:      store,
				Classifier: errorclass.NewDeterministicClassifier(),
			})
		})
		return cachedErrorSvc, errorSvcErr
	}

	f.SessionEventServiceFunc = func() (*sessionevent.Service, error) {
		sessionEventSvcOnce.Do(func() {
			store, stErr := f.Store()
			if stErr != nil {
				sessionEventSvcErr = stErr
				return
			}
			cachedSessionEventSvc = sessionevent.NewService(store, slog.Default())
		})
		return cachedSessionEventSvc, sessionEventSvcErr
	}

	f.BenchmarkRecommenderFunc = func() (*benchmark.Recommender, error) {
		// Use MultiEmbeddedCatalog for multi-source composite scoring.
		benchCat, err := benchmark.NewMultiEmbeddedCatalog(benchmark.MultiCatalogConfig{})
		if err != nil {
			return nil, err
		}
		// Reuse the same pricing catalog as the calculator.
		priceCat := pricing.DefaultCatalog()
		liteLLMCat, liteLLMErr := pricing.NewLiteLLMCatalog(pricing.LiteLLMCatalogConfig{
			CacheDir: globalConfigDir(),
		})
		if liteLLMErr == nil {
			priceCat = pricing.NewFallbackCatalog(liteLLMCat, priceCat)
		}
		return benchmark.NewRecommender(benchCat, priceCat), nil
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

// newAuthenticatedClient creates an API client with the stored JWT token (if any).
func newAuthenticatedClient(serverURL string) *client.Client {
	var opts []client.Option
	if token := auth.LoadToken(globalConfigDir()); token != "" {
		opts = append(opts, client.WithAuthToken(token))
	}
	return client.New(serverURL, opts...)
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

// buildSearchEngine creates the appropriate search engine based on config.
// Returns nil if no engine is configured (falls back to basic LIKE in the service).
func buildSearchEngine(cfg *config.Config, store storage.Store) search.Engine {
	if cfg == nil {
		return nil
	}

	engineName := cfg.GetSearchEngine()

	// Always have LIKE as the ultimate fallback.
	likeEngine := like.New(store)

	switch engineName {
	case "fts5":
		// FTS5 needs access to the underlying *sql.DB.
		type dbGetter interface {
			DB() *sql.DB
		}
		if dg, ok := store.(dbGetter); ok {
			eng, err := fts5.New(dg.DB())
			if err != nil {
				log.Printf("[search] failed to init FTS5: %v, falling back to LIKE", err)
				return likeEngine
			}
			log.Printf("[search] FTS5 engine initialized")
			return search.NewChain(log.Default(), eng, likeEngine)
		}
		log.Printf("[search] store does not expose DB() for FTS5, falling back to LIKE")
		return likeEngine
	case "like", "":
		return likeEngine
	default:
		log.Printf("[search] unknown engine %q, falling back to LIKE", engineName)
		return likeEngine
	}
}
