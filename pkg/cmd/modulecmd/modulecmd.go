// Package modulecmd implements the `aisync module` command group.
// It provides subcommands for managing diagnostic script modules:
//   - init:   scaffold a new script module from a template (interactive or flags)
//   - create: AI-assisted module generation from natural language description
//   - list:   show discovered script modules
package modulecmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/diagnostic"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/llmfactory"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// NewCmdModule creates the `aisync module` command group.
func NewCmdModule(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "module",
		Short: "Manage diagnostic script modules",
		Long: `Script modules are external executables that extend aisync inspect.

They receive session JSON on stdin and output detected problems as JSON on stdout.
Scripts can be written in any language (Python, Bash, Go, etc.).

Modules are discovered from two locations:
  - .aisync/modules/  (per-project, higher priority)
  - ~/.aisync/modules/ (global)

Use 'aisync module init' to create a new module from a template.
Use 'aisync module create' to generate a module from a description (requires LLM).
Use 'aisync module list' to see discovered modules.`,
	}

	cmd.AddCommand(newCmdModuleInit(f))
	cmd.AddCommand(newCmdModuleCreate(f))
	cmd.AddCommand(newCmdModuleList(f))

	return cmd
}

// ── init subcommand ─────────────────────────────────────────────────────────

func newCmdModuleInit(f *cmdutil.Factory) *cobra.Command {
	var (
		lang   string
		global bool
	)

	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Create a new script module from a template",
		Long: `Creates a new diagnostic script module in .aisync/modules/ (or ~/.aisync/modules/ with --global).

When called without arguments, launches an interactive wizard.
When called with a name, creates the module directly.

Examples:
  aisync module init                       # interactive wizard
  aisync module init detect-retry          # Python module in .aisync/modules/
  aisync module init detect-retry --sh     # Shell module
  aisync module init detect-retry --global # In ~/.aisync/modules/`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}

			// Interactive wizard when no name provided
			if name == "" {
				scanner := bufio.NewScanner(os.Stdin)
				out := f.IOStreams.Out

				fmt.Fprintf(out, "\n  === Create a new diagnostic module ===\n\n")

				name = prompt(scanner, "Module name (e.g. detect-retry-loop)", "")
				if name == "" {
					return fmt.Errorf("module name is required")
				}

				langChoice := promptChoice(scanner, "Language:", []string{"Python (recommended)", "Shell (bash)"})
				if langChoice == 1 {
					lang = "sh"
				} else {
					lang = "python"
				}

				scopeChoice := promptChoice(scanner, "Scope:", []string{"Project (.aisync/modules/)", "Global (~/.aisync/modules/)"})
				global = scopeChoice == 1

				fmt.Fprintln(out)
			}

			return createModule(f.IOStreams.Out, name, lang, global)
		},
	}

	cmd.Flags().StringVar(&lang, "lang", "python", "Template language: python, sh")
	cmd.Flags().BoolVar(&global, "global", false, "Create in ~/.aisync/modules/ instead of .aisync/modules/")

	// Shortcuts
	cmd.Flags().Bool("sh", false, "Shortcut for --lang=sh")
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if sh, _ := cmd.Flags().GetBool("sh"); sh {
			lang = "sh"
		}
		return nil
	}

	return cmd
}

// createModule writes a template module script to disk.
// Used by both `init` (from template) and as a helper for write operations.
func createModule(out io.Writer, name, lang string, global bool) error {
	dir, err := moduleDir(global)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	var filename, content string
	switch lang {
	case "sh", "bash":
		filename = name + ".sh"
		content = bashTemplate(name)
	default:
		filename = name + ".py"
		content = pythonTemplate(name)
	}

	path := filepath.Join(dir, filename)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("module already exists: %s", path)
	}

	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		return fmt.Errorf("writing module: %w", err)
	}

	fmt.Fprintf(out, "Created module: %s\n", path)
	fmt.Fprintf(out, "\nNext steps:\n")
	fmt.Fprintf(out, "  1. Edit %s to add your detection logic\n", path)
	fmt.Fprintf(out, "  2. Test it: aisync show --json <session-id> | %s\n", path)
	fmt.Fprintf(out, "  3. It will run automatically on every 'aisync inspect'\n")

	return nil
}

