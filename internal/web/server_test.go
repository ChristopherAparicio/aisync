package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	srv, err := New(Config{
		SessionService: sessionSvc,
		Addr:           ":0",
	})
	if err != nil {
		t.Fatalf("new web server: %v", err)
	}
	return srv
}

func TestDashboard_empty(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "aisync") {
		t.Error("expected body to contain 'aisync'")
	}
	if !strings.Contains(body, "No sessions captured yet") {
		t.Error("expected empty state message")
	}
}

func TestDashboard_withSessions(t *testing.T) {
	srv := newTestServer(t)

	// Seed sessions via the service.
	sess := testutil.NewSession("web-dash-1")
	data, _ := json.Marshal(sess)
	_, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if strings.Contains(body, "No sessions captured yet") {
		t.Error("should not show empty state when sessions exist")
	}
	if !strings.Contains(body, "Sessions") {
		t.Error("expected 'Sessions' KPI card")
	}
	if !strings.Contains(body, "Recent Sessions") {
		t.Error("expected 'Recent Sessions' section")
	}
	if !strings.Contains(body, "web-dash-1") {
		t.Error("expected session ID in recent sessions table")
	}
}

// ── Sessions List ──

func TestSessionsList_empty(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "No sessions found") {
		t.Error("expected empty state message")
	}
}

func TestSessionsList_withData(t *testing.T) {
	srv := newTestServer(t)

	// Seed 2 sessions.
	for _, id := range []string{"sess-list-1", "sess-list-2"} {
		sess := testutil.NewSession(id)
		data, _ := json.Marshal(sess)
		if _, err := srv.sessionSvc.Import(service.ImportRequest{
			Data:         data,
			SourceFormat: "aisync",
			IntoTarget:   "aisync",
		}); err != nil {
			t.Fatalf("import %s: %v", id, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "sess-list-1") {
		t.Error("expected sess-list-1 in results")
	}
	if !strings.Contains(body, "sess-list-2") {
		t.Error("expected sess-list-2 in results")
	}
	if !strings.Contains(body, "2 sessions") {
		t.Error("expected '2 sessions' count")
	}
}

func TestSessionsList_filterByBranch(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("filter-test")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Filter by existing branch should find it.
	req := httptest.NewRequest(http.MethodGet, "/sessions?branch=feature/test", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "filter-test") {
		t.Error("expected filter-test in results")
	}

	// Filter by non-existing branch should show empty.
	req2 := httptest.NewRequest(http.MethodGet, "/sessions?branch=nonexistent", nil)
	w2 := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w2, req2)

	body2 := w2.Body.String()
	if !strings.Contains(body2, "No sessions found") {
		t.Error("expected empty state for nonexistent branch")
	}
}

func TestSessionsTablePartial(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("partial-test")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// HTMX partial should return just the table, not the full layout.
	req := httptest.NewRequest(http.MethodGet, "/partials/sessions-table", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "partial-test") {
		t.Error("expected partial-test in table partial")
	}
	// The partial should NOT contain the full layout nav.
	if strings.Contains(body, "<nav") {
		t.Error("partial should not contain full layout nav")
	}
}

// ── Session Detail ──

func TestSessionDetail_notFound(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/sessions/nonexistent-id", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestSessionDetail_withData(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("detail-test-1")
	sess.Summary = "Implement feature X"
	sess.Messages[1].ToolCalls = []session.ToolCall{
		{
			ID:         "tc-1",
			Name:       "bash",
			Input:      "go build ./...",
			Output:     "ok",
			State:      session.ToolStateCompleted,
			DurationMs: 1500,
		},
	}
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/detail-test-1", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Check metadata header.
	if !strings.Contains(body, "detail-test-") {
		t.Error("expected session ID in header")
	}
	if !strings.Contains(body, "claude-code") {
		t.Error("expected provider badge")
	}
	if !strings.Contains(body, "feature/test") {
		t.Error("expected branch in metadata")
	}
	if !strings.Contains(body, "Implement feature X") {
		t.Error("expected summary text")
	}

	// Check messages render.
	if !strings.Contains(body, "chat-user") {
		t.Error("expected user message with chat-user class")
	}
	if !strings.Contains(body, "chat-assistant") {
		t.Error("expected assistant message with chat-assistant class")
	}
	if !strings.Contains(body, "Hello from detail-test-1") {
		t.Error("expected user message content")
	}

	// Check tool calls.
	if !strings.Contains(body, "bash") {
		t.Error("expected tool call name 'bash'")
	}
	if !strings.Contains(body, "go build") {
		t.Error("expected tool call input")
	}

	// Check file changes.
	if !strings.Contains(body, "src/main.go") {
		t.Error("expected file change path")
	}
	if !strings.Contains(body, "modified") {
		t.Error("expected file change type")
	}
}

func TestSessionDetail_costBreakdown(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("cost-detail-1")
	// Use a model name the pricing calculator recognizes.
	sess.Messages[1].Model = "claude-sonnet-4-20250514"
	sess.Messages[1].InputTokens = 5000
	sess.Messages[1].OutputTokens = 2000
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/cost-detail-1", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Cost Breakdown") {
		t.Error("expected cost breakdown section")
	}
	if !strings.Contains(body, "claude-sonnet-4") {
		t.Error("expected model name in cost breakdown")
	}
	if !strings.Contains(body, "$") {
		t.Error("expected dollar sign in cost display")
	}
}

