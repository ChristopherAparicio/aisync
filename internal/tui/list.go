package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// sessionListMsg carries loaded session list data.
type sessionListMsg []session.Summary

// listModel holds the state for the session list view.
type listModel struct {
	factory  *cmdutil.Factory
	sessions []session.Summary
	width    int
	height   int
	cursor   int
	offset   int
	loaded   bool
	showAll  bool
}

func newListModel(f *cmdutil.Factory) listModel {
	return listModel{
		factory: f,
		showAll: true,
	}
}

func (m *listModel) setSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *listModel) setSessions(sessions sessionListMsg) {
	m.sessions = sessions
	m.loaded = true
	m.cursor = 0
	m.offset = 0
}

func (m listModel) loadSessions() tea.Cmd {
	f := m.factory
	all := m.showAll
	return func() tea.Msg {
		store, err := f.Store()
		if err != nil {
			return errMsg{fmt.Errorf("opening store: %w", err)}
		}

		gitClient, err := f.Git()
		if err != nil {
			return errMsg{fmt.Errorf("not a git repository")}
		}

		topLevel, _ := gitClient.TopLevel()
		branch, _ := gitClient.CurrentBranch()

		opts := session.ListOptions{
			ProjectPath: topLevel,
			All:         all,
		}
		if !all {
			opts.Branch = branch
		}

		summaries, listErr := store.List(opts)
		if listErr != nil {
			return errMsg{fmt.Errorf("listing sessions: %w", listErr)}
		}

		return sessionListMsg(summaries)
	}
}

func (m listModel) selectedID() session.ID {
	if len(m.sessions) == 0 {
		return ""
	}
	return m.sessions[m.cursor].ID
}

func (m *listModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		if m.cursor < len(m.sessions)-1 {
			m.cursor++
			// Scroll if needed
			visibleRows := m.visibleRows()
			if m.cursor-m.offset >= visibleRows {
				m.offset++
			}
		}
		return nil

	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			if m.cursor < m.offset {
				m.offset = m.cursor
			}
		}
		return nil

	case "a":
		m.showAll = !m.showAll
		return m.loadSessions()

	case "r":
		if sid := m.selectedID(); sid != "" {
			return func() tea.Msg {
				return statusMsg{text: fmt.Sprintf("Restore %s — use: aisync restore --session %s", sid, sid)}
			}
		}

	case "e":
		if sid := m.selectedID(); sid != "" {
			return func() tea.Msg {
				return statusMsg{text: fmt.Sprintf("Export %s — use: aisync export --session %s", sid, sid)}
			}
		}

	case "d":
		if sid := m.selectedID(); sid != "" {
			return m.deleteSession(sid)
		}

	case "pgdown":
		visibleRows := m.visibleRows()
		m.cursor += visibleRows
		if m.cursor >= len(m.sessions) {
			m.cursor = len(m.sessions) - 1
		}
		m.offset = m.cursor - visibleRows + 1
		if m.offset < 0 {
			m.offset = 0
		}
		return nil

	case "pgup":
		visibleRows := m.visibleRows()
		m.cursor -= visibleRows
		if m.cursor < 0 {
			m.cursor = 0
		}
		m.offset = m.cursor
		return nil
	}

	return nil
}

func (m listModel) deleteSession(sid session.ID) tea.Cmd {
	f := m.factory
	return func() tea.Msg {
		store, err := f.Store()
		if err != nil {
			return errMsg{err}
		}
		if delErr := store.Delete(sid); delErr != nil {
			return errMsg{delErr}
		}
		return statusMsg{text: fmt.Sprintf("Deleted session %s", sid)}
	}
}

func (m listModel) visibleRows() int {
	// Reserve lines for header (2) + footer (1) + borders
	rows := m.height - 6
	if rows < 3 {
		rows = 3
	}
	return rows
}

func (m listModel) view() string {
	if !m.loaded {
		return "\n  Loading sessions..."
	}

	var b strings.Builder

	// Title with filter indicator
	title := "  Sessions"
	if m.showAll {
		title += " (all)"
	} else {
		title += " (current branch)"
	}
	title += fmt.Sprintf("  [%d total]", len(m.sessions))
	b.WriteString(styleTitle.Render(title))
	b.WriteString("\n")

	if len(m.sessions) == 0 {
		b.WriteString(styleMuted.Render("\n  No sessions found. Press [a] to toggle all/branch filter.\n"))
		return b.String()
	}

	// Table header
	header := fmt.Sprintf("  %-14s  %-12s  %-22s  %8s  %8s  %s",
		"ID", "PROVIDER", "BRANCH", "MSGS", "TOKENS", "CREATED")
	b.WriteString(styleHeader.Render(header))
	b.WriteString("\n")

	// Table rows
	visibleRows := m.visibleRows()
	end := m.offset + visibleRows
	if end > len(m.sessions) {
		end = len(m.sessions)
	}

	for i := m.offset; i < end; i++ {
		s := m.sessions[i]
		id := truncateStr(string(s.ID), 14)
		prov := truncateStr(string(s.Provider), 12)
		br := truncateStr(s.Branch, 22)
		tokens := formatTokenCount(s.TotalTokens)
		created := formatTimeAgo(s.CreatedAt)

		row := fmt.Sprintf("  %-14s  %-12s  %-22s  %8d  %8s  %s",
			id, prov, br, s.MessageCount, tokens, created)

		if i == m.cursor {
			cursor := styleHighlight.Render(">")
			row = cursor + row[1:]
			b.WriteString(styleSelectedRow.Render(row))
		} else {
			b.WriteString(styleNormalRow.Render(row))
		}
		b.WriteString("\n")
	}

	// Scroll indicator
	if len(m.sessions) > visibleRows {
		scrollInfo := fmt.Sprintf("  showing %d-%d of %d", m.offset+1, end, len(m.sessions))
		b.WriteString(styleMuted.Render(scrollInfo))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Actions
	actions := "  [enter] details  [r]estore  [e]xport  [d]elete  [a] toggle all/branch  [esc] back"
	b.WriteString(styleMuted.Render(actions))

	return b.String()
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

// formatTimeAgo returns a human-readable time ago string.
func formatTimeAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}
