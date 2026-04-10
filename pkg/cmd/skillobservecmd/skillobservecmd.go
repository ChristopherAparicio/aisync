// Package skillobservecmd implements the `aisync skill-observe` CLI command.
//
// It exposes the offline, deterministic Skill Observer engine
// (internal/skillobs). For a given session, it compares:
//
//   - Recommended skills (what the user's messages imply they need, via
//     keyword matching against skill descriptions)
//   - Loaded skills      (what was actually pulled in during the session,
//     detected from tool calls and message text)
//
// The diff yields:
//
//   - Missed     — recommended but not loaded (agent/prompt improvement signal)
//   - Discovered — loaded but not recommended (recommender keyword gap)
//
// No LLM is ever called. This is a pure observation tool — it reports what
// was detected, not what a model thinks you should do. A downstream LLM
// agent (via shell or MCP) can consume this output to decide whether to
// tweak an agent prompt, tighten a skill's keywords, or ignore the signal.
package skillobservecmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/skillobs"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the skill-observe command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID string
	JSON      bool
	Quiet     bool
}

// NewCmdSkillObserve creates the `aisync skill-observe` command.
func NewCmdSkillObserve(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "skill-observe <session-id>",
		Short: "Observe skill usage vs recommendations in a session (100% offline, no LLM)",
		Long: `Runs the Skill Observer engine on a session to surface skill-loading
gaps. It cross-references what the user asked for with what skills
were actually pulled in, and produces four lists:

  - Available  — every skill known for the project's registry
  - Recommended — skills the keyword recommender matched on user messages
  - Loaded      — skills actually detected as loaded during the session
  - Missed      — recommended but NOT loaded (highest-signal column)
  - Discovered  — loaded but NOT recommended (recommender coverage gap)

No LLM is called — matching is done against skill descriptions and
tool-call patterns. This is a pure observation tool: it flags potential
gaps so an external agent (human or LLM) can decide whether to act.

Examples:
  aisync skill-observe <session-id>          # human-readable report
  aisync skill-observe <session-id> --json   # machine-readable JSON
  aisync skill-observe <session-id> --quiet  # one-liner summary`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runSkillObserve(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")
	cmd.Flags().BoolVar(&opts.Quiet, "quiet", false, "One-liner summary only")

	return cmd
}

func runSkillObserve(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	// Load session (Messages + ProjectPath).
	sess, err := svc.Get(opts.SessionID)
	if err != nil {
		return fmt.Errorf("loading session %s: %w", opts.SessionID, err)
	}
	if sess == nil {
		return fmt.Errorf("session %s not found", opts.SessionID)
	}

	// Load registry capabilities via the rich scanner (NOT store.ListCapabilities:
	// that one returns PersistedCapability which lacks Description, and the
	// keyword recommender needs Description for matching).
	regSvc, err := opts.Factory.RegistryService()
	if err != nil {
		return fmt.Errorf("initializing registry service: %w", err)
	}

	project, err := regSvc.ScanProject(sess.ProjectPath)
	if err != nil {
		return fmt.Errorf("scanning project registry at %s: %w", sess.ProjectPath, err)
	}

	// Run the observer.
	observation := skillobs.Observe(sess.Messages, project.Capabilities)
	if observation == nil {
		if opts.JSON {
			return writeJSON(out, map[string]any{
				"session_id":   opts.SessionID,
				"project_path": sess.ProjectPath,
				"observation":  nil,
				"message":      "no skills available for this project",
			})
		}
		fmt.Fprintln(out, "No skills available for this project's registry.")
		return nil
	}

	if opts.JSON {
		return writeJSON(out, map[string]any{
			"session_id":       opts.SessionID,
			"project_path":     sess.ProjectPath,
			"message_count":    len(sess.Messages),
			"capability_count": len(project.Capabilities),
			"observation":      observation,
		})
	}

	return render(out, opts, sess.ProjectPath, len(sess.Messages), len(project.Capabilities), observation)
}

// --- Rendering ---

func render(out io.Writer, opts *Options, projectPath string, msgCount, capCount int, obs *analysis.SkillObservation) error {
	if opts.Quiet {
		fmt.Fprintf(out,
			"available=%d  recommended=%d  loaded=%d  missed=%d  discovered=%d\n",
			len(obs.Available), len(obs.Recommended),
			len(obs.Loaded), len(obs.Missed), len(obs.Discovered),
		)
		return nil
	}

	fmt.Fprintf(out, "=== SKILL OBSERVATION ===\n")
	fmt.Fprintf(out, "Session:      %s\n", opts.SessionID)
	fmt.Fprintf(out, "Project:      %s\n", projectPath)
	fmt.Fprintf(out, "Messages:     %d\n", msgCount)
	fmt.Fprintf(out, "Capabilities: %d (from registry scan)\n\n", capCount)

	writeSection(out, "Available", obs.Available,
		"all skills known for this project")
	writeSection(out, "Recommended", obs.Recommended,
		"skills the keyword recommender matched on user messages")
	writeSection(out, "Loaded", obs.Loaded,
		"skills actually detected as loaded during the session")
	writeSection(out, "Missed", obs.Missed,
		"recommended but NOT loaded — potential gap")
	writeSection(out, "Discovered", obs.Discovered,
		"loaded but NOT recommended — recommender coverage gap")

	// Verdict line.
	switch {
	case len(obs.Missed) == 0 && len(obs.Discovered) == 0 && len(obs.Recommended) > 0:
		fmt.Fprintln(out, "Verdict: clean — recommended skills were all loaded.")
	case len(obs.Missed) > 0:
		fmt.Fprintf(out, "Verdict: %d missed skill(s) — consider tightening the agent prompt or skill triggers.\n", len(obs.Missed))
	case len(obs.Discovered) > 0:
		fmt.Fprintf(out, "Verdict: %d discovered skill(s) — recommender keyword coverage is incomplete.\n", len(obs.Discovered))
	default:
		fmt.Fprintln(out, "Verdict: no recommendations matched — session had no skill-triggering content.")
	}
	return nil
}

func writeSection(out io.Writer, label string, items []string, hint string) {
	fmt.Fprintf(out, "%s (%d) — %s\n", label, len(items), hint)
	if len(items) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else {
		fmt.Fprintf(out, "  %s\n", strings.Join(items, ", "))
	}
	fmt.Fprintln(out)
}

// --- Helpers ---

func writeJSON(out io.Writer, payload any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}
