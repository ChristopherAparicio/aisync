// Package servecmd implements the `aisync serve` CLI command.
// It starts a unified HTTP server that serves both the JSON API
// and the web dashboard on a single port. The --web-only flag
// restricts it to only the web dashboard (used by `aisync web`).
package servecmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/api"
	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/internal/llmfactory"
	"github.com/ChristopherAparicio/aisync/internal/llmqueue"
	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/replay"
	"github.com/ChristopherAparicio/aisync/internal/scheduler"
	"github.com/ChristopherAparicio/aisync/internal/skillresolver"
	"github.com/ChristopherAparicio/aisync/internal/skillresolver/llmanalyzer"
	"github.com/ChristopherAparicio/aisync/internal/web"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

const defaultAddr = "127.0.0.1:8371"

// daemonEnvVar is set in the child process to prevent re-forking.
const daemonEnvVar = "_AISYNC_DAEMON"

// pidFileName is the name of the PID file written inside ~/.aisync/.
const pidFileName = "aisync.pid"

// logFileName is the name of the log file written inside ~/.aisync/.
const logFileName = "aisync.log"

// configDir returns the path to ~/.aisync/, creating it if necessary.
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	dir := filepath.Join(home, ".aisync")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// pidFilePath returns the full path to the PID file.
func pidFilePath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, pidFileName), nil
}

// writePIDFile writes the given PID to ~/.aisync/aisync.pid.
func writePIDFile(pid int) error {
	path, err := pidFilePath()
	if err != nil {
		return err
	}
	return writePIDFileAt(path, pid)
}

// writePIDFileAt writes the given PID to the specified path.
func writePIDFileAt(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// readPIDFile reads the PID from ~/.aisync/aisync.pid.
// Returns 0 and an error if the file does not exist or is malformed.
func readPIDFile() (int, error) {
	path, err := pidFilePath()
	if err != nil {
		return 0, err
	}
	return readPIDFileAt(path)
}

// readPIDFileAt reads the PID from the specified path.
func readPIDFileAt(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("malformed PID file %s: %w", path, err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("invalid PID %d in %s", pid, path)
	}
	return pid, nil
}

// removePIDFile removes the PID file. It is not an error if the file
// does not exist.
func removePIDFile() error {
	path, err := pidFilePath()
	if err != nil {
		return err
	}
	return removePIDFileAt(path)
}

// removePIDFileAt removes the PID file at the specified path.
// It is not an error if the file does not exist.
func removePIDFileAt(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// daemonize re-executes the current binary with the daemon env var set,
// redirecting stdout/stderr to the log file. It returns the child PID.
func daemonize(logPath string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("resolve executable: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open log file: %w", err)
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), daemonEnvVar+"=1")
	// Detach from parent process group so child survives parent exit.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return 0, fmt.Errorf("start daemon: %w", err)
	}
	logFile.Close()

	return cmd.Process.Pid, nil
}

// stopDaemon reads the PID file and sends SIGTERM to the running daemon.
func stopDaemon() error {
	pid, err := readPIDFile()
	if err != nil {
		return fmt.Errorf("no running daemon found: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Process may already be gone — clean up PID file anyway.
		_ = removePIDFile()
		return fmt.Errorf("signal process %d: %w", pid, err)
	}

	return nil
}

// isProcessAlive checks whether a process with the given PID exists.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks existence without actually sending a signal.
	return proc.Signal(syscall.Signal(0)) == nil
}

