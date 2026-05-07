package tagcmd

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

func tagTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
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

// ── parseSessionAndTags ──

func TestParseSessionAndTags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantID   session.ID
		wantTags []string
		wantErr  bool
	}{
		{"empty", nil, "", nil, false},
		{"only tags", []string{"bug", "urgent"}, "", []string{"bug", "urgent"}, false},
		{"id then tags", []string{"ses_abc12345", "bug"}, "ses_abc12345", []string{"bug"}, false},
		{"id only", []string{"ses_abc12345"}, "ses_abc12345", []string{}, false},
		{"single tag no id", []string{"hotfix"}, "", []string{"hotfix"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, tags, err := parseSessionAndTags(c.args)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != c.wantID {
				t.Errorf("id = %q, want %q", id, c.wantID)
			}
			// Treat nil and []string{} as equivalent.
			gotEmpty := len(tags) == 0
			wantEmpty := len(c.wantTags) == 0
			if gotEmpty != wantEmpty {
				t.Errorf("tags = %v, want %v", tags, c.wantTags)
			}
			if !gotEmpty && !wantEmpty {
				if strings.Join(tags, ",") != strings.Join(c.wantTags, ",") {
					t.Errorf("tags = %v, want %v", tags, c.wantTags)
				}
			}
		})
	}
}

// ── NewCmdTag flags ──

func TestNewCmdTag_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdTag(f)

	for _, name := range []string{"remove", "list", "all", "quiet"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

// ── runTag: invalid combinations ──

func TestRunTag_AllExclusiveWithListAndRemove(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := tagTestFactory(t, store)

	opts := &Options{IO: ios, Factory: f, All: true, List: true}
	if err := runTag(opts); err == nil {
		t.Fatal("expected error for --all + --list")
	}

	opts = &Options{IO: ios, Factory: f, All: true, Remove: true}
	if err := runTag(opts); err == nil {
		t.Fatal("expected error for --all + --remove")
	}
}

func TestRunTag_ListAndRemoveMutuallyExclusive(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := tagTestFactory(t, store)
	opts := &Options{IO: ios, Factory: f, List: true, Remove: true}
	if err := runTag(opts); err == nil {
		t.Fatal("expected error for --list + --remove")
	}
}

// ── runTag: add ──

func TestRunTag_AddTags_ToExplicitSession(t *testing.T) {
	store := testutil.NewMockStore()
	id := session.ID("ses_abcdef12")
	store.Sessions[id] = &session.Session{ID: id}

	f, ios := tagTestFactory(t, store)
	opts := &Options{
		IO:      ios,
		Factory: f,
		Args:    []string{string(id), "Bug", "Urgent"},
	}
	if err := runTag(opts); err != nil {
		t.Fatalf("runTag: %v", err)
	}

	out := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "Added 2 tag(s)") {
		t.Errorf("output missing add confirmation: %q", out)
	}

	tags, _ := store.GetTags(id)
	if len(tags) != 2 || tags[0] != "bug" || tags[1] != "urgent" {
		t.Errorf("stored tags = %v, want [bug urgent]", tags)
	}
}

