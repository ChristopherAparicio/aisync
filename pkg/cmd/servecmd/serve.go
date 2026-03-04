// Package servecmd implements the `aisync serve` CLI command.
// It starts the HTTP API server, exposing session and sync operations
// as JSON endpoints on a configurable address (default: 127.0.0.1:8371).
package servecmd

import (
	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/api"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

const defaultAddr = "127.0.0.1:8371"

// NewCmdServe creates the `aisync serve` command.
func NewCmdServe(f *cmdutil.Factory) *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the aisync HTTP API server",
		Long: `Start the aisync HTTP API server.

The server exposes session and sync operations as JSON endpoints.
It listens on 127.0.0.1:8371 by default and shuts down gracefully
on SIGINT/SIGTERM.

Endpoints:
  GET    /api/v1/health                Health check
  POST   /api/v1/sessions/capture      Capture a session
  POST   /api/v1/sessions/restore      Restore a session
  GET    /api/v1/sessions/{id}         Get session by ID
  GET    /api/v1/sessions              List sessions
  DELETE /api/v1/sessions/{id}         Delete a session
  POST   /api/v1/sessions/export       Export a session
  POST   /api/v1/sessions/import       Import a session
  POST   /api/v1/sessions/link         Link session to PR/commit
  POST   /api/v1/sessions/comment      Post PR comment
  GET    /api/v1/stats                 Session statistics
  POST   /api/v1/sync/push             Push sessions to sync branch
  POST   /api/v1/sync/pull             Pull sessions from sync branch
  POST   /api/v1/sync/sync             Sync (pull + push)
  GET    /api/v1/sync/index            Read sync index`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionSvc, err := f.SessionService()
			if err != nil {
				return err
			}

			// SyncService is optional — server works without it
			syncSvc, _ := f.SyncService()

			srv := api.New(api.Config{
				SessionService: sessionSvc,
				SyncService:    syncSvc,
				Addr:           addr,
			})

			return srv.ListenAndServe()
		},
	}

	cmd.Flags().StringVar(&addr, "addr", defaultAddr, "Address to listen on (host:port)")

	return cmd
}
