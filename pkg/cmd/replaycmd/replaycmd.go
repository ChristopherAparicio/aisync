// Package replaycmd implements the `aisync replay` CLI command.
// It replays a captured session in an isolated git worktree, using the same
// (or a different) AI agent, and compares the results against the original.
package replaycmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/replay"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the replay command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID   string
	Provider    string
	Agent       string
	Model       string
	CommitSHA   string
	MaxMessages int
	JSON        bool
}

// NewCmdReplay creates the `aisync replay` command.
func NewCmdReplay(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "replay <session-id>",
		Short: "Replay a session in an isolated worktree to validate improvements",
		Long: `Replay user messages from a captured session against an AI agent in an
isolated git worktree. Compares the replay result against the original to
determine if changes (skill improvements, agent config, model switch)
actually produce better outcomes.

This is regression testing for AI agents — if you improved a SKILL.md,
replay the session that missed it and verify the skill is now loaded.

The replay creates a temporary git worktree at the original commit,
sends the same user messages to the agent, and cleans up after.

Examples:
  aisync replay abc123                            # replay with same agent/provider
  aisync replay abc123 --provider opencode        # replay with different provider
  aisync replay abc123 --agent coder --model gpt-4o  # override agent & model
  aisync replay abc123 --max-messages 3           # only first 3 user messages
  aisync replay --json abc123                     # structured JSON output`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runReplay(opts)
		},
	}

	cmd.Flags().StringVar(&opts.Provider, "provider", "", "Override provider (opencode, claude-code)")
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Override agent name")
	cmd.Flags().StringVar(&opts.Model, "model", "", "Override model")
	cmd.Flags().StringVar(&opts.CommitSHA, "commit", "", "Override git commit to checkout")
	cmd.Flags().IntVar(&opts.MaxMessages, "max-messages", 0, "Limit number of user messages to replay (0 = all)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")

	return cmd
}

func runReplay(opts *Options) error {
	out := opts.IO.Out

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("initializing store: %w", err)
	}

	sid, err := session.ParseID(opts.SessionID)
	if err != nil {
		return err
	}

	// Determine provider for runner selection.
	var provider session.ProviderName
	if opts.Provider != "" {
		parsed, parseErr := session.ParseProviderName(opts.Provider)
		if parseErr != nil {
			return parseErr
		}
		provider = parsed
	}

	// Build available runners.
	runners := []replay.Runner{
		replay.NewOpenCodeRunner(),
		replay.NewClaudeCodeRunner(),
	}

	engine := replay.NewEngine(replay.EngineConfig{
		Store:    store,
		Runners:  runners,
		Capturer: replay.NewProviderCapturer(store, nil),
	})

	req := replay.Request{
		SourceSessionID: sid,
		Provider:        provider,
		Agent:           opts.Agent,
		Model:           opts.Model,
		CommitSHA:       opts.CommitSHA,
		MaxMessages:     opts.MaxMessages,
	}

	if !opts.JSON {
		fmt.Fprintf(out, "Replaying session %s...\n", sid)
	}

	result, err := engine.Replay(context.Background(), req)
	if err != nil {
		return fmt.Errorf("replay failed: %w", err)
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Text output.
	fmt.Fprintf(out, "\n=== Replay Result ===\n")
	fmt.Fprintf(out, "  Original: %s (%s/%s)\n", result.OriginalSession.ID, result.OriginalSession.Provider, result.OriginalSession.Agent)
	fmt.Fprintf(out, "  Duration: %s\n", result.Duration.Round(1))
	fmt.Fprintf(out, "  Worktree: %s\n", result.WorktreePath)

	if result.Error != "" {
		fmt.Fprintf(out, "\n  Error: %s\n", result.Error)
	}

	if result.Comparison != nil {
		c := result.Comparison
		fmt.Fprintf(out, "\n=== Comparison ===\n")
		fmt.Fprintf(out, "  Tokens:     %d → %d (%+d)\n", c.OriginalTokens, c.ReplayTokens, c.TokenDelta)
		fmt.Fprintf(out, "  Errors:     %d → %d (%+d)\n", c.OriginalErrors, c.ReplayErrors, c.ErrorDelta)
		fmt.Fprintf(out, "  Messages:   %d → %d\n", c.OriginalMessages, c.ReplayMessages)
		fmt.Fprintf(out, "  Tool calls: %d → %d\n", c.OriginalToolCalls, c.ReplayToolCalls)

		if len(c.NewSkillsLoaded) > 0 {
			fmt.Fprintf(out, "  New skills: %v\n", c.NewSkillsLoaded)
		}
		if len(c.SkillsLost) > 0 {
			fmt.Fprintf(out, "  Skills lost: %v\n", c.SkillsLost)
		}

		verdictIcon := "="
		switch c.Verdict {
		case "improved":
			verdictIcon = "↑"
		case "degraded":
			verdictIcon = "↓"
		}
		fmt.Fprintf(out, "\n  Verdict: %s %s\n", verdictIcon, c.Verdict)
	} else {
		fmt.Fprintf(out, "\n  Note: Replay session capture not yet implemented — agent ran but no comparison available.\n")
		fmt.Fprintf(out, "  The replay execution completed successfully. Session comparison will be available\n")
		fmt.Fprintf(out, "  once provider-specific capture from worktree is implemented.\n")
	}

	return nil
}
