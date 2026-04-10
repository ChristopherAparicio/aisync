package capture

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/secrets"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

func TestCapture_autoDetect(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:          "ses-1",
			Provider:    session.ProviderClaudeCode,
			Agent:       "claude",
			Branch:      "main",
			ProjectPath: "/test/project",
			StorageMode: session.StorageModeCompact,
			Summary:     "Test session",
			Messages:    []session.Message{{ID: "m1", Role: session.RoleUser, Content: "hello"}},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.Session.ID != "ses-1" {
		t.Errorf("Session.ID = %q, want %q", result.Session.ID, "ses-1")
	}
	if result.Provider != session.ProviderClaudeCode {
		t.Errorf("Provider = %q, want %q", result.Provider, session.ProviderClaudeCode)
	}

	// captureOne no longer calls Save() — the service layer handles persistence.
	// Verify that captureOne returned the session without saving.
	if store.SaveCount != 0 {
		t.Errorf("Save() called %d times, want 0 (deferred to service layer)", store.SaveCount)
	}

	// Verify branch link was added
	var hasBranchLink bool
	for _, link := range result.Session.Links {
		if link.LinkType == session.LinkBranch && link.Ref == "main" {
			hasBranchLink = true
		}
	}
	if !hasBranchLink {
		t.Error("Branch link not added to session")
	}
}

func TestCapture_explicitProvider(t *testing.T) {
	store := testutil.NewMockStore()
	claude := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "claude-1", Provider: session.ProviderClaudeCode, Branch: "feat"},
		},
		exportSession: &session.Session{
			ID:       "claude-1",
			Provider: session.ProviderClaudeCode,
		},
	}
	opencode := &mockProvider{
		name: session.ProviderOpenCode,
		sessions: []session.Summary{
			{ID: "oc-1", Provider: session.ProviderOpenCode, CreatedAt: time.Now()},
		},
	}

	reg := provider.NewRegistry(claude, opencode)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test/project",
		Branch:       "feat",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderClaudeCode,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.Session.ID != "claude-1" {
		t.Errorf("Session.ID = %q, want %q", result.Session.ID, "claude-1")
	}
}

func TestCapture_messageOverride(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:      "ses-1",
			Summary: "Original summary",
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
		Message:     "Custom summary",
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.Session.Summary != "Custom summary" {
		t.Errorf("Summary = %q, want %q", result.Session.Summary, "Custom summary")
	}
}

func TestCapture_withScanner_maskMode(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:       "ses-1",
			Provider: session.ProviderClaudeCode,
			Messages: []session.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: session.RoleUser},
			},
		},
	}

	sc := secrets.NewScanner(session.SecretModeMask, nil)
	reg := provider.NewRegistry(prov)
	svc := NewServiceWithScanner(reg, store, sc)

	result, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.SecretsFound == 0 {
		t.Error("expected secrets to be found")
	}
	// Verify masking was applied
	content := result.Session.Messages[0].Content
	if content == "Here is AKIAIOSFODNN7EXAMPLE" {
		t.Error("content should be masked but was not modified")
	}
}

func TestCapture_withScanner_blockMode(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:       "ses-1",
			Provider: session.ProviderClaudeCode,
			Messages: []session.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: session.RoleUser},
			},
		},
	}

	sc := secrets.NewScanner(session.SecretModeBlock, nil)
	reg := provider.NewRegistry(prov)
	svc := NewServiceWithScanner(reg, store, sc)

	_, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err == nil {
		t.Fatal("Capture() should return error in block mode when secrets found")
	}
	if store.LastSaved != nil {
		t.Error("session should NOT be saved in block mode")
	}
}

func TestCapture_withScanner_warnMode(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:       "ses-1",
			Provider: session.ProviderClaudeCode,
			Messages: []session.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: session.RoleUser},
			},
		},
	}

	sc := secrets.NewScanner(session.SecretModeWarn, nil)
	reg := provider.NewRegistry(prov)
	svc := NewServiceWithScanner(reg, store, sc)

	result, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.SecretsFound == 0 {
		t.Error("expected secrets to be reported")
	}
	// In warn mode, content should NOT be masked
	if result.Session.Messages[0].Content != "Here is AKIAIOSFODNN7EXAMPLE" {
		t.Error("content should NOT be masked in warn mode")
	}
}

func TestCapture_noScanner(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:       "ses-1",
			Provider: session.ProviderClaudeCode,
			Messages: []session.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: session.RoleUser},
			},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store) // No scanner

	result, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.SecretsFound != 0 {
		t.Error("expected 0 secrets when no scanner configured")
	}
}

