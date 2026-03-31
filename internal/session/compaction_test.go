package session

import (
	"math"
	"testing"
)

// helper builds a minimal message slice with only the fields DetectCompactions reads.
func msgs(inputs ...int) []Message {
	out := make([]Message, len(inputs))
	for i, tok := range inputs {
		out[i] = Message{
			Role:        RoleAssistant,
			InputTokens: tok,
		}
	}
	return out
}

// msgsWithCache builds messages with both InputTokens and CacheReadTokens.
func msgsWithCache(pairs ...int) []Message {
	if len(pairs)%2 != 0 {
		panic("msgsWithCache requires even number of args (input, cache pairs)")
	}
	out := make([]Message, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out[i/2] = Message{
			Role:            RoleAssistant,
			InputTokens:     pairs[i],
			CacheReadTokens: pairs[i+1],
		}
	}
	return out
}

func TestDetectCompactions_noMessages(t *testing.T) {
	s := DetectCompactions(nil, 0)
	if s.TotalCompactions != 0 {
		t.Errorf("expected 0 compactions, got %d", s.TotalCompactions)
	}
	if s.Events != nil {
		t.Errorf("expected nil events, got %v", s.Events)
	}
}

func TestDetectCompactions_noCompaction(t *testing.T) {
	// Monotonically increasing tokens — no compaction.
	s := DetectCompactions(msgs(5000, 15000, 30000, 50000, 80000), 0)
	if s.TotalCompactions != 0 {
		t.Errorf("expected 0 compactions, got %d", s.TotalCompactions)
	}
}

func TestDetectCompactions_belowMinBaseline(t *testing.T) {
	// Big drop but baseline is below CompactionMinBaseline (10K).
	// 8000 → 2000 is a 75% drop, but baseline < 10K.
	s := DetectCompactions(msgs(8000, 2000, 5000), 0)
	if s.TotalCompactions != 0 {
		t.Errorf("expected 0 compactions for baseline < %d, got %d", CompactionMinBaseline, s.TotalCompactions)
	}
}

func TestDetectCompactions_singleCompaction(t *testing.T) {
	// 80K → 10K = 87.5% drop.
	m := msgs(5000, 20000, 50000, 80000, 10000, 25000, 40000)
	s := DetectCompactions(m, 0)

	if s.TotalCompactions != 1 {
		t.Fatalf("expected 1 compaction, got %d", s.TotalCompactions)
	}

	e := s.Events[0]
	if e.BeforeInputTokens != 80000 {
		t.Errorf("before tokens: want 80000, got %d", e.BeforeInputTokens)
	}
	if e.AfterInputTokens != 10000 {
		t.Errorf("after tokens: want 10000, got %d", e.AfterInputTokens)
	}
	if e.TokensLost != 70000 {
		t.Errorf("tokens lost: want 70000, got %d", e.TokensLost)
	}

	expectedDrop := (1.0 - 10000.0/80000.0) * 100 // 87.5
	if math.Abs(e.DropPercent-expectedDrop) > 0.1 {
		t.Errorf("drop percent: want ~%.1f, got %.1f", expectedDrop, e.DropPercent)
	}

	if s.TotalTokensLost != 70000 {
		t.Errorf("total tokens lost: want 70000, got %d", s.TotalTokensLost)
	}
	if s.AvgDropPercent != expectedDrop {
		t.Errorf("avg drop: want %.1f, got %.1f", expectedDrop, s.AvgDropPercent)
	}
}

