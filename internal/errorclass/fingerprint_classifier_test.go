package errorclass

import (
	"fmt"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── mock store for FingerprintClassifier ──

type fpMockStore struct {
	groups map[string]session.ErrorFingerprintGroup
}

func (m *fpMockStore) SaveErrors(_ []session.SessionError) error { return nil }
func (m *fpMockStore) GetErrors(_ session.ID) ([]session.SessionError, error) {
	return nil, nil
}
func (m *fpMockStore) GetErrorSummary(_ session.ID) (*session.SessionErrorSummary, error) {
	return nil, nil
}
func (m *fpMockStore) ListRecentErrors(_ int, _ session.ErrorCategory) ([]session.SessionError, error) {
	return nil, nil
}
func (m *fpMockStore) UpsertFingerprint(_ session.ErrorFingerprintGroup) error { return nil }
func (m *fpMockStore) ListFingerprintGroups(_ bool, _ int) ([]session.ErrorFingerprintGroup, error) {
	return nil, nil
}
func (m *fpMockStore) GetFingerprintGroup(_ string) (*session.ErrorFingerprintGroup, error) {
	return nil, nil
}
func (m *fpMockStore) ClassifyFingerprintGroup(_ string, _ session.ErrorCategory, _ string, _ string) error {
	return nil
}

func (m *fpMockStore) GetFingerprintMatch(fp string) (*session.ErrorFingerprintGroup, error) {
	g, ok := m.groups[fp]
	if !ok {
		return nil, nil
	}
	if g.Category == "" || g.Category == session.ErrorCategoryUnknown {
		return nil, nil
	}
	return &g, nil
}

// ── Tests ──

func TestFingerprintClassifier_Name(t *testing.T) {
	c := NewFingerprintClassifier(FingerprintClassifierConfig{})
	if got := c.Name(); got != "fingerprint" {
		t.Errorf("Name() = %q, want %q", got, "fingerprint")
	}
}

func TestFingerprintClassifier_MatchFound(t *testing.T) {
	store := &fpMockStore{
		groups: map[string]session.ErrorFingerprintGroup{
			"fp-net": {
				Fingerprint: "fp-net",
				Category:    session.ErrorCategoryNetworkError,
				Message:     "AWS region connectivity",
			},
		},
	}
	c := NewFingerprintClassifier(FingerprintClassifierConfig{Store: store})

	err := session.SessionError{
		ID:          "err-1",
		Fingerprint: "fp-net",
		RawError:    "Unable to connect to region us-east-1",
	}

	result := c.Classify(err)

	if result.Category != session.ErrorCategoryNetworkError {
		t.Errorf("Category = %q, want %q", result.Category, session.ErrorCategoryNetworkError)
	}
	if result.Message != "AWS region connectivity" {
		t.Errorf("Message = %q, want %q", result.Message, "AWS region connectivity")
	}
	if result.Confidence != "high" {
		t.Errorf("Confidence = %q, want %q", result.Confidence, "high")
	}
	if result.Source != session.ErrorSourceProvider {
		t.Errorf("Source = %q, want %q", result.Source, session.ErrorSourceProvider)
	}
	// Original fields preserved.
	if result.ID != "err-1" {
		t.Errorf("ID changed to %q", result.ID)
	}
	if result.RawError != "Unable to connect to region us-east-1" {
		t.Errorf("RawError changed")
	}
}

func TestFingerprintClassifier_NoMatch(t *testing.T) {
	store := &fpMockStore{groups: map[string]session.ErrorFingerprintGroup{}}
	c := NewFingerprintClassifier(FingerprintClassifierConfig{Store: store})

	err := session.SessionError{
		ID:          "err-2",
		Fingerprint: "fp-unknown",
	}

	result := c.Classify(err)
	if result.Category != session.ErrorCategoryUnknown {
		t.Errorf("Category = %q, want %q", result.Category, session.ErrorCategoryUnknown)
	}
}

func TestFingerprintClassifier_EmptyFingerprint(t *testing.T) {
	store := &fpMockStore{groups: map[string]session.ErrorFingerprintGroup{}}
	c := NewFingerprintClassifier(FingerprintClassifierConfig{Store: store})

	err := session.SessionError{
		ID:          "err-3",
		Fingerprint: "",
	}

	result := c.Classify(err)
	if result.Category != session.ErrorCategoryUnknown {
		t.Errorf("Category = %q, want %q for empty fingerprint", result.Category, session.ErrorCategoryUnknown)
	}
}

func TestFingerprintClassifier_NilStore(t *testing.T) {
	c := NewFingerprintClassifier(FingerprintClassifierConfig{Store: nil})

	err := session.SessionError{
		ID:          "err-4",
		Fingerprint: "fp-test",
	}

	result := c.Classify(err)
	if result.Category != session.ErrorCategoryUnknown {
		t.Errorf("Category = %q, want %q for nil store", result.Category, session.ErrorCategoryUnknown)
	}
}

func TestFingerprintClassifier_SourceInference(t *testing.T) {
	tests := []struct {
		category   session.ErrorCategory
		wantSource session.ErrorSource
	}{
		{session.ErrorCategoryProviderError, session.ErrorSourceProvider},
		{session.ErrorCategoryRateLimit, session.ErrorSourceProvider},
		{session.ErrorCategoryContextOverflow, session.ErrorSourceProvider},
		{session.ErrorCategoryAuthError, session.ErrorSourceProvider},
		{session.ErrorCategoryNetworkError, session.ErrorSourceProvider},
		{session.ErrorCategoryToolError, session.ErrorSourceTool},
		{session.ErrorCategoryAborted, session.ErrorSourceClient},
		{session.ErrorCategoryValidation, session.ErrorSourceClient}, // default fallback
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("category_%s", tt.category), func(t *testing.T) {
			store := &fpMockStore{
				groups: map[string]session.ErrorFingerprintGroup{
					"fp-src": {
						Fingerprint: "fp-src",
						Category:    tt.category,
						Message:     "test",
					},
				},
			}
			c := NewFingerprintClassifier(FingerprintClassifierConfig{Store: store})

			err := session.SessionError{
				ID:          "err-src",
				Fingerprint: "fp-src",
			}

			result := c.Classify(err)
			if result.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q for category %q", result.Source, tt.wantSource, tt.category)
			}
		})
	}
}

func TestFingerprintClassifier_InComposite(t *testing.T) {
	// Simulate the real chain: deterministic → fingerprint → (no LLM)
	// An error that deterministic can't classify but fingerprint can.
	// Use a unique provider-specific string that no deterministic pattern matches.
	store := &fpMockStore{
		groups: map[string]session.ErrorFingerprintGroup{
			"fp-custom": {
				Fingerprint: "fp-custom",
				Category:    session.ErrorCategoryToolError,
				Message:     "known flaky tool",
			},
		},
	}

	composite := NewCompositeClassifier(CompositeClassifierConfig{
		Classifiers: []session.ErrorClassifier{
			NewDeterministicClassifier(),
			NewFingerprintClassifier(FingerprintClassifierConfig{Store: store}),
		},
	})

	// Error with no HTTP status and a raw message that matches zero deterministic
	// patterns — so the fingerprint classifier must be the one to claim it.
	err := session.SessionError{
		ID:          "err-composite",
		Fingerprint: "fp-custom",
		RawError:    "FluxCapacitorMisalignment: jigawatts off by 0.21",
	}

	result := composite.Classify(err)
	if result.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q (fingerprint should have matched)", result.Category, session.ErrorCategoryToolError)
	}
	if result.Message != "known flaky tool" {
		t.Errorf("Message = %q, want %q", result.Message, "known flaky tool")
	}
}
