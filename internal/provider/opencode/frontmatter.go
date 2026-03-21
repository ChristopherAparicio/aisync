package opencode

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"
)

// frontmatter represents the YAML front matter found in OpenCode command
// and agent Markdown files. Example:
//
//	---
//	description: Create or update the feature specification.
//	handoffs:
//	  - label: Build Technical Plan
//	    agent: speckit.plan
//	    prompt: Create a plan for the spec.
//	    send: true
//	tools:
//	  - github/github-mcp-server:issue_write
//	---
type frontmatter struct {
	Description string          `yaml:"description"`
	Handoffs    []frontmatterHO `yaml:"handoffs"`
	Tools       []string        `yaml:"tools"`
	AllowedTool []string        `yaml:"allowed_tool"` // alternative key used by some agents
	Agent       string          `yaml:"agent"`        // for skills that declare their agent
	Custom      map[string]any  `yaml:",inline"`      // catch-all for unknown fields
}

// frontmatterHO represents a single handoff entry in the YAML front matter.
type frontmatterHO struct {
	Label  string `yaml:"label"`
	Agent  string `yaml:"agent"`
	Prompt string `yaml:"prompt"`
	Send   bool   `yaml:"send"`
}

// parseFrontmatter extracts YAML front matter from Markdown content.
// Returns the parsed frontmatter and the remaining body content.
// If no front matter is found (no leading ---), returns a zero frontmatter
// and the full content as body.
func parseFrontmatter(content []byte) (frontmatter, string) {
	var fm frontmatter

	trimmed := bytes.TrimSpace(content)
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return fm, string(content)
	}

	// Find the closing ---
	rest := trimmed[3:]
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return fm, string(content)
	}

	fmBytes := rest[:idx]
	body := rest[idx+4:] // skip "\n---"

	_ = yaml.Unmarshal(fmBytes, &fm)

	return fm, strings.TrimSpace(string(body))
}

// parseToolRef parses a tool reference string like "github/github-mcp-server:issue_write"
// into server and tool parts. If there's no colon, the whole string is treated as the tool name.
func parseToolRef(ref string) (server, tool string) {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", parts[0]
}
