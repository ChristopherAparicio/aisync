package diagnostic

import (
	"sort"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// InspectReport is the complete diagnostic output for a session.
type InspectReport struct {
	// Identity
	SessionID string `json:"session_id"`
	Provider  string `json:"provider"`
	Agent     string `json:"agent"`

	// Message counts
	Messages int `json:"messages"`
	UserMsgs int `json:"user_messages"`
	AsstMsgs int `json:"assistant_messages"`

	// Sections
	Tokens     *TokenSection      `json:"tokens"`
	Images     *ImageSection      `json:"images"`
	Compaction *CompactionSection `json:"compactions"`
	Commands   *CommandSection    `json:"commands"`
	ToolErrors *ToolErrorSection  `json:"tool_errors"`
	Patterns   *PatternSection    `json:"patterns"`

	// Detected problems (sorted by severity then metric)
	Problems []Problem `json:"problems"`

	// Trend compares this session's metrics against the project's historical baseline.
	// Nil when no baseline is available (fewer than 3 historical sessions).
	Trend *TrendComparison `json:"trend,omitempty"`

	// Module activation metadata (which modules ran and how many problems each found)
	ModuleResults []ModuleResult `json:"module_results,omitempty"`

	// moduleData holds module-specific pre-computed stats keyed by module name.
	// Each module owns its slot (e.g. "rtk" → *rtk.Analysis, "api" → *api.Analysis).
	// Not serialized to JSON — only used during detection.
	moduleData map[string]any `json:"-"`
}

// SetModuleData stores module-specific data on the report, keyed by module name.
// Modules call this from Detect() to attach pre-computed stats.
func (r *InspectReport) SetModuleData(key string, data any) {
	if r.moduleData == nil {
		r.moduleData = make(map[string]any)
	}
	r.moduleData[key] = data
}

// GetModuleData retrieves module-specific data previously stored via SetModuleData.
// Returns nil if no data was stored for that key.
func (r *InspectReport) GetModuleData(key string) any {
	if r.moduleData == nil {
		return nil
	}
	return r.moduleData[key]
}

// --- Token Section ---

// TokenSection summarizes token usage.
type TokenSection struct {
	Input      int     `json:"input_tokens"`
	Output     int     `json:"output_tokens"`
	Total      int     `json:"total_tokens"`
	Image      int     `json:"image_tokens"`
	CacheRead  int     `json:"cache_read_tokens"`
	CacheWrite int     `json:"cache_write_tokens"`
	CachePct   float64 `json:"cache_read_pct"`
	EstCost    float64 `json:"estimated_cost"`
	// Input/Output ratio: how many input tokens per output token.
	// High ratio (>50) means the LLM is processing far more context than it produces.
	InputOutputRatio float64      `json:"input_output_ratio"`
	Models           []ModelUsage `json:"models,omitempty"`
}

// ModelUsage breaks down tokens by model.
type ModelUsage struct {
	Model  string `json:"model"`
	Input  int    `json:"input_tokens"`
	Output int    `json:"output_tokens"`
	Msgs   int    `json:"messages"`
}

// --- Image Section ---

// ImageSection analyzes screenshot/image token costs.
type ImageSection struct {
	InlineImages   int           `json:"inline_images"`
	InlineTokens   int           `json:"inline_tokens"`
	ToolReadImages int           `json:"tool_read_images"`
	SimctlCaptures int           `json:"simctl_captures"`
	SipsResizes    int           `json:"sips_resizes"`
	TotalBilledTok int64         `json:"total_billed_tokens"`
	EstImageCost   float64       `json:"est_image_cost_usd"`
	AvgTurnsInCtx  float64       `json:"avg_turns_in_context"`
	Details        []ImageDetail `json:"details,omitempty"`
}

// ImageDetail describes one image read event.
type ImageDetail struct {
	MsgIdx         int    `json:"msg_index"`
	Path           string `json:"path"`
	EstTokens      int    `json:"est_tokens"`
	MsgsInContext  int    `json:"msgs_in_context"`
	AssistantTurns int    `json:"assistant_turns"`
	BilledTokens   int    `json:"billed_tokens"`
}

// --- Compaction Section ---

// CompactionSection summarizes compaction patterns.
type CompactionSection struct {
	Count             int              `json:"count"`
	CascadeCount      int              `json:"cascade_count"`
	DetectionCoverage string           `json:"detection_coverage"`
	PerUserMsg        float64          `json:"per_user_message"`
	LastQuartileRate  float64          `json:"last_quartile_rate"`
	TotalTokensLost   int              `json:"total_tokens_lost"`
	AvgBeforeTokens   int              `json:"avg_before_tokens,omitempty"`
	IntervalMin       int              `json:"interval_min_msgs,omitempty"`
	IntervalMax       int              `json:"interval_max_msgs,omitempty"`
	IntervalAvg       float64          `json:"interval_avg_msgs,omitempty"`
	IntervalMedian    int              `json:"interval_median_msgs,omitempty"`
	Events            []CompactionView `json:"events,omitempty"`
}

// CompactionView describes one compaction event.
type CompactionView struct {
	BeforeMsgIdx int     `json:"before_msg_idx"`
	AfterMsgIdx  int     `json:"after_msg_idx"`
	BeforeTokens int     `json:"before_tokens"`
	AfterTokens  int     `json:"after_tokens"`
	TokensLost   int     `json:"tokens_lost"`
	DropPct      float64 `json:"drop_pct"`
	IsCascade    bool    `json:"is_cascade,omitempty"`
	MergedLegs   int     `json:"merged_legs,omitempty"`
}

// --- Command Section ---

// CommandSection summarizes command output costs.
type CommandSection struct {
	TotalCommands    int            `json:"total_commands"`
	TotalOutputBytes int64          `json:"total_output_bytes"`
	TotalOutputTok   int64          `json:"total_output_tokens"`
	UniqueCommands   int            `json:"unique_commands"`
	RepeatedRatio    float64        `json:"repeated_ratio"` // duplicates / total
	TopByOutput      []CommandEntry `json:"top_by_output,omitempty"`
}

// CommandEntry describes aggregated output for a base command.
type CommandEntry struct {
	Command     string `json:"command"`
	Invocations int    `json:"invocations"`
	TotalBytes  int64  `json:"total_bytes"`
	AvgBytes    int64  `json:"avg_bytes"`
	EstTokens   int64  `json:"est_tokens"`
	ErrorCount  int    `json:"error_count,omitempty"`
	AvgDuration int    `json:"avg_duration_ms,omitempty"`
}

// --- Tool Error Section ---

// ToolErrorSection analyzes tool call failures.
type ToolErrorSection struct {
	TotalToolCalls int              `json:"total_tool_calls"`
	ErrorCount     int              `json:"error_count"`
	ErrorRate      float64          `json:"error_rate"` // errors / total
	ConsecutiveMax int              `json:"consecutive_max_errors"`
	ErrorLoops     []ErrorLoop      `json:"error_loops,omitempty"`
	TopErrorTools  []ToolErrorEntry `json:"top_error_tools,omitempty"`
}

// ErrorLoop describes a sequence of consecutive errors on the same tool.
type ErrorLoop struct {
	ToolName    string `json:"tool_name"`
	StartMsgIdx int    `json:"start_msg_idx"`
	EndMsgIdx   int    `json:"end_msg_idx"`
	ErrorCount  int    `json:"error_count"`
	TotalTokens int    `json:"total_tokens_wasted"`
}

// ToolErrorEntry describes error stats for a specific tool.
type ToolErrorEntry struct {
	Name       string  `json:"name"`
	TotalCalls int     `json:"total_calls"`
	Errors     int     `json:"errors"`
	ErrorRate  float64 `json:"error_rate"`
}

// --- Pattern Section ---

// PatternSection detects behavioral anti-patterns in agent sessions.
type PatternSection struct {
	// Write-without-read: tool calls that write/edit files never read first.
	WriteWithoutReadCount int `json:"write_without_read_count"`
	// Read-then-ignore: files read but never referenced in subsequent tool calls.
	ReadThenIgnoreCount int `json:"read_then_ignore_count"`
	// Glob storms: >5 consecutive glob/search tool calls without acting on results.
	GlobStormCount int `json:"glob_storm_count"`
	// Back-and-forth: user message immediately followed by another user message (corrections).
	UserCorrectionCount int `json:"user_correction_count"`
	// Long assistant runs: >10 consecutive assistant messages without user input.
	LongRunCount     int `json:"long_run_count"`
	LongestRunLength int `json:"longest_run_length"`
}

// --- Builder ---

// BuildReport constructs a full InspectReport from a session and optional events.
// Extra modules (e.g. script modules) are appended to the default built-in modules.
func BuildReport(sess *session.Session, events []sessionevent.Event, extraModules ...AnalysisModule) *InspectReport {
	var userCount, asstCount int
	for _, msg := range sess.Messages {
		switch msg.Role {
		case session.RoleUser:
			userCount++
		case session.RoleAssistant:
			asstCount++
		}
	}

	r := &InspectReport{
		SessionID: string(sess.ID),
		Provider:  string(sess.Provider),
		Agent:     sess.Agent,
		Messages:  len(sess.Messages),
		UserMsgs:  userCount,
		AsstMsgs:  asstCount,
	}

	r.Tokens = buildTokenSection(sess)
	r.Images = buildImageSection(sess)
	r.Compaction = buildCompactionSection(sess)
	r.Commands = buildCommandSection(sess, events)
	r.ToolErrors = buildToolErrorSection(sess)
	r.Patterns = buildPatternSection(sess)

	// Run modular detection system.
	// Each module builds its own stats in Detect() — BuildReport does not
	// need to know about module-specific concerns (RTK, API, etc.).
	modules := DefaultModules()
	modules = append(modules, extraModules...)
	problems, moduleResults := RunModules(modules, sess, r)
	r.Problems = problems
	r.ModuleResults = moduleResults

	// Sort: high severity first, then by metric descending
	sort.Slice(r.Problems, func(i, j int) bool {
		si, sj := severityRank(r.Problems[i].Severity), severityRank(r.Problems[j].Severity)
		if si != sj {
			return si < sj
		}
		return r.Problems[i].Metric > r.Problems[j].Metric
	})

	return r
}

func severityRank(s Severity) int {
	switch s {
	case SeverityHigh:
		return 0
	case SeverityMedium:
		return 1
	case SeverityLow:
		return 2
	default:
		return 3
	}
}

// --- Section builders ---

func buildTokenSection(sess *session.Session) *TokenSection {
	t := &TokenSection{
		Input:      sess.TokenUsage.InputTokens,
		Output:     sess.TokenUsage.OutputTokens,
		Total:      sess.TokenUsage.TotalTokens,
		Image:      sess.TokenUsage.ImageTokens,
		CacheRead:  sess.TokenUsage.CacheRead,
		CacheWrite: sess.TokenUsage.CacheWrite,
		EstCost:    sess.EstimatedCost,
	}
	if t.Input > 0 {
		t.CachePct = float64(t.CacheRead) / float64(t.Input) * 100
	}
	if t.Output > 0 {
		t.InputOutputRatio = float64(t.Input) / float64(t.Output)
	}

	modelMap := make(map[string]*ModelUsage)
	for _, msg := range sess.Messages {
		if msg.Model == "" {
			continue
		}
		mu, ok := modelMap[msg.Model]
		if !ok {
			mu = &ModelUsage{Model: msg.Model}
			modelMap[msg.Model] = mu
		}
		mu.Input += msg.InputTokens
		mu.Output += msg.OutputTokens
		mu.Msgs++
	}
	for _, mu := range modelMap {
		t.Models = append(t.Models, *mu)
	}
	sort.Slice(t.Models, func(i, j int) bool {
		return t.Models[i].Input > t.Models[j].Input
	})

	return t
}

func buildImageSection(sess *session.Session) *ImageSection {
	r := &ImageSection{}

	// Inline images
	for _, msg := range sess.Messages {
		for _, img := range msg.Images {
			r.InlineImages++
			r.InlineTokens += img.TokensEstimate
		}
		for _, cb := range msg.ContentBlocks {
			if cb.Type == session.ContentBlockImage && cb.Image != nil {
				r.InlineImages++
				r.InlineTokens += cb.Image.TokensEstimate
			}
		}
	}

	// Compaction points for context duration calculation
	compactions := session.DetectCompactions(sess.Messages, 0)
	compactionIndices := make([]int, 0, len(compactions.Events))
	for _, c := range compactions.Events {
		compactionIndices = append(compactionIndices, c.AfterMessageIdx)
	}
	sort.Ints(compactionIndices)

	// Scan tool calls
	type imgRead struct {
		msgIdx int
		path   string
		tokens int
	}
	var reads []imgRead

	for i, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			name := strings.ToLower(tc.Name)
			if name == "read" || name == "mcp_read" {
				input := strings.ToLower(tc.Input)
				if strings.Contains(input, ".png") || strings.Contains(input, ".jpg") || strings.Contains(input, ".jpeg") {
					reads = append(reads, imgRead{msgIdx: i, path: extractPath(tc.Input), tokens: 1500})
				}
			}
			if name == "bash" || name == "mcp_bash" {
				if strings.Contains(tc.Input, "screenshot") && strings.Contains(tc.Input, "simctl") {
					r.SimctlCaptures++
				}
				if strings.Contains(tc.Input, "sips") && strings.Contains(tc.Input, "-Z") {
					r.SipsResizes++
				}
			}
		}
	}
	r.ToolReadImages = len(reads)

	// Context duration + billed tokens
	var totalTurns int
	for _, img := range reads {
		msgsUntilReset := len(sess.Messages) - img.msgIdx
		for _, cIdx := range compactionIndices {
			if cIdx > img.msgIdx {
				msgsUntilReset = cIdx - img.msgIdx
				break
			}
		}
		assistantTurns := 0
		end := img.msgIdx + msgsUntilReset
		if end > len(sess.Messages) {
			end = len(sess.Messages)
		}
		for j := img.msgIdx; j < end; j++ {
			if sess.Messages[j].Role == session.RoleAssistant {
				assistantTurns++
			}
		}
		if assistantTurns < 1 {
			assistantTurns = 1
		}
		billed := img.tokens * assistantTurns
		r.TotalBilledTok += int64(billed)
		totalTurns += assistantTurns
		r.Details = append(r.Details, ImageDetail{
			MsgIdx: img.msgIdx, Path: img.path, EstTokens: img.tokens,
			MsgsInContext: msgsUntilReset, AssistantTurns: assistantTurns, BilledTokens: billed,
		})
	}
	if len(reads) > 0 {
		r.AvgTurnsInCtx = float64(totalTurns) / float64(len(reads))
	}
	r.EstImageCost = float64(r.TotalBilledTok) * 3.0 / 1_000_000

	return r
}

