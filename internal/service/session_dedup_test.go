package service

import (
	"context"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Fork dedup tests ──

// dedupMockStore extends mockStore with fork relation support.
type dedupMockStore struct {
	mockStore
	forkRelations []session.ForkRelation
}

func (m *dedupMockStore) SaveForkRelation(rel session.ForkRelation) error {
	// Upsert: replace if same original+fork pair exists.
	for i, existing := range m.forkRelations {
		if existing.OriginalID == rel.OriginalID && existing.ForkID == rel.ForkID {
			m.forkRelations[i] = rel
			return nil
		}
	}
	m.forkRelations = append(m.forkRelations, rel)
	return nil
}

func (m *dedupMockStore) ListAllForkRelations() ([]session.ForkRelation, error) {
	return m.forkRelations, nil
}

func (m *dedupMockStore) List(opts session.ListOptions) ([]session.Summary, error) {
	var result []session.Summary
	for _, s := range m.sessions {
		sm := session.Summary{
			ID:          s.ID,
			Provider:    s.Provider,
			Branch:      s.Branch,
			ProjectPath: s.ProjectPath,
			Summary:     s.Summary,
			TotalTokens: s.TokenUsage.TotalTokens,
			CreatedAt:   s.CreatedAt,
		}
		result = append(result, sm)
	}
	return result, nil
}

// TestComputeTokenBuckets_forkDedup verifies that messages in the shared
// prefix of a fork session are skipped during bucket computation.
func TestComputeTokenBuckets_forkDedup(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)

	// Original session: 4 messages, each with 100 input + 50 output tokens.
	original := &session.Session{
		ID: "ses_original", Provider: "opencode", ProjectPath: "/test/proj",
		CreatedAt: now.Add(-1 * time.Hour),
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "msg0", Timestamp: now.Add(-60 * time.Minute), InputTokens: 100, OutputTokens: 50, ProviderID: "anthropic"},
			{Role: session.RoleAssistant, Content: "reply0", Timestamp: now.Add(-55 * time.Minute), InputTokens: 100, OutputTokens: 50, ProviderID: "anthropic"},
			{Role: session.RoleUser, Content: "msg1", Timestamp: now.Add(-50 * time.Minute), InputTokens: 100, OutputTokens: 50, ProviderID: "anthropic"},
			{Role: session.RoleAssistant, Content: "reply1", Timestamp: now.Add(-45 * time.Minute), InputTokens: 100, OutputTokens: 50, ProviderID: "anthropic"},
		},
		TokenUsage: session.TokenUsage{TotalTokens: 600},
	}

	// Fork session: same 4 messages + 2 new ones. Fork point = 4.
	fork := &session.Session{
		ID: "ses_fork", Provider: "opencode", ProjectPath: "/test/proj",
		CreatedAt: now.Add(-30 * time.Minute),
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "msg0", Timestamp: now.Add(-60 * time.Minute), InputTokens: 100, OutputTokens: 50, ProviderID: "anthropic"},
			{Role: session.RoleAssistant, Content: "reply0", Timestamp: now.Add(-55 * time.Minute), InputTokens: 100, OutputTokens: 50, ProviderID: "anthropic"},
			{Role: session.RoleUser, Content: "msg1", Timestamp: now.Add(-50 * time.Minute), InputTokens: 100, OutputTokens: 50, ProviderID: "anthropic"},
			{Role: session.RoleAssistant, Content: "reply1", Timestamp: now.Add(-45 * time.Minute), InputTokens: 100, OutputTokens: 50, ProviderID: "anthropic"},
			// New messages after fork point:
			{Role: session.RoleUser, Content: "new_msg", Timestamp: now.Add(-30 * time.Minute), InputTokens: 200, OutputTokens: 100, ProviderID: "anthropic"},
			{Role: session.RoleAssistant, Content: "new_reply", Timestamp: now.Add(-25 * time.Minute), InputTokens: 200, OutputTokens: 100, ProviderID: "anthropic"},
		},
		TokenUsage: session.TokenUsage{TotalTokens: 1200},
	}

	store := &dedupMockStore{
		mockStore: mockStore{sessions: map[session.ID]*session.Session{
			original.ID: original,
			fork.ID:     fork,
		}},
		forkRelations: []session.ForkRelation{
			{
				OriginalID: "ses_original",
				ForkID:     "ses_fork",
				ForkPoint:  4, // first 4 messages are shared
			},
		},
	}

	svc := NewSessionService(SessionServiceConfig{
		Store:   store,
		Pricing: pricing.NewCalculator(),
	})

	result, err := svc.ComputeTokenBuckets(context.Background(), ComputeTokenBucketsRequest{
		Granularity: "1h",
	})
	if err != nil {
		t.Fatalf("ComputeTokenBuckets: %v", err)
	}

	if result.SessionsScanned != 2 {
		t.Errorf("SessionsScanned = %d, want 2", result.SessionsScanned)
	}

	// Without dedup: 4+6 = 10 messages scanned.
	// With dedup: 4 (original) + 2 (fork, skipping 4 shared) = 6 messages.
	if result.MessagesScanned != 6 {
		t.Errorf("MessagesScanned = %d, want 6 (4 original + 2 new fork msgs)", result.MessagesScanned)
	}

	// Without dedup total would be: 1200 input + 600 output = 1800 tokens across 10 msgs.
	// With dedup: 800 input + 400 output = 1200 tokens across 6 msgs.
	// The key verification is MessagesScanned=6 which proves dedup worked.
}

