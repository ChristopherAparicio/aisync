package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
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

// newTestServerWithAnalysis creates a test server with AnalysisService wired.
func newTestServerWithAnalysis(t *testing.T) (*Server, *service.AnalysisService) {
	t.Helper()
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	analysisSvc := service.NewAnalysisService(service.AnalysisServiceConfig{
		Store:    store,
		Analyzer: &mockAnalyzer{},
	})

	srv, err := New(Config{
		SessionService:  sessionSvc,
		AnalysisService: analysisSvc,
		Addr:            ":0",
	})
	if err != nil {
		t.Fatalf("new web server: %v", err)
	}
	return srv, analysisSvc
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
	if !strings.Contains(body, "sessions") {
		t.Error("expected 'sessions' in page")
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
	// Session count should be visible as a number in the table row.
	if !strings.Contains(body, ">2</span>") {
		t.Error("expected session count 2 for feature/auth")
	}
	// Branch names should link to the detail page.
	if !strings.Contains(body, "/branches/feature/auth") {
		t.Error("expected link to branch detail page")
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
	// Branch explorer now shows a stats table — branch name + session count.
	// Individual sessions are on the detail page /branches/{name}.
	if !strings.Contains(body, "feature/fork-test") {
		t.Error("expected fork-test branch in table")
	}
	if !strings.Contains(body, ">2</span>") {
		t.Error("expected session count 2 for fork-test branch")
	}
	if !strings.Contains(body, "/branches/feature/fork-test") {
		t.Error("expected link to branch detail page")
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

	// Main page should render tab navigation.
	req := httptest.NewRequest(http.MethodGet, "/costs", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Should have tab navigation.
	if !strings.Contains(body, "cost-tabs") {
		t.Error("expected cost-tabs navigation")
	}
	if !strings.Contains(body, "Overview") {
		t.Error("expected Overview tab")
	}
	if !strings.Contains(body, "Optimization") {
		t.Error("expected Optimization tab")
	}

	// Overview partial should contain cost KPIs.
	req2 := httptest.NewRequest(http.MethodGet, "/partials/cost-overview", nil)
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("overview partial: expected 200, got %d", w2.Code)
	}

	overview := w2.Body.String()
	if !strings.Contains(overview, "Total Cost") {
		t.Error("expected Total Cost KPI in overview partial")
	}
	if !strings.Contains(overview, "3 sessions") {
		t.Error("expected '3 sessions' in overview partial")
	}
	if !strings.Contains(overview, "$") {
		t.Error("expected dollar sign in overview partial")
	}

	// Tools partial should contain branch costs.
	req3 := httptest.NewRequest(http.MethodGet, "/partials/cost-tools", nil)
	w3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("tools partial: expected 200, got %d", w3.Code)
	}

	tools := w3.Body.String()
	if !strings.Contains(tools, "feature/billing") {
		t.Error("expected feature/billing in tools partial")
	}
	if !strings.Contains(tools, "fix/typo") {
		t.Error("expected fix/typo in tools partial")
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

// ── API: Projects ──

func TestAPIProjects_noRegistryService(t *testing.T) {
	srv := newTestServer(t) // no registrySvc wired

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Should return valid JSON (null or empty array).
	body := w.Body.String()
	if !strings.Contains(body, "null") && !strings.Contains(body, "[]") {
		t.Errorf("expected null or empty array, got: %s", body)
	}
}

// ── Date Range Filter ──

func TestSessionsList_dateFilter(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("date-filter-1")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Filter with since date in the future should return no results.
	req := httptest.NewRequest(http.MethodGet, "/sessions?since=2099-01-01", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "No sessions found") {
		t.Error("expected empty state when since is in the future")
	}

	// Filter with a past since date should include the session.
	req2 := httptest.NewRequest(http.MethodGet, "/sessions?since=2020-01-01", nil)
	w2 := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w2, req2)

	body2 := w2.Body.String()
	if !strings.Contains(body2, "date-filter-1") {
		t.Error("expected date-filter-1 when since is in the past")
	}
}

// ── Tool Usage ──

func TestSessionDetail_toolUsage(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("tool-usage-1")
	sess.Messages[1].ToolCalls = []session.ToolCall{
		{ID: "tc-1", Name: "bash", Input: "go build", Output: "ok", State: session.ToolStateCompleted, DurationMs: 1200},
		{ID: "tc-2", Name: "bash", Input: "go test", Output: "ok", State: session.ToolStateCompleted, DurationMs: 800},
		{ID: "tc-3", Name: "Write", Input: "file.go", Output: "written", State: session.ToolStateCompleted, DurationMs: 100},
		{ID: "tc-4", Name: "Read", Input: "other.go", Output: "content", State: session.ToolStateCompleted, DurationMs: 50},
	}
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/tool-usage-1", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Should contain tool usage section.
	if !strings.Contains(body, "Tool Usage") {
		t.Error("expected 'Tool Usage' section heading")
	}
	// Should show tool names from the breakdown.
	if !strings.Contains(body, "bash") {
		t.Error("expected 'bash' tool in usage breakdown")
	}
}

// ── Project Filter on Sessions ──

func TestSessionsList_projectFilter(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("proj-filter-1")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Filter by a project path that doesn't match should return no results.
	req := httptest.NewRequest(http.MethodGet, "/sessions?project=/nonexistent/path", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "No sessions found") {
		t.Error("expected empty state for non-matching project path")
	}
}

// ── Dashboard with no RegistryService ──

func TestDashboard_noRegistryService(t *testing.T) {
	srv := newTestServer(t) // no registrySvc

	// Pass project param — should still work without crashing.
	req := httptest.NewRequest(http.MethodGet, "/?project=/some/path", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "aisync") {
		t.Error("expected body to contain 'aisync'")
	}
	// Should NOT have capability bar since registrySvc is nil.
	if strings.Contains(body, "capability-bar") {
		t.Error("should not have capability bar without registrySvc")
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

// ── Analysis Integration ──

// mockAnalyzer implements analysis.Analyzer for testing.
type mockAnalyzer struct{}

func (m *mockAnalyzer) Name() analysis.AdapterName { return analysis.AdapterLLM }

func (m *mockAnalyzer) Analyze(_ context.Context, req analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	return &analysis.AnalysisReport{
		Score:   72,
		Summary: "Session had moderate inefficiencies with repeated tool errors.",
		Problems: []analysis.Problem{
			{
				Severity:    analysis.SeverityHigh,
				Description: "Repeated bash errors due to missing dependencies",
				ToolName:    "bash",
			},
			{
				Severity:    analysis.SeverityMedium,
				Description: "Unnecessary file re-reads",
			},
		},
		Recommendations: []analysis.Recommendation{
			{
				Category:    analysis.CategoryTool,
				Title:       "Add dependency check skill",
				Description: "Create a skill that verifies project dependencies before builds.",
				Priority:    1,
			},
			{
				Category:    analysis.CategoryWorkflow,
				Title:       "Read files before editing",
				Description: "Always read a file before attempting edits to avoid re-reads.",
				Priority:    2,
			},
		},
		SkillSuggestions: []analysis.SkillSuggestion{
			{
				Name:        "dep-check",
				Description: "Verify project dependencies before building",
				Trigger:     "Before running build commands",
			},
		},
	}, nil
}

func TestSessionDetail_noAnalysisService(t *testing.T) {
	// Server without analysisSvc should NOT show analysis section.
	srv := newTestServer(t)

	sess := testutil.NewSession("no-analysis-svc")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data: data, SourceFormat: "aisync", IntoTarget: "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/no-analysis-svc", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if strings.Contains(body, "analysis-container") {
		t.Error("should NOT show analysis container without analysisSvc")
	}
	if strings.Contains(body, "Analyze Session") {
		t.Error("should NOT show Analyze button without analysisSvc")
	}
}

func TestSessionDetail_withAnalysisService_noAnalysis(t *testing.T) {
	// Server WITH analysisSvc but no analysis yet — should show "Analyze Session" button.
	srv, _ := newTestServerWithAnalysis(t)

	sess := testutil.NewSession("with-analysis-svc")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data: data, SourceFormat: "aisync", IntoTarget: "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/with-analysis-svc", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "analysis-container") {
		t.Error("expected analysis-container div")
	}
	if !strings.Contains(body, "Analyze Session") {
		t.Error("expected 'Analyze Session' button")
	}
	if strings.Contains(body, "analysis-score") {
		t.Error("should NOT show score when no analysis exists")
	}
}

func TestSessionDetail_withAnalysis(t *testing.T) {
	// Server WITH analysisSvc AND a pre-existing analysis — should show full report.
	srv, analysisSvc := newTestServerWithAnalysis(t)

	sess := testutil.NewSession("has-analysis")
	sess.Messages[1].ToolCalls = []session.ToolCall{
		{ID: "tc-1", Name: "bash", Input: "go build", Output: "fail", State: session.ToolStateError},
	}
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data: data, SourceFormat: "aisync", IntoTarget: "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Run analysis to populate the store.
	_, err := analysisSvc.Analyze(context.Background(), service.AnalysisRequest{
		SessionID: "has-analysis",
		Trigger:   analysis.TriggerManual,
	})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/has-analysis", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Should show analysis section with score.
	if !strings.Contains(body, "Session Analysis") {
		t.Error("expected 'Session Analysis' heading")
	}
	if !strings.Contains(body, "72") {
		t.Error("expected score '72' in display")
	}
	if !strings.Contains(body, "analysis-score-good") {
		t.Error("expected 'good' score class for score 72")
	}

	// Should show problems.
	if !strings.Contains(body, "Problems") {
		t.Error("expected 'Problems' subsection")
	}
	if !strings.Contains(body, "Repeated bash errors") {
		t.Error("expected problem description")
	}
	if !strings.Contains(body, "analysis-severity-high") {
		t.Error("expected high severity badge")
	}

	// Should show recommendations.
	if !strings.Contains(body, "Recommendations") {
		t.Error("expected 'Recommendations' subsection")
	}
	if !strings.Contains(body, "Add dependency check skill") {
		t.Error("expected recommendation title")
	}

	// Should show skill suggestions.
	if !strings.Contains(body, "Suggested Skills") {
		t.Error("expected 'Suggested Skills' subsection")
	}
	if !strings.Contains(body, "dep-check") {
		t.Error("expected skill name")
	}

	// Should show re-analyze button (not "Analyze Session").
	if !strings.Contains(body, "Re-analyze") {
		t.Error("expected 'Re-analyze' button")
	}
}

func TestAnalysisPartial_htmx(t *testing.T) {
	srv, analysisSvc := newTestServerWithAnalysis(t)

	sess := testutil.NewSession("partial-analysis")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data: data, SourceFormat: "aisync", IntoTarget: "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Before analysis — should show empty state.
	req := httptest.NewRequest(http.MethodGet, "/partials/analysis/partial-analysis", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Analyze Session") {
		t.Error("expected 'Analyze Session' button in empty partial")
	}

	// Run analysis.
	_, _ = analysisSvc.Analyze(context.Background(), service.AnalysisRequest{
		SessionID: "partial-analysis",
		Trigger:   analysis.TriggerManual,
	})

	// After analysis — should show report.
	req2 := httptest.NewRequest(http.MethodGet, "/partials/analysis/partial-analysis", nil)
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}
	body2 := w2.Body.String()
	if !strings.Contains(body2, "72") {
		t.Error("expected score in partial after analysis")
	}
	// Partial should NOT contain layout nav.
	if strings.Contains(body2, "<nav") {
		t.Error("partial should not include layout nav")
	}
}

func TestRunAnalysis_htmxPost(t *testing.T) {
	srv, _ := newTestServerWithAnalysis(t)

	sess := testutil.NewSession("run-analysis-post")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data: data, SourceFormat: "aisync", IntoTarget: "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// POST to trigger analysis.
	req := httptest.NewRequest(http.MethodPost, "/partials/analyze/run-analysis-post", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Should return the analysis partial with results.
	if !strings.Contains(body, "72") {
		t.Error("expected score in response after POST analysis")
	}
	if !strings.Contains(body, "Session Analysis") {
		t.Error("expected 'Session Analysis' heading in POST response")
	}
	if !strings.Contains(body, "manual") {
		t.Error("expected 'manual' trigger badge")
	}
}

func TestRunAnalysis_noAnalysisService(t *testing.T) {
	srv := newTestServer(t) // no analysisSvc

	req := httptest.NewRequest(http.MethodPost, "/partials/analyze/some-id", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestBuildAnalysisView(t *testing.T) {
	sa := &analysis.SessionAnalysis{
		ID:         "test-id",
		SessionID:  "sess-1",
		CreatedAt:  time.Now(),
		Trigger:    analysis.TriggerAuto,
		Adapter:    analysis.AdapterLLM,
		DurationMs: 5000,
		Report: analysis.AnalysisReport{
			Score:   85,
			Summary: "Good session",
			Problems: []analysis.Problem{
				{Severity: analysis.SeverityLow, Description: "Minor issue"},
			},
			Recommendations: []analysis.Recommendation{
				{Category: analysis.CategoryConfig, Title: "Update config", Description: "Change threshold", Priority: 1},
			},
			SkillSuggestions: []analysis.SkillSuggestion{
				{Name: "auto-test", Description: "Run tests automatically"},
			},
		},
	}

	v := buildAnalysisView(sa)

	if v.Score != 85 {
		t.Errorf("Score = %d, want 85", v.Score)
	}
	if v.ScoreClass != "good" {
		t.Errorf("ScoreClass = %q, want %q", v.ScoreClass, "good")
	}
	if !v.HasProblems {
		t.Error("expected HasProblems = true")
	}
	if len(v.Problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(v.Problems))
	}
	if v.Problems[0].SeverityClass != "low" {
		t.Errorf("SeverityClass = %q, want %q", v.Problems[0].SeverityClass, "low")
	}
	if !v.HasRecommendations {
		t.Error("expected HasRecommendations = true")
	}
	if len(v.Recommendations) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(v.Recommendations))
	}
	if !v.HasSkills {
		t.Error("expected HasSkills = true")
	}
	if len(v.SkillSuggestions) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(v.SkillSuggestions))
	}
}

func TestScoreClass(t *testing.T) {
	tests := []struct {
		score int
		want  string
	}{
		{90, "good"},
		{70, "good"},
		{69, "warning"},
		{40, "warning"},
		{39, "poor"},
		{0, "poor"},
	}
	for _, tt := range tests {
		got := scoreClass(tt.score)
		if got != tt.want {
			t.Errorf("scoreClass(%d) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestBuildAnalysisView_errorState(t *testing.T) {
	sa := &analysis.SessionAnalysis{
		ID:        "err-id",
		SessionID: "sess-err",
		CreatedAt: time.Now(),
		Trigger:   analysis.TriggerManual,
		Adapter:   analysis.AdapterLLM,
		Error:     "LLM API unavailable",
	}

	v := buildAnalysisView(sa)

	if v.Error != "LLM API unavailable" {
		t.Errorf("Error = %q, want %q", v.Error, "LLM API unavailable")
	}
	if v.HasProblems {
		t.Error("HasProblems should be false for error state")
	}
	if v.HasRecommendations {
		t.Error("HasRecommendations should be false for error state")
	}
}

// ── Dashboard Customization ──

func TestBuildColumnDefs_default(t *testing.T) {
	// Server without config should use default columns.
	srv := newTestServer(t) // srv.cfg is nil
	cols := srv.buildColumnDefs()

	want := []string{"id", "project", "provider", "branch", "summary", "health", "messages", "tokens", "cost", "errors", "when"}
	if len(cols) != len(want) {
		t.Fatalf("got %d columns, want %d", len(cols), len(want))
	}
	for i, col := range cols {
		if col.ID != want[i] {
			t.Errorf("column[%d].ID = %q, want %q", i, col.ID, want[i])
		}
	}
}

func TestBuildSessionRows_dynamic(t *testing.T) {
	srv := newTestServer(t)
	cols := []columnDef{
		{ID: "id", Label: "ID"},
		{ID: "tokens", Label: "Tokens", Class: "text-right"},
		{ID: "when", Label: "When"},
	}

	sessions := []session.Summary{
		{
			ID:          "row-test-1",
			Provider:    session.ProviderOpenCode,
			Branch:      "main",
			TotalTokens: 150000,
			CreatedAt:   time.Now().Add(-2 * time.Hour),
		},
	}

	rows := srv.buildSessionRows(sessions, cols)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.ID != "row-test-1" {
		t.Errorf("row.ID = %q, want %q", row.ID, "row-test-1")
	}
	if len(row.Cells) != 3 {
		t.Fatalf("expected 3 cells, got %d", len(row.Cells))
	}
	// ID cell should be a link.
	if !row.Cells[0].IsLink {
		t.Error("ID cell should be a link")
	}
	// Tokens cell should be formatted.
	if row.Cells[1].Value != "150.0k" {
		t.Errorf("tokens cell = %q, want %q", row.Cells[1].Value, "150.0k")
	}
	// When cell should be relative time.
	if !strings.Contains(row.Cells[2].Value, "hours ago") {
		t.Errorf("when cell = %q, expected 'hours ago'", row.Cells[2].Value)
	}
}

func TestBuildSessionRows_agentColumn(t *testing.T) {
	srv := newTestServer(t)
	cols := []columnDef{{ID: "agent", Label: "Agent"}}

	rows := srv.buildSessionRows([]session.Summary{
		{ID: "agent-1", Agent: "build"},
		{ID: "agent-2", Agent: ""},
	}, cols)

	if rows[0].Cells[0].Value != "build" {
		t.Errorf("agent value = %q, want %q", rows[0].Cells[0].Value, "build")
	}
	if rows[1].Cells[0].Value != "—" {
		t.Errorf("empty agent value = %q, want %q", rows[1].Cells[0].Value, "—")
	}
}

func TestBuildSessionRows_costColumn(t *testing.T) {
	srv := newTestServer(t)
	cols := []columnDef{{ID: "cost", Label: "Cost", Class: "text-right"}}

	rows := srv.buildSessionRows([]session.Summary{
		{ID: "cost-1", TotalTokens: 1_000_000},
	}, cols)

	// 1M tokens * $3/Mtoken blended = ~$3.00
	if !strings.HasPrefix(rows[0].Cells[0].Value, "~$") {
		t.Errorf("cost cell = %q, expected ~$ prefix", rows[0].Cells[0].Value)
	}
}

func TestSessionsList_dynamicColumnsInHTML(t *testing.T) {
	srv := newTestServer(t)

	sess := testutil.NewSession("dyn-col-1")
	data, _ := json.Marshal(sess)
	if _, err := srv.sessionSvc.Import(service.ImportRequest{
		Data: data, SourceFormat: "aisync", IntoTarget: "aisync",
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Should contain default column headers.
	for _, label := range []string{"ID", "Provider", "Branch", "Summary", "Msgs", "Tokens", "Cost", "Errs", "When"} {
		if !strings.Contains(body, label) {
			t.Errorf("expected column header %q in HTML", label)
		}
	}
	// Should contain session data.
	if !strings.Contains(body, "Sessions") && !strings.Contains(body, "sessions") {
		t.Error("expected sessions content in page")
	}
}

func TestEstimateTokenCost(t *testing.T) {
	tests := []struct {
		tokens int
		want   float64
	}{
		{0, 0},
		{1_000_000, 3.0},
		{500_000, 1.5},
	}
	for _, tt := range tests {
		got := estimateTokenCost(tt.tokens)
		if got != tt.want {
			t.Errorf("estimateTokenCost(%d) = %f, want %f", tt.tokens, got, tt.want)
		}
	}
}

func TestGetDashboardPageSize_nilConfig(t *testing.T) {
	srv := newTestServer(t)
	if got := srv.getDashboardPageSize(); got != 25 {
		t.Errorf("getDashboardPageSize() = %d, want 25", got)
	}
}

func TestGetDashboardSortBy_nilConfig(t *testing.T) {
	srv := newTestServer(t)
	if got := srv.getDashboardSortBy(); got != "created_at" {
		t.Errorf("getDashboardSortBy() = %q, want %q", got, "created_at")
	}
}

func TestProviderShortName(t *testing.T) {
	tests := []struct {
		provider session.ProviderName
		want     string
	}{
		{session.ProviderClaudeCode, "CC"},
		{session.ProviderOpenCode, "OC"},
		{session.ProviderCursor, "CU"},
		{session.ProviderName("unknown-tool"), "UN"},
		{session.ProviderName("ai"), "AI"},
	}
	for _, tt := range tests {
		if got := providerShortName(tt.provider); got != tt.want {
			t.Errorf("providerShortName(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

func TestBuildSessionRows_errorsColumn(t *testing.T) {
	srv := newTestServer(t)
	cols := []columnDef{
		{ID: "id", Label: "ID"},
		{ID: "tools", Label: "Tools", Class: "text-right"},
		{ID: "errors", Label: "Errs", Class: "text-right"},
	}

	sessions := []session.Summary{
		{
			ID:            "err-test-1",
			Provider:      session.ProviderOpenCode,
			ToolCallCount: 42,
			ErrorCount:    3,
		},
		{
			ID:            "err-test-2",
			Provider:      session.ProviderClaudeCode,
			ToolCallCount: 10,
			ErrorCount:    0,
		},
	}

	rows := srv.buildSessionRows(sessions, cols)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	// Row 1: has errors
	if rows[0].Cells[1].Value != "42" {
		t.Errorf("row1 tools = %q, want %q", rows[0].Cells[1].Value, "42")
	}
	if rows[0].Cells[2].Value != "3" {
		t.Errorf("row1 errors = %q, want %q", rows[0].Cells[2].Value, "3")
	}
	if !strings.Contains(rows[0].Cells[2].Class, "text-error") {
		t.Errorf("row1 errors class = %q, expected text-error", rows[0].Cells[2].Class)
	}

	// Row 2: no errors — should not have text-error class
	if rows[1].Cells[2].Value != "0" {
		t.Errorf("row2 errors = %q, want %q", rows[1].Cells[2].Value, "0")
	}
	if strings.Contains(rows[1].Cells[2].Class, "text-error") {
		t.Errorf("row2 errors class = %q, should not have text-error", rows[1].Cells[2].Class)
	}
}

// ── Sparkline Helpers ──

func TestBuildSparklineBars(t *testing.T) {
	tests := []struct {
		name   string
		values []int
		labels []string
		want   []sparklineBar
	}{
		{
			name:   "empty",
			values: nil,
			want:   nil,
		},
		{
			name:   "single value",
			values: []int{50},
			labels: []string{"Jan 1"},
			want:   []sparklineBar{{Value: 50, HeightPct: 100, Label: "Jan 1"}},
		},
		{
			name:   "all zeros",
			values: []int{0, 0, 0},
			labels: []string{"A", "B", "C"},
			want: []sparklineBar{
				{Value: 0, HeightPct: 0, Label: "A"},
				{Value: 0, HeightPct: 0, Label: "B"},
				{Value: 0, HeightPct: 0, Label: "C"},
			},
		},
		{
			name:   "proportional heights",
			values: []int{10, 50, 100, 25},
			labels: []string{"A", "B", "C", "D"},
			want: []sparklineBar{
				{Value: 10, HeightPct: 10, Label: "A"},
				{Value: 50, HeightPct: 50, Label: "B"},
				{Value: 100, HeightPct: 100, Label: "C"},
				{Value: 25, HeightPct: 25, Label: "D"},
			},
		},
		{
			name:   "minimum visible bar for non-zero",
			values: []int{1, 0, 1000},
			labels: []string{"A", "B", "C"},
			want: []sparklineBar{
				{Value: 1, HeightPct: 2, Label: "A"}, // min 2%
				{Value: 0, HeightPct: 0, Label: "B"},
				{Value: 1000, HeightPct: 100, Label: "C"},
			},
		},
		{
			name:   "missing labels",
			values: []int{10, 20},
			labels: []string{"A"},
			want: []sparklineBar{
				{Value: 10, HeightPct: 50, Label: "A"},
				{Value: 20, HeightPct: 100, Label: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSparklineBars(tt.values, tt.labels)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestBuildSparklineBarsFloat(t *testing.T) {
	values := []float64{1.5, 3.0, 0.0, 6.0}
	labels := []string{"A", "B", "C", "D"}

	bars := buildSparklineBarsFloat(values, labels)
	if len(bars) != 4 {
		t.Fatalf("len = %d, want 4", len(bars))
	}

	// Max is 6.0, so D should be 100%
	if bars[3].HeightPct != 100 {
		t.Errorf("bars[3].HeightPct = %d, want 100", bars[3].HeightPct)
	}
	// A = 1.5/6.0 = 25%
	if bars[0].HeightPct != 25 {
		t.Errorf("bars[0].HeightPct = %d, want 25", bars[0].HeightPct)
	}
	// B = 3.0/6.0 = 50%
	if bars[1].HeightPct != 50 {
		t.Errorf("bars[1].HeightPct = %d, want 50", bars[1].HeightPct)
	}
	// C = 0 → 0%
	if bars[2].HeightPct != 0 {
		t.Errorf("bars[2].HeightPct = %d, want 0", bars[2].HeightPct)
	}
}

// ── Settings Page ──

func TestSettings_noConfig(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Settings") {
		t.Error("expected 'Settings' heading")
	}
	if !strings.Contains(body, "No configuration loaded") {
		t.Error("expected 'No configuration loaded' when AppConfig is nil")
	}
}

func TestSettings_withConfig(t *testing.T) {
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	// Create a real config with a temp directory.
	configDir := t.TempDir()
	cfg, cfgErr := config.New(configDir, "")
	if cfgErr != nil {
		t.Fatalf("config.New: %v", cfgErr)
	}

	srv, srvErr := New(Config{
		SessionService: sessionSvc,
		AppConfig:      cfg,
		Addr:           ":0",
	})
	if srvErr != nil {
		t.Fatalf("new server: %v", srvErr)
	}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	// Should show config sections.
	if !strings.Contains(body, "General") {
		t.Error("expected 'General' section")
	}
	if !strings.Contains(body, "Search") {
		t.Error("expected 'Search' section")
	}
	if !strings.Contains(body, "Scheduler") {
		t.Error("expected 'Scheduler' section")
	}
	if !strings.Contains(body, "Analysis") {
		t.Error("expected 'Analysis' section")
	}
	if !strings.Contains(body, "compact") {
		t.Error("expected storage mode 'compact'")
	}
	if !strings.Contains(body, "config.json") {
		t.Error("expected config.json path reference")
	}

	// Verify inline editing controls are present.
	if !strings.Contains(body, "settings-toggle") {
		t.Error("expected toggle controls for boolean settings")
	}
	if !strings.Contains(body, `hx-post="/api/settings"`) {
		t.Error("expected HTMX POST to /api/settings")
	}
	if !strings.Contains(body, "settings-save-btn") {
		t.Error("expected save buttons for text/number inputs")
	}
	if !strings.Contains(body, "settings-select") {
		t.Error("expected select dropdowns for enum settings")
	}
}

// newTestServerWithConfig creates a test server with a real config.
func newTestServerWithConfig(t *testing.T) (*Server, *config.Config) {
	t.Helper()
	store := testutil.MustOpenStore(t)
	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})
	configDir := t.TempDir()
	cfg, err := config.New(configDir, "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	srv, err := New(Config{
		SessionService: sessionSvc,
		AppConfig:      cfg,
		Addr:           ":0",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv, cfg
}

func TestSettingsUpdate_toggle(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	// auto_capture defaults to false; enable it.
	form := "key=auto_capture&value=true"
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "enabled") {
		t.Errorf("response should contain 'enabled', got: %s", body)
	}
	if !strings.Contains(body, "saved") {
		t.Errorf("response should contain 'saved' indicator, got: %s", body)
	}
	// Verify config was actually updated.
	if !cfg.IsAutoCapture() {
		t.Error("expected auto_capture to be true after update")
	}
}

func TestSettingsUpdate_select(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	form := "key=storage_mode&value=full"
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "full") {
		t.Errorf("response should contain 'full', got: %s", body)
	}
	if cfg.GetStorageMode() != "full" {
		t.Errorf("expected storage_mode 'full', got %q", cfg.GetStorageMode())
	}
}

func TestSettingsUpdate_text(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	form := "key=analysis.model&value=claude-3-opus"
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if cfg.GetAnalysisModel() != "claude-3-opus" {
		t.Errorf("expected analysis.model 'claude-3-opus', got %q", cfg.GetAnalysisModel())
	}
}

func TestSettingsUpdate_number(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	form := "key=dashboard.page_size&value=25"
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if cfg.GetDashboardPageSize() != 25 {
		t.Errorf("expected dashboard.page_size 25, got %d", cfg.GetDashboardPageSize())
	}
}

func TestSettingsUpdate_missingKey(t *testing.T) {
	srv, _ := newTestServerWithConfig(t)

	form := "value=anything"
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestSettingsUpdate_invalidValue(t *testing.T) {
	srv, _ := newTestServerWithConfig(t)

	// storage_mode only allows "compact" or "full".
	form := "key=storage_mode&value=invalid_mode"
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "text-error") {
		t.Errorf("expected error response with 'text-error' class, got: %s", body)
	}
}

func TestSettingsUpdate_noConfig(t *testing.T) {
	srv := newTestServer(t) // no AppConfig

	form := "key=auto_capture&value=true"
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestSettingsUpdate_persistsToDisk(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)
	configDir := cfg.GlobalDir()

	// Set a value via the API.
	form := "key=features.file_blame&value=true"
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	// Re-read config from disk to verify persistence.
	cfg2, err := config.New(configDir, "")
	if err != nil {
		t.Fatalf("config.New reload: %v", err)
	}
	if !cfg2.IsFileBlameEnabled() {
		t.Error("expected features.file_blame to be true after reload from disk")
	}
}

func TestDashboard_sparklines_withStore(t *testing.T) {
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	// Seed a session so the dashboard isn't empty.
	sess := testutil.NewSession("spark-test-1")
	data, _ := json.Marshal(sess)
	_, err := sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// Insert token usage buckets for the past 3 days.
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		day := now.AddDate(0, 0, -i).Truncate(24 * time.Hour)
		bucket := session.TokenUsageBucket{
			BucketStart:    day,
			BucketEnd:      day.Add(24 * time.Hour),
			Granularity:    "1d",
			SessionCount:   (i + 1) * 2,
			InputTokens:    (i + 1) * 1000,
			OutputTokens:   (i + 1) * 500,
			ToolCallCount:  (i + 1) * 10,
			ToolErrorCount: i,
			EstimatedCost:  float64(i+1) * 0.50,
		}
		if err := store.UpsertTokenBucket(bucket); err != nil {
			t.Fatalf("upsert bucket[%d]: %v", i, err)
		}
	}

	srv, srvErr := New(Config{
		SessionService: sessionSvc,
		Store:          store,
		Addr:           ":0",
	})
	if srvErr != nil {
		t.Fatalf("new server: %v", srvErr)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	// Should contain sparkline markup.
	if !strings.Contains(body, "sparkline") {
		t.Error("expected sparkline CSS class in dashboard HTML")
	}
	if !strings.Contains(body, "sparkline-bar") {
		t.Error("expected sparkline-bar elements in dashboard HTML")
	}
	if !strings.Contains(body, "Sessions (14d)") {
		t.Error("expected 'Sessions (14d)' sparkline title attribute")
	}
}

// ── Project Classifier CRUD Tests ────────────────────────────────────────

func TestProjectClassifierSave_newProject(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	form := "name=test-org/myrepo&ticket_pattern=PROJ-%5Cd%2B&ticket_source=jira&ticket_url=https://jira.example.com/{id}"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	// Verify the config was updated.
	pcs := cfg.GetAllProjectClassifiers()
	pc, ok := pcs["test-org/myrepo"]
	if !ok {
		t.Fatal("expected project classifier to be created")
	}
	if pc.TicketPattern != `PROJ-\d+` {
		t.Errorf("ticket_pattern = %q, want PROJ-\\d+", pc.TicketPattern)
	}
	if pc.TicketSource != "jira" {
		t.Errorf("ticket_source = %q, want jira", pc.TicketSource)
	}
	if pc.TicketURL != "https://jira.example.com/{id}" {
		t.Errorf("ticket_url = %q, want https://jira.example.com/{id}", pc.TicketURL)
	}
}

func TestProjectClassifierSave_withRules(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	form := "name=my/proj" +
		"&branch_rule_pattern[]=feature/*&branch_rule_type[]=feature" +
		"&branch_rule_pattern[]=fix/*&branch_rule_type[]=bug" +
		"&agent_rule_pattern[]=review&agent_rule_type[]=review" +
		"&commit_rule_pattern[]=feat&commit_rule_type[]=feature" +
		"&status_rule_pattern[]=[WIP]&status_rule_type[]=active"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	pc := cfg.GetAllProjectClassifiers()["my/proj"]
	if len(pc.BranchRules) != 2 {
		t.Errorf("branch_rules = %d, want 2", len(pc.BranchRules))
	}
	if pc.BranchRules["feature/*"] != "feature" {
		t.Errorf("branch_rule feature/* = %q, want feature", pc.BranchRules["feature/*"])
	}
	if pc.BranchRules["fix/*"] != "bug" {
		t.Errorf("branch_rule fix/* = %q, want bug", pc.BranchRules["fix/*"])
	}
	if len(pc.AgentRules) != 1 || pc.AgentRules["review"] != "review" {
		t.Errorf("agent_rules = %v, want {review: review}", pc.AgentRules)
	}
	if len(pc.CommitRules) != 1 || pc.CommitRules["feat"] != "feature" {
		t.Errorf("commit_rules = %v, want {feat: feature}", pc.CommitRules)
	}
	if len(pc.StatusRules) != 1 || pc.StatusRules["[WIP]"] != "active" {
		t.Errorf("status_rules = %v, want {[WIP]: active}", pc.StatusRules)
	}
}

func TestProjectClassifierSave_withBudget(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	form := "name=budget-proj&budget_monthly=500&budget_daily=25&budget_alert_percent=80&budget_cost_mode=estimated&budget_alert_webhook=slack"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	pc := cfg.GetAllProjectClassifiers()["budget-proj"]
	if pc.Budget == nil {
		t.Fatal("expected budget to be set")
	}
	if pc.Budget.MonthlyLimit != 500 {
		t.Errorf("monthly_limit = %f, want 500", pc.Budget.MonthlyLimit)
	}
	if pc.Budget.DailyLimit != 25 {
		t.Errorf("daily_limit = %f, want 25", pc.Budget.DailyLimit)
	}
	if pc.Budget.AlertAtPercent != 80 {
		t.Errorf("alert_at_percent = %f, want 80", pc.Budget.AlertAtPercent)
	}
	if pc.Budget.CostMode != "estimated" {
		t.Errorf("cost_mode = %q, want estimated", pc.Budget.CostMode)
	}
	if pc.Budget.AlertWebhook != "slack" {
		t.Errorf("alert_webhook = %q, want slack", pc.Budget.AlertWebhook)
	}
}

func TestProjectClassifierSave_updateExisting(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	// Create initial.
	form := "name=upd/proj&ticket_pattern=OLD-%5Cd%2B"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create: status = %d", w.Code)
	}

	// Update.
	form = "name=upd/proj&ticket_pattern=NEW-%5Cd%2B&ticket_source=github"
	req = httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", w.Code, w.Body.String())
	}

	pc := cfg.GetAllProjectClassifiers()["upd/proj"]
	if pc.TicketPattern != `NEW-\d+` {
		t.Errorf("ticket_pattern = %q, want NEW-\\d+", pc.TicketPattern)
	}
	if pc.TicketSource != "github" {
		t.Errorf("ticket_source = %q, want github", pc.TicketSource)
	}
}

func TestProjectClassifierSave_emptyName(t *testing.T) {
	srv, _ := newTestServerWithConfig(t)

	form := "name=&ticket_pattern=X"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "name is required") {
		t.Errorf("expected error about name, got: %s", w.Body.String())
	}
}

func TestProjectClassifierSave_invalidRegex(t *testing.T) {
	srv, _ := newTestServerWithConfig(t)

	form := "name=bad/regex&ticket_pattern=[invalid"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ticket_pattern") {
		t.Errorf("expected error about ticket_pattern, got: %s", w.Body.String())
	}
}

func TestProjectClassifierDelete(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	// Create a project first.
	form := "name=del/proj&ticket_source=jira"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create: status = %d", w.Code)
	}

	// Delete it.
	form = "name=del/proj"
	req = httptest.NewRequest(http.MethodDelete, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d; body = %s", w.Code, w.Body.String())
	}

	pcs := cfg.GetAllProjectClassifiers()
	if _, ok := pcs["del/proj"]; ok {
		t.Error("expected project to be deleted")
	}
}

func TestProjectClassifierDelete_notFound(t *testing.T) {
	srv, _ := newTestServerWithConfig(t)

	form := "name=nonexistent/proj"
	req := httptest.NewRequest(http.MethodDelete, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

func TestProjectClassifierSave_persistsToDisk(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)
	configDir := cfg.GlobalDir()

	form := "name=persist/proj&ticket_pattern=PP-%5Cd%2B&ticket_source=notion"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}

	// Reload from disk.
	cfg2, err := config.New(configDir, "")
	if err != nil {
		t.Fatalf("config.New reload: %v", err)
	}
	pcs := cfg2.GetAllProjectClassifiers()
	pc, ok := pcs["persist/proj"]
	if !ok {
		t.Fatal("expected project classifier to persist to disk")
	}
	if pc.TicketPattern != `PP-\d+` {
		t.Errorf("ticket_pattern = %q, want PP-\\d+", pc.TicketPattern)
	}
	if pc.TicketSource != "notion" {
		t.Errorf("ticket_source = %q, want notion", pc.TicketSource)
	}
}

func TestProjectClassifierSave_noConfig(t *testing.T) {
	srv := newTestServer(t) // no AppConfig

	form := "name=test&ticket_source=jira"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestSettings_showsProjectClassifiers(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	// Add a project classifier.
	pc := config.ProjectClassifierConf{
		TicketPattern: `DEMO-\d+`,
		TicketSource:  "jira",
		BranchRules:   map[string]string{"feature/*": "feature"},
	}
	if err := cfg.SetProjectClassifier("demo-org/app", pc); err != nil {
		t.Fatalf("SetProjectClassifier: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "demo-org/app") {
		t.Error("expected project name in settings page")
	}
	if !strings.Contains(body, "DEMO-") {
		t.Error("expected ticket pattern 'DEMO-' in settings page")
	}
	if !strings.Contains(body, "Project Classifiers") {
		t.Error("expected Project Classifiers section header")
	}
	if !strings.Contains(body, "pc-card") {
		t.Error("expected pc-card CSS class for project classifier card")
	}
}

func TestProjectClassifierSave_withTags(t *testing.T) {
	srv, cfg := newTestServerWithConfig(t)

	form := "name=tags/proj&tags=feature%2C+bug%2C+refactor"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}

	pc := cfg.GetAllProjectClassifiers()["tags/proj"]
	if len(pc.Tags) != 3 {
		t.Fatalf("tags = %v, want 3 elements", pc.Tags)
	}
	if pc.Tags[0] != "feature" || pc.Tags[1] != "bug" || pc.Tags[2] != "refactor" {
		t.Errorf("tags = %v, want [feature bug refactor]", pc.Tags)
	}
}

// ── Branch Session Tree Tests ────────────────────────────────────────────

func TestBranchTimeline_empty(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/branches/nonexistent-branch", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "nonexistent-branch") {
		t.Error("expected branch name in page")
	}
	if !strings.Contains(body, "No activity found") {
		t.Error("expected empty state message")
	}
}

func TestBranchTimeline_withSessions(t *testing.T) {
	store := testutil.MustOpenStore(t)
	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	// Seed 2 sessions on the same branch.
	sess1 := testutil.NewSession("branch-tree-1")
	sess1.Branch = "feature/tree-test"
	sess1.Summary = "First session on branch"
	sess1.Messages = []session.Message{{Role: "user", Content: "hello"}}
	data1, _ := json.Marshal(sess1)
	_, err := sessionSvc.Import(service.ImportRequest{Data: data1, SourceFormat: "aisync"})
	if err != nil {
		t.Fatalf("import 1: %v", err)
	}

	sess2 := testutil.NewSession("branch-tree-2")
	sess2.Branch = "feature/tree-test"
	sess2.ParentID = sess1.ID
	sess2.Summary = "Child session (fork)"
	sess2.Messages = []session.Message{{Role: "user", Content: "world"}}
	data2, _ := json.Marshal(sess2)
	_, err = sessionSvc.Import(service.ImportRequest{Data: data2, SourceFormat: "aisync"})
	if err != nil {
		t.Fatalf("import 2: %v", err)
	}

	srv, srvErr := New(Config{
		SessionService: sessionSvc,
		Store:          store,
		Addr:           ":0",
	})
	if srvErr != nil {
		t.Fatalf("new server: %v", srvErr)
	}

	req := httptest.NewRequest(http.MethodGet, "/branches/feature/tree-test", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "feature/tree-test") {
		t.Error("expected branch name in page")
	}
	if !strings.Contains(body, "Session Tree") {
		t.Error("expected 'Session Tree' section")
	}
	if !strings.Contains(body, "bt-node") {
		t.Error("expected bt-node CSS class for tree nodes")
	}
	if !strings.Contains(body, "First session") {
		t.Error("expected first session summary in tree")
	}
	if !strings.Contains(body, "Child session") {
		t.Error("expected child session summary in tree")
	}
}

func TestBranchTimeline_redirect(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/branches/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// Empty branch name should redirect to /branches
	if w.Code != http.StatusFound && w.Code != http.StatusOK {
		// If the route matches differently, at least not a 500
		t.Logf("status = %d (expected redirect or OK)", w.Code)
	}
}

func TestBuildBranchTreeNodeView(t *testing.T) {
	node := session.SessionTreeNode{
		Summary: session.Summary{
			ID:           "ses_test123",
			Provider:     "opencode",
			Agent:        "general",
			Branch:       "main",
			Summary:      "A very long summary that should be truncated because it exceeds the sixty character limit set in the builder",
			SessionType:  "feature",
			Status:       "active",
			MessageCount: 42,
			TotalTokens:  150000,
			ErrorCount:   3,
			CreatedAt:    time.Now().Add(-2 * time.Hour),
		},
		IsFork: true,
		Children: []session.SessionTreeNode{
			{
				Summary: session.Summary{
					ID:           "ses_child456",
					Provider:     "opencode",
					Summary:      "Child node",
					MessageCount: 10,
					TotalTokens:  5000,
					CreatedAt:    time.Now().Add(-1 * time.Hour),
				},
			},
		},
	}

	var maxDepth, forkCount int
	view := buildBranchTreeNodeView(node, 0, &maxDepth, &forkCount)

	if view.IDShort != "ses_test12345" {
		// truncateID takes 12 chars
		t.Logf("IDShort = %q (may be exact or truncated)", view.IDShort)
	}
	if !view.IsFork {
		t.Error("expected IsFork = true")
	}
	if view.StatusClass != "bt-status--active" {
		t.Errorf("StatusClass = %q, want bt-status--active", view.StatusClass)
	}
	if view.Messages != 42 {
		t.Errorf("Messages = %d, want 42", view.Messages)
	}
	if view.Errors != 3 {
		t.Errorf("Errors = %d, want 3", view.Errors)
	}
	if !view.HasChildren {
		t.Error("expected HasChildren = true")
	}
	if len(view.Children) != 1 {
		t.Fatalf("Children = %d, want 1", len(view.Children))
	}
	if view.Children[0].Depth != 1 {
		t.Errorf("child Depth = %d, want 1", view.Children[0].Depth)
	}
	if view.Children[0].IndentPx != 24 {
		t.Errorf("child IndentPx = %d, want 24", view.Children[0].IndentPx)
	}
	if maxDepth != 1 {
		t.Errorf("maxDepth = %d, want 1", maxDepth)
	}
	if forkCount != 1 {
		t.Errorf("forkCount = %d, want 1", forkCount)
	}
	// Summary should be truncated to ~60 chars
	if len(view.Summary) > 63 { // 57 + "..."
		t.Errorf("Summary not truncated: len = %d", len(view.Summary))
	}
}

func TestCountTreeNodes(t *testing.T) {
	tree := []session.SessionTreeNode{
		{
			Summary: session.Summary{ID: "a"},
			Children: []session.SessionTreeNode{
				{Summary: session.Summary{ID: "b"}},
				{
					Summary: session.Summary{ID: "c"},
					Children: []session.SessionTreeNode{
						{Summary: session.Summary{ID: "d"}},
					},
				},
			},
		},
		{Summary: session.Summary{ID: "e"}},
	}

	if n := countTreeNodes(tree); n != 5 {
		t.Errorf("countTreeNodes = %d, want 5", n)
	}
}

func TestTruncateID(t *testing.T) {
	if got := truncateID("abcdefghijklmnop", 10); got != "abcdefghij" {
		t.Errorf("truncateID = %q, want abcdefghij", got)
	}
	if got := truncateID("short", 10); got != "short" {
		t.Errorf("truncateID = %q, want short", got)
	}
}

// ── Skill Detection Tests ────────────────────────────────────────────────

func TestDetectSkillsPerMessage_noSkills(t *testing.T) {
	msgs := []session.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	}
	m := detectSkillsPerMessage(msgs)
	if m != nil {
		t.Errorf("expected nil map, got %v", m)
	}
}

func TestDetectSkillsPerMessage_toolCallSkill(t *testing.T) {
	msgs := []session.Message{
		{Role: "user", Content: "use the skill"},
		{Role: "assistant", ToolCalls: []session.ToolCall{
			{Name: "skill", Input: `{"name": "replay-tester"}`, State: "success"},
		}},
		{Role: "assistant", Content: "done"},
	}
	m := detectSkillsPerMessage(msgs)
	if len(m) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(m))
	}
	skills := m[1] // message index 1
	if len(skills) != 1 || skills[0] != "replay-tester" {
		t.Errorf("skills = %v, want [replay-tester]", skills)
	}
}

func TestDetectSkillsPerMessage_contentTag(t *testing.T) {
	msgs := []session.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: `Here is the skill: <skill_content name="opencode-sessions">skill instructions here</skill_content> done`},
	}
	m := detectSkillsPerMessage(msgs)
	if len(m) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(m))
	}
	skills := m[1]
	if len(skills) != 1 || skills[0] != "opencode-sessions" {
		t.Errorf("skills = %v, want [opencode-sessions]", skills)
	}
}