// ── create subcommand (AI-assisted) ─────────────────────────────────────────

func newCmdModuleCreate(f *cmdutil.Factory) *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Generate a module from a natural language description (requires LLM)",
		Long: `Describe what you want to detect in plain language, and an LLM
will generate a working script module for you.

Requires an LLM to be configured (Ollama, Claude CLI, or Anthropic API).
Configure one with 'aisync config set analysis.adapter ollama' or similar.

Examples:
  aisync module create
  aisync module create --global`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := f.IOStreams.Out

			// Get LLM client
			llmClient, err := getLLMClient(f)
			if err != nil {
				fmt.Fprintf(out, "No LLM configured. %v\n", err)
				fmt.Fprintf(out, "\nTo configure one:\n")
				fmt.Fprintf(out, "  aisync config set analysis.adapter ollama\n")
				fmt.Fprintf(out, "  aisync config set analysis.ollama_url http://localhost:11434\n")
				fmt.Fprintf(out, "  aisync config set analysis.model qwen3:8b\n")
				fmt.Fprintf(out, "\nOr use 'aisync module init' for a manual template.\n")
				return nil
			}

			scanner := bufio.NewScanner(os.Stdin)

			fmt.Fprintf(out, "\n  === AI-Assisted Module Creation ===\n\n")
			fmt.Fprintf(out, "  Describe what you want to detect. Be specific.\n")
			fmt.Fprintf(out, "  Examples:\n")
			fmt.Fprintf(out, "    - \"Detect when pytest is run more than 5 times without code changes\"\n")
			fmt.Fprintf(out, "    - \"Flag sessions where npm is used instead of pnpm\"\n")
			fmt.Fprintf(out, "    - \"Alert when git push happens without running tests first\"\n\n")

			description := prompt(scanner, "What should this module detect?", "")
			if description == "" {
				return fmt.Errorf("description is required")
			}

			name := prompt(scanner, "Module name", suggestName(description))
			if name == "" {
				return fmt.Errorf("module name is required")
			}

			scopeChoice := promptChoice(scanner, "Scope:", []string{"Project (.aisync/modules/)", "Global (~/.aisync/modules/)"})
			global = scopeChoice == 1

			// Generate with LLM
			fmt.Fprintf(out, "\nGenerating module with LLM (this may take a minute)...\n")

			script, err := generateScript(cmd.Context(), llmClient, name, description)
			if err != nil {
				return fmt.Errorf("LLM generation failed: %w", err)
			}

			// Show the generated script
			fmt.Fprintf(out, "\n── Generated: %s.py ──\n\n", name)
			fmt.Fprintf(out, "%s\n", script)
			fmt.Fprintf(out, "── end ──\n\n")

			// Confirm
			if !promptYesNo(scanner, "Save and enable this module?", true) {
				fmt.Fprintf(out, "Cancelled.\n")
				return nil
			}

			// Write to disk
			dir, err := moduleDir(global)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("creating directory: %w", err)
			}

			path := filepath.Join(dir, name+".py")
			if _, statErr := os.Stat(path); statErr == nil {
				if !promptYesNo(scanner, fmt.Sprintf("Module %s already exists. Overwrite?", path), false) {
					fmt.Fprintf(out, "Cancelled.\n")
					return nil
				}
			}

			if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
				return fmt.Errorf("writing module: %w", err)
			}

			fmt.Fprintf(out, "\nSaved: %s\n", path)
			fmt.Fprintf(out, "It will run automatically on every 'aisync inspect'.\n")
			fmt.Fprintf(out, "\nTest it: aisync show --json <session-id> | %s\n", path)

			return nil
		},
	}

	cmd.Flags().BoolVar(&global, "global", false, "Create in ~/.aisync/modules/ instead of .aisync/modules/")

	return cmd
}

// getLLMClient tries to create an LLM client from the aisync config.
func getLLMClient(f *cmdutil.Factory) (llm.Client, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, fmt.Errorf("no config available: %w", err)
	}
	return llmfactory.NewClientFromConfig(cfg, "")
}

