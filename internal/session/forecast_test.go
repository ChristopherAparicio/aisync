package session

import (
	"testing"
)

func TestForecastSaturation_EmptyInput(t *testing.T) {
	result := ForecastSaturation(nil)
	if result.SessionsWithForecast != 0 {
		t.Errorf("expected 0 sessions, got %d", result.SessionsWithForecast)
	}
	if len(result.Models) != 0 {
		t.Errorf("expected 0 models, got %d", len(result.Models))
	}
}

func TestForecastSaturation_SingleSession_NoCompaction(t *testing.T) {
	sessions := []SessionForecastInput{
		{
			Model:                "claude-opus-4",
			MaxInputTokens:       200000,
			MessageCount:         20,
			PeakInputTokens:      50000,
			MsgAtFirstCompaction: 0,
			TokenGrowthPerMsg:    2500,
		},
	}

	result := ForecastSaturation(sessions)
	if result.SessionsWithForecast != 1 {
		t.Fatalf("expected 1 session, got %d", result.SessionsWithForecast)
	}
	if result.SessionsWithCompacted != 0 {
		t.Errorf("expected 0 compacted, got %d", result.SessionsWithCompacted)
	}
	if result.AvgTokenGrowthPerMsg != 2500 {
		t.Errorf("expected growth 2500, got %d", result.AvgTokenGrowthPerMsg)
	}

	if len(result.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result.Models))
	}
	m := result.Models[0]
	if m.Model != "claude-opus-4" {
		t.Errorf("expected claude-opus-4, got %s", m.Model)
	}
	if m.PredictedMsgsTo80 != 64 { // 200000*0.8/2500 = 64
		t.Errorf("expected predicted msgs to 80%%: 64, got %d", m.PredictedMsgsTo80)
	}
	if m.PredictedMsgsTo100 != 80 { // 200000/2500 = 80
		t.Errorf("expected predicted msgs to 100%%: 80, got %d", m.PredictedMsgsTo100)
	}
}

func TestForecastSaturation_CompactedSessions(t *testing.T) {
	sessions := []SessionForecastInput{
		{
			Model: "claude-opus-4", MaxInputTokens: 200000, MessageCount: 50,
			PeakInputTokens: 180000, MsgAtFirstCompaction: 30, TokenGrowthPerMsg: 4000,
		},
		{
			Model: "claude-opus-4", MaxInputTokens: 200000, MessageCount: 80,
			PeakInputTokens: 195000, MsgAtFirstCompaction: 40, TokenGrowthPerMsg: 3500,
		},
		{
			Model: "claude-opus-4", MaxInputTokens: 200000, MessageCount: 20,
			PeakInputTokens: 60000, MsgAtFirstCompaction: 0, TokenGrowthPerMsg: 3000,
		},
	}

	result := ForecastSaturation(sessions)
	if result.SessionsWithForecast != 3 {
		t.Fatalf("expected 3 sessions, got %d", result.SessionsWithForecast)
	}
	if result.SessionsWithCompacted != 2 {
		t.Errorf("expected 2 compacted, got %d", result.SessionsWithCompacted)
	}
	if result.AvgMsgsToCompaction != 35 { // (30+40)/2 = 35
		t.Errorf("expected avg msgs to compaction 35, got %d", result.AvgMsgsToCompaction)
	}

	if len(result.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result.Models))
	}
	m := result.Models[0]
	if m.CompactedCount != 2 {
		t.Errorf("expected 2 compacted, got %d", m.CompactedCount)
	}
	if m.AvgMsgsToCompacted != 35 {
		t.Errorf("expected avg 35, got %d", m.AvgMsgsToCompacted)
	}
	if m.MedianMsgsToCompacted != 35 { // median of [30, 40] = 35
		t.Errorf("expected median 35, got %d", m.MedianMsgsToCompacted)
	}
}

