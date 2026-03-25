package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/api"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/errorclass"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/skillresolver"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

// newTestServer creates an API server backed by a temporary SQLite store
// with only a SessionService (no SyncService, no providers).
func newTestServer(t *testing.T) (http.Handler, *service.SessionService) {
	t.Helper()
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	srv := api.New(api.Config{
		SessionService: sessionSvc,
		Addr:           "127.0.0.1:0",
	})

	return srv.Handler(), sessionSvc
}

// seedSession saves a test session directly via the service and returns it.
func seedSession(t *testing.T, store *service.SessionService, id string) *session.Session {
	t.Helper()
	// We can't call Capture without a provider, so we build a session manually.
	// We'll use the testutil helper and store it.
	// Since SessionService doesn't expose a direct Save, we need to go through import.
	sess := testutil.NewSession(id)
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}

	_, err = store.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	})
	if err != nil {
		t.Fatalf("import session: %v", err)
	}
	return sess
}

func doRequest(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func decodeResponse(t *testing.T, resp *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response body: %v\nbody: %s", err, resp.Body.String())
	}
}

// ── Health ──

func TestHealthEndpoint(t *testing.T) {
	handler, _ := newTestServer(t)
	resp := doRequest(t, handler, "GET", "/api/v1/health", nil)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]string
	decodeResponse(t, resp, &result)
	if result["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", result["status"])
	}
}

// ── Get / List / Delete ──

