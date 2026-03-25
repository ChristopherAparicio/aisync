package backfillcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// EventsBackfillResult holds the outcome of the events backfill.
type EventsBackfillResult struct {
	TotalSessions     int  `json:"total_sessions"`
	Processed         int  `json:"processed"`
	Skipped           int  `json:"skipped"`
	Errors            int  `json:"errors"`
	BucketsRecomputed bool `json:"buckets_recomputed"`
}

func newCmdBackfillEvents(f *cmdutil.Factory) *cobra.Command {
	var (
		jsonOutput bool
		sessionID  string
		recompute  bool
	)

	cmd := &cobra.Command{
		Use:   "events",
		Short: "Extract session events and recompute analytics buckets",
		Long: `Process sessions to extract structured events (tool calls, skills,
commands, agents, errors, images) and populate the analytics dashboard.

This is useful for:
  - Backfilling events for sessions captured before the events feature
  - Fixing stale analytics after garbage collection
  - Recomputing all event buckets from scratch

By default, processes ALL sessions. Use --session to process a single one.
Use --recompute-only to just rebuild buckets without re-extracting events.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			eventSvc, err := f.SessionEventService()
			if err != nil {
				return fmt.Errorf("session event service: %w", err)
			}

			result := EventsBackfillResult{}

			// Recompute-only mode: just rebuild buckets from existing events.
			if recompute {
				fmt.Fprintln(f.IOStreams.Out, "Recomputing all event buckets from stored events...")
				if err := eventSvc.RecomputeAllBuckets(); err != nil {
					return fmt.Errorf("recompute buckets: %w", err)
				}
				result.BucketsRecomputed = true
				if jsonOutput {
					data, _ := json.MarshalIndent(result, "", "  ")
					fmt.Fprintln(f.IOStreams.Out, string(data))
				} else {
					fmt.Fprintln(f.IOStreams.Out, "Bucket recomputation complete.")
				}
				return nil
			}

			sessSvc, err := f.SessionService()
			if err != nil {
				return fmt.Errorf("session service: %w", err)
			}

			// Single session mode.
			if sessionID != "" {
				fmt.Fprintf(f.IOStreams.Out, "Processing session %s...\n", sessionID)
				sess, getErr := resolveSession(sessSvc, session.ID(sessionID))
				if getErr != nil {
					return getErr
				}
				if err := eventSvc.ReprocessSession(sess); err != nil {
					return fmt.Errorf("processing session %s: %w", sessionID, err)
				}
				result.TotalSessions = 1
				result.Processed = 1
				printResult(f, result, jsonOutput)
				return nil
			}

			// All sessions mode.
			ctx := context.Background()
			summaries, listErr := sessSvc.List(service.ListRequest{All: true})
			if listErr != nil {
				return fmt.Errorf("listing sessions: %w", listErr)
			}

			result.TotalSessions = len(summaries)
			fmt.Fprintf(f.IOStreams.Out, "Processing %d sessions...\n", len(summaries))

			start := time.Now()
			for i, sm := range summaries {
				_ = ctx // keep ctx for future use
				sess, getErr := resolveSession(sessSvc, sm.ID)
				if getErr != nil {
					result.Skipped++
					continue
				}

				if err := eventSvc.ProcessSession(sess); err != nil {
					result.Errors++
					continue
				}
				result.Processed++

				// Progress every 50 sessions.
				if (i+1)%50 == 0 {
					fmt.Fprintf(f.IOStreams.Out, "  %d/%d processed...\n", i+1, len(summaries))
				}
			}

			elapsed := time.Since(start).Round(time.Millisecond)
			fmt.Fprintf(f.IOStreams.Out, "Done in %s.\n", elapsed)
			printResult(f, result, jsonOutput)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	cmd.Flags().StringVar(&sessionID, "session", "", "Process a single session by ID")
	cmd.Flags().BoolVar(&recompute, "recompute-only", false, "Only recompute buckets from existing events (no re-extraction)")

	return cmd
}

func resolveSession(svc service.SessionServicer, id session.ID) (*session.Session, error) {
	sess, err := svc.Get(string(id))
	if err != nil {
		return nil, fmt.Errorf("get session %s: %w", id, err)
	}
	return sess, nil
}

func printResult(f *cmdutil.Factory, result EventsBackfillResult, jsonOutput bool) {
	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(f.IOStreams.Out, string(data))
		return
	}

	fmt.Fprintf(f.IOStreams.Out, "Backfill complete:\n")
	fmt.Fprintf(f.IOStreams.Out, "  Total sessions: %d\n", result.TotalSessions)
	fmt.Fprintf(f.IOStreams.Out, "  Processed:      %d\n", result.Processed)
	if result.Skipped > 0 {
		fmt.Fprintf(f.IOStreams.Out, "  Skipped:        %d\n", result.Skipped)
	}
	if result.Errors > 0 {
		fmt.Fprintf(f.IOStreams.Out, "  Errors:         %d\n", result.Errors)
	}
}
