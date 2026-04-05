package identity

import (
	"testing"
)

func TestMatchNames_ExactEmail(t *testing.T) {
	result := MatchNames("John Doe", "john@company.com", "John Doe", "johnd", "john@company.com")
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.Score)
	}
	if result.Reason != "exact email match" {
		t.Errorf("expected reason 'exact email match', got %q", result.Reason)
	}
}

func TestMatchNames_ExactEmailCaseInsensitive(t *testing.T) {
	result := MatchNames("John Doe", "John.Doe@Company.COM", "John Doe", "", "john.doe@company.com")
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.Score)
	}
}

func TestMatchNames_ExactNameMatch(t *testing.T) {
	result := MatchNames("John Doe", "john@local.dev", "John Doe", "", "different@email.com")
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.Score)
	}
	if result.Reason != "exact name match" {
		t.Errorf("expected reason 'exact name match', got %q", result.Reason)
	}
}

func TestMatchNames_ExactNameViaDisplayName(t *testing.T) {
	result := MatchNames("Christophe", "chris@local.dev", "Christophe Aparicio", "Christophe", "pro@company.com")
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 via display name, got %f", result.Score)
	}
}

func TestMatchNames_TokenOverlap_FirstLastName(t *testing.T) {
	// "Christophe Aparicio" vs "Christophe Aparicio-Martin" → high overlap
	result := MatchNames("Christophe Aparicio", "chris@local.dev", "Christophe Aparicio-Martin", "", "pro@company.com")
	if result.Score < 0.5 {
		t.Errorf("expected score >= 0.5 for partial name match, got %f", result.Score)
	}
}

func TestMatchNames_TokenOverlap_DotSeparator(t *testing.T) {
	// Git name "christophe.aparicio" vs Slack "Christophe Aparicio"
	result := MatchNames("christophe.aparicio", "chris@local.dev", "Christophe Aparicio", "", "pro@company.com")
	if result.Score < 0.8 {
		t.Errorf("expected score >= 0.8 for dot-separated name, got %f", result.Score)
	}
}

func TestMatchNames_LevenshteinSimilarity(t *testing.T) {
	// Close but not exact: "Christophe" vs "Christopher"
	result := MatchNames("Christopher", "chris@local.dev", "Christophe", "", "pro@company.com")
	if result.Score < 0.7 {
		t.Errorf("expected score >= 0.7 for similar names, got %f", result.Score)
	}
}

func TestMatchNames_EmailUsernameVsName(t *testing.T) {
	// Git email "christophe.aparicio@company.com" → username tokens match Slack name
	result := MatchNames("Unknown User", "christophe.aparicio@company.com", "Christophe Aparicio", "", "pro@company.com")
	if result.Score < 0.5 {
		t.Errorf("expected score >= 0.5 for email username match, got %f", result.Score)
	}
}

func TestMatchNames_NoMatch(t *testing.T) {
	result := MatchNames("Alice Smith", "alice@local.dev", "Bob Johnson", "bobj", "bob@company.com")
	if result.Score > 0.3 {
		t.Errorf("expected score <= 0.3 for unrelated names, got %f", result.Score)
	}
}

func TestMatchNames_EmptyInputs(t *testing.T) {
	result := MatchNames("", "", "", "", "")
	if result.Score != 0 {
		t.Errorf("expected score 0 for empty inputs, got %f", result.Score)
	}
}

func TestMatchNames_EmptyGitName_WithEmail(t *testing.T) {
	// Empty git name but email username matches
	result := MatchNames("", "john.doe@company.com", "John Doe", "", "different@company.com")
	if result.Score < 0.5 {
		t.Errorf("expected score >= 0.5 for email username match with empty git name, got %f", result.Score)
	}
}

// --- tokenize tests ---

func TestTokenize_Basic(t *testing.T) {
	tokens := tokenize("John Doe")
	if len(tokens) != 2 || tokens[0] != "john" || tokens[1] != "doe" {
		t.Errorf("expected [john doe], got %v", tokens)
	}
}

func TestTokenize_DotSeparated(t *testing.T) {
	tokens := tokenize("john.doe")
	if len(tokens) != 2 || tokens[0] != "john" || tokens[1] != "doe" {
		t.Errorf("expected [john doe], got %v", tokens)
	}
}

func TestTokenize_DashSeparated(t *testing.T) {
	tokens := tokenize("john-doe")
	if len(tokens) != 2 || tokens[0] != "john" || tokens[1] != "doe" {
		t.Errorf("expected [john doe], got %v", tokens)
	}
}

func TestTokenize_UnderscoreSeparated(t *testing.T) {
	tokens := tokenize("john_doe")
	if len(tokens) != 2 || tokens[0] != "john" || tokens[1] != "doe" {
		t.Errorf("expected [john doe], got %v", tokens)
	}
}

func TestTokenize_EmailUsername(t *testing.T) {
	tokens := tokenize("john.doe@company.com")
	// Should split into: john, doe, company, com
	if len(tokens) < 2 {
		t.Errorf("expected at least 2 tokens, got %v", tokens)
	}
	if tokens[0] != "john" || tokens[1] != "doe" {
		t.Errorf("expected first two tokens to be [john doe], got %v", tokens)
	}
}

