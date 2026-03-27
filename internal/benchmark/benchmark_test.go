package benchmark

import (
	"testing"
)

// ── EmbeddedCatalog tests ──

func TestNewEmbeddedCatalog(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog: %v", err)
	}

	entries := cat.List()
	if len(entries) == 0 {
		t.Fatal("expected entries in embedded catalog")
	}

	// Should be sorted by score descending.
	for i := 1; i < len(entries); i++ {
		if entries[i].Score > entries[i-1].Score {
			t.Errorf("entries not sorted: [%d] %s (%.1f) > [%d] %s (%.1f)",
				i, entries[i].Model, entries[i].Score,
				i-1, entries[i-1].Model, entries[i-1].Score)
		}
	}
}

func TestEmbeddedCatalog_Lookup_ExactMatch(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog: %v", err)
	}

	entry, ok := cat.Lookup("claude-opus-4")
	if !ok {
		t.Fatal("claude-opus-4 not found")
	}
	if entry.Score != 72.0 {
		t.Errorf("claude-opus-4 score = %.1f, want 72.0", entry.Score)
	}
}

func TestEmbeddedCatalog_Lookup_AliasMatch(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog: %v", err)
	}

	tests := []struct {
		query string
		want  string
		score float64
	}{
		{"claude-opus-4-20250514", "claude-opus-4", 72.0},
		{"claude-opus-4-1", "claude-opus-4", 72.0},
		{"anthropic.claude-opus-4-6-v1", "claude-opus-4", 72.0},
		{"claude-sonnet-4-20250514", "claude-sonnet-4", 60.9},
		{"gpt-4o-2025", "gpt-4o", 68.2},
		{"deepseek/deepseek-r1", "deepseek-r1", 73.6},
		{"deepseek/deepseek-chat", "deepseek-v3", 70.2},
	}

	for _, tt := range tests {
		entry, ok := cat.Lookup(tt.query)
		if !ok {
			t.Errorf("Lookup(%q): not found", tt.query)
			continue
		}
		if entry.Model != tt.want {
			t.Errorf("Lookup(%q).Model = %q, want %q", tt.query, entry.Model, tt.want)
		}
		if entry.Score != tt.score {
			t.Errorf("Lookup(%q).Score = %.1f, want %.1f", tt.query, entry.Score, tt.score)
		}
	}
}

func TestEmbeddedCatalog_Lookup_PrefixMatch(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog: %v", err)
	}

	// Versioned model names should prefix-match to the short name.
	entry, ok := cat.Lookup("gemini-2.5-pro-exp-0325")
	if !ok {
		t.Fatal("gemini-2.5-pro-exp-0325 should prefix-match to gemini-2.5-pro")
	}
	if entry.Model != "gemini-2.5-pro" {
		t.Errorf("expected gemini-2.5-pro, got %s", entry.Model)
	}
}

func TestEmbeddedCatalog_Lookup_NotFound(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog: %v", err)
	}

	_, ok := cat.Lookup("nonexistent-model-v99")
	if ok {
		t.Error("expected nonexistent model to return false")
	}
}

func TestEmbeddedCatalog_Lookup_CaseInsensitive(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog: %v", err)
	}

	entry, ok := cat.Lookup("Claude-Opus-4")
	if !ok {
		t.Fatal("case-insensitive lookup failed")
	}
	if entry.Score != 72.0 {
		t.Errorf("score = %.1f, want 72.0", entry.Score)
	}
}

func TestEmbeddedCatalog_Lookup_ProviderPrefix(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog: %v", err)
	}

	// Bedrock-style prefix.
	entry, ok := cat.Lookup("anthropic.claude-opus-4-6-v1")
	if !ok {
		t.Fatal("bedrock model name not found")
	}
	if entry.Model != "claude-opus-4" {
		t.Errorf("expected claude-opus-4, got %s", entry.Model)
	}
}

func TestEmbeddedCatalog_AllEntriesHaveSource(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog: %v", err)
	}

	for _, e := range cat.List() {
		if e.Source != SourceAiderPolyglot {
			t.Errorf("entry %s has source %q, want %q", e.Model, e.Source, SourceAiderPolyglot)
		}
		if e.Date == "" {
			t.Errorf("entry %s has empty date", e.Model)
		}
	}
}

// ── Recommender tests ──

