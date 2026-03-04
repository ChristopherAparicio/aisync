package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/llm"
)

func TestNew_defaultBinary(t *testing.T) {
	c := New()
	if c.binaryPath != "claude" {
		t.Errorf("default binaryPath = %q, want %q", c.binaryPath, "claude")
	}
}

func TestNew_withBinary(t *testing.T) {
	c := New(WithBinary("/usr/local/bin/claude"))
	if c.binaryPath != "/usr/local/bin/claude" {
		t.Errorf("binaryPath = %q, want %q", c.binaryPath, "/usr/local/bin/claude")
	}
}

func TestComplete_binaryNotFound(t *testing.T) {
	c := New(WithBinary("this-binary-does-not-exist-anywhere"))
	_, err := c.Complete(context.Background(), llm.CompletionRequest{
		UserPrompt: "hello",
	})
	if err == nil {
		t.Fatal("expected error when binary not found")
	}
}

func TestComplete_mockBinary(t *testing.T) {
	// Create a mock "claude" script that returns valid JSON
	dir := t.TempDir()
	mockBinary := filepath.Join(dir, "claude")

	script := `#!/bin/sh
echo '{"result":"test response","model":"claude-sonnet-4-20250514","input_tokens":10,"output_tokens":5}'
`
	if err := os.WriteFile(mockBinary, []byte(script), 0o755); err != nil {
		t.Fatalf("writing mock binary: %v", err)
	}

	c := New(WithBinary(mockBinary))
	resp, err := c.Complete(context.Background(), llm.CompletionRequest{
		UserPrompt: "test prompt",
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if resp.Content != "test response" {
		t.Errorf("Content = %q, want %q", resp.Content, "test response")
	}
	if resp.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model = %q, want %q", resp.Model, "claude-sonnet-4-20250514")
	}
	if resp.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", resp.InputTokens)
	}
	if resp.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want 5", resp.OutputTokens)
	}
}

func TestComplete_withSystemPrompt(t *testing.T) {
	// Create a mock binary that echoes arguments to stderr (for validation)
	dir := t.TempDir()
	mockBinary := filepath.Join(dir, "claude")

	script := `#!/bin/sh
echo '{"result":"ok","model":"test","input_tokens":1,"output_tokens":1}'
`
	if err := os.WriteFile(mockBinary, []byte(script), 0o755); err != nil {
		t.Fatalf("writing mock binary: %v", err)
	}

	c := New(WithBinary(mockBinary))
	resp, err := c.Complete(context.Background(), llm.CompletionRequest{
		SystemPrompt: "You are a test assistant",
		UserPrompt:   "hello",
		Model:        "sonnet",
		MaxTokens:    100,
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if resp.Content != "ok" {
		t.Errorf("Content = %q, want %q", resp.Content, "ok")
	}
}

func TestComplete_nonJSONFallback(t *testing.T) {
	// Create a mock binary that returns plain text (non-JSON)
	dir := t.TempDir()
	mockBinary := filepath.Join(dir, "claude")

	script := `#!/bin/sh
echo 'This is plain text, not JSON'
`
	if err := os.WriteFile(mockBinary, []byte(script), 0o755); err != nil {
		t.Fatalf("writing mock binary: %v", err)
	}

	c := New(WithBinary(mockBinary))
	resp, err := c.Complete(context.Background(), llm.CompletionRequest{
		UserPrompt: "test",
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	// Should fallback to raw content
	if resp.Content != "This is plain text, not JSON" {
		t.Errorf("Content = %q, want %q", resp.Content, "This is plain text, not JSON")
	}
}

func TestComplete_contextCancelled(t *testing.T) {
	// Create a slow mock binary
	dir := t.TempDir()
	mockBinary := filepath.Join(dir, "claude")

	script := `#!/bin/sh
sleep 10
echo '{"result":"late"}'
`
	if err := os.WriteFile(mockBinary, []byte(script), 0o755); err != nil {
		t.Fatalf("writing mock binary: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	c := New(WithBinary(mockBinary))
	_, err := c.Complete(ctx, llm.CompletionRequest{
		UserPrompt: "test",
	})
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}

// Ensure Client satisfies the llm.Client interface.
var _ llm.Client = (*Client)(nil)
