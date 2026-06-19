# Task 7 A/B Reindex Eval Report

## Precision@10 by Query

| Query | Category | Noisy P@10 | Clean P@10 | Delta |
|-------|----------|-----------|-----------|-------|
| authentication | domain | 0.50 | 0.50 | +0.00 |
| database migration | domain | 0.00 | 0.00 | +0.00 |
| docker | domain | 0.00 | 0.00 | +0.00 |
| omogen | project | 0.00 | 0.00 | +0.00 |
| cycloplan | project | 0.00 | 0.00 | +0.00 |
| aisync | project | 0.00 | 0.50 | +0.50 |
| internal/storage/store.go | path | 0.00 | 0.00 | +0.00 |
| apps/authent | path | 0.00 | 1.00 | +1.00 |
| git rebase | command | 0.00 | 0.00 | +0.00 |

## Index Size Comparison

| Index | Size (MB) | Reindex Time |
|-------|----------|--------------|
| noisy | 143.11 | 2m34.047s |
| clean | 46.70 | 49.687s |
| ratio | 32.6% | — |

## Zero-Regression Check (path + command)

- [path] `internal/storage/store.go`: clean P@10=0.00 — FAIL
- [path] `apps/authent`: clean P@10=1.00 — PASS
- [command] `git rebase`: clean P@10=0.00 — FAIL

## Summary

- domain+project avg noisy P@10: 0.083
- domain+project avg clean P@10: 0.167
- size: noisy=143.11 MB → clean=46.70 MB (32.6% of noisy)
