package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── TestExchanges_Empty ──────────────────────────────────────────────────────

func TestExchanges_Empty(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	if err := store.Save(&session.Session{
		ID:          "xchg-empty-1",
		Provider:    session.ProviderClaudeCode,
		StorageMode: session.StorageModeCompact,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/xchg-empty-1/exchanges", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "No messages found") {
		t.Errorf("expected empty-state text; body excerpt:\n%s", body[:min(500, len(body))])
	}
}

// ── TestExchanges_MergedOrdering ────────────────────────────────────────────

func TestExchanges_MergedOrdering(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	child := session.Session{
		ID:          "xchg-ord-child",
		Agent:       "child-agent",
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{ID: "c1", Role: session.RoleUser, Content: "child-T2", Timestamp: base.Add(time.Minute)},
			{ID: "c2", Role: session.RoleAssistant, Content: "child-T4", Timestamp: base.Add(3 * time.Minute)},
		},
	}

	parent := &session.Session{
		ID:          "xchg-ord-parent",
		Provider:    session.ProviderClaudeCode,
		Agent:       "parent-agent",
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{ID: "p1", Role: session.RoleUser, Content: "parent-T1", Timestamp: base},
			{ID: "p2", Role: session.RoleAssistant, Content: "parent-T3", Timestamp: base.Add(2 * time.Minute)},
		},
		Children: []session.Session{child},
	}

	if err := store.Save(parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/xchg-ord-parent/exchanges", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	body := w.Body.String()
	for _, want := range []string{"parent-T1", "child-T2", "parent-T3", "child-T4"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in body", want)
		}
	}

	pos := func(s string) int { return strings.Index(body, s) }
	if !(pos("parent-T1") < pos("child-T2") &&
		pos("child-T2") < pos("parent-T3") &&
		pos("parent-T3") < pos("child-T4")) {
		t.Errorf("messages not in timestamp order; positions: T1=%d T2=%d T3=%d T4=%d",
			pos("parent-T1"), pos("child-T2"), pos("parent-T3"), pos("child-T4"))
	}
}

// ── TestExchanges_AgentTagging ───────────────────────────────────────────────

func TestExchanges_AgentTagging(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	child := session.Session{
		ID:          "xchg-tag-child",
		Agent:       "worker-agent",
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{ID: "c1", Content: "child work", Timestamp: base.Add(time.Minute)},
		},
	}

	parent := &session.Session{
		ID:          "xchg-tag-parent",
		Provider:    session.ProviderClaudeCode,
		Agent:       "orchestrator-agent",
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{ID: "p1", Content: "parent task", Timestamp: base},
		},
		Children: []session.Session{child},
	}

	if err := store.Save(parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/xchg-tag-parent/exchanges", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "orchestrator-agent") {
		t.Errorf("expected parent agent 'orchestrator-agent' in body")
	}
	if !strings.Contains(body, "worker-agent") {
		t.Errorf("expected child agent 'worker-agent' in body")
	}
	if !strings.Contains(body, "xchg-tag-parent") {
		t.Errorf("expected parent session ID in page subtitle")
	}
}

// ── TestExchanges_ChildCap ───────────────────────────────────────────────────

func TestExchanges_ChildCap(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	const nChildren = 12
	children := make([]session.Session, nChildren)
	for i := range children {
		children[i] = session.Session{
			ID:          session.ID(fmt.Sprintf("xchg-cap-child-%02d", i)),
			Agent:       fmt.Sprintf("child-agent-%02d", i),
			StorageMode: session.StorageModeCompact,
			Messages: []session.Message{
				{
					ID:        fmt.Sprintf("cm-%02d", i),
					Content:   fmt.Sprintf("child-%02d-msg", i),
					Timestamp: base.Add(time.Duration(i+1) * time.Minute),
				},
			},
		}
	}

	parent := &session.Session{
		ID:          "xchg-cap-parent",
		Provider:    session.ProviderClaudeCode,
		Agent:       "cap-parent",
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{ID: "p1", Content: "parent-msg", Timestamp: base},
		},
		Children: children,
	}

	if err := store.Save(parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/xchg-cap-parent/exchanges", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d\n%s", w.Code, w.Body.String())
	}

	body := w.Body.String()

	if !strings.Contains(body, "hx-get") {
		t.Errorf("expected HasMore load-more trigger (hx-get) when >10 children")
	}
	if !strings.Contains(body, "/partials/session-exchanges/xchg-cap-parent") {
		t.Errorf("expected moreURL pointing to xchg-cap-parent in body")
	}
	if !strings.Contains(body, "from 12 agents") {
		t.Errorf("expected 'from 12 agents' in body (ChildCount=12); got excerpt:\n%s",
			body[:min(600, len(body))])
	}
}

// ── TestExchanges_MissingChildSkip ──────────────────────────────────────────

