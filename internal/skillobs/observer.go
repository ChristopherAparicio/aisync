package skillobs

import (
	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Observe produces a SkillObservation by combining skill recommendation
// (keyword matching on user messages) with skill load detection
// (scanning tool calls and message text).
//
// Returns nil if no skills are available for the project.
func Observe(messages []session.Message, capabilities []registry.Capability) *analysis.SkillObservation {
	// Build skill entries from registry.
	skills := BuildSkillEntries(capabilities)
	if len(skills) == 0 {
		return nil
	}

	// Available skill names.
	available := make([]string, len(skills))
	for i, s := range skills {
		available[i] = s.Name
	}

	// Recommend skills based on user messages.
	recommended := RecommendSkills(messages, skills)

	// Detect loaded skills from tool calls and text patterns.
	loaded := DetectLoadedSkills(messages)

	// Compute missed and discovered.
	recommendedSet := toSet(recommended)
	loadedSet := toSet(loaded)

	var missed []string
	for _, name := range recommended {
		if !loadedSet[name] {
			missed = append(missed, name)
		}
	}

	var discovered []string
	for _, name := range loaded {
		if !recommendedSet[name] {
			discovered = append(discovered, name)
		}
	}

	return &analysis.SkillObservation{
		Available:   available,
		Recommended: recommended,
		Loaded:      loaded,
		Missed:      missed,
		Discovered:  discovered,
	}
}

// toSet converts a string slice to a set (map[string]bool).
func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}