// ── Branch Explorer ──

func TestBranches_empty(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/branches", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "No branches found") {
		t.Error("expected empty state message")
	}
}

func TestBranches_withData(t *testing.T) {
	srv := newTestServer(t)

	// Seed sessions on two different branches.
	for _, tc := range []struct {
		id     string
		branch string
	}{
		{"branch-sess-1", "feature/auth"},
		{"branch-sess-2", "feature/auth"},
		{"branch-sess-3", "fix/typo"},
	} {
		sess := testutil.NewSession(tc.id)
		sess.Branch = tc.branch
		data, _ := json.Marshal(sess)
		if _, err := srv.sessionSvc.Import(service.ImportRequest{
			Data:         data,
			SourceFormat: "aisync",
			IntoTarget:   "aisync",
		}); err != nil {
			t.Fatalf("import %s: %v", tc.id, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/branches", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "feature/auth") {
		t.Error("expected feature/auth branch")
	}
	if !strings.Contains(body, "fix/typo") {
		t.Error("expected fix/typo branch")
	}
	if !strings.Contains(body, "2 sessions") {
		t.Error("expected '2 sessions' for feature/auth")
	}
	if !strings.Contains(body, "branch-sess-1") {
		t.Error("expected session ID in timeline")
	}
}

func TestBranches_withForks(t *testing.T) {
	srv := newTestServer(t)

	// Parent session.
	parent := testutil.NewSession("fork-parent")
	parent.Branch = "feature/fork-test"
	pdata, _ := json.Marshal(parent)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         pdata,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import parent: %v", err)
	}

	// Child session (forked from parent).
	child := testutil.NewSession("fork-child")
	child.Branch = "feature/fork-test"
	child.ParentID = "fork-parent"
	cdata, _ := json.Marshal(child)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         cdata,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import child: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/branches", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "fork-parent") {
		t.Error("expected parent session in timeline")
	}
	if !strings.Contains(body, "fork-child") {
		t.Error("expected child session in timeline")
	}
	// Child should be rendered in a nested timeline-children div.
	if !strings.Contains(body, "timeline-children") {
		t.Error("expected timeline-children div for fork")
	}
}

// ── Cost Dashboard ──

func TestCosts_empty(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/costs", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "No cost data available") {
		t.Error("expected empty state message")
	}
}

