package replay

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Runner is the port interface for executing a prompt via an AI coding agent.
// Each provider (OpenCode, Claude Code) has its own implementation.
type Runner interface {
	// Run sends a user message to the agent and waits for completion.
	// workDir is the directory to run in (the git worktree).
	// Returns the combined stdout+stderr output.
	Run(ctx context.Context, workDir string, message string, opts RunOptions) (string, error)

	// Name returns the runner identifier.
	Name() string
}

// RunOptions configures a single run execution.
type RunOptions struct {
	Agent string // agent name (e.g. "build", "coder")
	Model string // model override (e.g. "provider/model")
}

// ── OpenCode Runner ──

// OpenCodeRunner executes prompts via `opencode run`.
type OpenCodeRunner struct {
	binary string // path to opencode binary (default: "opencode")
}

// NewOpenCodeRunner creates a runner for OpenCode.
func NewOpenCodeRunner() *OpenCodeRunner {
	binary := "opencode"
	if path, err := exec.LookPath("opencode"); err == nil {
		binary = path
	}
	return &OpenCodeRunner{binary: binary}
}

func (r *OpenCodeRunner) Name() string { return string(session.ProviderOpenCode) }

func (r *OpenCodeRunner) Run(ctx context.Context, workDir string, message string, opts RunOptions) (string, error) {
	args := []string{"run"}

	if opts.Agent != "" {
		args = append(args, "--agent", opts.Agent)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	args = append(args, "--dir", workDir)
	args = append(args, "--format", "json")
	args = append(args, message)

	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Dir = workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("opencode run failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

// ── Claude Code Runner ──

// ClaudeCodeRunner executes prompts via `claude -p`.
type ClaudeCodeRunner struct {
	binary string
}

// NewClaudeCodeRunner creates a runner for Claude Code.
func NewClaudeCodeRunner() *ClaudeCodeRunner {
	binary := "claude"
	if path, err := exec.LookPath("claude"); err == nil {
		binary = path
	}
	return &ClaudeCodeRunner{binary: binary}
}

func (r *ClaudeCodeRunner) Name() string { return string(session.ProviderClaudeCode) }

func (r *ClaudeCodeRunner) Run(ctx context.Context, workDir string, message string, opts RunOptions) (string, error) {
	args := []string{"-p", "--output-format", "json"}

	if opts.Agent != "" {
		args = append(args, "--agent", opts.Agent)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	// Claude uses cwd, not --dir.
	args = append(args, message)

	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Dir = workDir // set working directory

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude run failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}
