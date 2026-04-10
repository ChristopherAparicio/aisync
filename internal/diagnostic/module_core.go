package diagnostic

import "github.com/ChristopherAparicio/aisync/internal/session"

func init() { RegisterModule(&CoreModule{}) }

// CoreModule contains the always-active detectors: compaction, tokens, commands,
// tool errors, and behavioral patterns. These are relevant for every session
// regardless of what tools or workflows are used.
type CoreModule struct{}

func (m *CoreModule) Name() string { return "core" }

// ShouldActivate always returns true — core detectors apply to all sessions.
func (m *CoreModule) ShouldActivate(_ *session.Session) bool { return true }

// Detect runs the 14 core detectors (the original set minus image-specific ones).
func (m *CoreModule) Detect(r *InspectReport, _ *session.Session) []Problem {
	var problems []Problem

	// Compaction detectors
	problems = append(problems, detectFrequentCompaction(r)...)
	problems = append(problems, detectContextNearLimit(r)...)
	problems = append(problems, detectCompactionAccelerating(r)...)

	// Command detectors
	problems = append(problems, detectVerboseCommands(r)...)
	problems = append(problems, detectRepeatedCommands(r)...)
	problems = append(problems, detectLongRunningCommands(r)...)

	// Token detectors
	problems = append(problems, detectLowCacheUtilization(r)...)
	problems = append(problems, detectHighInputRatio(r)...)
	problems = append(problems, detectContextThrashing(r)...)

	// Tool error detectors
	problems = append(problems, detectToolErrorLoops(r)...)
	problems = append(problems, detectAbandonedToolCalls(r)...)

	// Behavioral pattern detectors
	problems = append(problems, detectYoloEditing(r)...)
	problems = append(problems, detectExcessiveGlobbing(r)...)
	problems = append(problems, detectConversationDrift(r)...)

	return problems
}
