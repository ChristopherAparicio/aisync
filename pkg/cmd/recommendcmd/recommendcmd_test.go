package recommendcmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// testFactory creates a Factory wired to a temp SQLite store, optionally
// pre-seeded with a few RecommendationRecords. Each seed MUST carry a
// unique Fingerprint — the store's upsert uses it as the ON CONFLICT key.
// Returns the factory plus the raw store so callers can assert state.
func testFactory(t *testing.T, seed ...session.RecommendationRecord) (*cmdutil.Factory, storage.Store) {
	t.Helper()

	store, err := sqlite.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	for i := range seed {
		if err := store.UpsertRecommendation(&seed[i]); err != nil {
			t.Fatalf("upserting seed rec %d: %v", i, err)
		}
	}

	svc := service.NewSessionService(service.SessionServiceConfig{Store: store})

	f := &cmdutil.Factory{
		IOStreams: iostreams.Test(),
		StoreFunc: func() (storage.Store, error) { return store, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return svc, nil
		},
	}
	return f, store
}

// captureOut replaces the factory's Out buffer with a fresh one and wires
// it through the cobra command — mirroring the diagnosecmd pattern.
func captureOut(f *cmdutil.Factory) *bytes.Buffer {
	out := &bytes.Buffer{}
	f.IOStreams.Out = out
	return out
}

// ── stored path ──

// TestNewCmdRecommend_storedEmpty exercises the default path (read from
// store) with an empty store — the command should print a helpful hint
// and succeed, not error.
func TestNewCmdRecommend_storedEmpty(t *testing.T) {
	f, _ := testFactory(t)
	cmd := NewCmdRecommend(f)

	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "No recommendations found") {
		t.Errorf("expected 'No recommendations found' hint, got: %q", output)
	}
}

// TestNewCmdRecommend_storedHumanReadable verifies the default (stored,
// non-JSON, non-quiet) render path — headings, counts, and rec titles.
func TestNewCmdRecommend_storedHumanReadable(t *testing.T) {
	seed := []session.RecommendationRecord{
		{
			ProjectPath: "/tmp/project-a",
			Type:        "model-swap",
			Priority:    "high",
			Icon:        "!!",
			Title:       "Swap gpt-4 for gpt-4o-mini",
			Message:     "Cheaper and faster for routine tasks",
			Impact:      "save ~$120/mo",
			Fingerprint: "fp-human-1",
		},
		{
			ProjectPath: "/tmp/project-a",
			Type:        "skill-trigger",
			Priority:    "low",
			Icon:        "i",
			Title:       "Consider tightening skill triggers",
			Fingerprint: "fp-human-2",
		},
	}
	f, _ := testFactory(t, seed...)

	cmd := NewCmdRecommend(f)
	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--project", "/tmp/project-a"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "RECOMMENDATIONS (stored)") {
		t.Errorf("expected stored header, got: %q", output)
	}
	if !strings.Contains(output, "Swap gpt-4 for gpt-4o-mini") {
		t.Errorf("expected first rec title, got: %q", output)
	}
	if !strings.Contains(output, "Consider tightening skill triggers") {
		t.Errorf("expected second rec title, got: %q", output)
	}
}

// TestNewCmdRecommend_storedPriorityFilter verifies the --priority flag
// is propagated into the store filter and narrows the result set.
func TestNewCmdRecommend_storedPriorityFilter(t *testing.T) {
	seed := []session.RecommendationRecord{
		{
			ProjectPath: "/tmp/proj",
			Priority:    "high",
			Title:       "High impact thing",
			Fingerprint: "fp-prio-1",
		},
		{
			ProjectPath: "/tmp/proj",
			Priority:    "low",
			Title:       "Low priority thing",
			Fingerprint: "fp-prio-2",
		},
	}
	f, _ := testFactory(t, seed...)

	cmd := NewCmdRecommend(f)
	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--project", "/tmp/proj", "--priority", "high"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "High impact thing") {
		t.Errorf("expected high-priority rec in output, got: %q", output)
	}
	if strings.Contains(output, "Low priority thing") {
		t.Errorf("expected low-priority rec to be filtered out, got: %q", output)
	}
}

