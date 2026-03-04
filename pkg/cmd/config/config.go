// Package config implements the `aisync config` CLI command.
package config

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// NewCmdConfig creates the `aisync config` parent command with get/set subcommands.
func NewCmdConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage aisync configuration",
		Long: `View and update aisync configuration values.

Configuration is stored in two layers:
  - Global: ~/.aisync/config.json
  - Per-repo: .aisync/config.json (overrides global)

Supported keys:
  storage_mode              Storage mode for sessions (full, compact, summary)
  auto_capture              Auto-capture on commit (true, false)
  secrets.mode              Secret detection mode (mask, warn, block)
  secrets.custom_patterns.add  Add a custom secret pattern (NAME REGEX)`,
	}

	cmd.AddCommand(newCmdConfigGet(f))
	cmd.AddCommand(newCmdConfigSet(f))
	cmd.AddCommand(newCmdConfigList(f))

	return cmd
}

// newCmdConfigGet creates the `aisync config get <key>` subcommand.
func newCmdConfigGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			value, err := cfg.Get(args[0])
			if err != nil {
				return err
			}

			fmt.Fprintln(f.IOStreams.Out, value)
			return nil
		},
	}
}

// newCmdConfigSet creates the `aisync config set <key> <value>` subcommand.
func newCmdConfigSet(f *cmdutil.Factory) *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Long: `Set a configuration value and persist it to disk.

By default, saves to the per-repo config (.aisync/config.json).
Use --global to save to the global config (~/.aisync/config.json).

Examples:
  aisync config set storage_mode full
  aisync config set auto_capture false
  aisync config set secrets.mode warn
  aisync config set secrets.custom_patterns.add "MY_TOKEN MY_TOKEN_[a-zA-Z0-9]+"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSet(f, args[0], args[1], global)
		},
	}

	cmd.Flags().BoolVar(&global, "global", false, "Save to global config (~/.aisync/)")

	return cmd
}

// newCmdConfigList creates the `aisync config list` subcommand.
func newCmdConfigList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List all configuration values",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigList(f)
		},
	}
}

func runConfigSet(f *cmdutil.Factory, key, value string, global bool) error {
	cfg, err := f.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := cfg.Set(key, value); err != nil {
		return err
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Fprintf(f.IOStreams.Out, "Set %s = %s\n", key, value)
	return nil
}

func runConfigList(f *cmdutil.Factory) error {
	out := f.IOStreams.Out
	cfg, err := f.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	keys := []string{"storage_mode", "auto_capture", "secrets.mode"}
	for _, key := range keys {
		value, err := cfg.Get(key)
		if err != nil {
			continue
		}
		fmt.Fprintf(out, "%s = %s\n", key, value)
	}

	return nil
}

// ValidConfigKeys returns the valid configuration keys for shell completion.
func ValidConfigKeys() []string {
	return []string{
		"storage_mode",
		"auto_capture",
		"secrets.mode",
		"secrets.custom_patterns.add",
	}
}

// IOStreamsGetter is a helper interface for testing.
type IOStreamsGetter interface {
	GetIOStreams() *iostreams.IOStreams
}
