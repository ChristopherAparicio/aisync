// Package skillscmd implements the `aisync skills` CLI command group.
// It provides subcommands for analyzing and improving SKILL.md files
// based on missed-skill observations from captured sessions.
//
// Commands:
//   - `aisync skills suggest <session-id>` — list proposed improvements
//   - `aisync skills fix <session-id>`     — apply improvements to disk
//   - `aisync skills validate <session-id>` — fix + replay to verify
package skillscmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/llmfactory"
	"github.com/ChristopherAparicio/aisync/internal/replay"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/skillresolver"
	"github.com/ChristopherAparicio/aisync/internal/skillresolver/llmanalyzer"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// NewCmdSkills creates the `aisync skills` command group.
func NewCmdSkills(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Analyze and improve agent skill configurations",
		Long: `Manage AI agent skill improvements based on missed-skill observations.

When a session analysis detects that a skill SHOULD have been triggered but wasn't,
the skills resolver analyzes why and proposes SKILL.md improvements (better descriptions,
new keywords, trigger patterns).

Examples:
  aisync skills suggest abc123               # preview proposed improvements
  aisync skills fix abc123                   # apply improvements to SKILL.md files
  aisync skills fix abc123 --skills replay-tester  # only fix specific skill
  aisync skills validate abc123              # fix + replay to verify
  aisync skills suggest --json abc123        # structured JSON output`,
	}

	cmd.AddCommand(newCmdSuggest(f))
	cmd.AddCommand(newCmdFix(f))
	cmd.AddCommand(newCmdValidate(f))

	return cmd
}

// ── suggest subcommand ──

type suggestOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID  string
	SkillNames []string
	JSON       bool
}

func newCmdSuggest(f *cmdutil.Factory) *cobra.Command {
	opts := &suggestOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "suggest <session-id>",
		Short: "Preview skill improvement proposals without applying them",
		Long: `Analyze missed skills from a session and propose SKILL.md improvements.
This is a dry-run — no files are modified.

The resolver examines:
  1. Which skills were recommended but not loaded (from analysis)
  2. The current SKILL.md content (description, keywords)
  3. The user messages that should have triggered the skill

Then uses an LLM to propose targeted improvements.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runSuggest(opts)
		},
	}

	cmd.Flags().StringSliceVar(&opts.SkillNames, "skills", nil, "Only analyze specific skills (comma-separated)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")

	return cmd
}

func runSuggest(opts *suggestOptions) error {
	resolver, err := buildResolver(opts.Factory)
	if err != nil {
		return err
	}

	result, err := resolver.Resolve(context.Background(), skillresolver.ResolveRequest{
		SessionID:  opts.SessionID,
		SkillNames: opts.SkillNames,
		DryRun:     true,
	})
	if err != nil {
		return fmt.Errorf("resolving skills: %w", err)
	}

	return renderResult(opts.IO, result, opts.JSON)
}

// ── fix subcommand ──

type fixOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID  string
	SkillNames []string
	JSON       bool
}

func newCmdFix(f *cmdutil.Factory) *cobra.Command {
	opts := &fixOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "fix <session-id>",
		Short: "Apply skill improvements to SKILL.md files",
		Long: `Analyze missed skills and apply proposed improvements to SKILL.md files on disk.

This modifies files. Use 'aisync skills suggest' first to preview changes.

After applying, you can verify the improvements by replaying the session:
  aisync replay <session-id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runFix(opts)
		},
	}

	cmd.Flags().StringSliceVar(&opts.SkillNames, "skills", nil, "Only fix specific skills (comma-separated)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")

	return cmd
}

func runFix(opts *fixOptions) error {
	resolver, err := buildResolver(opts.Factory)
	if err != nil {
		return err
	}

	result, err := resolver.Resolve(context.Background(), skillresolver.ResolveRequest{
		SessionID:  opts.SessionID,
		SkillNames: opts.SkillNames,
		DryRun:     false,
	})
	if err != nil {
		return fmt.Errorf("resolving skills: %w", err)
	}

	return renderResult(opts.IO, result, opts.JSON)
}

// ── Shared helpers ──

