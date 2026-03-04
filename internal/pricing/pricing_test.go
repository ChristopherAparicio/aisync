package pricing

import (
	"math"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestNewCalculator(t *testing.T) {
	c := NewCalculator()
	if c == nil {
		t.Fatal("NewCalculator() returned nil")
	}
	if len(c.prices) == 0 {
		t.Fatal("NewCalculator() has no prices loaded")
	}
}

func TestLookup_exactMatch(t *testing.T) {
	c := NewCalculator()

	price, found := c.Lookup("claude-sonnet-4")
	if !found {
		t.Fatal("expected to find claude-sonnet-4")
	}
	if price.InputPerMToken != 3.0 {
		t.Errorf("InputPerMToken = %f, want 3.0", price.InputPerMToken)
	}
	if price.OutputPerMToken != 15.0 {
		t.Errorf("OutputPerMToken = %f, want 15.0", price.OutputPerMToken)
	}
}

func TestLookup_prefixMatch(t *testing.T) {
	c := NewCalculator()

	// Dated variant should match the prefix
	price, found := c.Lookup("claude-sonnet-4-20250514")
	if !found {
		t.Fatal("expected prefix match for claude-sonnet-4-20250514")
	}
	if price.Model != "claude-sonnet-4" {
		t.Errorf("Model = %q, want %q", price.Model, "claude-sonnet-4")
	}
}

func TestLookup_caseInsensitive(t *testing.T) {
	c := NewCalculator()

	_, found := c.Lookup("Claude-Sonnet-4")
	if !found {
		t.Fatal("expected case-insensitive match")
	}
}

func TestLookup_notFound(t *testing.T) {
	c := NewCalculator()

	_, found := c.Lookup("unknown-model-xyz")
	if found {
		t.Fatal("expected not found for unknown model")
	}
}

func TestLookup_prefixPriority(t *testing.T) {
	// Longer prefix should match first (gpt-4o-mini vs gpt-4o)
	c := NewCalculator()

	price, found := c.Lookup("gpt-4o-mini-2025-01-01")
	if !found {
		t.Fatal("expected to find gpt-4o-mini variant")
	}
	if price.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want %q (longer prefix should win)", price.Model, "gpt-4o-mini")
	}
}

func TestWithOverrides(t *testing.T) {
	c := NewCalculator()

	// Override claude-sonnet-4 pricing
	c2 := c.WithOverrides([]ModelPrice{
		{Model: "claude-sonnet-4", InputPerMToken: 5.0, OutputPerMToken: 20.0},
	})

	price, found := c2.Lookup("claude-sonnet-4")
	if !found {
		t.Fatal("expected to find overridden price")
	}
	if price.InputPerMToken != 5.0 {
		t.Errorf("InputPerMToken = %f, want 5.0 (overridden)", price.InputPerMToken)
	}

	// Original should be unchanged
	origPrice, _ := c.Lookup("claude-sonnet-4")
	if origPrice.InputPerMToken != 3.0 {
		t.Errorf("original InputPerMToken = %f, want 3.0", origPrice.InputPerMToken)
	}
}

func TestWithOverrides_addNew(t *testing.T) {
	c := NewCalculator()
	c2 := c.WithOverrides([]ModelPrice{
		{Model: "my-custom-model", InputPerMToken: 1.0, OutputPerMToken: 2.0},
	})

	_, found := c.Lookup("my-custom-model")
	if found {
		t.Fatal("original should not have custom model")
	}

	price, found := c2.Lookup("my-custom-model")
	if !found {
		t.Fatal("overridden calculator should find custom model")
	}
	if price.InputPerMToken != 1.0 {
		t.Errorf("InputPerMToken = %f, want 1.0", price.InputPerMToken)
	}
}

func TestMessageCost(t *testing.T) {
	c := NewCalculator()

	// claude-sonnet-4: $3/M input, $15/M output
	// 10,000 input tokens = $0.03
	// 5,000 output tokens = $0.075
	cost := c.MessageCost("claude-sonnet-4", 10_000, 5_000)

	assertFloat(t, "InputCost", cost.InputCost, 0.03)
	assertFloat(t, "OutputCost", cost.OutputCost, 0.075)
	assertFloat(t, "TotalCost", cost.TotalCost, 0.105)

	if cost.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", cost.Currency)
	}
}

func TestMessageCost_unknownModel(t *testing.T) {
	c := NewCalculator()

	cost := c.MessageCost("unknown-model", 10_000, 5_000)
	if cost.TotalCost != 0 {
		t.Errorf("TotalCost = %f, want 0 for unknown model", cost.TotalCost)
	}
}

