package skillobservecmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// testFactory creates a Factory wired to a temp SQLite store, optionally
// pre-seeded with a session. The RegistryService is built with an empty
// scanner registry so ScanProject returns zero capabilities — this keeps
// tests deterministic and side-effect free (no real ~/.claude/... scan).
// Pass withRegistry=false to leave RegistryServiceFunc nil (so we can
// assert the "registry service unavailable" error path).
func testFactory(t *testing.T, sess *session.Session, withRegistry bool) *cmdutil.Factory {
	t.Helper()

	store, err := sqlite.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	if sess != nil {
		if err := store.Save(sess); err != nil {
			t.Fatalf("saving session: %v", err)
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

	if withRegistry {
		// Empty scanner registry → ScanProject returns a project with no
		// capabilities, which makes skillobs.Observe return nil (no skills).
		// This is the "pure" test path: we exercise the wiring without
		// depending on what ~/.claude, ~/.cursor, etc. happen to contain
		// on the test host.
		emptyScanners := provider.NewScannerRegistry()
		regSvc := service.NewRegistryService(service.RegistryServiceConfig{
			Scanners: emptyScanners,
			Store:    store,
		})
		f.RegistryServiceFunc = func() (*service.RegistryService, error) {
			return regSvc, nil
		}
	}

	return f
}

// captureOut replaces the factory's Out buffer with a fresh one and wires
// it through the cobra command — mirroring the diagnosecmd pattern.
func captureOut(f *cmdutil.Factory) *bytes.Buffer {
	out := &bytes.Buffer{}
	f.IOStreams.Out = out
	return out
}

// newSeededSession returns a minimal session with a non-empty project path
// and a couple of messages. ProjectPath is set to a temp dir the test owns
// so ScanProject can resolve it without filesystem surprises.
func newSeededSession(t *testing.T) *session.Session {
	t.Helper()
	projectDir := t.TempDir()
	return &session.Session{
		ID:          "skillobs-test-1",
		ProjectPath: projectDir,
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "help me read this file", InputTokens: 10},
			{Role: session.RoleAssistant, Content: "ok", OutputTokens: 10},
		},
	}
}

// ── argument validation ──

func TestNewCmdSkillObserve_noArgs(t *testing.T) {
	f := testFactory(t, nil, true)
	cmd := NewCmdSkillObserve(f)
	cmd.SetArgs([]string{})
	// Silence cobra's usage output — we only care about the Execute err.
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error without args")
	}
}

func TestNewCmdSkillObserve_tooManyArgs(t *testing.T) {
	f := testFactory(t, nil, true)
	cmd := NewCmdSkillObserve(f)
	cmd.SetArgs([]string{"a", "b"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error with >1 arg")
	}
}

// ── error paths ──

// TestNewCmdSkillObserve_sessionNotFound verifies a clear error when the
// session ID does not resolve.
func TestNewCmdSkillObserve_sessionNotFound(t *testing.T) {
	f := testFactory(t, nil, true)
	cmd := NewCmdSkillObserve(f)

	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"nonexistent-session-id"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	if !strings.Contains(err.Error(), "nonexistent-session-id") {
		t.Errorf("expected session id in error, got: %v", err)
	}
}

// TestNewCmdSkillObserve_noRegistryService exercises the defensive path
// when the factory has no RegistryServiceFunc wired. The command must
// fail with a clear error before touching any skill logic.
func TestNewCmdSkillObserve_noRegistryService(t *testing.T) {
	sess := newSeededSession(t)
	f := testFactory(t, sess, false) // withRegistry=false
	cmd := NewCmdSkillObserve(f)

	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{string(sess.ID)})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when RegistryService is unavailable")
	}
	if !strings.Contains(err.Error(), "registry service") {
		t.Errorf("expected 'registry service' in error, got: %v", err)
	}
}

// ── happy path (empty registry) ──

// TestNewCmdSkillObserve_emptyRegistry runs the full pipeline against a
// seeded session and an empty scanner registry. skillobs.Observe returns
// nil for zero capabilities, which the command must surface as "No skills
// available for this project's registry." — not as an error.
func TestNewCmdSkillObserve_emptyRegistry(t *testing.T) {
	sess := newSeededSession(t)
	f := testFactory(t, sess, true)
	cmd := NewCmdSkillObserve(f)

	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetArgs([]string{string(sess.ID)})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "No skills available") {
		t.Errorf("expected 'No skills available' message, got: %q", output)
	}
}

// TestNewCmdSkillObserve_emptyRegistry_JSON verifies the --json branch of
// the empty-registry case emits a valid envelope with observation=null.
func TestNewCmdSkillObserve_emptyRegistry_JSON(t *testing.T) {
	sess := newSeededSession(t)
	f := testFactory(t, sess, true)
	cmd := NewCmdSkillObserve(f)

	out := captureOut(f)
	cmd.SetOut(out)
	cmd.SetArgs([]string{string(sess.ID), "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var envelope struct {
		SessionID   string `json:"session_id"`
		ProjectPath string `json:"project_path"`
		Observation any    `json:"observation"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal JSON: %v — raw: %s", err, out.String())
	}

	if envelope.SessionID != string(sess.ID) {
		t.Errorf("expected session_id %q, got %q", string(sess.ID), envelope.SessionID)
	}
	if envelope.ProjectPath != sess.ProjectPath {
		t.Errorf("expected project_path %q, got %q", sess.ProjectPath, envelope.ProjectPath)
	}
	if envelope.Observation != nil {
		t.Errorf("expected observation=null for empty registry, got: %v", envelope.Observation)
	}
	if !strings.Contains(envelope.Message, "no skills available") {
		t.Errorf("expected 'no skills available' message, got: %q", envelope.Message)
	}
}
