// Package authcmd implements the `aisync auth` CLI command group.
// It provides subcommands for user registration, login, and API key management.
//
// Commands:
//   - `aisync auth register` — create a new user account
//   - `aisync auth login`    — authenticate and store JWT token
//   - `aisync auth logout`   — clear stored token
//   - `aisync auth me`       — show current user info
//   - `aisync auth keys create`  — generate an API key
//   - `aisync auth keys list`    — list API keys
//   - `aisync auth keys revoke`  — deactivate an API key
package authcmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/client"
	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// globalConfigDir returns ~/.aisync/
func globalConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aisync")
}

// getClient creates an authenticated API client using the stored token.
func getClient(f *cmdutil.Factory) (*client.Client, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	serverURL := cfg.GetServerURL()
	if serverURL == "" {
		return nil, fmt.Errorf("server.url is not configured — run 'aisync config set server.url http://host:port' first")
	}

	token := auth.LoadToken(globalConfigDir())
	var opts []client.Option
	if token != "" {
		opts = append(opts, client.WithAuthToken(token))
	}

	return client.New(serverURL, opts...), nil
}

// NewCmdAuth creates the `aisync auth` command group.
func NewCmdAuth(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication (register, login, API keys)",
		Long: `Authenticate with an aisync server using username/password or API keys.

The server must have authentication enabled (server.auth.enabled = true).
Tokens are stored in ~/.aisync/token and sent automatically with requests.

Examples:
  aisync auth register -u alice -p secret123!  # create account (first user = admin)
  aisync auth login -u alice -p secret123!     # login and store token
  aisync auth me                                # show current user
  aisync auth keys create --name "CI"           # generate API key
  aisync auth keys list                         # list your API keys
  aisync auth logout                            # clear stored token`,
	}

	cmd.AddCommand(newCmdRegister(f))
	cmd.AddCommand(newCmdLogin(f))
	cmd.AddCommand(newCmdLogout(f))
	cmd.AddCommand(newCmdMe(f))
	cmd.AddCommand(newCmdKeys(f))

	return cmd
}

// ── register ──

func newCmdRegister(f *cmdutil.Factory) *cobra.Command {
	var username, password string

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Create a new user account",
		Long:  "Register a new user. The first registered user becomes admin.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegister(f, username, password)
		},
	}

	cmd.Flags().StringVarP(&username, "username", "u", "", "Username (required)")
	cmd.Flags().StringVarP(&password, "password", "p", "", "Password (min 8 chars, required)")
	_ = cmd.MarkFlagRequired("username")
	_ = cmd.MarkFlagRequired("password")

	return cmd
}

func runRegister(f *cmdutil.Factory, username, password string) error {
	out := f.IOStreams.Out

	c, err := getClient(f)
	if err != nil {
		return err
	}

	result, err := c.Register(client.AuthRegisterRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	// Store the token.
	if err := auth.SaveToken(globalConfigDir(), result.Token); err != nil {
		fmt.Fprintf(out, "Warning: could not save token: %v\n", err)
	}

	fmt.Fprintf(out, "Registered as %s (role: %s)\n", result.User.Username, result.User.Role)
	fmt.Fprintf(out, "Token saved to ~/.aisync/token\n")
	return nil
}

// ── login ──

func newCmdLogin(f *cmdutil.Factory) *cobra.Command {
	var username, password string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate and store JWT token",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogin(f, username, password)
		},
	}

	cmd.Flags().StringVarP(&username, "username", "u", "", "Username (required)")
	cmd.Flags().StringVarP(&password, "password", "p", "", "Password (required)")
	_ = cmd.MarkFlagRequired("username")
	_ = cmd.MarkFlagRequired("password")

	return cmd
}

func runLogin(f *cmdutil.Factory, username, password string) error {
	out := f.IOStreams.Out

	c, err := getClient(f)
	if err != nil {
		return err
	}

	result, err := c.Login(client.AuthLoginRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	if err := auth.SaveToken(globalConfigDir(), result.Token); err != nil {
		fmt.Fprintf(out, "Warning: could not save token: %v\n", err)
	}

	fmt.Fprintf(out, "Logged in as %s (role: %s)\n", result.User.Username, result.User.Role)
	return nil
}

// ── logout ──

func newCmdLogout(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear stored authentication token",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogout(f)
		},
	}
}

