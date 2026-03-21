package agentscmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// TreeOptions holds all inputs for the agents tree command.
type TreeOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	ProjectPath string
	JSON        bool
}

// NewCmdTree creates the `aisync agents tree` command.
func NewCmdTree(f *cmdutil.Factory) *cobra.Command {
	opts := &TreeOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "tree",
		Short: "Show capability tree for a project",
		Long:  "Displays all capabilities (global + project) organized as a tree, grouped by kind and scope.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTree(opts)
		},
	}

	cmd.Flags().StringVar(&opts.ProjectPath, "project", "", "Project path (defaults to current directory)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output as JSON")

	return cmd
}

func runTree(opts *TreeOptions) error {
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

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(project)
	}

	// Tree output
	fmt.Fprintf(out, "%s %s\n", project.Name, scopeLabel(project.RootPath))

	// MCP servers
	if len(project.MCPServers) > 0 {
		fmt.Fprintf(out, "  MCP Servers (%d)\n", len(project.MCPServers))
		for i, srv := range project.MCPServers {
			connector := "├──"
			if i == len(project.MCPServers)-1 {
				connector = "└──"
			}
			status := "●"
			if !srv.Enabled {
				status = "○"
			}
			fmt.Fprintf(out, "  %s %s %s [%s] (%s)\n", connector, status, srv.Name, srv.Type, srv.Scope)
		}
	}

	// Group capabilities by kind
	byKind := groupByKind(project.Capabilities)
	kinds := []registry.CapabilityKind{
		registry.KindAgent,
		registry.KindCommand,
		registry.KindSkill,
		registry.KindTool,
		registry.KindPlugin,
	}

	for _, kind := range kinds {
		caps, ok := byKind[kind]
		if !ok {
			continue
		}

		kindLabel := capitalize(string(kind)) + "s"
		fmt.Fprintf(out, "  %s (%d)\n", kindLabel, len(caps))

		for i, cap := range caps {
			connector := "├──"
			if i == len(caps)-1 {
				connector = "└──"
			}

			desc := cap.Description
			if len(desc) > 60 {
				desc = desc[:60] + "..."
			}

			scopeTag := fmt.Sprintf("(%s)", cap.Scope)
			if desc != "" {
				fmt.Fprintf(out, "  %s %s %s — %s\n", connector, cap.Name, scopeTag, desc)
			} else {
				fmt.Fprintf(out, "  %s %s %s\n", connector, cap.Name, scopeTag)
			}

			// Show handoffs
			for j, h := range cap.Handoffs {
				hConnector := "│   ├──"
				if j == len(cap.Handoffs)-1 && len(cap.MCPTools) == 0 && len(cap.ExposedTools) == 0 {
					hConnector = "│   └──"
				}
				if i == len(caps)-1 {
					hConnector = strings.Replace(hConnector, "│", " ", 1)
				}
				arrow := "→"
				if h.Send {
					arrow = "⇒"
				}
				fmt.Fprintf(out, "  %s %s %s %s\n", hConnector, h.Label, arrow, h.Target)
			}

			// Show MCP tools
			for j, tool := range cap.MCPTools {
				tConnector := "│   ├──"
				if j == len(cap.MCPTools)-1 && len(cap.ExposedTools) == 0 {
					tConnector = "│   └──"
				}
				if i == len(caps)-1 {
					tConnector = strings.Replace(tConnector, "│", " ", 1)
				}
				ref := tool.Tool
				if tool.Server != "" {
					ref = tool.Server + ":" + tool.Tool
				}
				fmt.Fprintf(out, "  %s tool: %s\n", tConnector, ref)
			}

			// Show exposed tools
			for j, t := range cap.ExposedTools {
				eConnector := "│   ├──"
				if j == len(cap.ExposedTools)-1 {
					eConnector = "│   └──"
				}
				if i == len(caps)-1 {
					eConnector = strings.Replace(eConnector, "│", " ", 1)
				}
				fmt.Fprintf(out, "  %s exposes: %s\n", eConnector, t)
			}
		}
	}

	// Stats
	stats := project.CapabilityStats()
	if len(stats) > 0 {
		parts := make([]string, len(stats))
		for i, s := range stats {
			parts[i] = fmt.Sprintf("%d %ss", s.Count, s.Kind)
		}
		fmt.Fprintf(out, "\n  Total: %s\n", strings.Join(parts, ", "))
	}

	return nil
}

// groupByKind groups capabilities by their kind.
func groupByKind(caps []registry.Capability) map[registry.CapabilityKind][]registry.Capability {
	m := make(map[registry.CapabilityKind][]registry.Capability)
	for _, c := range caps {
		m[c.Kind] = append(m[c.Kind], c)
	}
	return m
}

// capitalize returns s with the first letter uppercased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// scopeLabel returns a display string for a project path.
func scopeLabel(path string) string {
	return fmt.Sprintf("(%s)", path)
}
