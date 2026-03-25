// Package capture implements the `aisync capture` CLI command.
package capture

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the capture command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	ProviderFlag string
	ModeFlag     string
	Message      string
	BranchFlag   string // explicit branch override (e.g. from plugin running in a worktree)
	Auto         bool
	Summarize    bool
	All          bool   // capture all sessions for the project
	SessionID    string // capture a specific session by provider-native ID
}

// NewCmdCapture creates the `aisync capture` command.
func NewCmdCapture(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Capture the active AI session",
		Long:  "Captures the currently active AI session and stores it linked to the current branch.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCapture(opts)
		},
	}

	cmd.Flags().StringVar(&opts.ProviderFlag, "provider", "", "Force a specific provider (claude-code, opencode)")
	cmd.Flags().StringVar(&opts.ModeFlag, "mode", "", "Storage mode: full, compact, summary")
	cmd.Flags().StringVar(&opts.Message, "message", "", "Manual summary message")
	cmd.Flags().StringVar(&opts.BranchFlag, "branch", "", "Explicit git branch (useful when capturing from a worktree)")
	cmd.Flags().BoolVar(&opts.Auto, "auto", false, "Auto mode (used by git hooks, silent)")
	cmd.Flags().BoolVar(&opts.Summarize, "summarize", false, "AI-summarize the session after capture")
	cmd.Flags().BoolVar(&opts.All, "all", false, "Capture all sessions for the project (requires --provider)")
	cmd.Flags().StringVar(&opts.SessionID, "session-id", "", "Capture a specific session by provider-native ID")

	return cmd
}

func runCapture(opts *Options) error {
	// Git info
	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}

	// Branch: use explicit --branch flag if provided, otherwise detect from git.
	// The --branch flag is essential for worktree-based captures where the CWD
	// is not the project's git repo (e.g. OpenCode worktrees under ~/.local/share/opencode/worktree/).
	branch := opts.BranchFlag
	if branch == "" {
		var branchErr error
		branch, branchErr = gitClient.CurrentBranch()
		if branchErr != nil {
			return fmt.Errorf("could not determine current branch: %w", branchErr)
		}
	}

	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return fmt.Errorf("could not determine repository root: %w", err)
	}

	// Resolve storage mode
	cfg, cfgErr := opts.Factory.Config()
	mode := session.StorageModeCompact
	if cfgErr == nil {
		mode = cfg.GetStorageMode()
	}
	if opts.ModeFlag != "" {
		parsed, parseErr := session.ParseStorageMode(opts.ModeFlag)
		if parseErr != nil {
			return parseErr
		}
		mode = parsed
	}

	// Resolve provider
	var providerName session.ProviderName
	if opts.ProviderFlag != "" {
		parsed, parseErr := session.ParseProviderName(opts.ProviderFlag)
		if parseErr != nil {
			return parseErr
		}
		providerName = parsed
	}

	// Validate flag combinations
	if opts.All && opts.SessionID != "" {
		return fmt.Errorf("--all and --session-id are mutually exclusive")
	}
	if opts.All && providerName == "" {
		return fmt.Errorf("--all requires --provider (e.g. --provider opencode)")
	}

	// Get service
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	// Determine whether to summarize (flag overrides config)
	shouldSummarize := opts.Summarize
	if !shouldSummarize && cfgErr == nil {
		shouldSummarize = cfg.IsSummarizeEnabled()
	}

	// Determine model for summarization
	var summarizeModel string
	if cfgErr == nil {
		summarizeModel = cfg.GetSummarizeModel()
	}

	// For --all captures, don't force the current branch on every session.
	// OpenCode sessions don't track branches — forcing the current branch
	// would incorrectly assign "main" to sessions worked on other branches.
	captureBranch := branch
	if opts.All {
		captureBranch = "" // each session keeps whatever branch the provider reports
	}

	baseReq := service.CaptureRequest{
		ProjectPath:  topLevel,
		Branch:       captureBranch,
		Mode:         mode,
		ProviderName: providerName,
		Message:      opts.Message,
		Summarize:    shouldSummarize,
		Model:        summarizeModel,
	}

	// Dispatch: --all, --session-id, or default (most recent)
	switch {
	case opts.All:
		return runCaptureAll(opts, svc, baseReq)
	case opts.SessionID != "":
		return runCaptureByID(opts, svc, baseReq)
	default:
		return runCaptureSingle(opts, svc, baseReq)
	}
}

func runCaptureSingle(opts *Options, svc service.SessionServicer, req service.CaptureRequest) error {
	out := opts.IO.Out

	result, err := svc.Capture(req)
	if err != nil {
		if opts.Auto {
			return nil
		}
		return err
	}

	if !opts.Auto {
		printResult(opts, out, result)
	}
	return nil
}

func runCaptureAll(opts *Options, svc service.SessionServicer, req service.CaptureRequest) error {
	out := opts.IO.Out

	results, err := svc.CaptureAll(req)
	if err != nil {
		if opts.Auto {
			return nil
		}
		return err
	}

	if !opts.Auto {
		var skipped int
		for _, r := range results {
			if r.Skipped {
				skipped++
			}
		}
		fmt.Fprintf(out, "Captured %d sessions", len(results)-skipped)
		if skipped > 0 {
			fmt.Fprintf(out, " (%d unchanged, skipped)", skipped)
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out)

		for i, r := range results {
			if r.Skipped {
				continue
			}
			fmt.Fprintf(out, "[%d/%d] ", i+1, len(results))
			printResult(opts, out, r)
			fmt.Fprintln(out)
		}
	}
	return nil
}

func runCaptureByID(opts *Options, svc service.SessionServicer, req service.CaptureRequest) error {
	out := opts.IO.Out

	result, err := svc.CaptureByID(req, session.ID(opts.SessionID))
	if err != nil {
		if opts.Auto {
			return nil
		}
		return err
	}

	if !opts.Auto {
		printResult(opts, out, result)
	}
	return nil
}

func printResult(opts *Options, out io.Writer, result *service.CaptureResult) {
	if result.Skipped {
		fmt.Fprintf(out, "Skipped session %s (unchanged)\n", result.Session.ID)
		return
	}
	fmt.Fprintf(out, "Captured session %s\n", result.Session.ID)
	fmt.Fprintf(out, "  Provider: %s\n", result.Provider)
	fmt.Fprintf(out, "  Branch:   %s\n", result.Session.Branch)
	fmt.Fprintf(out, "  Mode:     %s\n", result.Session.StorageMode)
	fmt.Fprintf(out, "  Messages: %d\n", len(result.Session.Messages))
	if result.Session.Summary != "" {
		fmt.Fprintf(out, "  Summary:  %s\n", result.Session.Summary)
	}
	if result.Summarized {
		fmt.Fprintf(out, "  AI:       summarized\n")
	}
	if result.SecretsFound > 0 {
		scanner := opts.Factory.Scanner()
		fmt.Fprintf(out, "  Secrets:  %d detected", result.SecretsFound)
		if scanner != nil {
			switch scanner.Mode() {
			case session.SecretModeMask:
				fmt.Fprint(out, " (masked)")
			case session.SecretModeWarn:
				fmt.Fprint(out, " (warning: stored as-is)")
			}
		}
		fmt.Fprintln(out)
	}
}
