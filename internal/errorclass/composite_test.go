package errorclass_test

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/errorclass"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// stubClassifier returns a fixed result for testing.
type stubClassifier struct {
	name     string
	category session.ErrorCategory
}

func (s *stubClassifier) Classify(err session.SessionError) session.SessionError {
	if err.Category != "" && err.Category != session.ErrorCategoryUnknown {
		return err
	}
	err.Category = s.category
	err.Message = "classified by " + s.name
	err.Confidence = "high"
	return err
}

func (s *stubClassifier) Name() string { return s.name }

func TestCompositeClassifier_Name(t *testing.T) {
	c := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{})
	if c.Name() != "composite" {
		t.Errorf("Name() = %q, want %q", c.Name(), "composite")
	}
}

func TestCompositeClassifier_AlreadyClassified(t *testing.T) {
	c := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{
		Classifiers: []session.ErrorClassifier{
			&stubClassifier{name: "stub", category: session.ErrorCategoryRateLimit},
		},
	})

	err := c.Classify(session.SessionError{
		Category: session.ErrorCategoryToolError,
		Message:  "Already classified",
	})

	if err.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q (should not override)", err.Category, session.ErrorCategoryToolError)
	}
}

func TestCompositeClassifier_FirstClassifierWins(t *testing.T) {
	c := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{
		Classifiers: []session.ErrorClassifier{
			&stubClassifier{name: "first", category: session.ErrorCategoryProviderError},
			&stubClassifier{name: "second", category: session.ErrorCategoryRateLimit},
		},
	})

	err := c.Classify(session.SessionError{
		ID:       "err-1",
		RawError: "some error",
	})

	if err.Category != session.ErrorCategoryProviderError {
		t.Errorf("Category = %q, want %q (first should win)", err.Category, session.ErrorCategoryProviderError)
	}
	if err.Message != "classified by first" {
		t.Errorf("Message = %q, want %q", err.Message, "classified by first")
	}
}

func TestCompositeClassifier_FallsToSecondOnUnknown(t *testing.T) {
	c := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{
		Classifiers: []session.ErrorClassifier{
			&stubClassifier{name: "deterministic", category: session.ErrorCategoryUnknown},
			&stubClassifier{name: "llm", category: session.ErrorCategoryToolError},
		},
	})

	err := c.Classify(session.SessionError{
		ID:       "err-2",
		RawError: "ambiguous error",
	})

	if err.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q (should fall to second)", err.Category, session.ErrorCategoryToolError)
	}
	if err.Message != "classified by llm" {
		t.Errorf("Message = %q, want %q", err.Message, "classified by llm")
	}
}

func TestCompositeClassifier_AllUnknown(t *testing.T) {
	c := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{
		Classifiers: []session.ErrorClassifier{
			&stubClassifier{name: "first", category: session.ErrorCategoryUnknown},
			&stubClassifier{name: "second", category: session.ErrorCategoryUnknown},
		},
	})

	err := c.Classify(session.SessionError{
		ID:       "err-3",
		RawError: "truly unknown",
	})

	if err.Category != session.ErrorCategoryUnknown {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryUnknown)
	}
	if err.Confidence != "low" {
		t.Errorf("Confidence = %q, want %q", err.Confidence, "low")
	}
}

func TestCompositeClassifier_NoClassifiers(t *testing.T) {
	c := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{
		Classifiers: nil,
	})

	err := c.Classify(session.SessionError{
		ID:       "err-4",
		RawError: "no classifiers at all",
	})

	if err.Category != session.ErrorCategoryUnknown {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryUnknown)
	}
	if err.Message != "Unclassified error" {
		t.Errorf("Message = %q, want %q", err.Message, "Unclassified error")
	}
}

func TestCompositeClassifier_DeterministicPlusLLM_RealFlow(t *testing.T) {
	// Simulate the real composite flow: deterministic first, LLM fallback.
	det := errorclass.NewDeterministicClassifier()

	// Use a stub for the LLM part.
	llmStub := &stubClassifier{name: "llm", category: session.ErrorCategoryValidation}

	c := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{
		Classifiers: []session.ErrorClassifier{det, llmStub},
	})

	// Case 1: Deterministic handles HTTP 500 — LLM never called.
	err1 := c.Classify(session.SessionError{
		ID:         "err-det",
		HTTPStatus: 500,
	})
	if err1.Category != session.ErrorCategoryProviderError {
		t.Errorf("Case 1: Category = %q, want %q", err1.Category, session.ErrorCategoryProviderError)
	}

	// Case 2: Deterministic returns unknown — LLM fallback.
	err2 := c.Classify(session.SessionError{
		ID:       "err-llm",
		RawError: "some totally novel error nobody has seen before",
	})
	if err2.Category != session.ErrorCategoryValidation {
		t.Errorf("Case 2: Category = %q, want %q (LLM should handle)", err2.Category, session.ErrorCategoryValidation)
	}
}

func TestCompositeClassifier_PreservesIdentityFields(t *testing.T) {
	c := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{
		Classifiers: []session.ErrorClassifier{
			&stubClassifier{name: "classifier", category: session.ErrorCategoryRateLimit},
		},
	})

	err := c.Classify(session.SessionError{
		ID:        "err-5",
		SessionID: "sess-xyz",
		ToolName:  "Read",
		RawError:  "original raw error",
	})

	if err.ID != "err-5" {
		t.Errorf("ID = %q, want %q", err.ID, "err-5")
	}
	if err.SessionID != "sess-xyz" {
		t.Errorf("SessionID = %q, want %q", err.SessionID, "sess-xyz")
	}
	if err.ToolName != "Read" {
		t.Errorf("ToolName = %q, want %q", err.ToolName, "Read")
	}
}
