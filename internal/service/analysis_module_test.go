package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
)

// stubModule is a test double for analysis.AnalysisModule.
type stubModule struct {
	name   analysis.ModuleName
	result *analysis.ModuleResult
	err    error
}

func (m *stubModule) Name() analysis.ModuleName { return m.name }
func (m *stubModule) Analyze(_ context.Context, _ analysis.ModuleRequest) (*analysis.ModuleResult, error) {
	return m.result, m.err
}

func TestContainsModule(t *testing.T) {
	modules := []analysis.ModuleName{analysis.ModuleSessionQuality, analysis.ModuleToolEfficiency}
	if !containsModule(modules, analysis.ModuleSessionQuality) {
		t.Error("should contain session_quality")
	}
	if !containsModule(modules, analysis.ModuleToolEfficiency) {
		t.Error("should contain tool_efficiency")
	}
	if containsModule(modules, "unknown") {
		t.Error("should not contain unknown")
	}
	if containsModule(nil, analysis.ModuleSessionQuality) {
		t.Error("nil list should not contain anything")
	}
}

func TestFilterModules(t *testing.T) {
	teModule := &stubModule{name: analysis.ModuleToolEfficiency}
	registered := map[analysis.ModuleName]analysis.AnalysisModule{
		analysis.ModuleToolEfficiency: teModule,
	}

	// Requesting tool_efficiency should return it.
	result := filterModules([]analysis.ModuleName{analysis.ModuleToolEfficiency}, registered)
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].Name() != analysis.ModuleToolEfficiency {
		t.Errorf("module = %q, want tool_efficiency", result[0].Name())
	}

	// session_quality should be filtered out (handled by core).
	result = filterModules([]analysis.ModuleName{analysis.ModuleSessionQuality, analysis.ModuleToolEfficiency}, registered)
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1 (session_quality excluded)", len(result))
	}

	// Empty request returns nil.
	result = filterModules(nil, registered)
	if result != nil {
		t.Errorf("nil request should return nil, got %d", len(result))
	}

	// Unknown module is skipped.
	result = filterModules([]analysis.ModuleName{"unknown"}, registered)
	if len(result) != 0 {
		t.Errorf("unknown module should be skipped, got %d", len(result))
	}
}

func TestAvailableModules_WithModulesAndAnalyzer(t *testing.T) {
	teModule := &stubModule{name: analysis.ModuleToolEfficiency}
	svc := NewAnalysisService(AnalysisServiceConfig{
		Analyzer: &stubAnalyzer{name: "test"},
		Modules: map[analysis.ModuleName]analysis.AnalysisModule{
			analysis.ModuleToolEfficiency: teModule,
		},
	})

	modules := svc.AvailableModules()
	if len(modules) < 2 {
		t.Fatalf("expected at least 2 modules, got %d", len(modules))
	}

	// Should have session_quality (from analyzer) and tool_efficiency (from registered module).
	names := make(map[analysis.ModuleName]bool)
	for _, m := range modules {
		names[m.Name] = true
	}
	if !names[analysis.ModuleSessionQuality] {
		t.Error("missing session_quality")
	}
	if !names[analysis.ModuleToolEfficiency] {
		t.Error("missing tool_efficiency")
	}
}

func TestAvailableModules_NoAnalyzer(t *testing.T) {
	teModule := &stubModule{name: analysis.ModuleToolEfficiency}
	svc := NewAnalysisService(AnalysisServiceConfig{
		Modules: map[analysis.ModuleName]analysis.AnalysisModule{
			analysis.ModuleToolEfficiency: teModule,
		},
	})

	modules := svc.AvailableModules()
	if len(modules) != 1 {
		t.Fatalf("expected 1 module (no analyzer), got %d", len(modules))
	}
	if modules[0].Name != analysis.ModuleToolEfficiency {
		t.Errorf("module = %q, want tool_efficiency", modules[0].Name)
	}
}

// stubAnalyzer is a minimal analysis.Analyzer for testing.
type stubAnalyzer struct {
	name   string
	report *analysis.AnalysisReport
	err    error
}

func (a *stubAnalyzer) Name() analysis.AdapterName { return analysis.AdapterName(a.name) }
func (a *stubAnalyzer) Analyze(_ context.Context, _ analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	if a.err != nil {
		return nil, a.err
	}
	if a.report != nil {
		return a.report, nil
	}
	return &analysis.AnalysisReport{Score: 75, Summary: "test analysis"}, nil
}

func TestModuleResultPayloadRoundtrip(t *testing.T) {
	report := analysis.ToolEfficiencyReport{
		Summary:        "Good usage",
		OverallScore:   80,
		UsefulCalls:    5,
		RedundantCalls: 1,
	}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}

	mr := analysis.ModuleResult{
		Module:     analysis.ModuleToolEfficiency,
		Payload:    payload,
		TokensUsed: 500,
		DurationMs: 1000,
	}

	// Simulate storage roundtrip via AnalysisReport.
	ar := analysis.AnalysisReport{
		Score:         75,
		Summary:       "test",
		ModuleResults: []analysis.ModuleResult{mr},
	}

	data, err := json.Marshal(ar)
	if err != nil {
		t.Fatal(err)
	}

	var decoded analysis.AnalysisReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if len(decoded.ModuleResults) != 1 {
		t.Fatalf("module results = %d, want 1", len(decoded.ModuleResults))
	}
	if decoded.ModuleResults[0].Module != analysis.ModuleToolEfficiency {
		t.Errorf("module = %q", decoded.ModuleResults[0].Module)
	}

	var decodedReport analysis.ToolEfficiencyReport
	if err := json.Unmarshal(decoded.ModuleResults[0].Payload, &decodedReport); err != nil {
		t.Fatal(err)
	}
	if decodedReport.OverallScore != 80 {
		t.Errorf("score = %d, want 80", decodedReport.OverallScore)
	}
}