func TestDetectCompactions_sawtooth(t *testing.T) {
	// Classic sawtooth: fill → compact → fill → compact → fill.
	// Cycle 1: 20K → 50K → 100K → 12K (88% drop)
	// Cycle 2: 12K → 40K → 90K → 8K  (91.1% drop)
	m := msgs(20000, 50000, 100000, 12000, 40000, 90000, 8000, 30000)
	s := DetectCompactions(m, 0)

	if s.TotalCompactions != 2 {
		t.Fatalf("expected 2 compactions (sawtooth), got %d", s.TotalCompactions)
	}
	if s.SawtoothCycles != 2 {
		t.Errorf("expected 2 sawtooth cycles, got %d", s.SawtoothCycles)
	}

	// First compaction: 100K → 12K.
	if s.Events[0].BeforeInputTokens != 100000 || s.Events[0].AfterInputTokens != 12000 {
		t.Errorf("event 0: want 100K→12K, got %d→%d", s.Events[0].BeforeInputTokens, s.Events[0].AfterInputTokens)
	}

	// Second compaction: 90K → 8K.
	if s.Events[1].BeforeInputTokens != 90000 || s.Events[1].AfterInputTokens != 8000 {
		t.Errorf("event 1: want 90K→8K, got %d→%d", s.Events[1].BeforeInputTokens, s.Events[1].AfterInputTokens)
	}

	// Total tokens lost: 88K + 82K = 170K.
	wantLost := (100000 - 12000) + (90000 - 8000)
	if s.TotalTokensLost != wantLost {
		t.Errorf("total tokens lost: want %d, got %d", wantLost, s.TotalTokensLost)
	}

	// Median drop should be average of two values (even count).
	drop1 := (1.0 - 12000.0/100000.0) * 100 // 88.0
	drop2 := (1.0 - 8000.0/90000.0) * 100   // 91.11
	wantMedian := (drop1 + drop2) / 2
	if math.Abs(s.MedianDropPercent-wantMedian) > 0.1 {
		t.Errorf("median drop: want ~%.1f, got %.1f", wantMedian, s.MedianDropPercent)
	}
}

func TestDetectCompactions_cacheInvalidation(t *testing.T) {
	// Pre-compaction: 80K input, 50K cache read.
	// Post-compaction: 10K input, 100 cache read → cache invalidated.
	m := msgsWithCache(
		20000, 10000,
		50000, 30000,
		80000, 50000, // before compaction: high cache
		10000, 100, // after compaction: cache wiped
		30000, 20000,
	)
	s := DetectCompactions(m, 0)

	if s.TotalCompactions != 1 {
		t.Fatalf("expected 1 compaction, got %d", s.TotalCompactions)
	}
	if !s.Events[0].CacheInvalidated {
		t.Error("expected CacheInvalidated = true (cache_read dropped from 50K to 100)")
	}
}

func TestDetectCompactions_cacheNotInvalidated(t *testing.T) {
	// Pre-compaction: low cache read → can't confirm invalidation.
	m := msgsWithCache(
		20000, 500, // low cache
		80000, 800, // still low cache
		10000, 100, // post compaction, but prevCacheRead was only 800 (< 1000 threshold)
	)
	s := DetectCompactions(m, 0)

	if s.TotalCompactions != 1 {
		t.Fatalf("expected 1 compaction, got %d", s.TotalCompactions)
	}
	if s.Events[0].CacheInvalidated {
		t.Error("expected CacheInvalidated = false (prev cache was below threshold)")
	}
}

func TestDetectCompactions_costEstimation(t *testing.T) {
	// $15/M tokens = $0.000015 per token.
	inputRate := 15.0 / 1_000_000 // 0.000015
	m := msgs(20000, 80000, 10000, 50000)
	s := DetectCompactions(m, inputRate)

	if s.TotalCompactions != 1 {
		t.Fatalf("expected 1 compaction, got %d", s.TotalCompactions)
	}

	// Tokens lost: 70K. Cost: 70000 * 0.000015 = $1.05.
	expectedCost := 70000.0 * inputRate
	if math.Abs(s.Events[0].RebuildCost-expectedCost) > 0.001 {
		t.Errorf("rebuild cost: want ~$%.4f, got $%.4f", expectedCost, s.Events[0].RebuildCost)
	}
	if math.Abs(s.TotalRebuildCost-expectedCost) > 0.001 {
		t.Errorf("total rebuild cost: want ~$%.4f, got $%.4f", expectedCost, s.TotalRebuildCost)
	}
}

func TestDetectCompactions_costZeroRate(t *testing.T) {
	// When inputRate is 0, cost should be 0.
	m := msgs(20000, 80000, 10000)
	s := DetectCompactions(m, 0)

	if s.TotalCompactions != 1 {
		t.Fatalf("expected 1 compaction, got %d", s.TotalCompactions)
	}
	if s.Events[0].RebuildCost != 0 {
		t.Errorf("rebuild cost should be 0 when rate is 0, got %f", s.Events[0].RebuildCost)
	}
	if s.TotalRebuildCost != 0 {
		t.Errorf("total rebuild cost should be 0, got %f", s.TotalRebuildCost)
	}
}

