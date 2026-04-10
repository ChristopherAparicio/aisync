// Package diagnostic — provider documentation link registry.
//
// Each generated fix can reference provider-specific documentation so the user
// knows how to install or use the artefact. Links are provider-version-aware
// and include an aisync self-reference for the inspect command itself.
package diagnostic

import "github.com/ChristopherAparicio/aisync/internal/session"

// DocLink is a single documentation reference attached to a fix.
type DocLink struct {
	Label string `json:"label"` // e.g. "OpenCode Skills documentation"
	URL   string `json:"url"`   // full URL
}

// ProviderDocLinks returns the relevant documentation links for a given provider.
// These are appended to each ProblemFix as a "Learn more" section.
func ProviderDocLinks(provider session.ProviderName) []DocLink {
	links := providerDocs[provider]
	// Always include the aisync self-reference.
	links = append(links, DocLink{
		Label: "aisync inspect documentation",
		URL:   "https://github.com/ChristopherAparicio/aisync#inspect",
	})
	return links
}

// ArtefactDocLinks returns documentation links relevant to a specific artefact kind.
// These are more targeted than the provider-wide links.
func ArtefactDocLinks(provider session.ProviderName, kind ArtefactKind) []DocLink {
	key := docKey{provider: provider, kind: kind}
	return artefactDocs[key]
}

// ── Registry ──

// providerDocs maps providers to their general documentation links.
var providerDocs = map[session.ProviderName][]DocLink{
	session.ProviderOpenCode: {
		{
			Label: "OpenCode skills directory structure",
			URL:   "https://opencode.ai/docs/skills",
		},
		{
			Label: "OpenCode AGENTS.md configuration",
			URL:   "https://opencode.ai/docs/agents",
		},
	},
	session.ProviderClaudeCode: {
		{
			Label: "Claude Code custom slash commands",
			URL:   "https://docs.anthropic.com/en/docs/claude-code/tutorials#create-custom-slash-commands",
		},
		{
			Label: "Claude Code CLAUDE.md configuration",
			URL:   "https://docs.anthropic.com/en/docs/claude-code/memory#claudemd",
		},
	},
	session.ProviderCursor: {
		{
			Label: "Cursor rules configuration",
			URL:   "https://docs.cursor.com/context/rules-for-ai",
		},
	},
}

type docKey struct {
	provider session.ProviderName
	kind     ArtefactKind
}

// artefactDocs maps (provider, artefact kind) to targeted documentation.
var artefactDocs = map[docKey][]DocLink{
	{session.ProviderOpenCode, ArtefactSkill}: {
		{Label: "Creating OpenCode skills", URL: "https://opencode.ai/docs/skills#creating-skills"},
	},
	{session.ProviderOpenCode, ArtefactScript}: {
		{Label: "OpenCode bin/ scripts", URL: "https://opencode.ai/docs/skills#bin-scripts"},
	},
	{session.ProviderClaudeCode, ArtefactCommand}: {
		{Label: "Custom slash commands", URL: "https://docs.anthropic.com/en/docs/claude-code/tutorials#create-custom-slash-commands"},
	},
	{session.ProviderClaudeCode, ArtefactAgentInstructions}: {
		{Label: "CLAUDE.md memory files", URL: "https://docs.anthropic.com/en/docs/claude-code/memory#claudemd"},
	},
	{session.ProviderCursor, ArtefactAgentInstructions}: {
		{Label: "Cursor rules for AI", URL: "https://docs.cursor.com/context/rules-for-ai"},
	},
}
