// Package example demonstrates how to write a custom analysis module for aisync inspect.
//
// To create a new module:
//  1. Create a directory under internal/diagnostic/modules/<name>/
//  2. Implement the diagnostic.AnalysisModule interface (Name, ShouldActivate, Detect)
//  3. Register it in init() via diagnostic.RegisterModule()
//  4. Import the package with a blank import in the wiring file (see below)
//
// This example module detects sessions with an unusually high number of user messages,
// which may indicate the agent is struggling to understand the task.
package example

import (
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/diagnostic"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// NOTE: Do NOT register example in init() — this is a reference implementation.
// Real modules would have:
//   func init() { diagnostic.RegisterModule(&Module{}) }

// ProblemHighUserMsgRatio is the problem ID for this module's detector.
const ProblemHighUserMsgRatio diagnostic.ProblemID = "high-user-message-ratio"

// Module detects sessions where the user sent an unusual number of messages
// relative to the assistant, which may indicate repeated corrections or
// misunderstandings.
type Module struct{}

func (m *Module) Name() string { return "example" }

// ShouldActivate returns true when the session has at least 10 messages.
// Short sessions don't have enough data for meaningful ratio analysis.
func (m *Module) ShouldActivate(sess *session.Session) bool {
	return len(sess.Messages) >= 10
}

// Detect checks the user/assistant message ratio and flags it if unusually high.
func (m *Module) Detect(r *diagnostic.InspectReport, sess *session.Session) []diagnostic.Problem {
	userMsgs := 0
	asstMsgs := 0
	for _, msg := range sess.Messages {
		switch msg.Role {
		case session.RoleUser:
			userMsgs++
		case session.RoleAssistant:
			asstMsgs++
		}
	}

	if asstMsgs == 0 || userMsgs == 0 {
		return nil
	}

	ratio := float64(userMsgs) / float64(asstMsgs)
	if ratio < 0.8 {
		return nil // normal — most sessions have more assistant msgs than user msgs
	}

	sev := diagnostic.SeverityLow
	if ratio > 1.5 {
		sev = diagnostic.SeverityMedium
	}

	// Build details from user messages
	var shortMsgs []string
	for _, msg := range sess.Messages {
		if msg.Role == session.RoleUser && len(msg.Content) > 0 {
			text := msg.Content
			if len(text) > 60 {
				text = text[:57] + "..."
			}
			shortMsgs = append(shortMsgs, text)
		}
	}

	return []diagnostic.Problem{{
		ID:       ProblemHighUserMsgRatio,
		Severity: sev,
		Category: diagnostic.CategoryPatterns,
		Title:    "High user-to-assistant message ratio",
		Observation: fmt.Sprintf("%d user messages vs %d assistant messages (ratio %.1f). "+
			"Sample messages: %s",
			userMsgs, asstMsgs, ratio, strings.Join(shortMsgs[:min(3, len(shortMsgs))], "; ")),
		Impact: fmt.Sprintf("%.0f%% more user messages than typical. High ratio correlates "+
			"with repeated corrections and wasted context.", (ratio-0.5)*100),
		Metric:     ratio,
		MetricUnit: "ratio",
	}}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
