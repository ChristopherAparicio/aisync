package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// dashboardDataMsg carries loaded dashboard data.
type dashboardDataMsg struct {
	session            *session.Summary
	branch             string
	repoRoot           string
	provider           string
	hookStatus         string
	platformName       string
	sessionCount       int
	branchSessionCount int
	totalTokens        int
	syncAvailable      bool
}

// dashboardModel holds the state for the dashboard view.
type dashboardModel struct {
	factory *cmdutil.Factory
	data    dashboardDataMsg
	width   int
	height  int
	loaded  bool
}

func newDashboardModel(f *cmdutil.Factory) dashboardModel {
	return dashboardModel{factory: f}
}

func (m *dashboardModel) setSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *dashboardModel) setData(data dashboardDataMsg) {
	m.data = data
	m.loaded = true
}

func (m dashboardModel) init() tea.Cmd {
	f := m.factory
	return func() tea.Msg {
		data := dashboardDataMsg{}

		// Git info
		gitClient, err := f.Git()
		if err != nil {
			return errMsg{fmt.Errorf("not a git repository")}
		}

		data.branch, _ = gitClient.CurrentBranch()
		data.repoRoot, _ = gitClient.TopLevel()

		// Sync branch available?
		data.syncAvailable = gitClient.SyncBranchExists()

		// Hook status
		hooksPath, hookErr := gitClient.HooksPath()
		if hookErr == nil && hooksPath != "" {
			if gitClient.HookExists("pre-commit") {
				data.hookStatus = "installed"
			} else {
				data.hookStatus = "not installed"
			}
		} else {
			data.hookStatus = "unknown"
		}

		// Platform
		plat, platErr := f.Platform()
		if platErr == nil {
			data.platformName = string(plat.Name())
		}

		// Store info
		store, storeErr := f.Store()
		if storeErr == nil {
			// Current branch session
			sess, branchErr := store.GetLatestByBranch(data.repoRoot, data.branch)
			if branchErr == nil {
				summary := &session.Summary{
					ID:           sess.ID,
					Provider:     sess.Provider,
					Branch:       sess.Branch,
					Summary:      sess.Summary,
					MessageCount: len(sess.Messages),
					TotalTokens:  sess.TokenUsage.TotalTokens,
					CreatedAt:    sess.CreatedAt,
				}
				data.session = summary
				// Best-effort count — failure defaults to 0 (single-session display).
				data.branchSessionCount, _ = store.CountByBranch(data.repoRoot, data.branch)
			}

			// Total sessions
			all, listErr := store.List(session.ListOptions{
				ProjectPath: data.repoRoot,
				All:         true,
			})
			if listErr == nil {
				data.sessionCount = len(all)
				for _, s := range all {
					data.totalTokens += s.TotalTokens
				}
			}

			// Provider detection
			registry := f.Registry()
			provs, _ := registry.DetectAll(data.repoRoot, data.branch)
			if len(provs) > 0 {
				var names []string
				for _, p := range provs {
					names = append(names, string(p.Provider))
				}
				data.provider = strings.Join(names, ", ")
			} else {
				data.provider = "none detected"
			}
		}

		return data
	}
}

func (m dashboardModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "c":
		return func() tea.Msg {
			return statusMsg{text: "Capture not yet implemented in TUI — use: aisync capture"}
		}
	case "s":
		return func() tea.Msg {
			return statusMsg{text: "Sync not yet implemented in TUI — use: aisync sync"}
		}
	}
	return nil
}

func (m dashboardModel) view() string {
	if !m.loaded {
		return "\n  Loading dashboard..."
	}

	var b strings.Builder

	// Title
	b.WriteString(styleTitle.Render("  aisync Dashboard"))
	b.WriteString("\n\n")

	// Repository info
	b.WriteString(styleSubtitle.Render("  Repository"))
	b.WriteString("\n")
	b.WriteString(renderField("  Branch", m.data.branch))
	b.WriteString(renderField("  Root", m.data.repoRoot))
	b.WriteString(renderField("  Provider", m.data.provider))
	if m.data.platformName != "" {
		b.WriteString(renderField("  Platform", m.data.platformName))
	}
	b.WriteString("\n")

	// Hook status
	b.WriteString(styleSubtitle.Render("  Git Hooks"))
	b.WriteString("\n")
	hookStyle := styleWarning
	if m.data.hookStatus == "installed" {
		hookStyle = styleSuccess
	}
	b.WriteString(fmt.Sprintf("  %s %s\n",
		styleLabel.Render("Status"),
		hookStyle.Render(m.data.hookStatus)))

	syncStatus := "not available"
	syncStyle := styleMuted
	if m.data.syncAvailable {
		syncStatus = "branch exists"
		syncStyle = styleSuccess
	}
	b.WriteString(fmt.Sprintf("  %s %s\n",
		styleLabel.Render("Sync"),
		syncStyle.Render(syncStatus)))
	b.WriteString("\n")

	// Current session
	branchTitle := "  Current Branch Session"
	if m.data.branchSessionCount > 1 {
		branchTitle = fmt.Sprintf("  Current Branch Session (%d total)", m.data.branchSessionCount)
	}
	b.WriteString(styleSubtitle.Render(branchTitle))
	b.WriteString("\n")
	if m.data.session != nil {
		s := m.data.session
		b.WriteString(renderField("  ID", string(s.ID)))
		b.WriteString(renderField("  Provider", string(s.Provider)))
		b.WriteString(renderField("  Messages", fmt.Sprintf("%d", s.MessageCount)))
		b.WriteString(renderField("  Tokens", formatTokenCount(s.TotalTokens)))
		if s.Summary != "" {
			summary := s.Summary
			if len(summary) > 80 {
				summary = summary[:77] + "..."
			}
			b.WriteString(renderField("  Summary", summary))
		}
	} else {
		b.WriteString(styleMuted.Render("  No session for this branch.\n"))
	}
	b.WriteString("\n")

	// Project stats
	b.WriteString(styleSubtitle.Render("  Project Stats"))
	b.WriteString("\n")
	b.WriteString(renderField("  Sessions", fmt.Sprintf("%d", m.data.sessionCount)))
	b.WriteString(renderField("  Total tokens", formatTokenCount(m.data.totalTokens)))
	b.WriteString("\n")

	// Quick actions
	actions := lipgloss.NewStyle().
		Foreground(colorMuted).
		Render("  [c] capture  [s] sync  [l] sessions list  [tab] switch view  [q] quit")
	b.WriteString(actions)

	return b.String()
}

func renderField(label, value string) string {
	return fmt.Sprintf("  %s %s\n",
		styleLabel.Render(label),
		styleValue.Render(value))
}

func formatTokenCount(n int) string {
	if n == 0 {
		return "0"
	}
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
