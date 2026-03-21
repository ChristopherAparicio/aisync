// Package webcmd implements the `aisync web` CLI command.
// It is a convenience alias for `aisync serve --web-only`.
package webcmd

import (
	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmd/servecmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// NewCmdWeb creates the `aisync web` command.
// It delegates to `aisync serve --web-only` so that the web dashboard
// runs on the same unified server infrastructure.
func NewCmdWeb(f *cmdutil.Factory) *cobra.Command {
	serveCmd := servecmd.NewCmdServe(f)

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Launch the aisync web dashboard",
		Long: `Launch a local web dashboard for browsing sessions, viewing statistics,
and analyzing AI coding costs.

This is equivalent to running: aisync serve --web-only

The dashboard listens on 127.0.0.1:8371 by default and shuts down
gracefully on Ctrl+C.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Set --web-only flag before delegating to serve.
			_ = serveCmd.Flags().Set("web-only", "true")
			return serveCmd.RunE(serveCmd, args)
		},
	}

	cmd.Flags().StringP("addr", "", "127.0.0.1:8371", "Address to listen on (host:port)")

	// Sync the addr flag with the serve command's flag.
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("addr") {
			addr, _ := cmd.Flags().GetString("addr")
			return serveCmd.Flags().Set("addr", addr)
		}
		return nil
	}

	return cmd
}