// generateScript calls the LLM to generate a diagnostic module script.
func generateScript(ctx context.Context, client llm.Client, name, description string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	resp, err := client.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: scriptGenSystemPrompt,
		UserPrompt: fmt.Sprintf(
			"Module name: %s\nDescription: %s\n\nGenerate the Python script.",
			name, description,
		),
		MaxTokens: 4096,
	})
	if err != nil {
		return "", err
	}

	// Extract the script from the response — it may be wrapped in ```python...```
	script := extractScript(resp.Content)
	if script == "" {
		return "", fmt.Errorf("LLM response did not contain a valid script")
	}

	return script, nil
}

// extractScript extracts a Python script from LLM output that may contain
// markdown fences or explanatory text.
//
// It handles, in order:
//  1. ```python ... ``` fenced blocks (preferred)
//  2. ``` ... ``` generic fenced blocks (first one)
//  3. Raw content starting with #!/ or "import "
//
// Returns an empty string if no recognizable script is found.
func extractScript(content string) string {
	// Try to find ```python...``` block
	if idx := strings.Index(content, "```python"); idx >= 0 {
		rest := content[idx+len("```python"):]
		if end := strings.Index(rest, "```"); end >= 0 {
			script := strings.TrimSpace(rest[:end])
			if strings.HasPrefix(script, "#!/") {
				return script + "\n"
			}
			return "#!/usr/bin/env python3\n" + script + "\n"
		}
	}

	// Try generic ``` block
	if idx := strings.Index(content, "```"); idx >= 0 {
		rest := content[idx+3:]
		// Skip optional language identifier on same line
		if nl := strings.Index(rest, "\n"); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			script := strings.TrimSpace(rest[:end])
			if strings.HasPrefix(script, "#!/") {
				return script + "\n"
			}
			return "#!/usr/bin/env python3\n" + script + "\n"
		}
	}

	// No fences — try the whole content if it looks like a script
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "#!/") || strings.HasPrefix(trimmed, "import ") {
		return trimmed + "\n"
	}

	return ""
}

// suggestName generates a module name suggestion from the description.
// It looks for common imperative prefixes ("detect", "flag", "alert", "find", "check")
// and extracts the next two words as the module name.
// Returns "detect-custom" as a fallback.
func suggestName(description string) string {
	lower := strings.ToLower(description)
	// Extract key action words
	for _, prefix := range []string{"detect ", "flag ", "alert ", "find ", "check "} {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			rest := lower[idx+len(prefix):]
			words := strings.Fields(rest)
			if len(words) >= 2 {
				name := "detect-" + words[0] + "-" + words[1]
				// Strip any characters that aren't lowercase letters, digits, or hyphens
				name = strings.Map(func(r rune) rune {
					if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
						return r
					}
					return -1
				}, name)
				return name
			}
		}
	}
	return "detect-custom"
}

// ── list subcommand ─────────────────────────────────────────────────────────

