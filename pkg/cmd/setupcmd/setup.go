// Package setupcmd implements `aisync setup` — an interactive wizard
// that detects installed providers, installs plugins/hooks, configures
// MCP integration, and optionally sets up a remote server connection.
//
// Design goals:
//   - Zero friction: one command does everything
//   - Two modes: "agent" (capture & forward) and "server" (receive & serve)
//   - Idempotent: safe to re-run, won't break existing config
//   - Non-interactive fallback: --yes flag for CI/scripts
package setupcmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the setup command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	Yes       bool   // skip prompts, accept all defaults
	Mode      string // "agent" or "server" — empty = ask
	ServerURL string // remote server URL (agent mode)
	APIKey    string // API key (agent mode)
}

// NewCmdSetup creates the `aisync setup` command.
func NewCmdSetup(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup wizard for aisync",
		Long: `Set up aisync in one command.

Detects installed AI coding assistants (Claude Code, OpenCode, Cursor),
installs capture plugins/hooks, configures MCP integration, and optionally
connects to a remote aisync server.

Two modes:
  agent   — Capture sessions locally and forward them to a server
  server  — Run the aisync dashboard and API server

Examples:
  aisync setup                      # interactive wizard
  aisync setup --mode agent         # skip mode selection
  aisync setup --yes                # accept all defaults (non-interactive)
  aisync setup --mode server        # set up server mode`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Accept all defaults without prompting")
	cmd.Flags().StringVar(&opts.Mode, "mode", "", "Setup mode: agent or server")
	cmd.Flags().StringVar(&opts.ServerURL, "server-url", "", "Remote server URL (agent mode)")
	cmd.Flags().StringVar(&opts.APIKey, "api-key", "", "API key for remote server (agent mode)")

	return cmd
}

// ── Provider Detection ──

type detectedProvider struct {
	name      string // display name
	slug      string // claude-code, opencode, cursor
	installed bool
	path      string // where we detected it
}

func detectProviders() []detectedProvider {
	home, _ := os.UserHomeDir()

	providers := []detectedProvider{
		{
			name: "Claude Code",
			slug: "claude-code",
			path: filepath.Join(home, ".claude"),
		},
		{
			name: "OpenCode",
			slug: "opencode",
			path: filepath.Join(home, ".config", "opencode"),
		},
	}

	// Cursor path depends on OS
	cursorPath := filepath.Join(home, ".config", "Cursor")
	if runtime.GOOS == "darwin" {
		cursorPath = filepath.Join(home, "Library", "Application Support", "Cursor")
	}
	providers = append(providers, detectedProvider{
		name: "Cursor",
		slug: "cursor",
		path: cursorPath,
	})

	// Check which ones exist
	for i := range providers {
		if _, err := os.Stat(providers[i].path); err == nil {
			providers[i].installed = true
		}
	}

	return providers
}

// ── MCP Config Detection ──

type mcpConfig struct {
	path    string // path to the MCP config file
	exists  bool
	hasConf bool // already has aisync configured
}

func detectMCPConfig(providerSlug string) mcpConfig {
	home, _ := os.UserHomeDir()

	var configPath string
	switch providerSlug {
	case "claude-code":
		configPath = filepath.Join(home, ".claude", "settings.json")
	case "opencode":
		configPath = filepath.Join(home, ".config", "opencode", "config.json")
	default:
		return mcpConfig{}
	}

	mc := mcpConfig{path: configPath}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return mc
	}
	mc.exists = true
	mc.hasConf = strings.Contains(string(data), `"aisync"`)
	return mc
}

// ── Interactive Helpers ──

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
	for i, opt := range options {
		num := fmt.Sprintf("%d", i+1)
		if answer == num || strings.EqualFold(answer, opt) {
			return i
		}
	}
	return 0 // default to first option
}

// ── Main Setup Logic ──

func runSetup(opts *Options) error {
	out := opts.IO.Out
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Fprintln(out)
	fmt.Fprintln(out, "  aisync setup")
	fmt.Fprintln(out, "  ~~~~~~~~~~~~")
	fmt.Fprintln(out)

	// ── Step 1: Choose mode ──
	mode := opts.Mode
	if mode == "" {
		if opts.Yes {
			mode = "agent"
		} else {
			choice := promptChoice(scanner, "How do you want to use aisync?", []string{
				"Agent  — Capture AI sessions and forward to a server",
				"Server — Run the aisync dashboard and API",
			})
			if choice == 0 {
				mode = "agent"
			} else {
				mode = "server"
			}
		}
	}

	fmt.Fprintln(out)

	switch mode {
	case "agent":
		return setupAgent(opts, scanner)
	case "server":
		return setupServer(opts, scanner)
	default:
		return fmt.Errorf("unknown mode: %q (expected 'agent' or 'server')", mode)
	}
}

