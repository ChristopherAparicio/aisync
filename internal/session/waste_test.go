package session

import (
	"testing"
	"time"
)

func TestClassifyTokenWaste_EmptySession(t *testing.T) {
	result := ClassifyTokenWaste(nil, CompactionSummary{})
	if result.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0", result.TotalTokens)
	}
}

func TestClassifyTokenWaste_AllProductive(t *testing.T) {
	now := time.Now()
	msgs := []Message{
		{Role: RoleUser, InputTokens: 5000, OutputTokens: 0, Timestamp: now},
		{Role: RoleAssistant, InputTokens: 10000, OutputTokens: 2000, Timestamp: now.Add(30 * time.Second)},
		{Role: RoleUser, InputTokens: 12000, OutputTokens: 0, Timestamp: now.Add(60 * time.Second)},
		{Role: RoleAssistant, InputTokens: 15000, OutputTokens: 3000, Timestamp: now.Add(90 * time.Second),
			ToolCalls: []ToolCall{{Name: "bash", State: ToolStateCompleted}}},
	}

	result := ClassifyTokenWaste(msgs, CompactionSummary{})

	if result.Productive.Tokens == 0 {
		t.Error("Productive.Tokens should be > 0")
	}
	// First message(s) classified as idle context.
	if result.IdleContext.Tokens == 0 {
		t.Error("IdleContext.Tokens should be > 0 (system prompt)")
	}
	// No retries, no compaction, no cache misses.
	if result.Retry.Tokens != 0 {
		t.Errorf("Retry.Tokens = %d, want 0", result.Retry.Tokens)
	}
	if result.Compaction.Tokens != 0 {
		t.Errorf("Compaction.Tokens = %d, want 0", result.Compaction.Tokens)
	}
	if result.CacheMiss.Tokens != 0 {
		t.Errorf("CacheMiss.Tokens = %d, want 0", result.CacheMiss.Tokens)
	}
}

func TestClassifyTokenWaste_WithRetries(t *testing.T) {
	now := time.Now()
	msgs := []Message{
		{Role: RoleUser, InputTokens: 5000, Timestamp: now},
		{Role: RoleAssistant, InputTokens: 10000, OutputTokens: 1000, Timestamp: now.Add(30 * time.Second),
			ToolCalls: []ToolCall{{Name: "bash", State: ToolStateError}}},
		{Role: RoleAssistant, InputTokens: 11000, OutputTokens: 1500, Timestamp: now.Add(60 * time.Second),
			ToolCalls: []ToolCall{{Name: "bash", State: ToolStateCompleted}}}, // retry of bash
		{Role: RoleAssistant, InputTokens: 13000, OutputTokens: 2000, Timestamp: now.Add(90 * time.Second),
			ToolCalls: []ToolCall{{Name: "edit", State: ToolStateCompleted}}},
	}

	result := ClassifyTokenWaste(msgs, CompactionSummary{})

	// Message 2 (index 2) should be classified as retry (retrying bash after error).
	if result.Retry.Tokens == 0 {
		t.Error("Retry.Tokens should be > 0")
	}
	if result.Productive.Tokens == 0 {
		t.Error("Productive.Tokens should be > 0")
	}
}

func TestClassifyTokenWaste_WithCompaction(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, InputTokens: 5000},
		{Role: RoleAssistant, InputTokens: 100000, OutputTokens: 5000},
		{Role: RoleAssistant, InputTokens: 10000, OutputTokens: 3000}, // after compaction
	}

	compactions := CompactionSummary{
		TotalTokensLost: 85000,
	}

	result := ClassifyTokenWaste(msgs, compactions)

	if result.Compaction.Tokens != 85000 {
		t.Errorf("Compaction.Tokens = %d, want 85000", result.Compaction.Tokens)
	}
	if result.Compaction.Percent <= 0 {
		t.Errorf("Compaction.Percent = %.1f, want > 0", result.Compaction.Percent)
	}
}

