# learnings.md — ai5-search-v2

## Key Architecture Facts (from investigation)
- `internal/search/document.go:13` — `DocumentFromSession(sess *session.Session, maxContentLen int) Document`
  - Signature MUST NOT change (called at session_index.go:54 and :79)
  - Currently concatenates: user text + assistant text + bash/edit/write tool inputs/outputs
- `internal/session/session.go:115-132` — `Message` struct; Role at :123
- `internal/search/fts5/engine.go:26-49` — FTS5 schema + bm25 weights — DO NOT CHANGE
- `internal/provider/opencode/dbreader.go` — loadAllPartsForSession / loadParts
- `internal/service/session_index.go:54,79` — callers of DocumentFromSession

## Compaction Heuristic (validated empirically)
- Part type='compaction' = marker message {type, auto} with NO text content (carried by user message)
- The compaction SUMMARY = text of the NEXT assistant message (text part)
- 11,214 markers in opencode.db; sample of 300: 98% found, 98% start with "## Goal", median ~10KB
- 2% orphan markers (no following assistant message) → must skip gracefully

## A/B Investigation Results
- 4958 sessions in ~/.aisync/sessions.db
- clean (user-only) = 15% of noisy size, 6.6x signal density (5944 vs 39502 chars/session)
- "authentication" query: noisy ranks `house` #1 (inflated by tool echos x5.4); clean removes it from top-10
- Tool term inflation ratios: house x5.4, trainercycle x4.8, aisync x3.6

## Provider Note
- This machine: 100% OpenCode sessions (0 Claude JSONL)
- Filter in DocumentFromSession must be provider-agnostic (uses Message.Role + IsCompactionSummary)
- Compaction extraction is OpenCode-specific (lives in dbreader.go only)
## T1 done
IsCompactionSummary added at session.go after Role. Tests: round-trip + back-compat PASS.

## T3 done — eval harness architecture

### File: internal/search/eval_test.go (package search_test)

**precisionAtK formula**: `len(results[:k] ∩ expected) / len(expected)`
- Denominator is `len(expected)`, NOT k — this is recall@k semantics under the "precision@k" label
- Returns 0.0 for empty expected (no divide-by-zero)
- k is clamped to len(results) if k > len(results)

**DocumentBuilder type**: `func(sess *session.Session, maxLen int) search.Document`
- Defined in eval_test.go (package search_test scope only)
- noisyBuilder: wraps search.DocumentFromSession directly
- stubCleanBuilder: mirrors noisyBuilder; T6 replaces with real filter

**evalHarness struct**:
- Holds *fts5.Engine (in-memory SQLite per test via sql.Open("sqlite", ":memory:"))
- Constructed via newEvalHarness(t, noisy, clean DocumentBuilder)
- indexCorpus(ctx, corpus, builder) — iterates sessions, calls builder, calls engine.Index
- scoreQuery(ctx, evalQuery, k) — calls engine.Search, extracts session IDs, calls precisionAtK

**Fixture format** (testdata/eval_queries.json, created by T2):
```json
[{"query": "...", "expected_ids": ["ses_001", "ses_002"]}]
```
- TestEvalHarness_LoadFixture skips gracefully if file absent (os.IsNotExist guard)
- Validates: non-empty queries array, no empty query fields, no empty expected_ids arrays

**How T6/T7 plug in the real clean builder**:
1. T6 implements a real clean DocumentBuilder (e.g. `CleanDocumentFromSession`) in the search package
2. T7 changes the `stubCleanBuilder` reference in eval_test.go to point to T6's function
3. T7 also adds a corpus from a tmp DB copy and asserts `cleanScore >= noisyScore` on domain queries
- The harness is already wired for injection — no structural changes needed, just swap the function reference

## T2 done — eval query fixture

### File: internal/search/testdata/eval_queries.json

**Fixture**: 9 eval queries across 4 categories (domain×3, project×3, path×2, command×1), 12 unique session IDs.

**Baseline ID derivation method**: LIKE search on `sessions_fts_content.c2` (raw FTS content column). NOT derived from BM25/FTS5 MATCH ranking — avoids circular validation.

**DB copy used**: `cp ~/.aisync/sessions.db /tmp/aisync-eval-t2.db` (never touched the original).

**Category breakdown**:
- domain "authentication": ses_2c0c519e2ffeKr4iKBBb6dCKjq, ses_2bcbebecdffeWlT6BVTcyNhIcZ — user messages discuss DRF auth patterns (omogen ADR 008 review)
- domain "database migration": ses_2d5a53501ffenSM97z7X3LSFeS — user asks about SQLAlchemy+Alembic in TrainerCycle audit
- domain "docker": ses_2e411eb5effeIvDidbEqfvBJ0n, ses_2cfc478ecffeysKM5exqCxJmhV — user messages reference Docker explicitly
- project "omogen": ses_2bcec1f4dffe9gbmDC2SPkSSBZ, ses_2bcbebecdffeWlT6BVTcyNhIcZ — project_path contains omogen/backend
- project "cycloplan": ses_2bd79e29bffeJEtqFwNl74n2NQ, ses_2c0aa01d9ffexNoceyBQMlFh15 — project_path contains cycloplan
- project "aisync": ses_2d10f533effeDi56cuLr1LvrnB, ses_2d1d536a5ffe4fGzP3c41PmCEb — project_path contains aisync
- path "internal/storage/store.go": ses_2d10f533effeDi56cuLr1LvrnB — path in user's own message (not tool output)
- path "apps/authent": ses_2bcbebecdffeWlT6BVTcyNhIcZ — path in orchestrator/user message
- command "git rebase": ses_2d9e6b50affeADSfj1QVkViMZk, ses_2db719795ffecFr9IrTpmTauRO — user says "tu peux rebase depuis main"

**Key insight on path/command queries**: User messages sometimes contain explicit file paths and commands BEFORE any tool invocation. These sessions verify that search still finds user-requested paths/commands after tool-output content is filtered from FTS (the A3 regression risk). If FTS drops tool outputs but keeps user turns, these IDs should still rank high.

**All 12 IDs validated** via sqlite3: `SELECT COUNT(*) FROM sessions WHERE id='...'` — all return 1.
