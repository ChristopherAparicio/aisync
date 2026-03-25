package errorscmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// mockErrorService implements service.ErrorServicer for testing.
type mockErrorService struct {
	errors  []session.SessionError
	summary *session.SessionErrorSummary
	err     error
}

func (m *mockErrorService) ProcessSession(_ *session.Session) (*service.ProcessSessionResult, error) {
	return nil, nil
}
func (m *mockErrorService) GetErrors(_ session.ID) ([]session.SessionError, error) {
	return m.errors, m.err
}
func (m *mockErrorService) GetSummary(_ session.ID) (*session.SessionErrorSummary, error) {
	return m.summary, m.err
}
func (m *mockErrorService) ListRecent(_ int, _ session.ErrorCategory) ([]session.SessionError, error) {
	return m.errors, m.err
}

func TestRunSessionErrors_NoErrors(t *testing.T) {
	var buf bytes.Buffer
	io := &iostreams.IOStreams{Out: &buf}

	mock := &mockErrorService{}
	opts := &Options{
		IO:        io,
		SessionID: "test-session",
	}

	err := runSessionErrors(opts, mock, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := buf.String(); got != "No errors found for this session.\n" {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestRunSessionErrors_WithErrors(t *testing.T) {
	var buf bytes.Buffer
	io := &iostreams.IOStreams{Out: &buf}

	mock := &mockErrorService{
		errors: []session.SessionError{
			{
				ID:         "err-1",
				SessionID:  "test-session",
				Category:   session.ErrorCategoryProviderError,
				Source:     session.ErrorSourceProvider,
				Message:    "Internal Server Error",
				HTTPStatus: 500,
			},
			{
				ID:        "err-2",
				SessionID: "test-session",
				Category:  session.ErrorCategoryToolError,
				Source:    session.ErrorSourceTool,
				Message:   "Command failed: exit code 1",
				ToolName:  "bash",
			},
		},
	}

	opts := &Options{
		IO:        io,
		SessionID: "test-session",
	}

	err := runSessionErrors(opts, mock, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if len(output) == 0 {
		t.Fatal("expected output, got empty string")
	}
	// Should contain both error categories.
	if !contains(output, "provider_error") {
		t.Error("output should contain 'provider_error'")
	}
	if !contains(output, "tool_error") {
		t.Error("output should contain 'tool_error'")
	}
}

func TestRunSessionErrors_CategoryFilter(t *testing.T) {
	var buf bytes.Buffer
	io := &iostreams.IOStreams{Out: &buf}

	mock := &mockErrorService{
		errors: []session.SessionError{
			{ID: "err-1", Category: session.ErrorCategoryProviderError, Source: session.ErrorSourceProvider, Message: "500 error"},
			{ID: "err-2", Category: session.ErrorCategoryToolError, Source: session.ErrorSourceTool, Message: "bash fail"},
		},
	}

	opts := &Options{
		IO:        io,
		SessionID: "test-session",
		Category:  "tool_error",
	}

	err := runSessionErrors(opts, mock, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if contains(output, "provider_error") {
		t.Error("output should not contain 'provider_error' when filtering by tool_error")
	}
	if !contains(output, "tool_error") {
		t.Error("output should contain 'tool_error'")
	}
}

func TestRunSessionErrors_JSON(t *testing.T) {
	var buf bytes.Buffer
	io := &iostreams.IOStreams{Out: &buf}

	mock := &mockErrorService{
		errors: []session.SessionError{
			{ID: "err-1", Category: session.ErrorCategoryProviderError, Source: session.ErrorSourceProvider, Message: "test"},
		},
	}

	opts := &Options{
		IO:        io,
		SessionID: "test-session",
		JSON:      true,
	}

	err := runSessionErrors(opts, mock, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result struct {
		SessionID string                 `json:"session_id"`
		Errors    []session.SessionError `json:"errors"`
	}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if result.SessionID != "test-session" {
		t.Errorf("session_id = %q, want %q", result.SessionID, "test-session")
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
}

func TestRunRecentErrors_Empty(t *testing.T) {
	var buf bytes.Buffer
	io := &iostreams.IOStreams{Out: &buf}

	mock := &mockErrorService{}
	opts := &Options{
		IO:     io,
		Recent: true,
		Limit:  50,
	}

	err := runRecentErrors(opts, mock, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := buf.String(); got != "No recent errors found.\n" {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestNewCmdErrors_RequiresSessionIDOrRecent(t *testing.T) {
	f := &cmdutil.Factory{
		IOStreams: &iostreams.IOStreams{Out: &bytes.Buffer{}},
	}
	cmd := NewCmdErrors(f)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when neither session-id nor --recent is provided")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && bytes.Contains([]byte(s), []byte(substr))
}