func TestCapture_multiSession(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-first", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:          "ses-first",
			Provider:    session.ProviderClaudeCode,
			Branch:      "main",
			ProjectPath: "/test/project",
			Messages:    []session.Message{{ID: "m1", Role: session.RoleUser, Content: "hello"}},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	// First capture
	result1, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("First Capture() error: %v", err)
	}
	firstID := result1.Session.ID

	// Second capture with a different provider session ID
	prov.sessions = []session.Summary{
		{ID: "ses-second", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
	}
	prov.exportSession = &session.Session{
		ID:          "ses-second",
		Provider:    session.ProviderClaudeCode,
		Branch:      "main",
		ProjectPath: "/test/project",
		Summary:     "Updated session",
		Messages:    []session.Message{{ID: "m2", Role: session.RoleUser, Content: "world"}},
	}

	result2, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Second Capture() error: %v", err)
	}

	// Multi-session: each capture should produce a distinct ID
	if result2.Session.ID == firstID {
		t.Errorf("Second capture ID = %q, should differ from first (no dedup)", firstID)
	}

	// captureOne no longer calls Save() — the service layer handles persistence.
	if store.SaveCount != 0 {
		t.Errorf("saveCount = %d, want 0 (deferred to service layer)", store.SaveCount)
	}

	// Summary should reflect the second session
	if result2.Session.Summary != "Updated session" {
		t.Errorf("Summary = %q, want %q", result2.Session.Summary, "Updated session")
	}
}

