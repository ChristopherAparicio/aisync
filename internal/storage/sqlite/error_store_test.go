package sqlite

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── UpsertFingerprint + GetFingerprintGroup ──

func TestUpsertFingerprint_InsertAndGet(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().Truncate(time.Second)
	group := session.ErrorFingerprintGroup{
		Fingerprint:     "abc123",
		SampleRaw:       "connection refused",
		Category:        "",
		Message:         "",
		FirstSeen:       now.Add(-time.Hour),
		LastSeen:        now,
		OccurrenceCount: 5,
		ProjectCount:    2,
	}

	if err := store.UpsertFingerprint(group); err != nil {
		t.Fatalf("UpsertFingerprint() error = %v", err)
	}

	got, err := store.GetFingerprintGroup("abc123")
	if err != nil {
		t.Fatalf("GetFingerprintGroup() error = %v", err)
	}

	if got.Fingerprint != "abc123" {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, "abc123")
	}
	if got.SampleRaw != "connection refused" {
		t.Errorf("SampleRaw = %q, want %q", got.SampleRaw, "connection refused")
	}
	if got.OccurrenceCount != 5 {
		t.Errorf("OccurrenceCount = %d, want 5", got.OccurrenceCount)
	}
	if got.ProjectCount != 2 {
		t.Errorf("ProjectCount = %d, want 2", got.ProjectCount)
	}
	if got.Category != "" {
		t.Errorf("Category = %q, want empty", got.Category)
	}
}

func TestUpsertFingerprint_UpdateAccumulatesCount(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().Truncate(time.Second)

	// First insert.
	g1 := session.ErrorFingerprintGroup{
		Fingerprint:     "fp-accum",
		SampleRaw:       "original error",
		FirstSeen:       now.Add(-2 * time.Hour),
		LastSeen:        now.Add(-time.Hour),
		OccurrenceCount: 3,
		ProjectCount:    1,
	}
	if err := store.UpsertFingerprint(g1); err != nil {
		t.Fatalf("first UpsertFingerprint() error = %v", err)
	}

	// Second upsert — occurrence_count should accumulate, last_seen should update.
	g2 := session.ErrorFingerprintGroup{
		Fingerprint:     "fp-accum",
		SampleRaw:       "newer error text",
		FirstSeen:       now,
		LastSeen:        now,
		OccurrenceCount: 2,
		ProjectCount:    3,
	}
	if err := store.UpsertFingerprint(g2); err != nil {
		t.Fatalf("second UpsertFingerprint() error = %v", err)
	}

	got, err := store.GetFingerprintGroup("fp-accum")
	if err != nil {
		t.Fatalf("GetFingerprintGroup() error = %v", err)
	}

	if got.OccurrenceCount != 5 { // 3 + 2
		t.Errorf("OccurrenceCount = %d, want 5 (3+2)", got.OccurrenceCount)
	}
	if got.ProjectCount != 3 { // MAX(1, 3)
		t.Errorf("ProjectCount = %d, want 3 (MAX(1,3))", got.ProjectCount)
	}
	// SampleRaw should be updated since excluded.sample_raw != ''
	if got.SampleRaw != "newer error text" {
		t.Errorf("SampleRaw = %q, want %q", got.SampleRaw, "newer error text")
	}
}

func TestUpsertFingerprint_EmptySampleRawPreservesExisting(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().Truncate(time.Second)
	g1 := session.ErrorFingerprintGroup{
		Fingerprint:     "fp-sample",
		SampleRaw:       "keep this",
		FirstSeen:       now,
		LastSeen:        now,
		OccurrenceCount: 1,
		ProjectCount:    1,
	}
	if err := store.UpsertFingerprint(g1); err != nil {
		t.Fatalf("UpsertFingerprint() error = %v", err)
	}

	// Upsert with empty sample_raw — existing should be preserved.
	g2 := session.ErrorFingerprintGroup{
		Fingerprint:     "fp-sample",
		SampleRaw:       "",
		FirstSeen:       now,
		LastSeen:        now,
		OccurrenceCount: 1,
		ProjectCount:    1,
	}
	if err := store.UpsertFingerprint(g2); err != nil {
		t.Fatalf("UpsertFingerprint() error = %v", err)
	}

	got, err := store.GetFingerprintGroup("fp-sample")
	if err != nil {
		t.Fatalf("GetFingerprintGroup() error = %v", err)
	}
	if got.SampleRaw != "keep this" {
		t.Errorf("SampleRaw = %q, want %q (preserved)", got.SampleRaw, "keep this")
	}
}

