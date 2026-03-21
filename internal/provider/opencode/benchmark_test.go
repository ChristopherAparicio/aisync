package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Benchmark the full capture pipeline: DB read → Export → JSON marshal.
// Run with: go test -bench=. -benchmem -count=3 ./internal/provider/opencode/

// setupBenchDB creates an in-memory SQLite database with a session of the given size.
func setupBenchDB(b *testing.B, msgCount, partsPerMsg int) *Provider {
	b.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatalf("opening db: %v", err)
	}

	schema := `
		CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, sandboxes TEXT NOT NULL DEFAULT '[]');
		CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, slug TEXT NOT NULL DEFAULT '', directory TEXT NOT NULL, title TEXT NOT NULL, version TEXT NOT NULL DEFAULT '1.0.0', time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL);
		CREATE INDEX session_project_idx ON session (project_id);
		CREATE INDEX session_parent_idx ON session (parent_id);
		CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
		CREATE INDEX message_session_idx ON message (session_id);
		CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
		CREATE INDEX part_message_idx ON part (message_id);
		CREATE INDEX part_session_idx ON part (session_id);
	`
	if _, err := db.Exec(schema); err != nil {
		b.Fatalf("creating schema: %v", err)
	}

	// Insert project + session.
	db.Exec(`INSERT INTO project (id, worktree, time_created, time_updated, sandboxes) VALUES ('proj1', '/bench/project', 1000000, 1000000, '[]')`)
	db.Exec(`INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated) VALUES ('ses_bench', 'proj1', 'bench', '/bench/project', 'Benchmark session', '1.0.0', 1771245757992, 1771255877946)`)

	// Generate realistic message + part data.
	rng := rand.New(rand.NewSource(42))
	baseTime := int64(1771245758000)

	tx, _ := db.Begin()
	msgStmt, _ := tx.Prepare(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, 'ses_bench', ?, ?, ?)`)
	partStmt, _ := tx.Prepare(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, 'ses_bench', ?, ?, ?)`)

	for i := 0; i < msgCount; i++ {
		msgID := fmt.Sprintf("msg_%06d", i)
		ts := baseTime + int64(i*1000)

		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}

		msgData := map[string]interface{}{
			"role":       role,
			"agent":      "build",
			"modelID":    "claude-opus-4-6",
			"providerID": "anthropic",
			"cost":       0.03,
			"time":       map[string]interface{}{"created": ts, "completed": ts + 500},
			"tokens": map[string]interface{}{
				"input": 100 + rng.Intn(500), "output": 50 + rng.Intn(300),
				"reasoning": 0, "cache": map[string]interface{}{"read": rng.Intn(1000), "write": rng.Intn(200)},
			},
		}
		msgJSON, _ := json.Marshal(msgData)
		msgStmt.Exec(msgID, ts, ts, string(msgJSON))

		// Parts per message.
		for j := 0; j < partsPerMsg; j++ {
			partID := fmt.Sprintf("prt_%06d_%03d", i, j)
			partTS := ts + int64(j*100)

			var partData map[string]interface{}
			if j == 0 {
				// Text part with realistic-length content.
				text := generateText(rng, 200+rng.Intn(800))
				partData = map[string]interface{}{
					"type": "text",
					"text": text,
				}
			} else {
				// Tool part.
				partData = map[string]interface{}{
					"type":   "tool",
					"callID": fmt.Sprintf("call_%06d_%03d", i, j),
					"tool":   []string{"read", "write", "edit", "bash"}[rng.Intn(4)],
					"state": map[string]interface{}{
						"status": "completed",
						"input":  map[string]interface{}{"file_path": fmt.Sprintf("src/module%d/file%d.go", rng.Intn(20), rng.Intn(50))},
						"output": generateText(rng, 100+rng.Intn(2000)),
						"time":   map[string]interface{}{"start": partTS, "end": partTS + 200},
					},
				}
			}
			partJSON, _ := json.Marshal(partData)
			partStmt.Exec(partID, msgID, partTS, partTS, string(partJSON))
		}
	}

	msgStmt.Close()
	partStmt.Close()
	tx.Commit()

	b.Cleanup(func() { db.Close() })
	return &Provider{
		dataHome: b.TempDir(),
		reader:   &dbReader{db: db},
	}
}

func generateText(rng *rand.Rand, wordCount int) string {
	words := []string{"the", "function", "returns", "error", "nil", "package", "import", "struct",
		"interface", "method", "channel", "goroutine", "context", "cancel", "timeout",
		"database", "query", "insert", "update", "delete", "select", "from", "where",
		"create", "table", "index", "primary", "key", "foreign", "constraint",
		"if", "else", "for", "range", "switch", "case", "default", "break", "continue",
		"var", "const", "type", "func", "return", "defer", "go", "map", "slice", "string", "int"}
	var sb strings.Builder
	for i := 0; i < wordCount; i++ {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(words[rng.Intn(len(words))])
	}
	return sb.String()
}

// --- Benchmarks ---

// BenchmarkExport_Tiny: ~5 messages, 2 parts each = 10 parts
func BenchmarkExport_Tiny(b *testing.B) {
	p := setupBenchDB(b, 5, 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Export("ses_bench", session.StorageModeCompact)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExport_Small: ~30 messages, 3 parts each = 90 parts
func BenchmarkExport_Small(b *testing.B) {
	p := setupBenchDB(b, 30, 3)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Export("ses_bench", session.StorageModeCompact)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExport_Medium: ~100 messages, 4 parts each = 400 parts
func BenchmarkExport_Medium(b *testing.B) {
	p := setupBenchDB(b, 100, 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Export("ses_bench", session.StorageModeCompact)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExport_Large: ~400 messages, 4 parts each = 1600 parts
func BenchmarkExport_Large(b *testing.B) {
	p := setupBenchDB(b, 400, 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Export("ses_bench", session.StorageModeCompact)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExport_XLarge: ~800 messages, 5 parts each = 4000 parts
func BenchmarkExport_XLarge(b *testing.B) {
	p := setupBenchDB(b, 800, 5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Export("ses_bench", session.StorageModeCompact)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExport_Monster: ~2000 messages, 5 parts each = 10000 parts
func BenchmarkExport_Monster(b *testing.B) {
	p := setupBenchDB(b, 2000, 5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Export("ses_bench", session.StorageModeCompact)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExportAndMarshal measures Export + JSON serialization (the full pipeline minus store).
func BenchmarkExportAndMarshal_Medium(b *testing.B) {
	p := setupBenchDB(b, 100, 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess, err := p.Export("ses_bench", session.StorageModeCompact)
		if err != nil {
			b.Fatal(err)
		}
		data, err := json.Marshal(sess)
		if err != nil {
			b.Fatal(err)
		}
		_ = data
	}
}

func BenchmarkExportAndMarshal_Monster(b *testing.B) {
	p := setupBenchDB(b, 2000, 5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess, err := p.Export("ses_bench", session.StorageModeCompact)
		if err != nil {
			b.Fatal(err)
		}
		data, err := json.Marshal(sess)
		if err != nil {
			b.Fatal(err)
		}
		_ = data
	}
}

// BenchmarkDetect measures just the session listing (no export).
func BenchmarkDetect(b *testing.B) {
	p := setupBenchDB(b, 100, 3)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Detect("/bench/project", "")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFreshness measures just the freshness check (the skip path).
func BenchmarkFreshness(b *testing.B) {
	p := setupBenchDB(b, 2000, 5) // monster session
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.SessionFreshness("ses_bench")
		if err != nil {
			b.Fatal(err)
		}
	}
}