// ── Agent Setup ──

func setupAgent(opts *Options, scanner *bufio.Scanner) error {
	out := opts.IO.Out

	fmt.Fprintln(out, "  Mode: Agent (capture & forward)")
	fmt.Fprintln(out)

	// ── Detect providers ──
	fmt.Fprintln(out, "  Detecting AI coding assistants...")
	providers := detectProviders()
	anyInstalled := false
	for _, p := range providers {
		icon := "  "
		if p.installed {
			icon = "  "
			anyInstalled = true
		}
		fmt.Fprintf(out, "  %s %s", icon, p.name)
		if p.installed {
			fmt.Fprintf(out, "  (%s)", p.path)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out)

	if !anyInstalled {
		fmt.Fprintln(out, "  No AI assistants detected. You can still use aisync manually:")
		fmt.Fprintln(out, "    aisync capture --provider claude-code")
		fmt.Fprintln(out, "    aisync capture --provider opencode")
		fmt.Fprintln(out)
	}

	// ── Install plugins for each detected provider ──
	for _, p := range providers {
		if !p.installed {
			continue
		}

		install := opts.Yes || promptYesNo(scanner, fmt.Sprintf("Install capture plugin for %s?", p.name), true)
		if !install {
			continue
		}

		switch p.slug {
		case "claude-code":
			if err := installClaudeCodeHook(out, p.path); err != nil {
				fmt.Fprintf(out, "    Failed: %v\n", err)
			}
		case "opencode":
			if err := installOpenCodePlugin(out, p.path); err != nil {
				fmt.Fprintf(out, "    Failed: %v\n", err)
			}
		case "cursor":
			fmt.Fprintln(out, "    Cursor: no plugin needed — use periodic capture instead:")
			fmt.Fprintln(out, "      aisync config set scheduler.capture_all.enabled true")
			fmt.Fprintln(out, "      aisync config set scheduler.capture_all.provider cursor")
		}
		fmt.Fprintln(out)
	}

	// ── Configure MCP for each provider ──
	for _, p := range providers {
		if !p.installed {
			continue
		}
		if p.slug != "claude-code" && p.slug != "opencode" {
			continue
		}

		mc := detectMCPConfig(p.slug)
		if mc.hasConf {
			fmt.Fprintf(out, "  MCP for %s: already configured\n", p.name)
			continue
		}

		install := opts.Yes || promptYesNo(scanner, fmt.Sprintf("Configure MCP server for %s? (gives the AI access to aisync tools)", p.name), true)
		if !install {
			continue
		}

		if err := installMCPConfig(out, p.slug, mc.path); err != nil {
			fmt.Fprintf(out, "    Failed: %v\n", err)
		}
	}
	fmt.Fprintln(out)

	// ── Optional: remote server ──
	serverURL := opts.ServerURL
	apiKey := opts.APIKey

	if serverURL == "" && !opts.Yes {
		if promptYesNo(scanner, "Connect to a remote aisync server?", false) {
			serverURL = prompt(scanner, "Server URL", "https://localhost:8371")
			apiKey = prompt(scanner, "API key (sk-...)", "")
		}
	}

	if serverURL != "" {
		cfg, err := opts.Factory.Config()
		if err == nil {
			if setErr := cfg.Set("server.url", serverURL); setErr != nil {
				fmt.Fprintf(out, "    Warning: could not set server.url: %v\n", setErr)
			}
			if apiKey != "" {
				if setErr := cfg.Set("server.api_key", apiKey); setErr != nil {
					fmt.Fprintf(out, "    Warning: could not set server.api_key: %v\n", setErr)
				}
			}
			if saveErr := cfg.Save(); saveErr != nil {
				fmt.Fprintf(out, "    Warning: could not save config: %v\n", saveErr)
			} else {
				fmt.Fprintf(out, "  Server: %s\n", serverURL)
			}
		}
	}

	// ── Summary ──
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Setup complete!")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Quick commands:")
	fmt.Fprintln(out, "    aisync capture          # capture the current session")
	fmt.Fprintln(out, "    aisync list              # list captured sessions")
	fmt.Fprintln(out, "    aisync web               # open the dashboard")
	fmt.Fprintln(out, "    aisync stats             # view statistics")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  With plugins installed, sessions are captured automatically")
	fmt.Fprintln(out, "  when the AI finishes working. No manual action needed.")
	fmt.Fprintln(out)

	return nil
}

