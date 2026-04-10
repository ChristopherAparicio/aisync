package diagnostic

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestCompareTrend_InsufficientBaseline(t *testing.T) {
	report := &InspectReport{
		Tokens: &TokenSection{Input: 100_000, EstCost: 1.50},
	}

	// 0 rows
	if got := CompareTrend(report, nil); got != nil {
		t.Fatal("expected nil for empty history")
	}

	// 2 rows (< 3 minimum)
	history := []session.AnalyticsRow{
		makeRow(50_000, 0.50, 0, 0, 0, 0, time.Now().Add(-24*time.Hour)),
		makeRow(60_000, 0.60, 0, 0, 0, 0, time.Now()),
	}
	if got := CompareTrend(report, history); got != nil {
		t.Fatal("expected nil for 2-session history")
	}
}

func TestCompareTrend_BasicMetrics(t *testing.T) {
	report := &InspectReport{
		Messages: 40,
		Tokens: &TokenSection{
			Input:     200_000,
			CacheRead: 80_000,
			CachePct:  40.0,
			EstCost:   3.00,
		},
		Compaction: &CompactionSection{Count: 4},
		ToolErrors: &ToolErrorSection{
			TotalToolCalls: 50,
			ErrorCount:     5,
		},
	}

	now := time.Now()
	history := []session.AnalyticsRow{
		makeRow(100_000, 1.00, 2, 40_000, 5, 20, now.Add(-72*time.Hour)),
		makeRow(90_000, 0.90, 1, 35_000, 3, 18, now.Add(-48*time.Hour)),
		makeRow(110_000, 1.10, 3, 45_000, 4, 22, now.Add(-24*time.Hour)),
	}
	// Add tool call counts for error rate baseline
	for i := range history {
		history[i].ToolCallCount = 50
	}

	tc := CompareTrend(report, history)
	if tc == nil {
		t.Fatal("expected non-nil trend comparison")
	}

	if tc.BaselineSessions != 3 {
		t.Errorf("BaselineSessions = %d, want 3", tc.BaselineSessions)
	}
	if tc.BaselineDays < 3 {
		t.Errorf("BaselineDays = %d, want >= 3", tc.BaselineDays)
	}

	// Should have at least 6 metrics (cost, compactions, input tokens, cache %, error rate, messages)
	if len(tc.Metrics) < 6 {
		t.Errorf("got %d metrics, want >= 6", len(tc.Metrics))
	}

	// Verify cost metric
	costMetric := findMetric(tc.Metrics, "estimated_cost")
	if costMetric == nil {
		t.Fatal("missing estimated_cost metric")
	}
	if costMetric.Current != 3.0 {
		t.Errorf("cost current = %f, want 3.0", costMetric.Current)
	}
	// Baseline avg: (1.0 + 0.9 + 1.1) / 3 = 1.0
	if costMetric.Baseline != 1.0 {
		t.Errorf("cost baseline = %f, want 1.0", costMetric.Baseline)
	}
	if costMetric.Ratio != 3.0 {
		t.Errorf("cost ratio = %f, want 3.0", costMetric.Ratio)
	}
	// 3x is >= 2x threshold, should be anomaly
	if !costMetric.IsAnomaly {
		t.Error("cost should be flagged as anomaly (3x >= 2x threshold)")
	}

	// Verify compaction metric
	compMetric := findMetric(tc.Metrics, "compaction_count")
	if compMetric == nil {
		t.Fatal("missing compaction_count metric")
	}
	if compMetric.Current != 4.0 {
		t.Errorf("compaction current = %f, want 4.0", compMetric.Current)
	}
	// Baseline avg: (2+1+3)/3 = 2.0
	if compMetric.Baseline != 2.0 {
		t.Errorf("compaction baseline = %f, want 2.0", compMetric.Baseline)
	}
	if compMetric.Ratio != 2.0 {
		t.Errorf("compaction ratio = %f, want 2.0", compMetric.Ratio)
	}
	// 2.0x >= 1.5x threshold → anomaly
	if !compMetric.IsAnomaly {
		t.Error("compaction should be flagged as anomaly (2x >= 1.5x threshold)")
	}
}

func TestCompareTrend_NoAnomalies(t *testing.T) {
	report := &InspectReport{
		Messages: 20,
		Tokens: &TokenSection{
			Input:   100_000,
			EstCost: 1.00,
		},
		Compaction: &CompactionSection{Count: 2},
		ToolErrors: &ToolErrorSection{
			TotalToolCalls: 50,
			ErrorCount:     2,
		},
	}

	now := time.Now()
	history := []session.AnalyticsRow{
		makeRow(100_000, 1.00, 2, 0, 2, 20, now.Add(-72*time.Hour)),
		makeRow(100_000, 1.00, 2, 0, 2, 20, now.Add(-48*time.Hour)),
		makeRow(100_000, 1.00, 2, 0, 2, 20, now.Add(-24*time.Hour)),
	}
	for i := range history {
		history[i].ToolCallCount = 50
	}

	tc := CompareTrend(report, history)
	if tc == nil {
		t.Fatal("expected non-nil trend comparison")
	}

	if len(tc.Anomalies) != 0 {
		t.Errorf("expected 0 anomalies, got %d", len(tc.Anomalies))
		for _, a := range tc.Anomalies {
			t.Logf("  anomaly: %s ratio=%.2f", a.Name, a.Ratio)
		}
	}
}