// NewCmdServe creates the `aisync serve` command.
func NewCmdServe(f *cmdutil.Factory) *cobra.Command {
	var (
		addr    string
		webOnly bool
		daemon  bool
		stop    bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the aisync server (API + web dashboard)",
		Long: `Start a unified aisync server that serves both the JSON API
and the web dashboard on a single port.

By default both are enabled:
  /api/v1/*   → JSON API (capture, restore, stats, sync, etc.)
  /*          → Web dashboard (sessions, costs, branches)

Use --web-only to serve only the web dashboard (no API endpoints).
Use --daemon to run the server in the background as a daemon.
Use --stop to stop a running daemon.

The server listens on 127.0.0.1:8371 by default and shuts down
gracefully on SIGINT/SIGTERM.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if stop {
				if err := stopDaemon(); err != nil {
					return err
				}
				fmt.Fprintln(f.IOStreams.Out, "aisync daemon stopped")
				return nil
			}

			if daemon {
				// If we are the re-exec'd child, continue normally.
				if os.Getenv(daemonEnvVar) != "1" {
					return runDaemon(f)
				}
			}

			return runServe(f, addr, webOnly)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", defaultAddr, "Address to listen on (host:port)")
	cmd.Flags().BoolVar(&webOnly, "web-only", false, "Serve only the web dashboard (no API)")
	cmd.Flags().BoolVar(&daemon, "daemon", false, "Run the server in the background")
	cmd.Flags().BoolVar(&stop, "stop", false, "Stop a running daemon")

	return cmd
}

// runDaemon handles the parent side of daemonization: re-exec, verify,
// write PID file, and exit.
func runDaemon(f *cmdutil.Factory) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, logFileName)

	pid, err := daemonize(logPath)
	if err != nil {
		return err
	}

	// Give the child a moment to start, then verify it's alive.
	time.Sleep(1 * time.Second)
	if !isProcessAlive(pid) {
		return fmt.Errorf("daemon process %d exited immediately — check %s", pid, logPath)
	}

	if err := writePIDFile(pid); err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}

	fmt.Fprintf(f.IOStreams.Out, "aisync daemon started (pid %d, log %s)\n", pid, logPath)
	return nil
}

// runServe builds the unified server and starts listening.
func runServe(f *cmdutil.Factory, addr string, webOnly bool) error {
	isDaemonChild := os.Getenv(daemonEnvVar) == "1"

	// If we are the daemon child, write a PID file for ourselves.
	if isDaemonChild {
		if err := writePIDFile(os.Getpid()); err != nil {
			return fmt.Errorf("write PID file: %w", err)
		}
	}

	logger := log.New(os.Stderr, "[aisync] ", log.LstdFlags)
	mux := http.NewServeMux()

	// ── API routes ──
	if !webOnly {
		sessionSvc, err := f.SessionService()
		if err != nil {
			return fmt.Errorf("session service: %w", err)
		}
		syncSvc, _ := f.SyncService()

		analysisSvcAPI, _ := f.AnalysisService()

		// Create replay engine (optional — best-effort, nil if store unavailable).
		var replayEngine *replay.Engine
		if apiStore, storeErr := f.Store(); storeErr == nil {
			replayEngine = replay.NewEngine(replay.EngineConfig{
				Store: apiStore,
				Runners: []replay.Runner{
					replay.NewOpenCodeRunner(),
					replay.NewClaudeCodeRunner(),
				},
				Capturer: replay.NewProviderCapturer(apiStore, logger),
				Logger:   logger,
			})
		}

		// Create skill resolver (optional — best-effort, nil if LLM unavailable).
		var skillResolver skillresolver.ResolverServicer
		if analysisSvcAPI != nil {
			if appCfgAPI, cfgErr := f.Config(); cfgErr == nil {
				if baseAnalyzer, analyzerErr := llmfactory.NewAnalyzerFromConfig(appCfgAPI, ""); analyzerErr == nil {
					skillAnalyzer := llmanalyzer.New(llmanalyzer.Config{
						Analyzer: baseAnalyzer,
					})
					skillResolver = skillresolver.NewService(skillresolver.ServiceConfig{
						Sessions: sessionSvc,
						Analyses: analysisSvcAPI,
						Analyzer: skillAnalyzer,
					})
					logger.Println("Skill resolver enabled")
				} else {
					logger.Printf("skill resolver unavailable (LLM not configured): %v", analyzerErr)
				}
			}
		}

		// Create auth service (optional — enabled via server.auth.enabled).
		var authSvc auth.Servicer
		if appCfgAuth, cfgErr := f.Config(); cfgErr == nil && appCfgAuth.IsAuthEnabled() {
			if apiStore, storeErr := f.Store(); storeErr == nil {
				jwtSecret := appCfgAuth.GetJWTSecret()
				if jwtSecret == "" {
					// Auto-generate a secret for development (not persistent across restarts).
					jwtSecret = "aisync-dev-secret-not-for-production-use!!"
					logger.Println("WARNING: using auto-generated JWT secret — set server.auth.jwt_secret for production")
				}
				authSvc = auth.NewService(auth.ServiceConfig{
					Store:     apiStore,
					JWTSecret: jwtSecret,
					TokenTTL:  24 * time.Hour,
				})
				logger.Println("Authentication enabled")
			}
		}

		// Get error service (optional — best-effort, nil if unavailable).
		errorSvc, _ := f.ErrorService()

		// Get session event service (optional — best-effort, nil if unavailable).
		sessionEventSvc, _ := f.SessionEventService()

		apiSrv := api.New(api.Config{
			SessionService:      sessionSvc,
			SyncService:         syncSvc,
			AnalysisService:     analysisSvcAPI,
			ErrorService:        errorSvc,
			SessionEventService: sessionEventSvc,
			ReplayEngine:        replayEngine,
			SkillResolver:       skillResolver,
			AuthService:         authSvc,
			Addr:                addr, // not used for listen — just for internal reference
			Logger:              logger,
		})
		apiSrv.RegisterRoutes(mux)
		logger.Println("API endpoints enabled at /api/v1/*")
	}

	// ── Web dashboard routes ──
	sessionSvc, err := f.SessionService()
	if err != nil {
		return fmt.Errorf("session service: %w", err)
	}
	registrySvc, _ := f.RegistryService()
	analysisSvc, _ := f.AnalysisService()
	appCfg, _ := f.Config()
	store, _ := f.Store()
	webSessionEventSvc, _ := f.SessionEventService()

	webSrv, err := web.New(web.Config{
		SessionService:      sessionSvc,
		AnalysisService:     analysisSvc,
		RegistryService:     registrySvc,
		SessionEventService: webSessionEventSvc,
		Store:               store,
		AppConfig:           appCfg,
		Addr:                addr, // not used for listen
		Logger:              logger,
	})
	if err != nil {
		return fmt.Errorf("web dashboard: %w", err)
	}
	webSrv.RegisterRoutes(mux)
	logger.Println("Web dashboard enabled")

	// ── HTTP server lifecycle ──
	srv := &http.Server{
		Addr:              addr,
		Handler:           loggingMiddleware(logger, mux),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      10 * time.Minute, // analysis with local LLM can take several minutes
		IdleTimeout:       120 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	logger.Printf("listening on http://%s", ln.Addr())

	// ── LiteLLM Auto-Refresh (background) ──
	// If the LiteLLM price cache is stale (>7 days), refresh in background.
	go func() {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return
		}
		cacheDir := filepath.Join(homeDir, ".aisync")
		info := pricing.CacheInfo(cacheDir)
		if info.Stale || !info.Exists {
			logger.Printf("LiteLLM price cache is stale (age: %s), refreshing in background...", info.Age.Round(time.Hour))
			count, err := pricing.UpdateCache(pricing.LiteLLMCatalogConfig{CacheDir: cacheDir})
			if err != nil {
				logger.Printf("LiteLLM auto-refresh failed: %v", err)
			} else {
				logger.Printf("LiteLLM price cache refreshed (%d models)", count)
			}
		}
	}()

	// ── LLM Queue (serializes Ollama/LLM calls) ──
	llmQ := llmqueue.New(llmqueue.Config{
		MaxConcurrency: 1, // single GPU — one at a time
		QueueSize:      500,
		Logger:         logger,
	})
	logger.Println("LLM job queue started (concurrency=1)")

	// Expose queue stats via API.
	mux.HandleFunc("GET /api/v1/llm-queue/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llmQ.Stats())
	})

	// ── Scheduler (builds entries from config) ──
	var sched *scheduler.Scheduler
	if appCfg != nil {
		var entries []scheduler.Entry

		// Existing: analysis daily task (uses analysis.schedule config).
		if schedule := appCfg.GetAnalysisSchedule(); schedule != "" {
			analysisSvcSched, analysisErr := f.AnalysisService()
			storeSched, storeErr := f.Store()
			if analysisErr == nil && storeErr == nil {
				entries = append(entries, scheduler.Entry{
					Schedule: schedule,
					Task: scheduler.NewAnalyzeDailyTask(scheduler.AnalyzeDailyConfig{
						AnalysisService: analysisSvcSched,
						Store:           storeSched,
						Logger:          logger,
						ErrorThreshold:  appCfg.GetAnalysisErrorThreshold(),
						MinToolCalls:    appCfg.GetAnalysisMinToolCalls(),
					}),
				})
				logger.Printf("scheduled task: analyze_daily (%s)", schedule)
			}
		}

		// GC task — deletes sessions older than retention period.
		if appCfg.GetSchedulerGCEnabled() {
			entries = append(entries, scheduler.Entry{
				Schedule: appCfg.GetSchedulerGCCron(),
				Task: scheduler.NewGCTask(scheduler.GCTaskConfig{
					SessionService: sessionSvc,
					Logger:         logger,
					RetentionDays:  appCfg.GetSchedulerGCRetentionDays(),
				}),
			})
			logger.Printf("scheduled task: gc (%s, retention=%dd)",
				appCfg.GetSchedulerGCCron(), appCfg.GetSchedulerGCRetentionDays())
		}

		// Capture-all task — periodically captures from all providers.
		if appCfg.GetSchedulerCaptureAllEnabled() {
			entries = append(entries, scheduler.Entry{
				Schedule: appCfg.GetSchedulerCaptureAllCron(),
				Task: scheduler.NewCaptureAllTask(scheduler.CaptureAllTaskConfig{
					SessionService: sessionSvc,
					Logger:         logger,
				}),
			})
			logger.Printf("scheduled task: capture_all (%s)", appCfg.GetSchedulerCaptureAllCron())
		}

		// Stats report task — warms the stats cache periodically.
		if appCfg.GetSchedulerStatsReportEnabled() {
			entries = append(entries, scheduler.Entry{
				Schedule: appCfg.GetSchedulerStatsReportCron(),
				Task: scheduler.NewStatsReportTask(scheduler.StatsReportTaskConfig{
					SessionService: sessionSvc,
					Logger:         logger,
				}),
			})
			logger.Printf("scheduled task: stats_report (%s)", appCfg.GetSchedulerStatsReportCron())
		}

		// Token usage compute task — runs nightly at 3:30 AM.
		entries = append(entries, scheduler.Entry{
			Schedule: "30 3 * * *",
			Task: scheduler.NewTokenUsageTask(scheduler.TokenUsageTaskConfig{
				SessionService: sessionSvc,
				Logger:         logger,
			}),
		})
		logger.Println("scheduled task: token_usage_compute (30 3 * * *)")

		// Error reclassification task — reclassifies unknown errors via LLM.
		if schedule := appCfg.GetErrorsLLMSchedule(); schedule != "" {
			errorSvcSched, errSvcErr := f.ErrorService()
			storeSched2, storeErr2 := f.Store()
			if errSvcErr == nil && storeErr2 == nil {
				entries = append(entries, scheduler.Entry{
					Schedule: schedule,
					Task: scheduler.NewReclassifyTask(scheduler.ReclassifyConfig{
						ErrorService: errorSvcSched,
						Store:        storeSched2,
						Logger:       logger,
					}),
				})
				logger.Printf("scheduled task: reclassify_errors (%s)", schedule)
			}
		}

		// Create and start scheduler if any entries are configured.
		if len(entries) > 0 {
			var schedErr error
			sched, schedErr = scheduler.New(scheduler.Config{
				Entries: entries,
				Logger:  logger,
			})
			if schedErr != nil {
				logger.Printf("scheduler setup failed: %v (continuing without scheduler)", schedErr)
			} else {
				sched.Start()
				logger.Printf("scheduler started with %d task(s)", len(entries))
			}
		}
	}

	// ── Scheduler status endpoint ──
	if sched != nil {
		mux.HandleFunc("GET /api/v1/scheduler/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			results := sched.Status()
			data, _ := json.Marshal(results)
			w.Write(data)
		})
	}

	// ── Manual reclassification trigger ──
	{
		errorSvcTrigger, errSvcErr := f.ErrorService()
		storeTrigger, storeErr := f.Store()
		if errSvcErr == nil && storeErr == nil {
			mux.HandleFunc("POST /api/v1/errors/reclassify", func(w http.ResponseWriter, r *http.Request) {
				task := scheduler.NewReclassifyTask(scheduler.ReclassifyConfig{
					ErrorService: errorSvcTrigger,
					Store:        storeTrigger,
					Logger:       logger,
				})
				if err := task.Run(r.Context()); err != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					data, _ := json.Marshal(map[string]string{"error": err.Error()})
					w.Write(data)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				data, _ := json.Marshal(map[string]string{"status": "ok", "message": "reclassification complete"})
				w.Write(data)
			})
		}
	}

	// ── Manual backfill trigger ──
	mux.HandleFunc("POST /api/v1/backfill/remote-url", func(w http.ResponseWriter, r *http.Request) {
		result, err := sessionSvc.BackfillRemoteURLs(r.Context())
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			data, _ := json.Marshal(map[string]string{"error": err.Error()})
			w.Write(data)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(result)
		w.Write(data)
	})

	// ── Manual fork detection trigger ──
	mux.HandleFunc("POST /api/v1/backfill/forks", func(w http.ResponseWriter, r *http.Request) {
		result, err := sessionSvc.DetectForksBatch(r.Context())
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			data, _ := json.Marshal(map[string]string{"error": err.Error()})
			w.Write(data)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(result)
		w.Write(data)
	})

	// Serve in background.
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	// Wait for signal or error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
	case sig := <-sigCh:
		logger.Printf("received %v, shutting down...", sig)

		// Stop scheduler first (graceful — waits for running tasks).
		if sched != nil {
			sched.Stop()
		}

		// Drain LLM queue (wait for in-progress jobs, 30s max).
		logger.Println("draining LLM queue...")
		llmQ.Drain(30 * time.Second)
		llmQ.Stop()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if shutdownErr := srv.Shutdown(ctx); shutdownErr != nil {
			return fmt.Errorf("shutdown: %w", shutdownErr)
		}
	}

	// Clean up PID file on exit (daemon or otherwise).
	if isDaemonChild {
		_ = removePIDFile()
	}

	return nil
}

// ── Middleware ──

// loggingMiddleware logs each request with method, path, status, and duration.
func loggingMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		logger.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

// statusWriter captures the HTTP status code for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}
