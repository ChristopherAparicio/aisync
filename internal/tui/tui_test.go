package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		input int
	}{
		{"zero", "0", 0},
		{"small", "500", 500},
		{"thousands", "5.0k", 5000},
		{"millions", "1.5M", 1500000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTokenCount(tt.input)
			if got != tt.want {
				t.Errorf("formatTokenCount(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncateStr(t *testing.T) {
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
			got := truncateStr(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestFormatTimeAgo(t *testing.T) {
	tests := []struct {
		name string
		want string
		d    time.Duration
	}{
		{"just now", "just now", 30 * time.Second},
		{"minutes", "5m ago", 5 * time.Minute},
		{"hours", "3h ago", 3 * time.Hour},
		{"1 day", "1 day ago", 25 * time.Hour},
		{"days", "3d ago", 72 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimeAgo(time.Now().Add(-tt.d))
			if got != tt.want {
				t.Errorf("formatTimeAgo(-%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}

	t.Run("zero time", func(t *testing.T) {
		got := formatTimeAgo(time.Time{})
		if got != "-" {
			t.Errorf("formatTimeAgo(zero) = %q, want %q", got, "-")
		}
	})
}

func TestWrapText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  int // number of lines expected
	}{
		{"short", "hello", 80, 1},
		{"exact", "hello world", 11, 1},
		{"wrap", "hello world this is a long line that should wrap", 20, 3},
		{"newlines", "line1\nline2\nline3", 80, 3},
		{"empty", "", 80, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := wrapText(tt.input, tt.width)
			if len(lines) != tt.want {
				t.Errorf("wrapText(%q, %d) = %d lines, want %d", tt.input, tt.width, len(lines), tt.want)
			}
		})
	}
}

func TestChangeIcon(t *testing.T) {
	// Just verify each type returns something non-empty
	types := []session.ChangeType{
		session.ChangeCreated,
		session.ChangeModified,
		session.ChangeDeleted,
		session.ChangeRead,
	}
	for _, ct := range types {
		got := changeIcon(ct)
		if got == "" {
			t.Errorf("changeIcon(%q) returned empty string", ct)
		}
	}
}

func TestRenderField(t *testing.T) {
	result := renderField("Label", "Value")
	if !strings.Contains(result, "Label") {
		t.Error("expected label in output")
	}
	if !strings.Contains(result, "Value") {
		t.Error("expected value in output")
	}
}

func TestDetailBuildLines(t *testing.T) {
	sess := &session.Session{
		ID:       "test-id-123",
		Provider: session.ProviderClaudeCode,
		Branch:   "feature/test",
		Summary:  "Test session summary",
		TokenUsage: session.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
			TotalTokens:  1500,
		},
		Messages: []session.Message{
			{
				ID:      "msg-1",
				Role:    session.RoleUser,
				Content: "Hello",
			},
			{
				ID:      "msg-2",
				Role:    session.RoleAssistant,
				Content: "Hi there!",
				Model:   "claude-3",
				Tokens:  100,
			},
		},
		FileChanges: []session.FileChange{
			{FilePath: "main.go", ChangeType: session.ChangeModified},
			{FilePath: "new.go", ChangeType: session.ChangeCreated},
		},
		Links: []session.Link{
			{LinkType: session.LinkPR, Ref: "42"},
		},
	}

	m := detailModel{session: sess}
	lines := m.buildLines()

	if len(lines) == 0 {
		t.Fatal("expected non-empty lines")
	}

	// Check that key content is present
	fullText := strings.Join(lines, "\n")
	checks := []string{
		"test-id-123",
		"claude-code",
		"feature/test",
		"Test session summary",
		"1.5k",
		"main.go",
		"new.go",
		"USER",
		"ASSISTANT",
		"Hello",
		"Hi there",
		"42",
	}
	for _, check := range checks {
		if !strings.Contains(fullText, check) {
			t.Errorf("expected %q in detail view output", check)
		}
	}
}

func TestDashboardView_notLoaded(t *testing.T) {
	m := newDashboardModel(nil)
	v := m.view()
	if !strings.Contains(v, "Loading") {
		t.Errorf("expected 'Loading' when not loaded, got: %s", v)
	}
}

func TestListView_notLoaded(t *testing.T) {
	m := newListModel(nil)
	v := m.view()
	if !strings.Contains(v, "Loading") {
		t.Errorf("expected 'Loading' when not loaded, got: %s", v)
	}
}

func TestListView_empty(t *testing.T) {
	m := newListModel(nil)
	m.setSessions(sessionListMsg{})
	v := m.view()
	if !strings.Contains(v, "No sessions found") {
		t.Errorf("expected 'No sessions found', got: %s", v)
	}
}

func TestListView_withSessions(t *testing.T) {
	m := newListModel(nil)
	m.setSize(120, 40)
	m.setSessions(sessionListMsg{
		{
			ID:           "sess-1",
			Provider:     session.ProviderClaudeCode,
			Branch:       "main",
			MessageCount: 5,
			TotalTokens:  1000,
			CreatedAt:    time.Now(),
		},
		{
			ID:           "sess-2",
			Provider:     session.ProviderOpenCode,
			Branch:       "feature",
			MessageCount: 10,
			TotalTokens:  5000,
			CreatedAt:    time.Now(),
		},
	})

	v := m.view()
	if !strings.Contains(v, "sess-1") {
		t.Error("expected sess-1 in list output")
	}
	if !strings.Contains(v, "sess-2") {
		t.Error("expected sess-2 in list output")
	}
	if !strings.Contains(v, "2 total") {
		t.Error("expected '2 total' count")
	}
}

func TestListModel_navigation(t *testing.T) {
	m := newListModel(nil)
	m.setSize(120, 40)
	m.setSessions(sessionListMsg{
		{ID: "s1"},
		{ID: "s2"},
		{ID: "s3"},
	})

	// Initial cursor at 0
	if m.selectedID() != "s1" {
		t.Errorf("expected s1, got %s", m.selectedID())
	}

	// Move down
	m.handleKey(testKeyMsg("j"))
	if m.selectedID() != "s2" {
		t.Errorf("expected s2 after j, got %s", m.selectedID())
	}

	// Move down again
	m.handleKey(testKeyMsg("j"))
	if m.selectedID() != "s3" {
		t.Errorf("expected s3 after j, got %s", m.selectedID())
	}

	// Move down at bottom — stays at s3
	m.handleKey(testKeyMsg("j"))
	if m.selectedID() != "s3" {
		t.Errorf("expected s3 at bottom, got %s", m.selectedID())
	}

	// Move up
	m.handleKey(testKeyMsg("k"))
	if m.selectedID() != "s2" {
		t.Errorf("expected s2 after k, got %s", m.selectedID())
	}
}

func TestDetailView_notLoaded(t *testing.T) {
	m := newDetailModel()
	v := m.view()
	if !strings.Contains(v, "No session selected") {
		t.Errorf("expected 'No session selected', got: %s", v)
	}
}

func TestDetailView_scroll(t *testing.T) {
	m := newDetailModel()
	m.setSize(80, 10) // Very small height to force scrolling

	sess := &session.Session{
		ID:       "scroll-test",
		Provider: session.ProviderClaudeCode,
		Branch:   "main",
		Messages: make([]session.Message, 20), // Many messages to exceed viewport
	}
	m.setSession(sess)

	// Initially at scroll 0
	if m.scroll != 0 {
		t.Errorf("expected initial scroll 0, got %d", m.scroll)
	}

	// Scroll down
	m.handleKey(testKeyMsg("j"))
	if m.scroll != 1 {
		t.Errorf("expected scroll 1 after j, got %d", m.scroll)
	}

	// Scroll up
	m.handleKey(testKeyMsg("k"))
	if m.scroll != 0 {
		t.Errorf("expected scroll 0 after k, got %d", m.scroll)
	}

	// Can't scroll above 0
	m.handleKey(testKeyMsg("k"))
	if m.scroll != 0 {
		t.Errorf("expected scroll 0 at top, got %d", m.scroll)
	}
}

func TestEnterKey_navigatesToDetail(t *testing.T) {
	// Create a Model in the list view with sessions loaded
	m := Model{
		active: viewList,
		width:  120,
		height: 40,
	}
	m.list.setSize(120, 37)
	m.list.setSessions(sessionListMsg{
		{ID: "sess-abc", Provider: "claude-code", Branch: "main", MessageCount: 3},
		{ID: "sess-def", Provider: "open-code", Branch: "feature", MessageCount: 5},
	})

	// Verify we start in list view on first session
	if m.active != viewList {
		t.Fatalf("expected viewList, got %d", m.active)
	}
	if m.list.selectedID() != "sess-abc" {
		t.Fatalf("expected sess-abc selected, got %s", m.list.selectedID())
	}

	// Simulate pressing Enter (real Enter key, not runes)
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)
	updatedModel := result.(Model)

	// The view should IMMEDIATELY switch to detail (with loading state)
	if updatedModel.active != viewDetail {
		t.Fatalf("expected viewDetail immediately after Enter, got %d", updatedModel.active)
	}
	if !updatedModel.detail.loading {
		t.Fatal("expected detail.loading to be true while async load is in progress")
	}

	// A command MUST have been returned (the async loader)
	if cmd == nil {
		t.Fatal("expected a tea.Cmd from Enter key, got nil")
	}

	t.Logf("Enter key correctly switched to detail view with loading state")

	// Now simulate receiving sessionDetailMsg (what happens after async load succeeds)
	testSession := &session.Session{
		ID:       "sess-abc",
		Provider: session.ProviderClaudeCode,
		Branch:   "main",
	}
	result2, _ := updatedModel.Update(sessionDetailMsg{session: testSession})
	finalModel := result2.(Model)

	// NOW the view should be detail
	if finalModel.active != viewDetail {
		t.Errorf("expected viewDetail after sessionDetailMsg, got %d", finalModel.active)
	}

	// And the detail should have the session loaded (no longer in loading state)
	if finalModel.detail.loading {
		t.Error("expected detail.loading to be false after session loaded")
	}
	if finalModel.detail.session == nil {
		t.Error("expected detail.session to be set")
	}
	if finalModel.detail.session.ID != "sess-abc" {
		t.Errorf("expected detail session ID sess-abc, got %s", finalModel.detail.session.ID)
	}
	if !finalModel.detail.loaded {
		t.Error("expected detail.loaded to be true")
	}
}

// TestEnterKey_detailViewRendersContent verifies that after navigating to the
// detail view via Enter + sessionDetailMsg, the View() actually renders the
// session content (not the "No session selected" fallback).
func TestEnterKey_detailViewRendersContent(t *testing.T) {
	m := Model{
		active: viewList,
		width:  120,
		height: 40,
	}
	m.list.setSize(120, 37)
	m.detail.setSize(120, 37)
	m.list.setSessions(sessionListMsg{
		{ID: "sess-abc", Provider: "claude-code", Branch: "main"},
	})

	// Step 1: Press Enter
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, _ := m.Update(enterMsg)
	m = result.(Model)

	// Step 2: Receive sessionDetailMsg
	testSession := &session.Session{
		ID:       "sess-abc",
		Provider: session.ProviderClaudeCode,
		Branch:   "main",
		Summary:  "Test session for enter-key bug",
		Messages: []session.Message{
			{ID: "msg-1", Role: session.RoleUser, Content: "Hello from test"},
		},
	}
	result, _ = m.Update(sessionDetailMsg{session: testSession})
	m = result.(Model)

	// Step 3: Render the view
	output := m.View()

	// The view should show the session detail, NOT the fallback
	if strings.Contains(output, "No session selected") {
		t.Error("detail view shows 'No session selected' even though a session was loaded — the session data was lost!")
	}
	if !strings.Contains(output, "sess-abc") {
		t.Error("detail view does not contain session ID 'sess-abc'")
	}
	if !strings.Contains(output, "Test session for enter-key bug") {
		t.Error("detail view does not contain session summary")
	}
	if !strings.Contains(output, "Hello from test") {
		t.Error("detail view does not contain message content")
	}

	// Check detail tab is active
	if !strings.Contains(output, "Detail") {
		t.Error("detail tab not visible in output")
	}
}

// TestEnterKey_fullBubbleteaLoop simulates the actual bubbletea program loop
// to verify the Enter key flow works end-to-end with proper tea.Model interface
// boxing/unboxing, exactly as bubbletea does it at runtime.
func TestEnterKey_fullBubbleteaLoop(t *testing.T) {
	// Start with a Model (value type), just like tui.New() returns.
	initialModel := Model{
		active: viewList,
		width:  120,
		height: 40,
	}
	initialModel.list.setSize(120, 37)
	initialModel.detail.setSize(120, 37)
	initialModel.list.setSessions(sessionListMsg{
		{ID: "sess-abc", Provider: "claude-code", Branch: "main", MessageCount: 3},
	})

	// Bubbletea stores the model as tea.Model (interface).
	// This is what tea.NewProgram does internally.
	var model tea.Model = initialModel

	// Step 1: Simulate Enter keypress (exactly as bubbletea does it)
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	var cmd tea.Cmd
	model, cmd = model.Update(enterMsg)

	// Verify cmd was returned
	if cmd == nil {
		t.Fatal("BUG: Enter key did not return a tea.Cmd")
	}

	// Verify immediately switched to detail view with loading state
	m1 := model.(Model)
	if m1.active != viewDetail {
		t.Fatalf("expected viewDetail immediately after Enter, got %d", m1.active)
	}
	if !m1.detail.loading {
		t.Fatal("expected detail.loading to be true while async load is in progress")
	}

	// Step 2: Bubbletea would execute cmd() in a goroutine and send result.
	// We simulate this by directly creating the expected message.
	detailMsg := sessionDetailMsg{
		session: &session.Session{
			ID:       "sess-abc",
			Provider: session.ProviderClaudeCode,
			Branch:   "main",
			Summary:  "Test session",
			Messages: []session.Message{
				{ID: "m1", Role: session.RoleUser, Content: "Hello"},
			},
		},
	}

	// Bubbletea calls model.Update(detailMsg) on the CURRENT model
	model, _ = model.Update(detailMsg)

	// Step 3: Verify the transition happened
	m2 := model.(Model)
	if m2.active != viewDetail {
		t.Errorf("BUG: expected viewDetail after sessionDetailMsg, got %d", m2.active)
	}
	if m2.detail.session == nil {
		t.Fatal("BUG: detail.session is nil after sessionDetailMsg")
	}
	if m2.detail.session.ID != "sess-abc" {
		t.Errorf("expected session ID sess-abc, got %s", m2.detail.session.ID)
	}
	if !m2.detail.loaded {
		t.Error("expected detail.loaded to be true")
	}
	if len(m2.detail.lines) == 0 {
		t.Error("expected detail.lines to be built")
	}

	// Step 4: Verify the rendered view shows session content
	output := model.View()
	if strings.Contains(output, "No session selected") {
		t.Error("BUG: View shows 'No session selected' after successful navigation")
	}
	if !strings.Contains(output, "sess-abc") {
		t.Error("View does not contain session ID")
	}
	if !strings.Contains(output, "Test session") {
		t.Error("View does not contain session summary")
	}
}

// TestEnterKey_loadFailure verifies that when the async session load fails,
// we navigate back to the list view with an error message.
func TestEnterKey_loadFailure(t *testing.T) {
	m := Model{
		active: viewList,
		width:  120,
		height: 40,
	}
	m.list.setSize(120, 37)
	m.detail.setSize(120, 37)
	m.list.setSessions(sessionListMsg{
		{ID: "sess-abc", Provider: "claude-code", Branch: "main"},
	})

	// Step 1: Press Enter — should switch to detail with loading
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)
	m = result.(Model)

	if m.active != viewDetail {
		t.Fatalf("expected viewDetail after Enter, got %d", m.active)
	}
	if !m.detail.loading {
		t.Fatal("expected detail.loading to be true")
	}
	if cmd == nil {
		t.Fatal("expected a tea.Cmd from Enter key")
	}

	// Step 2: Simulate load failure (errMsg arrives)
	result, _ = m.Update(errMsg{fmt.Errorf("session not found: no such session")})
	m = result.(Model)

	// Should go back to list view
	if m.active != viewList {
		t.Errorf("expected viewList after load error, got %d", m.active)
	}
	if m.detail.loading {
		t.Error("expected detail.loading to be false after error")
	}
	// Error should be visible in status
	if !m.status.isError {
		t.Error("expected error status to be set")
	}
	if !strings.Contains(m.status.text, "session not found") {
		t.Errorf("expected error text to contain 'session not found', got %q", m.status.text)
	}
}

// TestDetailView_loading verifies the loading state renders correctly.
func TestDetailView_loading(t *testing.T) {
	m := newDetailModel()
	m.startLoading()
	v := m.view()
	if !strings.Contains(v, "Loading session") {
		t.Errorf("expected 'Loading session' during loading state, got: %s", v)
	}
}

// testKeyMsg creates a tea.KeyMsg for testing.
func testKeyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(s),
	}
}