func TestCaptureAll_explicitProvider(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderOpenCode,
		sessions: []session.Summary{
			{ID: "oc-1", Provider: session.ProviderOpenCode, CreatedAt: time.Now()},
			{ID: "oc-2", Provider: session.ProviderOpenCode, CreatedAt: time.Now().Add(-time.Hour)},
			{ID: "oc-3", Provider: session.ProviderOpenCode, CreatedAt: time.Now().Add(-2 * time.Hour)},
		},
		exportSessions: map[session.ID]*session.Session{
			"oc-1": {ID: "oc-1", Provider: session.ProviderOpenCode, Messages: []session.Message{{ID: "m1", Content: "hello"}}},
			"oc-2": {ID: "oc-2", Provider: session.ProviderOpenCode, Messages: []session.Message{{ID: "m2", Content: "world"}}},
			"oc-3": {ID: "oc-3", Provider: session.ProviderOpenCode, Messages: []session.Message{{ID: "m3", Content: "foo"}}},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	results, err := svc.CaptureAll(Request{
		ProjectPath:  "/test/project",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	})
	if err != nil {
		t.Fatalf("CaptureAll() error: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("CaptureAll() returned %d results, want 3", len(results))
	}

	// captureOne no longer calls Save() — the service layer handles persistence.
	if store.SaveCount != 0 {
		t.Errorf("saveCount = %d, want 0 (deferred to service layer)", store.SaveCount)
	}

	// Verify IDs match
	ids := make(map[session.ID]bool)
	for _, r := range results {
		ids[r.Session.ID] = true
	}
	for _, wantID := range []session.ID{"oc-1", "oc-2", "oc-3"} {
		if !ids[wantID] {
			t.Errorf("missing session %s in results", wantID)
		}
	}
}

func TestCaptureAll_noSessions(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name:     session.ProviderOpenCode,
		sessions: nil,
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	_, err := svc.CaptureAll(Request{
		ProjectPath:  "/test/project",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	})
	if err == nil {
		t.Error("CaptureAll() should return error when no sessions found")
	}
}

func TestCaptureByID(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderOpenCode,
		sessions: []session.Summary{
			{ID: "oc-1", Provider: session.ProviderOpenCode, CreatedAt: time.Now()},
			{ID: "oc-2", Provider: session.ProviderOpenCode, CreatedAt: time.Now().Add(-time.Hour)},
		},
		exportSessions: map[session.ID]*session.Session{
			"oc-1": {ID: "oc-1", Provider: session.ProviderOpenCode, Summary: "first"},
			"oc-2": {ID: "oc-2", Provider: session.ProviderOpenCode, Summary: "second"},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	// Capture only the second session
	result, err := svc.CaptureByID(Request{
		ProjectPath:  "/test/project",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	}, "oc-2")
	if err != nil {
		t.Fatalf("CaptureByID() error: %v", err)
	}

	if result.Session.ID != "oc-2" {
		t.Errorf("Session.ID = %q, want %q", result.Session.ID, "oc-2")
	}
	if result.Session.Summary != "second" {
		t.Errorf("Summary = %q, want %q", result.Session.Summary, "second")
	}
	if store.SaveCount != 0 {
		t.Errorf("saveCount = %d, want 0 (deferred to service layer)", store.SaveCount)
	}
}

func TestCaptureByID_notFound(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderOpenCode,
		sessions: []session.Summary{
			{ID: "oc-1", Provider: session.ProviderOpenCode, CreatedAt: time.Now()},
		},
		// No exportSessions map and no default exportSession → Export returns ErrSessionNotFound
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	_, err := svc.CaptureByID(Request{
		ProjectPath:  "/test/project",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	}, "nonexistent")
	if err == nil {
		t.Error("CaptureByID() should return error for nonexistent session")
	}
}

func TestCapture_noSessionsFound(t *testing.T) {
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name:     session.ProviderClaudeCode,
		sessions: nil, // no sessions
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	_, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err == nil {
		t.Error("Capture() should return error when no sessions found")
	}
}

// --- Skip-if-unchanged tests ---

func TestCapture_skipUnchangedSession(t *testing.T) {
	store := testutil.NewMockStore()
	store.Freshness = map[session.ID][2]int64{
		"ses-1": {50, 1771245758000}, // stored: 50 msgs, updated at timestamp
	}
	prov := &mockFreshnessProvider{
		mockProvider: mockProvider{
			name: session.ProviderOpenCode,
			sessions: []session.Summary{
				{ID: "ses-1", Provider: session.ProviderOpenCode, Branch: "main", CreatedAt: time.Now()},
			},
			exportSession: &session.Session{
				ID: "ses-1", Provider: session.ProviderOpenCode,
				Branch: "main", ProjectPath: "/test/project",
				Messages: make([]session.Message, 50),
			},
		},
		freshness: map[session.ID]*provider.Freshness{
			"ses-1": {MessageCount: 50, UpdatedAt: 1771245758000}, // same as stored
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test/project",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}
	if !result.Skipped {
		t.Error("Capture() should skip unchanged session")
	}
	if store.SaveCount != 0 {
		t.Errorf("Save() called %d times, want 0 (skipped)", store.SaveCount)
	}
}

func TestCapture_noSkipWhenMessageCountDiffers(t *testing.T) {
	store := testutil.NewMockStore()
	store.Freshness = map[session.ID][2]int64{
		"ses-1": {50, 1771245758000}, // stored: 50 msgs
	}
	prov := &mockFreshnessProvider{
		mockProvider: mockProvider{
			name: session.ProviderOpenCode,
			sessions: []session.Summary{
				{ID: "ses-1", Provider: session.ProviderOpenCode, Branch: "main", CreatedAt: time.Now()},
			},
			exportSession: &session.Session{
				ID: "ses-1", Provider: session.ProviderOpenCode,
				Branch: "main", ProjectPath: "/test/project",
				Messages: make([]session.Message, 55),
			},
		},
		freshness: map[session.ID]*provider.Freshness{
			"ses-1": {MessageCount: 55, UpdatedAt: 1771245760000}, // 55 > 50 → changed
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test/project",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}
	if result.Skipped {
		t.Error("Capture() should NOT skip when message count differs")
	}
	if store.SaveCount != 0 {
		t.Errorf("Save() called %d times, want 0 (deferred to service layer)", store.SaveCount)
	}
}

func TestCapture_noSkipWhenUpdatedAtDiffers(t *testing.T) {
	// Simulates rewind: message count same, but updatedAt changed.
	store := testutil.NewMockStore()
	store.Freshness = map[session.ID][2]int64{
		"ses-1": {50, 1771245758000},
	}
	prov := &mockFreshnessProvider{
		mockProvider: mockProvider{
			name: session.ProviderOpenCode,
			sessions: []session.Summary{
				{ID: "ses-1", Provider: session.ProviderOpenCode, Branch: "main", CreatedAt: time.Now()},
			},
			exportSession: &session.Session{
				ID: "ses-1", Provider: session.ProviderOpenCode,
				Branch: "main", ProjectPath: "/test/project",
				Messages: make([]session.Message, 50),
			},
		},
		freshness: map[session.ID]*provider.Freshness{
			"ses-1": {MessageCount: 50, UpdatedAt: 1771245800000}, // same count, different timestamp
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test/project",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}
	if result.Skipped {
		t.Error("Capture() should NOT skip when updatedAt differs (rewind case)")
	}
	if store.SaveCount != 0 {
		t.Errorf("Save() called %d times, want 0 (deferred to service layer)", store.SaveCount)
	}
}

func TestCapture_noSkipOnFirstCapture(t *testing.T) {
	store := testutil.NewMockStore() // no freshness data → first capture
	prov := &mockFreshnessProvider{
		mockProvider: mockProvider{
			name: session.ProviderOpenCode,
			sessions: []session.Summary{
				{ID: "ses-new", Provider: session.ProviderOpenCode, Branch: "main", CreatedAt: time.Now()},
			},
			exportSession: &session.Session{
				ID: "ses-new", Provider: session.ProviderOpenCode,
				Branch: "main", ProjectPath: "/test/project",
				Messages: make([]session.Message, 10),
			},
		},
		freshness: map[session.ID]*provider.Freshness{
			"ses-new": {MessageCount: 10, UpdatedAt: 1771245758000},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test/project",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}
	if result.Skipped {
		t.Error("Capture() should NOT skip on first capture")
	}
	if store.SaveCount != 0 {
		t.Errorf("Save() called %d times, want 0 (deferred to service layer)", store.SaveCount)
	}
}

func TestCapture_noSkipForNonFreshnessProvider(t *testing.T) {
	// Regular provider (no FreshnessChecker) — always captures.
	store := testutil.NewMockStore()
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-claude", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID: "ses-claude", Provider: session.ProviderClaudeCode,
			Branch: "main", ProjectPath: "/test/project",
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test/project",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderClaudeCode,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}
	if result.Skipped {
		t.Error("Capture() should NOT skip for providers without FreshnessChecker")
	}
	if store.SaveCount != 0 {
		t.Errorf("Save() called %d times, want 0 (deferred to service layer)", store.SaveCount)
	}
}

// --- Incremental capture tests ---

func TestCapture_incrementalExport_usedWhenAvailable(t *testing.T) {
	store := testutil.NewMockStore()
	// Pre-populate store with existing session (3 messages).
	existingSession := &session.Session{
		ID:       "ses-inc",
		Provider: session.ProviderOpenCode,
		Branch:   "main",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hello"},
			{ID: "m2", Role: session.RoleAssistant, Content: "hi"},
			{ID: "m3", Role: session.RoleUser, Content: "do X"},
		},
		TokenUsage: session.TokenUsage{InputTokens: 300, OutputTokens: 200, TotalTokens: 500},
	}
	store.Sessions["ses-inc"] = existingSession
	store.Freshness = map[session.ID][2]int64{
		"ses-inc": {3, 1000}, // stored: 3 msgs
	}

	prov := &mockIncrementalProvider{
		mockFreshnessProvider: mockFreshnessProvider{
			mockProvider: mockProvider{
				name: session.ProviderOpenCode,
				sessions: []session.Summary{
					{ID: "ses-inc", Provider: session.ProviderOpenCode, Branch: "main", CreatedAt: time.Now()},
				},
				exportSession: &session.Session{
					ID: "ses-inc", Provider: session.ProviderOpenCode,
					Branch: "main", ProjectPath: "/test",
					Messages: make([]session.Message, 5), // 5 total — full export
				},
			},
			freshness: map[session.ID]*provider.Freshness{
				"ses-inc": {MessageCount: 5, UpdatedAt: 2000}, // 5 > 3 → changed
			},
		},
		incrementalResult: &provider.IncrementalResult{
			NewMessages: []session.Message{
				{ID: "m4", Role: session.RoleAssistant, Content: "done X"},
				{ID: "m5", Role: session.RoleUser, Content: "thanks"},
			},
			UpdatedAt:  2000,
			TokenUsage: session.TokenUsage{InputTokens: 500, OutputTokens: 350, TotalTokens: 850},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	// Should have used incremental path, not full export.
	if !prov.incrementalCalled {
		t.Error("ExportIncremental() was not called")
	}
	if prov.exportCalled {
		t.Error("Export() should NOT be called when incremental succeeds")
	}

	// Merged session should have 5 messages (3 existing + 2 new).
	if len(result.Session.Messages) != 5 {
		t.Errorf("message count = %d, want 5", len(result.Session.Messages))
	}

	// Token usage should be updated to the full session's totals.
	if result.Session.TokenUsage.TotalTokens != 850 {
		t.Errorf("TotalTokens = %d, want 850", result.Session.TokenUsage.TotalTokens)
	}
}

func TestCapture_incrementalExport_fallbackOnError(t *testing.T) {
	store := testutil.NewMockStore()
	store.Sessions["ses-fb"] = &session.Session{
		ID:       "ses-fb",
		Provider: session.ProviderOpenCode,
		Messages: []session.Message{{ID: "m1"}},
	}
	store.Freshness = map[session.ID][2]int64{
		"ses-fb": {1, 1000},
	}

	prov := &mockIncrementalProvider{
		mockFreshnessProvider: mockFreshnessProvider{
			mockProvider: mockProvider{
				name: session.ProviderOpenCode,
				sessions: []session.Summary{
					{ID: "ses-fb", Provider: session.ProviderOpenCode, Branch: "main", CreatedAt: time.Now()},
				},
				exportSession: &session.Session{
					ID: "ses-fb", Provider: session.ProviderOpenCode,
					Branch: "main", ProjectPath: "/test",
					Messages: []session.Message{{ID: "m1"}, {ID: "m2"}, {ID: "m3"}},
				},
			},
			freshness: map[session.ID]*provider.Freshness{
				"ses-fb": {MessageCount: 3, UpdatedAt: 2000},
			},
		},
		incrementalErr: provider.ErrIncrementalNotPossible, // force fallback
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	// Incremental was attempted but failed — should fall back to full Export.
	if !prov.incrementalCalled {
		t.Error("ExportIncremental() should have been called")
	}
	if !prov.exportCalled {
		t.Error("Export() should be called as fallback when incremental fails")
	}

	// Full export returns 3 messages.
	if len(result.Session.Messages) != 3 {
		t.Errorf("message count = %d, want 3", len(result.Session.Messages))
	}
}

func TestCapture_incrementalExport_firstCapture_usesFullExport(t *testing.T) {
	store := testutil.NewMockStore() // no freshness, no stored session

	prov := &mockIncrementalProvider{
		mockFreshnessProvider: mockFreshnessProvider{
			mockProvider: mockProvider{
				name: session.ProviderOpenCode,
				sessions: []session.Summary{
					{ID: "ses-new", Provider: session.ProviderOpenCode, Branch: "main", CreatedAt: time.Now()},
				},
				exportSession: &session.Session{
					ID: "ses-new", Provider: session.ProviderOpenCode,
					Branch: "main", ProjectPath: "/test",
					Messages: []session.Message{{ID: "m1"}, {ID: "m2"}},
				},
			},
			freshness: map[session.ID]*provider.Freshness{
				"ses-new": {MessageCount: 2, UpdatedAt: 1000},
			},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test",
		Branch:       "main",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderOpenCode,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	// First capture: no stored session → should use full Export, not incremental.
	if prov.incrementalCalled {
		t.Error("ExportIncremental() should NOT be called on first capture")
	}
	if !prov.exportCalled {
		t.Error("Export() should be called on first capture")
	}

	if len(result.Session.Messages) != 2 {
		t.Errorf("message count = %d, want 2", len(result.Session.Messages))
	}
}

// --- Mocks ---

type mockProvider struct {
	exportSession  *session.Session
	exportSessions map[session.ID]*session.Session // keyed exports for multi-session tests
	name           session.ProviderName
	sessions       []session.Summary
}

func (m *mockProvider) Name() session.ProviderName { return m.name }
func (m *mockProvider) Detect(_ string, _ string) ([]session.Summary, error) {
	return m.sessions, nil
}
func (m *mockProvider) Export(id session.ID, _ session.StorageMode) (*session.Session, error) {
	// Check keyed exports first (for multi-session tests)
	if m.exportSessions != nil {
		if s, ok := m.exportSessions[id]; ok {
			copy := *s
			return &copy, nil
		}
	}
	if m.exportSession == nil {
		return nil, session.ErrSessionNotFound
	}
	s := *m.exportSession
	return &s, nil
}
func (m *mockProvider) CanImport() bool                 { return true }
func (m *mockProvider) Import(_ *session.Session) error { return nil }

// mockFreshnessProvider wraps mockProvider and adds FreshnessChecker support.
type mockFreshnessProvider struct {
	mockProvider
	freshness map[session.ID]*provider.Freshness
}

func (m *mockFreshnessProvider) SessionFreshness(id session.ID) (*provider.Freshness, error) {
	if m.freshness != nil {
		if f, ok := m.freshness[id]; ok {
			return f, nil
		}
	}
	return nil, session.ErrSessionNotFound
}

// mockIncrementalProvider supports FreshnessChecker + IncrementalExporter.
type mockIncrementalProvider struct {
	mockFreshnessProvider
	incrementalResult *provider.IncrementalResult
	incrementalErr    error
	exportCalled      bool
	incrementalCalled bool
}

func (m *mockIncrementalProvider) Export(id session.ID, mode session.StorageMode) (*session.Session, error) {
	m.exportCalled = true
	return m.mockProvider.Export(id, mode)
}

func (m *mockIncrementalProvider) ExportIncremental(_ session.ID, _ int, _ session.StorageMode) (*provider.IncrementalResult, error) {
	m.incrementalCalled = true
	if m.incrementalErr != nil {
		return nil, m.incrementalErr
	}
	return m.incrementalResult, nil
}
