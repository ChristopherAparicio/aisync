// Package notification implements the Notification bounded context.
//
// Architecture (Hexagonal / Ports & Adapters):
//
//	Domain:   Event types, Recipient, RenderedMessage — pure value objects
//	Ports:    Channel (send), Formatter (render), Router (route events → recipients)
//	Service:  NotificationService orchestrates routing + formatting + dispatch
//	Adapters: slack/, webhook/ — infrastructure implementations of Channel + Formatter
//
// Events are fired by scheduler tasks (DailyDigest, WeeklyReport, BudgetAlert)
// or by PostCaptureFunc hooks. The service is channel-agnostic — Slack, email,
// generic webhooks all implement the same Channel port.
package notification

import "time"

// ── Event Types ──

// EventType identifies what kind of notification event occurred.
type EventType string

const (
	// Real-time alerts
	EventBudgetAlert     EventType = "budget.alert"
	EventErrorSpike      EventType = "error.spike"
	EventSessionCaptured EventType = "session.captured"

	// Scheduled digests
	EventDailyDigest    EventType = "daily.digest"
	EventWeeklyReport   EventType = "weekly.report"
	EventPersonalDaily  EventType = "personal.daily"
	EventRecommendation EventType = "recommendation"
)

// Severity indicates the urgency level of a notification.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// ── Event ──

