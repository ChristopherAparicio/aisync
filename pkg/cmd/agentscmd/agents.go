// Package agentscmd implements the `aisync agents` CLI command group.
// It provides commands to list, inspect, and visualize agent capabilities
// discovered from provider config files across projects.
package agentscmd

import (
	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// NewCmdAgents creates the `aisync agents` command group.
func NewCmdAgents(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agents",
		Short:   "Discover and inspect agent capabilities",
		Long:    "Discovers AI agent capabilities (agents, commands, skills, tools, plugins, MCP servers) across projects by scanning provider config files.",
		Aliases: []string{"agent", "registry"},
	}

	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdTree(f))
	cmd.AddCommand(NewCmdShow(f))

	return cmd
}
