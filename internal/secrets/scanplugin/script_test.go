package scanplugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestScriptAdapter_Scan(t *testing.T) {
	// Create a test script that echoes back a match
	dir := t.TempDir()
	script := filepath.Join(dir, "scan.sh")
	scriptContent := `#!/bin/sh
# Read stdin (content to scan)
content=$(cat)
# Check if content contains "secret123"
if echo "$content" | grep -q "secret123"; then
  echo '[{"type":"TEST_SECRET","value":"secret123","start_pos":5,"end_pos":14}]'
else
  echo '[]'
fi
`
	if writeErr := os.WriteFile(script, []byte(scriptContent), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}

	adapter := NewScriptAdapter(script, nil, session.SecretModeMask)

	// Test with matching content
	matches := adapter.Scan("here secret123 there")
	if len(matches) != 1 {
		t.Fatalf("Scan() returned %d matches, want 1", len(matches))
	}
	if matches[0].Type != "TEST_SECRET" {
		t.Errorf("Type = %q, want TEST_SECRET", matches[0].Type)
	}
	if matches[0].Value != "secret123" {
		t.Errorf("Value = %q, want secret123", matches[0].Value)
	}
}

func TestScriptAdapter_Scan_noMatches(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "scan.sh")
	scriptContent := `#!/bin/sh
echo '[]'
`
	if writeErr := os.WriteFile(script, []byte(scriptContent), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}

	adapter := NewScriptAdapter(script, nil, session.SecretModeMask)
	matches := adapter.Scan("clean content")
	if len(matches) != 0 {
		t.Errorf("Scan() returned %d matches, want 0", len(matches))
	}
}

func TestScriptAdapter_Scan_scriptFails(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail.sh")
	scriptContent := `#!/bin/sh
exit 1
`
	if writeErr := os.WriteFile(script, []byte(scriptContent), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}

	adapter := NewScriptAdapter(script, nil, session.SecretModeMask)
	matches := adapter.Scan("content")
	if matches != nil {
		t.Errorf("Scan() should return nil on script failure, got %v", matches)
	}
}

func TestScriptAdapter_Scan_scriptNotFound(t *testing.T) {
	adapter := NewScriptAdapter("/nonexistent/script", nil, session.SecretModeMask)
	matches := adapter.Scan("content")
	if matches != nil {
		t.Errorf("Scan() should return nil when script not found, got %v", matches)
	}
}

func TestScriptAdapter_Mask(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "scan.sh")
	// Script always finds sk-abc at positions 4-10 when present
	scriptContent := "#!/bin/sh\ncat > /dev/null\necho '[{\"type\":\"API_KEY\",\"value\":\"sk-abc\",\"start_pos\":4,\"end_pos\":10}]'\n"
	if writeErr := os.WriteFile(script, []byte(scriptContent), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}

	adapter := NewScriptAdapter(script, nil, session.SecretModeMask)

	// First verify Scan returns matches
	matches := adapter.Scan("key=sk-abc!")
	if len(matches) != 1 {
		t.Fatalf("Scan() returned %d matches, want 1", len(matches))
	}

	result := adapter.Mask("key=sk-abc!")
	expected := "key=***REDACTED:API_KEY***!"
	if result != expected {
		t.Errorf("Mask() = %q, want %q", result, expected)
	}
}

func TestScriptAdapter_Mode(t *testing.T) {
	adapter := NewScriptAdapter("echo", nil, session.SecretModeWarn)
	if adapter.Mode() != session.SecretModeWarn {
		t.Errorf("Mode() = %q, want %q", adapter.Mode(), session.SecretModeWarn)
	}
}

func TestScriptAdapter_withArgs(t *testing.T) {
	// Test passing extra args to the script
	dir := t.TempDir()
	script := filepath.Join(dir, "scan.sh")
	scriptContent := `#!/bin/sh
# $1 is the pattern name arg
echo '[]'
`
	if writeErr := os.WriteFile(script, []byte(scriptContent), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}

	adapter := NewScriptAdapter(script, []string{"--extra-arg"}, session.SecretModeMask)
	matches := adapter.Scan("content")
	if matches != nil {
		t.Errorf("Scan() returned %v, want nil", matches)
	}
}
