package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	gomcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

// newTestHandlers creates a handlers struct backed by a temporary SQLite store.
func newTestHandlers(t *testing.T) (*handlers, *service.SessionService) {
	t.Helper()
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	h := &handlers{
		sessionSvc: sessionSvc,
	}
	return h, sessionSvc
}

// callToolReq constructs a CallToolRequest with the given arguments map.
func callToolReq(name string, args map[string]any) gomcp.CallToolRequest {
	return gomcp.CallToolRequest{
		Params: gomcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}
}

// seedSession imports a test session and returns it.
func seedSession(t *testing.T, svc *service.SessionService, id string) *session.Session {
	t.Helper()
	sess := testutil.NewSession(id)
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}

	_, err = svc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	})
	if err != nil {
		t.Fatalf("import session: %v", err)
	}
	return sess
}

// requireTextResult extracts the text content from a tool result, failing if it's an error result.
func requireTextResult(t *testing.T, result *gomcp.CallToolResult) string {
	t.Helper()
	if result.IsError {
		// Extract error text from content
		for _, c := range result.Content {
			if tc, ok := c.(gomcp.TextContent); ok {
				t.Fatalf("unexpected tool error: %s", tc.Text)
			}
		}
		t.Fatal("unexpected tool error with no text content")
	}

	for _, c := range result.Content {
		if tc, ok := c.(gomcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content in result")
	return ""
}

// requireErrorResult extracts the error text from a tool result, failing if it's not an error.
func requireErrorResult(t *testing.T, result *gomcp.CallToolResult) string {
	t.Helper()
	if !result.IsError {
		t.Fatal("expected error result, got success")
	}

	for _, c := range result.Content {
		if tc, ok := c.(gomcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content in error result")
	return ""
}

// ── Get ──

func TestHandleGet(t *testing.T) {
	h, svc := newTestHandlers(t)
	sess := seedSession(t, svc, "get-test")

	req := callToolReq("aisync_get", map[string]any{
		"id": string(sess.ID),
	})

	result, err := h.handleGet(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGet error: %v", err)
	}

	text := requireTextResult(t, result)

	var got session.Session
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("expected ID %s, got %s", sess.ID, got.ID)
	}
}

func TestHandleGetNotFound(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_get", map[string]any{
		"id": "nonexistent",
	})

	result, err := h.handleGet(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGet error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", errText)
	}
}

// ── List ──

func TestHandleList(t *testing.T) {
	h, svc := newTestHandlers(t)
	seedSession(t, svc, "list-1")
	seedSession(t, svc, "list-2")

	req := callToolReq("aisync_list", map[string]any{
		"all": true,
	})

	result, err := h.handleList(context.Background(), req)
	if err != nil {
		t.Fatalf("handleList error: %v", err)
	}

	text := requireTextResult(t, result)

	var summaries []session.Summary
	if err := json.Unmarshal([]byte(text), &summaries); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(summaries) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(summaries))
	}
}

// ── Delete ──

func TestHandleDelete(t *testing.T) {
	h, svc := newTestHandlers(t)
	sess := seedSession(t, svc, "delete-me")

	req := callToolReq("aisync_delete", map[string]any{
		"id": string(sess.ID),
	})

	result, err := h.handleDelete(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDelete error: %v", err)
	}

	text := requireTextResult(t, result)
	if !strings.Contains(text, "deleted") {
		t.Errorf("expected 'deleted' in response, got: %s", text)
	}

	// Verify it's gone
	getReq := callToolReq("aisync_get", map[string]any{
		"id": string(sess.ID),
	})
	getResult, err := h.handleGet(context.Background(), getReq)
	if err != nil {
		t.Fatalf("handleGet after delete error: %v", err)
	}
	if !getResult.IsError {
		t.Error("expected error after delete, got success")
	}
}

func TestHandleDeleteNotFound(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_delete", map[string]any{
		"id": "00000000-0000-0000-0000-000000000000",
	})

	result, err := h.handleDelete(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDelete error: %v", err)
	}

	requireErrorResult(t, result)
}