// TestNewCmdRecommend_storedJSON checks that --json emits a valid
// payload with the expected envelope fields.
func TestNewCmdRecommend_storedJSON(t *testing.T) {
	seed := []session.RecommendationRecord{
		{
			ProjectPath: "/tmp/json-proj",
			Priority:    "medium",
			Title:       "JSON test",
			Fingerprint: "fp-json-1",
		},
	}
	f, _ := testFactory(t, seed...)

	cmd := NewCmdRecommend(f)
	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--project", "/tmp/json-proj", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var envelope struct {
		Source          string                         `json:"source"`
		ProjectPath     string                         `json:"project_path"`
		Total           int                            `json:"total"`
		Recommendations []session.RecommendationRecord `json:"recommendations"`
		Stats           session.RecommendationStats    `json:"stats"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal JSON output: %v — raw: %s", err, out.String())
	}

	if envelope.Source != "stored" {
		t.Errorf("expected source=stored, got %q", envelope.Source)
	}
	if envelope.Total != 1 {
		t.Errorf("expected total=1, got %d", envelope.Total)
	}
	if len(envelope.Recommendations) != 1 || envelope.Recommendations[0].Title != "JSON test" {
		t.Errorf("unexpected recommendations payload: %+v", envelope.Recommendations)
	}
}

// TestNewCmdRecommend_storedQuiet verifies the --quiet one-liner mode.
func TestNewCmdRecommend_storedQuiet(t *testing.T) {
	seed := []session.RecommendationRecord{
		{
			ProjectPath: "/tmp/p",
			Priority:    "high",
			Icon:        "!",
			Title:       "First",
			Fingerprint: "fp-quiet-1",
		},
		{
			ProjectPath: "/tmp/p",
			Priority:    "medium",
			Icon:        "~",
			Title:       "Second",
			Fingerprint: "fp-quiet-2",
		},
	}
	f, _ := testFactory(t, seed...)

	cmd := NewCmdRecommend(f)
	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--project", "/tmp/p", "--quiet"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 quiet lines, got %d: %q", len(lines), out.String())
	}
	// ListRecommendations orders by priority: high > medium, so the high
	// priority one must come first.
	if !strings.Contains(lines[0], "[HIGH]") || !strings.Contains(lines[0], "First") {
		t.Errorf("first quiet line unexpected: %q", lines[0])
	}
	if !strings.Contains(lines[1], "[MEDIUM]") || !strings.Contains(lines[1], "Second") {
		t.Errorf("second quiet line unexpected: %q", lines[1])
	}
}

// TestNewCmdRecommend_storedLimit verifies the --limit flag truncates
// the result set.
func TestNewCmdRecommend_storedLimit(t *testing.T) {
	seed := []session.RecommendationRecord{
		{
			ProjectPath: "/tmp/limit", Priority: "high", Title: "One",
			Fingerprint: "fp-lim-1",
		},
		{
			ProjectPath: "/tmp/limit", Priority: "high", Title: "Two",
			Fingerprint: "fp-lim-2",
		},
		{
			ProjectPath: "/tmp/limit", Priority: "high", Title: "Three",
			Fingerprint: "fp-lim-3",
		},
	}
	f, _ := testFactory(t, seed...)

	cmd := NewCmdRecommend(f)
	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--project", "/tmp/limit", "--limit", "2", "--quiet"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines with --limit=2, got %d: %q", len(lines), out.String())
	}
}

// ── fresh path ──

// TestNewCmdRecommend_freshEmpty exercises the expensive regen path with
// no sessions — GenerateRecommendations returns an empty slice, which
// the renderer should report as "No recommendations detected." without
// erroring.
func TestNewCmdRecommend_freshEmpty(t *testing.T) {
	f, _ := testFactory(t)
	cmd := NewCmdRecommend(f)

	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--fresh", "--project", "/nonexistent"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "No recommendations detected") {
		t.Errorf("expected 'No recommendations detected', got: %q", output)
	}
}

// TestNewCmdRecommend_freshJSON verifies --fresh + --json emits the
// expected envelope with source=fresh.
func TestNewCmdRecommend_freshJSON(t *testing.T) {
	f, _ := testFactory(t)
	cmd := NewCmdRecommend(f)

	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--fresh", "--project", "/nonexistent", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var envelope struct {
		Source          string                   `json:"source"`
		ProjectPath     string                   `json:"project_path"`
		Total           int                      `json:"total"`
		Recommendations []session.Recommendation `json:"recommendations"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal JSON: %v — raw: %s", err, out.String())
	}
	if envelope.Source != "fresh" {
		t.Errorf("expected source=fresh, got %q", envelope.Source)
	}
	if envelope.Total != 0 {
		t.Errorf("expected total=0 on empty store, got %d", envelope.Total)
	}
}

// ── pure unit tests ──

// TestFilterByPriority covers the tiny helper used by the fresh path.
func TestFilterByPriority(t *testing.T) {
	recs := []session.Recommendation{
		{Priority: "high", Title: "a"},
		{Priority: "medium", Title: "b"},
		{Priority: "high", Title: "c"},
	}

	got := filterByPriority(recs, "high")
	if len(got) != 2 {
		t.Fatalf("expected 2 high recs, got %d", len(got))
	}
	for _, r := range got {
		if r.Priority != "high" {
			t.Errorf("expected priority=high, got %q", r.Priority)
		}
	}

	if got := filterByPriority(recs, "critical"); len(got) != 0 {
		t.Errorf("expected 0 for missing priority, got %d", len(got))
	}
}

// TestBuildMetaLine verifies the meta line builder skips empty fields.
func TestBuildMetaLine(t *testing.T) {
	cases := []struct {
		name     string
		recType  string
		agent    string
		skill    string
		impact   string
		contains []string
		empty    bool
	}{
		{
			name:     "all populated",
			recType:  "swap",
			agent:    "coder",
			skill:    "bash",
			impact:   "save 10%",
			contains: []string{"type=swap", "agent=coder", "skill=bash", `impact="save 10%"`},
		},
		{
			name:     "only type",
			recType:  "swap",
			contains: []string{"type=swap"},
		},
		{
			name:  "all empty",
			empty: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildMetaLine(tc.recType, tc.agent, tc.skill, tc.impact)
			if tc.empty {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
				return
			}
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("expected %q in %q", want, got)
				}
			}
		})
	}
}
