package diffcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func diffTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)

	if store == nil {
		store = testutil.NewMockStore()
	}
	gitClient := git.NewClient(repoDir)

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (storage.Store, error) { return store, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store: store,
				Git:   gitClient,
			}), nil
		},
	}

	return f, ios
}

// populateDiffSessions creates two sessions with different providers and branches
// and adds them to the mock store.
func populateDiffSessions(store *testutil.MockStore) {
	left := testutil.NewSession("left-id")
	left.Provider = session.ProviderClaudeCode
	left.Branch = "feature/left"
	left.TokenUsage = session.TokenUsage{
		InputTokens:  100,
		OutputTokens: 200,
		TotalTokens:  300,
	}
	left.Messages = []session.Message{
		{ID: "m1", Role: session.RoleUser, Content: "shared prompt"},
		{ID: "m2", Role: session.RoleAssistant, Content: "left response"},
	}
	left.FileChanges = []session.FileChange{
		{FilePath: "shared.go", ChangeType: session.ChangeModified},
		{FilePath: "left-only.go", ChangeType: session.ChangeCreated},
	}

	right := testutil.NewSession("right-id")
	right.Provider = session.ProviderOpenCode
	right.Branch = "feature/right"
	right.TokenUsage = session.TokenUsage{
		InputTokens:  150,
		OutputTokens: 350,
		TotalTokens:  500,
	}
	right.Messages = []session.Message{
		{ID: "m1", Role: session.RoleUser, Content: "shared prompt"},
		{ID: "m3", Role: session.RoleAssistant, Content: "right response"},
	}
	right.FileChanges = []session.FileChange{
		{FilePath: "shared.go", ChangeType: session.ChangeModified},
		{FilePath: "right-only.go", ChangeType: session.ChangeCreated},
	}

	store.Sessions[session.ID("left-id")] = left
	store.Sessions[session.ID("right-id")] = right
}

func TestNewCmdDiff_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdDiff(f)

	if cmd.Flags().Lookup("json") == nil {
		t.Error("expected --json flag")
	}
}

func TestDiff_textOutput(t *testing.T) {
	store := testutil.NewMockStore()
	populateDiffSessions(store)

	f, ios := diffTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		JSON:    false,
	}

	err := runDiff(opts, "left-id", "right-id")
	if err != nil {
		t.Fatalf("runDiff() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	checks := []string{
		"left-id",
		"right-id",
		"Token delta",
		"Messages",
		"claude-code",
		"opencode",
	}
	for _, want := range checks {
		if !strings.Contains(output, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestDiff_jsonOutput(t *testing.T) {
	store := testutil.NewMockStore()
	populateDiffSessions(store)

	f, ios := diffTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		JSON:    true,
	}

	err := runDiff(opts, "left-id", "right-id")
	if err != nil {
		t.Fatalf("runDiff() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	checks := []string{
		`"left"`,
		`"right"`,
		`"token_delta"`,
		`"message_delta"`,
	}
	for _, want := range checks {
		if !strings.Contains(output, want) {
			t.Errorf("expected JSON to contain %q, got:\n%s", want, output)
		}
	}
}

func TestDiff_serviceError(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return nil, session.ErrConfigNotFound
		},
	}

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runDiff(opts, "left-id", "right-id")
	if err == nil {
		t.Fatal("expected error from runDiff when service fails")
	}
	if !strings.Contains(err.Error(), "initializing service") {
		t.Errorf("expected 'initializing service' in error, got: %v", err)
	}
}

func TestFormatDelta(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  string
	}{
		{"positive", 42, "+42"},
		{"zero", 0, "0"},
		{"negative", -10, "-10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDelta(tt.input)
			if got != tt.want {
				t.Errorf("formatDelta(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatCostDelta(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  string
	}{
		{"positive", 0.0050, "+$0.0050"},
		{"zero", 0.0, "$0.0000"},
		{"negative", -0.0123, "-$0.0123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCostDelta(tt.input)
			if got != tt.want {
				t.Errorf("formatCostDelta(%f) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
