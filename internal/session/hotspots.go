package session

import (
	"encoding/json"
	"sort"
)

// SessionHotspots aggregates pre-computed investigation data for a session.
// Computed once and cached; used by the session detail "Hot Spots" tab
// and by analysis agents via the investigation API.
type SessionHotspots struct {
	// Top commands ranked by total output bytes across all invocations.
	TopCommandsByOutput []CommandHotspot `json:"top_commands_by_output"`

	// Top commands ranked by invocation count (repeated patterns).
	TopCommandsByReuse []CommandHotspot `json:"top_commands_by_reuse"`

	// Compaction summary (carried forward from DetectCompactions).
	CompactionCount   int     `json:"compaction_count"`
	CompactionRate    float64 `json:"compaction_rate"`    // compactions per user message
	LastQuartileRate  float64 `json:"last_quartile_rate"` // rate over last 25% of user messages
	CascadeCount      int     `json:"cascade_count"`
	TotalTokensLost   int     `json:"total_tokens_lost"`
	DetectionCoverage string  `json:"detection_coverage"` // "full", "partial", "none"

	// Skill footprints: skills loaded during the session and their token cost.
	SkillFootprints []SkillFootprint `json:"skill_footprints,omitempty"`

	// Messages with the highest token cost (top 5).
	ExpensiveMessages []ExpensiveMessage `json:"expensive_messages"`

	// Session-level aggregates.
	TotalCommands     int `json:"total_commands"`
	TotalOutputBytes  int `json:"total_output_bytes"`
	UniqueCommands    int `json:"unique_commands"`
	CommandErrorCount int `json:"command_error_count"`
}

// CommandHotspot captures aggregated metrics for a single base command.
type CommandHotspot struct {
	BaseCommand string `json:"base_command"`       // e.g. "git", "go", "npm"
	Invocations int    `json:"invocations"`        // total runs
	TotalOutput int    `json:"total_output_bytes"` // total output bytes across all runs
	AvgOutput   int    `json:"avg_output_bytes"`   // average output per run
	ErrorCount  int    `json:"error_count"`        // runs that ended in error
	TokenImpact int    `json:"token_impact"`       // estimated tokens = TotalOutput / 4
}

