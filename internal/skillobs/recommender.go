package skillobs

import (
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// SkillEntry is a skill with its metadata for recommendation matching.
type SkillEntry struct {
	Name        string   // skill identifier (e.g. "django-migration-writer")
	Description string   // from SKILL.md frontmatter
	Keywords    []string // extracted from name + description + custom keywords
}

// BuildSkillEntries creates SkillEntry list from registry capabilities.
// Only capabilities with Kind == "skill" are included.
func BuildSkillEntries(caps []registry.Capability) []SkillEntry {
	var entries []SkillEntry
	for _, cap := range caps {
		if cap.Kind != registry.KindSkill {
			continue
		}
		entry := SkillEntry{
			Name:        cap.Name,
			Description: cap.Description,
		}
		entry.Keywords = extractKeywords(cap.Name, cap.Description)
		entries = append(entries, entry)
	}
	return entries
}

// RecommendSkills scans user messages and returns skill names whose keywords
// match the message content. Uses case-insensitive substring matching.
func RecommendSkills(messages []session.Message, skills []SkillEntry) []string {
	if len(skills) == 0 {
		return nil
	}

	// Build a single corpus from all user messages (case-lowered).
	var corpus strings.Builder
	for i := range messages {
		if messages[i].Role == session.RoleUser {
			corpus.WriteString(strings.ToLower(messages[i].Content))
			corpus.WriteByte(' ')
		}
	}
	text := corpus.String()
	if text == "" {
		return nil
	}

	seen := make(map[string]bool)
	var recommended []string

	for _, skill := range skills {
		if matchesAnyKeyword(text, skill.Keywords) && !seen[skill.Name] {
			seen[skill.Name] = true
			recommended = append(recommended, skill.Name)
		}
	}

	return recommended
}

// matchesAnyKeyword returns true if the text contains any of the keywords.
func matchesAnyKeyword(text string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

// extractKeywords generates a keyword list from a skill name and description.
// The name is split on hyphens/underscores. The description is split on whitespace
// and filtered to keep meaningful words (>= 4 chars, lowercased).
func extractKeywords(name, description string) []string {
	seen := make(map[string]bool)
	var keywords []string

	add := func(kw string) {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" && len(kw) >= 3 && !seen[kw] {
			seen[kw] = true
			keywords = append(keywords, kw)
		}
	}

	// Full name as keyword.
	add(name)

	// Split name on separators.
	for _, part := range splitIdentifier(name) {
		add(part)
	}

	// Extract words from description.
	for _, word := range strings.Fields(description) {
		word = strings.Trim(word, ".,;:!?()[]{}\"'")
		if len(word) >= 4 { // skip short words (a, the, for, etc.)
			add(word)
		}
	}

	return keywords
}

// splitIdentifier splits a name like "django-migration-writer" or "code_review"
// into parts: ["django", "migration", "writer"].
func splitIdentifier(name string) []string {
	// Replace common separators with space, then split.
	r := strings.NewReplacer("-", " ", "_", " ", ".", " ")
	return strings.Fields(r.Replace(name))
}
