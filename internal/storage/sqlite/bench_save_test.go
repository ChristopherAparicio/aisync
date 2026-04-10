package sqlite

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// benchSession creates a session with n messages for benchmarking.
// Each message includes realistic content, token usage, and tool calls
// to approximate real-world payload sizes.
func benchSession(id string, msgCount int) *session.Session {
	now := time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC)
	msgs := make([]session.Message, msgCount)

	for i := range msgs {
		role := session.RoleUser
		if i%2 == 1 {
			role = session.RoleAssistant
		}

		msg := session.Message{
			ID:           fmt.Sprintf("msg-%04d", i),
			Role:         role,
			Content:      fmt.Sprintf("This is message %d with some realistic content that mimics a typical conversation turn between a user and an AI coding assistant. It includes details about code, file paths, and technical discussions that are common in aisync captures.", i),
			Timestamp:    now.Add(time.Duration(i) * time.Minute),
			InputTokens:  1200 + i*10,
			OutputTokens: 800 + i*5,
		}

		// Assistant messages get tool calls (~30% of messages).
		if role == session.RoleAssistant && i%3 == 0 {
			msg.ToolCalls = []session.ToolCall{
				{
					ID:     fmt.Sprintf("tc-%04d", i),
					Name:   "read_file",
					Input:  fmt.Sprintf(`{"path": "src/auth/handler_%d.go"}`, i),
					Output: fmt.Sprintf("package auth\n\nfunc Handle%d() error {\n\treturn nil\n}\n", i),
					State:  session.ToolStateCompleted,
				},
			}
		}

		msgs[i] = msg
	}

	return &session.Session{
		ID:          session.ID(id),
		Version:     1,
		Provider:    session.ProviderOpenCode,
		Agent:       "claude",
		Branch:      "feature/capture-optimization",
		ProjectPath: "/home/dev/project",
		ExportedAt:  now,
		CreatedAt:   now,
		Summary:     "Benchmark session for capture pipeline optimization",
		StorageMode: session.StorageModeCompact,
		Messages:    msgs,
		TokenUsage: session.TokenUsage{
			InputTokens:  int(float64(msgCount) * 1200),
			OutputTokens: int(float64(msgCount) * 800),
			TotalTokens:  int(float64(msgCount) * 2000),
		},
		FileChanges: []session.FileChange{
			{FilePath: "internal/capture/service.go", ChangeType: session.ChangeModified},
			{FilePath: "internal/service/session_capture.go", ChangeType: session.ChangeModified},
		},
		Links: []session.Link{
			{LinkType: session.LinkBranch, Ref: "feature/capture-optimization"},
		},
	}
}

// BenchmarkSave measures the cost of a single Save() call (marshal → compress → UPSERT)
// at different session sizes. This is the operation we eliminated one instance of
// in the Phase A capture pipeline optimization.
func BenchmarkSave(b *testing.B) {
	for _, msgCount := range []int{50, 200, 500} {
		b.Run(fmt.Sprintf("msgs_%d", msgCount), func(b *testing.B) {
			dbPath := filepath.Join(b.TempDir(), "bench.db")
			store, err := New(dbPath)
			if err != nil {
				b.Fatalf("New() error = %v", err)
			}
			defer store.Close()

			sess := benchSession("bench-session", msgCount)

			// Warm up: first save creates the row.
			if err := store.Save(sess); err != nil {
				b.Fatalf("Save() warm-up error = %v", err)
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := store.Save(sess); err != nil {
					b.Fatalf("Save() error = %v", err)
				}
			}
		})
	}
}

// BenchmarkSaveDouble measures the cost of two consecutive Save() calls
// on the same session (the pattern we eliminated). Compare with BenchmarkSave
// to see the savings: the difference is exactly one redundant Save().
func BenchmarkSaveDouble(b *testing.B) {
	for _, msgCount := range []int{50, 200, 500} {
		b.Run(fmt.Sprintf("msgs_%d", msgCount), func(b *testing.B) {
			dbPath := filepath.Join(b.TempDir(), "bench.db")
			store, err := New(dbPath)
			if err != nil {
				b.Fatalf("New() error = %v", err)
			}
			defer store.Close()

			sess := benchSession("bench-session", msgCount)

			// Warm up.
			if err := store.Save(sess); err != nil {
				b.Fatalf("Save() warm-up error = %v", err)
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// Simulate the OLD pattern: Save #1 (capture) → Save #2 (stampCosts).
				if err := store.Save(sess); err != nil {
					b.Fatalf("Save() #1 error = %v", err)
				}
				if err := store.Save(sess); err != nil {
					b.Fatalf("Save() #2 error = %v", err)
				}
			}
		})
	}
}

// BenchmarkSaveTriple measures the cost of three consecutive Save() calls
// (the worst case: capture → stampCosts → AI summarize). This was the
// pre-optimization path when --summarize was used.
func BenchmarkSaveTriple(b *testing.B) {
	for _, msgCount := range []int{50, 200, 500} {
		b.Run(fmt.Sprintf("msgs_%d", msgCount), func(b *testing.B) {
			dbPath := filepath.Join(b.TempDir(), "bench.db")
			store, err := New(dbPath)
			if err != nil {
				b.Fatalf("New() error = %v", err)
			}
			defer store.Close()

			sess := benchSession("bench-session", msgCount)

			// Warm up.
			if err := store.Save(sess); err != nil {
				b.Fatalf("Save() warm-up error = %v", err)
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// Simulate the OLD worst-case: Save #1 → Save #2 → Save #3.
				if err := store.Save(sess); err != nil {
					b.Fatalf("Save() #1 error = %v", err)
				}
				if err := store.Save(sess); err != nil {
					b.Fatalf("Save() #2 error = %v", err)
				}
				if err := store.Save(sess); err != nil {
					b.Fatalf("Save() #3 error = %v", err)
				}
			}
		})
	}
}
