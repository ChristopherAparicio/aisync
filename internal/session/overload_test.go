package session

import "testing"

func TestDetectOverload_TooFewMessages(t *testing.T) {
	msgs := make([]Message, 5) // less than 10
	result := DetectOverload(msgs)
	if result.Verdict != "healthy" {
		t.Errorf("Verdict = %q, want healthy for short session", result.Verdict)
	}
	if result.HealthScore != 100 {
		t.Errorf("HealthScore = %d, want 100", result.HealthScore)
	}
}

func TestDetectOverload_HealthySession(t *testing.T) {
	// Build a session with consistent output/input ratio and no errors.
	msgs := make([]Message, 20)
	for i := range msgs {
		msgs[i] = Message{
			Role:         RoleAssistant,
			InputTokens:  10000 + i*500,
			OutputTokens: 1500 + i*75,
			ToolCalls: []ToolCall{
				{Name: "read_file", State: ToolStateCompleted},
			},
		}
	}

	result := DetectOverload(msgs)
	if result.Verdict != "healthy" {
		t.Errorf("Verdict = %q, want healthy", result.Verdict)
	}
	if result.HealthScore < 70 {
		t.Errorf("HealthScore = %d, want ≥70", result.HealthScore)
	}
	if result.IsOverloaded {
		t.Error("IsOverloaded should be false")
	}
}

func TestDetectOverload_DecliningOutputRatio(t *testing.T) {
	// First half: high output/input ratio. Second half: low.
	msgs := make([]Message, 20)
	for i := range msgs {
		input := 10000 + i*1000
		output := 2000 // constant first half
		if i >= 10 {
			output = 300 // dramatic decline in second half
		}
		msgs[i] = Message{
			Role:         RoleAssistant,
			InputTokens:  input,
			OutputTokens: output,
		}
	}

	result := DetectOverload(msgs)
	if result.OutputRatioDecay <= 30 {
		t.Errorf("OutputRatioDecay = %.1f%%, want >30%%", result.OutputRatioDecay)
	}
	if result.HealthScore >= 85 {
		t.Errorf("HealthScore = %d, want <85 for declining output", result.HealthScore)
	}
}

func TestDetectOverload_IncreasingErrors(t *testing.T) {
	// First half: no errors. Second half: 50% error rate.
	msgs := make([]Message, 20)
	for i := range msgs {
		state := ToolStateCompleted
		if i >= 10 && i%2 == 0 {
			state = ToolStateError
		}
		msgs[i] = Message{
			Role:         RoleAssistant,
			InputTokens:  10000,
			OutputTokens: 1000,
			ToolCalls: []ToolCall{
				{Name: "bash", State: state},
			},
		}
	}

	result := DetectOverload(msgs)
	if result.ErrorRateGrowth <= 0 {
		t.Errorf("ErrorRateGrowth = %.1f, want >0", result.ErrorRateGrowth)
	}
	if result.LateErrorRate <= 0 {
		t.Errorf("LateErrorRate = %.1f%%, want >0", result.LateErrorRate)
	}
}

func TestDetectOverload_ToolRetries(t *testing.T) {
	// Session with retry sequences (same tool called 3+ times consecutively).
	msgs := []Message{
		{Role: RoleAssistant, InputTokens: 10000, OutputTokens: 1000, ToolCalls: []ToolCall{{Name: "bash", State: ToolStateError}}},
		{Role: RoleAssistant, InputTokens: 11000, OutputTokens: 1000, ToolCalls: []ToolCall{{Name: "bash", State: ToolStateError}}},
		{Role: RoleAssistant, InputTokens: 12000, OutputTokens: 1000, ToolCalls: []ToolCall{{Name: "bash", State: ToolStateError}}},
		{Role: RoleAssistant, InputTokens: 13000, OutputTokens: 1000, ToolCalls: []ToolCall{{Name: "read_file", State: ToolStateCompleted}}},
		{Role: RoleAssistant, InputTokens: 14000, OutputTokens: 1000, ToolCalls: []ToolCall{{Name: "edit", State: ToolStateCompleted}}},
		{Role: RoleAssistant, InputTokens: 15000, OutputTokens: 1000, ToolCalls: []ToolCall{{Name: "edit", State: ToolStateError}}},
		{Role: RoleAssistant, InputTokens: 16000, OutputTokens: 1000, ToolCalls: []ToolCall{{Name: "edit", State: ToolStateError}}},
		{Role: RoleAssistant, InputTokens: 17000, OutputTokens: 1000, ToolCalls: []ToolCall{{Name: "edit", State: ToolStateError}}},
		{Role: RoleAssistant, InputTokens: 18000, OutputTokens: 1000, ToolCalls: []ToolCall{{Name: "edit", State: ToolStateError}}},
		{Role: RoleAssistant, InputTokens: 19000, OutputTokens: 1000, ToolCalls: []ToolCall{{Name: "bash", State: ToolStateCompleted}}},
	}

	result := DetectOverload(msgs)
	if result.RetryCount < 2 {
		t.Errorf("RetryCount = %d, want ≥2 (bash 3x + edit 4x)", result.RetryCount)
	}
}

