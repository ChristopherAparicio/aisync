package hermes

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/provider/hermes/testdata"
)

func TestFixture(t *testing.T) {
	dbPath := testdata.NewFixtureDB(t)

	reader, err := newDBReader(filepath.Dir(dbPath))
	if err != nil {
		t.Fatalf("newDBReader() error = %v", err)
	}
	defer reader.close()

	if got := countRows(t, reader.db, "sessions"); got != 3 {
		t.Fatalf("sessions count = %d, want 3", got)
	}
	if got := countRows(t, reader.db, "messages"); got != 2 {
		t.Fatalf("messages count = %d, want 2", got)
	}
	if got := countRows(t, reader.db, "compression_locks"); got != 0 {
		t.Fatalf("compression_locks count = %d, want 0", got)
	}

	children, err := reader.findChildSessions("fixture-parent-001")
	if err != nil {
		t.Fatalf("findChildSessions() error = %v", err)
	}
	if len(children) != 1 || children[0].ID != "fixture-child-001" {
		t.Fatalf("child sessions = %+v, want fixture-child-001", children)
	}

	msgs, err := reader.listMessages("fixture-parent-001")
	if err != nil {
		t.Fatalf("listMessages() error = %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages len = %d, want 2", len(msgs))
	}
	if msgs[0].ToolName.String != "delegate_task" {
		t.Fatalf("tool name = %q, want delegate_task", msgs[0].ToolName.String)
	}
	if !strings.Contains(msgs[0].ToolCalls.String, "fixture-child-001") {
		t.Fatalf("tool_calls = %q, want child session reference", msgs[0].ToolCalls.String)
	}
	if got := msgs[1].Content.String; got != "\x00\x01{\"text\":\"hello from sentinel\"}" {
		t.Fatalf("sentinel content = %q, want prefix sentinel", got)
	}

	cron, err := reader.readSession("cron_job123_1700000000")
	if err != nil {
		t.Fatalf("readSession(cron) error = %v", err)
	}
	if cron.Source != "cron" {
		t.Fatalf("cron source = %q, want cron", cron.Source)
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("counting %s rows: %v", table, err)
	}
	return count
}