// ── Export ──

func TestHandleExport(t *testing.T) {
	h, svc := newTestHandlers(t)
	sess := seedSession(t, svc, "export-test")

	req := callToolReq("aisync_export", map[string]any{
		"session_id": string(sess.ID),
		"format":     "aisync",
	})

	result, err := h.handleExport(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExport error: %v", err)
	}

	text := requireTextResult(t, result)

	// Exported data should be valid JSON containing the session
	var exported session.Session
	if err := json.Unmarshal([]byte(text), &exported); err != nil {
		t.Fatalf("unmarshal exported: %v", err)
	}
	if exported.ID != sess.ID {
		t.Errorf("expected ID %s, got %s", sess.ID, exported.ID)
	}
}

// ── Import ──

func TestHandleImport(t *testing.T) {
	h, _ := newTestHandlers(t)

	sess := testutil.NewSession("import-via-mcp")
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := callToolReq("aisync_import", map[string]any{
		"data":          string(data),
		"source_format": "aisync",
		"into_target":   "aisync",
	})

	result, err := h.handleImport(context.Background(), req)
	if err != nil {
		t.Fatalf("handleImport error: %v", err)
	}

	text := requireTextResult(t, result)
	if !strings.Contains(text, string(sess.ID)) {
		t.Errorf("expected session ID in result, got: %s", text)
	}

	// Verify retrievable
	getReq := callToolReq("aisync_get", map[string]any{
		"id": string(sess.ID),
	})
	getResult, err := h.handleGet(context.Background(), getReq)
	if err != nil {
		t.Fatalf("handleGet after import: %v", err)
	}
	requireTextResult(t, getResult) // no error = found
}

// ── Search ──

func TestHandleSearch(t *testing.T) {
	h, svc := newTestHandlers(t)
	seedSession(t, svc, "search-1")
	seedSession(t, svc, "search-2")

	req := callToolReq("aisync_search", map[string]any{
		"keyword": "Test session",
	})

	result, err := h.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSearch error: %v", err)
	}

	text := requireTextResult(t, result)

	var searchResult session.SearchResult
	if err := json.Unmarshal([]byte(text), &searchResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if searchResult.TotalCount < 2 {
		t.Errorf("expected at least 2 results, got %d", searchResult.TotalCount)
	}
}

func TestHandleSearchNoResults(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_search", map[string]any{
		"keyword": "nonexistent-query",
	})

	result, err := h.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSearch error: %v", err)
	}

	text := requireTextResult(t, result)

	var searchResult session.SearchResult
	if err := json.Unmarshal([]byte(text), &searchResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if searchResult.TotalCount != 0 {
		t.Errorf("expected 0 results, got %d", searchResult.TotalCount)
	}
}

func TestHandleSearchWithFilters(t *testing.T) {
	h, svc := newTestHandlers(t)
	seedSession(t, svc, "filter-1")
	seedSession(t, svc, "filter-2")

	req := callToolReq("aisync_search", map[string]any{
		"branch": "feature/test",
		"limit":  float64(10),
		"offset": float64(0),
	})

	result, err := h.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSearch error: %v", err)
	}

	text := requireTextResult(t, result)

	var searchResult session.SearchResult
	if err := json.Unmarshal([]byte(text), &searchResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if searchResult.TotalCount < 2 {
		t.Errorf("expected at least 2 results for branch filter, got %d", searchResult.TotalCount)
	}
	if searchResult.Limit != 10 {
		t.Errorf("expected limit=10, got %d", searchResult.Limit)
	}
}

func TestHandleSearchBadProvider(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_search", map[string]any{
		"provider": "invalid",
	})

	result, err := h.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSearch error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "unknown provider") {
		t.Errorf("expected 'unknown provider' in error, got: %s", errText)
	}
}

// ── Blame ──