func TestGetFingerprintGroup_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetFingerprintGroup("nonexistent")
	if err == nil {
		t.Fatal("GetFingerprintGroup() expected error for nonexistent fingerprint")
	}
}

// ── ListFingerprintGroups ──

func TestListFingerprintGroups_All(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().Truncate(time.Second)
	groups := []session.ErrorFingerprintGroup{
		{Fingerprint: "fp-a", SampleRaw: "error A", OccurrenceCount: 10, ProjectCount: 1, FirstSeen: now, LastSeen: now},
		{Fingerprint: "fp-b", SampleRaw: "error B", OccurrenceCount: 5, ProjectCount: 1, FirstSeen: now, LastSeen: now, Category: session.ErrorCategoryNetworkError, Message: "network issue"},
		{Fingerprint: "fp-c", SampleRaw: "error C", OccurrenceCount: 20, ProjectCount: 2, FirstSeen: now, LastSeen: now},
	}
	for _, g := range groups {
		if err := store.UpsertFingerprint(g); err != nil {
			t.Fatalf("UpsertFingerprint(%s) error = %v", g.Fingerprint, err)
		}
	}

	got, err := store.ListFingerprintGroups(false, 100)
	if err != nil {
		t.Fatalf("ListFingerprintGroups() error = %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}

	// Should be ordered by occurrence_count DESC: fp-c(20), fp-a(10), fp-b(5).
	if got[0].Fingerprint != "fp-c" {
		t.Errorf("got[0].Fingerprint = %q, want fp-c (highest count)", got[0].Fingerprint)
	}
	if got[1].Fingerprint != "fp-a" {
		t.Errorf("got[1].Fingerprint = %q, want fp-a", got[1].Fingerprint)
	}
	if got[2].Fingerprint != "fp-b" {
		t.Errorf("got[2].Fingerprint = %q, want fp-b", got[2].Fingerprint)
	}
}

func TestListFingerprintGroups_OnlyUnclassified(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().Truncate(time.Second)
	groups := []session.ErrorFingerprintGroup{
		{Fingerprint: "fp-classified", SampleRaw: "classified", OccurrenceCount: 10, ProjectCount: 1, FirstSeen: now, LastSeen: now, Category: session.ErrorCategoryNetworkError, Message: "network"},
		{Fingerprint: "fp-unknown", SampleRaw: "unknown cat", OccurrenceCount: 5, ProjectCount: 1, FirstSeen: now, LastSeen: now, Category: session.ErrorCategoryUnknown},
		{Fingerprint: "fp-empty", SampleRaw: "no cat", OccurrenceCount: 3, ProjectCount: 1, FirstSeen: now, LastSeen: now, Category: ""},
	}
	for _, g := range groups {
		if err := store.UpsertFingerprint(g); err != nil {
			t.Fatalf("UpsertFingerprint(%s) error = %v", g.Fingerprint, err)
		}
	}

	got, err := store.ListFingerprintGroups(true, 100)
	if err != nil {
		t.Fatalf("ListFingerprintGroups(unclassified) error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (unknown + empty category)", len(got))
	}

	// Should be ordered by occurrence_count DESC: fp-unknown(5), fp-empty(3).
	if got[0].Fingerprint != "fp-unknown" {
		t.Errorf("got[0].Fingerprint = %q, want fp-unknown", got[0].Fingerprint)
	}
	if got[1].Fingerprint != "fp-empty" {
		t.Errorf("got[1].Fingerprint = %q, want fp-empty", got[1].Fingerprint)
	}
}

func TestListFingerprintGroups_Limit(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().Truncate(time.Second)
	for i := 0; i < 10; i++ {
		g := session.ErrorFingerprintGroup{
			Fingerprint:     "fp-limit-" + string(rune('a'+i)),
			SampleRaw:       "error",
			OccurrenceCount: 10 - i,
			ProjectCount:    1,
			FirstSeen:       now,
			LastSeen:        now,
		}
		if err := store.UpsertFingerprint(g); err != nil {
			t.Fatalf("UpsertFingerprint() error = %v", err)
		}
	}

	got, err := store.ListFingerprintGroups(false, 3)
	if err != nil {
		t.Fatalf("ListFingerprintGroups() error = %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (limit)", len(got))
	}
}

func TestListFingerprintGroups_DefaultLimit(t *testing.T) {
	store := mustOpenStore(t)

	got, err := store.ListFingerprintGroups(false, 0)
	if err != nil {
		t.Fatalf("ListFingerprintGroups() error = %v", err)
	}

	if got == nil {
		// Empty is fine, just checking no error.
		got = []session.ErrorFingerprintGroup{}
	}
	_ = got // no-op, just ensure no panic
}

// ── ClassifyFingerprintGroup ──

func TestClassifyFingerprintGroup_UpdatesGroupAndErrors(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().Truncate(time.Second)

	// Save a session first (needed for foreign key).
	sess := testSession("sess-classify")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save session error = %v", err)
	}

	// Save errors with a fingerprint.
	errors := []session.SessionError{
		{
			ID:          "err-1",
			SessionID:   "sess-classify",
			Category:    session.ErrorCategoryUnknown,
			Source:      session.ErrorSourceProvider,
			Message:     "unknown error",
			RawError:    "Unable to connect to region us-east-1",
			Fingerprint: "fp-classify-test",
			OccurredAt:  now,
		},
		{
			ID:          "err-2",
			SessionID:   "sess-classify",
			Category:    session.ErrorCategoryUnknown,
			Source:      session.ErrorSourceProvider,
			Message:     "unknown error",
			RawError:    "Unable to connect to region eu-west-1",
			Fingerprint: "fp-classify-test",
			OccurredAt:  now,
		},
	}
	if err := store.SaveErrors(errors); err != nil {
		t.Fatalf("SaveErrors() error = %v", err)
	}

	// Create fingerprint group.
	group := session.ErrorFingerprintGroup{
		Fingerprint:     "fp-classify-test",
		SampleRaw:       "Unable to connect to region us-east-1",
		Category:        session.ErrorCategoryUnknown,
		OccurrenceCount: 2,
		ProjectCount:    1,
		FirstSeen:       now,
		LastSeen:        now,
	}
	if err := store.UpsertFingerprint(group); err != nil {
		t.Fatalf("UpsertFingerprint() error = %v", err)
	}

	// Classify it.
	if err := store.ClassifyFingerprintGroup("fp-classify-test", session.ErrorCategoryNetworkError, "AWS region connectivity", "user"); err != nil {
		t.Fatalf("ClassifyFingerprintGroup() error = %v", err)
	}

	// Verify fingerprint group was updated.
	got, err := store.GetFingerprintGroup("fp-classify-test")
	if err != nil {
		t.Fatalf("GetFingerprintGroup() error = %v", err)
	}
	if got.Category != session.ErrorCategoryNetworkError {
		t.Errorf("group.Category = %q, want %q", got.Category, session.ErrorCategoryNetworkError)
	}
	if got.Message != "AWS region connectivity" {
		t.Errorf("group.Message = %q, want %q", got.Message, "AWS region connectivity")
	}
	if got.ClassifiedBy != "user" {
		t.Errorf("group.ClassifiedBy = %q, want %q", got.ClassifiedBy, "user")
	}
	if got.ClassifiedAt.IsZero() {
		t.Error("group.ClassifiedAt is zero, want non-zero")
	}

	// Verify session_errors were bulk-updated.
	sessErrors, err := store.GetErrors("sess-classify")
	if err != nil {
		t.Fatalf("GetErrors() error = %v", err)
	}
	for _, e := range sessErrors {
		if e.Category != session.ErrorCategoryNetworkError {
			t.Errorf("error %s category = %q, want %q", e.ID, e.Category, session.ErrorCategoryNetworkError)
		}
		if e.Message != "AWS region connectivity" {
			t.Errorf("error %s message = %q, want %q", e.ID, e.Message, "AWS region connectivity")
		}
		if e.Confidence != "high" {
			t.Errorf("error %s confidence = %q, want %q", e.ID, e.Confidence, "high")
		}
	}
}

// ── GetFingerprintMatch ──

func TestGetFingerprintMatch_ClassifiedReturnsGroup(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().Truncate(time.Second)
	group := session.ErrorFingerprintGroup{
		Fingerprint:     "fp-match",
		SampleRaw:       "connection timeout",
		Category:        session.ErrorCategoryNetworkError,
		Message:         "network timeout",
		ClassifiedBy:    "user",
		ClassifiedAt:    now,
		FirstSeen:       now,
		LastSeen:        now,
		OccurrenceCount: 10,
		ProjectCount:    1,
	}
	if err := store.UpsertFingerprint(group); err != nil {
		t.Fatalf("UpsertFingerprint() error = %v", err)
	}

	got, err := store.GetFingerprintMatch("fp-match")
	if err != nil {
		t.Fatalf("GetFingerprintMatch() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetFingerprintMatch() = nil, want non-nil for classified group")
	}
	if got.Category != session.ErrorCategoryNetworkError {
		t.Errorf("Category = %q, want %q", got.Category, session.ErrorCategoryNetworkError)
	}
}

func TestGetFingerprintMatch_UnknownReturnsNil(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().Truncate(time.Second)
	group := session.ErrorFingerprintGroup{
		Fingerprint:     "fp-unknown-match",
		SampleRaw:       "something unknown",
		Category:        session.ErrorCategoryUnknown,
		FirstSeen:       now,
		LastSeen:        now,
		OccurrenceCount: 3,
		ProjectCount:    1,
	}
	if err := store.UpsertFingerprint(group); err != nil {
		t.Fatalf("UpsertFingerprint() error = %v", err)
	}

	got, err := store.GetFingerprintMatch("fp-unknown-match")
	if err != nil {
		t.Fatalf("GetFingerprintMatch() error = %v", err)
	}
	if got != nil {
		t.Errorf("GetFingerprintMatch() = %v, want nil for unknown category", got)
	}
}

func TestGetFingerprintMatch_EmptyCategoryReturnsNil(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Now().Truncate(time.Second)
	group := session.ErrorFingerprintGroup{
		Fingerprint:     "fp-empty-match",
		SampleRaw:       "unclassified",
		Category:        "",
		FirstSeen:       now,
		LastSeen:        now,
		OccurrenceCount: 1,
		ProjectCount:    1,
	}
	if err := store.UpsertFingerprint(group); err != nil {
		t.Fatalf("UpsertFingerprint() error = %v", err)
	}

	got, err := store.GetFingerprintMatch("fp-empty-match")
	if err != nil {
		t.Fatalf("GetFingerprintMatch() error = %v", err)
	}
	if got != nil {
		t.Errorf("GetFingerprintMatch() = %v, want nil for empty category", got)
	}
}

func TestGetFingerprintMatch_NonexistentReturnsNil(t *testing.T) {
	store := mustOpenStore(t)

	got, err := store.GetFingerprintMatch("does-not-exist")
	if err != nil {
		t.Fatalf("GetFingerprintMatch() error = %v", err)
	}
	if got != nil {
		t.Errorf("GetFingerprintMatch() = %v, want nil for nonexistent", got)
	}
}

// ── SaveErrors with fingerprint ──

func TestSaveErrors_PersistsFingerprint(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("sess-fp")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save session error = %v", err)
	}

	now := time.Now().Truncate(time.Second)
	errors := []session.SessionError{
		{
			ID:          "err-fp-1",
			SessionID:   "sess-fp",
			Category:    session.ErrorCategoryUnknown,
			Source:      session.ErrorSourceProvider,
			Message:     "unknown",
			RawError:    "some error",
			Fingerprint: "fp-save-test",
			OccurredAt:  now,
		},
	}
	if err := store.SaveErrors(errors); err != nil {
		t.Fatalf("SaveErrors() error = %v", err)
	}

	got, err := store.GetErrors("sess-fp")
	if err != nil {
		t.Fatalf("GetErrors() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Fingerprint != "fp-save-test" {
		t.Errorf("Fingerprint = %q, want %q", got[0].Fingerprint, "fp-save-test")
	}
}

func TestListRecentErrors_IncludesFingerprint(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("sess-recent-fp")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save session error = %v", err)
	}

	now := time.Now().Truncate(time.Second)
	errors := []session.SessionError{
		{
			ID:          "err-recent-1",
			SessionID:   "sess-recent-fp",
			Category:    session.ErrorCategoryUnknown,
			Source:      session.ErrorSourceProvider,
			Message:     "test",
			Fingerprint: "fp-recent",
			OccurredAt:  now,
		},
	}
	if err := store.SaveErrors(errors); err != nil {
		t.Fatalf("SaveErrors() error = %v", err)
	}

	got, err := store.ListRecentErrors(10, "")
	if err != nil {
		t.Fatalf("ListRecentErrors() error = %v", err)
	}
	if len(got) == 0 {
		t.Fatal("ListRecentErrors() returned 0 errors")
	}
	if got[0].Fingerprint != "fp-recent" {
		t.Errorf("Fingerprint = %q, want %q", got[0].Fingerprint, "fp-recent")
	}
}
