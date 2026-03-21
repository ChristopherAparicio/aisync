package agentscmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// ShowOptions holds all inputs for the agents show command.
type ShowOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	Name        string
	ProjectPath string
	JSON        bool
}

// NewCmdShow creates the `aisync agents show <name>` command.
func NewCmdShow(f *cmdutil.Factory) *cobra.Command {
	opts := &ShowOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details of a specific capability",
		Long:  "Displays detailed information about a specific agent, command, skill, tool, or plugin by name.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			return runShow(opts)
		},
	}

	cmd.Flags().StringVar(&opts.ProjectPath, "project", "", "Project path (defaults to current directory)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output as JSON")

	return cmd
}

func runShow(opts *ShowOptions) error {
	out := opts.IO.Out

	// Default to current directory
	projectPath := opts.ProjectPath
	if projectPath == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("could not determine current directory: %w", err)
		}
		projectPath = wd
	}

	svc, err := opts.Factory.RegistryService()
	if err != nil {
		return fmt.Errorf("could not initialize registry service: %w", err)
	}

	project, err := svc.ScanProject(projectPath)
	if err != nil {
		return fmt.Errorf("could not scan project: %w", err)
	}

	cap := project.FindCapability(opts.Name)
	if cap == nil {
		return fmt.Errorf("capability %q not found in project %s", opts.Name, project.Name)
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(cap)
	}

	// Detail output
	fmt.Fprintf(out, "Name:        %s\n", cap.Name)
	fmt.Fprintf(out, "Kind:        %s\n", cap.Kind)
	fmt.Fprintf(out, "Scope:       %s\n", cap.Scope)
	if cap.Description != "" {
		fmt.Fprintf(out, "Description: %s\n", cap.Description)
	}
	if cap.FilePath != "" {
		fmt.Fprintf(out, "File:        %s\n", cap.RelPath(projectPath))
	}

	if len(cap.Handoffs) > 0 {
		fmt.Fprintln(out, "\nHandoffs:")
		for _, h := range cap.Handoffs {
			arrow := "→"
			if h.Send {
				arrow = "⇒ (auto-send)"
			}
			fmt.Fprintf(out, "  %s %s %s\n", h.Label, arrow, h.Target)
			if h.Prompt != "" {
				prompt := h.Prompt
				if len(prompt) > 80 {
					prompt = prompt[:80] + "..."
				}
				fmt.Fprintf(out, "    Prompt: %s\n", prompt)
			}
		}
	}

	if len(cap.MCPTools) > 0 {
		fmt.Fprintln(out, "\nMCP Tools:")
		for _, t := range cap.MCPTools {
			ref := t.Tool
			if t.Server != "" {
				ref = t.Server + ":" + t.Tool
			}
			fmt.Fprintf(out, "  %s\n", ref)
		}
	}

	if len(cap.ExposedTools) > 0 {
		fmt.Fprintln(out, "\nExposed Tools:")
		for _, t := range cap.ExposedTools {
			fmt.Fprintf(out, "  %s\n", t)
		}
	}

	return nil
}