func TestSessionCost_basic(t *testing.T) {
	c := NewCalculator()

	sess := &session.Session{
		TokenUsage: session.TokenUsage{
			InputTokens:  20_000,
			OutputTokens: 10_000,
			TotalTokens:  30_000,
		},
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Hello"},
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", OutputTokens: 6_000},
			{Role: session.RoleUser, Content: "More work"},
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", OutputTokens: 4_000},
		},
	}

	est := c.SessionCost(sess)

	if len(est.PerModel) != 1 {
		t.Fatalf("PerModel length = %d, want 1", len(est.PerModel))
	}

	mc := est.PerModel[0]
	if mc.Model != "claude-sonnet-4" {
		t.Errorf("Model = %q, want claude-sonnet-4", mc.Model)
	}
	if mc.MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", mc.MessageCount)
	}
	if mc.OutputTokens != 10_000 {
		t.Errorf("OutputTokens = %d, want 10000", mc.OutputTokens)
	}
	// Input tokens should be distributed fully to this model (only one model)
	if mc.InputTokens != 20_000 {
		t.Errorf("InputTokens = %d, want 20000", mc.InputTokens)
	}

	// claude-sonnet-4: $3/M input, $15/M output
	// 20,000 input → $0.06, 10,000 output → $0.15
	assertFloat(t, "TotalCost", est.TotalCost.TotalCost, 0.21)
	if len(est.UnknownModels) != 0 {
		t.Errorf("UnknownModels = %v, want empty", est.UnknownModels)
	}
}

func TestSessionCost_multiModel(t *testing.T) {
	c := NewCalculator()

	sess := &session.Session{
		TokenUsage: session.TokenUsage{
			InputTokens:  30_000,
			OutputTokens: 15_000,
			TotalTokens:  45_000,
		},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", OutputTokens: 10_000},
			{Role: session.RoleAssistant, Model: "claude-opus-4", OutputTokens: 5_000},
		},
	}

	est := c.SessionCost(sess)

	if len(est.PerModel) != 2 {
		t.Fatalf("PerModel length = %d, want 2", len(est.PerModel))
	}

	// Input distributed proportionally: sonnet gets 2/3, opus gets 1/3
	for _, mc := range est.PerModel {
		switch mc.Model {
		case "claude-sonnet-4":
			if mc.InputTokens != 20_000 {
				t.Errorf("sonnet InputTokens = %d, want 20000", mc.InputTokens)
			}
		case "claude-opus-4":
			if mc.InputTokens != 10_000 {
				t.Errorf("opus InputTokens = %d, want 10000", mc.InputTokens)
			}
		default:
			t.Errorf("unexpected model %q", mc.Model)
		}
	}

	if est.TotalCost.TotalCost <= 0 {
		t.Error("expected positive total cost")
	}
}

func TestSessionCost_unknownModel(t *testing.T) {
	c := NewCalculator()

	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "mystery-llm-v99", OutputTokens: 5_000},
		},
	}

	est := c.SessionCost(sess)

	if len(est.PerModel) != 0 {
		t.Errorf("PerModel should be empty for unknown model, got %d", len(est.PerModel))
	}
	if len(est.UnknownModels) != 1 || est.UnknownModels[0] != "mystery-llm-v99" {
		t.Errorf("UnknownModels = %v, want [mystery-llm-v99]", est.UnknownModels)
	}
	if est.TotalCost.TotalCost != 0 {
		t.Errorf("TotalCost = %f, want 0 for unknown model", est.TotalCost.TotalCost)
	}
}

func TestSessionCost_noMessages(t *testing.T) {
	c := NewCalculator()

	est := c.SessionCost(&session.Session{})
	if est.TotalCost.TotalCost != 0 {
		t.Errorf("TotalCost = %f, want 0 for empty session", est.TotalCost.TotalCost)
	}
}

func TestSessionCost_nil(t *testing.T) {
	c := NewCalculator()
	est := c.SessionCost(nil)
	if est == nil {
		t.Fatal("SessionCost(nil) should not return nil")
	}
}

func TestSessionCost_noInputTokens(t *testing.T) {
	c := NewCalculator()

	// Session without input token data — only output cost should be computed
	sess := &session.Session{
		TokenUsage: session.TokenUsage{
			InputTokens:  0,
			OutputTokens: 5_000,
			TotalTokens:  5_000,
		},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", OutputTokens: 5_000},
		},
	}

	est := c.SessionCost(sess)

	mc := est.PerModel[0]
	if mc.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", mc.InputTokens)
	}
	// Output only: 5000 * 15 / 1M = $0.075
	assertFloat(t, "TotalCost", est.TotalCost.TotalCost, 0.075)
}

func TestSessionCost_userMessagesIgnored(t *testing.T) {
	c := NewCalculator()

	sess := &session.Session{
		TokenUsage: session.TokenUsage{InputTokens: 10_000},
		Messages: []session.Message{
			{Role: session.RoleUser, Model: "claude-sonnet-4", InputTokens: 100},
			{Role: session.RoleSystem, Model: "claude-sonnet-4", InputTokens: 50},
		},
	}

	est := c.SessionCost(sess)
	if len(est.PerModel) != 0 {
		t.Errorf("PerModel should be empty (only user/system messages), got %d", len(est.PerModel))
	}
}

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.0001 {
		t.Errorf("%s = %f, want %f", name, got, want)
	}
}