func runLogout(f *cmdutil.Factory) error {
	out := f.IOStreams.Out
	if err := auth.ClearToken(globalConfigDir()); err != nil {
		return fmt.Errorf("clearing token: %w", err)
	}
	fmt.Fprintln(out, "Logged out. Token removed from ~/.aisync/token")
	return nil
}

// ── me ──

func newCmdMe(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "me",
		Short: "Show current authenticated user",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMe(f)
		},
	}
}

func runMe(f *cmdutil.Factory) error {
	out := f.IOStreams.Out

	c, err := getClient(f)
	if err != nil {
		return err
	}

	token := auth.LoadToken(globalConfigDir())
	if token == "" {
		return fmt.Errorf("not logged in — run 'aisync auth login' first")
	}

	user, err := c.AuthMe()
	if err != nil {
		return fmt.Errorf("fetching user info: %w", err)
	}

	fmt.Fprintf(out, "User:     %s\n", user.Username)
	fmt.Fprintf(out, "ID:       %s\n", user.ID)
	fmt.Fprintf(out, "Role:     %s\n", user.Role)
	return nil
}

// ── keys (subcommand group) ──

func newCmdKeys(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage API keys",
	}

	cmd.AddCommand(newCmdKeysCreate(f))
	cmd.AddCommand(newCmdKeysList(f))
	cmd.AddCommand(newCmdKeysRevoke(f))

	return cmd
}

func newCmdKeysCreate(f *cmdutil.Factory) *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Generate a new API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeysCreate(f, name)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Key name (e.g. 'CI pipeline')")

	return cmd
}

func runKeysCreate(f *cmdutil.Factory, name string) error {
	out := f.IOStreams.Out

	c, err := getClient(f)
	if err != nil {
		return err
	}

	result, err := c.AuthCreateAPIKey(client.CreateAPIKeyRequest{Name: name})
	if err != nil {
		return fmt.Errorf("creating API key: %w", err)
	}

	fmt.Fprintf(out, "API key created:\n")
	fmt.Fprintf(out, "  Name:   %s\n", result.APIKey.Name)
	fmt.Fprintf(out, "  Key:    %s\n", result.RawKey)
	fmt.Fprintf(out, "  Prefix: %s\n", result.APIKey.KeyPrefix)
	fmt.Fprintf(out, "\nSave this key — it will not be shown again.\n")
	fmt.Fprintf(out, "Use it with: X-API-Key: %s\n", result.RawKey)
	return nil
}

func newCmdKeysList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List your API keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeysList(f)
		},
	}
}

func runKeysList(f *cmdutil.Factory) error {
	out := f.IOStreams.Out

	c, err := getClient(f)
	if err != nil {
		return err
	}

	keys, err := c.AuthListAPIKeys()
	if err != nil {
		return fmt.Errorf("listing API keys: %w", err)
	}

	if len(keys) == 0 {
		fmt.Fprintln(out, "No API keys found.")
		return nil
	}

	fmt.Fprintf(out, "%-36s  %-20s  %-10s  %-8s  %s\n", "ID", "NAME", "PREFIX", "ACTIVE", "CREATED")
	for _, k := range keys {
		active := "yes"
		if !k.Active {
			active = "revoked"
		}
		fmt.Fprintf(out, "%-36s  %-20s  %-10s  %-8s  %s\n",
			k.ID, truncate(k.Name, 20), k.KeyPrefix, active, k.CreatedAt)
	}
	return nil
}

func newCmdKeysRevoke(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <key-id>",
		Short: "Revoke an API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeysRevoke(f, args[0])
		},
	}
}

func runKeysRevoke(f *cmdutil.Factory, keyID string) error {
	out := f.IOStreams.Out

	c, err := getClient(f)
	if err != nil {
		return err
	}

	if err := c.AuthRevokeAPIKey(keyID); err != nil {
		return fmt.Errorf("revoking API key: %w", err)
	}

	fmt.Fprintf(out, "API key %s revoked.\n", keyID)
	return nil
}

// truncate shortens a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// ── IOStreams helper ──

var _ iostreams.IOStreams = iostreams.IOStreams{} // compile check — just verifying import
