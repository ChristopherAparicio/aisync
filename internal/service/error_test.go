package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── mockErrorStore ──

type mockErrorStore struct {
	saved   []session.SessionError
	errors  map[session.ID][]session.SessionError
	saveErr error
}

func newMockErrorStore() *mockErrorStore {
	return &mockErrorStore{
		errors: make(map[session.ID][]session.SessionError),
	}
}

func (m *mockErrorStore) SaveErrors(errors []session.SessionError) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saved = append(m.saved, errors...)
	for _, e := range errors {
		m.errors[e.SessionID] = append(m.errors[e.SessionID], e)
	}
	return nil
}

func (m *mockErrorStore) GetErrors(sessionID session.ID) ([]session.SessionError, error) {
	return m.errors[sessionID], nil
}

func (m *mockErrorStore) GetErrorSummary(sessionID session.ID) (*session.SessionErrorSummary, error) {
	errs := m.errors[sessionID]
	summary := session.NewSessionErrorSummary(sessionID, errs)
	return &summary, nil
}

func (m *mockErrorStore) ListRecentErrors(limit int, category session.ErrorCategory) ([]session.SessionError, error) {
	var all []session.SessionError
	for _, errs := range m.errors {
		for _, e := range errs {
			if category != "" && e.Category != category {
				continue
			}
			all = append(all, e)
		}
	}
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	return all, nil
}

// ── mockClassifier ──

type mockClassifier struct {
	name   string
	called int
}

func (m *mockClassifier) Classify(err session.SessionError) session.SessionError {
	m.called++
	// Simple classification: HTTP 500 → provider_error, else unknown
	if err.HTTPStatus >= 500 {
		err.Category = session.ErrorCategoryProviderError
		err.Source = session.ErrorSourceProvider
		err.Confidence = "high"
	} else if err.HTTPStatus == 429 {
		err.Category = session.ErrorCategoryRateLimit
		err.Source = session.ErrorSourceProvider
		err.Confidence = "high"
	} else if err.ToolName != "" {
		err.Category = session.ErrorCategoryToolError
		err.Source = session.ErrorSourceTool
		err.Confidence = "high"
	} else {
		err.Category = session.ErrorCategoryUnknown
		err.Source = session.ErrorSourceClient
		err.Confidence = "low"
	}
	return err
}

func (m *mockClassifier) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock"
}

// ── Tests ──

func TestErrorService_ProcessSession_classifiesAndSaves(t *testing.T) {
	store := newMockErrorStore()
	classifier := &mockClassifier{}
	svc := NewErrorService(ErrorServiceConfig{
		Store:      store,
		Classifier: classifier,
	})

	sess := &session.Session{
		ID: "test-sess-1",
		Errors: []session.SessionError{
			{
				ID:         "err-1",
				SessionID:  "test-sess-1",
				HTTPStatus: 500,
				RawError:   "Internal server error",
				OccurredAt: time.Now(),
			},
			{
				ID:         "err-2",
				SessionID:  "test-sess-1",
				HTTPStatus: 429,
				RawError:   "Rate limit exceeded",
				OccurredAt: time.Now(),
			},
			{
				ID:         "err-3",
				SessionID:  "test-sess-1",
				ToolName:   "bash",
				RawError:   "exit code 1",
				OccurredAt: time.Now(),
			},
		},
	}

	result, err := svc.ProcessSession(sess)
	if err != nil {
		t.Fatalf("ProcessSession() error: %v", err)
	}

	if result.Processed != 3 {
		t.Errorf("Processed = %d, want 3", result.Processed)
	}
	if result.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", result.Skipped)
	}
	if classifier.called != 3 {
		t.Errorf("classifier called %d times, want 3", classifier.called)
	}

	// Check categories
	if result.ByCategory[session.ErrorCategoryProviderError] != 1 {
		t.Errorf("provider_error count = %d, want 1", result.ByCategory[session.ErrorCategoryProviderError])
	}
	if result.ByCategory[session.ErrorCategoryRateLimit] != 1 {
		t.Errorf("rate_limit count = %d, want 1", result.ByCategory[session.ErrorCategoryRateLimit])
	}
	if result.ByCategory[session.ErrorCategoryToolError] != 1 {
		t.Errorf("tool_error count = %d, want 1", result.ByCategory[session.ErrorCategoryToolError])
	}

	// Check persistence
	if len(store.saved) != 3 {
		t.Errorf("saved count = %d, want 3", len(store.saved))
	}
}