func TestRunTag_AddTags_ReportsAlreadyPresent(t *testing.T) {
	store := testutil.NewMockStore()
	id := session.ID("ses_abcdef12")
	store.Sessions[id] = &session.Session{ID: id}
	if _, err := store.AddTags(id, []string{"bug"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f, ios := tagTestFactory(t, store)
	opts := &Options{
		IO:      ios,
		Factory: f,
		Args:    []string{string(id), "bug", "urgent"},
	}
	if err := runTag(opts); err != nil {
		t.Fatalf("runTag: %v", err)
	}
	out := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "1 already present") {
		t.Errorf("expected 'already present' note, got: %q", out)
	}
}

func TestRunTag_AddTags_NoTagsError(t *testing.T) {
	store := testutil.NewMockStore()
	id := session.ID("ses_abcdef12")
	store.Sessions[id] = &session.Session{ID: id}

	f, ios := tagTestFactory(t, store)
	opts := &Options{
		IO:      ios,
		Factory: f,
		Args:    []string{string(id)}, // id but no tags
	}
	err := runTag(opts)
	if err == nil {
		t.Fatal("expected 'no tags supplied' error")
	}
	if !strings.Contains(err.Error(), "no tags supplied") {
		t.Errorf("err = %v, want 'no tags supplied'", err)
	}
}

func TestRunTag_AddTags_QuietSuppressesOutput(t *testing.T) {
	store := testutil.NewMockStore()
	id := session.ID("ses_abcdef12")
	store.Sessions[id] = &session.Session{ID: id}

	f, ios := tagTestFactory(t, store)
	opts := &Options{
		IO:      ios,
		Factory: f,
		Quiet:   true,
		Args:    []string{string(id), "bug"},
	}
	if err := runTag(opts); err != nil {
		t.Fatalf("runTag: %v", err)
	}
	if got := ios.Out.(*bytes.Buffer).String(); got != "" {
		t.Errorf("quiet output = %q, want empty", got)
	}
}

// ── runTag: remove ──

func TestRunTag_RemoveTags(t *testing.T) {
	store := testutil.NewMockStore()
	id := session.ID("ses_abcdef12")
	store.Sessions[id] = &session.Session{ID: id}
	if _, err := store.AddTags(id, []string{"bug", "urgent"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f, ios := tagTestFactory(t, store)
	opts := &Options{
		IO:      ios,
		Factory: f,
		Remove:  true,
		Args:    []string{string(id), "bug"},
	}
	if err := runTag(opts); err != nil {
		t.Fatalf("runTag: %v", err)
	}
	out := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "Removed 1 tag(s)") {
		t.Errorf("expected removal confirmation, got: %q", out)
	}
	tags, _ := store.GetTags(id)
	if len(tags) != 1 || tags[0] != "urgent" {
		t.Errorf("remaining tags = %v, want [urgent]", tags)
	}
}

// ── runTag: --list ──

func TestRunTag_List_ShowTags(t *testing.T) {
	store := testutil.NewMockStore()
	id := session.ID("ses_abcdef12")
	store.Sessions[id] = &session.Session{ID: id}
	if _, err := store.AddTags(id, []string{"alpha", "beta"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f, ios := tagTestFactory(t, store)
	opts := &Options{
		IO:      ios,
		Factory: f,
		List:    true,
		Args:    []string{string(id)},
	}
	if err := runTag(opts); err != nil {
		t.Fatalf("runTag: %v", err)
	}
	out := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "Tags for") {
		t.Errorf("missing header in: %q", out)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("missing tags in: %q", out)
	}
}

func TestRunTag_List_NoTagsMessage(t *testing.T) {
	store := testutil.NewMockStore()
	id := session.ID("ses_abcdef12")
	store.Sessions[id] = &session.Session{ID: id}

	f, ios := tagTestFactory(t, store)
	opts := &Options{
		IO:      ios,
		Factory: f,
		List:    true,
		Args:    []string{string(id)},
	}
	if err := runTag(opts); err != nil {
		t.Fatalf("runTag: %v", err)
	}
	out := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "has no tags") {
		t.Errorf("expected 'has no tags' message, got: %q", out)
	}
}

func TestRunTag_List_QuietPrintsOnePerLine(t *testing.T) {
	store := testutil.NewMockStore()
	id := session.ID("ses_abcdef12")
	store.Sessions[id] = &session.Session{ID: id}
	if _, err := store.AddTags(id, []string{"alpha", "beta"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f, ios := tagTestFactory(t, store)
	opts := &Options{
		IO:      ios,
		Factory: f,
		List:    true,
		Quiet:   true,
		Args:    []string{string(id)},
	}
	if err := runTag(opts); err != nil {
		t.Fatalf("runTag: %v", err)
	}
	out := ios.Out.(*bytes.Buffer).String()
	if out != "alpha\nbeta\n" {
		t.Errorf("quiet list = %q, want \"alpha\\nbeta\\n\"", out)
	}
}

// ── runTag: --all ──

func TestRunTag_All_ShowsCounts(t *testing.T) {
	store := testutil.NewMockStore()
	id1 := session.ID("ses_111aaa11")
	id2 := session.ID("ses_222bbb22")
	store.Sessions[id1] = &session.Session{ID: id1}
	store.Sessions[id2] = &session.Session{ID: id2}
	if _, err := store.AddTags(id1, []string{"shared", "rare"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := store.AddTags(id2, []string{"shared"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f, ios := tagTestFactory(t, store)
	opts := &Options{IO: ios, Factory: f, All: true}
	if err := runTag(opts); err != nil {
		t.Fatalf("runTag: %v", err)
	}
	out := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "TAG") || !strings.Contains(out, "SESSIONS") {
		t.Errorf("missing table header: %q", out)
	}
	if !strings.Contains(out, "shared") || !strings.Contains(out, "rare") {
		t.Errorf("missing tags in --all output: %q", out)
	}
	// "shared" should appear before "rare" (count DESC).
	sharedIdx := strings.Index(out, "shared")
	rareIdx := strings.Index(out, "rare")
	if sharedIdx == -1 || rareIdx == -1 || sharedIdx > rareIdx {
		t.Errorf("expected 'shared' before 'rare' (count DESC), got: %q", out)
	}
}

func TestRunTag_All_EmptyMessage(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := tagTestFactory(t, store)
	opts := &Options{IO: ios, Factory: f, All: true}
	if err := runTag(opts); err != nil {
		t.Fatalf("runTag: %v", err)
	}
	out := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "No tags yet") {
		t.Errorf("expected empty notice, got: %q", out)
	}
}