func buildCompactionSection(sess *session.Session) *CompactionSection {
	comp := session.DetectCompactions(sess.Messages, 0)
	r := &CompactionSection{
		Count:             len(comp.Events),
		CascadeCount:      comp.CascadeCount,
		DetectionCoverage: comp.DetectionCoverage,
		PerUserMsg:        comp.CompactionsPerUserMessage,
		LastQuartileRate:  comp.LastQuartileCompactionRate,
		TotalTokensLost:   comp.TotalTokensLost,
	}

	var beforeSum int
	for _, e := range comp.Events {
		beforeSum += e.BeforeInputTokens
		r.Events = append(r.Events, CompactionView{
			BeforeMsgIdx: e.BeforeMessageIdx, AfterMsgIdx: e.AfterMessageIdx,
			BeforeTokens: e.BeforeInputTokens, AfterTokens: e.AfterInputTokens,
			TokensLost: e.TokensLost, DropPct: e.DropPercent,
			IsCascade: e.IsCascade, MergedLegs: e.MergedLegs,
		})
	}
	if len(comp.Events) > 0 {
		r.AvgBeforeTokens = beforeSum / len(comp.Events)
	}

	// Interval stats
	if len(comp.Events) > 1 {
		var gaps []int
		for i := 1; i < len(comp.Events); i++ {
			gap := comp.Events[i].BeforeMessageIdx - comp.Events[i-1].AfterMessageIdx
			if gap > 0 {
				gaps = append(gaps, gap)
			}
		}
		if len(gaps) > 0 {
			sort.Ints(gaps)
			var sum int
			for _, g := range gaps {
				sum += g
			}
			r.IntervalMin = gaps[0]
			r.IntervalMax = gaps[len(gaps)-1]
			r.IntervalAvg = float64(sum) / float64(len(gaps))
			r.IntervalMedian = gaps[len(gaps)/2]
		}
	}

	return r
}