func TestCosts_withData(t *testing.T) {
	srv := newTestServer(t)

	// Seed sessions with known models for cost computation.
	for _, tc := range []struct {
		id     string
		branch string
		model  string
	}{
		{"cost-sess-1", "feature/billing", "claude-sonnet-4-20250514"},
		{"cost-sess-2", "feature/billing", "claude-sonnet-4-20250514"},
		{"cost-sess-3", "fix/typo", "gpt-4o"},
	} {
		sess := testutil.NewSession(tc.id)
		sess.Branch = tc.branch
		sess.Messages[1].Model = tc.model
		sess.Messages[1].InputTokens = 3000
		sess.Messages[1].OutputTokens = 1000
		data, _ := json.Marshal(sess)
		if _, err := srv.sessionSvc.Import(service.ImportRequest{
			Data:         data,
			SourceFormat: "aisync",
			IntoTarget:   "aisync",
		}); err != nil {
			t.Fatalf("import %s: %v", tc.id, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/costs", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Should have cost KPIs.
	if !strings.Contains(body, "Total Cost") {
		t.Error("expected Total Cost KPI")
	}
	if !strings.Contains(body, "3 sessions") {
		t.Error("expected '3 sessions' in total")
	}

	// Per-branch costs.
	if !strings.Contains(body, "feature/billing") {
		t.Error("expected feature/billing in branch costs")
	}
	if !strings.Contains(body, "fix/typo") {
		t.Error("expected fix/typo in branch costs")
	}

	// Should show dollar amounts.
	if !strings.Contains(body, "$") {
		t.Error("expected dollar sign in cost display")
	}
}

// ── Click-to-Restore ──

func TestSessionDetail_restorePanel(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("restore-panel-1")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/restore-panel-1", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Should show the restore panel.
	if !strings.Contains(body, "Restore Session") {
		t.Error("expected 'Restore Session' heading in restore panel")
	}
	// Should contain the default restore command.
	if !strings.Contains(body, "aisync restore --session restore-panel-1") {
		t.Error("expected default restore command")
	}
	// Should have the provider selector.
	if !strings.Contains(body, `<select id="restore-provider"`) {
		t.Error("expected provider select element")
	}
	// Should have Copy button.
	if !strings.Contains(body, "Copy") {
		t.Error("expected Copy button")
	}
}

func TestRestoreCommand_defaultProvider(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("restore-cmd-1")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// HTMX partial: no provider = default command.
	req := httptest.NewRequest(http.MethodGet, "/partials/restore-command/restore-cmd-1", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "aisync restore --session restore-cmd-1") {
		t.Errorf("expected default restore command, got: %s", body)
	}
	// Should NOT contain --provider or --as-context.
	if strings.Contains(body, "--provider") {
		t.Error("default command should not include --provider flag")
	}
	if strings.Contains(body, "--as-context") {
		t.Error("default command should not include --as-context flag")
	}
}

func TestRestoreCommand_withProvider(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("restore-cmd-2")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Request with explicit provider.
	req := httptest.NewRequest(http.MethodGet, "/partials/restore-command/restore-cmd-2?provider=opencode", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "--provider opencode") {
		t.Errorf("expected --provider opencode, got: %s", body)
	}
}

func TestRestoreCommand_asContext(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("restore-cmd-3")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Request with context=true.
	req := httptest.NewRequest(http.MethodGet, "/partials/restore-command/restore-cmd-3?context=true", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "--as-context") {
		t.Errorf("expected --as-context flag, got: %s", body)
	}
}

func TestRestoreCommand_contextProvider(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("restore-cmd-4")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Selecting "CONTEXT.md" provider should use --as-context.
	req := httptest.NewRequest(http.MethodGet, "/partials/restore-command/restore-cmd-4?provider=context", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "--as-context") {
		t.Errorf("expected --as-context for context provider, got: %s", body)
	}
}

func TestRestoreCommand_notFound(t *testing.T) {
	srv := newTestServer(t)

	// Session doesn't exist — partial still generates a valid command
	// (no lookup needed, just builds command from ID).
	req := httptest.NewRequest(http.MethodGet, "/partials/restore-command/nonexistent-id", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "aisync restore --session nonexistent-id") {
		t.Errorf("expected command with the ID, got: %s", body)
	}
}

func TestBuildRestoreCmd(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		provider  string
		asContext bool
		want      string
	}{
		{
			name:      "default",
			sessionID: "abc-123",
			want:      "aisync restore --session abc-123",
		},
		{
			name:      "with provider",
			sessionID: "abc-123",
			provider:  "opencode",
			want:      "aisync restore --session abc-123 --provider opencode",
		},
		{
			name:      "as context",
			sessionID: "abc-123",
			asContext: true,
			want:      "aisync restore --session abc-123 --as-context",
		},
		{
			name:      "context provider",
			sessionID: "abc-123",
			provider:  "context",
			want:      "aisync restore --session abc-123 --as-context",
		},
		{
			name:      "provider and context",
			sessionID: "abc-123",
			provider:  "claude-code",
			asContext: true,
			want:      "aisync restore --session abc-123 --as-context",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRestoreCmd(tt.sessionID, tt.provider, tt.asContext)
			if got != tt.want {
				t.Errorf("buildRestoreCmd(%q, %q, %v) = %q, want %q",
					tt.sessionID, tt.provider, tt.asContext, got, tt.want)
			}
		})
	}
}

// ── Error KPIs ──

func TestSessionDetail_errorCount(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("error-kpi-1")
	sess.Messages[1].ToolCalls = []session.ToolCall{
		{ID: "tc-1", Name: "bash", Input: "go build", Output: "ok", State: session.ToolStateCompleted},
		{ID: "tc-2", Name: "bash", Input: "gh api ...", Output: "404 Not Found", State: session.ToolStateError},
		{ID: "tc-3", Name: "bash", Input: "gh api v2", Output: "422 Unprocessable", State: session.ToolStateError},
		{ID: "tc-4", Name: "bash", Input: "gh api v3", Output: "ok", State: session.ToolStateCompleted},
		{ID: "tc-5", Name: "Write", Input: "file.go", Output: "written", State: session.ToolStateCompleted},
	}
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/error-kpi-1", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Should show error count.
	if !strings.Contains(body, "Errors") {
		t.Error("expected 'Errors' KPI label")
	}
	// Should highlight with error class (2 errors out of 5 tool calls).
	if !strings.Contains(body, "kpi-error") {
		t.Error("expected kpi-error class when errors > 0")
	}
	// Should show error rate (2/5 = 40%).
	if !strings.Contains(body, "40%") {
		t.Error("expected '40%' error rate")
	}
	// Tool calls with error state should have red badge.
	if !strings.Contains(body, "tool-error") {
		t.Error("expected tool-error class on failed tool calls")
	}
}

func TestSessionDetail_noErrors(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("no-errors-1")
	sess.Messages[1].ToolCalls = []session.ToolCall{
		{ID: "tc-1", Name: "bash", Input: "go build", Output: "ok", State: session.ToolStateCompleted},
	}
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/no-errors-1", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Should show Errors label but no error class.
	if !strings.Contains(body, "Errors") {
		t.Error("expected 'Errors' KPI label")
	}
	if strings.Contains(body, "kpi-error") {
		t.Error("should NOT have kpi-error class when errors = 0")
	}
}

// ── Static ──

func TestStaticCSS(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Errorf("expected text/css, got %s", ct)
	}
}