func TestForecastSaturation_MultiModel(t *testing.T) {
	sessions := []SessionForecastInput{
		{Model: "claude-opus-4", MaxInputTokens: 200000, MessageCount: 50, PeakInputTokens: 160000, MsgAtFirstCompaction: 30, TokenGrowthPerMsg: 4000},
		{Model: "claude-sonnet-4", MaxInputTokens: 1000000, MessageCount: 100, PeakInputTokens: 300000, MsgAtFirstCompaction: 0, TokenGrowthPerMsg: 5000},
	}

	result := ForecastSaturation(sessions)
	if len(result.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(result.Models))
	}
	// Models sorted by compacted count desc — opus first (has compaction).
	if result.Models[0].Model != "claude-opus-4" {
		t.Errorf("expected opus first (more compactions), got %s", result.Models[0].Model)
	}
	if result.Models[1].Model != "claude-sonnet-4" {
		t.Errorf("expected sonnet second, got %s", result.Models[1].Model)
	}

	// Sonnet with 1M context at 5K/msg: predicted 160 msgs to 80%, 200 msgs to 100%.
	sonnet := result.Models[1]
	if sonnet.PredictedMsgsTo80 != 160 { // 1000000*0.8/5000 = 160
		t.Errorf("sonnet predicted to 80%%: expected 160, got %d", sonnet.PredictedMsgsTo80)
	}
	if sonnet.PredictedMsgsTo100 != 200 { // 1000000/5000 = 200
		t.Errorf("sonnet predicted to 100%%: expected 200, got %d", sonnet.PredictedMsgsTo100)
	}
}

func TestForecastSaturation_Histogram(t *testing.T) {
	sessions := []SessionForecastInput{
		{Model: "m", MaxInputTokens: 200000, MessageCount: 50, PeakInputTokens: 180000, MsgAtFirstCompaction: 5, TokenGrowthPerMsg: 4000},
		{Model: "m", MaxInputTokens: 200000, MessageCount: 60, PeakInputTokens: 190000, MsgAtFirstCompaction: 15, TokenGrowthPerMsg: 4000},
		{Model: "m", MaxInputTokens: 200000, MessageCount: 70, PeakInputTokens: 195000, MsgAtFirstCompaction: 25, TokenGrowthPerMsg: 4000},
		{Model: "m", MaxInputTokens: 200000, MessageCount: 80, PeakInputTokens: 180000, MsgAtFirstCompaction: 35, TokenGrowthPerMsg: 4000},
		{Model: "m", MaxInputTokens: 200000, MessageCount: 90, PeakInputTokens: 180000, MsgAtFirstCompaction: 55, TokenGrowthPerMsg: 4000},
	}

	result := ForecastSaturation(sessions)
	if len(result.CompactionHistogram) == 0 {
		t.Fatal("expected histogram data")
	}

	// Bucket "1-20" should have 2 (msgs 5 and 15).
	if result.CompactionHistogram[0].Label != "1-20" {
		t.Errorf("expected label 1-20, got %s", result.CompactionHistogram[0].Label)
	}
	if result.CompactionHistogram[0].Count != 2 {
		t.Errorf("expected 2 in first bucket, got %d", result.CompactionHistogram[0].Count)
	}
	// Bucket "21-40" should have 2 (msgs 25 and 35).
	if result.CompactionHistogram[1].Label != "21-40" {
		t.Errorf("expected label 21-40, got %s", result.CompactionHistogram[1].Label)
	}
	if result.CompactionHistogram[1].Count != 2 {
		t.Errorf("expected 2 in second bucket, got %d", result.CompactionHistogram[1].Count)
	}
	// Bucket "41-60" should have 1 (msg 55).
	if result.CompactionHistogram[2].Label != "41-60" {
		t.Errorf("expected label 41-60, got %s", result.CompactionHistogram[2].Label)
	}
	if result.CompactionHistogram[2].Count != 1 {
		t.Errorf("expected 1 in third bucket, got %d", result.CompactionHistogram[2].Count)
	}
}

func TestForecastSaturation_SkipSessionsWithoutEnoughData(t *testing.T) {
	sessions := []SessionForecastInput{
		{Model: "m", MaxInputTokens: 0, MessageCount: 50, PeakInputTokens: 10000},                               // no context window → skip
		{Model: "m", MaxInputTokens: 200000, MessageCount: 1, PeakInputTokens: 10000},                           // 1 message → skip
		{Model: "m", MaxInputTokens: 200000, MessageCount: 10, PeakInputTokens: 50000, TokenGrowthPerMsg: 5000}, // valid
	}

	result := ForecastSaturation(sessions)
	if result.SessionsWithForecast != 1 {
		t.Errorf("expected 1 valid session, got %d", result.SessionsWithForecast)
	}
}

