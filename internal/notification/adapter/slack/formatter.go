package slack

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/notification"
)

// Formatter renders notification events as Slack Block Kit messages.
type Formatter struct{}

// NewFormatter creates a Slack Block Kit formatter.
func NewFormatter() *Formatter {
	return &Formatter{}
}

// Format renders an event into a Slack Block Kit message.
func (f *Formatter) Format(event notification.Event) (notification.RenderedMessage, error) {
	var blocks []block
	var fallback string

	switch event.Type {
	case notification.EventBudgetAlert:
		blocks, fallback = f.formatBudgetAlert(event)
	case notification.EventErrorSpike:
		blocks, fallback = f.formatErrorSpike(event)
	case notification.EventSessionCaptured:
		blocks, fallback = f.formatSessionCaptured(event)
	case notification.EventDailyDigest:
		blocks, fallback = f.formatDailyDigest(event)
	case notification.EventWeeklyReport:
		blocks, fallback = f.formatWeeklyReport(event)
	case notification.EventPersonalDaily:
		blocks, fallback = f.formatPersonalDigest(event)
	case notification.EventRecommendation:
		blocks, fallback = f.formatRecommendations(event)
	default:
		blocks = []block{section(fmt.Sprintf("*%s* notification from aisync", event.Type))}
		fallback = string(event.Type)
	}

	// Add dashboard link if available
	if event.DashboardURL != "" {
		blocks = append(blocks, contextBlock(fmt.Sprintf("<%s|View in Dashboard>", event.DashboardURL)))
	}

	payload := slackPayload{
		Blocks: blocks,
		Text:   fallback,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return notification.RenderedMessage{}, fmt.Errorf("slack: marshal: %w", err)
	}

	return notification.RenderedMessage{
		Body:         body,
		FallbackText: fallback,
	}, nil
}

// ── Event-specific formatters ──

func (f *Formatter) formatBudgetAlert(event notification.Event) ([]block, string) {
	data, ok := event.Data.(notification.BudgetAlertData)
	if !ok {
		return []block{section("Budget alert (invalid data)")}, "Budget alert"
	}

	emoji := ":warning:"
	if data.AlertLevel == "exceeded" {
		emoji = ":rotating_light:"
	}

	title := fmt.Sprintf("%s *Budget Alert: %s*", emoji, event.Project)
	pct := int(data.Percent)
	bar := progressBar(pct)

	details := fmt.Sprintf("*%s:* $%.0f / $%.0f (%d%%)\n%s",
		capitalize(data.AlertType), data.Spent, data.Limit, pct, bar)

	blocks := []block{
		section(title),
		section(details),
	}

	if data.Projected > 0 {
		blocks = append(blocks, section(fmt.Sprintf("*Projected:* $%.0f by period end", data.Projected)))
	}
	if data.TopConsumer != "" {
		blocks = append(blocks, contextBlock(fmt.Sprintf("Top consumer: %s | Sessions today: %d",
			data.TopConsumer, data.SessionsToday)))
	}

	fallback := fmt.Sprintf("Budget %s: %s — $%.0f/$%.0f (%d%%)",
		data.AlertLevel, event.Project, data.Spent, data.Limit, pct)
	return blocks, fallback
}

func (f *Formatter) formatErrorSpike(event notification.Event) ([]block, string) {
	data, ok := event.Data.(notification.ErrorSpikeData)
	if !ok {
		return []block{section("Error spike (invalid data)")}, "Error spike"
	}

	title := fmt.Sprintf(":fire: *Error Spike: %s*", event.Project)
	details := fmt.Sprintf("*%d errors* in the last %d minutes",
		data.ErrorCount, data.WindowMinutes)

	blocks := []block{
		section(title),
		section(details),
	}

	if len(data.ErrorTypes) > 0 {
		blocks = append(blocks, contextBlock("Error types: "+strings.Join(data.ErrorTypes, ", ")))
	}

	return blocks, fmt.Sprintf("Error spike: %s — %d errors in %d min",
		event.Project, data.ErrorCount, data.WindowMinutes)
}

func (f *Formatter) formatSessionCaptured(event notification.Event) ([]block, string) {
	data, ok := event.Data.(notification.SessionCapturedData)
	if !ok {
		return []block{section("Session captured (invalid data)")}, "Session captured"
	}

	title := fmt.Sprintf(":zap: *Session Captured*: %s", event.Project)
	details := fmt.Sprintf("*Agent:* %s | *Branch:* %s | *Tokens:* %dK\n>%s",
		data.Agent, data.Branch, data.Tokens/1000, data.Summary)

	blocks := []block{
		section(title),
		section(details),
	}

	return blocks, fmt.Sprintf("Session captured: %s (%s on %s)",
		data.SessionID[:min(12, len(data.SessionID))], data.Agent, data.Branch)
}

func (f *Formatter) formatDailyDigest(event notification.Event) ([]block, string) {
	data, ok := event.Data.(notification.DigestData)
	if !ok {
		return []block{section("Daily digest (invalid data)")}, "Daily digest"
	}

	title := fmt.Sprintf(":chart_with_upwards_trend: *Daily AI Report — %s — %s*",
		event.Project, data.Period)

	stats := fmt.Sprintf("Sessions: %d  |  Tokens: %dK  |  Cost: $%.2f  |  Errors: %d",
		data.SessionCount, data.TotalTokens/1000, data.TotalCost, data.ErrorCount)

	blocks := []block{
		section(title),
		section(stats),
	}

	// Owner leaderboard
	if len(data.Owners) > 0 {
		lines := []string{"*Contributors:*"}
		for _, o := range data.Owners {
			prefix := ""
			if o.Kind == "machine" {
				prefix = ":robot_face: "
			}
			lines = append(lines, fmt.Sprintf("  %s%s  %d sessions  $%.2f  %d errors",
				prefix, o.Name, o.SessionCount, o.TotalCost, o.ErrorCount))
		}
		blocks = append(blocks, section(strings.Join(lines, "\n")))
	}

	return blocks, fmt.Sprintf("Daily report: %s — %d sessions, $%.2f",
		event.Project, data.SessionCount, data.TotalCost)
}

