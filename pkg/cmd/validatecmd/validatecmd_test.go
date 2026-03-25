package validatecmd

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

func validateTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)
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

func makeValidSession(id string) *session.Session {
	return &session.Session{
		ID:          session.ID(id),
		Provider:    session.ProviderOpenCode,
		Agent:       "opencode",
		Branch:      "main",
		ProjectPath: "/tmp/test",
		Messages: []session.Message{
			{ID: "u1", Role: session.RoleUser, Content: "Hello", Timestamp: time.Now()},
			{
				ID: "a1", Role: session.RoleAssistant, Content: "Writing file...",
				Timestamp: time.Now(),
				ToolCalls: []session.ToolCall{
					{ID: "tool1", Name: "Write", State: session.ToolStateCompleted, Output: "Done"},
				},
			},
			{ID: "u2", Role: session.RoleUser, Content: "Thanks", Timestamp: time.Now()},
		},
	}
}

func makeBrokenSession(id string) *session.Session {
	return &session.Session{
		ID:          session.ID(id),
		Provider:    session.ProviderOpenCode,
		Agent:       "opencode",
		Branch:      "main",
		ProjectPath: "/tmp/test",
		Messages: []session.Message{
			{ID: "u1", Role: session.RoleUser, Content: "Start", Timestamp: time.Now()},
			{
				ID: "a1", Role: session.RoleAssistant, Content: "Running tool...",
				Timestamp: time.Now(),
				ToolCalls: []session.ToolCall{
					{ID: "toolu_01BROKEN", Name: "Write", State: session.ToolStatePending},
				},
			},
			// No tool_result — session ends broken
		},
	}
}

func TestValidate_validSession(t *testing.T) {
	sess := makeValidSession("valid-001")
	store := testutil.NewMockStore()
	store.Sessions[sess.ID] = sess

	f, ios := validateTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
	}

	err := runValidate(opts)
	if err != nil {
		t.Fatalf("runValidate() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "valid") {
		t.Errorf("expected 'valid' in output, got: %s", output)
	}
}

func TestValidate_brokenSession(t *testing.T) {
	sess := makeBrokenSession("broken-001")
	store := testutil.NewMockStore()
	store.Sessions[sess.ID] = sess

	f, ios := validateTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
	}

	err := runValidate(opts)
	if err == nil {
		t.Fatal("expected error for broken session")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected 'validation failed' in error, got: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "toolu_01BROKEN") {
		t.Errorf("expected tool ID 'toolu_01BROKEN' in output, got: %s", output)
	}
	if !strings.Contains(output, "Write") {
		t.Errorf("expected tool name 'Write' in output, got: %s", output)
	}
	if !strings.Contains(output, "rewind") {
		t.Errorf("expected rewind suggestion in output, got: %s", output)
	}
}

func TestValidate_jsonOutput(t *testing.T) {
	sess := makeBrokenSession("json-001")
	store := testutil.NewMockStore()
	store.Sessions[sess.ID] = sess

	f, ios := validateTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
		JSON:      true,
	}

	// JSON output should not return error even for broken sessions
	err := runValidate(opts)
	if err != nil {
		t.Fatalf("JSON mode should not error, got: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "\"valid\":") {
		t.Errorf("expected JSON with 'valid' field, got: %s", output)
	}
	if !strings.Contains(output, "orphan_tool_use") {
		t.Errorf("expected JSON with orphan_tool_use issue, got: %s", output)
	}
}

func TestValidate_quietMode(t *testing.T) {
	sess := makeValidSession("quiet-ok")
	store := testutil.NewMockStore()
	store.Sessions[sess.ID] = sess

	f, ios := validateTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
		Quiet:     true,
	}

	err := runValidate(opts)
	if err != nil {
		t.Fatalf("quiet valid should not error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if output != "" {
		t.Errorf("quiet mode should produce no output for valid session, got: %s", output)
	}
}

func TestValidate_quietModeInvalid(t *testing.T) {
	sess := makeBrokenSession("quiet-fail")
	store := testutil.NewMockStore()
	store.Sessions[sess.ID] = sess

	f, ios := validateTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
		Quiet:     true,
	}

	err := runValidate(opts)
	if err == nil {
		t.Fatal("quiet mode for broken session should return error")
	}
}

func TestValidate_autoFix(t *testing.T) {
	sess := makeBrokenSession("fix-001")
	store := testutil.NewMockStore()
	store.Sessions[sess.ID] = sess

	f, ios := validateTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
		Fix:       true,
	}

	// Auto-fix should still return error (validation failed) but also create fixed session
	err := runValidate(opts)
	if err == nil {
		t.Fatal("expected validation error even with fix")
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Auto-fixing") {
		t.Errorf("expected auto-fix message in output, got: %s", output)
	}
	if !strings.Contains(output, "Created fixed session") {
		t.Errorf("expected 'Created fixed session' in output, got: %s", output)
	}

	// Verify a new session was saved
	if len(store.Sessions) != 2 {
		t.Errorf("expected 2 sessions (original + fixed), got %d", len(store.Sessions))
	}
}

func TestNewCmdValidate_flags(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.Test()}
	cmd := NewCmdValidate(f)

	flags := []string{"fix", "json", "quiet"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag to be registered", name)
		}
	}
}
