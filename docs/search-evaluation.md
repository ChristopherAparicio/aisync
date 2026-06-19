# Search Evaluation

aisync uses SQLite FTS5 plus an in-process two-stage re-ranker for local
keyword/full-text search over captured sessions. This evaluation suite tracks
whether real user retrieval questions can find the expected OpenCode/AI sessions.

## Why this exists

Search quality regressions are easy to miss because the dataset is personal and
messy: project renames, huge sessions, compaction summaries, handoff blocks,
subagents, and meta-evaluation sessions all affect ranking. A versioned query
fixture makes those failures measurable before adding semantic/vector search.

## Files

- `eval/search/queries.json` — labeled retrieval questions and expected sessions.
- `scripts/search_eval.py` — runner that shells out to the production binary.

## Run

The runner invokes `aisync list --global --search <query> --json` so it measures
exactly what production serves (FTS5 bm25 plus the in-process re-ranker), with no
risk of drift between a duplicated Python scorer and the Go engine. Install the
binary first with `make install`, then:

```bash
python3 scripts/search_eval.py \
  --binary ~/go/bin/aisync \
  --queries eval/search/queries.json
```

`--binary` defaults to `aisync` on `PATH`. Write JSON or CSV for tracking:

```bash
python3 scripts/search_eval.py --format json --output eval/search/results/latest.json
python3 scripts/search_eval.py --format csv --output eval/search/results/latest.csv
```

## Metrics

- `P@1`, `P@3`, `P@5`, `P@10`: fraction of labeled queries where at least one
  expected session appears within the top K results.
- `MRR`: mean reciprocal rank of the first expected session.

Queries without `expected_session_ids` are allowed for exploration but are not
included in aggregate metrics.

## Two-stage re-ranker

`Search()` fetches a bm25-ordered candidate pool, then re-ranks it in process.
Base score is `-bm25` (higher is better); boosts are applied as booleans rather
than tuned scalars to avoid overfitting:

- path match: all query tokens present in `project_path` (reconnects project
  renames such as `opencode-agent-swarm`).
- summary exact substring and summary all-tokens-present (rescues long sessions
  whose content match is washed out by length normalization).
- mild recency decay.

## Baseline

Measured against the personal production DB (`~/.aisync/sessions.db`, 12 labeled
queries) via the shell-out harness:

| Metric | FTS5 only | FTS5 + re-ranker |
|---|---|---|
| P@1  | 0.500 | 0.583 |
| P@3  | 0.583 | 0.750 |
| P@5  | 0.667 | 0.833 |
| P@10 | 0.750 | 0.917 |
| MRR  | 0.576 | 0.697 |

Every metric improved with no per-query regressions; gains spread across roughly
eight queries rather than one, so the booster is not overfit. Notable target
movements: `opencode agent swarm` from rank 13 to 2, `Hermes Agent Coding` from
rank 14 to 6.

### Out of scope for this iteration

- `caveman`: the target word never appears within the 50k-character indexed
  content cap, so FTS5 cannot match it. Fixing this needs typed metadata or a
  semantic sidecar, not re-ranking.
- `ses_18a710ffd` (Hermes / `/dev/house`, ~352M tokens): length normalization
  still suppresses its content match; the summary boost lifts it but it will not
  reach rank 1 without dedicated long-session handling.

## How to use results

When a query fails, classify it as one of:

- `project-alias`: old/new project names or path renames are not connected.
- `long-session`: the target session is too large or poorly represented by the
  current 50k-character indexed content cap.
- `meta-pollution`: newer benchmark or discussion sessions mention the query but
  are not the real target.
- `semantic-gap`: exact keywords are absent but a human would expect a match.
- `event-gap`: compaction/handoff/file-change data should be indexed as a typed
  document rather than buried in session content.

Those labels decide whether to improve FTS5 metadata, add typed search documents,
or prototype a semantic sidecar such as TurboVec.
