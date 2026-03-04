package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// sessionDetailMsg carries a loaded session.
type sessionDetailMsg struct {
	session *session.Session
}

// detailModel holds the state for the session detail view.
type detailModel struct {
	session *session.Session
	lines   []string
	width   int
	height  int
	scroll  int
	loaded  bool
	loading bool
}

func newDetailModel() detailModel {
	return detailModel{}
}

func (m *detailModel) setSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *detailModel) setSession(s *session.Session) {
	m.session = s
	m.scroll = 0
	m.loaded = true
	m.loading = false
	m.lines = m.buildLines()
}

func (m *detailModel) startLoading() {
	m.loading = true
	m.loaded = false
	m.session = nil
	m.lines = nil
	m.scroll = 0
}

func (m detailModel) loadSession(f *cmdutil.Factory, sid session.ID) tea.Cmd {
	return func() tea.Msg {
		store, err := f.Store()
		if err != nil {
			return errMsg{fmt.Errorf("opening store: %w", err)}
		}
		session, getErr := store.Get(sid)
		if getErr != nil {
			return errMsg{fmt.Errorf("session not found: %w", getErr)}
		}
		return sessionDetailMsg{session: session}
	}
}

func (m *detailModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		maxScroll := len(m.lines) - m.visibleRows()
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.scroll < maxScroll {
			m.scroll++
		}
		return nil

	case "k", "up":
		if m.scroll > 0 {
			m.scroll--
		}
		return nil

	case "pgdown":
		visibleRows := m.visibleRows()
		m.scroll += visibleRows
		maxScroll := len(m.lines) - visibleRows
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.scroll > maxScroll {
			m.scroll = maxScroll
		}
		return nil

	case "pgup":
		m.scroll -= m.visibleRows()
		if m.scroll < 0 {
			m.scroll = 0
		}
		return nil

	case "r":
		if m.session != nil {
			sid := m.session.ID
			return func() tea.Msg {
				return statusMsg{text: fmt.Sprintf("Restore — use: aisync restore --session %s", sid)}
			}
		}

	case "e":
		if m.session != nil {
			sid := m.session.ID
			return func() tea.Msg {
				return statusMsg{text: fmt.Sprintf("Export — use: aisync export --session %s", sid)}
			}
		}

	case "o":
		if m.session != nil {
			return func() tea.Msg {
				return statusMsg{text: "Comment on PR — use: aisync comment --pr <number>"}
			}
		}
	}

	return nil
}

func (m detailModel) visibleRows() int {
	rows := m.height - 4
	if rows < 3 {
		rows = 3
	}
	return rows
}

