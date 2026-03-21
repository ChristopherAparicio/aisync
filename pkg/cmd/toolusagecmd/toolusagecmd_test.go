package toolusagecmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func toolUsageTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)

	if store == nil {
		store = testutil.NewMockStore()
	}
	gitClient := git.NewClient(repoDir)

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (storage.Store, error) { return store, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store: store,
				Git:   gitClient,
			}), nil
		},
	}

	return f, ios
}

func TestNewCmdToolUsage_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdToolUsage(f)

	if cmd.Flags().Lookup("json") == nil {
		t.Error("expected --json flag")
	}
}

func TestToolUsage_tableOutput(t *testing.T) {
	store := testutil.NewMockStore()
	now := time.Now()

	sess := &session.Session{
		ID:       "tool-test",
		Provider: session.ProviderClaudeCode,
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "fix the bug", Timestamp: now},
			{
				ID: "m2", Role: session.RoleAssistant, Content: "I'll read the file", Timestamp: now,
				ToolCalls: []session.ToolCall{
					{ID: "tc1", Name: "Read", Input: "src/main.go", InputTokens: 100, OutputTokens: 50, State: session.ToolStateCompleted},
					{ID: "tc2", Name: "Edit", Input: "src/main.go", InputTokens: 200, OutputTokens: 150, State: session.ToolStateCompleted},
				},
			},
			{
				ID: "m3", Role: session.RoleAssistant, Content: "Done", Timestamp: now,
				ToolCalls: []session.ToolCall{
					{ID: "tc3", Name: "Read", Input: "src/util.go", InputTokens: 80, OutputTokens: 40, State: session.ToolStateCompleted},
				},
			},
		},
		TokenUsage: session.TokenUsage{InputTokens: 380, OutputTokens: 240, TotalTokens: 620},
		CreatedAt:  now,
	}
	_ = store.Save(sess)

	f, ios := toolUsageTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "tool-test",
		JSON:      false,
	}

	err := runToolUsage(opts)
	if err != nil {
		t.Fatalf("runToolUsage() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	// Should show the table header
	if !strings.Contains(output, "Tool Usage") {
		t.Errorf("expected 'Tool Usage' header, got:\n%s", output)
	}
	// Should contain tool names
	if !strings.Contains(output, "Read") {
		t.Errorf("expected 'Read' tool in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Edit") {
		t.Errorf("expected 'Edit' tool in output, got:\n%s", output)
	}
	// Should show total calls (3 tool calls)
	if !strings.Contains(output, "3 total calls") {
		t.Errorf("expected '3 total calls', got:\n%s", output)
	}
}

func TestToolUsage_jsonOutput(t *testing.T) {
	store := testutil.NewMockStore()
	now := time.Now()

	sess := &session.Session{
		ID:       "tool-json",
		Provider: session.ProviderClaudeCode,
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "fix it", Timestamp: now},
			{
				ID: "m2", Role: session.RoleAssistant, Content: "reading", Timestamp: now,
				ToolCalls: []session.ToolCall{
					{ID: "tc1", Name: "Read", Input: "f.go", InputTokens: 100, OutputTokens: 50, State: session.ToolStateCompleted},
				},
			},
		},
		TokenUsage: session.TokenUsage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		CreatedAt:  now,
	}
	_ = store.Save(sess)

	f, ios := toolUsageTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "tool-json",
		JSON:      true,
	}

	err := runToolUsage(opts)
	if err != nil {
		t.Fatalf("runToolUsage() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	// Verify it's valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	// Check expected top-level keys
	for _, key := range []string{"tools", "total_calls"} {
		if _, ok := result[key]; !ok {
			t.Errorf("expected JSON key %q, got: %s", key, output)
		}
	}
}

func TestToolUsage_noToolCalls(t *testing.T) {
	store := testutil.NewMockStore()
	now := time.Now()

	sess := &session.Session{
		ID:       "no-tools",
		Provider: session.ProviderClaudeCode,
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hello", Timestamp: now},
			{ID: "m2", Role: session.RoleAssistant, Content: "hi there", Timestamp: now},
		},
		CreatedAt: now,
	}
	_ = store.Save(sess)

	f, ios := toolUsageTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "no-tools",
	}

	err := runToolUsage(opts)
	if err != nil {
		t.Fatalf("runToolUsage() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No tool calls found") {
		t.Errorf("expected 'No tool calls found', got:\n%s", output)
	}
}

func TestToolUsage_serviceError(t *testing.T) {
	ios := iostreams.Test()

	f := &cmdutil.Factory{
		IOStreams: ios,
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return nil, fmt.Errorf("database connection failed")
		},
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "any",
	}

	err := runToolUsage(opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "database connection failed") {
		t.Errorf("expected 'database connection failed' in error, got: %v", err)
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		input int
	}{
		{"zero", "0", 0},
		{"small", "500", 500},
		{"thousands", "1.5k", 1500},
		{"millions", "1.5M", 1500000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTokens(tt.input)
			if got != tt.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatCost(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		input float64
	}{
		{"zero", "$0.00", 0},
		{"tiny", "$0.0050", 0.005},
		{"normal", "$1.50", 1.50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCost(tt.input)
			if got != tt.want {
				t.Errorf("formatCost(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		maxLen int
	}{
		{"short", "hello", "hello", 10},
		{"exact", "hello", "hello", 5},
		{"long", "hello world", "hello w…", 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
