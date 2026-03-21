package analysis

import (
	"testing"
	"time"
)

func TestTriggerValid(t *testing.T) {
	tests := []struct {
		trigger Trigger
		want    bool
	}{
		{TriggerAuto, true},
		{TriggerManual, true},
		{"unknown", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := tt.trigger.Valid(); got != tt.want {
			t.Errorf("Trigger(%q).Valid() = %v, want %v", tt.trigger, got, tt.want)
		}
	}
}

func TestAdapterNameValid(t *testing.T) {
	tests := []struct {
		adapter AdapterName
		want    bool
	}{
		{AdapterLLM, true},
		{AdapterOpenCode, true},
		{"gpt", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := tt.adapter.Valid(); got != tt.want {
			t.Errorf("AdapterName(%q).Valid() = %v, want %v", tt.adapter, got, tt.want)
		}
	}
}

func TestSeverityValid(t *testing.T) {
	tests := []struct {
		severity Severity
		want     bool
	}{
		{SeverityLow, true},
		{SeverityMedium, true},
		{SeverityHigh, true},
		{"critical", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := tt.severity.Valid(); got != tt.want {
			t.Errorf("Severity(%q).Valid() = %v, want %v", tt.severity, got, tt.want)
		}
	}
}

func TestRecommendationCategoryValid(t *testing.T) {
	tests := []struct {
		cat  RecommendationCategory
		want bool
	}{
		{CategorySkill, true},
		{CategoryConfig, true},
		{CategoryWorkflow, true},
		{CategoryTool, true},
		{"other", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := tt.cat.Valid(); got != tt.want {
			t.Errorf("RecommendationCategory(%q).Valid() = %v, want %v", tt.cat, got, tt.want)
		}
	}
}

func TestSessionAnalysisOK(t *testing.T) {
	a := &SessionAnalysis{ID: "1", SessionID: "s1", CreatedAt: time.Now()}
	if !a.OK() {
		t.Error("expected OK() = true for analysis without error")
	}

	a.Error = "something failed"
	if a.OK() {
		t.Error("expected OK() = false for analysis with error")
	}
}

func TestAnalysisReportValidate(t *testing.T) {
	tests := []struct {
		name    string
		report  AnalysisReport
		wantErr bool
	}{
		{
			name: "valid minimal report",
			report: AnalysisReport{
				Score:   75,
				Summary: "Session was efficient overall.",
			},
			wantErr: false,
		},
		{
			name: "valid full report",
			report: AnalysisReport{
				Score:   42,
				Summary: "Several issues detected.",
				Problems: []Problem{
					{Severity: SeverityHigh, Description: "Too many retries on file reads"},
					{Severity: SeverityLow, Description: "Unused tool calls", ToolName: "grep"},
				},
				Recommendations: []Recommendation{
					{Category: CategorySkill, Title: "Create file-reader skill", Description: "Batch file reads", Priority: 1},
					{Category: CategoryConfig, Title: "Increase timeout", Description: "Reduce retries", Priority: 2},
				},
				SkillSuggestions: []SkillSuggestion{
					{Name: "batch-reader", Description: "Read multiple files at once"},
				},
			},
			wantErr: false,
		},
		{
			name: "score too low",
			report: AnalysisReport{
				Score:   -1,
				Summary: "Bad score.",
			},
			wantErr: true,
		},
		{
			name: "score too high",
			report: AnalysisReport{
				Score:   101,
				Summary: "Bad score.",
			},
			wantErr: true,
		},
		{
			name: "empty summary",
			report: AnalysisReport{
				Score:   50,
				Summary: "",
			},
			wantErr: true,
		},
		{
			name: "invalid problem severity",
			report: AnalysisReport{
				Score:   50,
				Summary: "OK.",
				Problems: []Problem{
					{Severity: "critical", Description: "Something"},
				},
			},
			wantErr: true,
		},
		{
			name: "empty problem description",
			report: AnalysisReport{
				Score:   50,
				Summary: "OK.",
				Problems: []Problem{
					{Severity: SeverityHigh, Description: ""},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid recommendation category",
			report: AnalysisReport{
				Score:   50,
				Summary: "OK.",
				Recommendations: []Recommendation{
					{Category: "other", Title: "Do something", Description: "Details", Priority: 1},
				},
			},
			wantErr: true,
		},
		{
			name: "empty recommendation title",
			report: AnalysisReport{
				Score:   50,
				Summary: "OK.",
				Recommendations: []Recommendation{
					{Category: CategorySkill, Title: "", Description: "Details", Priority: 1},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.report.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnumStrings(t *testing.T) {
	// Verify String() returns the underlying value for all enum types.
	if TriggerAuto.String() != "auto" {
		t.Errorf("TriggerAuto.String() = %q", TriggerAuto.String())
	}
	if AdapterLLM.String() != "llm" {
		t.Errorf("AdapterLLM.String() = %q", AdapterLLM.String())
	}
	if SeverityHigh.String() != "high" {
		t.Errorf("SeverityHigh.String() = %q", SeverityHigh.String())
	}
	if CategorySkill.String() != "skill" {
		t.Errorf("CategorySkill.String() = %q", CategorySkill.String())
	}
}
