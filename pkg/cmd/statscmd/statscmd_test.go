package statscmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func statsTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
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

func TestNewCmdStats_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdStats(f)

	flags := []string{"branch", "provider", "all"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestStats_noSessions(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := statsTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runStats(opts)
	if err != nil {
		t.Fatalf("runStats() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No sessions found") {
		t.Errorf("expected 'No sessions found', got: %s", output)
	}
}

func TestStats_withSessions(t *testing.T) {
	store := testutil.NewMockStore()

	// Save sessions with file changes
	s1 := &session.Session{
		ID:       "stats-1",
		Provider: session.ProviderClaudeCode,
		Branch:   "feature-auth",
		Messages: make([]session.Message, 5),
		TokenUsage: session.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
			TotalTokens:  1500,
		},
		FileChanges: []session.FileChange{
			{FilePath: "auth.go", ChangeType: session.ChangeCreated},
			{FilePath: "main.go", ChangeType: session.ChangeModified},
		},
		CreatedAt: time.Now(),
	}
	s2 := &session.Session{
		ID:       "stats-2",
		Provider: session.ProviderOpenCode,
		Branch:   "feature-api",
		Messages: make([]session.Message, 10),
		TokenUsage: session.TokenUsage{
			InputTokens:  3000,
			OutputTokens: 2000,
			TotalTokens:  5000,
		},
		FileChanges: []session.FileChange{
			{FilePath: "api.go", ChangeType: session.ChangeCreated},
			{FilePath: "main.go", ChangeType: session.ChangeModified},
		},
		CreatedAt: time.Now(),
	}
	_ = store.Save(s1)
	_ = store.Save(s2)

	f, ios := statsTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runStats(opts)
	if err != nil {
		t.Fatalf("runStats() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	// Check overall stats
	if !strings.Contains(output, "Sessions:  2") {
		t.Errorf("expected 2 sessions in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Messages:  15") {
		t.Errorf("expected 15 messages in output, got:\n%s", output)
	}
	if !strings.Contains(output, "6.5k") {
		t.Errorf("expected 6.5k total tokens in output, got:\n%s", output)
	}

	// Check by provider
	if !strings.Contains(output, "claude-code") {
		t.Error("expected claude-code in provider stats")
	}
	if !strings.Contains(output, "opencode") {
		t.Error("expected opencode in provider stats")
	}

	// Check by branch
	if !strings.Contains(output, "feature-auth") {
		t.Error("expected feature-auth in branch stats")
	}
	if !strings.Contains(output, "feature-api") {
		t.Error("expected feature-api in branch stats")
	}

	// Check most touched files
	if !strings.Contains(output, "main.go") {
		t.Error("expected main.go in top files")
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		input int
	}{
		{"zero", "0", 0},
		{"small", "500", 500},
		{"thousands", "57.0k", 57000},
		{"millions", "1.5M", 1500000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTokens(tt.input)
			if got != tt.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		maxLen int
	}{
		{"short", "hello", "hello", 10},
		{"exact", "hello", "hello", 5},
		{"long", "hello world", "hello w…", 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
