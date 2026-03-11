// Package webcmd implements the `aisync web` CLI command.
// It launches the aisync web dashboard on a local HTTP server.
package webcmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/web"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

const defaultAddr = "127.0.0.1:8372"

// NewCmdWeb creates the `aisync web` command.
func NewCmdWeb(f *cmdutil.Factory) *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Launch the aisync web dashboard",
		Long: `Launch a local web dashboard for browsing sessions, viewing statistics,
and analyzing AI coding costs.

The dashboard runs at http://127.0.0.1:8372 by default and shuts down
gracefully on Ctrl+C.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionSvc, err := f.SessionService()
			if err != nil {
				return err
			}

			srv, err := web.New(web.Config{
				SessionService: sessionSvc,
				Addr:           addr,
			})
			if err != nil {
				return fmt.Errorf("initializing web server: %w", err)
			}

			return srv.ListenAndServe()
		},
	}

	cmd.Flags().StringVar(&addr, "addr", defaultAddr, "Address to listen on (host:port)")

	return cmd
}
