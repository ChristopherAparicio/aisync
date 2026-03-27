package rules

import (
	"regexp"

	"github.com/ChristopherAparicio/aisync/internal/security"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// CodeInjection detects potential backdoor patterns in code written by the agent.
type CodeInjection struct{}

func (r *CodeInjection) Name() string                     { return "code_injection" }
func (r *CodeInjection) Category() security.AlertCategory { return security.CategoryCodeInjection }

var codeInjectionPatterns = []struct {
	name     string
	re       *regexp.Regexp
	severity security.Severity
	desc     string
}{
	// Python
	{"python_exec", regexp.MustCompile(`\bexec\s*\(\s*[^)]*\b(input|request|os\.environ|sys\.argv)`), security.SeverityHigh,
		"Python exec() with user input or environment data"},
	{"python_eval", regexp.MustCompile(`\beval\s*\(\s*[^)]*\b(input|request|os\.environ)`), security.SeverityHigh,
		"Python eval() with user input"},
	{"python_os_system", regexp.MustCompile(`\bos\.system\s*\(\s*[^)]*\b(input|request|f")`), security.SeverityHigh,
		"Python os.system() with dynamic input"},
	{"python_subprocess_shell", regexp.MustCompile(`subprocess\.(call|run|Popen)\s*\([^)]*shell\s*=\s*True`), security.SeverityMedium,
		"Python subprocess with shell=True"},
	{"python_pickle_load", regexp.MustCompile(`pickle\.loads?\s*\(`), security.SeverityMedium,
		"Pickle deserialization (potential code execution)"},

	// JavaScript/Node.js
	{"js_eval", regexp.MustCompile(`\beval\s*\(\s*[^)]*\b(req\.|process\.env|argv)`), security.SeverityHigh,
		"JavaScript eval() with request data"},
	{"js_child_process", regexp.MustCompile(`child_process\.\w+\s*\([^)]*\b(req\.|body\.|query\.)`), security.SeverityHigh,
		"Node.js child_process with request data"},
	{"js_vm_run", regexp.MustCompile(`\bnew\s+vm\.Script\b`), security.SeverityMedium,
		"Node.js vm.Script (dynamic code execution)"},

	// Go
	{"go_exec_cmd", regexp.MustCompile(`exec\.Command\s*\([^)]*\b(r\.|req\.|os\.Getenv)`), security.SeverityHigh,
		"Go exec.Command with request/env data"},

	// SQL injection patterns in generated code
	{"sql_injection_concat", regexp.MustCompile(`(?i)(SELECT|INSERT|UPDATE|DELETE)\s+.*["']\s*\+\s*(req|params|body|input)`), security.SeverityHigh,
		"SQL query string concatenation with user input"},
	{"sql_injection_fstring", regexp.MustCompile(`(?i)f["'].*\b(SELECT|INSERT|UPDATE|DELETE)\b.*\{.*\b(req|params|body|input)`), security.SeverityHigh,
		"SQL query f-string with user input"},

	// Generic backdoor patterns
	{"hardcoded_backdoor", regexp.MustCompile(`(?i)(backdoor|reverse.?shell|bind.?shell|rootkit)`), security.SeverityHigh,
		"Code contains backdoor-related keywords"},
	{"obfuscated_code", regexp.MustCompile(`(?i)(base64\.b64decode|atob|fromCharCode)\s*\(\s*['"][A-Za-z0-9+/=]{50,}`), security.SeverityMedium,
		"Obfuscated code (base64 encoded string being decoded)"},
}

func (r *CodeInjection) Scan(sess *session.Session) []security.Alert {
	var alerts []security.Alert

	for i, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			// Only check code-writing tools.
			switch tc.Name {
			case "Write", "write", "Edit", "edit":
			default:
				continue
			}

			code := tc.Input
			for _, cp := range codeInjectionPatterns {
				if cp.re.MatchString(code) {
					alerts = append(alerts, security.Alert{
						Category:     security.CategoryCodeInjection,
						Severity:     cp.severity,
						Rule:         "code_" + cp.name,
						Title:        cp.desc,
						Description:  "The agent wrote code containing a potentially unsafe pattern.",
						Evidence:     truncateEvidence(cp.re.FindString(code)),
						MessageIndex: i,
						ToolCall:     tc.Name,
					})
				}
			}
		}
	}
	return alerts
}
