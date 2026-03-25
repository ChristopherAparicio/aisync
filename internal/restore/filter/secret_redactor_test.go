package filter

import (
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestSecretRedactor_Name(t *testing.T) {
	f := NewSecretRedactor()
	if f.Name() != "secret-redactor" {
		t.Errorf("Name() = %q, want %q", f.Name(), "secret-redactor")
	}
}

func TestSecretRedactor_noSecrets(t *testing.T) {
	f := NewSecretRedactor()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "hello world", Role: session.RoleUser},
			{Content: "no secrets here", Role: session.RoleAssistant},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("filter should not have applied")
	}
	if result.Messages[0].Content != "hello world" {
		t.Error("content should be unchanged")
	}
}

func TestSecretRedactor_redactsAWSKey(t *testing.T) {
	f := NewSecretRedactor()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Content: "My AWS key is AKIAIOSFODNN7EXAMPLE",
				Role:    session.RoleUser,
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Error("filter should have applied")
	}
	if fr.SecretsFound < 1 {
		t.Error("should have found at least 1 secret")
	}

	content := result.Messages[0].Content
	if strings.Contains(content, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("content should not contain the raw secret, got %q", content)
	}
	if !strings.Contains(content, "$AWS_ACCESS_KEY_ID") {
		t.Errorf("content should contain $AWS_ACCESS_KEY_ID, got %q", content)
	}
}

func TestSecretRedactor_redactsToolCallInputs(t *testing.T) {
	f := NewSecretRedactor()
	// Use a valid GitHub token format: ghp_ + 36 alphanumeric chars
	githubToken := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{
						ID:    "tc-1",
						Name:  "bash",
						Input: "git clone https://" + githubToken + "@github.com/org/repo",
					},
				},
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Error("filter should have applied")
	}

	input := result.Messages[0].ToolCalls[0].Input
	if strings.Contains(input, "ghp_") {
		t.Errorf("tool call input should not contain raw GitHub token, got %q", input)
	}
	if !strings.Contains(input, "$GITHUB_TOKEN") {
		t.Errorf("tool call input should contain $GITHUB_TOKEN, got %q", input)
	}
}

func TestSecretRedactor_redactsToolCallOutputs(t *testing.T) {
	f := NewSecretRedactor()
	// OpenAI key pattern: sk- + 20+ alphanumeric chars
	openaiKey := "sk-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{
						ID:     "tc-1",
						Name:   "bash",
						Output: "Found key: " + openaiKey,
					},
				},
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Error("filter should have applied")
	}

	output := result.Messages[0].ToolCalls[0].Output
	if strings.Contains(output, openaiKey) {
		t.Errorf("tool call output should not contain raw OpenAI key, got %q", output)
	}
}

func TestSecretRedactor_redactsContentBlocks(t *testing.T) {
	f := NewSecretRedactor()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ContentBlocks: []session.ContentBlock{
					{
						Type: session.ContentBlockText,
						Text: "API key: AKIAIOSFODNN7EXAMPLE",
					},
				},
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Error("filter should have applied")
	}

	text := result.Messages[0].ContentBlocks[0].Text
	if strings.Contains(text, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("content block should not contain raw AWS key, got %q", text)
	}
}

func TestSecretRedactor_redactsThinking(t *testing.T) {
	f := NewSecretRedactor()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Role:     session.RoleAssistant,
				Thinking: "I see the API key AKIAIOSFODNN7EXAMPLE in the config",
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Error("filter should have applied")
	}

	if strings.Contains(result.Messages[0].Thinking, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("thinking content should not contain raw AWS key")
	}
}

func TestSecretRedactor_doesNotModifyOriginal(t *testing.T) {
	f := NewSecretRedactor()
	original := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "AKIAIOSFODNN7EXAMPLE"},
		},
	}

	originalContent := original.Messages[0].Content
	_, _, err := f.Apply(original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if original.Messages[0].Content != originalContent {
		t.Error("Apply should not modify the original session")
	}
}

func TestTypeToVarName(t *testing.T) {
	tests := []struct {
		secretType string
		want       string
	}{
		{"AWS_ACCESS_KEY", "AWS_ACCESS_KEY_ID"},
		{"GITHUB_TOKEN", "GITHUB_TOKEN"},
		{"OPENAI_API_KEY", "OPENAI_API_KEY"},
		{"GENERIC_SECRET", "SECRET_KEY"},
		{"UNKNOWN_TYPE", "UNKNOWN_TYPE"},
	}

	for _, tt := range tests {
		t.Run(tt.secretType, func(t *testing.T) {
			seen := make(map[string]int)
			got := typeToVarName(tt.secretType, seen)
			if got != tt.want {
				t.Errorf("typeToVarName(%q) = %q, want %q", tt.secretType, got, tt.want)
			}
		})
	}
}

func TestTypeToVarName_duplicates(t *testing.T) {
	seen := make(map[string]int)

	first := typeToVarName("GITHUB_TOKEN", seen)
	if first != "GITHUB_TOKEN" {
		t.Errorf("first call should return GITHUB_TOKEN, got %q", first)
	}

	second := typeToVarName("GITHUB_TOKEN", seen)
	if second != "GITHUB_TOKEN_2" {
		t.Errorf("second call should return GITHUB_TOKEN_2, got %q", second)
	}

	third := typeToVarName("GITHUB_TOKEN", seen)
	if third != "GITHUB_TOKEN_3" {
		t.Errorf("third call should return GITHUB_TOKEN_3, got %q", third)
	}
}
