package rules

import (
	"regexp"

	"github.com/ChristopherAparicio/aisync/internal/security"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// SecretExposure detects API keys, tokens, and credentials in session content.
type SecretExposure struct{}

func (r *SecretExposure) Name() string                     { return "secret_exposure" }
func (r *SecretExposure) Category() security.AlertCategory { return security.CategorySecretExposure }

var secretPatterns = []struct {
	name     string
	re       *regexp.Regexp
	severity security.Severity
}{
	{"AWS access key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`), security.SeverityCritical},
	{"AWS secret key", regexp.MustCompile(`(?i)aws[_\-]?secret[_\-]?access[_\-]?key[\s]*[=:]\s*[A-Za-z0-9/+=]{40}`), security.SeverityCritical},
	{"GitHub token", regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`), security.SeverityHigh},
	{"GitLab token", regexp.MustCompile(`glpat-[A-Za-z0-9\-_]{20,}`), security.SeverityHigh},
	{"OpenAI API key", regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`), security.SeverityHigh},
	{"Anthropic API key", regexp.MustCompile(`sk-ant-[A-Za-z0-9\-_]{20,}`), security.SeverityHigh},
	{"Stripe secret key", regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`), security.SeverityCritical},
	{"Private key", regexp.MustCompile(`-----BEGIN\s+(RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`), security.SeverityCritical},
	{"JWT token", regexp.MustCompile(`eyJ[A-Za-z0-9\-_]{20,}\.eyJ[A-Za-z0-9\-_]{20,}\.[A-Za-z0-9\-_]+`), security.SeverityMedium},
	{"Slack token", regexp.MustCompile(`xox[bprs]-[0-9]{10,}-[A-Za-z0-9\-]+`), security.SeverityHigh},
	{"Generic password", regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[=:]\s*['"]?[^\s'"]{8,}`), security.SeverityMedium},
}

func (r *SecretExposure) Scan(sess *session.Session) []security.Alert {
	var alerts []security.Alert

	for i, msg := range sess.Messages {
		// Only scan assistant messages and tool outputs (user may intentionally share secrets).
		if msg.Role == session.RoleUser {
			continue
		}

		texts := []string{msg.Content}
		for _, tc := range msg.ToolCalls {
			texts = append(texts, tc.Output)
			// Also check if the agent writes secrets to files.
			if tc.Name == "Write" || tc.Name == "write" {
				texts = append(texts, tc.Input)
			}
		}

		for _, text := range texts {
			for _, sp := range secretPatterns {
				if sp.re.MatchString(text) {
					alerts = append(alerts, security.Alert{
						Category:     security.CategorySecretExposure,
						Severity:     sp.severity,
						Rule:         "secret_" + sp.name,
						Title:        sp.name + " detected in output",
						Description:  "The agent produced output containing a potential " + sp.name + ".",
						Evidence:     truncateEvidence(sp.re.FindString(text)),
						MessageIndex: i,
					})
					break // one alert per message per pattern is enough
				}
			}
		}
	}
	return alerts
}
