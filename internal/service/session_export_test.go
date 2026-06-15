package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

func bundleTestSession(id session.ID, branch string) *session.Session {
	return &session.Session{
		ID:          id,
		Provider:    session.ProviderClaudeCode,
		Branch:      branch,
		ProjectPath: "/tmp/proj",
		Version:     1,
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hi"},
			{ID: "m2", Role: session.RoleAssistant, Content: "hello"},
		},
	}
}

func TestExportAll_JSONLBundle(t *testing.T) {
	store := testutil.NewMockStore(
		bundleTestSession("sess-a", "feat/x"),
		bundleTestSession("sess-b", "feat/x"),
	)
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.ExportAll(ExportAllRequest{Branch: "feat/x"})
	if err != nil {
		t.Fatalf("ExportAll() error: %v", err)
	}
	if result.Count != 2 {
		t.Errorf("Count = %d, want 2", result.Count)
	}

	lines := strings.Split(strings.TrimSpace(string(result.Data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d JSONL lines, want 2", len(lines))
	}
	for i, ln := range lines {
		if !json.Valid([]byte(ln)) {
			t.Errorf("line %d is not valid JSON: %s", i, ln)
		}
	}
	if !IsBundle(result.Data) {
		t.Error("ExportAll output should be detected as a bundle")
	}
}

func TestExportAll_Empty(t *testing.T) {
	store := testutil.NewMockStore()
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.ExportAll(ExportAllRequest{Branch: "none"})
	if err != nil {
		t.Fatalf("ExportAll() error: %v", err)
	}
	if result.Count != 0 || len(result.Data) != 0 {
		t.Errorf("expected empty result, got count=%d len=%d", result.Count, len(result.Data))
	}
}

func TestImportBundle_Roundtrip(t *testing.T) {
	src := testutil.NewMockStore(
		bundleTestSession("sess-a", "feat/x"),
		bundleTestSession("sess-b", "feat/x"),
	)
	exported, err := NewSessionService(SessionServiceConfig{Store: src}).
		ExportAll(ExportAllRequest{Branch: "feat/x"})
	if err != nil {
		t.Fatalf("ExportAll() error: %v", err)
	}

	dst := testutil.NewMockStore()
	res, err := NewSessionService(SessionServiceConfig{Store: dst}).
		ImportBundle(ImportRequest{Data: exported.Data})
	if err != nil {
		t.Fatalf("ImportBundle() error: %v", err)
	}
	if res.Imported != 2 || res.Failed != 0 {
		t.Fatalf("Imported=%d Failed=%d, want 2/0", res.Imported, res.Failed)
	}

	for _, id := range []session.ID{"sess-a", "sess-b"} {
		if _, getErr := dst.Get(id); getErr != nil {
			t.Errorf("session %s not stored after bundle import: %v", id, getErr)
		}
	}
}

func TestImportBundle_PartialFailure(t *testing.T) {
	valid, err := json.Marshal(bundleTestSession("ok", "feat/x"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data := append(valid, '\n')
	data = append(data, []byte(`{"provider":"claude-code","messages":"not-an-array"}`+"\n")...)

	dst := testutil.NewMockStore()
	res, err := NewSessionService(SessionServiceConfig{Store: dst}).
		ImportBundle(ImportRequest{Data: data})
	if err != nil {
		t.Fatalf("ImportBundle() error: %v", err)
	}
	if res.Imported != 1 {
		t.Errorf("Imported = %d, want 1", res.Imported)
	}
	if res.Failed != 1 {
		t.Errorf("Failed = %d, want 1", res.Failed)
	}
	if len(res.Errors) != 1 {
		t.Errorf("Errors = %d, want 1", len(res.Errors))
	}
}

func TestIsBundle(t *testing.T) {
	single, err := json.MarshalIndent(bundleTestSession("x", "b"), "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"empty", []byte(""), false},
		{"whitespace", []byte("  \n  "), false},
		{"pretty-printed single export", single, false},
		{"claude jsonl", []byte(`{"type":"user","message":{}}` + "\n"), false},
		{"single compact session is not a bundle", []byte(`{"provider":"claude-code","messages":[]}` + "\n"), false},
		{"multi bundle line", []byte(`{"provider":"opencode","messages":[]}` + "\n" + `{"provider":"claude-code","messages":[]}`), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBundle(tt.data); got != tt.want {
				t.Errorf("IsBundle() = %v, want %v", got, tt.want)
			}
		})
	}
}
