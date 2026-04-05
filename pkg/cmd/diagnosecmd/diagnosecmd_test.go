package diagnosecmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// testFactory creates a Factory wired to a temp SQLite store with test data.
func testFactory(t *testing.T, sess *session.Session) *cmdutil.Factory {
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

	return &cmdutil.Factory{
		IOStreams: iostreams.Test(),
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return svc, nil
		},
	}
}

func TestNewCmdDiagnose_noArgs(t *testing.T) {
	f := testFactory(t, nil)
	cmd := NewCmdDiagnose(f)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error without args")
	}
}

func TestNewCmdDiagnose_sessionNotFound(t *testing.T) {
	f := testFactory(t, nil)
	cmd := NewCmdDiagnose(f)
	cmd.SetArgs([]string{"nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestNewCmdDiagnose_quickScan(t *testing.T) {
	sess := &session.Session{
		ID: "diag-test-1",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Fix bug", InputTokens: 100},
			{Role: session.RoleAssistant, Content: "Done", OutputTokens: 200,
				ToolCalls: []session.ToolCall{
					{Name: "write", State: session.ToolStateCompleted},
				}},
			{Role: session.RoleUser, Content: "Thanks", InputTokens: 50},
			{Role: session.RoleAssistant, Content: "Welcome", OutputTokens: 100},
		},
		TokenUsage: session.TokenUsage{InputTokens: 150, OutputTokens: 300, TotalTokens: 450},
	}

	f := testFactory(t, sess)
	cmd := NewCmdDiagnose(f)

	out := &bytes.Buffer{}
	f.IOStreams.Out = out
	cmd.SetOut(out)
	cmd.SetArgs([]string{"diag-test-1"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "HEALTHY") && !strings.Contains(output, "Score:") {
		t.Errorf("expected health output, got: %s", output)
	}
}

func TestNewCmdDiagnose_jsonOutput(t *testing.T) {
	sess := &session.Session{
		ID: "diag-json",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Hello", InputTokens: 10},
			{Role: session.RoleAssistant, Content: "Hi", OutputTokens: 10},
			{Role: session.RoleUser, Content: "Bye", InputTokens: 10},
			{Role: session.RoleAssistant, Content: "Bye", OutputTokens: 10},
		},
		TokenUsage: session.TokenUsage{InputTokens: 20, OutputTokens: 20, TotalTokens: 40},
	}

	f := testFactory(t, sess)
	cmd := NewCmdDiagnose(f)

	out := &bytes.Buffer{}
	f.IOStreams.Out = out
	cmd.SetOut(out)
	cmd.SetArgs([]string{"diag-json", "--json"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, `"verdict"`) {
		t.Errorf("expected JSON with verdict field, got: %s", output)
	}
	if !strings.Contains(output, `"health_score"`) {
		t.Errorf("expected JSON with health_score field, got: %s", output)
	}
}

func TestNewCmdDiagnose_quietOutput(t *testing.T) {
	sess := &session.Session{
		ID: "diag-quiet",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Hello", InputTokens: 10},
			{Role: session.RoleAssistant, Content: "Hi", OutputTokens: 10},
			{Role: session.RoleUser, Content: "Bye", InputTokens: 10},
			{Role: session.RoleAssistant, Content: "Bye", OutputTokens: 10},
		},
		TokenUsage: session.TokenUsage{InputTokens: 20, OutputTokens: 20, TotalTokens: 40},
	}

	f := testFactory(t, sess)
	cmd := NewCmdDiagnose(f)

	out := &bytes.Buffer{}
	f.IOStreams.Out = out
	cmd.SetOut(out)
	cmd.SetArgs([]string{"diag-quiet", "--quiet"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output := out.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 1 {
		t.Errorf("expected single line in quiet mode, got %d lines: %q", len(lines), output)
	}
	if !strings.Contains(output, "/100") {
		t.Errorf("expected score in quiet output, got: %s", output)
	}
}

func TestNewCmdDiagnose_brokenSession(t *testing.T) {
	msgs := make([]session.Message, 40)
	for i := range msgs {
		msgs[i].InputTokens = 2000
		msgs[i].OutputTokens = 500
	}
	// Add lots of errors in the last quarter.
	for i := 30; i < 40; i++ {
		msgs[i].ToolCalls = []session.ToolCall{
			{Name: "bash", State: session.ToolStateError},
			{Name: "bash", State: session.ToolStateError},
			{Name: "bash", State: session.ToolStateError},
		}
	}

	sess := &session.Session{
		ID:       "diag-broken",
		Messages: msgs,
		Errors: []session.SessionError{
			{MessageIndex: 30, Category: session.ErrorCategoryToolError},
			{MessageIndex: 35, Category: session.ErrorCategoryToolError, ToolCallID: "tc-1"},
		},
		TokenUsage: session.TokenUsage{InputTokens: 80000, OutputTokens: 20000, TotalTokens: 100000},
	}

	f := testFactory(t, sess)
	cmd := NewCmdDiagnose(f)

	out := &bytes.Buffer{}
	f.IOStreams.Out = out
	cmd.SetOut(out)
	cmd.SetArgs([]string{"diag-broken"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output := out.String()
	// Should show restore advice.
	if !strings.Contains(output, "Restore Advice") {
		t.Errorf("expected restore advice section, got: %s", output)
	}
	// Should show tool report.
	if !strings.Contains(output, "Tool Report") {
		t.Errorf("expected tool report section, got: %s", output)
	}
}
