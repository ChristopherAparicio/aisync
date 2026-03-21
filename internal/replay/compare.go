package replay

import (
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/skillobs"
)

// Compare produces a Comparison between an original and replay session.
func Compare(original, replay *session.Session) *Comparison {
	c := &Comparison{
		OriginalTokens:   original.TokenUsage.TotalTokens,
		ReplayTokens:     replay.TokenUsage.TotalTokens,
		OriginalMessages: len(original.Messages),
		ReplayMessages:   len(replay.Messages),
	}

	c.TokenDelta = c.ReplayTokens - c.OriginalTokens

	// Count errors.
	c.OriginalErrors = countErrors(original.Messages)
	c.ReplayErrors = countErrors(replay.Messages)
	c.ErrorDelta = c.ReplayErrors - c.OriginalErrors

	// Count tool calls.
	c.OriginalToolCalls = countToolCalls(original.Messages)
	c.ReplayToolCalls = countToolCalls(replay.Messages)

	// Skills comparison.
	c.OriginalSkills = skillobs.DetectLoadedSkills(original.Messages)
	c.ReplaySkills = skillobs.DetectLoadedSkills(replay.Messages)

	origSet := toStringSet(c.OriginalSkills)
	replaySet := toStringSet(c.ReplaySkills)

	for _, s := range c.ReplaySkills {
		if !origSet[s] {
			c.NewSkillsLoaded = append(c.NewSkillsLoaded, s)
		}
	}
	for _, s := range c.OriginalSkills {
		if !replaySet[s] {
			c.SkillsLost = append(c.SkillsLost, s)
		}
	}

	// Verdict.
	c.Verdict = computeVerdict(c)

	return c
}

// computeVerdict determines if the replay was better, same, or worse.
func computeVerdict(c *Comparison) string {
	score := 0

	// Fewer errors is good.
	if c.ErrorDelta < 0 {
		score += 2
	} else if c.ErrorDelta > 0 {
		score -= 2
	}

	// Fewer tokens is good (if significant — >10% change).
	if c.OriginalTokens > 0 {
		pctChange := float64(c.TokenDelta) / float64(c.OriginalTokens) * 100
		if pctChange < -10 {
			score++
		} else if pctChange > 10 {
			score--
		}
	}

	// New skills loaded is good (means the agent found them).
	if len(c.NewSkillsLoaded) > 0 {
		score++
	}
	// Skills lost is bad.
	if len(c.SkillsLost) > 0 {
		score--
	}

	if score > 0 {
		return "improved"
	} else if score < 0 {
		return "degraded"
	}
	return "same"
}

func countErrors(messages []session.Message) int {
	count := 0
	for i := range messages {
		for j := range messages[i].ToolCalls {
			if messages[i].ToolCalls[j].State == session.ToolStateError {
				count++
			}
		}
	}
	return count
}

func countToolCalls(messages []session.Message) int {
	count := 0
	for i := range messages {
		count += len(messages[i].ToolCalls)
	}
	return count
}

func toStringSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}
