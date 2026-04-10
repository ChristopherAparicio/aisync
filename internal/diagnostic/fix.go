package diagnostic

import "github.com/ChristopherAparicio/aisync/internal/session"

// FixSet is the complete output of fix generation for a session.
// It groups fixes by the problem they address.
type FixSet struct {
	SessionID string       `json:"session_id"`
	Provider  string       `json:"provider"`
	Fixes     []ProblemFix `json:"fixes"`
	Applied   bool         `json:"applied"` // true if --apply was used
}

// ProblemFix ties a detected problem to its generated artefacts.
type ProblemFix struct {
	ProblemID    ProblemID  `json:"problem_id"`
	ProblemTitle string     `json:"problem_title"`
	Severity     Severity   `json:"severity"`
	Artefacts    []Artefact `json:"artefacts"`
	DocLinks     []DocLink  `json:"doc_links,omitempty"` // provider documentation references
}

// ArtefactKind describes what type of artefact is being generated.
type ArtefactKind string

const (
	ArtefactAgentInstructions ArtefactKind = "agent_instructions" // AGENTS.md, CLAUDE.md, .cursorrules
	ArtefactScript            ArtefactKind = "script"             // capture-screen.sh, etc.
	ArtefactSkill             ArtefactKind = "skill"              // .opencode/skills/*/SKILL.md
	ArtefactCommand           ArtefactKind = "command"            // .claude/commands/*.md
)

// Artefact is a single generated file or patch that addresses a detected problem.
type Artefact struct {
	Kind        ArtefactKind `json:"kind"`
	Description string       `json:"description"` // what this artefact does
	RelPath     string       `json:"rel_path"`    // relative path from project root, e.g. ".opencode/skills/screenshot-capture/SKILL.md"
	Content     string       `json:"content"`     // full file content (for new files) or patch to append
	AppendTo    bool         `json:"append_to"`   // if true, append content to existing file instead of creating
}

// ProviderPaths maps provider names to their ecosystem file paths.
type ProviderPaths struct {
	InstructionsFile string // e.g. "AGENTS.md", "CLAUDE.md", ".cursorrules"
	ScriptsDir       string // e.g. ".opencode/bin/", ".claude/commands/"
	SkillsDir        string // e.g. ".opencode/skills/" (empty if unsupported)
	CommandsDir      string // e.g. ".claude/commands/" (empty if unsupported)
}

// ProviderPathsFor returns the ecosystem paths for a given provider.
func ProviderPathsFor(provider session.ProviderName) ProviderPaths {
	switch provider {
	case session.ProviderOpenCode:
		return ProviderPaths{
			InstructionsFile: "AGENTS.md",
			ScriptsDir:       ".opencode/bin/",
			SkillsDir:        ".opencode/skills/",
		}
	case session.ProviderClaudeCode:
		return ProviderPaths{
			InstructionsFile: "CLAUDE.md",
			CommandsDir:      ".claude/commands/",
		}
	case session.ProviderCursor:
		return ProviderPaths{
			InstructionsFile: ".cursorrules",
		}
	default:
		// Fallback: use a generic instructions file
		return ProviderPaths{
			InstructionsFile: "AGENTS.md",
		}
	}
}
