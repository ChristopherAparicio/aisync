package skillresolver

import (
	"fmt"
	"os"
	"strings"
)

// ── SKILL.md File Operations ──

// SkillFile represents a parsed SKILL.md file.
type SkillFile struct {
	// Path is the absolute path to the SKILL.md file.
	Path string

	// Name is from the YAML frontmatter.
	Name string

	// Description is from the YAML frontmatter.
	Description string

	// Body is everything after the frontmatter (the markdown content).
	Body string

	// Raw is the complete original file content.
	Raw string
}

// ReadSkillFile reads and parses a SKILL.md file.
// The file format is:
//
//	---
//	name: skill-name
//	description: Short description
//	---
//	# Markdown content...
func ReadSkillFile(path string) (*SkillFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	raw := string(data)
	sf := &SkillFile{
		Path: path,
		Raw:  raw,
	}

	// Parse YAML frontmatter.
	name, desc, body, parseErr := parseFrontmatter(raw)
	if parseErr != nil {
		return nil, fmt.Errorf("parsing frontmatter in %s: %w", path, parseErr)
	}
	sf.Name = name
	sf.Description = desc
	sf.Body = body

	return sf, nil
}

// WriteSkillFile writes a SkillFile back to disk.
// It reconstructs the file from the structured fields.
func WriteSkillFile(sf *SkillFile) error {
	content := buildSkillFileContent(sf.Name, sf.Description, sf.Body)
	if err := os.WriteFile(sf.Path, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", sf.Path, err)
	}
	return nil
}

// ApplyImprovement modifies a SkillFile in-place according to an improvement.
// Returns true if any changes were made.
func ApplyImprovement(sf *SkillFile, imp SkillImprovement) bool {
	changed := false

	switch imp.Kind {
	case KindDescription:
		if imp.ProposedDescription != "" && imp.ProposedDescription != sf.Description {
			sf.Description = imp.ProposedDescription
			changed = true
		}

	case KindKeywords:
		// Add keywords to the description if they're not already present.
		// Keywords are appended as a parenthetical list at the end.
		if len(imp.AddKeywords) > 0 {
			newKeywords := filterNewKeywords(sf.Description, sf.Body, imp.AddKeywords)
			if len(newKeywords) > 0 {
				sf.Description = appendKeywordsToDescription(sf.Description, newKeywords)
				changed = true
			}
		}

	case KindTriggerPattern:
		// Add trigger patterns as a section in the body.
		if len(imp.AddTriggerPatterns) > 0 {
			sf.Body = appendTriggerPatterns(sf.Body, imp.AddTriggerPatterns)
			changed = true
		}

	case KindContent:
		if imp.ProposedContent != "" && imp.ProposedContent != sf.Body {
			sf.Body = imp.ProposedContent
			changed = true
		}
	}

	return changed
}

// ── Internal helpers ──

// parseFrontmatter extracts name, description, and body from a SKILL.md file.
func parseFrontmatter(content string) (name, description, body string, err error) {
	content = strings.TrimSpace(content)

	if !strings.HasPrefix(content, "---") {
		return "", "", content, fmt.Errorf("missing frontmatter delimiter")
	}

	// Find the closing --- delimiter.
	rest := content[3:] // skip opening ---
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return "", "", content, fmt.Errorf("unclosed frontmatter")
	}

	frontmatter := rest[:endIdx]
	body = strings.TrimSpace(rest[endIdx+4:]) // skip \n---

	// Parse simple YAML key: value pairs.
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if key, val, ok := parseYAMLLine(line); ok {
			switch key {
			case "name":
				name = val
			case "description":
				description = val
			}
		}
	}

	return name, description, body, nil
}

// parseYAMLLine parses a simple "key: value" line.
func parseYAMLLine(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	// Remove optional surrounding quotes.
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	return key, value, true
}

// buildSkillFileContent reconstructs a SKILL.md file from its parts.
func buildSkillFileContent(name, description, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %s\n", name))
	b.WriteString(fmt.Sprintf("description: %s\n", description))
	b.WriteString("---\n")
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// filterNewKeywords returns keywords not already present in the description or body.
func filterNewKeywords(description, body string, keywords []string) []string {
	lower := strings.ToLower(description + " " + body)
	var newKW []string
	for _, kw := range keywords {
		if !strings.Contains(lower, strings.ToLower(kw)) {
			newKW = append(newKW, kw)
		}
	}
	return newKW
}

// appendKeywordsToDescription appends keywords to a description.
// If the description already ends with a keywords section, it extends it.
func appendKeywordsToDescription(description string, keywords []string) string {
	// Check if description already has a keywords section (e.g. "... (keywords: a, b)")
	if idx := strings.LastIndex(description, "(keywords:"); idx >= 0 {
		// Extract existing keywords and merge.
		existing := description[idx:]
		prefix := strings.TrimSpace(description[:idx])
		existing = strings.TrimPrefix(existing, "(keywords:")
		existing = strings.TrimSuffix(existing, ")")
		existing = strings.TrimSpace(existing)

		allKW := strings.Split(existing, ",")
		for _, kw := range keywords {
			allKW = append(allKW, strings.TrimSpace(kw))
		}

		trimmed := make([]string, 0, len(allKW))
		for _, kw := range allKW {
			kw = strings.TrimSpace(kw)
			if kw != "" {
				trimmed = append(trimmed, kw)
			}
		}

		return fmt.Sprintf("%s (keywords: %s)", prefix, strings.Join(trimmed, ", "))
	}

	// Append new keywords section.
	return fmt.Sprintf("%s (keywords: %s)", description, strings.Join(keywords, ", "))
}

// appendTriggerPatterns adds a trigger patterns section to the body.
func appendTriggerPatterns(body string, patterns []string) string {
	var b strings.Builder
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n## Trigger Patterns\n\n")
	for _, p := range patterns {
		b.WriteString(fmt.Sprintf("- %s\n", p))
	}
	return b.String()
}