// buildResolver creates a ResolverServicer from the factory dependencies.
func buildResolver(f *cmdutil.Factory) (skillresolver.ResolverServicer, error) {
	sessSvc, err := f.SessionService()
	if err != nil {
		return nil, fmt.Errorf("initializing session service: %w", err)
	}

	analysisSvc, err := f.AnalysisService()
	if err != nil {
		return nil, fmt.Errorf("initializing analysis service: %w", err)
	}

	// Build the LLM-based skill analyzer using the same factory pipeline as analysis.
	cfg, cfgErr := f.Config()
	if cfgErr != nil {
		return nil, fmt.Errorf("loading config: %w", cfgErr)
	}

	// Use the default profile (or analysis profile) for the skill resolver.
	baseAnalyzer, analyzerErr := llmfactory.NewAnalyzerFromConfig(cfg, "")
	if analyzerErr != nil {
		return nil, fmt.Errorf("building LLM analyzer for skill resolution: %w", analyzerErr)
	}

	// Wrap the analysis.Analyzer with our skill-specific adapter.
	skillAnalyzer := llmanalyzer.New(llmanalyzer.Config{
		Analyzer: baseAnalyzer,
	})

	return skillresolver.NewService(skillresolver.ServiceConfig{
		Sessions: sessSvc,
		Analyses: analysisSvc,
		Analyzer: skillAnalyzer,
	}), nil
}

// renderResult outputs the resolution result in text or JSON format.
func renderResult(io *iostreams.IOStreams, result *skillresolver.ResolveResult, jsonOut bool) error {
	out := io.Out

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Text output.
	if len(result.Improvements) == 0 {
		fmt.Fprintf(out, "No skill improvements found for session %s.\n", result.SessionID)
		if result.Verdict == skillresolver.VerdictNoChange {
			fmt.Fprintf(out, "Either no skills were missed, or no analysis exists for this session.\n")
			fmt.Fprintf(out, "Run 'aisync analyze %s' first.\n", result.SessionID)
		}
		return nil
	}

	fmt.Fprintf(out, "=== Skill Improvements for %s ===\n\n", result.SessionID)

	for i, imp := range result.Improvements {
		fmt.Fprintf(out, "%d. %s (%s)\n", i+1, imp.SkillName, imp.Kind)
		fmt.Fprintf(out, "   Path: %s\n", imp.SkillPath)

		switch imp.Kind {
		case skillresolver.KindDescription:
			fmt.Fprintf(out, "   Current:  %s\n", imp.CurrentDescription)
			fmt.Fprintf(out, "   Proposed: %s\n", imp.ProposedDescription)

		case skillresolver.KindKeywords:
			fmt.Fprintf(out, "   Add keywords: %s\n", strings.Join(imp.AddKeywords, ", "))

		case skillresolver.KindTriggerPattern:
			fmt.Fprintf(out, "   Add trigger patterns:\n")
			for _, p := range imp.AddTriggerPatterns {
				fmt.Fprintf(out, "     - %s\n", p)
			}

		case skillresolver.KindContent:
			preview := imp.ProposedContent
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			fmt.Fprintf(out, "   Proposed content: %s\n", preview)
		}

		fmt.Fprintf(out, "   Confidence: %.0f%%\n", imp.Confidence*100)
		fmt.Fprintf(out, "   Reasoning: %s\n", imp.Reasoning)
		fmt.Fprintf(out, "\n")
	}

	// Summary.
	verdictIcon := ""
	switch result.Verdict {
	case skillresolver.VerdictFixed:
		verdictIcon = "+"
	case skillresolver.VerdictPartial:
		verdictIcon = "~"
	case skillresolver.VerdictPending:
		verdictIcon = "..."
	case skillresolver.VerdictNoChange:
		verdictIcon = "-"
	}

	fmt.Fprintf(out, "Verdict: %s %s\n", verdictIcon, result.Verdict)
	if result.Applied > 0 {
		fmt.Fprintf(out, "Applied: %d/%d improvements written to disk\n", result.Applied, len(result.Improvements))
		fmt.Fprintf(out, "\nVerify with: aisync replay %s\n", result.SessionID)
	} else if result.Verdict == skillresolver.VerdictPending {
		fmt.Fprintf(out, "Apply with: aisync skills fix %s\n", result.SessionID)
	}
	fmt.Fprintf(out, "Duration: %s\n", result.Duration.Round(1))

	return nil
}

