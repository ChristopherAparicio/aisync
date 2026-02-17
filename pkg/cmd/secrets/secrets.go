// Package secrets implements the `aisync secrets` CLI commands.
package secrets

import (
	"fmt"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	secretslib "github.com/ChristopherAparicio/aisync/internal/secrets"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// ScanOptions holds all inputs for the secrets scan command.
type ScanOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionFlag string
}

// NewCmdSecrets creates the `aisync secrets` command group.
func NewCmdSecrets(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Secret detection utilities",
		Long:  "Commands for detecting and managing secrets in captured sessions.",
	}

	cmd.AddCommand(newCmdScan(f))
	cmd.AddCommand(newCmdAddPattern(f))

	return cmd
}

// AddPatternOptions holds inputs for the add-pattern command.
type AddPatternOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

func newCmdAddPattern(f *cmdutil.Factory) *cobra.Command {
	opts := &AddPatternOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "add-pattern <name> <regex>",
		Short: "Add a custom secret detection pattern",
		Long:  "Adds a custom regex pattern for secret detection. The pattern is saved to the config file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAddPattern(opts, args[0], args[1])
		},
	}

	return cmd
}

func runAddPattern(opts *AddPatternOptions, name string, pattern string) error {
	out := opts.IO.Out

	// Validate the regex compiles
	if _, compileErr := regexp.Compile(pattern); compileErr != nil {
		return fmt.Errorf("invalid regex %q: %w", pattern, compileErr)
	}

	cfg, cfgErr := opts.Factory.Config()
	if cfgErr != nil {
		return fmt.Errorf("loading config: %w", cfgErr)
	}

	// Config returns domain.Config interface; we need the concrete type
	// to call AddCustomPattern. Use the Set method with a custom key approach.
	// Store as "NAME REGEX" in the custom_patterns list.
	entry := name + " " + pattern

	// Use direct Set approach — add to custom_patterns via config
	if setErr := cfg.Set("secrets.custom_patterns.add", entry); setErr != nil {
		return fmt.Errorf("adding pattern: %w", setErr)
	}

	if saveErr := cfg.Save(); saveErr != nil {
		return fmt.Errorf("saving config: %w", saveErr)
	}

	fmt.Fprintf(out, "Added custom pattern: %s %s\n", name, pattern)
	return nil
}

func newCmdScan(f *cmdutil.Factory) *cobra.Command {
	opts := &ScanOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan stored sessions for secrets",
		Long:  "Scans captured sessions for secrets like API keys, tokens, and credentials.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScan(opts)
		},
	}

	cmd.Flags().StringVar(&opts.SessionFlag, "session", "", "Scan a specific session by ID")

	return cmd
}

func runScan(opts *ScanOptions) error {
	out := opts.IO.Out

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	// Get secrets mode from config (defaults to mask)
	cfg, cfgErr := opts.Factory.Config()
	mode := domain.SecretModeMask
	if cfgErr == nil {
		mode = cfg.GetSecretsMode()
	}

	scanner := secretslib.NewScanner(mode, nil)

	// Determine which sessions to scan
	var sessions []*domain.Session
	if opts.SessionFlag != "" {
		// Scan a specific session
		sessionID, parseErr := domain.ParseSessionID(opts.SessionFlag)
		if parseErr != nil {
			return parseErr
		}
		session, getErr := store.Get(sessionID)
		if getErr != nil {
			return fmt.Errorf("session %q not found: %w", opts.SessionFlag, getErr)
		}
		sessions = append(sessions, session)
	} else {
		// Scan all sessions
		summaries, listErr := store.List(domain.ListOptions{All: true})
		if listErr != nil {
			return fmt.Errorf("listing sessions: %w", listErr)
		}
		if len(summaries) == 0 {
			fmt.Fprintln(out, "No sessions found.")
			return nil
		}
		for _, s := range summaries {
			session, getErr := store.Get(s.ID)
			if getErr != nil {
				continue // skip sessions that can't be loaded
			}
			sessions = append(sessions, session)
		}
	}

	fmt.Fprintf(out, "Scanning %d session(s)...\n", len(sessions))

	var totalSecrets int
	for _, session := range sessions {
		matches := scanner.ScanSession(session)
		if len(matches) > 0 {
			totalSecrets += len(matches)
			fmt.Fprintf(out, "!! Session %s: %d secret(s) found\n", session.ID, len(matches))
			fmt.Fprintf(out, "   %s\n", secretslib.FormatMatches(matches))
		} else {
			fmt.Fprintf(out, "ok Session %s: clean\n", session.ID)
		}
	}

	fmt.Fprintln(out)
	if totalSecrets > 0 {
		fmt.Fprintf(out, "Found %d secret(s) across %d session(s).\n", totalSecrets, len(sessions))
		fmt.Fprintln(out, "Run 'aisync capture' with secrets.mode=mask to redact secrets automatically.")
	} else {
		fmt.Fprintln(out, "All sessions clean. No secrets detected.")
	}

	return nil
}