func TestDetectCompactions_skipsZeroTokenMessages(t *testing.T) {
	// Messages with InputTokens=0 are skipped (user messages without token data).
	m := []Message{
		{Role: RoleAssistant, InputTokens: 20000},
		{Role: RoleUser, InputTokens: 0}, // skipped
		{Role: RoleAssistant, InputTokens: 80000},
		{Role: RoleUser, InputTokens: 0},          // skipped
		{Role: RoleAssistant, InputTokens: 10000}, // compaction detected: 80K → 10K
	}
	s := DetectCompactions(m, 0)

	if s.TotalCompactions != 1 {
		t.Fatalf("expected 1 compaction, got %d", s.TotalCompactions)
	}
	if s.Events[0].BeforeInputTokens != 80000 {
		t.Errorf("should detect compaction from 80K, got before=%d", s.Events[0].BeforeInputTokens)
	}
}

func TestDetectCompactions_exactThreshold(t *testing.T) {
	// Exactly 50% drop should NOT trigger (threshold is strictly less than 0.50).
	// 100K → 50K = ratio 0.50, NOT < 0.50.
	m := msgs(20000, 100000, 50000)
	s := DetectCompactions(m, 0)

	if s.TotalCompactions != 0 {
		t.Errorf("exact 50%% drop should not trigger compaction, got %d events", s.TotalCompactions)
	}
}

func TestDetectCompactions_justBelowThreshold(t *testing.T) {
	// 100K → 49999 = ratio 0.49999, just below threshold.
	m := msgs(20000, 100000, 49999)
	s := DetectCompactions(m, 0)

	if s.TotalCompactions != 1 {
		t.Errorf("drop to 49999/100000 (ratio <0.50) should trigger, got %d", s.TotalCompactions)
	}
}

func TestDetectCompactions_messageIndices(t *testing.T) {
	// Verify BeforeMessageIdx and AfterMessageIdx are correct when some messages
	// are skipped (InputTokens=0).
	m := []Message{
		{Role: RoleUser, InputTokens: 0},          // idx 0 — skipped
		{Role: RoleAssistant, InputTokens: 20000}, // idx 1
		{Role: RoleUser, InputTokens: 0},          // idx 2 — skipped
		{Role: RoleAssistant, InputTokens: 80000}, // idx 3
		{Role: RoleUser, InputTokens: 0},          // idx 4 — skipped
		{Role: RoleAssistant, InputTokens: 10000}, // idx 5 — compaction
	}
	s := DetectCompactions(m, 0)

	if s.TotalCompactions != 1 {
		t.Fatalf("expected 1 compaction, got %d", s.TotalCompactions)
	}
	if s.Events[0].BeforeMessageIdx != 3 {
		t.Errorf("BeforeMessageIdx: want 3, got %d", s.Events[0].BeforeMessageIdx)
	}
	if s.Events[0].AfterMessageIdx != 5 {
		t.Errorf("AfterMessageIdx: want 5, got %d", s.Events[0].AfterMessageIdx)
	}
}

func TestDetectCompactions_fillAndRecoveryStats(t *testing.T) {
	// Sawtooth with known fill and recovery patterns.
	//
	// Messages: [20K, 50K, 80K, 10K, 30K, 60K, 90K, 8K, 25K]
	//   idx:       0     1     2    3     4     5     6    7    8
	//
	// Compaction 1: idx 2 (80K) → idx 3 (10K)
	//   Messages to fill: idx 2 - idx 0 = 2 (messages 0,1 built context before compaction)
	//   Recovery after: peak in [3..5] (before next compaction at idx 6) = 60K, recovery = 60K - 10K = 50K
	//
	// Compaction 2: idx 6 (90K) → idx 7 (8K)
	//   Messages to fill: idx 6 - idx 3 = 3 (messages 3,4,5 rebuilt context)
	//   Recovery after: peak in [7..8] = 25K, recovery = 25K - 8K = 17K
	//
	// AvgMessagesToFill = (2 + 3) / 2 = 2
	// AvgRecoveryTokens = (50000 + 17000) / 2 = 33500
	m := msgs(20000, 50000, 80000, 10000, 30000, 60000, 90000, 8000, 25000)
	s := DetectCompactions(m, 0)

	if s.TotalCompactions != 2 {
		t.Fatalf("expected 2 compactions, got %d", s.TotalCompactions)
	}
	if s.AvgMessagesToFill != 2 {
		t.Errorf("AvgMessagesToFill: want 2, got %d", s.AvgMessagesToFill)
	}
	if s.AvgRecoveryTokens != 33500 {
		t.Errorf("AvgRecoveryTokens: want 33500, got %d", s.AvgRecoveryTokens)
	}
}

