package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ChristopherAparicio/aisync/internal/domain"
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
	types := []domain.ChangeType{
		domain.ChangeCreated,
		domain.ChangeModified,
		domain.ChangeDeleted,
		domain.ChangeRead,
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
	session := &domain.Session{
		ID:       "test-id-123",
		Provider: domain.ProviderClaudeCode,
		Branch:   "feature/test",
		Summary:  "Test session summary",
		TokenUsage: domain.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
			TotalTokens:  1500,
		},
		Messages: []domain.Message{
			{
				ID:      "msg-1",
				Role:    domain.RoleUser,
				Content: "Hello",
			},
			{
				ID:      "msg-2",
				Role:    domain.RoleAssistant,
				Content: "Hi there!",
				Model:   "claude-3",
				Tokens:  100,
			},
		},
		FileChanges: []domain.FileChange{
			{FilePath: "main.go", ChangeType: domain.ChangeModified},
			{FilePath: "new.go", ChangeType: domain.ChangeCreated},
		},
		Links: []domain.Link{
			{LinkType: domain.LinkPR, Ref: "42"},
		},
	}

	m := detailModel{session: session}
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
			Provider:     domain.ProviderClaudeCode,
			Branch:       "main",
			MessageCount: 5,
			TotalTokens:  1000,
			CreatedAt:    time.Now(),
		},
		{
			ID:           "sess-2",
			Provider:     domain.ProviderOpenCode,
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

	session := &domain.Session{
		ID:       "scroll-test",
		Provider: domain.ProviderClaudeCode,
		Branch:   "main",
		Messages: make([]domain.Message, 20), // Many messages to exceed viewport
	}
	m.setSession(session)

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

// testKeyMsg creates a tea.KeyMsg for testing.
func testKeyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(s),
	}
}