func (f *Formatter) formatWeeklyReport(event notification.Event) ([]block, string) {
	data, ok := event.Data.(notification.DigestData)
	if !ok {
		return []block{section("Weekly report (invalid data)")}, "Weekly report"
	}

	title := fmt.Sprintf(":bar_chart: *Weekly AI Report — %s — %s*",
		event.Project, data.Period)

	stats := fmt.Sprintf("Sessions: %d (%s)  |  Cost: $%.2f (%s)  |  Errors: %d (%s)",
		data.SessionCount, data.SessionDelta,
		data.TotalCost, data.CostDelta,
		data.ErrorCount, data.ErrorDelta)

	verdict := ""
	if data.Verdict != "" {
		verdictEmoji := ":white_check_mark:"
		if data.Verdict == "declining" {
			verdictEmoji = ":warning:"
		}
		verdict = fmt.Sprintf("Verdict: %s %s", verdictEmoji, data.Verdict)
	}

	blocks := []block{
		section(title),
		section(stats),
	}
	if verdict != "" {
		blocks = append(blocks, section(verdict))
	}

	// Leaderboard
	if len(data.Owners) > 0 {
		lines := []string{"*Leaderboard:*"}
		for i, o := range data.Owners {
			prefix := fmt.Sprintf("  %d. ", i+1)
			if o.Kind == "machine" {
				prefix = "  :robot_face: "
			}
			lines = append(lines, fmt.Sprintf("%s%s  %d sessions  $%.2f  %d errors",
				prefix, o.Name, o.SessionCount, o.TotalCost, o.ErrorCount))
		}
		blocks = append(blocks, section(strings.Join(lines, "\n")))
	}

	return blocks, fmt.Sprintf("Weekly report: %s — %d sessions, $%.2f",
		event.Project, data.SessionCount, data.TotalCost)
}

func (f *Formatter) formatPersonalDigest(event notification.Event) ([]block, string) {
	data, ok := event.Data.(notification.PersonalDigestData)
	if !ok {
		return []block{section("Personal digest (invalid data)")}, "Your daily AI summary"
	}

	title := fmt.Sprintf(":wave: *Your AI Sessions Today — %s*", data.Period)
	stats := fmt.Sprintf("Sessions: %d  |  Tokens: %dK  |  Cost: $%.2f  |  Errors: %d",
		data.SessionCount, data.TotalTokens/1000, data.TotalCost, data.ErrorCount)

	blocks := []block{
		section(title),
		section(stats),
	}

	if data.TeamAvgCost > 0 {
		diff := ((data.TotalCost - data.TeamAvgCost) / data.TeamAvgCost) * 100
		sign := "+"
		if diff < 0 {
			sign = ""
		}
		blocks = append(blocks, contextBlock(
			fmt.Sprintf("vs Team Average: %s%.0f%% cost ($%.2f vs $%.2f avg)",
				sign, diff, data.TotalCost, data.TeamAvgCost)))
	}

	return blocks, fmt.Sprintf("Your daily AI summary: %d sessions, $%.2f",
		data.SessionCount, data.TotalCost)
}

func (f *Formatter) formatRecommendations(event notification.Event) ([]block, string) {
	data, ok := event.Data.(notification.RecommendationData)
	if !ok {
		return []block{section("Recommendations (invalid data)")}, "AI recommendations"
	}

	project := event.Project
	if project == "" {
		project = "All Projects"
	}

	title := fmt.Sprintf(":bulb: *AI Recommendations — %s*", project)
	summary := fmt.Sprintf("*%d recommendations* (%d high priority)", data.TotalCount, data.HighCount)

	blocks := []block{
		section(title),
		section(summary),
		divider(),
	}

	for _, item := range data.Items {
		priorityEmoji := ":white_circle:"
		switch item.Priority {
		case "high":
			priorityEmoji = ":red_circle:"
		case "medium":
			priorityEmoji = ":large_orange_circle:"
		}

		recTitle := fmt.Sprintf("%s %s *%s*", priorityEmoji, item.Icon, item.Title)
		blocks = append(blocks, section(recTitle))

		detail := fmt.Sprintf(">%s", item.Message)
		if item.Impact != "" {
			detail += fmt.Sprintf("\n>_Impact: %s_", item.Impact)
		}
		blocks = append(blocks, section(detail))
	}

	fallback := fmt.Sprintf("AI recommendations: %s — %d items (%d high)",
		project, data.TotalCount, data.HighCount)
	return blocks, fallback
}

// ── Block Kit helpers ──

type slackPayload struct {
	Blocks []block `json:"blocks"`
	Text   string  `json:"text"` // fallback for notifications
}

type block struct {
	Type     string  `json:"type"`
	Text     *text   `json:"text,omitempty"`
	Elements []block `json:"elements,omitempty"`
}

type text struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func section(markdown string) block {
	return block{
		Type: "section",
		Text: &text{Type: "mrkdwn", Text: markdown},
	}
}

func divider() block {
	return block{Type: "divider"}
}

func contextBlock(markdown string) block {
	return block{
		Type: "context",
		Elements: []block{
			{Type: "mrkdwn", Text: &text{Type: "mrkdwn", Text: markdown}},
		},
	}
}

func progressBar(pct int) string {
	filled := pct / 5
	if filled > 20 {
		filled = 20
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", 20-filled)
}

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
