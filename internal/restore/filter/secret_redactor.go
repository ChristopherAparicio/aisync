package filter

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/secrets"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// SecretRedactor scans session content for secrets and replaces them with
// environment variable references like $AWS_ACCESS_KEY_ID instead of the
// legacy ***REDACTED:TYPE*** format.
//
// This makes restored sessions cleaner — the AI sees variable references
// that look natural in code context, rather than jarring redaction markers.
type SecretRedactor struct {
	scanner *secrets.Scanner
}

// NewSecretRedactor creates a SecretRedactor with the default pattern set.
func NewSecretRedactor() *SecretRedactor {
	return &SecretRedactor{
		scanner: secrets.NewScanner(session.SecretModeMask, nil),
	}
}

// NewSecretRedactorWithScanner creates a SecretRedactor with a custom scanner.
func NewSecretRedactorWithScanner(s *secrets.Scanner) *SecretRedactor {
	return &SecretRedactor{scanner: s}
}

// Name returns the filter identifier.
func (f *SecretRedactor) Name() string { return "secret-redactor" }

// Apply scans all message content and tool call inputs/outputs for secrets
// and replaces them with $VARIABLE_NAME references.
func (f *SecretRedactor) Apply(sess *session.Session) (*session.Session, *session.FilterResult, error) {
	cp := session.CopySession(sess)

	totalSecrets := 0
	messagesModified := 0

	for i := range cp.Messages {
		msgModified := false

		// Scan and redact message content.
		if redacted, n := f.redactText(cp.Messages[i].Content); n > 0 {
			cp.Messages[i].Content = redacted
			totalSecrets += n
			msgModified = true
		}

		// Scan and redact thinking content.
		if redacted, n := f.redactText(cp.Messages[i].Thinking); n > 0 {
			cp.Messages[i].Thinking = redacted
			totalSecrets += n
			msgModified = true
		}

		// Scan and redact tool call inputs AND outputs.
		for j := range cp.Messages[i].ToolCalls {
			tc := &cp.Messages[i].ToolCalls[j]

			if redacted, n := f.redactText(tc.Input); n > 0 {
				tc.Input = redacted
				totalSecrets += n
				msgModified = true
			}
			if redacted, n := f.redactText(tc.Output); n > 0 {
				tc.Output = redacted
				totalSecrets += n
				msgModified = true
			}
		}

		// Scan and redact content blocks.
		for j := range cp.Messages[i].ContentBlocks {
			cb := &cp.Messages[i].ContentBlocks[j]
			if redacted, n := f.redactText(cb.Text); n > 0 {
				cb.Text = redacted
				totalSecrets += n
				msgModified = true
			}
			if redacted, n := f.redactText(cb.Thinking); n > 0 {
				cb.Thinking = redacted
				totalSecrets += n
				msgModified = true
			}
		}

		if msgModified {
			messagesModified++
		}
	}

	if totalSecrets == 0 {
		return cp, &session.FilterResult{
			FilterName: f.Name(),
			Applied:    false,
			Summary:    "no secrets detected",
		}, nil
	}

	return cp, &session.FilterResult{
		FilterName:       f.Name(),
		Applied:          true,
		Summary:          fmt.Sprintf("redacted %d secret(s) in %d message(s)", totalSecrets, messagesModified),
		MessagesModified: messagesModified,
		SecretsFound:     totalSecrets,
	}, nil
}

// redactText scans text for secrets and replaces them with $VARIABLE_NAME.
// Returns the redacted text and the count of secrets found.
func (f *SecretRedactor) redactText(content string) (string, int) {
	if content == "" {
		return content, 0
	}

	matches := f.scanner.Scan(content)
	if len(matches) == 0 {
		return content, 0
	}

	// Sort by position ascending, then remove overlapping matches.
	// Multiple patterns can match the same region; we keep the longest match.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].StartPos != matches[j].StartPos {
			return matches[i].StartPos < matches[j].StartPos
		}
		return (matches[i].EndPos - matches[i].StartPos) > (matches[j].EndPos - matches[j].StartPos)
	})

	var deduped []session.SecretMatch
	lastEnd := 0
	for _, m := range matches {
		if m.StartPos < lastEnd {
			continue // overlapping with a previous match, skip
		}
		deduped = append(deduped, m)
		lastEnd = m.EndPos
	}

	// Replace from end to start so byte offsets stay valid.
	result := content
	seen := make(map[string]int) // type -> occurrence count for uniqueness

	for i := len(deduped) - 1; i >= 0; i-- {
		m := deduped[i]
		varName := typeToVarName(m.Type, seen)
		result = result[:m.StartPos] + "$" + varName + result[m.EndPos:]
	}

	return result, len(deduped)
}

// typeToVarName converts a secret type (e.g. "AWS_ACCESS_KEY") to a
// variable name (e.g. "AWS_ACCESS_KEY_ID"). If the same type appears
// multiple times, appends _2, _3, etc.
func typeToVarName(secretType string, seen map[string]int) string {
	base := strings.ToUpper(strings.ReplaceAll(secretType, " ", "_"))

	// Map common secret types to standard env var names.
	varMap := map[string]string{
		"AWS_ACCESS_KEY":     "AWS_ACCESS_KEY_ID",
		"AWS_SECRET_KEY":     "AWS_SECRET_ACCESS_KEY",
		"GITHUB_TOKEN":       "GITHUB_TOKEN",
		"GITHUB_PAT":         "GITHUB_TOKEN",
		"SLACK_TOKEN":        "SLACK_TOKEN",
		"SLACK_WEBHOOK":      "SLACK_WEBHOOK_URL",
		"OPENAI_API_KEY":     "OPENAI_API_KEY",
		"ANTHROPIC_API_KEY":  "ANTHROPIC_API_KEY",
		"STRIPE_SECRET_KEY":  "STRIPE_SECRET_KEY",
		"STRIPE_PUBLISH_KEY": "STRIPE_PUBLISHABLE_KEY",
		"JWT_TOKEN":          "JWT_TOKEN",
		"GENERIC_API_KEY":    "API_KEY",
		"GENERIC_SECRET":     "SECRET_KEY",
		"DATABASE_URL":       "DATABASE_URL",
		"PRIVATE_KEY":        "PRIVATE_KEY",
		"PASSWORD":           "PASSWORD",
		"BEARER_TOKEN":       "BEARER_TOKEN",
		"BASIC_AUTH":         "BASIC_AUTH",
	}

	name := base
	if mapped, ok := varMap[base]; ok {
		name = mapped
	}

	seen[name]++
	if seen[name] > 1 {
		name = fmt.Sprintf("%s_%d", name, seen[name])
	}

	return name
}