func TestClassifyAlternative(t *testing.T) {
	tests := []struct {
		name string
		alt  ModelAlternative
		want string
	}{
		{
			name: "no-brainer: better quality AND cheaper",
			alt:  ModelAlternative{ScoreDelta: 5.0, CostSavings: 80.0, QualityDrop: 0},
			want: "no-brainer",
		},
		{
			name: "tradeoff: small quality drop, big savings",
			alt:  ModelAlternative{ScoreDelta: -10.0, CostSavings: 90.0, QualityDrop: 10.0},
			want: "tradeoff",
		},
		{
			name: "risky: large quality drop",
			alt:  ModelAlternative{ScoreDelta: -20.0, CostSavings: 95.0, QualityDrop: 20.0},
			want: "risky",
		},
		{
			name: "upgrade: better quality but more expensive",
			alt:  ModelAlternative{ScoreDelta: 15.0, CostSavings: -50.0, QualityDrop: 0},
			want: "upgrade",
		},
		{
			name: "tradeoff boundary: exactly 15% drop",
			alt:  ModelAlternative{ScoreDelta: -15.0, CostSavings: 70.0, QualityDrop: 15.0},
			want: "tradeoff",
		},
		{
			name: "risky boundary: 15.1% drop",
			alt:  ModelAlternative{ScoreDelta: -15.1, CostSavings: 70.0, QualityDrop: 15.1},
			want: "risky",
		},
	}

	for _, tt := range tests {
		got := classifyAlternative(tt.alt)
		if got != tt.want {
			t.Errorf("%s: verdict = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestNewCatalogFromData(t *testing.T) {
	yaml := `
source: test
date: "2026-01-01"
entries:
  - model: model-a
    score: 80.0
    aliases: ["model-a-v2"]
  - model: model-b
    score: 60.0
  - model: model-c
    score: 70.0
`
	cat, err := NewCatalogFromData([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	entries := cat.List()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Should be sorted by score descending.
	if entries[0].Model != "model-a" || entries[1].Model != "model-c" || entries[2].Model != "model-b" {
		t.Errorf("unexpected order: %s, %s, %s", entries[0].Model, entries[1].Model, entries[2].Model)
	}

	// Alias lookup.
	entry, ok := cat.Lookup("model-a-v2")
	if !ok {
		t.Fatal("alias model-a-v2 not found")
	}
	if entry.Model != "model-a" {
		t.Errorf("alias lookup: got %s, want model-a", entry.Model)
	}

	// Source populated from file header.
	if entries[0].Source != "test" {
		t.Errorf("source = %q, want test", entries[0].Source)
	}
}

// ── normalizeModel tests ──

func TestNormalizeModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Claude-Opus-4", "claude-opus-4"},
		{"anthropic.claude-opus-4-6-v1", "claude-opus-4-6-v1"},
		{"bedrock/anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"}, // strips bedrock/ first
		{"openai/gpt-4o", "gpt-4o"},
		{"deepseek/deepseek-chat", "deepseek-chat"},
		{"gpt-4o", "gpt-4o"},
	}

	for _, tt := range tests {
		got := normalizeModel(tt.input)
		if got != tt.want {
			t.Errorf("normalizeModel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── QAC computation tests ──

func TestComputeQAC(t *testing.T) {
	tests := []struct {
		name  string
		cost  float64
		score float64
		want  float64
	}{
		{"opus: $15/M at 72%", 15.0, 72.0, 20.833},           // 15 / 0.72 = 20.833
		{"deepseek-r1: $0.55/M at 73.6%", 0.55, 73.6, 0.747}, // 0.55 / 0.736 = 0.747
		{"gpt-5: $10/M at 88%", 10.0, 88.0, 11.364},          // 10 / 0.88 = 11.364
		{"zero score", 15.0, 0.0, 0.0},                       // undefined
		{"zero cost", 0.0, 72.0, 0.0},                        // free model
	}

	for _, tt := range tests {
		got := ComputeQAC(tt.cost, tt.score)
		// Allow 0.01 tolerance for floating point.
		diff := got - tt.want
		if diff < 0 {
			diff = -diff
		}
		if diff > 0.01 {
			t.Errorf("%s: QAC = %.3f, want %.3f", tt.name, got, tt.want)
		}
	}
}

func TestModelAlternative_QACFields(t *testing.T) {
	// Verify that classifyAlternative still works correctly with QAC fields present.
	alt := ModelAlternative{
		CurrentModel: "claude-opus-4",
		CurrentScore: 72.0,
		CurrentCost:  15.0,
		AltModel:     "deepseek-r1",
		AltScore:     73.6,
		AltCost:      0.55,
		ScoreDelta:   1.6,
		CostSavings:  96.3,
		QualityDrop:  0,
		CurrentQAC:   ComputeQAC(15.0, 72.0),
		AltQAC:       ComputeQAC(0.55, 73.6),
	}

	// This should be a no-brainer: better score AND cheaper.
	verdict := classifyAlternative(alt)
	if verdict != "no-brainer" {
		t.Errorf("expected no-brainer, got %s", verdict)
	}

	// QAC savings should be massive.
	if alt.CurrentQAC <= 0 || alt.AltQAC <= 0 {
		t.Fatal("QAC values should be positive")
	}
	qacSavings := (alt.CurrentQAC - alt.AltQAC) / alt.CurrentQAC * 100
	if qacSavings < 95 {
		t.Errorf("expected QAC savings >95%%, got %.1f%%", qacSavings)
	}
}