func TestErrorService_ProcessSession_skipsAlreadyClassified(t *testing.T) {
	store := newMockErrorStore()
	classifier := &mockClassifier{}
	svc := NewErrorService(ErrorServiceConfig{
		Store:      store,
		Classifier: classifier,
	})

	sess := &session.Session{
		ID: "test-sess-2",
		Errors: []session.SessionError{
			{
				ID:         "err-pre",
				SessionID:  "test-sess-2",
				Category:   session.ErrorCategoryProviderError, // already classified
				Source:     session.ErrorSourceProvider,
				HTTPStatus: 500,
				OccurredAt: time.Now(),
			},
			{
				ID:         "err-raw",
				SessionID:  "test-sess-2",
				HTTPStatus: 429,
				RawError:   "Rate limit",
				OccurredAt: time.Now(),
			},
		},
	}

	result, err := svc.ProcessSession(sess)
	if err != nil {
		t.Fatalf("ProcessSession() error: %v", err)
	}

	if result.Processed != 1 {
		t.Errorf("Processed = %d, want 1", result.Processed)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
	if classifier.called != 1 {
		t.Errorf("classifier called %d times, want 1 (should skip pre-classified)", classifier.called)
	}
}

func TestErrorService_ProcessSession_noErrors(t *testing.T) {
	store := newMockErrorStore()
	classifier := &mockClassifier{}
	svc := NewErrorService(ErrorServiceConfig{
		Store:      store,
		Classifier: classifier,
	})

	sess := &session.Session{ID: "empty-sess"}

	result, err := svc.ProcessSession(sess)
	if err != nil {
		t.Fatalf("ProcessSession() error: %v", err)
	}

	if result.Processed != 0 {
		t.Errorf("Processed = %d, want 0", result.Processed)
	}
	if result.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", result.Skipped)
	}
	if classifier.called != 0 {
		t.Errorf("classifier should not be called for empty errors")
	}
	if len(store.saved) != 0 {
		t.Errorf("nothing should be saved for empty errors")
	}
}

func TestErrorService_ProcessSession_nilSession(t *testing.T) {
	svc := NewErrorService(ErrorServiceConfig{
		Store:      newMockErrorStore(),
		Classifier: &mockClassifier{},
	})

	_, err := svc.ProcessSession(nil)
	if err == nil {
		t.Fatal("expected error for nil session")
	}
}

func TestErrorService_ProcessSession_nilClassifier(t *testing.T) {
	svc := NewErrorService(ErrorServiceConfig{
		Store: newMockErrorStore(),
	})

	_, err := svc.ProcessSession(&session.Session{ID: "test"})
	if err == nil {
		t.Fatal("expected error for nil classifier")
	}
}