func TestExchanges_MissingChildSkip(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	parent := &session.Session{
		ID:          "xchg-miss-parent",
		Provider:    session.ProviderClaudeCode,
		Agent:       "parent-agent",
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{ID: "p1", Content: "parent-msg", Timestamp: base},
		},
	}
	if err := store.Save(parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}

	child := &session.Session{
		ID:          "xchg-miss-child",
		Provider:    session.ProviderClaudeCode,
		StorageMode: session.StorageModeCompact,
	}
	if err := store.Save(child); err != nil {
		t.Fatalf("save child: %v", err)
	}
	if err := store.LinkSessions(session.SessionLink{
		SourceSessionID: "xchg-miss-parent",
		TargetSessionID: "xchg-miss-child",
		LinkType:        session.SessionLinkDelegatedTo,
	}); err != nil {
		t.Fatalf("link sessions: %v", err)
	}
	if err := store.Delete("xchg-miss-child"); err != nil {
		t.Fatalf("delete child: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/xchg-miss-parent/exchanges", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "1 messages") {
		t.Errorf("expected '1 messages' badge (missing child skipped); body:\n%s", body[:min(600, len(body))])
	}
	if !strings.Contains(body, "from 0 agents") {
		t.Errorf("expected 'from 0 agents' (missing child skipped); body:\n%s", body[:min(600, len(body))])
	}
}

// ── TestExchanges_SentinelDecoded ────────────────────────────────────────────

func TestExchanges_SentinelDecoded(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	sess := &session.Session{
		ID:          "xchg-sentinel-1",
		Provider:    session.ProviderClaudeCode,
		Agent:       "hermes-agent",
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{
				ID:        "sm-1",
				Role:      session.RoleAssistant,
				Content:   "\x00\x01{\"text\":\"hello from hermes\"}",
				Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	if err := store.Save(sess); err != nil {
		t.Fatalf("save: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/xchg-sentinel-1/exchanges", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	body := w.Body.String()
	if strings.Contains(body, "\x00") || strings.Contains(body, "\x01") {
		t.Errorf("response body must not contain raw sentinel bytes; body excerpt:\n%q",
			body[:min(400, len(body))])
	}
	if !strings.Contains(body, "hello from hermes") {
		t.Errorf("expected decoded content 'hello from hermes' in body")
	}
}

// ── TestExchangesMore_NextBatch ──────────────────────────────────────────────

func TestExchangesMore_NextBatch(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	const nChildren = 12
	children := make([]session.Session, nChildren)
	for i := range children {
		children[i] = session.Session{
			ID:          session.ID(fmt.Sprintf("xchg-more-child-%02d", i)),
			Agent:       fmt.Sprintf("more-agent-%02d", i),
			StorageMode: session.StorageModeCompact,
			Messages: []session.Message{
				{
					ID:        fmt.Sprintf("mcm-%02d", i),
					Content:   fmt.Sprintf("more-child-%02d-content", i),
					Timestamp: base.Add(time.Duration(i+1) * time.Minute),
				},
			},
		}
	}

	parent := &session.Session{
		ID:          "xchg-more-parent",
		Provider:    session.ProviderClaudeCode,
		Agent:       "more-parent",
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{ID: "mp1", Content: "more-parent-msg", Timestamp: base},
		},
		Children: children,
	}

	if err := store.Save(parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/partials/session-exchanges/xchg-more-parent?offset=10", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d\n%s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, "more-child-10-content") {
		t.Errorf("expected child-10 content in second batch")
	}
	if !strings.Contains(body, "more-child-11-content") {
		t.Errorf("expected child-11 content in second batch")
	}
	if strings.Contains(body, "more-child-00-content") {
		t.Errorf("child-00 should not appear in offset=10 batch")
	}
	if strings.Contains(body, "hx-get") {
		t.Errorf("final batch should not render another HasMore trigger")
	}
}

// ── TestExchangesMore_OffsetPastEnd ──────────────────────────────────────────

func TestExchangesMore_OffsetPastEnd(t *testing.T) {
	srv, store, _ := newTestServerWithStoreAndConfig(t)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	children := make([]session.Session, 5)
	for i := range children {
		children[i] = session.Session{
			ID:          session.ID(fmt.Sprintf("xchg-past-child-%02d", i)),
			StorageMode: session.StorageModeCompact,
			Messages: []session.Message{
				{ID: fmt.Sprintf("pmc-%02d", i), Content: fmt.Sprintf("past-child-%02d", i),
					Timestamp: base.Add(time.Duration(i+1) * time.Minute)},
			},
		}
	}

	parent := &session.Session{
		ID:          "xchg-past-parent",
		Provider:    session.ProviderClaudeCode,
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{ID: "pp1", Content: "past-parent-msg", Timestamp: base},
		},
		Children: children,
	}

	if err := store.Save(parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/partials/session-exchanges/xchg-past-parent?offset=10", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "" {
		t.Errorf("expected empty body for offset past end, got: %q", w.Body.String())
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
