package client_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/ChristopherAparicio/aisync/client"
	"github.com/ChristopherAparicio/aisync/internal/api"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

// newTestClient creates an httptest server backed by a real SQLite store
// and returns a client.Client pointed at it.
func newTestClient(t *testing.T) *client.Client {
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

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return client.New(ts.URL)
}

// seedViaClient imports a test session via the client SDK.
func seedViaClient(t *testing.T, c *client.Client, id string) {
	t.Helper()
	sess := testutil.NewSession(id)
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	result, err := c.Import(client.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.SessionID != id {
		t.Fatalf("expected session ID %s, got %s", id, result.SessionID)
	}
}

// ── Health ──

func TestClientHealth(t *testing.T) {
	c := newTestClient(t)
	if err := c.Health(); err != nil {
		t.Fatalf("health: %v", err)
	}
}

// ── Get ──

func TestClientGet(t *testing.T) {
	c := newTestClient(t)
	seedViaClient(t, c, "get-test")

	sess, err := c.Get("get-test")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sess.ID != "get-test" {
		t.Errorf("expected ID get-test, got %s", sess.ID)
	}
	if sess.Provider != string(session.ProviderClaudeCode) {
		t.Errorf("expected provider claude-code, got %s", sess.Provider)
	}
	if len(sess.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(sess.Messages))
	}
}

func TestClientGetNotFound(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !client.IsNotFound(err) {
		t.Errorf("expected IsNotFound=true, got error: %v", err)
	}
}

// ── List ──

func TestClientList(t *testing.T) {
	c := newTestClient(t)
	seedViaClient(t, c, "list-a")
	seedViaClient(t, c, "list-b")

	summaries, err := c.List(client.ListOptions{All: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(summaries) < 2 {
		t.Errorf("expected at least 2 summaries, got %d", len(summaries))
	}
}

// ── Delete ──

func TestClientDelete(t *testing.T) {
	c := newTestClient(t)
	seedViaClient(t, c, "delete-me")

	if err := c.Delete("delete-me"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := c.Get("delete-me")
	if !client.IsNotFound(err) {
		t.Errorf("expected IsNotFound after delete, got: %v", err)
	}
}

func TestClientDeleteNotFound(t *testing.T) {
	c := newTestClient(t)
	err := c.Delete("does-not-exist")
	if !client.IsNotFound(err) {
		t.Errorf("expected IsNotFound, got: %v", err)
	}
}

// ── Export ──

func TestClientExport(t *testing.T) {
	c := newTestClient(t)
	seedViaClient(t, c, "export-sdk")

	result, err := c.Export(client.ExportRequest{
		SessionID: "export-sdk",
		Format:    "aisync",
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	if result.Format != "aisync" {
		t.Errorf("expected format aisync, got %s", result.Format)
	}
	if result.SessionID != "export-sdk" {
		t.Errorf("expected session_id export-sdk, got %s", result.SessionID)
	}
	if len(result.Data) == 0 {
		t.Error("expected non-empty export data")
	}

	// Verify exported data is valid session JSON
	var exported client.Session
	if err := json.Unmarshal(result.Data, &exported); err != nil {
		t.Fatalf("unmarshal exported data: %v", err)
	}
	if exported.ID != "export-sdk" {
		t.Errorf("exported session ID mismatch: %s", exported.ID)
	}
}

// ── Import ──

func TestClientImport(t *testing.T) {
	c := newTestClient(t)

	sess := testutil.NewSession("import-sdk")
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	result, err := c.Import(client.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.SessionID != "import-sdk" {
		t.Errorf("expected session ID import-sdk, got %s", result.SessionID)
	}
	if result.SourceFormat != "aisync" {
		t.Errorf("expected source format aisync, got %s", result.SourceFormat)
	}

	// Verify retrievable
	got, err := c.Get("import-sdk")
	if err != nil {
		t.Fatalf("get after import: %v", err)
	}
	if got.ID != "import-sdk" {
		t.Errorf("retrieved session ID mismatch: %s", got.ID)
	}
}

// ── Stats ──

func TestClientStats(t *testing.T) {
	c := newTestClient(t)
	seedViaClient(t, c, "stats-a")
	seedViaClient(t, c, "stats-b")

	result, err := c.Stats(client.StatsOptions{All: true})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if result.TotalSessions < 2 {
		t.Errorf("expected at least 2 sessions, got %d", result.TotalSessions)
	}
	if result.TotalTokens <= 0 {
		t.Errorf("expected positive total tokens, got %d", result.TotalTokens)
	}
}

// ── Sync unavailable ──

func TestClientSyncUnavailable(t *testing.T) {
	c := newTestClient(t) // no SyncService configured

	t.Run("Push", func(t *testing.T) {
		_, err := c.Push(false)
		if !client.IsUnavailable(err) {
			t.Errorf("expected IsUnavailable, got: %v", err)
		}
	})

	t.Run("Pull", func(t *testing.T) {
		_, err := c.Pull(false)
		if !client.IsUnavailable(err) {
			t.Errorf("expected IsUnavailable, got: %v", err)
		}
	})

	t.Run("Sync", func(t *testing.T) {
		_, err := c.Sync(false)
		if !client.IsUnavailable(err) {
			t.Errorf("expected IsUnavailable, got: %v", err)
		}
	})

	t.Run("ReadIndex", func(t *testing.T) {
		_, err := c.ReadIndex()
		if !client.IsUnavailable(err) {
			t.Errorf("expected IsUnavailable, got: %v", err)
		}
	})
}

// ── Error type assertions ──

func TestAPIErrorFormat(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}

	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("expected *client.APIError, got %T", err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("expected status 404, got %d", apiErr.StatusCode)
	}
	if apiErr.Code != "session_not_found" {
		t.Errorf("expected code session_not_found, got %q", apiErr.Code)
	}
}

// ── Round-trip: import → export → re-import ──

func TestRoundTrip(t *testing.T) {
	c := newTestClient(t)

	// 1. Import a session
	original := testutil.NewSession("roundtrip-1")
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	_, err = c.Import(client.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// 2. Export it
	exported, err := c.Export(client.ExportRequest{
		SessionID: "roundtrip-1",
		Format:    "aisync",
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// 3. Delete and re-import the exported data
	if err := c.Delete("roundtrip-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = c.Import(client.ImportRequest{
		Data:         exported.Data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	})
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}

	// 4. Verify the re-imported session matches
	got, err := c.Get("roundtrip-1")
	if err != nil {
		t.Fatalf("get after re-import: %v", err)
	}
	if got.ID != "roundtrip-1" {
		t.Errorf("expected roundtrip-1, got %s", got.ID)
	}
	if len(got.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(got.Messages))
	}
}