func TestHandleBlame(t *testing.T) {
	h, svc := newTestHandlers(t)
	seedSession(t, svc, "blame-mcp-1")

	req := callToolReq("aisync_blame", map[string]any{
		"file": "src/main.go", // matches testutil.NewSession's FileChanges
		"all":  true,
	})

	result, err := h.handleBlame(context.Background(), req)
	if err != nil {
		t.Fatalf("handleBlame error: %v", err)
	}

	text := requireTextResult(t, result)

	var blameResult struct {
		Entries []struct {
			SessionID string `json:"session_id"`
		} `json:"Entries"`
	}
	if err := json.Unmarshal([]byte(text), &blameResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(blameResult.Entries) < 1 {
		t.Errorf("expected at least 1 blame entry, got %d", len(blameResult.Entries))
	}
}

func TestHandleBlame_NoFile(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_blame", map[string]any{
		"file": "",
	})

	result, err := h.handleBlame(context.Background(), req)
	if err != nil {
		t.Fatalf("handleBlame error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "file") {
		t.Errorf("expected error about file, got: %s", errText)
	}
}

func TestHandleBlame_NoResults(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_blame", map[string]any{
		"file": "nonexistent.go",
	})

	result, err := h.handleBlame(context.Background(), req)
	if err != nil {
		t.Fatalf("handleBlame error: %v", err)
	}

	text := requireTextResult(t, result)

	var blameResult struct {
		Entries []struct{} `json:"Entries"`
	}
	if err := json.Unmarshal([]byte(text), &blameResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(blameResult.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(blameResult.Entries))
	}
}

// ── Stats ──

func TestHandleStats(t *testing.T) {
	h, svc := newTestHandlers(t)
	seedSession(t, svc, "stats-1")
	seedSession(t, svc, "stats-2")

	req := callToolReq("aisync_stats", map[string]any{
		"all": true,
	})

	result, err := h.handleStats(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStats error: %v", err)
	}

	text := requireTextResult(t, result)

	var stats struct {
		TotalSessions int `json:"TotalSessions"`
		TotalTokens   int `json:"TotalTokens"`
	}
	if err := json.Unmarshal([]byte(text), &stats); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
	if stats.TotalSessions < 2 {
		t.Errorf("expected at least 2 sessions, got %d", stats.TotalSessions)
	}
}

// ── Ingest ──

func TestHandleIngest(t *testing.T) {
	h, _ := newTestHandlers(t)

	messagesJSON := `[{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi there","model":"qwen3:30b","input_tokens":100,"output_tokens":20}]`

	req := callToolReq("aisync_ingest", map[string]any{
		"provider":      "parlay",
		"messages_json": messagesJSON,
		"agent":         "jarvis",
		"summary":       "Test ingest via MCP",
	})

	result, err := h.handleIngest(context.Background(), req)
	if err != nil {
		t.Fatalf("handleIngest error: %v", err)
	}

	text := requireTextResult(t, result)

	var ingestResult service.IngestResult
	if err := json.Unmarshal([]byte(text), &ingestResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if ingestResult.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if ingestResult.Provider != "parlay" {
		t.Errorf("expected provider 'parlay', got %q", ingestResult.Provider)
	}

	// Verify retrievable via get.
	getReq := callToolReq("aisync_get", map[string]any{
		"id": string(ingestResult.SessionID),
	})
	getResult, err := h.handleGet(context.Background(), getReq)
	if err != nil {
		t.Fatalf("handleGet after ingest: %v", err)
	}
	requireTextResult(t, getResult)
}

func TestHandleIngest_WithToolCalls(t *testing.T) {
	h, _ := newTestHandlers(t)

	messagesJSON := `[{"role":"user","content":"What time is it?"},{"role":"assistant","content":"Let me check...","tool_calls":[{"name":"bash","input":"date +%H:%M","output":"14:30","state":"completed"}],"input_tokens":50,"output_tokens":15}]`

	req := callToolReq("aisync_ingest", map[string]any{
		"provider":      "ollama",
		"messages_json": messagesJSON,
		"session_id":    "custom-ingest-id",
	})

	result, err := h.handleIngest(context.Background(), req)
	if err != nil {
		t.Fatalf("handleIngest error: %v", err)
	}

	text := requireTextResult(t, result)

	var ingestResult service.IngestResult
	if err := json.Unmarshal([]byte(text), &ingestResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if string(ingestResult.SessionID) != "custom-ingest-id" {
		t.Errorf("expected session ID 'custom-ingest-id', got %q", ingestResult.SessionID)
	}
}

func TestHandleIngest_MissingProvider(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_ingest", map[string]any{
		"messages_json": `[{"role":"user","content":"Hello"}]`,
	})

	result, err := h.handleIngest(context.Background(), req)
	if err != nil {
		t.Fatalf("handleIngest error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "provider") {
		t.Errorf("expected error about provider, got: %s", errText)
	}
}

func TestHandleIngest_MissingMessages(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_ingest", map[string]any{
		"provider": "parlay",
	})

	result, err := h.handleIngest(context.Background(), req)
	if err != nil {
		t.Fatalf("handleIngest error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "messages_json") {
		t.Errorf("expected error about messages_json, got: %s", errText)
	}
}