func newCmdModuleList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List discovered script modules",
		Long:  `Shows all script modules found in .aisync/modules/ and ~/.aisync/modules/.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := f.IOStreams.Out
			dirs := diagnostic.DefaultScriptDirs()
			modules := diagnostic.DiscoverScriptModules(dirs)

			if len(modules) == 0 {
				fmt.Fprintf(out, "No script modules found.\n\n")
				fmt.Fprintf(out, "Searched:\n")
				fmt.Fprintf(out, "  project: %s\n", dirs.ProjectDir)
				fmt.Fprintf(out, "  global:  %s\n", dirs.GlobalDir)
				fmt.Fprintf(out, "\nRun 'aisync module init <name>' to create one.\n")
				fmt.Fprintf(out, "Or  'aisync module create' to generate one with AI.\n")
				return nil
			}

			fmt.Fprintf(out, "Discovered %d script module(s):\n\n", len(modules))
			fmt.Fprintf(out, "  %-30s  %-8s  %s\n", "NAME", "SOURCE", "PATH")
			for _, m := range modules {
				fmt.Fprintf(out, "  %-30s  %-8s  %s\n", m.Name(), m.Source(), m.Path())
			}
			fmt.Fprintln(out)

			return nil
		},
	}
}

// ── Shared helpers ──────────────────────────────────────────────────────────

// moduleDir returns the directory where modules should be written.
// If global is true, returns ~/.aisync/modules/; otherwise .aisync/modules/.
func moduleDir(global bool) (string, error) {
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		return filepath.Join(home, ".aisync", "modules"), nil
	}
	return filepath.Join(".aisync", "modules"), nil
}

// ── Interactive prompt helpers (same pattern as setupcmd) ────────────────────

func prompt(scanner *bufio.Scanner, question, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("  %s [%s]: ", question, defaultVal)
	} else {
		fmt.Printf("  %s: ", question)
	}
	scanner.Scan()
	answer := strings.TrimSpace(scanner.Text())
	if answer == "" {
		return defaultVal
	}
	return answer
}

func promptYesNo(scanner *bufio.Scanner, question string, defaultYes bool) bool {
	suffix := "[Y/n]"
	if !defaultYes {
		suffix = "[y/N]"
	}
	fmt.Printf("  %s %s: ", question, suffix)
	scanner.Scan()
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer == "" {
		return defaultYes
	}
	return answer == "y" || answer == "yes"
}

func promptChoice(scanner *bufio.Scanner, question string, options []string) int {
	fmt.Printf("\n  %s\n", question)
	for i, opt := range options {
		fmt.Printf("    %d) %s\n", i+1, opt)
	}
	fmt.Print("  Choice: ")
	scanner.Scan()
	answer := strings.TrimSpace(scanner.Text())
	for i := range options {
		num := fmt.Sprintf("%d", i+1)
		if answer == num {
			return i
		}
	}
	return 0 // default to first option
}

// ── Templates ───────────────────────────────────────────────────────────────

func pythonTemplate(name string) string {
	return fmt.Sprintf(`#!/usr/bin/env python3
"""
aisync diagnostic module: %s

This script receives a session JSON on stdin and outputs detected
problems as a JSON array on stdout.

Contract:
  - stdin:  full session JSON (same as 'aisync show --json' output)
  - stdout: JSON array of problems [{id, severity, title, observation, impact, ...}]
  - exit 0: success (even if no problems found — output '[]')
  - exit 1: skip (script decided this session doesn't apply)

Test manually:
  aisync show --json <session-id> | ./%s.py
"""

import json
import sys


def main():
    session = json.load(sys.stdin)
    problems = []

    # --- Your detection logic here ---
    #
    # Available session fields:
    #   session["id"]                   - session ID (e.g. "ses_abc123")
    #   session["provider"]             - "claude", "opencode", "cursor"
    #   session["agent"]                - agent name
    #   session["messages"]             - list of messages
    #     msg["role"]                   - "user" or "assistant"
    #     msg["content"]                - message text
    #     msg["tool_calls"]             - list of tool invocations
    #       tc["name"]                  - tool name (e.g. "bash", "mcp_bash")
    #       tc["input"]                 - tool input (command string, file path, etc.)
    #       tc["output"]                - tool output
    #       tc["state"]                 - "success", "error", "pending"
    #     msg["input_tokens"]           - tokens consumed
    #     msg["output_tokens"]          - tokens produced
    #
    # Example: detect repeated identical commands
    #
    # commands = []
    # for msg in session.get("messages", []):
    #     for tc in msg.get("tool_calls", []):
    #         if tc.get("name") in ("bash", "mcp_bash", "execute_command"):
    #             commands.append(tc.get("input", ""))
    #
    # from collections import Counter
    # for cmd, count in Counter(commands).items():
    #     if count >= 5:
    #         problems.append({
    #             "id": "%s-repeated-command",
    #             "severity": "medium",
    #             "category": "commands",
    #             "title": f"Command repeated {count} times",
    #             "observation": f"'{cmd[:60]}' executed {count} times",
    #             "impact": f"~{count * 500} tokens wasted on duplicate output",
    #             "metric": count,
    #             "metric_unit": "count",
    #         })

    json.dump(problems, sys.stdout)


if __name__ == "__main__":
    main()
`, name, name, name)
}

func bashTemplate(name string) string {
	return strings.ReplaceAll(fmt.Sprintf(`#!/bin/sh
