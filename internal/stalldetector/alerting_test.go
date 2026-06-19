package stalldetector

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestEvaluate_NoThresholds_NoAlert(t *testing.T) {
	stats := &session.StallStats{TotalCount: 100, LiveCount: 50, CostLostUSD: 999}
	dec := Evaluate(AlertThresholds{}, stats, stats)
	if dec != nil {
		t.Fatalf("expected nil decision when no thresholds set, got %+v", dec)
	}
}

func TestEvaluate_BelowAllThresholds_NoAlert(t *testing.T) {
	live := &session.StallStats{LiveCount: 2}
	recent := &session.StallStats{TotalCount: 3, CostLostUSD: 0.5}
	dec := Evaluate(AlertThresholds{
		LiveCount:    5,
		NewStalls24h: 10,
		CostLost24h:  1.0,
	}, live, recent)
	if dec != nil {
		t.Fatalf("expected nil decision below thresholds, got %+v", dec)
	}
}

func TestEvaluate_LiveCount_TriggersWarning(t *testing.T) {
	live := &session.StallStats{LiveCount: 7}
	recent := &session.StallStats{}
	dec := Evaluate(AlertThresholds{LiveCount: 5}, live, recent)
	if dec == nil {
		t.Fatal("expected decision, got nil")
	}
	if dec.Severity != AlertSeverityWarning {
		t.Errorf("severity = %q, want warning", dec.Severity)
	}
	if len(dec.Reasons) != 1 {
		t.Errorf("expected 1 reason, got %d: %v", len(dec.Reasons), dec.Reasons)
	}
	if dec.LiveCount != 7 {
		t.Errorf("LiveCount = %d, want 7", dec.LiveCount)
	}
}

func TestEvaluate_LiveCount_3xEscalatesToCritical(t *testing.T) {
	live := &session.StallStats{LiveCount: 15} // 3x threshold of 5
	dec := Evaluate(AlertThresholds{LiveCount: 5}, live, nil)
	if dec == nil || dec.Severity != AlertSeverityCritical {
		t.Fatalf("expected critical severity, got %+v", dec)
	}
}

func TestEvaluate_CostThreshold_FiresWithUSDReason(t *testing.T) {
	recent := &session.StallStats{TotalCount: 1, CostLostUSD: 2.50}
	dec := Evaluate(AlertThresholds{CostLost24h: 1.0}, nil, recent)
	if dec == nil {
		t.Fatal("expected decision, got nil")
	}
	if len(dec.Reasons) != 1 {
		t.Fatalf("expected 1 reason, got %v", dec.Reasons)
	}
	// Reason should mention USD amount.
	if !containsAll(dec.Reasons[0], "cost lost (24h)", "$2.50", "$1.00") {
		t.Errorf("reason missing expected tokens: %q", dec.Reasons[0])
	}
	if dec.CostLost24h != 2.50 {
		t.Errorf("CostLost24h = %v, want 2.50", dec.CostLost24h)
	}
}

func TestEvaluate_MultipleRules_AccumulateReasons(t *testing.T) {
	live := &session.StallStats{LiveCount: 6}
	recent := &session.StallStats{TotalCount: 20, CostLostUSD: 5.0}
	dec := Evaluate(AlertThresholds{
		LiveCount:    5,
		NewStalls24h: 10,
		CostLost24h:  1.0,
	}, live, recent)
	if dec == nil {
		t.Fatal("expected decision, got nil")
	}
	if len(dec.Reasons) != 3 {
		t.Errorf("expected 3 reasons, got %d: %v", len(dec.Reasons), dec.Reasons)
	}
}

func TestEvaluate_TopRootCauseAndProvider(t *testing.T) {
	recent := &session.StallStats{
		TotalCount: 10,
		ByRootCause: map[session.StallRootCause]session.StallStatsRow{
			session.StallRootCauseStreamStall:   {Count: 8},
			session.StallRootCauseAborted:       {Count: 2},
			session.StallRootCauseRateLimit429:  {Count: 1},
		},
		ByProvider: map[string]session.StallStatsRow{
			"anthropic": {Count: 6},
			"openai":    {Count: 4},
		},
	}
	dec := Evaluate(AlertThresholds{NewStalls24h: 5}, nil, recent)
	if dec == nil {
		t.Fatal("expected decision, got nil")
	}
	if dec.TopRootCause != session.StallRootCauseStreamStall {
		t.Errorf("TopRootCause = %q, want stream_stall", dec.TopRootCause)
	}
	if dec.TopProvider != "anthropic" {
		t.Errorf("TopProvider = %q, want anthropic", dec.TopProvider)
	}
	if dec.RootCauseCounts[session.StallRootCauseStreamStall] != 8 {
		t.Errorf("RootCauseCounts missing stream_stall=8: %v", dec.RootCauseCounts)
	}
}

func TestEvaluate_NilStats_NoAlert(t *testing.T) {
	dec := Evaluate(AlertThresholds{LiveCount: 5}, nil, nil)
	if dec != nil {
		t.Fatalf("expected nil decision with nil stats, got %+v", dec)
	}
}

func TestTopRootCause_DeterministicOnTies(t *testing.T) {
	m := map[session.StallRootCause]session.StallStatsRow{
		session.StallRootCauseStreamStall:  {Count: 3},
		session.StallRootCauseAborted:      {Count: 3},
		session.StallRootCauseRateLimit429: {Count: 3},
	}
	got := topRootCause(m)
	// Alphabetical tiebreak: "aborted" < "rate_limit_429" < "stream_stall".
	if got != session.StallRootCauseAborted {
		t.Errorf("topRootCause(tie) = %q, want aborted", got)
	}
}

func TestItoaFtoa(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"}, {1, "1"}, {10, "10"}, {-5, "-5"}, {123, "123"},
	}
	for _, c := range cases {
		if got := itoa(c.in); got != c.want {
			t.Errorf("itoa(%d) = %q, want %q", c.in, got, c.want)
		}
	}
	fcases := []struct {
		in   float64
		want string
	}{
		{0, "0.00"}, {1.5, "1.50"}, {2.50, "2.50"}, {10.07, "10.07"},
	}
	for _, c := range fcases {
		if got := ftoa(c.in); got != c.want {
			t.Errorf("ftoa(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// containsAll returns true if s contains all of substrs.
func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !indexOf(s, sub) {
			return false
		}
	}
	return true
}

func indexOf(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