func TestClassifyTokenWaste_WithCacheMiss(t *testing.T) {
	now := time.Now()
	msgs := []Message{
		{Role: RoleUser, InputTokens: 5000, Timestamp: now},
		{Role: RoleAssistant, InputTokens: 10000, OutputTokens: 2000, Timestamp: now.Add(30 * time.Second)},
		{Role: RoleUser, InputTokens: 12000, Timestamp: now.Add(10 * time.Minute)},                          // 10 min gap
		{Role: RoleAssistant, InputTokens: 50000, OutputTokens: 3000, Timestamp: now.Add(11 * time.Minute)}, // cache miss
	}

	result := ClassifyTokenWaste(msgs, CompactionSummary{})

	if result.CacheMiss.Tokens == 0 {
		t.Error("CacheMiss.Tokens should be > 0 (gap > 5 min)")
	}
	// The assistant's output after cache miss is still productive.
	if result.Productive.Tokens == 0 {
		t.Error("Productive.Tokens should be > 0 (output is still useful)")
	}
}

func TestClassifyTokenWaste_IdleContext(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, InputTokens: 8000, OutputTokens: 0}, // system prompt + first user msg
		{Role: RoleAssistant, InputTokens: 15000, OutputTokens: 3000},
	}

	result := ClassifyTokenWaste(msgs, CompactionSummary{})

	// First message's input tokens = idle context (system prompt, tool defs).
	if result.IdleContext.Tokens != 8000 {
		t.Errorf("IdleContext.Tokens = %d, want 8000", result.IdleContext.Tokens)
	}
}

func TestClassifyTokenWaste_Percentages(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, InputTokens: 1000, OutputTokens: 0},
		{Role: RoleAssistant, InputTokens: 5000, OutputTokens: 2000},
	}

	result := ClassifyTokenWaste(msgs, CompactionSummary{})

	total := result.Productive.Percent + result.Retry.Percent + result.Compaction.Percent +
		result.CacheMiss.Percent + result.IdleContext.Percent

	// Percentages should sum to ~100%.
	if total < 99.0 || total > 101.0 {
		t.Errorf("Percentages sum = %.1f, want ~100", total)
	}
	if result.ProductivePct != result.Productive.Percent {
		t.Errorf("ProductivePct = %.1f, want %.1f", result.ProductivePct, result.Productive.Percent)
	}
}

func TestAggregateWaste(t *testing.T) {
	b1 := TokenWasteBreakdown{
		TotalTokens: 10000,
		Productive:  WasteBucket{Tokens: 6000},
		Retry:       WasteBucket{Tokens: 2000},
		Compaction:  WasteBucket{Tokens: 1000},
		CacheMiss:   WasteBucket{Tokens: 500},
		IdleContext: WasteBucket{Tokens: 500},
	}
	b2 := TokenWasteBreakdown{
		TotalTokens: 20000,
		Productive:  WasteBucket{Tokens: 15000},
		Retry:       WasteBucket{Tokens: 3000},
		Compaction:  WasteBucket{Tokens: 0},
		CacheMiss:   WasteBucket{Tokens: 1000},
		IdleContext: WasteBucket{Tokens: 1000},
	}

	agg := AggregateWaste([]TokenWasteBreakdown{b1, b2})

	if agg.TotalTokens != 30000 {
		t.Errorf("TotalTokens = %d, want 30000", agg.TotalTokens)
	}
	if agg.Productive.Tokens != 21000 {
		t.Errorf("Productive.Tokens = %d, want 21000", agg.Productive.Tokens)
	}
	if agg.ProductivePct < 69 || agg.ProductivePct > 71 {
		t.Errorf("ProductivePct = %.1f, want ~70", agg.ProductivePct)
	}
}

func TestIdentifyRetryMessages(t *testing.T) {
	msgs := []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{{Name: "bash", State: ToolStateError}}},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{Name: "bash", State: ToolStateCompleted}}}, // retry
		{Role: RoleAssistant, ToolCalls: []ToolCall{{Name: "edit", State: ToolStateCompleted}}}, // not a retry
	}

	retries := identifyRetryMessages(msgs)
	if !retries[1] {
		t.Error("Message 1 should be marked as retry")
	}
	if retries[2] {
		t.Error("Message 2 should not be marked as retry")
	}
}

