// Package claude implements the LLM client port using the Claude CLI binary.
// It calls `claude --print --output-format json` for single-turn completions.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/llm"
)

// Client calls the `claude` CLI binary for LLM completions.
// The binary must be available in PATH (or set explicitly via WithBinary).
type Client struct {
	binaryPath string
}

// Option configures the Claude CLI client.
type Option func(*Client)

// WithBinary sets the path to the claude binary.
// Default: "claude" (found in PATH).
func WithBinary(path string) Option {
	return func(c *Client) {
		c.binaryPath = path
	}
}

// New creates a Claude CLI client with the given options.
func New(opts ...Option) *Client {
	c := &Client{
		binaryPath: "claude",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// cliResponse is the JSON structure returned by `claude --print --output-format json`.
type cliResponse struct {
	Result       string  `json:"result"`
	Model        string  `json:"model"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	// SessionID and other fields are present but not needed.
}

// Complete sends a prompt to the Claude CLI and returns the response.
// It uses `claude --print --output-format json` for non-interactive single-turn mode.
func (c *Client) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	args := []string{"--print", "--output-format", "json"}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.SystemPrompt != "" {
		args = append(args, "--system-prompt", req.SystemPrompt)
	}
	if req.MaxTokens > 0 {
		args = append(args, "--max-tokens", fmt.Sprintf("%d", req.MaxTokens))
	}

	// The user prompt is passed via stdin to avoid shell escaping issues.
	args = append(args, "--prompt", "-")

	cmd := exec.CommandContext(ctx, c.binaryPath, args...)
	cmd.Stdin = strings.NewReader(req.UserPrompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("claude CLI: %s: %w", stderrStr, err)
		}
		return nil, fmt.Errorf("claude CLI: %w", err)
	}

	var resp cliResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		// If JSON parsing fails, try to return the raw output as content.
		// This handles the case where --output-format json is not supported.
		raw := strings.TrimSpace(stdout.String())
		if raw != "" {
			return &llm.CompletionResponse{
				Content: raw,
			}, nil
		}
		return nil, fmt.Errorf("parsing claude response: %w", err)
	}

	return &llm.CompletionResponse{
		Content:      resp.Result,
		Model:        resp.Model,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}, nil
}