func buildCommandSection(sess *session.Session, events []sessionevent.Event) *CommandSection {
	r := &CommandSection{}

	type cmdAgg struct {
		command    string
		count      int
		totalBytes int64
		errors     int
		totalDur   int
	}
	aggMap := make(map[string]*cmdAgg)
	fullCommands := make(map[string]bool) // track unique full commands

	if len(events) > 0 {
		for _, evt := range events {
			if evt.Type != sessionevent.EventCommand || evt.Command == nil {
				continue
			}
			r.TotalCommands++
			r.TotalOutputBytes += int64(evt.Command.OutputBytes)
			r.TotalOutputTok += int64(evt.Command.OutputTokens)
			fullCommands[evt.Command.FullCommand] = true
			base := evt.Command.BaseCommand
			if base == "" {
				base = "(unknown)"
			}
			a, ok := aggMap[base]
			if !ok {
				a = &cmdAgg{command: base}
				aggMap[base] = a
			}
			a.count++
			a.totalBytes += int64(evt.Command.OutputBytes)
			a.totalDur += evt.Command.DurationMs
		}
	} else {
		// Fallback: scan tool calls
		for _, msg := range sess.Messages {
			for _, tc := range msg.ToolCalls {
				name := strings.ToLower(tc.Name)
				if name == "bash" || name == "mcp_bash" || name == "execute_command" {
					r.TotalCommands++
					outLen := int64(len(tc.Output))
					r.TotalOutputBytes += outLen
					r.TotalOutputTok += outLen / 4
					full := ExtractCommandFull(tc.Input)
					fullCommands[full] = true
					base := ExtractCommandBase(tc.Input)
					a, ok := aggMap[base]
					if !ok {
						a = &cmdAgg{command: base}
						aggMap[base] = a
					}
					a.count++
					a.totalBytes += outLen
					if tc.State == session.ToolStateError {
						a.errors++
					}
				}
			}
		}
	}

	r.UniqueCommands = len(fullCommands)
	if r.TotalCommands > 0 {
		r.RepeatedRatio = 1.0 - float64(r.UniqueCommands)/float64(r.TotalCommands)
	}

	entries := make([]CommandEntry, 0, len(aggMap))
	for _, a := range aggMap {
		avg := int64(0)
		if a.count > 0 {
			avg = a.totalBytes / int64(a.count)
		}
		avgDur := 0
		if a.count > 0 {
			avgDur = a.totalDur / a.count
		}
		entries = append(entries, CommandEntry{
			Command: a.command, Invocations: a.count, TotalBytes: a.totalBytes,
			AvgBytes: avg, EstTokens: a.totalBytes / 4,
			ErrorCount: a.errors, AvgDuration: avgDur,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TotalBytes > entries[j].TotalBytes
	})
	if len(entries) > 20 {
		entries = entries[:20]
	}
	r.TopByOutput = entries

	return r
}

func buildToolErrorSection(sess *session.Session) *ToolErrorSection {
	r := &ToolErrorSection{}

	type toolStats struct {
		name   string
		total  int
		errors int
	}
	statsMap := make(map[string]*toolStats)

	// Track consecutive errors for loop detection
	var loops []ErrorLoop
	var currentLoopTool string
	var currentLoopStart, currentLoopErrors, currentLoopTokens int

	for i, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			r.TotalToolCalls++
			ts, ok := statsMap[tc.Name]
			if !ok {
				ts = &toolStats{name: tc.Name}
				statsMap[tc.Name] = ts
			}
			ts.total++

			isError := tc.State == session.ToolStateError
			if isError {
				r.ErrorCount++
				ts.errors++

				// Track consecutive errors
				if tc.Name == currentLoopTool {
					currentLoopErrors++
					currentLoopTokens += msg.InputTokens
				} else {
					// Flush previous loop if significant
					if currentLoopErrors >= 3 {
						loops = append(loops, ErrorLoop{
							ToolName: currentLoopTool, StartMsgIdx: currentLoopStart,
							EndMsgIdx: i - 1, ErrorCount: currentLoopErrors,
							TotalTokens: currentLoopTokens,
						})
					}
					currentLoopTool = tc.Name
					currentLoopStart = i
					currentLoopErrors = 1
					currentLoopTokens = msg.InputTokens
				}
			} else {
				// Success breaks the loop
				if currentLoopErrors >= 3 {
					loops = append(loops, ErrorLoop{
						ToolName: currentLoopTool, StartMsgIdx: currentLoopStart,
						EndMsgIdx: i, ErrorCount: currentLoopErrors,
						TotalTokens: currentLoopTokens,
					})
				}
				currentLoopErrors = 0
				currentLoopTool = ""
			}
		}
	}
	// Flush final loop
	if currentLoopErrors >= 3 {
		loops = append(loops, ErrorLoop{
			ToolName: currentLoopTool, StartMsgIdx: currentLoopStart,
			EndMsgIdx: len(sess.Messages) - 1, ErrorCount: currentLoopErrors,
			TotalTokens: currentLoopTokens,
		})
	}

	r.ErrorLoops = loops
	for _, l := range loops {
		if l.ErrorCount > r.ConsecutiveMax {
			r.ConsecutiveMax = l.ErrorCount
		}
	}

	if r.TotalToolCalls > 0 {
		r.ErrorRate = float64(r.ErrorCount) / float64(r.TotalToolCalls)
	}

	// Top error tools
	var topErrors []ToolErrorEntry
	for _, ts := range statsMap {
		if ts.errors > 0 {
			rate := float64(ts.errors) / float64(ts.total)
			topErrors = append(topErrors, ToolErrorEntry{
				Name: ts.name, TotalCalls: ts.total, Errors: ts.errors, ErrorRate: rate,
			})
		}
	}
	sort.Slice(topErrors, func(i, j int) bool {
		return topErrors[i].Errors > topErrors[j].Errors
	})
	if len(topErrors) > 10 {
		topErrors = topErrors[:10]
	}
	r.TopErrorTools = topErrors

	return r
}

