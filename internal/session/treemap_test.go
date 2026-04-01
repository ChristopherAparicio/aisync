package session

import "testing"

func TestBuildCostTreemap_Empty(t *testing.T) {
	result := BuildCostTreemap(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestBuildCostTreemap_EmptyMap(t *testing.T) {
	result := BuildCostTreemap(map[string]map[string]CostTreemapNode{})
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestBuildCostTreemap_SingleBackendSingleModel(t *testing.T) {
	data := map[string]map[string]CostTreemapNode{
		"anthropic": {
			"claude-sonnet-4-20250514": {Name: "claude-sonnet-4-20250514", Cost: 10.0, Tokens: 500000, SessionCount: 50},
		},
	}
	result := BuildCostTreemap(data)

	if len(result) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result))
	}
	if result[0].Name != "anthropic" {
		t.Errorf("name = %s, want anthropic", result[0].Name)
	}
	if result[0].Cost != 10.0 {
		t.Errorf("cost = %f, want 10.0", result[0].Cost)
	}
	if result[0].Share != 100.0 {
		t.Errorf("share = %f, want 100", result[0].Share)
	}
	if len(result[0].Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(result[0].Children))
	}
	if result[0].Children[0].Share != 100.0 {
		t.Errorf("child share = %f, want 100", result[0].Children[0].Share)
	}
}

func TestBuildCostTreemap_MultiBackendMultiModel(t *testing.T) {
	data := map[string]map[string]CostTreemapNode{
		"anthropic": {
			"claude-sonnet-4-20250514": {Name: "claude-sonnet-4-20250514", Cost: 8.0, Tokens: 400000, SessionCount: 40},
			"claude-haiku-4-20250514":  {Name: "claude-haiku-4-20250514", Cost: 2.0, Tokens: 200000, SessionCount: 30},
		},
		"amazon-bedrock": {
			"claude-sonnet-4-20250514": {Name: "claude-sonnet-4-20250514", Cost: 5.0, Tokens: 300000, SessionCount: 20},
		},
	}
	result := BuildCostTreemap(data)

	if len(result) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result))
	}

	// Sorted by cost descending: anthropic (10) > bedrock (5).
	if result[0].Name != "anthropic" {
		t.Errorf("first = %s, want anthropic (highest cost)", result[0].Name)
	}
	if result[0].Cost != 10.0 {
		t.Errorf("anthropic cost = %f, want 10", result[0].Cost)
	}
	if result[1].Name != "amazon-bedrock" {
		t.Errorf("second = %s, want amazon-bedrock", result[1].Name)
	}
	if result[1].Cost != 5.0 {
		t.Errorf("bedrock cost = %f, want 5", result[1].Cost)
	}

	// Total is 15, so anthropic share = 10/15 ≈ 66.67%.
	expectedAnthShare := 10.0 / 15.0 * 100.0
	if diff := result[0].Share - expectedAnthShare; diff > 0.01 || diff < -0.01 {
		t.Errorf("anthropic share = %f, want ~%.2f", result[0].Share, expectedAnthShare)
	}

	// Anthropic children sorted: sonnet (8) > haiku (2).
	if len(result[0].Children) != 2 {
		t.Fatalf("anthropic children = %d, want 2", len(result[0].Children))
	}
	if result[0].Children[0].Cost != 8.0 {
		t.Errorf("first child cost = %f, want 8 (sonnet)", result[0].Children[0].Cost)
	}
	// Child share: 8/10 = 80%.
	if diff := result[0].Children[0].Share - 80.0; diff > 0.01 || diff < -0.01 {
		t.Errorf("sonnet share = %f, want 80", result[0].Children[0].Share)
	}
}

func TestBuildCostTreemap_TokensAggregation(t *testing.T) {
	data := map[string]map[string]CostTreemapNode{
		"backend1": {
			"model-a": {Name: "model-a", Cost: 5.0, Tokens: 100000, SessionCount: 10},
			"model-b": {Name: "model-b", Cost: 3.0, Tokens: 60000, SessionCount: 8},
		},
	}
	result := BuildCostTreemap(data)

	if len(result) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result))
	}
	// Parent tokens = sum of children.
	if result[0].Tokens != 160000 {
		t.Errorf("tokens = %d, want 160000", result[0].Tokens)
	}
	if result[0].SessionCount != 18 {
		t.Errorf("sessions = %d, want 18", result[0].SessionCount)
	}
}

func TestBuildCostTreemap_SortDescending(t *testing.T) {
	data := map[string]map[string]CostTreemapNode{
		"cheap":     {"m": {Name: "m", Cost: 1.0}},
		"mid":       {"m": {Name: "m", Cost: 5.0}},
		"expensive": {"m": {Name: "m", Cost: 10.0}},
	}
	result := BuildCostTreemap(data)

	if len(result) != 3 {
		t.Fatalf("expected 3, got %d", len(result))
	}
	if result[0].Name != "expensive" {
		t.Errorf("first = %s, want expensive", result[0].Name)
	}
	if result[1].Name != "mid" {
		t.Errorf("second = %s, want mid", result[1].Name)
	}
	if result[2].Name != "cheap" {
		t.Errorf("third = %s, want cheap", result[2].Name)
	}
}
