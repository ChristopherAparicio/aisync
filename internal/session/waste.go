package session

import "time"

// WasteCategory classifies how tokens were used.
type WasteCategory string

const (
	WasteCategoryProductive  WasteCategory = "productive"   // successful tool calls, accepted output
	WasteCategoryRetry       WasteCategory = "retry"        // failed tool calls + subsequent retries
	WasteCategoryCompaction  WasteCategory = "compaction"   // tokens lost to context summarization
	WasteCategoryCacheMiss   WasteCategory = "cache_miss"   // full-price input after cache TTL expiry
	WasteCategoryIdleContext WasteCategory = "idle_context" // system prompt overhead, unused tool descriptions
)

// TokenWasteBreakdown classifies a session's (or set of sessions') tokens by waste type.
type TokenWasteBreakdown struct {
	TotalTokens int `json:"total_tokens"` // sum of all classified tokens

	// Per-category breakdown.
	Productive  WasteBucket `json:"productive"`
	Retry       WasteBucket `json:"retry"`
	Compaction  WasteBucket `json:"compaction"`
	CacheMiss   WasteBucket `json:"cache_miss"`
	IdleContext WasteBucket `json:"idle_context"`

	// Summary.
	ProductivePct float64 `json:"productive_pct"` // productive / total * 100
	WastePct      float64 `json:"waste_pct"`      // (total - productive) / total * 100
}

// WasteBucket holds token count and percentage for a waste category.
type WasteBucket struct {
	Tokens  int     `json:"tokens"`
	Percent float64 `json:"percent"` // 0-100
}

// ClassifyTokenWaste analyzes a session's messages and classifies token usage.
//
// Classification logic:
//  1. Compaction waste: tokens lost from detected compaction events
//  2. Retry waste: tokens from messages containing only failed tool calls,
//     plus tokens from the immediately subsequent retry message
//  3. Cache miss waste: input tokens from assistant messages after gaps > 5 min
//     (cache TTL expiry means full-price re-read of context)
//  4. Idle context: first message's input tokens (system prompt, tool defs)
//  5. Productive: everything else
func ClassifyTokenWaste(messages []Message, compactions CompactionSummary) TokenWasteBreakdown {
	result := TokenWasteBreakdown{}

	if len(messages) == 0 {
		return result
	}

	var totalInputTokens int
	var totalOutputTokens int

	// Pre-compute: identify retry messages (message after a failed tool call message).
	retryMsgs := identifyRetryMessages(messages)

	// Pre-compute: identify cache miss messages (assistant msg after >5min gap).
	cacheMissMsgs := identifyCacheMissMessages(messages)

	// 1. Compaction waste: from detected compaction events.
	result.Compaction.Tokens = compactions.TotalTokensLost

	// 2-5. Walk all messages and classify.
	for i := range messages {
		msg := &messages[i]
		msgTokens := msg.InputTokens + msg.OutputTokens
		totalInputTokens += msg.InputTokens
		totalOutputTokens += msg.OutputTokens

		// Idle context: first message's input tokens = system prompt overhead.
		// Only the very first message (user's initial prompt) carries system prompt + tool defs.
		if i == 0 && msg.InputTokens > 0 {
			result.IdleContext.Tokens += msg.InputTokens
			// Output tokens (if any) are still productive work.
			result.Productive.Tokens += msg.OutputTokens
			continue
		}

		// Retry waste: messages that are retrying a failed operation.
		if retryMsgs[i] {
			result.Retry.Tokens += msgTokens
			continue
		}

		// Cache miss waste: assistant messages after cache TTL gap.
		if cacheMissMsgs[i] {
			result.CacheMiss.Tokens += msg.InputTokens
			// Output tokens from cache miss messages are still productive.
			result.Productive.Tokens += msg.OutputTokens
			continue
		}

		// Everything else is productive.
		result.Productive.Tokens += msgTokens
	}

	// Total = all message tokens + compaction waste.
	result.TotalTokens = totalInputTokens + totalOutputTokens + result.Compaction.Tokens

	// Compute percentages.
	if result.TotalTokens > 0 {
		total := float64(result.TotalTokens)
		result.Productive.Percent = float64(result.Productive.Tokens) / total * 100
		result.Retry.Percent = float64(result.Retry.Tokens) / total * 100
		result.Compaction.Percent = float64(result.Compaction.Tokens) / total * 100
		result.CacheMiss.Percent = float64(result.CacheMiss.Tokens) / total * 100
		result.IdleContext.Percent = float64(result.IdleContext.Tokens) / total * 100
		result.ProductivePct = result.Productive.Percent
		result.WastePct = 100 - result.ProductivePct
	}

	return result
}

