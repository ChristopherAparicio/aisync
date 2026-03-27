// Package security provides AI agent security analysis.
//
// It detects security threats in AI coding sessions based on OWASP LLM Top 10:
//   - Prompt injection (incoming + processing)
//   - Data exfiltration (suspicious network commands)
//   - Secret exposure (API keys, tokens, credentials)
//   - Dangerous commands (rm -rf, sudo, chmod 777)
//   - Suspicious network activity (curl to unknown hosts, DNS tunneling)
//   - Code injection (eval, exec, backdoor patterns)
package security

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Severity levels for security alerts.
type Severity string

const (
	SeverityCritical Severity = "critical" // immediate action required
	SeverityHigh     Severity = "high"     // significant risk
	SeverityMedium   Severity = "medium"   // potential risk, investigate
	SeverityLow      Severity = "low"      // informational
)

// AlertCategory identifies the type of security threat.
type AlertCategory string

const (
	CategoryPromptInjection AlertCategory = "prompt_injection"
	CategoryExfiltration    AlertCategory = "data_exfiltration"
	CategorySecretExposure  AlertCategory = "secret_exposure"
	CategoryDangerousCmd    AlertCategory = "dangerous_command"
	CategoryNetworkActivity AlertCategory = "network_activity"
	CategoryCodeInjection   AlertCategory = "code_injection"
)

// Alert represents a single detected security issue in a session.
type Alert struct {
	ID           string        `json:"id"`
	Category     AlertCategory `json:"category"`
	Severity     Severity      `json:"severity"`
	Rule         string        `json:"rule"`          // rule that triggered (e.g. "exfil_curl_external")
	Title        string        `json:"title"`         // short headline
	Description  string        `json:"description"`   // detailed explanation
	Evidence     string        `json:"evidence"`      // the actual content that triggered the alert (truncated)
	MessageIndex int           `json:"message_index"` // which message in the session
	ToolCall     string        `json:"tool_call"`     // tool name if applicable (e.g. "bash")
	Timestamp    time.Time     `json:"timestamp"`
}

// ScanResult is the output of analyzing a session for security issues.
type ScanResult struct {
	SessionID     session.ID `json:"session_id"`
	AlertCount    int        `json:"alert_count"`
	CriticalCount int        `json:"critical_count"`
	HighCount     int        `json:"high_count"`
	MediumCount   int        `json:"medium_count"`
	LowCount      int        `json:"low_count"`
	Alerts        []Alert    `json:"alerts"`
	ScannedAt     time.Time  `json:"scanned_at"`
	RiskScore     int        `json:"risk_score"` // 0-100, higher = more risk
}

// ProjectSecuritySummary aggregates security findings across sessions.
type ProjectSecuritySummary struct {
	TotalSessions      int             `json:"total_sessions"`
	SessionsWithAlerts int             `json:"sessions_with_alerts"`
	TotalAlerts        int             `json:"total_alerts"`
	CriticalAlerts     int             `json:"critical_alerts"`
	HighAlerts         int             `json:"high_alerts"`
	AvgRiskScore       float64         `json:"avg_risk_score"`
	TopCategories      []CategoryCount `json:"top_categories"`
	RecentAlerts       []Alert         `json:"recent_alerts"` // last 10
	RiskiestSessions   []SessionRisk   `json:"riskiest_sessions"`
}

// CategoryCount is a count of alerts by category.
type CategoryCount struct {
	Category AlertCategory `json:"category"`
	Count    int           `json:"count"`
}

// SessionRisk summarizes security risk for a single session.
type SessionRisk struct {
	SessionID   session.ID    `json:"session_id"`
	Summary     string        `json:"summary"`
	RiskScore   int           `json:"risk_score"`
	AlertCount  int           `json:"alert_count"`
	TopCategory AlertCategory `json:"top_category"`
}

// Rule is a single security detection rule.
type Rule interface {
	// Name returns the rule identifier.
	Name() string

	// Category returns the alert category this rule detects.
	Category() AlertCategory

	// Scan checks a session and returns any alerts found.
	Scan(sess *session.Session) []Alert
}

// Analyzer is the port interface for the security module.
type Analyzer interface {
	// ScanSession analyzes a session for security threats.
	ScanSession(sess *session.Session) *ScanResult

	// ScanProject analyzes all sessions for a project.
	ScanProject(projectPath string) (*ProjectSecuritySummary, error)
}
