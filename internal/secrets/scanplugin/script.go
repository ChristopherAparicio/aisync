package scanplugin

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ScriptAdapter wraps an external script as a SecretScanner.
// The script receives content via stdin and writes JSON matches to stdout.
//
// Expected output format (JSON array):
//
//	[{"type":"API_KEY","value":"sk-abc123","start_pos":10,"end_pos":22}]
//
// If the script exits with a non-zero code, the scan returns an empty result.
type ScriptAdapter struct {
	command string
	mode    session.SecretMode
	args    []string
}

// NewScriptAdapter creates a scanner that delegates to an external script.
func NewScriptAdapter(command string, args []string, mode session.SecretMode) *ScriptAdapter {
	return &ScriptAdapter{
		command: command,
		args:    args,
		mode:    mode,
	}
}

// Scan runs the external script with content on stdin and parses JSON matches from stdout.
func (s *ScriptAdapter) Scan(content string) []session.SecretMatch {
	cmd := exec.Command(s.command, s.args...)
	cmd.Stdin = strings.NewReader(content)

	output, runErr := cmd.Output()
	if runErr != nil {
		// Script failed — treat as no secrets found
		return nil
	}

	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" || trimmed == "[]" {
		return nil
	}

	var matches []session.SecretMatch
	if jsonErr := json.Unmarshal([]byte(trimmed), &matches); jsonErr != nil {
		return nil
	}

	return matches
}

// Mask replaces detected secrets with ***REDACTED:TYPE*** placeholders.
func (s *ScriptAdapter) Mask(content string) string {
	matches := s.Scan(content)
	if len(matches) == 0 {
		return content
	}

	// Replace from end to start so byte offsets stay valid
	result := content
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		if m.StartPos >= 0 && m.EndPos <= len(result) && m.StartPos < m.EndPos {
			replacement := fmt.Sprintf("***REDACTED:%s***", m.Type)
			result = result[:m.StartPos] + replacement + result[m.EndPos:]
		}
	}

	return result
}

// Mode returns the current secret handling mode.
func (s *ScriptAdapter) Mode() session.SecretMode {
	return s.mode
}