func TestHandleIngest_InvalidMessagesJSON(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_ingest", map[string]any{
		"provider":      "parlay",
		"messages_json": "not valid json",
	})

	result, err := h.handleIngest(context.Background(), req)
	if err != nil {
		t.Fatalf("handleIngest error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "invalid messages_json") {
		t.Errorf("expected 'invalid messages_json' in error, got: %s", errText)
	}
}

func TestHandleIngest_BadProvider(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_ingest", map[string]any{
		"provider":      "nonexistent",
		"messages_json": `[{"role":"user","content":"Hello"}]`,
	})

	result, err := h.handleIngest(context.Background(), req)
	if err != nil {
		t.Fatalf("handleIngest error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "unknown provider") {
		t.Errorf("expected 'unknown provider' in error, got: %s", errText)
	}
}

// ── Sync without SyncService ──

func TestSyncToolsUnavailable(t *testing.T) {
	h, _ := newTestHandlers(t) // no SyncService

	tests := []struct {
		name    string
		handler func(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error)
		args    map[string]any
	}{
		{"push", h.handlePush, map[string]any{"remote": false}},
		{"pull", h.handlePull, map[string]any{"remote": false}},
		{"sync", h.handleSync, map[string]any{"remote": false}},
		{"index", h.handleIndex, map[string]any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := callToolReq("aisync_"+tt.name, tt.args)
			result, err := tt.handler(context.Background(), req)
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}

			errText := requireErrorResult(t, result)
			if !strings.Contains(errText, "not available") {
				t.Errorf("expected 'not available' in error, got: %s", errText)
			}
		})
	}
}

// ── NewServer ──

func TestNewServerRegistersAllTools(t *testing.T) {
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	s := NewServer(Config{
		SessionService: sessionSvc,
		Version:        "test",
	})

	tools := s.ListTools()

	expectedTools := []string{
		"aisync_capture", "aisync_restore", "aisync_get", "aisync_list",
		"aisync_delete", "aisync_export", "aisync_import", "aisync_link",
		"aisync_comment", "aisync_search", "aisync_blame", "aisync_explain",
		"aisync_rewind", "aisync_stats", "aisync_cost",
		"aisync_tool_usage", "aisync_efficiency", "aisync_diff", "aisync_gc",
		"aisync_off_topic", "aisync_forecast", "aisync_validate", "aisync_ingest",
		"aisync_push", "aisync_pull", "aisync_sync", "aisync_index",
		"aisync_errors",
		"aisync_session_events", "aisync_session_event_summary", "aisync_event_buckets",
	}

	for _, name := range expectedTools {
		if _, ok := tools[name]; !ok {
			t.Errorf("expected tool %q to be registered", name)
		}
	}

	if len(tools) != len(expectedTools) {
		t.Errorf("expected %d tools, got %d", len(expectedTools), len(tools))
	}
}