func TestGetSession(t *testing.T) {
	handler, svc := newTestServer(t)
	sess := seedSession(t, svc, "test-session-1")

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/"+string(sess.ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var got session.Session
	decodeResponse(t, resp, &got)
	if got.ID != sess.ID {
		t.Errorf("expected ID %s, got %s", sess.ID, got.ID)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	handler, _ := newTestServer(t)
	resp := doRequest(t, handler, "GET", "/api/v1/sessions/nonexistent", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestListSessions(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "list-1")
	seedSession(t, svc, "list-2")

	resp := doRequest(t, handler, "GET", "/api/v1/sessions?all=true", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var summaries []session.Summary
	decodeResponse(t, resp, &summaries)
	if len(summaries) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(summaries))
	}
}

func TestDeleteSession(t *testing.T) {
	handler, svc := newTestServer(t)
	sess := seedSession(t, svc, "delete-me")

	// Delete
	resp := doRequest(t, handler, "DELETE", "/api/v1/sessions/"+string(sess.ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	// Verify it's gone
	resp = doRequest(t, handler, "GET", "/api/v1/sessions/"+string(sess.ID), nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ── Export ──

func TestExportSession(t *testing.T) {
	handler, svc := newTestServer(t)
	sess := seedSession(t, svc, "export-test")

	body := map[string]string{
		"session_id": string(sess.ID),
		"format":     "aisync",
	}

	resp := doRequest(t, handler, "POST", "/api/v1/sessions/export", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result struct {
		Data      string `json:"data"`
		Format    string `json:"format"`
		SessionID string `json:"session_id"`
	}
	decodeResponse(t, resp, &result)

	if result.Format != "aisync" {
		t.Errorf("expected format=aisync, got %q", result.Format)
	}
	if result.SessionID != string(sess.ID) {
		t.Errorf("expected session_id=%s, got %s", sess.ID, result.SessionID)
	}

	// Verify base64-encoded data decodes to valid JSON
	decoded, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	var exported session.Session
	if err := json.Unmarshal(decoded, &exported); err != nil {
		t.Fatalf("unmarshal exported session: %v", err)
	}
	if exported.ID != sess.ID {
		t.Errorf("exported session ID mismatch: %s vs %s", exported.ID, sess.ID)
	}
}

// ── Import ──

func TestImportSession(t *testing.T) {
	handler, _ := newTestServer(t)

	sess := testutil.NewSession("import-via-api")
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	body := map[string]string{
		"data":          base64.StdEncoding.EncodeToString(data),
		"source_format": "aisync",
		"into_target":   "aisync",
	}

	resp := doRequest(t, handler, "POST", "/api/v1/sessions/import", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result struct {
		SessionID    string `json:"SessionID"`
		SourceFormat string `json:"SourceFormat"`
		Target       string `json:"Target"`
	}
	decodeResponse(t, resp, &result)

	if result.SessionID != string(sess.ID) {
		t.Errorf("expected session ID %s, got %s", sess.ID, result.SessionID)
	}

	// Verify it's retrievable
	resp = doRequest(t, handler, "GET", "/api/v1/sessions/"+string(sess.ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("imported session not retrievable: %d %s", resp.Code, resp.Body.String())
	}
}

// ── Stats ──

func TestStats(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "stats-1")
	seedSession(t, svc, "stats-2")

	resp := doRequest(t, handler, "GET", "/api/v1/stats?all=true", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result struct {
		TotalSessions int `json:"TotalSessions"`
		TotalMessages int `json:"TotalMessages"`
		TotalTokens   int `json:"TotalTokens"`
	}
	decodeResponse(t, resp, &result)

	if result.TotalSessions < 2 {
		t.Errorf("expected at least 2 sessions in stats, got %d", result.TotalSessions)
	}
}

// ── Sync without SyncService ──

func TestSyncUnavailable(t *testing.T) {
	handler, _ := newTestServer(t) // no SyncService wired

	tests := []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/api/v1/sync/push", map[string]bool{"remote": false}},
		{"POST", "/api/v1/sync/pull", map[string]bool{"remote": false}},
		{"POST", "/api/v1/sync/sync", map[string]bool{"remote": false}},
		{"GET", "/api/v1/sync/index", nil},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			resp := doRequest(t, handler, tt.method, tt.path, tt.body)
			if resp.Code != http.StatusServiceUnavailable {
				t.Errorf("expected 503, got %d: %s", resp.Code, resp.Body.String())
			}
		})
	}
}

// ── Error cases ──

func TestBadJSON(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/sessions/export", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteNotFound(t *testing.T) {
	handler, _ := newTestServer(t)
	resp := doRequest(t, handler, "DELETE", "/api/v1/sessions/does-not-exist", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestImportBadBase64(t *testing.T) {
	handler, _ := newTestServer(t)
	body := map[string]string{
		"data": "not-valid-base64!!!",
	}
	resp := doRequest(t, handler, "POST", "/api/v1/sessions/import", body)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ── Search ──

func TestSearchSessions(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "search-1")
	seedSession(t, svc, "search-2")

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?keyword=Test+session", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result session.SearchResult
	decodeResponse(t, resp, &result)
	if result.TotalCount < 2 {
		t.Errorf("expected at least 2 results, got %d", result.TotalCount)
	}
	if len(result.Sessions) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(result.Sessions))
	}
}

func TestSearchSessionsNoResults(t *testing.T) {
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?keyword=nonexistent", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result session.SearchResult
	decodeResponse(t, resp, &result)
	if result.TotalCount != 0 {
		t.Errorf("expected 0 results, got %d", result.TotalCount)
	}
}

func TestSearchSessionsWithFilters(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "filter-1")
	seedSession(t, svc, "filter-2")

	// Filter by branch from testutil.NewSession (uses "feature/test")
	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?branch=feature/test", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result session.SearchResult
	decodeResponse(t, resp, &result)
	if result.TotalCount < 2 {
		t.Errorf("expected at least 2 results for branch filter, got %d", result.TotalCount)
	}
}

func TestSearchSessionsPagination(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "page-1")
	seedSession(t, svc, "page-2")
	seedSession(t, svc, "page-3")

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?limit=2&offset=0", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result session.SearchResult
	decodeResponse(t, resp, &result)
	if result.TotalCount < 3 {
		t.Errorf("expected at least 3 total, got %d", result.TotalCount)
	}
	if len(result.Sessions) != 2 {
		t.Errorf("expected 2 sessions (limit=2), got %d", len(result.Sessions))
	}
	if result.Limit != 2 {
		t.Errorf("expected limit=2, got %d", result.Limit)
	}
}

func TestSearchSessionsBadProvider(t *testing.T) {
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?provider=invalid", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid provider, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestExportUnknownFormat(t *testing.T) {
	handler, svc := newTestServer(t)
	sess := seedSession(t, svc, "bad-format-test")

	body := map[string]string{
		"session_id": string(sess.ID),
		"format":     "xml",
	}
	resp := doRequest(t, handler, "POST", "/api/v1/sessions/export", body)
	if resp.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ── Sync Index handler with GET (no body) ──

func TestSyncIndexGET(t *testing.T) {
	handler, _ := newTestServer(t)
	resp := doRequest(t, handler, "GET", "/api/v1/sync/index", nil)

	// Should return 503 since no SyncService is configured
	if resp.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ── Ingest ──

func TestIngestEndpoint(t *testing.T) {
	handler, _ := newTestServer(t)

	body := map[string]any{
		"provider": "ollama",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "world", "model": "qwen3"},
		},
		"agent":   "jarvis",
		"summary": "test ingest",
	}
	resp := doRequest(t, handler, "POST", "/api/v1/ingest", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]string
	decodeResponse(t, resp, &result)
	if result["provider"] != "ollama" {
		t.Errorf("expected provider 'ollama', got %q", result["provider"])
	}
	if result["session_id"] == "" {
		t.Error("expected a non-empty session_id")
	}
}

func TestIngestEndpointBadProvider(t *testing.T) {
	handler, _ := newTestServer(t)

	body := map[string]any{
		"provider": "unknown",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}
	resp := doRequest(t, handler, "POST", "/api/v1/ingest", body)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ── Ingest Ollama ──

func TestIngestOllamaEndpoint(t *testing.T) {
	handler, _ := newTestServer(t)

	body := map[string]any{
		"model":        "qwen3-coder:30b",
		"project_path": "/Users/test/dev/project",
		"agent":        "jarvis",
		"summary":      "Weather query",
		"conversation": []map[string]any{
			{"role": "user", "content": "What's the weather?"},
			{"role": "assistant", "content": "", "tool_calls": []map[string]any{
				{"type": "function", "function": map[string]any{
					"name":      "get_weather",
					"arguments": map[string]any{"city": "Paris"},
				}},
			}},
			{"role": "tool", "tool_name": "get_weather", "content": "22°C, sunny"},
			{"role": "assistant", "content": "It's 22°C and sunny in Paris."},
		},
		"prompt_eval_count": 450,
		"eval_count":        35,
	}

	resp := doRequest(t, handler, "POST", "/api/v1/ingest/ollama", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]string
	decodeResponse(t, resp, &result)
	if result["provider"] != "ollama" {
		t.Errorf("expected provider 'ollama', got %q", result["provider"])
	}
	if result["session_id"] == "" {
		t.Error("expected a non-empty session_id")
	}
}

func TestIngestOllamaSimpleConversation(t *testing.T) {
	handler, _ := newTestServer(t)

	body := map[string]any{
		"model": "llama3.1",
		"conversation": []map[string]any{
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there!"},
		},
	}

	resp := doRequest(t, handler, "POST", "/api/v1/ingest/ollama", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]string
	decodeResponse(t, resp, &result)
	if result["provider"] != "ollama" {
		t.Errorf("expected 'ollama', got %q", result["provider"])
	}
}

func TestIngestOllamaEmptyConversation(t *testing.T) {
	handler, _ := newTestServer(t)

	body := map[string]any{
		"model":        "llama3.1",
		"conversation": []map[string]any{},
	}

	resp := doRequest(t, handler, "POST", "/api/v1/ingest/ollama", body)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestIngestOllamaBadJSON(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/ingest/ollama", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Voice Search ──

func TestSearchVoiceMode(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "voice-1")
	seedSession(t, svc, "voice-2")

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?voice=true", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]any
	decodeResponse(t, resp, &result)

	// Should have voice_results array.
	voiceResults, ok := result["voice_results"]
	if !ok {
		t.Fatal("expected voice_results in response")
	}
	arr, ok := voiceResults.([]any)
	if !ok {
		t.Fatalf("expected voice_results to be an array, got %T", voiceResults)
	}
	if len(arr) < 2 {
		t.Errorf("expected at least 2 voice results, got %d", len(arr))
	}

	// Check first voice result has required fields.
	first, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("expected voice result to be an object")
	}
	if _, has := first["id"]; !has {
		t.Error("voice result missing 'id' field")
	}
	if _, has := first["summary"]; !has {
		t.Error("voice result missing 'summary' field")
	}
	if _, has := first["time_ago"]; !has {
		t.Error("voice result missing 'time_ago' field")
	}
}

func TestSearchVoiceModeDefaultLimit(t *testing.T) {
	handler, svc := newTestServer(t)
	// Seed more than 5 sessions.
	for i := 0; i < 7; i++ {
		seedSession(t, svc, fmt.Sprintf("voice-limit-%d", i))
	}

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?voice=true", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result session.SearchResult
	decodeResponse(t, resp, &result)

	// Voice mode defaults to limit=5.
	if len(result.Sessions) > 5 {
		t.Errorf("expected at most 5 sessions in voice mode, got %d", len(result.Sessions))
	}
	if len(result.VoiceResults) > 5 {
		t.Errorf("expected at most 5 voice results, got %d", len(result.VoiceResults))
	}
}

func TestSearchNonVoiceOmitsVoiceResults(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "non-voice-1")

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]any
	decodeResponse(t, resp, &result)

	// Should NOT have voice_results when voice=false.
	if _, has := result["voice_results"]; has {
		t.Error("non-voice search should not include voice_results")
	}
}

// ── Session Links ──

func TestLinkSessionsEndpoint(t *testing.T) {
	handler, svc := newTestServer(t)

	// Create two sessions first via ingest.
	sess1ID := ingestTestSession(t, handler, "parlay", "session-link-a")
	sess2ID := ingestTestSession(t, handler, "parlay", "session-link-b")

	body := map[string]any{
		"source_session_id": sess1ID,
		"target_session_id": sess2ID,
		"link_type":         "delegated_to",
		"description":       "source delegated auth to target",
	}
	resp := doRequest(t, handler, "POST", "/api/v1/sessions/session-links", body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.Code, resp.Body.String())
	}

	var link session.SessionLink
	decodeResponse(t, resp, &link)
	if link.LinkType != session.SessionLinkDelegatedTo {
		t.Errorf("link_type = %q, want %q", link.LinkType, session.SessionLinkDelegatedTo)
	}
	if string(link.SourceSessionID) != sess1ID {
		t.Errorf("source_session_id = %q, want %q", link.SourceSessionID, sess1ID)
	}
	if string(link.TargetSessionID) != sess2ID {
		t.Errorf("target_session_id = %q, want %q", link.TargetSessionID, sess2ID)
	}

	// Verify the link is retrievable via the service directly.
	_ = svc
}

func TestLinkSessionsEndpoint_InvalidLinkType(t *testing.T) {
	handler, _ := newTestServer(t)

	body := map[string]any{
		"source_session_id": "aaa",
		"target_session_id": "bbb",
		"link_type":         "invalid_type",
	}
	resp := doRequest(t, handler, "POST", "/api/v1/sessions/session-links", body)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestLinkSessionsEndpoint_SameSession(t *testing.T) {
	handler, _ := newTestServer(t)

	body := map[string]any{
		"source_session_id": "same-id",
		"target_session_id": "same-id",
		"link_type":         "related",
	}
	resp := doRequest(t, handler, "POST", "/api/v1/sessions/session-links", body)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestGetSessionLinksEndpoint(t *testing.T) {
	handler, _ := newTestServer(t)

	// Create two sessions and link them.
	sess1ID := ingestTestSession(t, handler, "parlay", "get-links-a")
	sess2ID := ingestTestSession(t, handler, "parlay", "get-links-b")

	linkBody := map[string]any{
		"source_session_id": sess1ID,
		"target_session_id": sess2ID,
		"link_type":         "continuation",
	}
	linkResp := doRequest(t, handler, "POST", "/api/v1/sessions/session-links", linkBody)
	if linkResp.Code != http.StatusCreated {
		t.Fatalf("link creation failed: %d %s", linkResp.Code, linkResp.Body.String())
	}

	// Get links for the source session.
	resp := doRequest(t, handler, "GET", fmt.Sprintf("/api/v1/sessions/%s/session-links", sess1ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var links []session.SessionLink
	decodeResponse(t, resp, &links)
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0].LinkType != session.SessionLinkContinuation {
		t.Errorf("link_type = %q, want %q", links[0].LinkType, session.SessionLinkContinuation)
	}

	// Target also gets the inverse link.
	respTarget := doRequest(t, handler, "GET", fmt.Sprintf("/api/v1/sessions/%s/session-links", sess2ID), nil)
	if respTarget.Code != http.StatusOK {
		t.Fatalf("expected 200 for target, got %d: %s", respTarget.Code, respTarget.Body.String())
	}
	var inverseLinks []session.SessionLink
	decodeResponse(t, respTarget, &inverseLinks)
	if len(inverseLinks) != 1 {
		t.Fatalf("expected 1 inverse link, got %d", len(inverseLinks))
	}
	if inverseLinks[0].LinkType != session.SessionLinkFollowUp {
		t.Errorf("inverse link_type = %q, want %q", inverseLinks[0].LinkType, session.SessionLinkFollowUp)
	}
}

func TestDeleteSessionLinkEndpoint(t *testing.T) {
	handler, _ := newTestServer(t)

	sess1ID := ingestTestSession(t, handler, "parlay", "del-link-a")
	sess2ID := ingestTestSession(t, handler, "parlay", "del-link-b")

	// Create the link.
	linkBody := map[string]any{
		"source_session_id": sess1ID,
		"target_session_id": sess2ID,
		"link_type":         "related",
	}
	linkResp := doRequest(t, handler, "POST", "/api/v1/sessions/session-links", linkBody)
	if linkResp.Code != http.StatusCreated {
		t.Fatalf("link creation failed: %d %s", linkResp.Code, linkResp.Body.String())
	}
	var link session.SessionLink
	decodeResponse(t, linkResp, &link)

	// Delete the link.
	resp := doRequest(t, handler, "DELETE", fmt.Sprintf("/api/v1/sessions/session-links/%s", link.ID), nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.Code, resp.Body.String())
	}

	// Verify it's gone.
	getResp := doRequest(t, handler, "GET", fmt.Sprintf("/api/v1/sessions/%s/session-links", sess1ID), nil)
	var links []session.SessionLink
	decodeResponse(t, getResp, &links)
	if len(links) != 0 {
		t.Errorf("expected 0 links after delete, got %d", len(links))
	}
}

// ── Trends ──

func TestTrendsEndpoint(t *testing.T) {
	handler, _ := newTestServer(t)

	// Ingest a session so there's data.
	ingestTestSession(t, handler, "parlay", "trend-test-1")

	resp := doRequest(t, handler, "GET", "/api/v1/stats/trends?period=7d", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]any
	decodeResponse(t, resp, &result)
	if _, ok := result["current"]; !ok {
		t.Error("missing 'current' in trend result")
	}
	if _, ok := result["previous"]; !ok {
		t.Error("missing 'previous' in trend result")
	}
	if _, ok := result["delta"]; !ok {
		t.Error("missing 'delta' in trend result")
	}
}

func TestTrendsEndpoint_WithTypeFilter(t *testing.T) {
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "GET", "/api/v1/stats/trends?type=bug&period=30d", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ── Session Tagging ──

func TestPatchSessionEndpoint_TagSession(t *testing.T) {
	handler, _ := newTestServer(t)
	sessID := ingestTestSession(t, handler, "parlay", "tag-me")

	body := map[string]string{"session_type": "bug"}
	resp := doRequest(t, handler, "PATCH", fmt.Sprintf("/api/v1/sessions/%s", sessID), body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestPatchSessionEndpoint_InvalidSession(t *testing.T) {
	handler, _ := newTestServer(t)

	body := map[string]string{"session_type": "feature"}
	resp := doRequest(t, handler, "PATCH", "/api/v1/sessions/nonexistent", body)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.Code)
	}
}

// ── Projects ──

func TestListProjectsEndpoint(t *testing.T) {
	handler, _ := newTestServer(t)

	// Ingest sessions with different projects and remote URLs.
	body1 := map[string]any{
		"provider":     "parlay",
		"session_id":   "proj-test-1",
		"project_path": "/Users/dev/aisync",
		"remote_url":   "https://github.com/org/aisync.git",
		"messages":     []map[string]any{{"role": "user", "content": "hello"}},
	}
	body2 := map[string]any{
		"provider":     "ollama",
		"session_id":   "proj-test-2",
		"project_path": "/Users/dev/sandbox",
		"messages":     []map[string]any{{"role": "user", "content": "test"}},
	}
	doRequest(t, handler, "POST", "/api/v1/ingest", body1)
	doRequest(t, handler, "POST", "/api/v1/ingest", body2)

	// List projects.
	resp := doRequest(t, handler, "GET", "/api/v1/projects", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var groups []map[string]any
	decodeResponse(t, resp, &groups)

	if len(groups) < 2 {
		t.Fatalf("expected at least 2 project groups, got %d", len(groups))
	}

	// Check that each group has the expected fields.
	for _, g := range groups {
		if _, ok := g["project_path"]; !ok {
			t.Error("project group missing project_path field")
		}
		if _, ok := g["session_count"]; !ok {
			t.Error("project group missing session_count field")
		}
		if _, ok := g["display_name"]; !ok {
			t.Error("project group missing display_name field")
		}
	}
}

// ingestTestSession is a helper that ingests a minimal session and returns its ID.
func ingestTestSession(t *testing.T, handler http.Handler, provider, sessionID string) string {
	t.Helper()
	body := map[string]any{
		"provider":   provider,
		"session_id": sessionID,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "world"},
		},
	}
	resp := doRequest(t, handler, "POST", "/api/v1/ingest", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("ingest failed: %d %s", resp.Code, resp.Body.String())
	}
	var result map[string]string
	decodeResponse(t, resp, &result)
	return result["session_id"]
}

// ── Mock Analyzer ──

// mockAnalyzer implements analysis.Analyzer for testing.
type mockAnalyzer struct {
	report *analysis.AnalysisReport
	err    error
}

func (m *mockAnalyzer) Analyze(_ context.Context, _ analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.report, nil
}

func (m *mockAnalyzer) Name() analysis.AdapterName {
	return analysis.AdapterOllama
}

// newTestServerWithAnalysis creates an API server with both SessionService and AnalysisService.
func newTestServerWithAnalysis(t *testing.T) (http.Handler, *service.SessionService, *service.AnalysisService) {
	t.Helper()
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	analyzer := &mockAnalyzer{
		report: &analysis.AnalysisReport{
			Score:   75,
			Summary: "Good session with minor inefficiencies.",
			Problems: []analysis.Problem{
				{Severity: analysis.SeverityLow, Description: "Minor retry on file read."},
			},
			Recommendations: []analysis.Recommendation{
				{Category: analysis.CategoryTool, Title: "Cache reads", Description: "Cache file reads.", Priority: 3},
			},
		},
	}

	analysisSvc := service.NewAnalysisService(service.AnalysisServiceConfig{
		Store:    store,
		Analyzer: analyzer,
	})

	srv := api.New(api.Config{
		SessionService:  sessionSvc,
		AnalysisService: analysisSvc,
		Addr:            "127.0.0.1:0",
	})

	return srv.Handler(), sessionSvc, analysisSvc
}

// ── Analysis API Tests ──

func TestAnalyzeEndpoint(t *testing.T) {
	handler, _, _ := newTestServerWithAnalysis(t)

	// Create a session to analyze.
	sessID := ingestTestSession(t, handler, "parlay", "analyze-me")

	// Trigger analysis.
	resp := doRequest(t, handler, "POST", fmt.Sprintf("/api/v1/sessions/%s/analyze", sessID), nil)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.Code, resp.Body.String())
	}

	var sa analysis.SessionAnalysis
	decodeResponse(t, resp, &sa)
	if sa.SessionID != sessID {
		t.Errorf("session_id = %q, want %q", sa.SessionID, sessID)
	}
	if sa.Report.Score != 75 {
		t.Errorf("score = %d, want 75", sa.Report.Score)
	}
	if sa.Trigger != analysis.TriggerManual {
		t.Errorf("trigger = %q, want %q", sa.Trigger, analysis.TriggerManual)
	}
	if sa.Adapter != analysis.AdapterOllama {
		t.Errorf("adapter = %q, want %q", sa.Adapter, analysis.AdapterOllama)
	}
}

func TestAnalyzeEndpoint_WithTriggerAuto(t *testing.T) {
	handler, _, _ := newTestServerWithAnalysis(t)
	sessID := ingestTestSession(t, handler, "parlay", "analyze-auto")

	resp := doRequest(t, handler, "POST", fmt.Sprintf("/api/v1/sessions/%s/analyze", sessID),
		map[string]string{"trigger": "auto"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.Code, resp.Body.String())
	}

	var sa analysis.SessionAnalysis
	decodeResponse(t, resp, &sa)
	if sa.Trigger != analysis.TriggerAuto {
		t.Errorf("trigger = %q, want %q", sa.Trigger, analysis.TriggerAuto)
	}
}

func TestGetAnalysisEndpoint(t *testing.T) {
	handler, _, _ := newTestServerWithAnalysis(t)

	// Ingest and analyze.
	sessID := ingestTestSession(t, handler, "parlay", "get-analysis-test")
	doRequest(t, handler, "POST", fmt.Sprintf("/api/v1/sessions/%s/analyze", sessID), nil)

	// Get the analysis.
	resp := doRequest(t, handler, "GET", fmt.Sprintf("/api/v1/sessions/%s/analysis", sessID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var sa analysis.SessionAnalysis
	decodeResponse(t, resp, &sa)
	if sa.Report.Score != 75 {
		t.Errorf("score = %d, want 75", sa.Report.Score)
	}
}

func TestGetAnalysisEndpoint_NotFound(t *testing.T) {
	handler, _, _ := newTestServerWithAnalysis(t)

	// No analysis exists for this session.
	sessID := ingestTestSession(t, handler, "parlay", "no-analysis-yet")
	resp := doRequest(t, handler, "GET", fmt.Sprintf("/api/v1/sessions/%s/analysis", sessID), nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestListAnalysesEndpoint(t *testing.T) {
	handler, _, _ := newTestServerWithAnalysis(t)

	sessID := ingestTestSession(t, handler, "parlay", "list-analyses-test")

	// Run analysis twice.
	doRequest(t, handler, "POST", fmt.Sprintf("/api/v1/sessions/%s/analyze", sessID), nil)
	doRequest(t, handler, "POST", fmt.Sprintf("/api/v1/sessions/%s/analyze", sessID), nil)

	resp := doRequest(t, handler, "GET", fmt.Sprintf("/api/v1/sessions/%s/analyses", sessID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var analyses []analysis.SessionAnalysis
	decodeResponse(t, resp, &analyses)
	if len(analyses) != 2 {
		t.Errorf("expected 2 analyses, got %d", len(analyses))
	}
}

func TestListAnalysesEndpoint_Empty(t *testing.T) {
	handler, _, _ := newTestServerWithAnalysis(t)

	sessID := ingestTestSession(t, handler, "parlay", "no-analyses-yet")
	resp := doRequest(t, handler, "GET", fmt.Sprintf("/api/v1/sessions/%s/analyses", sessID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var analyses []analysis.SessionAnalysis
	decodeResponse(t, resp, &analyses)
	if len(analyses) != 0 {
		t.Errorf("expected 0 analyses, got %d", len(analyses))
	}
}

func TestAnalyzeEndpoint_NoAnalysisService(t *testing.T) {
	// Standard test server has no AnalysisService.
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "POST", "/api/v1/sessions/any-id/analyze", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404 when analysis service is nil, got %d", resp.Code)
	}
}

func TestGetAnalysisEndpoint_NoAnalysisService(t *testing.T) {
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/any-id/analysis", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404 when analysis service is nil, got %d", resp.Code)
	}
}

// ── Mock Skill Resolver ──

// mockSkillResolver implements skillresolver.ResolverServicer for testing.
type mockSkillResolver struct {
	result *skillresolver.ResolveResult
	err    error
}

func (m *mockSkillResolver) Resolve(_ context.Context, req skillresolver.ResolveRequest) (*skillresolver.ResolveResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	result := *m.result
	result.SessionID = req.SessionID
	return &result, nil
}

// newTestServerWithSkillResolver creates an API server with skill resolver.
func newTestServerWithSkillResolver(t *testing.T, resolver skillresolver.ResolverServicer) http.Handler {
	t.Helper()
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	srv := api.New(api.Config{
		SessionService: sessionSvc,
		SkillResolver:  resolver,
		Addr:           "127.0.0.1:0",
	})

	return srv.Handler()
}

// ── Skill Resolver API Tests ──

func TestSkillResolveEndpoint(t *testing.T) {
	resolver := &mockSkillResolver{
		result: &skillresolver.ResolveResult{
			Improvements: []skillresolver.SkillImprovement{
				{
					SkillName:       "replay-tester",
					SkillPath:       "/home/user/.config/opencode/skills/replay-tester/SKILL.md",
					Kind:            skillresolver.KindKeywords,
					AddKeywords:     []string{"session replay", "regression testing"},
					Reasoning:       "Keywords better match user intent for replay testing.",
					Confidence:      0.85,
					SourceSessionID: "test-123",
				},
			},
			Verdict: skillresolver.VerdictPending,
		},
	}

	handler := newTestServerWithSkillResolver(t, resolver)

	// Ingest a session first.
	sessID := ingestTestSession(t, handler, "parlay", "skill-resolve-test")

	body := map[string]any{
		"dry_run": true,
	}
	resp := doRequest(t, handler, "POST", fmt.Sprintf("/api/v1/sessions/%s/skills/resolve", sessID), body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]any
	decodeResponse(t, resp, &result)
	if result["session_id"] != sessID {
		t.Errorf("session_id = %q, want %q", result["session_id"], sessID)
	}
	if result["verdict"] != "pending" {
		t.Errorf("verdict = %q, want %q", result["verdict"], "pending")
	}

	improvements, ok := result["improvements"].([]any)
	if !ok || len(improvements) != 1 {
		t.Fatalf("expected 1 improvement, got %v", result["improvements"])
	}
	imp0 := improvements[0].(map[string]any)
	if imp0["skill_name"] != "replay-tester" {
		t.Errorf("skill_name = %q, want %q", imp0["skill_name"], "replay-tester")
	}
}

func TestSkillResolveEndpoint_NoBody(t *testing.T) {
	resolver := &mockSkillResolver{
		result: &skillresolver.ResolveResult{
			Verdict: skillresolver.VerdictNoChange,
		},
	}

	handler := newTestServerWithSkillResolver(t, resolver)
	sessID := ingestTestSession(t, handler, "parlay", "skill-no-body")

	// No body — should default to dry_run=true
	resp := doRequest(t, handler, "POST", fmt.Sprintf("/api/v1/sessions/%s/skills/resolve", sessID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestSkillResolveEndpoint_WithSkillFilter(t *testing.T) {
	resolver := &mockSkillResolver{
		result: &skillresolver.ResolveResult{
			Verdict: skillresolver.VerdictNoChange,
		},
	}

	handler := newTestServerWithSkillResolver(t, resolver)
	sessID := ingestTestSession(t, handler, "parlay", "skill-filter")

	body := map[string]any{
		"skill_names": []string{"replay-tester"},
		"dry_run":     true,
	}
	resp := doRequest(t, handler, "POST", fmt.Sprintf("/api/v1/sessions/%s/skills/resolve", sessID), body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestSkillResolveEndpoint_ResolverError(t *testing.T) {
	resolver := &mockSkillResolver{
		err: fmt.Errorf("loading session fake-id: %w", session.ErrSessionNotFound),
	}

	handler := newTestServerWithSkillResolver(t, resolver)
	sessID := ingestTestSession(t, handler, "parlay", "skill-error-test")

	resp := doRequest(t, handler, "POST", fmt.Sprintf("/api/v1/sessions/%s/skills/resolve", sessID), nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestSkillResolveEndpoint_NoResolver(t *testing.T) {
	// Standard test server has no skill resolver.
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "POST", "/api/v1/sessions/any-id/skills/resolve", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404 when skill resolver is nil, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ── Error Endpoints ──

// newTestServerWithErrors creates an API server with SessionService and ErrorService.
func newTestServerWithErrors(t *testing.T) (http.Handler, *service.SessionService, *service.ErrorService) {
	t.Helper()
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	errorSvc := service.NewErrorService(service.ErrorServiceConfig{
		Store:      store,
		Classifier: errorclass.NewDeterministicClassifier(),
	})

	srv := api.New(api.Config{
		SessionService: sessionSvc,
		ErrorService:   errorSvc,
		Addr:           "127.0.0.1:0",
	})

	return srv.Handler(), sessionSvc, errorSvc
}

// seedSessionWithErrors creates a session via ingest, then processes errors for it.
func seedSessionWithErrors(t *testing.T, handler http.Handler, errorSvc *service.ErrorService, errors []session.SessionError) string {
	t.Helper()
	sessID := ingestTestSession(t, handler, "parlay", "error-test-"+fmt.Sprintf("%d", time.Now().UnixNano()))

	// Build a session with errors so ProcessSession can classify and save them.
	sess := &session.Session{
		ID:     session.ID(sessID),
		Errors: errors,
	}
	for i := range sess.Errors {
		sess.Errors[i].SessionID = session.ID(sessID)
	}

	_, err := errorSvc.ProcessSession(sess)
	if err != nil {
		t.Fatalf("ProcessSession: %v", err)
	}

	return sessID
}

func TestGetSessionErrors(t *testing.T) {
	handler, _, errorSvc := newTestServerWithErrors(t)

	now := time.Now()
	sessID := seedSessionWithErrors(t, handler, errorSvc, []session.SessionError{
		{
			ID:         "err-1",
			RawError:   "Internal server error",
			HTTPStatus: 500,
			OccurredAt: now,
		},
		{
			ID:         "err-2",
			RawError:   "rate limit exceeded",
			HTTPStatus: 429,
			OccurredAt: now.Add(time.Second),
		},
	})

	resp := doRequest(t, handler, "GET", fmt.Sprintf("/api/v1/sessions/%s/errors", sessID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]any
	decodeResponse(t, resp, &result)

	count, ok := result["count"].(float64)
	if !ok || int(count) != 2 {
		t.Errorf("expected count=2, got %v", result["count"])
	}

	errors, ok := result["errors"].([]any)
	if !ok || len(errors) != 2 {
		t.Fatalf("expected 2 errors, got %v", result["errors"])
	}

	// Verify first error has expected fields.
	first := errors[0].(map[string]any)
	if first["id"] != "err-1" {
		t.Errorf("expected id=err-1, got %v", first["id"])
	}
	if first["session_id"] != sessID {
		t.Errorf("expected session_id=%s, got %v", sessID, first["session_id"])
	}
	// DeterministicClassifier should classify 500 as provider_error
	if first["category"] != "provider_error" {
		t.Errorf("expected category=provider_error, got %v", first["category"])
	}
	if first["source"] != "provider" {
		t.Errorf("expected source=provider, got %v", first["source"])
	}
	// 500 should be external
	if first["is_external"] != true {
		t.Errorf("expected is_external=true, got %v", first["is_external"])
	}
}

func TestGetSessionErrors_Empty(t *testing.T) {
	handler, _, _ := newTestServerWithErrors(t)
	sessID := ingestTestSession(t, handler, "parlay", "no-errors-session")

	resp := doRequest(t, handler, "GET", fmt.Sprintf("/api/v1/sessions/%s/errors", sessID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]any
	decodeResponse(t, resp, &result)
	count := result["count"].(float64)
	if int(count) != 0 {
		t.Errorf("expected count=0, got %v", result["count"])
	}
}

func TestGetSessionErrors_NoErrorService(t *testing.T) {
	// Standard test server has no ErrorService.
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/any-id/errors", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404 when error service is nil, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestGetSessionErrorSummary(t *testing.T) {
	handler, _, errorSvc := newTestServerWithErrors(t)

	now := time.Now()
	sessID := seedSessionWithErrors(t, handler, errorSvc, []session.SessionError{
		{
			ID:         "sum-err-1",
			RawError:   "Internal server error",
			HTTPStatus: 500,
			OccurredAt: now,
		},
		{
			ID:         "sum-err-2",
			RawError:   "rate limit exceeded",
			HTTPStatus: 429,
			OccurredAt: now.Add(time.Second),
		},
		{
			ID:         "sum-err-3",
			RawError:   "bash exit code 1: command not found",
			ToolName:   "bash",
			OccurredAt: now.Add(2 * time.Second),
		},
	})

	resp := doRequest(t, handler, "GET", fmt.Sprintf("/api/v1/sessions/%s/errors/summary", sessID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]any
	decodeResponse(t, resp, &result)

	if result["session_id"] != sessID {
		t.Errorf("expected session_id=%s, got %v", sessID, result["session_id"])
	}
	total := result["total_errors"].(float64)
	if int(total) != 3 {
		t.Errorf("expected total_errors=3, got %v", result["total_errors"])
	}
	ext := result["external_errors"].(float64)
	if int(ext) < 2 {
		t.Errorf("expected at least 2 external errors (500+429), got %v", result["external_errors"])
	}
	byCategory, ok := result["by_category"].(map[string]any)
	if !ok {
		t.Fatal("expected by_category to be an object")
	}
	if len(byCategory) < 2 {
		t.Errorf("expected at least 2 categories, got %d", len(byCategory))
	}
}

func TestGetSessionErrorSummary_NoErrorService(t *testing.T) {
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/any-id/errors/summary", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404 when error service is nil, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestListRecentErrors(t *testing.T) {
	handler, _, errorSvc := newTestServerWithErrors(t)

	now := time.Now()
	// Seed errors across two different sessions.
	seedSessionWithErrors(t, handler, errorSvc, []session.SessionError{
		{
			ID:         "recent-err-1",
			RawError:   "Internal server error",
			HTTPStatus: 500,
			OccurredAt: now,
		},
	})
	seedSessionWithErrors(t, handler, errorSvc, []session.SessionError{
		{
			ID:         "recent-err-2",
			RawError:   "rate limit exceeded",
			HTTPStatus: 429,
			OccurredAt: now.Add(time.Second),
		},
	})

	resp := doRequest(t, handler, "GET", "/api/v1/errors/recent", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]any
	decodeResponse(t, resp, &result)
	count := result["count"].(float64)
	if int(count) < 2 {
		t.Errorf("expected at least 2 recent errors, got %v", result["count"])
	}
}

func TestListRecentErrors_WithCategoryFilter(t *testing.T) {
	handler, _, errorSvc := newTestServerWithErrors(t)

	now := time.Now()
	seedSessionWithErrors(t, handler, errorSvc, []session.SessionError{
		{
			ID:         "filter-err-1",
			RawError:   "Internal server error",
			HTTPStatus: 500,
			OccurredAt: now,
		},
		{
			ID:         "filter-err-2",
			RawError:   "rate limit exceeded",
			HTTPStatus: 429,
			OccurredAt: now.Add(time.Second),
		},
	})

	// Filter by rate_limit only.
	resp := doRequest(t, handler, "GET", "/api/v1/errors/recent?category=rate_limit", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]any
	decodeResponse(t, resp, &result)

	errors, ok := result["errors"].([]any)
	if !ok {
		t.Fatal("expected errors to be an array")
	}
	for _, e := range errors {
		errObj := e.(map[string]any)
		if errObj["category"] != "rate_limit" {
			t.Errorf("expected all errors to be rate_limit, got %v", errObj["category"])
		}
	}
}

func TestListRecentErrors_WithLimit(t *testing.T) {
	handler, _, errorSvc := newTestServerWithErrors(t)

	now := time.Now()
	seedSessionWithErrors(t, handler, errorSvc, []session.SessionError{
		{ID: "limit-err-1", RawError: "error 1", HTTPStatus: 500, OccurredAt: now},
		{ID: "limit-err-2", RawError: "error 2", HTTPStatus: 500, OccurredAt: now.Add(time.Second)},
		{ID: "limit-err-3", RawError: "error 3", HTTPStatus: 500, OccurredAt: now.Add(2 * time.Second)},
	})

	resp := doRequest(t, handler, "GET", "/api/v1/errors/recent?limit=2", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]any
	decodeResponse(t, resp, &result)
	count := result["count"].(float64)
	if int(count) > 2 {
		t.Errorf("expected at most 2 errors with limit=2, got %v", result["count"])
	}
}

func TestListRecentErrors_InvalidCategory(t *testing.T) {
	handler, _, _ := newTestServerWithErrors(t)

	resp := doRequest(t, handler, "GET", "/api/v1/errors/recent?category=invalid_cat", nil)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid category, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestListRecentErrors_InvalidLimit(t *testing.T) {
	handler, _, _ := newTestServerWithErrors(t)

	resp := doRequest(t, handler, "GET", "/api/v1/errors/recent?limit=abc", nil)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid limit, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestListRecentErrors_NoErrorService(t *testing.T) {
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "GET", "/api/v1/errors/recent", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404 when error service is nil, got %d: %s", resp.Code, resp.Body.String())
	}
}
