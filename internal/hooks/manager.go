// Package hooks manages git hook installation and removal for aisync.
// It embeds hook scripts and chains them with existing hooks to avoid conflicts.
package hooks

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/*.sh
var hookTemplates embed.FS

// startMarker and endMarker delimit the aisync-managed section in hook files.
const (
	startMarker = "# ── aisync:start ──"
	endMarker   = "# ── aisync:end ──"
)

// hookNames lists the hooks that aisync manages.
var hookNames = []string{"pre-commit", "commit-msg", "post-checkout"}

// Status represents the installation status of a single hook.
type Status struct {
	Name      string
	Installed bool
}

// Manager installs and uninstalls aisync git hooks.
type Manager struct {
	hooksDir string
}

// NewManager creates a hooks manager for the given git hooks directory.
func NewManager(hooksDir string) *Manager {
	return &Manager{hooksDir: hooksDir}
}

// HookNames returns the list of hooks managed by aisync.
func HookNames() []string {
	return hookNames
}

// Install installs all aisync hooks, chaining with existing hooks if present.
func (m *Manager) Install() error {
	for _, name := range hookNames {
		if err := m.installHook(name); err != nil {
			return fmt.Errorf("installing %s hook: %w", name, err)
		}
	}
	return nil
}

// Uninstall removes aisync sections from all managed hooks.
// If the hook file only contained aisync content, the file is removed.
func (m *Manager) Uninstall() error {
	for _, name := range hookNames {
		if err := m.uninstallHook(name); err != nil {
			return fmt.Errorf("uninstalling %s hook: %w", name, err)
		}
	}
	return nil
}

// Statuses returns the installation status of each managed hook.
func (m *Manager) Statuses() []Status {
	result := make([]Status, 0, len(hookNames))
	for _, name := range hookNames {
		result = append(result, Status{
			Name:      name,
			Installed: m.isInstalled(name),
		})
	}
	return result
}

// IsInstalled returns true if a specific hook has an aisync section.
func (m *Manager) IsInstalled(name string) bool {
	return m.isInstalled(name)
}

func (m *Manager) isInstalled(name string) bool {
	hookPath := filepath.Join(m.hooksDir, name)
	data, err := os.ReadFile(hookPath)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), startMarker)
}

func (m *Manager) installHook(name string) error {
	hookPath := filepath.Join(m.hooksDir, name)

	// Read the aisync section from the embedded template
	aisyncSection, err := m.readAisyncSection(name)
	if err != nil {
		return err
	}

	// Ensure hooks directory exists
	if err := os.MkdirAll(m.hooksDir, 0o755); err != nil {
		return fmt.Errorf("creating hooks directory: %w", err)
	}

	// Check for existing hook file
	existing, readErr := os.ReadFile(hookPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("reading existing hook: %w", readErr)
	}

	var content string
	if len(existing) > 0 {
		existingStr := string(existing)
		if strings.Contains(existingStr, startMarker) {
			// Replace existing aisync section
			content = replaceAisyncSection(existingStr, aisyncSection)
		} else {
			// Chain: append aisync section to existing hook
			content = existingStr + "\n" + aisyncSection + "\n"
		}
	} else {
		// New hook file
		content = "#!/usr/bin/env bash\n" + aisyncSection + "\n"
	}

	if err := os.WriteFile(hookPath, []byte(content), 0o755); err != nil {
		return fmt.Errorf("writing hook file: %w", err)
	}

	return nil
}

func (m *Manager) uninstallHook(name string) error {
	hookPath := filepath.Join(m.hooksDir, name)

	existing, err := os.ReadFile(hookPath)
	if os.IsNotExist(err) {
		return nil // nothing to uninstall
	}
	if err != nil {
		return fmt.Errorf("reading hook: %w", err)
	}

	existingStr := string(existing)
	if !strings.Contains(existingStr, startMarker) {
		return nil // no aisync section
	}

	cleaned := removeAisyncSection(existingStr)

	// If only the shebang line (or empty) remains, remove the file
	trimmed := strings.TrimSpace(cleaned)
	if trimmed == "" || trimmed == "#!/usr/bin/env bash" || trimmed == "#!/bin/bash" || trimmed == "#!/bin/sh" {
		return os.Remove(hookPath)
	}

	return os.WriteFile(hookPath, []byte(cleaned), 0o755)
}

// readAisyncSection reads the aisync-managed section from the embedded template.
func (m *Manager) readAisyncSection(hookName string) (string, error) {
	templateFile := fmt.Sprintf("templates/%s.sh", hookName)
	data, err := hookTemplates.ReadFile(templateFile)
	if err != nil {
		return "", fmt.Errorf("reading template %s: %w", templateFile, err)
	}

	content := string(data)
	startIdx := strings.Index(content, startMarker)
	endIdx := strings.Index(content, endMarker)
	if startIdx == -1 || endIdx == -1 {
		return "", fmt.Errorf("template %s missing aisync markers", templateFile)
	}

	return content[startIdx : endIdx+len(endMarker)], nil
}

// replaceAisyncSection replaces the existing aisync section in a hook file.
func replaceAisyncSection(content, newSection string) string {
	startIdx := strings.Index(content, startMarker)
	endIdx := strings.Index(content, endMarker)
	if startIdx == -1 || endIdx == -1 {
		return content
	}
	return content[:startIdx] + newSection + content[endIdx+len(endMarker):]
}

// removeAisyncSection removes the aisync section from a hook file.
func removeAisyncSection(content string) string {
	startIdx := strings.Index(content, startMarker)
	endIdx := strings.Index(content, endMarker)
	if startIdx == -1 || endIdx == -1 {
		return content
	}

	before := content[:startIdx]
	after := content[endIdx+len(endMarker):]

	// Clean up extra blank lines left behind
	result := before + after
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return result
}