// identifyRetryMessages marks message indices where the assistant is retrying
// a previously failed operation. A retry is detected when:
//   - The previous assistant message had a failing tool call (State == error)
//   - The current message calls the same tool again
func identifyRetryMessages(messages []Message) map[int]bool {
	retries := make(map[int]bool)

	for i := 1; i < len(messages); i++ {
		prev := &messages[i-1]
		curr := &messages[i]

		if curr.Role != RoleAssistant || prev.Role != RoleAssistant {
			continue
		}

		// Check if previous message had any failed tool calls.
		prevFailed := make(map[string]bool)
		for j := range prev.ToolCalls {
			if prev.ToolCalls[j].State == ToolStateError {
				prevFailed[prev.ToolCalls[j].Name] = true
			}
		}

		if len(prevFailed) == 0 {
			continue
		}

		// Check if current message retries any of the failed tools.
		for j := range curr.ToolCalls {
			if prevFailed[curr.ToolCalls[j].Name] {
				retries[i] = true
				break
			}
		}
	}

	return retries
}

// identifyCacheMissMessages marks assistant messages that appear after a gap
// exceeding the cache TTL (5 minutes), meaning all input tokens are re-read
// at full price instead of cache-read price.
//
// The cache expires based on inactivity from the last assistant response.
// A long gap before a user message causes the cache to expire, so the
// *next* assistant message pays full price. We track time from the last
// assistant response to detect this.
func identifyCacheMissMessages(messages []Message) map[int]bool {
	const cacheTTL = 5 * time.Minute
	misses := make(map[int]bool)

	var lastAssistantTime time.Time
	for i := range messages {
		msg := &messages[i]
		if msg.Timestamp.IsZero() {
			continue
		}
		if msg.Role == RoleAssistant {
			if !lastAssistantTime.IsZero() {
				gap := msg.Timestamp.Sub(lastAssistantTime)
				if gap > cacheTTL {
					misses[i] = true
				}
			}
			lastAssistantTime = msg.Timestamp
		}
	}

	return misses
}

// AggregateWaste combines waste breakdowns from multiple sessions.
func AggregateWaste(breakdowns []TokenWasteBreakdown) TokenWasteBreakdown {
	agg := TokenWasteBreakdown{}
	for _, b := range breakdowns {
		agg.TotalTokens += b.TotalTokens
		agg.Productive.Tokens += b.Productive.Tokens
		agg.Retry.Tokens += b.Retry.Tokens
		agg.Compaction.Tokens += b.Compaction.Tokens
		agg.CacheMiss.Tokens += b.CacheMiss.Tokens
		agg.IdleContext.Tokens += b.IdleContext.Tokens
	}
	if agg.TotalTokens > 0 {
		total := float64(agg.TotalTokens)
		agg.Productive.Percent = float64(agg.Productive.Tokens) / total * 100
		agg.Retry.Percent = float64(agg.Retry.Tokens) / total * 100
		agg.Compaction.Percent = float64(agg.Compaction.Tokens) / total * 100
		agg.CacheMiss.Percent = float64(agg.CacheMiss.Tokens) / total * 100
		agg.IdleContext.Percent = float64(agg.IdleContext.Tokens) / total * 100
		agg.ProductivePct = agg.Productive.Percent
		agg.WastePct = 100 - agg.ProductivePct
	}
	return agg
}
