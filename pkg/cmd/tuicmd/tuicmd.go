// Package tuicmd implements the `aisync tui` CLI command.
// It launches an interactive terminal user interface for browsing sessions.
package tuicmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/tui"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// NewCmdTUI creates the `aisync tui` command.
func NewCmdTUI(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch interactive terminal UI",
		Long:  "Opens an interactive terminal interface for browsing, inspecting, and managing AI sessions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(f)
		},
	}
	return cmd
}

func runTUI(f *cmdutil.Factory) error {
	model := tui.New(f)
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}
