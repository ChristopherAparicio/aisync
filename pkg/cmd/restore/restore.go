// Package restore implements the `aisync restore` CLI command.
package restore

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	restoresvc "github.com/ChristopherAparicio/aisync/internal/restore"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the restore command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionFlag  string
	ProviderFlag string
	AgentFlag    string
	AsContext    bool
	PRFlag       int
}

// NewCmdRestore creates the `aisync restore` command.
func NewCmdRestore(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a captured AI session",
		Long:  "Restores a previously captured session into an AI tool, or generates a CONTEXT.md file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestore(opts)
		},
	}

	cmd.Flags().StringVar(&opts.SessionFlag, "session", "", "Restore a specific session by ID")
	cmd.Flags().StringVar(&opts.ProviderFlag, "provider", "", "Force restore into a specific provider")
	cmd.Flags().StringVar(&opts.AgentFlag, "agent", "", "Target agent name (e.g., for OpenCode multi-agent)")
	cmd.Flags().BoolVar(&opts.AsContext, "as-context", false, "Generate CONTEXT.md instead of native import")
	cmd.Flags().IntVar(&opts.PRFlag, "pr", 0, "Restore session linked to this PR number")

	return cmd
}

func runRestore(opts *Options) error {
	out := opts.IO.Out

	// Git info
	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}

	branch, err := gitClient.CurrentBranch()
	if err != nil {
		return fmt.Errorf("could not determine current branch: %w", err)
	}

	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return fmt.Errorf("could not determine repository root: %w", err)
	}

	// Parse session ID if provided
	var sessionID domain.SessionID
	if opts.SessionFlag != "" {
		parsed, parseErr := domain.ParseSessionID(opts.SessionFlag)
		if parseErr != nil {
			return parseErr
		}
		sessionID = parsed
	}

	// If --pr is set, look up the session linked to that PR
	if opts.PRFlag > 0 && sessionID == "" {
		store, storeErr := opts.Factory.Store()
		if storeErr != nil {
			return fmt.Errorf("opening store: %w", storeErr)
		}
		summaries, lookupErr := store.GetByLink(domain.LinkPR, strconv.Itoa(opts.PRFlag))
		if lookupErr != nil {
			return fmt.Errorf("no session linked to PR #%d: %w", opts.PRFlag, lookupErr)
		}
		if len(summaries) == 0 {
			return fmt.Errorf("no session linked to PR #%d", opts.PRFlag)
		}
		sessionID = summaries[0].ID
		fmt.Fprintf(out, "Found session %s linked to PR #%d\n", sessionID, opts.PRFlag)
	}

	// Parse provider if provided
	var providerName domain.ProviderName
	if opts.ProviderFlag != "" {
		parsed, parseErr := domain.ParseProviderName(opts.ProviderFlag)
		if parseErr != nil {
			return parseErr
		}
		providerName = parsed
	}

	// Get dependencies
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	registry := opts.Factory.Registry()
	conv := converter.New()
	svc := restoresvc.NewServiceWithConverter(registry, store, conv)

	// Restore
	result, err := svc.Restore(restoresvc.Request{
		ProjectPath:  topLevel,
		Branch:       branch,
		SessionID:    sessionID,
		ProviderName: providerName,
		Agent:        opts.AgentFlag,
		AsContext:    opts.AsContext,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Restored session %s\n", result.Session.ID)
	fmt.Fprintf(out, "  Provider: %s\n", result.Session.Provider)

	switch result.Method {
	case "native":
		fmt.Fprintln(out, "  Method:   native import")
		fmt.Fprintln(out, "  Launch your AI agent to continue with this context.")
	case "converted":
		fmt.Fprintln(out, "  Method:   cross-provider conversion")
		fmt.Fprintln(out, "  Session was converted to the target provider format.")
		fmt.Fprintln(out, "  Launch your AI agent to continue with this context.")
	case "context":
		fmt.Fprintf(out, "  Method:   CONTEXT.md\n")
		fmt.Fprintf(out, "  File:     %s\n", result.ContextPath)
		fmt.Fprintln(out, "  Open CONTEXT.md or paste it into your AI agent to resume.")
	}

	return nil
}
