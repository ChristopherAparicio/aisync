# F3 QA Report — ai5-blame-file-attribution

Date: 2026-06-15
Binary: bin/aisync (built from HEAD)
DB: /tmp/aisync-qa-kOaJOa.db

---

## Setup

make build: PASS
DB seeded: PASS

Note: The env var to override the DB path is `AISYNC_DATABASE_PATH`, not `AISYNC_DB`.
`GetDatabasePath()` in internal/config/config.go reads `AISYNC_DATABASE_PATH`.

---

## Scenario Results

### Scenario 1 — single-file AGENT column: PASS

Command: `AISYNC_DATABASE_PATH=$TMPDB bin/aisync blame internal/a.go`

Output (f3-qa-s1.txt):
- "AGENT" header present in table
- AGENT column shows "-" for ses-empty-1 (most recent session for a.go; has empty agent)

### Scenario 2 — multi-file both covered: PASS

Command: `AISYNC_DATABASE_PATH=$TMPDB bin/aisync blame internal/a.go internal/b.go`

Output (f3-qa-s2.txt):
- "jarvis" present (ses-jarvis-1, a.go)
- "hermes" present (ses-hermes-1, b.go)
- Both files covered in output

Observation: Multi-file mode without --all returns ALL sessions for the queried files
(3 rows shown). Single-file mode without --all returns only the most recent (1 row).
Behavioral asymmetry — not necessarily a bug, but worth noting.

### Scenario 3 — --json agent field: PASS

Command: `AISYNC_DATABASE_PATH=$TMPDB bin/aisync blame --json internal/a.go`

Output (f3-qa-s3.txt):
- JSON array contains `"agent"` key
- Value is `""` (empty string) for ses-empty-1; raw value preserved in JSON (not coerced to "-")
- This is correct: "-" placeholder is a rendering concern for table mode only

### Scenario 4 — --quiet IDs only: PASS

Command: `AISYNC_DATABASE_PATH=$TMPDB bin/aisync blame --quiet internal/a.go`

Output (f3-qa-s4.txt):
- Only "ses-empty-1" printed (bare ID)
- No "AGENT" header, no "jarvis"

### Scenario 5 — empty agent renders as "-": PASS

Command: `AISYNC_DATABASE_PATH=$TMPDB bin/aisync blame --all internal/a.go`

Output (f3-qa-s5.txt):
- 2 rows shown (ses-empty-1 and ses-jarvis-1)
- ses-empty-1 AGENT column: "-" (empty agent string rendered as "-")
- ses-jarvis-1 AGENT column: "jarvis"

### Scenario 6 — no args error: PASS

Command: `AISYNC_DATABASE_PATH=$TMPDB bin/aisync blame`

Output (f3-qa-s6.txt):
- `Error: requires at least one file argument or --project flag`
- exit:1

### Scenario 7 — nonexistent file safe: PASS

Command: `AISYNC_DATABASE_PATH=$TMPDB bin/aisync blame nonexistent/file.go`

Output (f3-qa-s7.txt):
- `No AI sessions found for file "nonexistent/file.go"`
- exit:0 (no crash, no panic)

---

## --project scenario: SKIPPED

Reason: All seeded sessions have `project_path = ''`. A FilesForProject query against
an empty project_path string would not represent real usage. Skipped per the task spec
guidance ("skip if seeding is complex").

---

## Edge Cases

- Env var name mismatch: spec used `AISYNC_DB`; actual var is `AISYNC_DATABASE_PATH`
- JSON mode preserves raw `agent` value (`""`); table mode renders empty agent as `"-"`
- Multi-file mode without --all returns all sessions (not most-recent-per-file) — contrast
  with single-file mode which applies the "most recent only" filter

---

## Evidence Files

- f3-qa-s1.txt  Scenario 1 output
- f3-qa-s2.txt  Scenario 2 output
- f3-qa-s3.txt  Scenario 3 output
- f3-qa-s4.txt  Scenario 4 output
- f3-qa-s5.txt  Scenario 5 output
- f3-qa-s6.txt  Scenario 6 output
- f3-qa-s7.txt  Scenario 7 output

---

VERDICT: APPROVE