// TestComputeTokenBuckets_noForks verifies normal behavior when no forks exist.
func TestComputeTokenBuckets_noForks(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)

	sess := &session.Session{
		ID: "ses_normal", Provider: "opencode", ProjectPath: "/test/proj",
		CreatedAt: now.Add(-1 * time.Hour),
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "hello", Timestamp: now.Add(-30 * time.Minute), InputTokens: 100, OutputTokens: 50, ProviderID: "anthropic"},
			{Role: session.RoleAssistant, Content: "hi", Timestamp: now.Add(-25 * time.Minute), InputTokens: 100, OutputTokens: 50, ProviderID: "anthropic"},
		},
		TokenUsage: session.TokenUsage{TotalTokens: 300},
	}

	store := &dedupMockStore{
		mockStore: mockStore{sessions: map[session.ID]*session.Session{
			sess.ID: sess,
		}},
		// No fork relations.
	}

	svc := NewSessionService(SessionServiceConfig{
		Store:   store,
		Pricing: pricing.NewCalculator(),
	})

	result, err := svc.ComputeTokenBuckets(context.Background(), ComputeTokenBucketsRequest{
		Granularity: "1h",
	})
	if err != nil {
		t.Fatalf("ComputeTokenBuckets: %v", err)
	}

	if result.MessagesScanned != 2 {
		t.Errorf("MessagesScanned = %d, want 2", result.MessagesScanned)
	}
}

// TestDetectForksBatch_rewindDetection verifies that "Rewind of ses_XXX at message N"
// sessions are detected as forks.
func TestDetectForksBatch_rewindDetection(t *testing.T) {
	now := time.Now().UTC()

	original := &session.Session{
		ID: "ses_abc123", Provider: "opencode", ProjectPath: "/test/proj",
		CreatedAt: now.Add(-2 * time.Hour),
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "hello", Timestamp: now.Add(-120 * time.Minute), InputTokens: 100, OutputTokens: 50},
			{Role: session.RoleAssistant, Content: "hi", Timestamp: now.Add(-115 * time.Minute), InputTokens: 200, OutputTokens: 100},
			{Role: session.RoleUser, Content: "do stuff", Timestamp: now.Add(-110 * time.Minute), InputTokens: 300, OutputTokens: 150},
			{Role: session.RoleAssistant, Content: "done", Timestamp: now.Add(-105 * time.Minute), InputTokens: 400, OutputTokens: 200},
		},
		TokenUsage: session.TokenUsage{TotalTokens: 1500},
	}

	rewind := &session.Session{
		ID: "ses_rewind1", Provider: "opencode", ProjectPath: "/test/proj",
		Summary:   "Rewind of ses_abc123 at message 2",
		CreatedAt: now.Add(-1 * time.Hour),
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "hello", Timestamp: now.Add(-120 * time.Minute), InputTokens: 100, OutputTokens: 50},
			{Role: session.RoleAssistant, Content: "hi", Timestamp: now.Add(-115 * time.Minute), InputTokens: 200, OutputTokens: 100},
			{Role: session.RoleUser, Content: "different thing", Timestamp: now.Add(-60 * time.Minute), InputTokens: 500, OutputTokens: 250},
		},
		TokenUsage: session.TokenUsage{TotalTokens: 1200},
	}

	store := &dedupMockStore{
		mockStore: mockStore{sessions: map[session.ID]*session.Session{
			original.ID: original,
			rewind.ID:   rewind,
		}},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.DetectForksBatch(context.Background())
	if err != nil {
		t.Fatalf("DetectForksBatch: %v", err)
	}

	if result.ForksDetected != 1 {
		t.Errorf("ForksDetected = %d, want 1", result.ForksDetected)
	}
	if result.RelationsSaved != 1 {
		t.Errorf("RelationsSaved = %d, want 1", result.RelationsSaved)
	}

	// Verify the saved relation.
	if len(store.forkRelations) != 1 {
		t.Fatalf("forkRelations = %d, want 1", len(store.forkRelations))
	}
	rel := store.forkRelations[0]
	if rel.OriginalID != "ses_abc123" {
		t.Errorf("OriginalID = %q, want %q", rel.OriginalID, "ses_abc123")
	}
	if rel.ForkID != "ses_rewind1" {
		t.Errorf("ForkID = %q, want %q", rel.ForkID, "ses_rewind1")
	}
	if rel.ForkPoint != 2 {
		t.Errorf("ForkPoint = %d, want 2", rel.ForkPoint)
	}
	// Shared tokens: messages 0 and 1 from original.
	// msg0: 100 input + 50 output, msg1: 200 input + 100 output.
	wantSharedInput := 100 + 200
	wantSharedOutput := 50 + 100
	if rel.SharedInputTokens != wantSharedInput {
		t.Errorf("SharedInputTokens = %d, want %d", rel.SharedInputTokens, wantSharedInput)
	}
	if rel.SharedOutputTokens != wantSharedOutput {
		t.Errorf("SharedOutputTokens = %d, want %d", rel.SharedOutputTokens, wantSharedOutput)
	}
}

