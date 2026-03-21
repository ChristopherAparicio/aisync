package listcmd

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

func listTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
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

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		input int
	}{
		{"zero", "-", 0},
		{"small", "500", 500},
		{"thousands", "57k", 57000},
		{"exact thousand", "1k", 1000},
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

func TestTimeAgo(t *testing.T) {
	tests := []struct {
		name string
		want string
		d    time.Duration
	}{
		{"just now", "just now", 30 * time.Second},
		{"minutes", "5 min ago", 5 * time.Minute},
		{"hours", "3 hours ago", 3 * time.Hour},
		{"1 day", "1 day ago", 25 * time.Hour},
		{"days", "3 days ago", 72 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := timeAgo(time.Now().Add(-tt.d))
			if got != tt.want {
				t.Errorf("timeAgo(-%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}

	t.Run("zero time", func(t *testing.T) {
		got := timeAgo(time.Time{})
		if got != "-" {
			t.Errorf("timeAgo(zero) = %q, want %q", got, "-")
		}
	})
}

func TestNewCmdList_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdList(f)

	flags := []string{"all", "quiet", "pr"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestList_emptyBranch(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := listTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runList(opts)
	if err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No sessions found") {
		t.Error("expected 'No sessions found' message")
	}
}

func TestList_withSessions(t *testing.T) {
	store := testutil.NewMockStore()
	store.Summaries = []session.Summary{
		{
			ID:           "test-session-1",
			Provider:     session.ProviderClaudeCode,
			Branch:       "main",
			MessageCount: 10,
			TotalTokens:  5000,
			CreatedAt:    time.Now(),
		},
	}

	f, ios := listTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runList(opts)
	if err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	// ID is truncated to 12 chars: "test-sessio…"
	if !strings.Contains(output, "test-sessio") {
		t.Error("expected session ID in output")
	}
	if !strings.Contains(output, "claude-code") {
		t.Error("expected provider name in output")
	}
}

func TestList_quiet(t *testing.T) {
	store := testutil.NewMockStore()
	store.Summaries = []session.Summary{
		{
			ID:       "quiet-session-1",
			Provider: session.ProviderClaudeCode,
			Branch:   "main",
		},
	}

	f, ios := listTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		Quiet:   true,
	}

	err := runList(opts)
	if err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "quiet-session-1") {
		t.Error("expected session ID in quiet output")
	}
	// Quiet mode should NOT contain headers
	if strings.Contains(output, "PROVIDER") {
		t.Error("quiet mode should not contain table headers")
	}
}

func TestList_byPR(t *testing.T) {
	store := testutil.NewMockStore()
	// Pre-populate the link data
	store.Links["pr:42"] = []session.Summary{
		{
			ID:           "pr-linked-session",
			Provider:     session.ProviderClaudeCode,
			Branch:       "feature-x",
			MessageCount: 5,
			TotalTokens:  2000,
			CreatedAt:    time.Now(),
		},
	}

	f, ios := listTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		PRFlag:  42,
	}

	err := runList(opts)
	if err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	// ID is truncated to 12 chars: "pr-linked-s…"
	if !strings.Contains(output, "pr-linked-s") {
		t.Error("expected session ID in PR list output")
	}
}

func TestList_byPR_notFound(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := listTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		PRFlag:  999,
	}

	err := runList(opts)
	if err == nil {
		t.Fatal("expected error when no sessions linked to PR")
	}
	// Service returns session.ErrSessionNotFound when no sessions linked to a PR
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestList_byPR_quiet(t *testing.T) {
	store := testutil.NewMockStore()
	store.Links["pr:10"] = []session.Summary{
		{
			ID:       "pr-quiet-session",
			Provider: session.ProviderOpenCode,
			Branch:   "fix-bug",
		},
	}

	f, ios := listTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		PRFlag:  10,
		Quiet:   true,
	}

	err := runList(opts)
	if err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "pr-quiet-session") {
		t.Error("expected session ID in quiet PR output")
	}
	if strings.Contains(output, "PROVIDER") {
		t.Error("quiet mode should not contain table headers")
	}
}

func TestList_similar(t *testing.T) {
	store := testutil.NewMockStore()

	s1 := &session.Session{
		ID:       "target",
		Provider: session.ProviderClaudeCode,
		Branch:   "main",
		FileChanges: []session.FileChange{
			{FilePath: "auth.go"},
			{FilePath: "main.go"},
			{FilePath: "config.go"},
		},
		CreatedAt: time.Now(),
	}
	s2 := &session.Session{
		ID:       "similar-1",
		Provider: session.ProviderOpenCode,
		Branch:   "main",
		Summary:  "similar session",
		FileChanges: []session.FileChange{
			{FilePath: "auth.go"},
			{FilePath: "main.go"},
		},
		CreatedAt: time.Now(),
	}
	s3 := &session.Session{
		ID:       "different",
		Provider: session.ProviderClaudeCode,
		Branch:   "main",
		Summary:  "different session",
		FileChanges: []session.FileChange{
			{FilePath: "api.go"},
			{FilePath: "handler.go"},
		},
		CreatedAt: time.Now(),
	}
	_ = store.Save(s1)
	_ = store.Save(s2)
	_ = store.Save(s3)

	store.Summaries = []session.Summary{
		{ID: "target", Provider: session.ProviderClaudeCode, Branch: "main"},
		{ID: "similar-1", Provider: session.ProviderOpenCode, Branch: "main", Summary: "similar session"},
		{ID: "different", Provider: session.ProviderClaudeCode, Branch: "main", Summary: "different session"},
	}

	f, ios := listTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		Similar: "target",
	}

	err := runList(opts)
	if err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "similar-1") {
		t.Error("expected similar-1 in output")
	}
	if strings.Contains(output, "different") {
		t.Error("session 'different' should not appear (no file overlap)")
	}
	if !strings.Contains(output, "SIMILARITY") {
		t.Error("expected SIMILARITY header")
	}
}

func TestList_similar_noFiles(t *testing.T) {
	store := testutil.NewMockStore()

	s1 := &session.Session{
		ID:        "no-files",
		Provider:  session.ProviderClaudeCode,
		Branch:    "main",
		CreatedAt: time.Now(),
	}
	_ = store.Save(s1)
	store.Summaries = []session.Summary{{ID: "no-files", Provider: session.ProviderClaudeCode, Branch: "main"}}

	f, ios := listTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		Similar: "no-files",
	}

	err := runList(opts)
	if err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "no file changes") {
		t.Error("expected 'no file changes' message")
	}
}