func TestErrorService_ProcessSession_nilStore(t *testing.T) {
	svc := NewErrorService(ErrorServiceConfig{
		Classifier: &mockClassifier{},
	})

	_, err := svc.ProcessSession(&session.Session{ID: "test"})
	if err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestErrorService_ProcessSession_saveError(t *testing.T) {
	store := newMockErrorStore()
	store.saveErr = fmt.Errorf("disk full")
	svc := NewErrorService(ErrorServiceConfig{
		Store:      store,
		Classifier: &mockClassifier{},
	})

	sess := &session.Session{
		ID: "fail-sess",
		Errors: []session.SessionError{
			{ID: "e1", SessionID: "fail-sess", HTTPStatus: 500, OccurredAt: time.Now()},
		},
	}

	_, err := svc.ProcessSession(sess)
	if err == nil {
		t.Fatal("expected error when store fails")
	}
}

func TestErrorService_GetErrors(t *testing.T) {
	store := newMockErrorStore()
	store.errors["sess-x"] = []session.SessionError{
		{ID: "e1", SessionID: "sess-x", Category: session.ErrorCategoryProviderError},
		{ID: "e2", SessionID: "sess-x", Category: session.ErrorCategoryToolError},
	}

	svc := NewErrorService(ErrorServiceConfig{
		Store:      store,
		Classifier: &mockClassifier{},
	})

	errors, err := svc.GetErrors("sess-x")
	if err != nil {
		t.Fatalf("GetErrors() error: %v", err)
	}
	if len(errors) != 2 {
		t.Errorf("got %d errors, want 2", len(errors))
	}
}

func TestErrorService_GetSummary(t *testing.T) {
	store := newMockErrorStore()
	now := time.Now()
	store.errors["sess-y"] = []session.SessionError{
		{ID: "e1", SessionID: "sess-y", Category: session.ErrorCategoryProviderError, Source: session.ErrorSourceProvider, OccurredAt: now.Add(-10 * time.Minute)},
		{ID: "e2", SessionID: "sess-y", Category: session.ErrorCategoryProviderError, Source: session.ErrorSourceProvider, OccurredAt: now.Add(-5 * time.Minute)},
		{ID: "e3", SessionID: "sess-y", Category: session.ErrorCategoryToolError, Source: session.ErrorSourceTool, OccurredAt: now},
	}

	svc := NewErrorService(ErrorServiceConfig{
		Store:      store,
		Classifier: &mockClassifier{},
	})

	summary, err := svc.GetSummary("sess-y")
	if err != nil {
		t.Fatalf("GetSummary() error: %v", err)
	}

	if summary.TotalErrors != 3 {
		t.Errorf("TotalErrors = %d, want 3", summary.TotalErrors)
	}
	if summary.ExternalErrors != 2 {
		t.Errorf("ExternalErrors = %d, want 2 (provider_error is external)", summary.ExternalErrors)
	}
	if summary.InternalErrors != 1 {
		t.Errorf("InternalErrors = %d, want 1", summary.InternalErrors)
	}
}

func TestErrorService_ListRecent(t *testing.T) {
	store := newMockErrorStore()
	store.errors["s1"] = []session.SessionError{
		{ID: "e1", SessionID: "s1", Category: session.ErrorCategoryProviderError},
		{ID: "e2", SessionID: "s1", Category: session.ErrorCategoryToolError},
	}
	store.errors["s2"] = []session.SessionError{
		{ID: "e3", SessionID: "s2", Category: session.ErrorCategoryProviderError},
	}

	svc := NewErrorService(ErrorServiceConfig{
		Store:      store,
		Classifier: &mockClassifier{},
	})

	// All errors
	all, err := svc.ListRecent(0, "")
	if err != nil {
		t.Fatalf("ListRecent() error: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("got %d errors, want 3", len(all))
	}

	// Filter by category
	providers, err := svc.ListRecent(0, session.ErrorCategoryProviderError)
	if err != nil {
		t.Fatalf("ListRecent(provider_error) error: %v", err)
	}
	if len(providers) != 2 {
		t.Errorf("got %d provider errors, want 2", len(providers))
	}

	// With limit
	limited, err := svc.ListRecent(1, "")
	if err != nil {
		t.Fatalf("ListRecent(limit=1) error: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("got %d errors with limit=1, want 1", len(limited))
	}
}

func TestErrorService_UnknownCategoryIsReclassified(t *testing.T) {
	store := newMockErrorStore()
	classifier := &mockClassifier{}
	svc := NewErrorService(ErrorServiceConfig{
		Store:      store,
		Classifier: classifier,
	})

	sess := &session.Session{
		ID: "reclass-sess",
		Errors: []session.SessionError{
			{
				ID:         "err-unknown",
				SessionID:  "reclass-sess",
				Category:   session.ErrorCategoryUnknown, // unknown should be re-classified
				HTTPStatus: 500,
				OccurredAt: time.Now(),
			},
		},
	}

	result, err := svc.ProcessSession(sess)
	if err != nil {
		t.Fatalf("ProcessSession() error: %v", err)
	}

	// "unknown" category should be re-classified, not skipped
	if result.Processed != 1 {
		t.Errorf("Processed = %d, want 1 (unknown should be re-classified)", result.Processed)
	}
	if result.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", result.Skipped)
	}
	if classifier.called != 1 {
		t.Errorf("classifier called %d times, want 1", classifier.called)
	}
}