// ── validate subcommand ──

type validateOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID  string
	SkillNames []string
	Provider   string
	JSON       bool
}

func newCmdValidate(f *cmdutil.Factory) *cobra.Command {
	opts := &validateOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "validate <session-id>",
		Short: "Fix skills and replay the session to verify improvements",
		Long: `End-to-end validation: apply skill improvements, then replay the session
to check if the missed skills are now loaded.

This is the complete improvement loop:
  1. Analyze missed skills (same as 'suggest')
  2. Apply improvements to SKILL.md files (same as 'fix')
  3. Replay the session with the improved skills
  4. Check if previously-missed skills are now loaded in the replay

Requires a replay-capable setup (git repo, agent available).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runValidate(opts)
		},
	}

	cmd.Flags().StringSliceVar(&opts.SkillNames, "skills", nil, "Only validate specific skills (comma-separated)")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "Override provider for replay (opencode, claude-code)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")

	return cmd
}

// validateResult is the structured output for JSON mode.
type validateResult struct {
	SessionID    string                           `json:"session_id"`
	Improvements []skillresolver.SkillImprovement `json:"improvements"`
	Applied      int                              `json:"applied"`
	ReplayResult *replay.Result                   `json:"replay_result,omitempty"`
	SkillsBefore []string                         `json:"skills_before,omitempty"`
	SkillsAfter  []string                         `json:"skills_after,omitempty"`
	NewLoaded    []string                         `json:"new_skills_loaded,omitempty"`
	Validated    bool                             `json:"validated"`
	Verdict      string                           `json:"verdict"` // "validated", "partial", "not_improved", "replay_failed"
	Error        string                           `json:"error,omitempty"`
}

func runValidate(opts *validateOptions) error {
	out := opts.IO.Out
	logger := log.New(out, "", 0)

	result := validateResult{
		SessionID: opts.SessionID,
	}

	// 1. Apply skill improvements.
	if !opts.JSON {
		fmt.Fprintf(out, "=== Step 1: Analyzing and applying skill improvements ===\n")
	}

	resolver, err := buildResolver(opts.Factory)
	if err != nil {
		return err
	}

	resolveResult, err := resolver.Resolve(context.Background(), skillresolver.ResolveRequest{
		SessionID:  opts.SessionID,
		SkillNames: opts.SkillNames,
		DryRun:     false, // Apply!
	})
	if err != nil {
		return fmt.Errorf("resolving skills: %w", err)
	}

	result.Improvements = resolveResult.Improvements
	result.Applied = resolveResult.Applied

	if len(resolveResult.Improvements) == 0 {
		result.Verdict = "no_improvements"
		if opts.JSON {
			return json.NewEncoder(out).Encode(result)
		}
		fmt.Fprintf(out, "No skill improvements found. Nothing to validate.\n")
		return nil
	}

	if resolveResult.Applied == 0 {
		result.Verdict = "not_applied"
		result.Error = "improvements proposed but none could be applied to disk"
		if opts.JSON {
			return json.NewEncoder(out).Encode(result)
		}
		fmt.Fprintf(out, "Improvements proposed but could not be applied to disk.\n")
		return nil
	}

	if !opts.JSON {
		fmt.Fprintf(out, "  Applied %d improvements to %d skills.\n\n",
			resolveResult.Applied, countUniqueSkills(resolveResult.Improvements))
	}

	// 2. Replay the session.
	if !opts.JSON {
		fmt.Fprintf(out, "=== Step 2: Replaying session to validate ===\n")
	}

	store, err := opts.Factory.Store()
	if err != nil {
		result.Verdict = "replay_failed"
		result.Error = fmt.Sprintf("store unavailable: %v", err)
		if opts.JSON {
			return json.NewEncoder(out).Encode(result)
		}
		fmt.Fprintf(out, "Cannot replay: %v\n", err)
		return nil
	}

	sid, err := session.ParseID(opts.SessionID)
	if err != nil {
		return err
	}

	var provider session.ProviderName
	if opts.Provider != "" {
		parsed, parseErr := session.ParseProviderName(opts.Provider)
		if parseErr != nil {
			return parseErr
		}
		provider = parsed
	}

	engine := replay.NewEngine(replay.EngineConfig{
		Store: store,
		Runners: []replay.Runner{
			replay.NewOpenCodeRunner(),
			replay.NewClaudeCodeRunner(),
		},
		Capturer: replay.NewProviderCapturer(store, logger),
	})

	replayResult, err := engine.Replay(context.Background(), replay.Request{
		SourceSessionID: sid,
		Provider:        provider,
	})
	if err != nil {
		result.Verdict = "replay_failed"
		result.Error = fmt.Sprintf("replay failed: %v", err)
		if opts.JSON {
			return json.NewEncoder(out).Encode(result)
		}
		fmt.Fprintf(out, "  Replay failed: %v\n", err)
		fmt.Fprintf(out, "\nImprovements were applied but could not be verified via replay.\n")
		fmt.Fprintf(out, "You can manually verify with: aisync replay %s\n", opts.SessionID)
		return nil
	}

	result.ReplayResult = replayResult

	// 3. Check if missed skills are now loaded.
	if !opts.JSON {
		fmt.Fprintf(out, "  Replay completed in %s.\n\n", replayResult.Duration.Round(1))
		fmt.Fprintf(out, "=== Step 3: Validation ===\n")
	}

	if replayResult.Comparison != nil {
		c := replayResult.Comparison
		result.SkillsBefore = c.OriginalSkills
		result.SkillsAfter = c.ReplaySkills
		result.NewLoaded = c.NewSkillsLoaded

		// Check if the fixed skills are among the newly loaded ones.
		fixedSkills := uniqueSkillNames(resolveResult.Improvements)
		loaded := 0
		for _, fixed := range fixedSkills {
			for _, newSkill := range c.NewSkillsLoaded {
				if fixed == newSkill {
					loaded++
					break
				}
			}
		}

		if loaded == len(fixedSkills) {
			result.Validated = true
			result.Verdict = "validated"
		} else if loaded > 0 {
			result.Validated = true
			result.Verdict = "partial"
		} else {
			result.Verdict = "not_improved"
		}

		if !opts.JSON {
			if len(c.NewSkillsLoaded) > 0 {
				fmt.Fprintf(out, "  New skills loaded: %s\n", strings.Join(c.NewSkillsLoaded, ", "))
			}
			if len(c.SkillsLost) > 0 {
				fmt.Fprintf(out, "  Skills lost: %s\n", strings.Join(c.SkillsLost, ", "))
			}
			fmt.Fprintf(out, "  Fixed skills verified: %d/%d\n", loaded, len(fixedSkills))
		}
	} else {
		// No comparison available (capture not yet supported for this provider).
		result.Verdict = "replay_no_comparison"
		if !opts.JSON {
			fmt.Fprintf(out, "  Replay completed but comparison not available.\n")
			fmt.Fprintf(out, "  Skills may have improved — manual verification recommended.\n")
		}
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Text verdict.
	verdictIcon := ""
	switch result.Verdict {
	case "validated":
		verdictIcon = "+"
	case "partial":
		verdictIcon = "~"
	case "not_improved":
		verdictIcon = "-"
	case "replay_no_comparison":
		verdictIcon = "?"
	}
	fmt.Fprintf(out, "\nVerdict: %s %s\n", verdictIcon, result.Verdict)

	return nil
}

// countUniqueSkills counts unique skill names in improvements.
func countUniqueSkills(improvements []skillresolver.SkillImprovement) int {
	seen := make(map[string]bool)
	for _, imp := range improvements {
		seen[imp.SkillName] = true
	}
	return len(seen)
}

// uniqueSkillNames returns deduplicated skill names from improvements.
func uniqueSkillNames(improvements []skillresolver.SkillImprovement) []string {
	seen := make(map[string]bool)
	var result []string
	for _, imp := range improvements {
		if !seen[imp.SkillName] {
			seen[imp.SkillName] = true
			result = append(result, imp.SkillName)
		}
	}
	return result
}