// SkillFootprint captures a skill that was loaded and its context cost.
type SkillFootprint struct {
	Name            string `json:"name"`
	LoadCount       int    `json:"load_count"`
	TotalBytes      int    `json:"total_bytes"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

// ExpensiveMessage captures a message with unusually high token cost.
type ExpensiveMessage struct {
	Index        int         `json:"index"` // 0-based message index
	Role         MessageRole `json:"role"`
	InputTokens  int         `json:"input_tokens"`
	OutputTokens int         `json:"output_tokens"`
	TotalTokens  int         `json:"total_tokens"` // input + output
	Model        string      `json:"model,omitempty"`
}

// ComputeHotspots computes investigation hot-spots from a session's data.
// This is a pure function with no side effects — suitable for caching.
// The inputRate parameter is the cost per input token (pass 0 to skip cost estimation).
func ComputeHotspots(sess *Session, inputRate float64) SessionHotspots {
	var h SessionHotspots

	// 1. Command aggregation.
	type cmdAgg struct {
		count      int
		totalOut   int
		errorCount int
	}
	cmdMap := make(map[string]*cmdAgg)

	for i := range sess.Messages {
		for j := range sess.Messages[i].ToolCalls {
			tc := &sess.Messages[i].ToolCalls[j]
			base := ExtractBaseCommand(tc.Name, tc.Input)
			if base == "" {
				continue
			}
			h.TotalCommands++
			outBytes := len(tc.Output)
			h.TotalOutputBytes += outBytes

			agg, ok := cmdMap[base]
			if !ok {
				agg = &cmdAgg{}
				cmdMap[base] = agg
			}
			agg.count++
			agg.totalOut += outBytes
			if tc.State == ToolStateError {
				agg.errorCount++
				h.CommandErrorCount++
			}
		}
	}
	h.UniqueCommands = len(cmdMap)

	// Build sorted command lists.
	var cmdList []CommandHotspot
	for name, agg := range cmdMap {
		avg := 0
		if agg.count > 0 {
			avg = agg.totalOut / agg.count
		}
		cmdList = append(cmdList, CommandHotspot{
			BaseCommand: name,
			Invocations: agg.count,
			TotalOutput: agg.totalOut,
			AvgOutput:   avg,
			ErrorCount:  agg.errorCount,
			TokenImpact: agg.totalOut / 4,
		})
	}

	// Top by output.
	byOutput := make([]CommandHotspot, len(cmdList))
	copy(byOutput, cmdList)
	sort.Slice(byOutput, func(i, j int) bool {
		return byOutput[i].TotalOutput > byOutput[j].TotalOutput
	})
	if len(byOutput) > 10 {
		byOutput = byOutput[:10]
	}
	h.TopCommandsByOutput = byOutput

	// Top by reuse.
	byReuse := make([]CommandHotspot, len(cmdList))
	copy(byReuse, cmdList)
	sort.Slice(byReuse, func(i, j int) bool {
		return byReuse[i].Invocations > byReuse[j].Invocations
	})
	if len(byReuse) > 10 {
		byReuse = byReuse[:10]
	}
	h.TopCommandsByReuse = byReuse

	// 2. Compaction data.
	compSummary := DetectCompactions(sess.Messages, inputRate)
	h.CompactionCount = compSummary.TotalCompactions
	h.CompactionRate = compSummary.CompactionsPerUserMessage
	h.LastQuartileRate = compSummary.LastQuartileCompactionRate
	h.CascadeCount = compSummary.CascadeCount
	h.TotalTokensLost = compSummary.TotalTokensLost
	h.DetectionCoverage = compSummary.DetectionCoverage

	// 3. Skill footprints.
	skillMap := make(map[string]*SkillFootprint)
	for i := range sess.Messages {
		for j := range sess.Messages[i].ToolCalls {
			tc := &sess.Messages[i].ToolCalls[j]
			if isSkillTool(tc.Name) {
				name := extractSkillNameFromTC(tc)
				if name == "" {
					continue
				}
				sf, ok := skillMap[name]
				if !ok {
					sf = &SkillFootprint{Name: name}
					skillMap[name] = sf
				}
				sf.LoadCount++
				outLen := len(tc.Output)
				sf.TotalBytes += outLen
				sf.EstimatedTokens += outLen / 4
			}
		}
	}
	for _, sf := range skillMap {
		h.SkillFootprints = append(h.SkillFootprints, *sf)
	}
	sort.Slice(h.SkillFootprints, func(i, j int) bool {
		return h.SkillFootprints[i].EstimatedTokens > h.SkillFootprints[j].EstimatedTokens
	})

	// 4. Expensive messages (top 5 by total tokens).
	type msgCost struct {
		idx    int
		role   MessageRole
		input  int
		output int
		total  int
		model  string
	}
	var msgs []msgCost
	for i, m := range sess.Messages {
		total := m.InputTokens + m.OutputTokens
		if total > 0 {
			msgs = append(msgs, msgCost{
				idx: i, role: m.Role, input: m.InputTokens,
				output: m.OutputTokens, total: total, model: m.Model,
			})
		}
	}
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].total > msgs[j].total
	})
	limit := 5
	if len(msgs) < limit {
		limit = len(msgs)
	}
	for _, mc := range msgs[:limit] {
		h.ExpensiveMessages = append(h.ExpensiveMessages, ExpensiveMessage{
			Index: mc.idx, Role: mc.role, InputTokens: mc.input,
			OutputTokens: mc.output, TotalTokens: mc.total, Model: mc.model,
		})
	}

	return h
}

// isSkillTool checks if a tool call name indicates a skill load.
func isSkillTool(name string) bool {
	return name == "skill" || name == "load_skill" || name == "mcp_skill"
}

// extractSkillNameFromTC tries to get the skill name from a tool call.
func extractSkillNameFromTC(tc *ToolCall) string {
	// Try input JSON: {"name": "skill-name"}
	if tc.Input != "" {
		var obj map[string]interface{}
		if err := parseJSON(tc.Input, &obj); err == nil {
			if name, ok := obj["name"].(string); ok && name != "" {
				return name
			}
		}
	}
	return ""
}

// parseJSON is a minimal helper that avoids importing encoding/json in
// the function signature (already imported at package level in commands.go).
func parseJSON(s string, v interface{}) error {
	// Delegate to encoding/json via the already-imported package.
	// This is in a separate function to avoid a circular import.
	return json.Unmarshal([]byte(s), v)
}