func TestTokenize_Empty(t *testing.T) {
	tokens := tokenize("")
	if len(tokens) != 0 {
		t.Errorf("expected empty, got %v", tokens)
	}
}

func TestTokenize_Deduplicates(t *testing.T) {
	tokens := tokenize("john john")
	if len(tokens) != 1 {
		t.Errorf("expected 1 unique token, got %v", tokens)
	}
}

// --- tokenOverlap tests ---

func TestTokenOverlap_Identical(t *testing.T) {
	score := tokenOverlap("John Doe", "John Doe")
	if score != 1.0 {
		t.Errorf("expected 1.0 for identical names, got %f", score)
	}
}

func TestTokenOverlap_NoOverlap(t *testing.T) {
	score := tokenOverlap("Alice Smith", "Bob Johnson")
	if score != 0 {
		t.Errorf("expected 0 for no overlap, got %f", score)
	}
}

func TestTokenOverlap_Partial(t *testing.T) {
	// "John Doe" (2 tokens) vs "John Smith" (2 tokens) → intersection=1, union=3 → 0.333
	score := tokenOverlap("John Doe", "John Smith")
	expected := 1.0 / 3.0
	if diff := score - expected; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected ~%f, got %f", expected, score)
	}
}

func TestTokenOverlap_DifferentSeparators(t *testing.T) {
	score := tokenOverlap("john.doe", "John Doe")
	if score != 1.0 {
		t.Errorf("expected 1.0 for same tokens with different separators, got %f", score)
	}
}

func TestTokenOverlap_Empty(t *testing.T) {
	score := tokenOverlap("", "John")
	if score != 0 {
		t.Errorf("expected 0 for empty input, got %f", score)
	}
}

// --- levenshtein tests ---

func TestLevenshteinDistance_Identical(t *testing.T) {
	if d := levenshteinDistance("hello", "hello"); d != 0 {
		t.Errorf("expected 0, got %d", d)
	}
}

func TestLevenshteinDistance_OneEdit(t *testing.T) {
	if d := levenshteinDistance("hello", "hallo"); d != 1 {
		t.Errorf("expected 1, got %d", d)
	}
}

func TestLevenshteinDistance_Empty(t *testing.T) {
	if d := levenshteinDistance("", "hello"); d != 5 {
		t.Errorf("expected 5, got %d", d)
	}
	if d := levenshteinDistance("hello", ""); d != 5 {
		t.Errorf("expected 5, got %d", d)
	}
}

func TestLevenshteinDistance_BothEmpty(t *testing.T) {
	if d := levenshteinDistance("", ""); d != 0 {
		t.Errorf("expected 0, got %d", d)
	}
}

func TestLevenshteinSimilarity_Identical(t *testing.T) {
	// Both empty → returns 0 (no meaningful comparison)
	if s := levenshteinSimilarity("", ""); s != 0 {
		t.Errorf("expected 0 for both empty, got %f", s)
	}
	if s := levenshteinSimilarity("hello", "hello"); s != 1.0 {
		t.Errorf("expected 1.0 for identical strings, got %f", s)
	}
}

func TestLevenshteinSimilarity_Similar(t *testing.T) {
	// "christophe" vs "christopher" → distance 2, max len 11 → 1 - 2/11 = 0.818
	s := levenshteinSimilarity("christophe", "christopher")
	if s < 0.7 || s > 0.95 {
		t.Errorf("expected similarity ~0.8, got %f", s)
	}
}

// --- extractEmailUsername tests ---

func TestExtractEmailUsername(t *testing.T) {
	tests := []struct {
		email    string
		expected string
	}{
		{"john.doe@company.com", "john.doe"},
		{"john@local", "john"},
		{"@invalid.com", ""},
		{"noemail", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractEmailUsername(tt.email)
		if got != tt.expected {
			t.Errorf("extractEmailUsername(%q) = %q, want %q", tt.email, got, tt.expected)
		}
	}
}

// --- normalizeName tests ---

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  John  Doe  ", "john doe"},
		{"JOHN DOE", "john doe"},
		{"john doe", "john doe"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeName(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- ScoreToConfidence tests ---

func TestScoreToConfidence(t *testing.T) {
	tests := []struct {
		score    float64
		expected MatchConfidence
	}{
		{1.0, ConfidenceExact},
		{0.95, ConfidenceHigh},
		{0.8, ConfidenceHigh},
		{0.75, ConfidenceMedium},
		{0.6, ConfidenceMedium},
		{0.5, ConfidenceLow},
		{0.4, ConfidenceLow},
		{0.3, ConfidenceNone},
		{0.0, ConfidenceNone},
	}
	for _, tt := range tests {
		got := ScoreToConfidence(tt.score)
		if got != tt.expected {
			t.Errorf("ScoreToConfidence(%f) = %q, want %q", tt.score, got, tt.expected)
		}
	}
}

// --- min3 tests ---

func TestMin3(t *testing.T) {
	if min3(1, 2, 3) != 1 {
		t.Error("min3(1,2,3) should be 1")
	}
	if min3(3, 1, 2) != 1 {
		t.Error("min3(3,1,2) should be 1")
	}
	if min3(2, 3, 1) != 1 {
		t.Error("min3(2,3,1) should be 1")
	}
	if min3(5, 5, 5) != 5 {
		t.Error("min3(5,5,5) should be 5")
	}
}
