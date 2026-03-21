package rewindcmd

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func rewindTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
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

func makeRewindSession(id string) *session.Session {
	sess := testutil.NewSession(id)
	sess.Messages = []session.Message{
		{Role: "user", Content: "msg1"},
		{Role: "assistant", Content: "msg2"},
		{Role: "user", Content: "msg3"},
		{Role: "assistant", Content: "msg4"},
		{Role: "user", Content: "msg5"},
	}
	return sess
}

func TestNewCmdRewind_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdRewind(f)

	flags := []string{"message", "json"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}

	// --message should be required
	msgFlag := cmd.Flags().Lookup("message")
	if msgFlag == nil {
		t.Fatal("--message flag not found")
	}
}

func TestRewind_success(t *testing.T) {
	store := testutil.NewMockStore()
	sess := makeRewindSession("rewind-test")
	store.Sessions[sess.ID] = sess

	f, ios := rewindTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
		AtMessage: 3,
	}

	err := runRewind(opts)
	if err != nil {
		t.Fatalf("runRewind() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	checks := []string{
		"Rewound session",
		"New session:",
		"Messages:    3",
	}
	for _, want := range checks {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got:\n%s", want, output)
		}
	}

	// Verify the new session was saved to the store.
	if store.SaveCount < 1 {
		t.Error("expected at least 1 Save() call for the rewound session")
	}
	if store.LastSaved == nil {
		t.Fatal("expected LastSaved to be set")
	}
	if len(store.LastSaved.Messages) != 3 {
		t.Errorf("expected 3 messages in rewound session, got %d", len(store.LastSaved.Messages))
	}
	if store.LastSaved.ParentID != sess.ID {
		t.Errorf("expected ParentID = %s, got %s", sess.ID, store.LastSaved.ParentID)
	}
}

func TestRewind_jsonOutput(t *testing.T) {
	store := testutil.NewMockStore()
	sess := makeRewindSession("rewind-json-test")
	store.Sessions[sess.ID] = sess

	f, ios := rewindTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
		AtMessage: 3,
		JSON:      true,
	}

	err := runRewind(opts)
	if err != nil {
		t.Fatalf("runRewind() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	var result service.RewindResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("JSON output should be valid: %v\noutput: %s", err, output)
	}
	if result.OriginalID != sess.ID {
		t.Errorf("expected OriginalID = %s, got %s", sess.ID, result.OriginalID)
	}
	if result.TruncatedAt != 3 {
		t.Errorf("expected TruncatedAt = 3, got %d", result.TruncatedAt)
	}
	if result.MessagesRemoved != 2 {
		t.Errorf("expected MessagesRemoved = 2, got %d", result.MessagesRemoved)
	}
	if result.NewSession == nil {
		t.Fatal("expected NewSession to be non-nil in JSON output")
	}
	if len(result.NewSession.Messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result.NewSession.Messages))
	}
}

func TestRewind_serviceError(t *testing.T) {
	ios := iostreams.Test()

	f := &cmdutil.Factory{
		IOStreams: ios,
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return nil, fmt.Errorf("store unavailable")
		},
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "some-id",
		AtMessage: 1,
	}

	err := runRewind(opts)
	if err == nil {
		t.Fatal("expected error from service init failure")
	}
	if !strings.Contains(err.Error(), "initializing service") {
		t.Errorf("expected 'initializing service' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "store unavailable") {
		t.Errorf("expected 'store unavailable' in error, got: %v", err)
	}
}

func TestRewind_sessionNotFound(t *testing.T) {
	store := testutil.NewMockStore()
	// Store is empty — no session exists.

	f, ios := rewindTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "nonexistent-id",
		AtMessage: 1,
	}

	err := runRewind(opts)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "session not found") {
		t.Errorf("expected 'session not found' error, got: %v", err)
	}
}

func TestRewind_messageOutOfRange(t *testing.T) {
	store := testutil.NewMockStore()
	sess := makeRewindSession("rewind-range-test")
	store.Sessions[sess.ID] = sess

	f, ios := rewindTestFactory(t, store)

	// AtMessage = 10 is out of range for 5 messages.
	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
		AtMessage: 10,
	}

	err := runRewind(opts)
	if err == nil {
		t.Fatal("expected error for out-of-range message index")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("expected 'out of range' error, got: %v", err)
	}
}

func TestRewind_atMessageZero(t *testing.T) {
	store := testutil.NewMockStore()
	sess := makeRewindSession("rewind-zero-test")
	store.Sessions[sess.ID] = sess

	f, ios := rewindTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
		AtMessage: 0,
	}

	err := runRewind(opts)
	if err == nil {
		t.Fatal("expected error for message index 0")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("expected 'out of range' error, got: %v", err)
	}
}