func TestForecastSaturation_Recommendation(t *testing.T) {
	tests := []struct {
		name     string
		mf       ModelSaturationForecast
		contains string
	}{
		{
			name:     "majority compacted",
			mf:       ModelSaturationForecast{SessionCount: 10, CompactedCount: 6, MedianMsgsToCompacted: 30},
			contains: "Majority of sessions hit compaction",
		},
		{
			name:     "frequent compaction",
			mf:       ModelSaturationForecast{SessionCount: 10, CompactedCount: 3, PredictedMsgsTo80: 40},
			contains: "degraded zone around message 40",
		},
		{
			name:     "high utilization",
			mf:       ModelSaturationForecast{SessionCount: 10, CompactedCount: 0, AvgPeakUtilization: 75},
			contains: "Context usage is high",
		},
		{
			name:     "underutilized",
			mf:       ModelSaturationForecast{SessionCount: 10, CompactedCount: 0, AvgPeakUtilization: 15},
			contains: "underutilized",
		},
		{
			name:     "balanced",
			mf:       ModelSaturationForecast{SessionCount: 10, CompactedCount: 0, AvgPeakUtilization: 45},
			contains: "well-sized",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := forecastRecommendation(tc.mf)
			if rec == "" {
				t.Fatal("empty recommendation")
			}
			found := false
			for i := 0; i+len(tc.contains) <= len(rec); i++ {
				if rec[i:i+len(tc.contains)] == tc.contains {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("recommendation %q does not contain %q", rec, tc.contains)
			}
		})
	}
}

func TestForecastSaturation_PeakUtilizationCappedAt100(t *testing.T) {
	sessions := []SessionForecastInput{
		{Model: "m", MaxInputTokens: 100000, MessageCount: 50, PeakInputTokens: 120000, TokenGrowthPerMsg: 3000},
	}

	result := ForecastSaturation(sessions)
	if len(result.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result.Models))
	}
	if result.Models[0].AvgPeakUtilization > 100.01 { // float tolerance
		t.Errorf("expected capped at 100, got %.1f", result.Models[0].AvgPeakUtilization)
	}
}

func TestMeanInt(t *testing.T) {
	tests := []struct {
		input []int
		want  int
	}{
		{nil, 0},
		{[]int{10}, 10},
		{[]int{10, 20}, 15},
		{[]int{10, 20, 30}, 20},
	}
	for _, tc := range tests {
		got := meanInt(tc.input)
		if got != tc.want {
			t.Errorf("meanInt(%v) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestMedianInt(t *testing.T) {
	tests := []struct {
		input []int
		want  int
	}{
		{nil, 0},
		{[]int{10}, 10},
		{[]int{10, 20}, 15},
		{[]int{10, 20, 30}, 20},
		{[]int{1, 2, 3, 4}, 2},
	}
	for _, tc := range tests {
		got := medianInt(tc.input)
		if got != tc.want {
			t.Errorf("medianInt(%v) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestBuildCompactionHistogram(t *testing.T) {
	tests := []struct {
		name  string
		input []int
		wantN int // expected number of buckets
		first HistogramBucket
	}{
		{"empty", nil, 0, HistogramBucket{}},
		{"single", []int{5}, 1, HistogramBucket{Label: "1-20", Count: 1}},
		{"two_buckets", []int{5, 25}, 2, HistogramBucket{Label: "1-20", Count: 1}},
		{"all_in_one", []int{1, 10, 15, 20}, 1, HistogramBucket{Label: "1-20", Count: 4}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := buildCompactionHistogram(tc.input)
			if len(result) != tc.wantN {
				t.Errorf("expected %d buckets, got %d", tc.wantN, len(result))
			}
			if tc.wantN > 0 {
				if result[0].Label != tc.first.Label {
					t.Errorf("expected first label %q, got %q", tc.first.Label, result[0].Label)
				}
				if result[0].Count != tc.first.Count {
					t.Errorf("expected first count %d, got %d", tc.first.Count, result[0].Count)
				}
			}
		})
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{1000, "1000"},
		{-5, "-5"},
	}
	for _, tc := range tests {
		got := itoa(tc.input)
		if got != tc.want {
			t.Errorf("itoa(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