// ── Server Setup ──

func setupServer(opts *Options, scanner *bufio.Scanner) error {
	out := opts.IO.Out

	fmt.Fprintln(out, "  Mode: Server (dashboard & API)")
	fmt.Fprintln(out)

	// Enable auth
	enableAuth := opts.Yes || promptYesNo(scanner, "Enable authentication? (recommended for remote access)", true)

	cfg, err := opts.Factory.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if enableAuth {
		if setErr := cfg.Set("server.auth.enabled", "true"); setErr != nil {
			fmt.Fprintf(out, "    Warning: could not set auth.enabled: %v\n", setErr)
		}
		// Generate JWT secret
		jwtSecret := cfg.GetJWTSecret()
		if jwtSecret == "" {
			// Use openssl or crypto/rand
			if secret, genErr := generateSecret(); genErr == nil {
				if setErr := cfg.Set("server.auth.jwt_secret", secret); setErr != nil {
					fmt.Fprintf(out, "    Warning: could not set jwt_secret: %v\n", setErr)
				}
			}
		}
		fmt.Fprintln(out, "  Authentication: enabled")
		fmt.Fprintln(out, "  (First user to register at /api/v1/auth/register becomes admin)")
	}

	addr := "0.0.0.0:8371"
	if !opts.Yes {
		addr = prompt(scanner, "Listen address", addr)
	}

	if setErr := cfg.Set("server.addr", addr); setErr != nil {
		fmt.Fprintf(out, "    Warning: could not set server.addr: %v\n", setErr)
	}
	if saveErr := cfg.Save(); saveErr != nil {
		fmt.Fprintf(out, "    Warning: could not save config: %v\n", saveErr)
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Setup complete!")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Start the server:")
	fmt.Fprintf(out, "    aisync serve --addr %s\n", addr)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Dashboard:  http://"+addr)
	fmt.Fprintln(out, "  API:        http://"+addr+"/api/v1/health")
	fmt.Fprintln(out)

	if enableAuth {
		fmt.Fprintln(out, "  Register the first admin user:")
		fmt.Fprintf(out, "    curl -X POST http://%s/api/v1/auth/register \\\n", addr)
		fmt.Fprintln(out, `      -H 'Content-Type: application/json' \`)
		fmt.Fprintln(out, `      -d '{"username":"admin","password":"your-password"}'`)
		fmt.Fprintln(out)
	}

	// Ask if they want to start now
	if !opts.Yes {
		if promptYesNo(scanner, "Start the server now?", false) {
			fmt.Fprintf(out, "\n  Starting aisync serve --addr %s ...\n\n", addr)
			cmd := exec.Command("aisync", "serve", "--addr", addr)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			return cmd.Run()
		}
	}

	return nil
}

// ── Plugin Installers ──

func installClaudeCodeHook(out io.Writer, claudeDir string) error {
	settingsPath := filepath.Join(claudeDir, "settings.json")

	// Read existing settings
	var settings map[string]any
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading settings: %w", err)
		}
		settings = make(map[string]any)
	} else {
		if jsonErr := json.Unmarshal(data, &settings); jsonErr != nil {
			return fmt.Errorf("parsing settings.json: %w", jsonErr)
		}
	}

	// Check if hook already exists
	if hooks, ok := settings["hooks"]; ok {
		if hooksMap, ok := hooks.(map[string]any); ok {
			if stopHooks, ok := hooksMap["Stop"]; ok {
				hookData, _ := json.Marshal(stopHooks)
				if strings.Contains(string(hookData), "aisync") {
					fmt.Fprintln(out, "    Claude Code: hook already installed")
					return nil
				}
			}
		}
	}

	// Find aisync binary path
	aisyncBin := findAisyncBinary()

	// Build the hook entry
	hookEntry := map[string]any{
		"matcher": "",
		"hooks": []map[string]any{
			{
				"type":    "command",
				"command": aisyncBin + " capture --provider claude-code --auto",
			},
		},
	}

	// Merge into settings
	hooksMap, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooksMap = make(map[string]any)
	}

	// Get existing Stop hooks or create new array
	var stopHooks []any
	if existing, ok := hooksMap["Stop"].([]any); ok {
		stopHooks = existing
	}
	stopHooks = append(stopHooks, hookEntry)
	hooksMap["Stop"] = stopHooks
	settings["hooks"] = hooksMap

	// Write back
	newData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, newData, 0o644); err != nil {
		return fmt.Errorf("writing settings: %w", err)
	}

	fmt.Fprintln(out, "    Claude Code: Stop hook installed")
	fmt.Fprintf(out, "    Config: %s\n", settingsPath)
	fmt.Fprintln(out, "    Restart Claude Code to activate.")
	return nil
}

func installOpenCodePlugin(out io.Writer, opencodePath string) error {
	pluginsDir := filepath.Join(opencodePath, "plugins")
	targetDir := filepath.Join(pluginsDir, "opencode-aisync")

	// Check if already installed
	if _, err := os.Stat(filepath.Join(targetDir, "aisync.ts")); err == nil {
		fmt.Fprintln(out, "    OpenCode: plugin already installed")
		return nil
	}

	// Find the plugins source directory
	srcDir := findPluginsDir("opencode")
	if srcDir == "" {
		// Fallback: create a minimal plugin inline
		return installOpenCodePluginInline(out, targetDir)
	}

	// Create symlink
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return fmt.Errorf("creating plugins dir: %w", err)
	}

	// Remove existing target if it exists (might be a broken symlink)
	os.Remove(targetDir)

	if err := os.Symlink(srcDir, targetDir); err != nil {
		// If symlink fails, try copy
		return copyDir(srcDir, targetDir)
	}

	fmt.Fprintln(out, "    OpenCode: plugin linked")
	fmt.Fprintf(out, "    %s -> %s\n", targetDir, srcDir)
	fmt.Fprintln(out, "    Restart OpenCode to activate.")
	return nil
}

func installOpenCodePluginInline(out io.Writer, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("creating plugin dir: %w", err)
	}

	// Write a minimal plugin that calls aisync capture
	pluginCode := `// opencode-aisync — Auto-capture sessions into aisync
// Installed by: aisync setup
import type { Plugin } from "@opencode-ai/plugin"

const AisyncPlugin: Plugin = async (ctx) => {
  const { $, worktree } = ctx
  const captured = new Set()
  const log = (msg) => { if (process.env.AISYNC_PLUGIN_DEBUG) console.log("[aisync] " + msg) }

  const capture = async (sessionId, reason) => {
    if (!sessionId || captured.has(sessionId)) return
    try {
      const branch = (await $` + "`" + `git -C ${worktree} rev-parse --abbrev-ref HEAD` + "`" + `.text()).trim()
      const args = ["capture", "--provider", "opencode", "--session-id", sessionId, "--mode", process.env.AISYNC_CAPTURE_MODE || "compact", "--auto"]
      if (branch) args.push("--branch", branch)
      await $` + "`" + `aisync ${args}` + "`" + `
      captured.add(sessionId)
      log("captured " + sessionId + " (" + reason + ")")
    } catch (e) { log("capture failed: " + e?.message) }
  }

  return {
    event: async ({ event }) => {
      if (event.type === "session.idle") await capture(event.properties?.sessionID, "idle")
      if (event.type === "session.error") await capture(event.properties?.sessionID, "error")
    }
  }
}
export default AisyncPlugin
`
	pkgJSON := `{
  "name": "opencode-aisync",
  "version": "1.0.0",
  "description": "Auto-capture AI sessions into aisync",
  "main": "aisync.ts"
}
`
	if err := os.WriteFile(filepath.Join(targetDir, "aisync.ts"), []byte(pluginCode), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(targetDir, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		return err
	}

	fmt.Fprintln(out, "    OpenCode: plugin installed (inline)")
	fmt.Fprintf(out, "    Dir: %s\n", targetDir)
	fmt.Fprintln(out, "    Restart OpenCode to activate.")
	return nil
}

// ── MCP Config Installer ──

func installMCPConfig(out io.Writer, providerSlug, configPath string) error {
	aisyncBin := findAisyncBinary()

	switch providerSlug {
	case "claude-code":
		return installClaudeCodeMCP(out, configPath, aisyncBin)
	case "opencode":
		return installOpenCodeMCP(out, configPath, aisyncBin)
	}
	return nil
}

func installClaudeCodeMCP(out io.Writer, configPath, aisyncBin string) error {
	var settings map[string]any
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(data) > 0 {
		if jsonErr := json.Unmarshal(data, &settings); jsonErr != nil {
			return fmt.Errorf("parsing %s: %w", configPath, jsonErr)
		}
	}
	if settings == nil {
		settings = make(map[string]any)
	}

	// Get or create mcpServers
	mcpServers, ok := settings["mcpServers"].(map[string]any)
	if !ok {
		mcpServers = make(map[string]any)
	}

	if _, exists := mcpServers["aisync"]; exists {
		fmt.Fprintln(out, "    Claude Code MCP: already configured")
		return nil
	}

	mcpServers["aisync"] = map[string]any{
		"command": aisyncBin,
		"args":    []string{"mcp"},
	}
	settings["mcpServers"] = mcpServers

	newData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(configPath, newData, 0o644); err != nil {
		return err
	}

	fmt.Fprintln(out, "    Claude Code MCP: configured")
	fmt.Fprintf(out, "    Config: %s\n", configPath)
	return nil
}

func installOpenCodeMCP(out io.Writer, configPath, aisyncBin string) error {
	var config map[string]any
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(data) > 0 {
		if jsonErr := json.Unmarshal(data, &config); jsonErr != nil {
			return fmt.Errorf("parsing %s: %w", configPath, jsonErr)
		}
	}
	if config == nil {
		config = make(map[string]any)
	}

	// Get or create mcp.servers
	mcpSection, ok := config["mcp"].(map[string]any)
	if !ok {
		mcpSection = make(map[string]any)
	}
	servers, ok := mcpSection["servers"].(map[string]any)
	if !ok {
		servers = make(map[string]any)
	}

	if _, exists := servers["aisync"]; exists {
		fmt.Fprintln(out, "    OpenCode MCP: already configured")
		return nil
	}

	servers["aisync"] = map[string]any{
		"command": aisyncBin,
		"args":    []string{"mcp"},
	}
	mcpSection["servers"] = servers
	config["mcp"] = mcpSection

	newData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	// Ensure directory exists
	if dir := filepath.Dir(configPath); dir != "" {
		os.MkdirAll(dir, 0o755)
	}

	if err := os.WriteFile(configPath, newData, 0o644); err != nil {
		return err
	}

	fmt.Fprintln(out, "    OpenCode MCP: configured")
	fmt.Fprintf(out, "    Config: %s\n", configPath)
	return nil
}

// ── Helpers ──

func findAisyncBinary() string {
	if p, err := exec.LookPath("aisync"); err == nil {
		return p
	}
	// Fallback paths
	home, _ := os.UserHomeDir()
	for _, candidate := range []string{
		filepath.Join(home, "go", "bin", "aisync"),
		filepath.Join(home, ".local", "bin", "aisync"),
		"/usr/local/bin/aisync",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "aisync" // hope it's in PATH
}

func findPluginsDir(provider string) string {
	// Try to find the aisync source tree's plugins directory
	// by looking relative to the aisync binary
	aisyncBin := findAisyncBinary()
	if aisyncBin == "aisync" {
		return ""
	}

	// Check relative to binary: ../plugins/<provider> or ../share/aisync/plugins/<provider>
	binDir := filepath.Dir(aisyncBin)
	candidates := []string{
		filepath.Join(binDir, "..", "plugins", provider),
		filepath.Join(binDir, "..", "share", "aisync", "plugins", provider),
		filepath.Join(binDir, "plugins", provider),
	}

	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "aisync.ts")); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}

	return ""
}

func generateSecret() (string, error) {
	cmd := exec.Command("openssl", "rand", "-hex", "32")
	out, err := cmd.Output()
	if err != nil {
		// Fallback to /dev/urandom
		f, fErr := os.Open("/dev/urandom")
		if fErr != nil {
			return "aisync-dev-secret-change-me-in-production", nil
		}
		defer f.Close()
		b := make([]byte, 32)
		if _, rErr := f.Read(b); rErr != nil {
			return "aisync-dev-secret-change-me-in-production", nil
		}
		return fmt.Sprintf("%x", b), nil
	}
	return strings.TrimSpace(string(out)), nil
}

func copyDir(src, dst string) error {
	cmd := exec.Command("cp", "-r", src, dst)
	return cmd.Run()
}
