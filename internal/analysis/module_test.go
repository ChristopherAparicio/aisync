package analysis

import (
	"encoding/json"
	"testing"
)

func TestAllModules(t *testing.T) {
	modules := AllModules()
	if len(modules) < 2 {
		t.Fatalf("AllModules() returned %d, want at least 2", len(modules))
	}
	if modules[0] != ModuleSessionQuality {
		t.Errorf("first module = %q, want %q", modules[0], ModuleSessionQuality)
	}
	if modules[1] != ModuleToolEfficiency {
		t.Errorf("second module = %q, want %q", modules[1], ModuleToolEfficiency)
	}
}

func TestModuleRegistry(t *testing.T) {
	registry := ModuleRegistry()
	if len(registry) < 2 {
		t.Fatalf("ModuleRegistry() returned %d, want at least 2", len(registry))
	}

	// Verify session_quality module.
	found := false
	for _, info := range registry {
		if info.Name == ModuleSessionQuality {
			found = true
			if info.Label == "" {
				t.Error("session_quality module has empty label")
			}
			if !info.RequiresLLM {
				t.Error("session_quality should require LLM")
			}
		}
	}
	if !found {
		t.Error("ModuleRegistry missing session_quality")
	}

	// Verify tool_efficiency module.
	found = false
	for _, info := range registry {
		if info.Name == ModuleToolEfficiency {
			found = true
			if info.Label == "" {
				t.Error("tool_efficiency module has empty label")
			}
		}
	}
	if !found {
		t.Error("ModuleRegistry missing tool_efficiency")
	}
}

func TestToolEfficiencyReportJSON(t *testing.T) {
	report := ToolEfficiencyReport{
		Summary:        "Good tool usage overall",
		OverallScore:   85,
		UsefulCalls:    10,
		RedundantCalls: 2,
		Patterns:       []string{"retry loop in bash"},
		ToolEvaluations: []ToolEvaluation{
			{
				Index:        0,
				ToolName:     "read",
				Usefulness:   "useful",
				Reason:       "Read config file needed",
				InputTokens:  15,
				OutputTokens: 200,
			},
			{
				Index:        1,
				ToolName:     "bash",
				Usefulness:   "redundant",
				Reason:       "Same command run twice",
				InputTokens:  30,
				OutputTokens: 50,
			},
		},
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ToolEfficiencyReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.OverallScore != 85 {
		t.Errorf("score = %d, want 85", decoded.OverallScore)
	}
	if decoded.UsefulCalls != 10 {
		t.Errorf("useful = %d, want 10", decoded.UsefulCalls)
	}
	if len(decoded.ToolEvaluations) != 2 {
		t.Errorf("evaluations = %d, want 2", len(decoded.ToolEvaluations))
	}
	if decoded.ToolEvaluations[0].Usefulness != "useful" {
		t.Errorf("eval[0].usefulness = %q, want useful", decoded.ToolEvaluations[0].Usefulness)
	}
}

func TestModuleResultJSON(t *testing.T) {
	report := ToolEfficiencyReport{
		Summary:      "test",
		OverallScore: 50,
	}
	payload, _ := json.Marshal(report)

	mr := ModuleResult{
		Module:     ModuleToolEfficiency,
		Payload:    payload,
		TokensUsed: 1500,
		DurationMs: 2000,
	}

	data, err := json.Marshal(mr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ModuleResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Module != ModuleToolEfficiency {
		t.Errorf("module = %q, want %q", decoded.Module, ModuleToolEfficiency)
	}
	if decoded.TokensUsed != 1500 {
		t.Errorf("tokens = %d, want 1500", decoded.TokensUsed)
	}

	// Verify payload roundtrips.
	var decodedReport ToolEfficiencyReport
	if err := json.Unmarshal(decoded.Payload, &decodedReport); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if decodedReport.OverallScore != 50 {
		t.Errorf("payload score = %d, want 50", decodedReport.OverallScore)
	}
}
