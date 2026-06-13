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
| noisy | 142.77 | 2m4.12s |
| clean | 46.24 | 41.532s |
| ratio | 32.4% | — |

## Zero-Regression Check (path + command)

- [path] `internal/storage/store.go`: clean P@10=0.00 — FAIL
- [path] `apps/authent`: clean P@10=1.00 — PASS
- [command] `git rebase`: clean P@10=0.00 — FAIL

## Summary

- domain+project avg noisy P@10: 0.083
- domain+project avg clean P@10: 0.167
- size: noisy=142.77 MB → clean=46.24 MB (32.4% of noisy)