func TestIdentifyCacheMissMessages(t *testing.T) {
	now := time.Now()
	msgs := []Message{
		{Role: RoleUser, Timestamp: now},
		{Role: RoleAssistant, Timestamp: now.Add(30 * time.Second)},               // first assistant, no gap
		{Role: RoleUser, Timestamp: now.Add(8 * time.Minute)},                     // user after 7.5 min gap
		{Role: RoleAssistant, Timestamp: now.Add(8*time.Minute + 30*time.Second)}, // cache miss: 8 min since last assistant
	}

	misses := identifyCacheMissMessages(msgs)
	if misses[1] {
		t.Error("Message 1 should not be cache miss (first assistant)")
	}
	if !misses[3] {
		t.Error("Message 3 should be cache miss (8 min since last assistant response)")
	}
}

func TestDetectCacheMisses_NoMessages(t *testing.T) {
	timeline := DetectCacheMisses(nil)
	if timeline.TotalMisses != 0 {
		t.Errorf("expected 0 misses, got %d", timeline.TotalMisses)
	}
}

func TestDetectCacheMisses_NoMisses(t *testing.T) {
	now := time.Now()
	msgs := []Message{
		{Role: RoleAssistant, Timestamp: now, InputTokens: 1000},
		{Role: RoleAssistant, Timestamp: now.Add(2 * time.Minute), InputTokens: 2000},
		{Role: RoleAssistant, Timestamp: now.Add(4 * time.Minute), InputTokens: 3000},
	}

	timeline := DetectCacheMisses(msgs)
	if timeline.TotalMisses != 0 {
		t.Errorf("expected 0 misses (all within TTL), got %d", timeline.TotalMisses)
	}
}

func TestDetectCacheMisses_SingleMiss(t *testing.T) {
	now := time.Now()
	msgs := []Message{
		{Role: RoleAssistant, Timestamp: now, InputTokens: 5000},
		{Role: RoleUser, Timestamp: now.Add(10 * time.Minute)},
		{Role: RoleAssistant, Timestamp: now.Add(11 * time.Minute), InputTokens: 20000},
	}

	timeline := DetectCacheMisses(msgs)
	if timeline.TotalMisses != 1 {
		t.Fatalf("expected 1 miss, got %d", timeline.TotalMisses)
	}
	event := timeline.Events[0]
	if event.MessageIndex != 2 {
		t.Errorf("expected message index 2, got %d", event.MessageIndex)
	}
	if event.GapMinutes != 11 {
		t.Errorf("expected gap ~11 min, got %d", event.GapMinutes)
	}
	if event.InputTokens != 20000 {
		t.Errorf("expected 20000 input tokens, got %d", event.InputTokens)
	}
	if timeline.TotalWastedTokens != 20000 {
		t.Errorf("expected 20000 wasted tokens, got %d", timeline.TotalWastedTokens)
	}
	if timeline.LongestGapMins != 11 {
		t.Errorf("expected longest gap 11 min, got %d", timeline.LongestGapMins)
	}
}

func TestDetectCacheMisses_MultipleMisses(t *testing.T) {
	now := time.Now()
	msgs := []Message{
		{Role: RoleAssistant, Timestamp: now, InputTokens: 1000},
		{Role: RoleAssistant, Timestamp: now.Add(3 * time.Minute), InputTokens: 2000},   // within TTL
		{Role: RoleAssistant, Timestamp: now.Add(15 * time.Minute), InputTokens: 3000},  // miss (12 min gap)
		{Role: RoleAssistant, Timestamp: now.Add(18 * time.Minute), InputTokens: 4000},  // within TTL
		{Role: RoleAssistant, Timestamp: now.Add(60 * time.Minute), InputTokens: 10000}, // miss (42 min gap)
	}

	timeline := DetectCacheMisses(msgs)
	if timeline.TotalMisses != 2 {
		t.Fatalf("expected 2 misses, got %d", timeline.TotalMisses)
	}
	if timeline.TotalWastedTokens != 13000 { // 3000 + 10000
		t.Errorf("expected 13000 wasted tokens, got %d", timeline.TotalWastedTokens)
	}
	if timeline.LongestGapMins != 42 {
		t.Errorf("expected longest gap 42 min, got %d", timeline.LongestGapMins)
	}
}
