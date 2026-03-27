package rules

import (
	"regexp"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/security"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// PromptInjection detects prompt injection attempts in session inputs and outputs.
// Covers both:
//   - Incoming: malicious content in files, tool results, user messages
//   - Processing: injected instructions that alter agent behavior
type PromptInjection struct{}

func (r *PromptInjection) Name() string                     { return "prompt_injection" }
func (r *PromptInjection) Category() security.AlertCategory { return security.CategoryPromptInjection }

var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+instructions`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior|your)\s+(instructions|rules|guidelines)`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+(a|an|in)\s+`),
	regexp.MustCompile(`(?i)new\s+instructions?\s*[:]\s*`),
	regexp.MustCompile(`(?i)system\s*prompt\s*[:]\s*`),
	regexp.MustCompile(`(?i)override\s+(system|safety|content)\s+(prompt|filter|policy)`),
	regexp.MustCompile(`(?i)\bDAN\b.*\bjailbreak\b`),
	regexp.MustCompile(`(?i)pretend\s+you\s+(are|have)\s+(no|unlimited)`),
	regexp.MustCompile(`(?i)act\s+as\s+if\s+you\s+have\s+no\s+(restrictions|limitations|rules)`),
	regexp.MustCompile(`(?i)forget\s+(everything|all|your)\s+(you|about|instructions)`),
	regexp.MustCompile(`(?i)<\s*system\s*>.*<\s*/\s*system\s*>`),
	regexp.MustCompile(`(?i)\[INST\].*\[/INST\]`),
}

func (r *PromptInjection) Scan(sess *session.Session) []security.Alert {
	var alerts []security.Alert

	for i, msg := range sess.Messages {
		// Check message content.
		for _, re := range injectionPatterns {
			if re.MatchString(msg.Content) {
				alerts = append(alerts, security.Alert{
					Category:     security.CategoryPromptInjection,
					Severity:     security.SeverityCritical,
					Rule:         "prompt_injection_content",
					Title:        "Prompt injection detected in message content",
					Description:  "A message contains patterns that attempt to override AI instructions.",
					Evidence:     truncateEvidence(re.FindString(msg.Content)),
					MessageIndex: i,
				})
			}
		}

		// Check tool call outputs (files read, bash output).
		for _, tc := range msg.ToolCalls {
			text := tc.Input + " " + tc.Output
			for _, re := range injectionPatterns {
				if re.MatchString(text) {
					alerts = append(alerts, security.Alert{
						Category:     security.CategoryPromptInjection,
						Severity:     security.SeverityHigh,
						Rule:         "prompt_injection_tool_output",
						Title:        "Prompt injection in tool output (" + tc.Name + ")",
						Description:  "A tool returned content containing prompt injection patterns. This could be an indirect injection via a malicious file.",
						Evidence:     truncateEvidence(re.FindString(text)),
						MessageIndex: i,
						ToolCall:     tc.Name,
					})
				}
			}
		}
	}
	return alerts
}

func truncateEvidence(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
