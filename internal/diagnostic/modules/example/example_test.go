package example

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/diagnostic"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestModule_Name(t *testing.T) {
	mod := &Module{}
	if mod.Name() != "example" {
		t.Errorf("expected name 'example', got %q", mod.Name())
	}
}

func TestModule_ShouldActivate_shortSession(t *testing.T) {
	mod := &Module{}
	sess := &session.Session{
		Messages: make([]session.Message, 5), // fewer than 10
	}
	if mod.ShouldActivate(sess) {
		t.Error("should not activate for sessions with fewer than 10 messages")
	}
}

func TestModule_ShouldActivate_longSession(t *testing.T) {
	mod := &Module{}
	sess := &session.Session{
		Messages: make([]session.Message, 15),
	}
	if !mod.ShouldActivate(sess) {
		t.Error("should activate for sessions with 10+ messages")
	}
}

func TestModule_Detect_normalRatio(t *testing.T) {
	mod := &Module{}
	msgs := []session.Message{
		{Role: session.RoleUser, Content: "do task 1"},
		{Role: session.RoleAssistant, Content: "done 1"},
		{Role: session.RoleAssistant, Content: "also done 2"},
		{Role: session.RoleUser, Content: "do task 2"},
		{Role: session.RoleAssistant, Content: "done 2"},
		{Role: session.RoleAssistant, Content: "also done 3"},
		{Role: session.RoleAssistant, Content: "finished 4"},
		{Role: session.RoleUser, Content: "do task 3"},
		{Role: session.RoleAssistant, Content: "done 3"},
		{Role: session.RoleAssistant, Content: "done 4"},
	}
	sess := &session.Session{Messages: msgs}
	r := &diagnostic.InspectReport{}
	problems := mod.Detect(r, sess)
	if len(problems) != 0 {
		t.Errorf("expected 0 problems for normal ratio (3 user / 7 asst), got %d", len(problems))
	}
}

func TestModule_Detect_highRatio(t *testing.T) {
	mod := &Module{}
	msgs := []session.Message{
		{Role: session.RoleUser, Content: "do task 1"},
		{Role: session.RoleAssistant, Content: "done 1"},
		{Role: session.RoleUser, Content: "no that's wrong"},
		{Role: session.RoleAssistant, Content: "retry 1"},
		{Role: session.RoleUser, Content: "still wrong"},
		{Role: session.RoleAssistant, Content: "retry 2"},
		{Role: session.RoleUser, Content: "try again"},
		{Role: session.RoleAssistant, Content: "retry 3"},
		{Role: session.RoleUser, Content: "no look at the code"},
		{Role: session.RoleAssistant, Content: "retry 4"},
		{Role: session.RoleUser, Content: "ugh try this"},
		{Role: session.RoleAssistant, Content: "done"},
	}
	sess := &session.Session{Messages: msgs}
	r := &diagnostic.InspectReport{}
	problems := mod.Detect(r, sess)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem for high ratio (6 user / 6 asst), got %d", len(problems))
	}
	p := problems[0]
	if p.ID != ProblemHighUserMsgRatio {
		t.Errorf("wrong problem ID: %s", p.ID)
	}
	if p.Category != diagnostic.CategoryPatterns {
		t.Errorf("wrong category: %s", p.Category)
	}
}

func TestModule_RegistersCleanly(t *testing.T) {
	// Verify the module implements the interface at compile time.
	var _ diagnostic.AnalysisModule = &Module{}
}
