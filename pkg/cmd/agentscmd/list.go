package agentscmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// ListOptions holds all inputs for the agents list command.
type ListOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	JSON bool
}

// NewCmdList creates the `aisync agents list` command.
// This is also the default action when `aisync agents` is run without a subcommand.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all projects with capability counts",
		Long:  "Discovers all registered projects and shows a summary of capabilities (agents, commands, skills, tools, plugins, MCP servers) for each.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output as JSON")

	return cmd
}

func runList(opts *ListOptions) error {
	out := opts.IO.Out

	svc, err := opts.Factory.RegistryService()
	if err != nil {
		return fmt.Errorf("could not initialize registry service: %w", err)
	}

	projects, err := svc.ListProjects()
	if err != nil {
		return fmt.Errorf("could not discover projects: %w", err)
	}

	if len(projects) == 0 {
		fmt.Fprintln(out, "No projects found.")
		fmt.Fprintln(out, "Hint: aisync discovers projects from OpenCode's project registry.")
		return nil
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(projects)
	}

	// Table output
	fmt.Fprintf(out, "%-30s %s\n", "PROJECT", "CAPABILITIES")
	fmt.Fprintf(out, "%-30s %s\n", "-------", "------------")

	for _, p := range projects {
		stats := p.CapabilityStats()
		summary := ""
		for i, stat := range stats {
			if i > 0 {
				summary += ", "
			}
			summary += fmt.Sprintf("%d %ss", stat.Count, stat.Kind)
		}
		if summary == "" {
			summary = "(none)"
		}

		name := p.Name
		if len(name) > 28 {
			name = name[:28] + ".."
		}
		fmt.Fprintf(out, "%-30s %s\n", name, summary)
	}

	fmt.Fprintf(out, "\n%d project(s) found.\n", len(projects))

	return nil
}
