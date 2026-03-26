// Package mcpcmd implements the `aisync mcp` CLI command.
// It starts the MCP (Model Context Protocol) server over stdio,
// allowing AI assistants to interact with aisync sessions directly
// through JSON-RPC 2.0.
package mcpcmd

import (
	"github.com/spf13/cobra"

	aisyncmcp "github.com/ChristopherAparicio/aisync/internal/mcp"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/mark3labs/mcp-go/server"
)

// NewCmdMCP creates the `aisync mcp` command.
func NewCmdMCP(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the aisync MCP server (stdio)",
		Long: `Start the aisync MCP server over stdio.

The MCP server exposes aisync tools via the Model Context Protocol,
allowing AI assistants (Claude, Cursor, OpenCode) to capture, restore,
list, export, and manage sessions directly.

Communication happens over stdin/stdout using JSON-RPC 2.0.

Tools:
  aisync_capture    Capture the current AI session
  aisync_restore    Restore an AI session
  aisync_get        Get session details by ID
  aisync_list       List captured sessions
  aisync_delete     Delete a session
  aisync_export     Export a session
  aisync_import     Import a session
  aisync_link       Link session to PR/commit
  aisync_comment    Post PR comment with session summary
  aisync_stats      Get session statistics
  aisync_push       Push sessions to sync branch
  aisync_pull       Pull sessions from sync branch
  aisync_sync       Sync sessions (pull + push)
  aisync_index      Read sync branch index`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionSvc, err := f.SessionService()
			if err != nil {
				return err
			}

			// Optional services — MCP server works without them
			syncSvc, _ := f.SyncService()
			errorSvc, _ := f.ErrorService()
			sessionEventSvc, _ := f.SessionEventService()

			s := aisyncmcp.NewServer(aisyncmcp.Config{
				SessionService:      sessionSvc,
				SyncService:         syncSvc,
				ErrorService:        errorSvc,
				SessionEventService: sessionEventSvc,
				Version:             cmd.Root().Version,
			})

			return server.ServeStdio(s)
		},
	}

	return cmd
}