// TestDetectForksBatch_branchlessSessions verifies that sessions without a branch
// are now included in fork detection (previously they were skipped).
func TestDetectForksBatch_branchlessSessions(t *testing.T) {
	now := time.Now().UTC()

	// Two branchless sessions with identical message prefixes (fork scenario).
	s1 := &session.Session{
		ID: "ses_a", Provider: "opencode", ProjectPath: "/test/proj",
		// No branch set!
		CreatedAt: now.Add(-2 * time.Hour),
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "implement auth", Timestamp: now.Add(-120 * time.Minute), InputTokens: 100, OutputTokens: 50},
			{Role: session.RoleAssistant, Content: "sure, here is auth", Timestamp: now.Add(-115 * time.Minute), InputTokens: 200, OutputTokens: 100},
			{Role: session.RoleUser, Content: "add tests", Timestamp: now.Add(-110 * time.Minute), InputTokens: 150, OutputTokens: 75},
			{Role: session.RoleAssistant, Content: "here are tests", Timestamp: now.Add(-105 * time.Minute), InputTokens: 250, OutputTokens: 125},
		},
		TokenUsage: session.TokenUsage{TotalTokens: 1050},
	}

	s2 := &session.Session{
		ID: "ses_b", Provider: "opencode", ProjectPath: "/test/proj",
		// No branch set!
		CreatedAt: now.Add(-1 * time.Hour),
		Messages: []session.Message{
			// Same first 4 messages as s1:
			{Role: session.RoleUser, Content: "implement auth", Timestamp: now.Add(-120 * time.Minute), InputTokens: 100, OutputTokens: 50},
			{Role: session.RoleAssistant, Content: "sure, here is auth", Timestamp: now.Add(-115 * time.Minute), InputTokens: 200, OutputTokens: 100},
			{Role: session.RoleUser, Content: "add tests", Timestamp: now.Add(-110 * time.Minute), InputTokens: 150, OutputTokens: 75},
			{Role: session.RoleAssistant, Content: "here are tests", Timestamp: now.Add(-105 * time.Minute), InputTokens: 250, OutputTokens: 125},
			// Plus a new message:
			{Role: session.RoleUser, Content: "deploy to prod", Timestamp: now.Add(-60 * time.Minute), InputTokens: 300, OutputTokens: 150},
		},
		TokenUsage: session.TokenUsage{TotalTokens: 1500},
	}

	store := &dedupMockStore{
		mockStore: mockStore{sessions: map[session.ID]*session.Session{
			s1.ID: s1,
			s2.ID: s2,
		}},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.DetectForksBatch(context.Background())
	if err != nil {
		t.Fatalf("DetectForksBatch: %v", err)
	}

	// Should detect the fork between s1 and s2 (branchless).
	if result.ForksDetected != 1 {
		t.Errorf("ForksDetected = %d, want 1 (branchless sessions should be compared)", result.ForksDetected)
	}
	if len(store.forkRelations) != 1 {
		t.Fatalf("forkRelations = %d, want 1", len(store.forkRelations))
	}

	rel := store.forkRelations[0]
	// s1 has 4 msgs, s2 has 5 → s1 is shorter → s1 is original.
	if rel.OriginalID != "ses_a" {
		t.Errorf("OriginalID = %q, want %q (shorter session)", rel.OriginalID, "ses_a")
	}
	if rel.ForkID != "ses_b" {
		t.Errorf("ForkID = %q, want %q (longer session)", rel.ForkID, "ses_b")
	}
	if rel.ForkPoint != 4 {
		t.Errorf("ForkPoint = %d, want 4", rel.ForkPoint)
	}
}