func TestCompareTrend_CacheNotAnomaly(t *testing.T) {
	// Cache read % should NEVER be flagged as anomaly (higher is better)
	report := &InspectReport{
		Messages: 20,
		Tokens: &TokenSection{
			Input:     100_000,
			CacheRead: 90_000,
			CachePct:  90.0,
			EstCost:   1.00,
		},
		Compaction: &CompactionSection{Count: 0},
		ToolErrors: &ToolErrorSection{TotalToolCalls: 10, ErrorCount: 0},
	}

	now := time.Now()
	history := []session.AnalyticsRow{
		makeRow(100_000, 1.00, 0, 10_000, 0, 20, now.Add(-72*time.Hour)),
		makeRow(100_000, 1.00, 0, 10_000, 0, 20, now.Add(-48*time.Hour)),
		makeRow(100_000, 1.00, 0, 10_000, 0, 20, now.Add(-24*time.Hour)),
	}
	for i := range history {
		history[i].ToolCallCount = 10
	}

	tc := CompareTrend(report, history)
	if tc == nil {
		t.Fatal("expected non-nil trend comparison")
	}

	cacheMetric := findMetric(tc.Metrics, "cache_read_pct")
	if cacheMetric == nil {
		t.Fatal("missing cache_read_pct metric")
	}
	// Even though cache_read_pct is 9x above baseline, it should not be an anomaly
	if cacheMetric.IsAnomaly {
		t.Error("cache_read_pct should never be flagged as anomaly (higher is better)")
	}
}

func TestCompareTrend_NarrativeFormat(t *testing.T) {
	report := &InspectReport{
		Messages:   20,
		Tokens:     &TokenSection{Input: 200_000, EstCost: 2.00},
		Compaction: &CompactionSection{Count: 0},
		ToolErrors: &ToolErrorSection{TotalToolCalls: 10, ErrorCount: 0},
	}

	now := time.Now()
	history := []session.AnalyticsRow{
		makeRow(100_000, 1.00, 0, 0, 0, 20, now.Add(-72*time.Hour)),
		makeRow(100_000, 1.00, 0, 0, 0, 20, now.Add(-48*time.Hour)),
		makeRow(100_000, 1.00, 0, 0, 0, 20, now.Add(-24*time.Hour)),
	}
	for i := range history {
		history[i].ToolCallCount = 10
	}

	tc := CompareTrend(report, history)
	if tc == nil {
		t.Fatal("expected non-nil trend comparison")
	}

	costMetric := findMetric(tc.Metrics, "estimated_cost")
	if costMetric == nil {
		t.Fatal("missing estimated_cost metric")
	}

	// Narrative should contain factual comparison, not advice
	if costMetric.Narrative == "" {
		t.Error("narrative should not be empty")
	}
	// Should contain "above baseline" since 2x > 1.05
	if !containsStr(costMetric.Narrative, "above baseline") {
		t.Errorf("narrative should mention 'above baseline', got: %s", costMetric.Narrative)
	}
}

func TestFmtMetricValue(t *testing.T) {
	tests := []struct {
		v    float64
		unit string
		want string
	}{
		{1.50, "USD", "$1.50"},
		{1_500_000, "tokens", "1.5M"},
		{1_500, "tokens", "1.5K"},
		{500, "tokens", "500"},
		{45.3, "pct", "45.3%"},
		{0.123, "ratio", "0.123"},
		{42, "count", "42"},
	}

	for _, tt := range tests {
		got := fmtMetricValue(tt.v, tt.unit)
		if got != tt.want {
			t.Errorf("fmtMetricValue(%f, %q) = %q, want %q", tt.v, tt.unit, got, tt.want)
		}
	}
}

func TestRoundTo(t *testing.T) {
	tests := []struct {
		v    float64
		n    int
		want float64
	}{
		{1.2345, 2, 1.23},
		{1.2355, 2, 1.24},
		{0.0, 4, 0.0},
		{100.0, 0, 100.0},
	}

	for _, tt := range tests {
		got := roundTo(tt.v, tt.n)
		if got != tt.want {
			t.Errorf("roundTo(%f, %d) = %f, want %f", tt.v, tt.n, got, tt.want)
		}
	}
}

// ── Helpers ──

func makeRow(inputTokens int, cost float64, compactionCount int, cacheReadTokens int, errorCount int, messageCount int, createdAt time.Time) session.AnalyticsRow {
	return session.AnalyticsRow{
		Analytics: session.Analytics{
			InputTokens:     inputTokens,
			EstimatedCost:   cost,
			CompactionCount: compactionCount,
			CacheReadTokens: cacheReadTokens,
		},
		CreatedAt:    createdAt,
		MessageCount: messageCount,
		ErrorCount:   errorCount,
	}
}

func findMetric(metrics []TrendMetric, name string) *TrendMetric {
	for i := range metrics {
		if metrics[i].Name == name {
			return &metrics[i]
		}
	}
	return nil
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