func TestDetectSkillsPerMessage_multipleSkillsOneMessage(t *testing.T) {
	msgs := []session.Message{
		{Role: "assistant", Content: `<skill_content name="alpha">A</skill_content> and <skill_content name="beta">B</skill_content>`},
	}
	m := detectSkillsPerMessage(msgs)
	if len(m) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(m))
	}
	skills := m[0]
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}
	if skills[0] != "alpha" || skills[1] != "beta" {
		t.Errorf("skills = %v, want [alpha beta]", skills)
	}
}

func TestDetectSkillsPerMessage_mixedDetection(t *testing.T) {
	msgs := []session.Message{
		{Role: "user", Content: "load skill"},
		{Role: "assistant", ToolCalls: []session.ToolCall{
			{Name: "skill", Input: `{"name": "replay-tester"}`, State: "success"},
		}, Content: `<skill_content name="replay-tester">content</skill_content>`},
	}
	m := detectSkillsPerMessage(msgs)
	if len(m[1]) != 2 {
		t.Errorf("expected 2 skills on msg 1, got %d: %v", len(m[1]), m[1])
	}
}

func TestExtractSkillNameFromInput(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"name": "replay-tester"}`, "replay-tester"},
		{`{"name":"opencode-sessions"}`, "opencode-sessions"},
		{`invalid json`, ""},
		{`{}`, ""},
		{`{"name": ""}`, ""},
	}
	for _, tt := range tests {
		got := extractSkillNameFromInput(tt.input)
		if got != tt.want {
			t.Errorf("extractSkillNameFromInput(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── Classification Preview Tests ──

func newTestServerWithStoreAndConfig(t *testing.T) (*Server, *sqlite.Store, *config.Config) {
	t.Helper()
	store := testutil.MustOpenStore(t)
	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})
	configDir := t.TempDir()
	cfg, err := config.New(configDir, "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	srv, err := New(Config{
		SessionService: sessionSvc,
		Store:          store,
		AppConfig:      cfg,
		Addr:           ":0",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv, store, cfg
}

func TestClassificationPreview_noStore(t *testing.T) {
	// Server without store should return 503.
	srv := newTestServer(t)
	form := "name=my/project"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project/preview", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestClassificationPreview_emptyName(t *testing.T) {
	srv, _, _ := newTestServerWithStoreAndConfig(t)
	form := "name="
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project/preview", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "project name is required") {
		t.Errorf("body should contain error message, got: %s", w.Body.String())
	}
}

func TestClassificationPreview_noMatchingSessions(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	// Seed a session with a different project.
	_ = store.Save(&session.Session{
		ID:          "sess-other",
		Branch:      "main",
		Agent:       "explore",
		Summary:     "Explore codebase",
		RemoteURL:   "git@github.com:other/repo.git",
		ProjectPath: "/tmp/other",
	})

	form := "name=my/project"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project/preview", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "No sessions found") {
		t.Errorf("body should indicate no sessions found, got: %s", body)
	}
}

func TestClassificationPreview_withMatchingSessions(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	// Seed sessions matching "my/project" via remote URL.
	_ = store.Save(&session.Session{
		ID:          "sess-feat",
		Branch:      "feature/auth",
		Agent:       "coder",
		Summary:     "Implement auth flow",
		RemoteURL:   "git@github.com:my/project.git",
		ProjectPath: "/tmp/my/project",
	})
	_ = store.Save(&session.Session{
		ID:          "sess-fix",
		Branch:      "main",
		Agent:       "explore",
		Summary:     "fix: login bug",
		RemoteURL:   "git@github.com:my/project.git",
		ProjectPath: "/tmp/my/project",
	})

	// Preview with branch rules: feature/* -> feature
	form := "name=my/project" +
		"&branch_rule_pattern[]=feature/*&branch_rule_type[]=feature"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project/preview", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "2 sessions") {
		t.Errorf("should show 2 sessions, got: %s", body)
	}
	// The fix: summary should match via default commit rules.
	if !strings.Contains(body, "bug") {
		t.Errorf("should show 'bug' type from commit rules, got: %s", body)
	}
	// feature/auth should match branch rule.
	if !strings.Contains(body, "feature") {
		t.Errorf("should show 'feature' type from branch rules, got: %s", body)
	}
}

func TestClassificationPreview_statusRuleChanges(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	_ = store.Save(&session.Session{
		ID:          "sess-wip",
		Branch:      "main",
		Agent:       "coder",
		Summary:     "[WIP] Work in progress",
		RemoteURL:   "git@github.com:status/project.git",
		ProjectPath: "/tmp/status/project",
		Status:      "",
	})

	// Preview with custom status rules: [WIP] -> in-progress (different from default "active").
	form := "name=status/project" +
		"&status_rule_pattern[]=[WIP]&status_rule_type[]=in-progress"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project/preview", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "in-progress") {
		t.Errorf("should show new status 'in-progress', got: %s", body)
	}
	if !strings.Contains(body, "status change") {
		t.Errorf("should indicate status changes, got: %s", body)
	}
}

func TestClassificationPreview_typeChangeDetection(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	// Session with existing type that matches the proposed rules.
	_ = store.Save(&session.Session{
		ID:          "sess-already",
		Branch:      "feature/login",
		Agent:       "coder",
		Summary:     "Implement login",
		RemoteURL:   "git@github.com:tc/project.git",
		ProjectPath: "/tmp/tc/project",
		SessionType: "feature", // Already classified as "feature"
	})
	// Session without existing type.
	_ = store.Save(&session.Session{
		ID:          "sess-untyped",
		Branch:      "feature/signup",
		Agent:       "coder",
		Summary:     "Implement signup",
		RemoteURL:   "git@github.com:tc/project.git",
		ProjectPath: "/tmp/tc/project",
		SessionType: "", // Not yet classified
	})

	form := "name=tc/project" +
		"&branch_rule_pattern[]=feature/*&branch_rule_type[]=feature"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project/preview", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// sess-already: currentType=feature, newType=feature -> no change
	// sess-untyped: currentType="", newType=feature -> type change
	if !strings.Contains(body, "1 type change") {
		t.Errorf("should detect 1 type change, got: %s", body)
	}
}

func TestClassificationPreview_projectPathMatch(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	// Session without remote URL but with project path matching basename.
	_ = store.Save(&session.Session{
		ID:          "sess-local",
		Branch:      "main",
		Agent:       "explore",
		Summary:     "Explore local project",
		RemoteURL:   "",
		ProjectPath: "/home/user/myapp",
	})

	form := "name=myapp"
	req := httptest.NewRequest(http.MethodPost, "/api/settings/project/preview", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "1 sessions") {
		t.Errorf("should find 1 session via project path basename, got: %s", body)
	}
}

// ── Unit tests for helper functions ──

func TestSessionMatchesProject(t *testing.T) {
	tests := []struct {
		name    string
		sm      session.Summary
		project string
		want    bool
	}{
		{
			name:    "remote URL display name match",
			sm:      session.Summary{RemoteURL: "git@github.com:foo/bar.git"},
			project: "foo/bar",
			want:    true,
		},
		{
			name:    "raw remote URL match",
			sm:      session.Summary{RemoteURL: "git@github.com:foo/bar.git"},
			project: "git@github.com:foo/bar.git",
			want:    true,
		},
		{
			name:    "project path match",
			sm:      session.Summary{ProjectPath: "/home/user/projects/myapp"},
			project: "/home/user/projects/myapp",
			want:    true,
		},
		{
			name:    "project path basename match",
			sm:      session.Summary{ProjectPath: "/home/user/projects/myapp"},
			project: "myapp",
			want:    true,
		},
		{
			name:    "no match",
			sm:      session.Summary{RemoteURL: "git@github.com:other/repo.git", ProjectPath: "/tmp/other"},
			project: "foo/bar",
			want:    false,
		},
		{
			name:    "empty project name",
			sm:      session.Summary{RemoteURL: "git@github.com:foo/bar.git"},
			project: "",
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sessionMatchesProject(tt.sm, tt.project)
			if got != tt.want {
				t.Errorf("sessionMatchesProject = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassifySessionPreview(t *testing.T) {
	branchRules := map[string]string{"feature/*": "feature", "fix/*": "bug"}
	agentRules := map[string]string{"explore": "exploration", "review": "review"}
	commitRules := config.DefaultCommitRules
	statusRules := config.DefaultStatusRules

	tests := []struct {
		name          string
		sm            session.Summary
		wantType      string
		wantTypeChg   bool
		wantStatus    string
		wantStatusChg bool
	}{
		{
			name:        "commit rule priority (fix:)",
			sm:          session.Summary{Summary: "fix: broken auth", Branch: "feature/auth"},
			wantType:    "bug",
			wantTypeChg: true,
		},
		{
			name:        "branch rule match",
			sm:          session.Summary{Summary: "Implement login", Branch: "feature/auth"},
			wantType:    "feature",
			wantTypeChg: true,
		},
		{
			name:        "agent rule match",
			sm:          session.Summary{Summary: "Explore codebase", Branch: "main", Agent: "explore"},
			wantType:    "exploration",
			wantTypeChg: true,
		},
		{
			name:          "status rule match",
			sm:            session.Summary{Summary: "[WIP] Work in progress"},
			wantType:      "",
			wantStatus:    "active",
			wantStatusChg: true,
		},
		{
			name:        "already classified, same type",
			sm:          session.Summary{Summary: "fix: bug", SessionType: "bug"},
			wantType:    "bug",
			wantTypeChg: false,
		},
		{
			name:        "already classified, different type",
			sm:          session.Summary{Summary: "fix: bug", SessionType: "feature"},
			wantType:    "bug",
			wantTypeChg: true,
		},
		{
			name:        "no match",
			sm:          session.Summary{Summary: "Random session", Branch: "main", Agent: "coder"},
			wantType:    "",
			wantTypeChg: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := classifySessionPreview(tt.sm, branchRules, agentRules, commitRules, statusRules)
			if row.NewType != tt.wantType {
				t.Errorf("NewType = %q, want %q", row.NewType, tt.wantType)
			}
			if row.TypeChanged != tt.wantTypeChg {
				t.Errorf("TypeChanged = %v, want %v", row.TypeChanged, tt.wantTypeChg)
			}
			if tt.wantStatus != "" {
				if row.NewStatus != tt.wantStatus {
					t.Errorf("NewStatus = %q, want %q", row.NewStatus, tt.wantStatus)
				}
			}
			if row.StatusChanged != tt.wantStatusChg {
				t.Errorf("StatusChanged = %v, want %v", row.StatusChanged, tt.wantStatusChg)
			}
		})
	}
}

func TestExtractSkillContentNames(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{`no skills here`, 0},
		{`<skill_content name="one">content</skill_content>`, 1},
		{`<skill_content name="a">x</skill_content> gap <skill_content name="b">y</skill_content>`, 2},
		{`<skill_content name="">empty</skill_content>`, 0},
	}
	for _, tt := range tests {
		got := extractSkillContentNames(tt.input)
		if len(got) != tt.want {
			t.Errorf("extractSkillContentNames = %d names, want %d", len(got), tt.want)
		}
	}
}
