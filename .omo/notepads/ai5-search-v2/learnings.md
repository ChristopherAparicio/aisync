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
