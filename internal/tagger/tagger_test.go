package tagger

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// mockAnalyzer returns a fixed report with the summary containing the tag JSON.
type mockAnalyzer struct {
	summary string
	err     error
}

func (m *mockAnalyzer) Analyze(_ context.Context, _ analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &analysis.AnalysisReport{
		Score:   50,
		Summary: m.summary,
	}, nil
}

func (m *mockAnalyzer) Name() analysis.AdapterName { return analysis.AdapterOllama }

func testSession(messages ...string) *session.Session {
	var msgs []session.Message
	for i, content := range messages {
		role := session.RoleUser
		if i%2 == 1 {
			role = session.RoleAssistant
		}
		msgs = append(msgs, session.Message{Role: role, Content: content})
	}
	return &session.Session{
		ID:       "test-session",
		Provider: session.ProviderOpenCode,
		Messages: msgs,
	}
}

func TestClassify_Feature(t *testing.T) {
	tagJSON, _ := json.Marshal(Result{Tag: "feature", Confidence: 0.9, Reasoning: "Building new auth system"})
	analyzer := &mockAnalyzer{summary: string(tagJSON)}

	result := Classify(context.Background(), analyzer, testSession("implement user authentication"), nil, 10)
	if result.Tag != "feature" {
		t.Errorf("Tag = %q, want %q", result.Tag, "feature")
	}
	if result.Confidence < 0.8 {
		t.Errorf("Confidence = %f, expected >= 0.8", result.Confidence)
	}
}

func TestClassify_Bug(t *testing.T) {
	tagJSON, _ := json.Marshal(Result{Tag: "bug", Confidence: 0.85, Reasoning: "Fixing login crash"})
	analyzer := &mockAnalyzer{summary: string(tagJSON)}

	result := Classify(context.Background(), analyzer, testSession("fix the login crash on mobile"), nil, 10)
	if result.Tag != "bug" {
		t.Errorf("Tag = %q, want %q", result.Tag, "bug")
	}
}

func TestClassify_CustomTags(t *testing.T) {
	tagJSON, _ := json.Marshal(Result{Tag: "hotfix", Confidence: 0.8, Reasoning: "Critical production fix"})
	analyzer := &mockAnalyzer{summary: string(tagJSON)}

	customTags := []string{"feature", "hotfix", "maintenance"}
	result := Classify(context.Background(), analyzer, testSession("urgent production fix"), customTags, 10)
	if result.Tag != "hotfix" {
		t.Errorf("Tag = %q, want %q", result.Tag, "hotfix")
	}
}

func TestClassify_AnalyzerError_FallsBackToOther(t *testing.T) {
	analyzer := &mockAnalyzer{err: fmt.Errorf("model not available")}

	result := Classify(context.Background(), analyzer, testSession("do something"), nil, 10)
	if result.Tag != "other" {
		t.Errorf("Tag = %q, want %q on error", result.Tag, "other")
	}
}

func TestClassify_InvalidJSON_FallsBackToTextMatch(t *testing.T) {
	analyzer := &mockAnalyzer{summary: "This is clearly a refactor session with code cleanup"}

	result := Classify(context.Background(), analyzer, testSession("refactor the auth module"), nil, 10)
	if result.Tag != "refactor" {
		t.Errorf("Tag = %q, want %q (text fallback)", result.Tag, "refactor")
	}
}

func TestParseClassifyResult_DirectJSON(t *testing.T) {
	input := `{"tag":"bug","confidence":0.9,"reasoning":"fixing crash"}`
	result := parseClassifyResult(input, session.DefaultSessionTypes)
	if result.Tag != "bug" {
		t.Errorf("Tag = %q, want %q", result.Tag, "bug")
	}
}

func TestParseClassifyResult_EmbeddedJSON(t *testing.T) {
	input := `Here is my analysis: {"tag":"feature","confidence":0.85,"reasoning":"new feature"} that's it.`
	result := parseClassifyResult(input, session.DefaultSessionTypes)
	if result.Tag != "feature" {
		t.Errorf("Tag = %q, want %q", result.Tag, "feature")
	}
}

func TestParseClassifyResult_TextFallback(t *testing.T) {
	input := "This session is about devops and infrastructure"
	result := parseClassifyResult(input, session.DefaultSessionTypes)
	if result.Tag != "devops" {
		t.Errorf("Tag = %q, want %q (text fallback)", result.Tag, "devops")
	}
}

func TestParseClassifyResult_NoMatch(t *testing.T) {
	input := "completely unrelated text"
	result := parseClassifyResult(input, session.DefaultSessionTypes)
	if result.Tag != "other" {
		t.Errorf("Tag = %q, want %q", result.Tag, "other")
	}
}

func TestBuildClassifyPrompt_LimitsMessages(t *testing.T) {
	sess := &session.Session{
		Messages: make([]session.Message, 20),
	}
	for i := range sess.Messages {
		sess.Messages[i] = session.Message{Role: session.RoleUser, Content: "msg"}
	}

	prompt := buildClassifyPrompt(sess, session.DefaultSessionTypes, 5)
	// Count [N] markers — should be at most 5.
	count := 0
	for i := range prompt {
		if i+1 < len(prompt) && prompt[i] == '[' && prompt[i+1] >= '1' && prompt[i+1] <= '9' {
			count++
		}
	}
	if count > 5 {
		t.Errorf("expected at most 5 messages in prompt, found %d markers", count)
	}
}
