package rules

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/security"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// DataExfiltration detects attempts to send data to external services.
type DataExfiltration struct{}

func (r *DataExfiltration) Name() string                     { return "data_exfiltration" }
func (r *DataExfiltration) Category() security.AlertCategory { return security.CategoryExfiltration }

var exfilPatterns = []*regexp.Regexp{
	// curl/wget to external hosts with data
	regexp.MustCompile(`(?i)curl\s+.*-[dX]\s+.*https?://`),
	regexp.MustCompile(`(?i)curl\s+.*--data.*https?://`),
	regexp.MustCompile(`(?i)wget\s+.*--post-(data|file).*https?://`),
	// Piping sensitive data to network commands
	regexp.MustCompile(`(?i)(cat|echo|printf)\s+.*\|\s*(curl|wget|nc|netcat)`),
	// Base64 encoding + sending (common exfiltration technique)
	regexp.MustCompile(`(?i)base64\s+.*\|\s*(curl|wget|nc)`),
	// DNS exfiltration
	regexp.MustCompile(`(?i)(dig|nslookup|host)\s+.*\$`),
}

// Known safe hosts that agents commonly interact with.
var safeHosts = map[string]bool{
	"github.com": true, "api.github.com": true,
	"gitlab.com":         true,
	"registry.npmjs.org": true, "pypi.org": true,
	"pkg.go.dev": true, "proxy.golang.org": true, "sum.golang.org": true,
	"localhost": true, "127.0.0.1": true, "0.0.0.0": true,
}

func (r *DataExfiltration) Scan(sess *session.Session) []security.Alert {
	var alerts []security.Alert

	for i, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.Name != "bash" && tc.Name != "Bash" {
				continue
			}
			cmd := tc.Input

			// Check explicit exfiltration patterns.
			for _, re := range exfilPatterns {
				if re.MatchString(cmd) {
					alerts = append(alerts, security.Alert{
						Category:     security.CategoryExfiltration,
						Severity:     security.SeverityCritical,
						Rule:         "exfil_data_transfer",
						Title:        "Potential data exfiltration via " + extractCommand(cmd),
						Description:  "A bash command appears to send data to an external service.",
						Evidence:     truncateEvidence(cmd),
						MessageIndex: i,
						ToolCall:     tc.Name,
					})
				}
			}

			// Check curl/wget to unknown external hosts.
			if strings.Contains(cmd, "curl") || strings.Contains(cmd, "wget") {
				host := extractHTTPHost(cmd)
				if host != "" && !safeHosts[host] && !isLocalhost(host) {
					alerts = append(alerts, security.Alert{
						Category:     security.CategoryExfiltration,
						Severity:     security.SeverityMedium,
						Rule:         "exfil_external_request",
						Title:        "HTTP request to external host: " + host,
						Description:  "The agent made an HTTP request to a non-standard external host.",
						Evidence:     truncateEvidence(cmd),
						MessageIndex: i,
						ToolCall:     tc.Name,
					})
				}
			}
		}
	}
	return alerts
}

func extractCommand(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) > 0 {
		return fields[0]
	}
	return "bash"
}

func extractHTTPHost(cmd string) string {
	re := regexp.MustCompile(`https?://([^/\s:]+)`)
	match := re.FindStringSubmatch(cmd)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func isLocalhost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "0.0.0.0" ||
		strings.HasPrefix(host, "192.168.") || strings.HasPrefix(host, "10.") ||
		strings.HasSuffix(host, ".local")
}

// extractURLHost parses a URL and returns the host.
func extractURLHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