// Event is the domain event that triggers a notification.
// It carries all the data needed for formatting — no store queries happen
// after this point. Scheduler tasks and hooks are responsible for collecting
// the data and constructing the event.
type Event struct {
	// Type identifies the event kind.
	Type EventType `json:"type"`

	// Severity indicates urgency (info, warning, critical).
	Severity Severity `json:"severity"`

	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`

	// Project is the project context (display name, e.g. "org/repo").
	// Empty for personal or global events.
	Project string `json:"project,omitempty"`

	// ProjectPath is the raw filesystem path (for linking).
	ProjectPath string `json:"project_path,omitempty"`

	// OwnerID is the session owner's user ID (for personal routing).
	OwnerID string `json:"owner_id,omitempty"`

	// DashboardURL is the base URL for "View in dashboard" links.
	DashboardURL string `json:"dashboard_url,omitempty"`

	// Data carries event-specific payload. The concrete type depends on EventType.
	// See BudgetAlertData, ErrorSpikeData, SessionCapturedData, DigestData, etc.
	Data any `json:"data"`
}

// ── Event-specific Data ──

// BudgetAlertData is the payload for EventBudgetAlert.
type BudgetAlertData struct {
	AlertType     string  `json:"alert_type"`     // "monthly" or "daily"
	AlertLevel    string  `json:"alert_level"`    // "warning", "exceeded"
	Spent         float64 `json:"spent"`          // amount spent
	Limit         float64 `json:"limit"`          // budget limit
	Percent       float64 `json:"percent"`        // spent/limit * 100
	Projected     float64 `json:"projected"`      // projected end-of-period spend
	DaysRemaining int     `json:"days_remaining"` // days left in period
	TopConsumer   string  `json:"top_consumer"`   // top spending owner name
	SessionsToday int     `json:"sessions_today"` // session count for today
}

// ErrorSpikeData is the payload for EventErrorSpike.
type ErrorSpikeData struct {
	ErrorCount    int      `json:"error_count"`    // errors in the window
	WindowMinutes int      `json:"window_minutes"` // detection window size
	Sessions      []string `json:"sessions"`       // affected session IDs
	ErrorTypes    []string `json:"error_types"`    // distinct error categories
}

// SessionCapturedData is the payload for EventSessionCaptured.
type SessionCapturedData struct {
	SessionID string `json:"session_id"`
	Provider  string `json:"provider"`
	Agent     string `json:"agent"`
	Branch    string `json:"branch"`
	Summary   string `json:"summary"`
	Tokens    int    `json:"tokens"`
}

// DigestData is the payload for EventDailyDigest and EventWeeklyReport.
type DigestData struct {
	// Period describes the time range (e.g. "2026-04-01" or "W14 2026").
	Period string `json:"period"`

	// Global stats
	SessionCount int     `json:"session_count"`
	TotalTokens  int     `json:"total_tokens"`
	TotalCost    float64 `json:"total_cost"`
	ErrorCount   int     `json:"error_count"`

	// Per-project breakdown
	Projects []DigestProjectData `json:"projects,omitempty"`

	// Per-owner breakdown (for leaderboard)
	Owners []DigestOwnerData `json:"owners,omitempty"`

	// Trends (weekly only)
	SessionDelta string `json:"session_delta,omitempty"` // e.g. "+12%"
	CostDelta    string `json:"cost_delta,omitempty"`    // e.g. "-5%"
	ErrorDelta   string `json:"error_delta,omitempty"`   // e.g. "-38%"
	Verdict      string `json:"verdict,omitempty"`       // e.g. "improving"
}

// DigestProjectData holds per-project stats within a digest.
type DigestProjectData struct {
	Name         string  `json:"name"`
	SessionCount int     `json:"session_count"`
	TotalTokens  int     `json:"total_tokens"`
	TotalCost    float64 `json:"total_cost"`
	ErrorCount   int     `json:"error_count"`
	BudgetPct    float64 `json:"budget_pct,omitempty"` // 0-100, empty if no budget
}

// DigestOwnerData holds per-owner stats within a digest (leaderboard).
type DigestOwnerData struct {
	Name         string  `json:"name"`
	Kind         string  `json:"kind"` // human, machine, unknown
	SessionCount int     `json:"session_count"`
	TotalCost    float64 `json:"total_cost"`
	ErrorCount   int     `json:"error_count"`
}

// PersonalDigestData is the payload for EventPersonalDaily.
type PersonalDigestData struct {
	Period       string  `json:"period"`
	OwnerName    string  `json:"owner_name"`
	SessionCount int     `json:"session_count"`
	TotalTokens  int     `json:"total_tokens"`
	TotalCost    float64 `json:"total_cost"`
	ErrorCount   int     `json:"error_count"`
	TeamAvgCost  float64 `json:"team_avg_cost"` // for comparison
}

// RecommendationData is the payload for EventRecommendation.
type RecommendationData struct {
	// TotalCount is the total number of recommendations generated.
	TotalCount int `json:"total_count"`

	// HighCount is the number of high-priority recommendations.
	HighCount int `json:"high_count"`

	// Items carries the recommendations to display (typically filtered to high-priority).
	Items []RecommendationItem `json:"items"`
}

// RecommendationItem is a single recommendation within a notification payload.
type RecommendationItem struct {
	Type     string `json:"type"`
	Priority string `json:"priority"`
	Icon     string `json:"icon"`
	Title    string `json:"title"`
	Message  string `json:"message"`
	Impact   string `json:"impact,omitempty"`
}

// ── Recipient ──

// RecipientType identifies how to reach the recipient.
type RecipientType string

const (
	RecipientChannel RecipientType = "channel" // post to a shared channel
	RecipientDM      RecipientType = "dm"      // direct message to a user
)

// Recipient describes who should receive a notification and how.
type Recipient struct {
	// Type is "channel" or "dm".
	Type RecipientType `json:"type"`

	// Target is the destination identifier:
	//   - For channels: "#channel-name" or channel ID
	//   - For DMs: user ID (e.g. Slack user ID "U0123ABCDEF")
	Target string `json:"target"`

	// Name is a human-readable label (for logging).
	Name string `json:"name,omitempty"`
}

// ── Rendered Message ──

// RenderedMessage is the formatted output ready for delivery.
// Each adapter produces its own format (Block Kit JSON, HTML email, etc.)
// but they all implement this interface for the service to dispatch.
type RenderedMessage struct {
	// Subject is used by email adapters; ignored by Slack/webhook.
	Subject string `json:"subject,omitempty"`

	// Body is the rendered payload. For Slack this is Block Kit JSON,
	// for webhooks it's the JSON body, for email it's HTML.
	Body []byte `json:"body"`

	// FallbackText is a plain-text summary for notifications that support it
	// (e.g. Slack's "text" field shown in mobile push notifications).
	FallbackText string `json:"fallback_text,omitempty"`
}
