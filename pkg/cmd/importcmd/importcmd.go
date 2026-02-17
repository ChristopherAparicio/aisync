// Package importcmd implements the `aisync import` CLI command.
// The package is named importcmd because "import" is a Go reserved word.
package importcmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the import command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	FormatFlag string
	IntoFlag   string
}

// NewCmdImport creates the `aisync import` command.
func NewCmdImport(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import a session from a file",
		Long:  "Imports a session from a file. Auto-detects the format or use --format to specify.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(opts, args[0])
		},
	}

	cmd.Flags().StringVar(&opts.FormatFlag, "format", "", "Source format: aisync, claude, opencode (default: auto-detect)")
	cmd.Flags().StringVar(&opts.IntoFlag, "into", "aisync", "Target: aisync (store only), claude-code, opencode")

	return cmd
}

func runImport(opts *Options, filePath string) error {
	out := opts.IO.Out

	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	if len(data) == 0 {
		return fmt.Errorf("file %s is empty", filePath)
	}

	// Determine source format
	var sourceFormat domain.ProviderName
	if opts.FormatFlag != "" {
		switch opts.FormatFlag {
		case "aisync":
			sourceFormat = "" // unified
		case "claude", "claude-code":
			sourceFormat = domain.ProviderClaudeCode
		case "opencode":
			sourceFormat = domain.ProviderOpenCode
		default:
			return fmt.Errorf("unknown format %q: valid values are [aisync, claude, opencode]", opts.FormatFlag)
		}
	} else {
		sourceFormat = converter.DetectFormat(data)
	}

	// Parse into unified session
	var session *domain.Session
	conv := converter.New()

	if sourceFormat == "" {
		// Unified aisync JSON
		session = &domain.Session{}
		if jsonErr := json.Unmarshal(data, session); jsonErr != nil {
			return fmt.Errorf("parsing aisync JSON: %w", jsonErr)
		}
		fmt.Fprintf(out, "Detected format: aisync (unified JSON)\n")
	} else {
		session, err = conv.FromNative(data, sourceFormat)
		if err != nil {
			return fmt.Errorf("parsing %s format: %w", sourceFormat, err)
		}
		fmt.Fprintf(out, "Detected format: %s\n", sourceFormat)
	}

	// Scan for secrets if scanner is available
	scanner := opts.Factory.Scanner()
	if scanner != nil && scanner.Mode() == domain.SecretModeMask {
		matches := scanner.Scan(sessionText(session))
		if len(matches) > 0 {
			for i := range session.Messages {
				session.Messages[i].Content = scanner.Mask(session.Messages[i].Content)
				for j := range session.Messages[i].ToolCalls {
					session.Messages[i].ToolCalls[j].Output = scanner.Mask(session.Messages[i].ToolCalls[j].Output)
				}
			}
			fmt.Fprintf(out, "Masked %d secret(s) before storing.\n", len(matches))
		}
	}

	// Determine target
	switch opts.IntoFlag {
	case "aisync", "":
		// Store in local SQLite only
		store, storeErr := opts.Factory.Store()
		if storeErr != nil {
			return fmt.Errorf("opening store: %w", storeErr)
		}
		if session.ID == "" {
			session.ID = domain.NewSessionID()
		}
		if saveErr := store.Save(session); saveErr != nil {
			return fmt.Errorf("storing session: %w", saveErr)
		}
		fmt.Fprintf(out, "Stored session %s locally.\n", session.ID)
		fmt.Fprintf(out, "Use 'aisync restore --session %s' to load into your agent.\n", session.ID)

	case "claude", "claude-code":
		// Convert to Claude and inject via provider
		registry := opts.Factory.Registry()
		prov, provErr := registry.Get(domain.ProviderClaudeCode)
		if provErr != nil {
			return fmt.Errorf("claude-code provider: %w", provErr)
		}
		if !prov.CanImport() {
			return fmt.Errorf("claude-code provider does not support import")
		}
		if importErr := prov.Import(session); importErr != nil {
			return fmt.Errorf("importing into claude-code: %w", importErr)
		}
		fmt.Fprintf(out, "Imported session %s into claude-code.\n", session.ID)

	case "opencode":
		// Convert to OpenCode and inject via provider
		registry := opts.Factory.Registry()
		prov, provErr := registry.Get(domain.ProviderOpenCode)
		if provErr != nil {
			return fmt.Errorf("opencode provider: %w", provErr)
		}
		if !prov.CanImport() {
			return fmt.Errorf("opencode provider does not support import")
		}
		if importErr := prov.Import(session); importErr != nil {
			return fmt.Errorf("importing into opencode: %w", importErr)
		}
		fmt.Fprintf(out, "Imported session %s into opencode.\n", session.ID)

	default:
		return fmt.Errorf("unknown target %q: valid values are [aisync, claude-code, opencode]", opts.IntoFlag)
	}

	return nil
}

// sessionText concatenates all scannable text from a session.
func sessionText(session *domain.Session) string {
	var text string
	for _, msg := range session.Messages {
		text += msg.Content + "\n"
		for _, tc := range msg.ToolCalls {
			text += tc.Output + "\n"
		}
	}
	return text
}