func buildPatternSection(sess *session.Session) *PatternSection {
	r := &PatternSection{}

	// Track files read vs files edited to detect write-without-read and read-then-ignore
	filesRead := make(map[string]bool)
	filesEdited := make(map[string]bool)

	// Track consecutive user messages (corrections)
	prevRole := session.MessageRole("")
	consecutiveAssistant := 0
	maxConsecutiveAssistant := 0

	// Track glob storms
	consecutiveSearches := 0

	for _, msg := range sess.Messages {
		// Consecutive user messages (corrections/clarifications)
		if msg.Role == session.RoleUser {
			if prevRole == session.RoleUser {
				r.UserCorrectionCount++
			}
			consecutiveAssistant = 0
		}

		// Long assistant runs
		if msg.Role == session.RoleAssistant {
			consecutiveAssistant++
			if consecutiveAssistant > maxConsecutiveAssistant {
				maxConsecutiveAssistant = consecutiveAssistant
			}
			if consecutiveAssistant > 10 && prevRole == session.RoleAssistant {
				// Count unique long runs
				if consecutiveAssistant == 11 {
					r.LongRunCount++
				}
			}
		}

		// Analyze tool calls
		hasSearch := false
		for _, tc := range msg.ToolCalls {
			name := strings.ToLower(tc.Name)

			// Track file operations
			if name == "read" || name == "mcp_read" {
				path := extractPath(tc.Input)
				filesRead[path] = true
			}
			if name == "edit" || name == "mcp_edit" || name == "write" || name == "mcp_write" {
				path := extractPath(tc.Input)
				if !filesRead[path] && name != "write" && name != "mcp_write" {
					// Edit without prior read (could be a new file write though)
				}
				filesEdited[path] = true
			}

			// Glob/search storms
			if name == "glob" || name == "mcp_glob" || name == "grep" || name == "mcp_grep" ||
				name == "search" || name == "mcp_search" || name == "find" {
				hasSearch = true
			}
		}

		if hasSearch {
			consecutiveSearches++
			if consecutiveSearches > 5 {
				if consecutiveSearches == 6 {
					r.GlobStormCount++
				}
			}
		} else if len(msg.ToolCalls) > 0 {
			consecutiveSearches = 0
		}

		prevRole = msg.Role
	}

	r.LongestRunLength = maxConsecutiveAssistant

	// Write-without-read: files that were edited but never read
	for path := range filesEdited {
		if !filesRead[path] {
			r.WriteWithoutReadCount++
		}
	}

	return r
}
