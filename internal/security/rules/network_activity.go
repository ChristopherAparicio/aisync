package rules

import (
	"regexp"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/security"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// NetworkActivity detects suspicious network connections and communications.
type NetworkActivity struct{}

func (r *NetworkActivity) Name() string                     { return "network_activity" }
func (r *NetworkActivity) Category() security.AlertCategory { return security.CategoryNetworkActivity }

var networkPatterns = []struct {
	name     string
	re       *regexp.Regexp
	severity security.Severity
	desc     string
}{
	{"netcat_listen", regexp.MustCompile(`\b(nc|netcat|ncat)\s+.*-l`), security.SeverityCritical,
		"Netcat listener (potential reverse shell)"},
	{"netcat_connect", regexp.MustCompile(`\b(nc|netcat|ncat)\s+\d+\.\d+\.\d+\.\d+`), security.SeverityHigh,
		"Netcat connection to IP address"},
	{"reverse_shell", regexp.MustCompile(`/dev/(tcp|udp)/`), security.SeverityCritical,
		"Bash reverse shell pattern (/dev/tcp)"},
	{"socat", regexp.MustCompile(`\bsocat\b.*TCP`), security.SeverityHigh,
		"Socat TCP connection"},
	{"ssh_tunnel", regexp.MustCompile(`ssh\s+.*-[LRD]\s+`), security.SeverityMedium,
		"SSH tunnel/port forwarding"},
	{"python_http_server", regexp.MustCompile(`python[3]?\s+.*-m\s+http\.server`), security.SeverityMedium,
		"Python HTTP server (potential file exfiltration)"},
	{"dns_lookup_var", regexp.MustCompile(`(dig|nslookup|host)\s+.*\$\(`), security.SeverityHigh,
		"DNS lookup with command substitution (potential DNS exfiltration)"},
	{"tor_proxy", regexp.MustCompile(`(?i)(tor|socks5?://|127\.0\.0\.1:9050)`), security.SeverityHigh,
		"Tor proxy usage detected"},
}

func (r *NetworkActivity) Scan(sess *session.Session) []security.Alert {
	var alerts []security.Alert

	for i, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.Name != "bash" && tc.Name != "Bash" {
				continue
			}
			cmd := tc.Input

			for _, np := range networkPatterns {
				if np.re.MatchString(cmd) {
					alerts = append(alerts, security.Alert{
						Category:     security.CategoryNetworkActivity,
						Severity:     np.severity,
						Rule:         "network_" + np.name,
						Title:        np.desc,
						Description:  "The agent executed a network command that may indicate suspicious activity.",
						Evidence:     truncateEvidence(cmd),
						MessageIndex: i,
						ToolCall:     tc.Name,
					})
				}
			}

			// Detect env var exfiltration via curl.
			if strings.Contains(cmd, "$") && (strings.Contains(cmd, "curl") || strings.Contains(cmd, "wget")) {
				alerts = append(alerts, security.Alert{
					Category:     security.CategoryNetworkActivity,
					Severity:     security.SeverityHigh,
					Rule:         "network_env_in_request",
					Title:        "Environment variable used in HTTP request",
					Description:  "The agent is including environment variables in an HTTP request, which could leak sensitive values.",
					Evidence:     truncateEvidence(cmd),
					MessageIndex: i,
					ToolCall:     tc.Name,
				})
			}
		}
	}
	return alerts
}