func TestDetectCompactions_multipleCompactionCosts(t *testing.T) {
	inputRate := 10.0 / 1_000_000 // $10/M
	m := msgs(20000, 100000, 10000, 50000, 80000, 5000)
	s := DetectCompactions(m, inputRate)

	if s.TotalCompactions != 2 {
		t.Fatalf("expected 2 compactions, got %d", s.TotalCompactions)
	}

	// Event 1: 100K → 10K, lost 90K. Cost = 90000 * 0.00001 = $0.90.
	// Event 2: 80K → 5K, lost 75K. Cost = 75000 * 0.00001 = $0.75.
	// Total cost = $1.65.
	wantTotal := (90000.0 + 75000.0) * inputRate
	if math.Abs(s.TotalRebuildCost-wantTotal) > 0.001 {
		t.Errorf("total rebuild cost: want ~$%.4f, got $%.4f", wantTotal, s.TotalRebuildCost)
	}
}

func TestDetectCompactions_productionLikeScenario(t *testing.T) {
	// Simulates a real session: gradual fill to ~180K, compaction to ~15K, then rebuild.
	// Based on production data: median drop is ~92%.
	m := msgs(
		8000,  // system prompt + context
		12000, // first exchange
		20000, // second exchange
		35000, // big tool output
		50000,
		75000,
		100000,
		130000,
		160000,
		185000, // approaching 200K limit
		15000,  // COMPACTION: 92% drop
		25000,  // rebuilding
		40000,
		55000,
		70000,
	)
	s := DetectCompactions(m, 15.0/1_000_000) // Claude Sonnet pricing

	if s.TotalCompactions != 1 {
		t.Fatalf("expected 1 compaction, got %d", s.TotalCompactions)
	}

	e := s.Events[0]
	if e.TokensLost != 170000 {
		t.Errorf("tokens lost: want 170000, got %d", e.TokensLost)
	}

	// Drop percent should be ~91.9%.
	wantDrop := (1.0 - 15000.0/185000.0) * 100
	if math.Abs(e.DropPercent-wantDrop) > 0.1 {
		t.Errorf("drop percent: want ~%.1f%%, got %.1f%%", wantDrop, e.DropPercent)
	}

	// Rebuild cost: 170K tokens * $15/M = $2.55.
	wantCost := 170000.0 * 15.0 / 1_000_000
	if math.Abs(e.RebuildCost-wantCost) > 0.01 {
		t.Errorf("rebuild cost: want ~$%.4f, got $%.4f", wantCost, e.RebuildCost)
	}
}

func TestMedianFloat64_empty(t *testing.T) {
	m := medianFloat64(nil)
	if m != 0 {
		t.Errorf("median of empty: want 0, got %f", m)
	}
}

func TestMedianFloat64_odd(t *testing.T) {
	events := []CompactionEvent{
		{DropPercent: 80},
		{DropPercent: 90},
		{DropPercent: 95},
	}
	m := medianFloat64(events)
	if m != 90 {
		t.Errorf("median of [80,90,95]: want 90, got %f", m)
	}
}

func TestMedianFloat64_even(t *testing.T) {
	events := []CompactionEvent{
		{DropPercent: 80},
		{DropPercent: 90},
	}
	m := medianFloat64(events)
	if m != 85 {
		t.Errorf("median of [80,90]: want 85, got %f", m)
	}
}

func TestMedianFloat64_single(t *testing.T) {
	events := []CompactionEvent{{DropPercent: 92.5}}
	m := medianFloat64(events)
	if m != 92.5 {
		t.Errorf("median of [92.5]: want 92.5, got %f", m)
	}
}
