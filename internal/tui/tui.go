// Package tui provides an interactive terminal user interface for aisync.
// It uses the bubbletea Elm architecture: Model -> Update -> View.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// view represents the current active screen.
type view int

const (
	viewDashboard view = iota
	viewList
	viewDetail
)

// statusMsg is a transient message displayed in the status bar.
type statusMsg struct {
	text    string
	isError bool
}

// Model is the top-level bubbletea model for aisync TUI.
type Model struct {
	factory   *cmdutil.Factory
	status    statusMsg
	detail    detailModel
	dashboard dashboardModel
	list      listModel
	width     int
	height    int
	active    view
	showHelp  bool
}

// New creates a new TUI model with the given factory.
func New(f *cmdutil.Factory) Model {
	return Model{
		factory:   f,
		active:    viewDashboard,
		dashboard: newDashboardModel(f),
		list:      newListModel(f),
		detail:    newDetailModel(),
	}
}

// Init initializes the TUI — loads dashboard data.
func (m Model) Init() tea.Cmd {
	return m.dashboard.init()
}

// Update handles all messages/events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.dashboard.setSize(msg.Width, msg.Height-3)
		m.list.setSize(msg.Width, msg.Height-3)
		m.detail.setSize(msg.Width, msg.Height-3)
		return m, nil

	case dashboardDataMsg:
		m.dashboard.setData(msg)
		return m, nil

	case sessionListMsg:
		m.list.setSessions(msg)
		return m, nil

	case sessionDetailMsg:
		m.detail.setSession(msg.session)
		m.active = viewDetail
		return m, nil

	case statusMsg:
		m.status = msg
		return m, nil

	case errMsg:
		m.status = statusMsg{text: msg.Error(), isError: true}
		// If we're in detail view and still loading, the load failed — go back to list.
		if m.active == viewDetail && m.detail.loading {
			m.detail.loading = false
			m.active = viewList
		}
		return m, nil
	}

	return m, nil
}

// View renders the current screen.
func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	// Tab bar
	tabs := m.renderTabs()

	// Active view content
	var content string
	switch m.active {
	case viewDashboard:
		content = m.dashboard.view()
	case viewList:
		content = m.list.view()
	case viewDetail:
		content = m.detail.view()
	}

	// Status bar
	status := m.renderStatusBar()

	return lipgloss.JoinVertical(lipgloss.Left, tabs, content, status)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global keys (always active)
	switch {
	case msg.String() == "q" || msg.String() == "ctrl+c":
		// In detail view, q goes back; ctrl+c always quits
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.active == viewDetail {
			m.active = viewList
			return m, nil
		}
		if m.active == viewList {
			m.active = viewDashboard
			return m, nil
		}
		return m, tea.Quit

	case msg.String() == "?":
		m.showHelp = !m.showHelp
		return m, nil

	case msg.String() == "tab":
		switch m.active {
		case viewDashboard:
			m.active = viewList
			return m, m.list.loadSessions()
		case viewList:
			m.active = viewDashboard
			return m, nil
		case viewDetail:
			m.active = viewList
			return m, nil
		}
		return m, nil

	case msg.String() == "esc":
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		switch m.active {
		case viewDetail:
			m.active = viewList
		case viewList:
			m.active = viewDashboard
		}
		return m, nil
	}

	// Delegate to active view
	switch m.active {
	case viewDashboard:
		cmd := m.dashboard.handleKey(msg)
		if cmd != nil {
			return m, cmd
		}
		// Handle dashboard actions that switch views
		switch msg.String() {
		case "l":
			m.active = viewList
			return m, m.list.loadSessions()
		}

	case viewList:
		cmd := m.list.handleKey(msg)
		if cmd != nil {
			return m, cmd
		}
		// Enter on a selected session → detail view
		if msg.String() == "enter" {
			if sid := m.list.selectedID(); sid != "" {
				m.detail.startLoading()
				m.active = viewDetail
				return m, m.detail.loadSession(m.factory, sid)
			}
		}

	case viewDetail:
		cmd := m.detail.handleKey(msg)
		if cmd != nil {
			return m, cmd
		}
	}

	return m, nil
}

func (m Model) renderTabs() string {
	tabs := []struct {
		label  string
		active bool
	}{
		{"Dashboard", m.active == viewDashboard},
		{"Sessions", m.active == viewList},
		{"Detail", m.active == viewDetail},
	}

	var rendered []string
	for _, t := range tabs {
		if t.active {
			rendered = append(rendered, styleActiveTab.Render(t.label))
		} else {
			rendered = append(rendered, styleTab.Render(t.label))
		}
	}

	tabLine := lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
	separator := lipgloss.NewStyle().
		Foreground(colorSecondary).
		Width(m.width).
		Render("─────────────────────────────────────────────────────────────────────────────────")

	return lipgloss.JoinVertical(lipgloss.Left, tabLine, separator)
}

func (m Model) renderStatusBar() string {
	var left string
	if m.status.text != "" {
		if m.status.isError {
			left = styleError.Render("Error: " + m.status.text)
		} else {
			left = styleSuccess.Render(m.status.text)
		}
	}

	help := styleMuted.Render("tab: switch view  ?: help  q: quit")

	if m.showHelp {
		help = m.renderHelpOverlay()
	}

	if left != "" {
		return fmt.Sprintf("%s  %s", left, help)
	}
	return help
}

func (m Model) renderHelpOverlay() string {
	var s string
	switch m.active {
	case viewDashboard:
		s = "Dashboard: [c]apture  [s]ync  [l]ist sessions  [tab] switch  [q] quit"
	case viewList:
		s = "Sessions: [j/k] navigate  [enter] details  [r]estore  [e]xport  [d]elete  [/] filter  [esc] back  [q] back"
	case viewDetail:
		s = "Detail: [j/k] scroll  [r]estore  [e]xport  [o] comment on PR  [esc] back  [q] back"
	}
	return styleMuted.Render(s)
}

// errMsg wraps an error for the bubbletea message system.
type errMsg struct{ error }