# aisync diagnostic module: %s
#
# This script receives a session JSON on stdin and outputs detected
# problems as a JSON array on stdout.
#
# Contract:
#   stdin:  full session JSON
#   stdout: JSON array of problems
#   exit 0: success (output '[]' if no problems)
#   exit 1: skip
#
# Test manually:
#   aisync show --json <session-id> | ./%s.sh
#
# Dependencies: jq (https://jqlang.github.io/jq/)

# Check for jq
if ! command -v jq >/dev/null 2>&1; then
    echo "jq is required but not found" >&2
    exit 1
fi

# Read session from stdin
SESSION=$(cat)

# --- Your detection logic here ---
#
# Example: detect sessions with more than 50 messages
# MSG_COUNT=$(echo "$SESSION" | jq '.messages | length')
# if [ "$MSG_COUNT" -gt 50 ]; then
#     echo "[{
#         \"id\": \"%s-long-session\",
#         \"severity\": \"low\",
#         \"title\": \"Session has $MSG_COUNT messages\",
#         \"observation\": \"Session contains $MSG_COUNT messages which is unusually long\",
#         \"impact\": \"Long sessions accumulate context and increase costs\",
#         \"metric\": $MSG_COUNT,
#         \"metric_unit\": \"count\"
#     }]"
#     exit 0
# fi

# No problems detected
echo '[]'
`, name, name, name), "\t", "    ")
}

// ── LLM system prompt for script generation ─────────────────────────────────

const scriptGenSystemPrompt = `You are a code generator for aisync diagnostic modules.

You generate Python scripts that detect problems in AI coding sessions.

## Contract

The script you generate MUST follow this contract exactly:
- Read a JSON session object from stdin
- Output a JSON array of problems to stdout
- Exit 0 on success (even if no problems found — output '[]')
- Exit 1 to skip (session doesn't apply)

## Output format

Each problem in the array must have these fields:
{
    "id": "kebab-case-problem-id",
    "severity": "high" | "medium" | "low",
    "category": "commands" | "tokens" | "images" | "compaction" | "tool_errors" | "patterns",
    "title": "Short factual title",
    "observation": "Factual description of what was observed (counts, ratios, measurements)",
    "impact": "Quantified impact (tokens wasted, time lost, etc.)",
    "metric": 42.0,
    "metric_unit": "count" | "tokens" | "ratio" | "USD"
}

## Session JSON structure

The stdin JSON has this structure:
{
    "id": "ses_abc123",
    "provider": "claude" | "opencode" | "cursor",
    "agent": "agent name",
    "messages": [
        {
            "role": "user" | "assistant",
            "content": "message text",
            "tool_calls": [
                {
                    "name": "bash" | "mcp_bash" | "read" | "mcp_read" | "edit" | "mcp_edit" | "write" | "mcp_write" | "glob" | "mcp_glob" | "grep" | "mcp_grep" | ...,
                    "input": "tool input (command, file path, etc.)",
                    "output": "tool output",
                    "state": "success" | "error" | "pending",
                    "duration_ms": 1234
                }
            ],
            "input_tokens": 5000,
            "output_tokens": 1000,
            "timestamp": "2024-01-15T10:30:00Z"
        }
    ],
    "token_usage": {
        "input_tokens": 50000,
        "output_tokens": 10000,
        "total_tokens": 60000
    }
}

## Rules

1. Generate ONLY the Python script — no explanations, no markdown around it (but you may use a python code fence)
2. Start with #!/usr/bin/env python3
3. Include a docstring describing what the module detects
4. Use only Python standard library (json, sys, collections, re, etc.)
5. Keep the script focused on ONE concern
6. Observations must be FACTUAL — counts, ratios, measurements. No prescriptions.
7. Forbidden words in observations: "should", "consider", "recommend", "suggest", "try", "fix"
8. Use: "detected", "observed", "measured", "found", "counted"
9. Handle edge cases gracefully (empty sessions, missing fields, etc.)
10. Output '[]' when no problems are detected
`

// ── Used by tests ───────────────────────────────────────────────────────────

// NewCmdModuleForTest creates the module command for testing.
func NewCmdModuleForTest(io *iostreams.IOStreams) *cobra.Command {
	f := &cmdutil.Factory{IOStreams: io}
	return NewCmdModule(f)
}