func (m detailModel) view() string {
	if m.loading {
		return "\n  Loading session..."
	}
	if !m.loaded || m.session == nil {
		return "\n  No session selected. Press [esc] to go back."
	}

	visibleRows := m.visibleRows()
	end := m.scroll + visibleRows
	if end > len(m.lines) {
		end = len(m.lines)
	}

	var b strings.Builder
	for _, line := range m.lines[m.scroll:end] {
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Scroll indicator
	if len(m.lines) > visibleRows {
		scrollInfo := fmt.Sprintf("  [line %d/%d]", m.scroll+1, len(m.lines))
		b.WriteString(styleMuted.Render(scrollInfo))
		b.WriteString("\n")
	}

	// Actions
	b.WriteString(styleMuted.Render("  [j/k] scroll  [r]estore  [e]xport  [o] comment on PR  [esc] back"))

	return b.String()
}

func (m detailModel) buildLines() []string {
	s := m.session
	if s == nil {
		return nil
	}

	var lines []string

	// Title
	lines = append(lines, styleTitle.Render("  Session Detail"))
	lines = append(lines, "")

	// Metadata
	lines = append(lines, styleSubtitle.Render("  Metadata"))
	lines = append(lines, renderField("  ID", string(s.ID)))
	lines = append(lines, renderField("  Provider", string(s.Provider)))
	lines = append(lines, renderField("  Branch", s.Branch))
	if s.Agent != "" {
		lines = append(lines, renderField("  Agent", s.Agent))
	}
	if s.CommitSHA != "" {
		lines = append(lines, renderField("  Commit", s.CommitSHA))
	}
	lines = append(lines, renderField("  Mode", string(s.StorageMode)))
	lines = append(lines, renderField("  Created", formatTimeAgo(s.CreatedAt)))
	lines = append(lines, "")

	// Token usage
	if s.TokenUsage.TotalTokens > 0 {
		lines = append(lines, styleSubtitle.Render("  Token Usage"))
		lines = append(lines, renderField("  Input", formatTokenCount(s.TokenUsage.InputTokens)))
		lines = append(lines, renderField("  Output", formatTokenCount(s.TokenUsage.OutputTokens)))
		lines = append(lines, renderField("  Total", formatTokenCount(s.TokenUsage.TotalTokens)))
		lines = append(lines, "")
	}

	// Summary
	if s.Summary != "" {
		lines = append(lines, styleSubtitle.Render("  Summary"))
		// Wrap long summaries
		for _, line := range wrapText(s.Summary, 70) {
			lines = append(lines, "  "+styleValue.Render(line))
		}
		lines = append(lines, "")
	}

	// Links
	if len(s.Links) > 0 {
		lines = append(lines, styleSubtitle.Render("  Links"))
		for _, l := range s.Links {
			lines = append(lines, fmt.Sprintf("  %s %s",
				styleLabel.Render(string(l.LinkType)),
				styleValue.Render(l.Ref)))
		}
		lines = append(lines, "")
	}

	// File changes
	if len(s.FileChanges) > 0 {
		lines = append(lines, styleSubtitle.Render(fmt.Sprintf("  File Changes (%d)", len(s.FileChanges))))
		for _, fc := range s.FileChanges {
			icon := changeIcon(fc.ChangeType)
			lines = append(lines, fmt.Sprintf("  %s %s",
				icon,
				styleValue.Render(fc.FilePath)))
		}
		lines = append(lines, "")
	}

	// Messages
	if len(s.Messages) > 0 {
		lines = append(lines, styleSubtitle.Render(fmt.Sprintf("  Messages (%d)", len(s.Messages))))
		lines = append(lines, "")

		for i, msg := range s.Messages {
			// Role badge
			var roleStyle string
			switch msg.Role {
			case session.RoleUser:
				roleStyle = styleHighlight.Render("USER")
			case session.RoleAssistant:
				roleStyle = styleSuccess.Render("ASSISTANT")
			case session.RoleSystem:
				roleStyle = styleWarning.Render("SYSTEM")
			default:
				roleStyle = styleMuted.Render(string(msg.Role))
			}

			header := fmt.Sprintf("  #%d  %s", i+1, roleStyle)
			if msg.Model != "" {
				header += styleMuted.Render("  [" + msg.Model + "]")
			}
			if msg.Tokens > 0 {
				header += styleMuted.Render(fmt.Sprintf("  %d tokens", msg.Tokens))
			}
			lines = append(lines, header)

			// Content (truncated for readability)
			content := msg.Content
			if len(content) > 500 {
				content = content[:497] + "..."
			}
			for _, line := range wrapText(content, 70) {
				lines = append(lines, "    "+styleMuted.Render(line))
			}

			// Tool calls
			if len(msg.ToolCalls) > 0 {
				lines = append(lines, fmt.Sprintf("    %s",
					styleWarning.Render(fmt.Sprintf("[%d tool calls]", len(msg.ToolCalls)))))
				for _, tc := range msg.ToolCalls {
					lines = append(lines, fmt.Sprintf("      %s %s",
						styleMuted.Render(tc.Name),
						styleMuted.Render(string(tc.State))))
				}
			}
			lines = append(lines, "")
		}
	}

	return lines
}

func changeIcon(ct session.ChangeType) string {
	switch ct {
	case session.ChangeCreated:
		return styleSuccess.Render("+")
	case session.ChangeModified:
		return styleWarning.Render("~")
	case session.ChangeDeleted:
		return styleError.Render("-")
	case session.ChangeRead:
		return styleMuted.Render("r")
	default:
		return " "
	}
}

func wrapText(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}

	var lines []string
	for _, paragraph := range strings.Split(s, "\n") {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		for len(paragraph) > width {
			// Find a good break point
			breakAt := width
			for breakAt > width/2 {
				if paragraph[breakAt] == ' ' {
					break
				}
				breakAt--
			}
			if breakAt <= width/2 {
				breakAt = width
			}
			lines = append(lines, paragraph[:breakAt])
			paragraph = paragraph[breakAt:]
			if len(paragraph) > 0 && paragraph[0] == ' ' {
				paragraph = paragraph[1:]
			}
		}
		if paragraph != "" {
			lines = append(lines, paragraph)
		}
	}
	return lines
}
