package rules

import (
	"regexp"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/security"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// DangerousCommands detects potentially destructive or privileged bash commands.
type DangerousCommands struct{}

func (r *DangerousCommands) Name() string                     { return "dangerous_commands" }
func (r *DangerousCommands) Category() security.AlertCategory { return security.CategoryDangerousCmd }

var dangerousPatterns = []struct {
	name     string
	re       *regexp.Regexp
	severity security.Severity
	desc     string
}{
	{"rm_recursive", regexp.MustCompile(`(?i)\brm\s+(-[rf]+\s+)+/`), security.SeverityCritical,
		"Recursive file deletion from root or system path"},
	{"rm_force_all", regexp.MustCompile(`\brm\s+(-[rf]+\s+)+(~|\$HOME|\*|\.\.)`), security.SeverityHigh,
		"Forceful deletion of home directory or wildcard"},
	{"sudo_usage", regexp.MustCompile(`\bsudo\s+`), security.SeverityMedium,
		"Elevated privilege execution via sudo"},
	{"chmod_world_writable", regexp.MustCompile(`\bchmod\s+(-R\s+)?(777|a\+rwx)`), security.SeverityHigh,
		"Setting world-writable permissions"},
	{"chown_root", regexp.MustCompile(`\bchown\s+(-R\s+)?root`), security.SeverityHigh,
		"Changing file ownership to root"},
	{"dd_disk", regexp.MustCompile(`\bdd\s+.*of=/dev/`), security.SeverityCritical,
		"Direct disk write — potential data destruction"},
	{"mkfs", regexp.MustCompile(`\bmkfs\b`), security.SeverityCritical,
		"Filesystem formatting command"},
	{"eval_shell", regexp.MustCompile(`\beval\s+\$`), security.SeverityHigh,
		"Dynamic command evaluation via eval"},
	{"pip_install_url", regexp.MustCompile(`(?i)pip\s+install\s+.*https?://`), security.SeverityMedium,
		"Installing Python package from URL (potential supply chain risk)"},
	{"npm_install_url", regexp.MustCompile(`(?i)npm\s+install\s+https?://`), security.SeverityMedium,
		"Installing npm package from URL (potential supply chain risk)"},
	{"curl_pipe_sh", regexp.MustCompile(`curl\s+.*\|\s*(ba)?sh`), security.SeverityCritical,
		"Downloading and executing remote script (curl | sh)"},
	{"wget_pipe_sh", regexp.MustCompile(`wget\s+.*-O\s*-\s*\|\s*(ba)?sh`), security.SeverityCritical,
		"Downloading and executing remote script (wget | sh)"},
	{"disable_firewall", regexp.MustCompile(`(?i)(ufw\s+disable|iptables\s+-F|firewall-cmd\s+--remove)`), security.SeverityHigh,
		"Disabling or flushing firewall rules"},
	{"ssh_keygen_overwrite", regexp.MustCompile(`ssh-keygen\s+.*-f\s+/`), security.SeverityMedium,
		"Generating SSH keys in non-standard locations"},
	{"git_force_push", regexp.MustCompile(`git\s+push\s+.*--force`), security.SeverityMedium,
		"Force pushing to remote — potential data loss"},
}

func (r *DangerousCommands) Scan(sess *session.Session) []security.Alert {
	var alerts []security.Alert

	for i, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.Name != "bash" && tc.Name != "Bash" {
				continue
			}
			cmd := tc.Input

			for _, dp := range dangerousPatterns {
				if dp.re.MatchString(cmd) {
					alerts = append(alerts, security.Alert{
						Category:     security.CategoryDangerousCmd,
						Severity:     dp.severity,
						Rule:         "dangerous_" + dp.name,
						Title:        dp.desc,
						Description:  "The agent executed a potentially dangerous bash command.",
						Evidence:     truncateEvidence(cmd),
						MessageIndex: i,
						ToolCall:     tc.Name,
					})
				}
			}

			// Check for dotfile modification (.bashrc, .profile, .ssh/config).
			if strings.Contains(cmd, ".bashrc") || strings.Contains(cmd, ".profile") ||
				strings.Contains(cmd, ".zshrc") || strings.Contains(cmd, ".ssh/") {
				alerts = append(alerts, security.Alert{
					Category:     security.CategoryDangerousCmd,
					Severity:     security.SeverityMedium,
					Rule:         "dangerous_dotfile_modify",
					Title:        "Modification of shell configuration or SSH files",
					Description:  "The agent is modifying dotfiles which could persist changes beyond the session.",
					Evidence:     truncateEvidence(cmd),
					MessageIndex: i,
					ToolCall:     tc.Name,
				})
			}
		}
	}
	return alerts
}
