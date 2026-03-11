package root

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmd/blamecmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/capture"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/commentcmd"
	configcmd "github.com/ChristopherAparicio/aisync/pkg/cmd/config"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/diffcmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/efficiencycmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/explaincmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/export"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/gccmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/hooks"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/importcmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/initcmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/linkcmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/listcmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/mcpcmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/restore"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/resumecmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/rewindcmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/searchcmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/secrets"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/servecmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/show"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/statscmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/status"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/synccmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/toolusagecmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/tuicmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/webcmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// NewCmdRoot creates the root `aisync` command.
// The version string is injected from main via -ldflags at build time.
func NewCmdRoot(f *cmdutil.Factory, version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "aisync",
		Short:         "Link AI sessions to Git branches",
		Long:          "aisync captures AI coding sessions (Claude Code, OpenCode, Cursor), stores them, and restores context when needed.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}

	// Subcommands
	cmd.AddCommand(newCmdVersion(version))
	cmd.AddCommand(newCmdCompletion())
	cmd.AddCommand(initcmd.NewCmdInit(f))
	cmd.AddCommand(status.NewCmdStatus(f))
	cmd.AddCommand(capture.NewCmdCapture(f))
	cmd.AddCommand(restore.NewCmdRestore(f))
	cmd.AddCommand(listcmd.NewCmdList(f))
	cmd.AddCommand(show.NewCmdShow(f))
	cmd.AddCommand(hooks.NewCmdHooks(f))
	cmd.AddCommand(secrets.NewCmdSecrets(f))
	cmd.AddCommand(export.NewCmdExport(f))
	cmd.AddCommand(importcmd.NewCmdImport(f))
	cmd.AddCommand(linkcmd.NewCmdLink(f))
	cmd.AddCommand(commentcmd.NewCmdComment(f))
	cmd.AddCommand(statscmd.NewCmdStats(f))
	cmd.AddCommand(searchcmd.NewCmdSearch(f))
	cmd.AddCommand(blamecmd.NewCmdBlame(f))
	cmd.AddCommand(explaincmd.NewCmdExplain(f))
	cmd.AddCommand(toolusagecmd.NewCmdToolUsage(f))
	cmd.AddCommand(efficiencycmd.NewCmdEfficiency(f))
	cmd.AddCommand(resumecmd.NewCmdResume(f))
	cmd.AddCommand(rewindcmd.NewCmdRewind(f))
	cmd.AddCommand(gccmd.NewCmdGC(f))
	cmd.AddCommand(diffcmd.NewCmdDiff(f))
	cmd.AddCommand(tuicmd.NewCmdTUI(f))
	cmd.AddCommand(configcmd.NewCmdConfig(f))
	cmd.AddCommand(synccmd.NewCmdPush(f))
	cmd.AddCommand(synccmd.NewCmdPull(f))
	cmd.AddCommand(synccmd.NewCmdSync(f))
	cmd.AddCommand(servecmd.NewCmdServe(f))
	cmd.AddCommand(webcmd.NewCmdWeb(f))
	cmd.AddCommand(mcpcmd.NewCmdMCP(f))

	return cmd
}

// newCmdVersion creates the `aisync version` subcommand.
func newCmdVersion(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of aisync",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Printf("aisync %s\n", version)
			return nil
		},
	}
}

// newCmdCompletion creates the `aisync completion` subcommand for shell completions.
func newCmdCompletion() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for aisync.

To load completions:

Bash:
  $ source <(aisync completion bash)
  # Or for permanent use:
  $ aisync completion bash > /etc/bash_completion.d/aisync

Zsh:
  $ aisync completion zsh > "${fpath[1]}/_aisync"

Fish:
  $ aisync completion fish | source
  # Or for permanent use:
  $ aisync completion fish > ~/.config/fish/completions/aisync.fish

PowerShell:
  PS> aisync completion powershell | Out-String | Invoke-Expression`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell %q", args[0])
			}
		},
	}

	return cmd
}