func TestDetectOverload_Overloaded(t *testing.T) {
	// Combine all bad signals: declining output, increasing errors, retries.
	msgs := make([]Message, 20)
	for i := range msgs {
		input := 10000 + i*2000
		output := 2000
		if i >= 10 {
			output = 200 // dramatic decline
		}
		state := ToolStateCompleted
		if i >= 10 {
			state = ToolStateError
		}
		msgs[i] = Message{
			Role:         RoleAssistant,
			InputTokens:  input,
			OutputTokens: output,
			ToolCalls: []ToolCall{
				{Name: "bash", State: state},
			},
		}
	}

	result := DetectOverload(msgs)
	if result.Verdict != "overloaded" {
		t.Errorf("Verdict = %q, want overloaded (score=%d)", result.Verdict, result.HealthScore)
	}
	if !result.IsOverloaded {
		t.Error("IsOverloaded should be true")
	}
	if result.HealthScore >= 40 {
		t.Errorf("HealthScore = %d, want <40 for overloaded session", result.HealthScore)
	}
}

func TestDetectOverload_InflectionPoint(t *testing.T) {
	// Build a session where output suddenly drops at message 12.
	msgs := make([]Message, 20)
	for i := range msgs {
		input := 10000 + i*1000
		output := 2000
		if i >= 12 {
			output = 100 // sudden drop
		}
		msgs[i] = Message{
			Role:         RoleAssistant,
			InputTokens:  input,
			OutputTokens: output,
		}
	}

	result := DetectOverload(msgs)
	if result.InflectionAt == 0 {
		t.Error("InflectionAt = 0, want non-zero for declining session")
	}
	// The inflection should be detected before or around the actual drop point.
	// The sliding window (5-msg) may detect the decline a few messages early.
	if result.InflectionAt < 4 || result.InflectionAt > 16 {
		t.Errorf("InflectionAt = %d, want between 4 and 16", result.InflectionAt)
	}
}

func TestDetectToolRetries(t *testing.T) {
	tests := []struct {
		name string
		msgs []Message
		want int
	}{
		{
			name: "no retries",
			msgs: []Message{
				{ToolCalls: []ToolCall{{Name: "a"}, {Name: "b"}, {Name: "c"}}},
			},
			want: 0,
		},
		{
			name: "exactly 3 same",
			msgs: []Message{
				{ToolCalls: []ToolCall{{Name: "a"}, {Name: "a"}, {Name: "a"}}},
			},
			want: 1,
		},
		{
			name: "4 same (still 1 retry sequence)",
			msgs: []Message{
				{ToolCalls: []ToolCall{{Name: "x"}, {Name: "x"}, {Name: "x"}, {Name: "x"}}},
			},
			want: 1,
		},
		{
			name: "across messages",
			msgs: []Message{
				{ToolCalls: []ToolCall{{Name: "a"}}},
				{ToolCalls: []ToolCall{{Name: "a"}}},
				{ToolCalls: []ToolCall{{Name: "a"}}},
			},
			want: 1,
		},
		{
			name: "two separate retry sequences",
			msgs: []Message{
				{ToolCalls: []ToolCall{{Name: "a"}, {Name: "a"}, {Name: "a"}}},
				{ToolCalls: []ToolCall{{Name: "b"}, {Name: "b"}, {Name: "b"}}},
			},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectToolRetries(tt.msgs)
			if got != tt.want {
				t.Errorf("detectToolRetries() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildOverloadReason(t *testing.T) {
	tests := []struct {
		name         string
		analysis     OverloadAnalysis
		wantContains string
	}{
		{
			name:         "healthy",
			analysis:     OverloadAnalysis{Verdict: "healthy"},
			wantContains: "stable",
		},
		{
			name:         "declining output",
			analysis:     OverloadAnalysis{Verdict: "declining", OutputRatioDecay: 25},
			wantContains: "output productivity declined",
		},
		{
			name:         "error growth",
			analysis:     OverloadAnalysis{Verdict: "declining", ErrorRateGrowth: 12},
			wantContains: "error rate increased",
		},
		{
			name:         "retries",
			analysis:     OverloadAnalysis{Verdict: "declining", RetryCount: 3},
			wantContains: "retry sequences",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := buildOverloadReason(tt.analysis)
			if reason == "" {
				t.Error("reason should not be empty")
			}
			if tt.wantContains != "" {
				found := false
				for i := 0; i <= len(reason)-len(tt.wantContains); i++ {
					if reason[i:i+len(tt.wantContains)] == tt.wantContains {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("reason = %q, want to contain %q", reason, tt.wantContains)
				}
			}
		})
	}
}
