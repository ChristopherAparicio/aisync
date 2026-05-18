package show

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

func showTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
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

func TestShow_displaysRemoteAndProject(t *testing.T) {
	store := testutil.NewMockStore()
	sess := &session.Session{
		ID:          "ses-show-remote",
		Provider:    session.ProviderOpenCode,
		Agent:       "opencode",
		Branch:      "opencode/sunny-cabin",
		RemoteURL:   "github.com/anomalyco/opencode",
		ProjectPath: "/home/u/.local/share/opencode/worktree/abc/sunny-cabin",
		StorageMode: session.StorageModeFull,
		CreatedAt:   time.Now(),
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	f, ios := showTestFactory(t, store)
	opts := &Options{IO: ios, Factory: f}
	if err := runShow(opts, "ses-show-remote"); err != nil {
		t.Fatalf("runShow: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Remote:   github.com/anomalyco/opencode") {
		t.Errorf("expected Remote line in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Project:  /home/u/.local/share/opencode/worktree/abc/sunny-cabin") {
		t.Errorf("expected Project line in output, got:\n%s", output)
	}
}

func TestShow_omitsRemoteAndProjectWhenEmpty(t *testing.T) {
	store := testutil.NewMockStore()
	sess := &session.Session{
		ID:          "ses-show-bare",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude-code",
		Branch:      "master",
		StorageMode: session.StorageModeFull,
		CreatedAt:   time.Now(),
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	f, ios := showTestFactory(t, store)
	opts := &Options{IO: ios, Factory: f}
	if err := runShow(opts, "ses-show-bare"); err != nil {
		t.Fatalf("runShow: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if strings.Contains(output, "Remote:") {
		t.Errorf("Remote line should be omitted when empty, got:\n%s", output)
	}
	if strings.Contains(output, "Project:") {
		t.Errorf("Project line should be omitted when empty, got:\n%s", output)
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		input int
	}{
		{"zero", "0", 0},
		{"small", "100", 100},
		{"thousands", "1,234", 1234},
		{"tens of thousands", "57,000", 57000},
		{"millions", "1,234,567", 1234567},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatNumber(tt.input)
			if got != tt.want {
				t.Errorf("formatNumber(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
