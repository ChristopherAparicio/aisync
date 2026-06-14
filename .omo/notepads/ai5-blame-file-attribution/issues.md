# Issues — ai5-blame-file-attribution

## [2026-06-14] Known Gotchas

- `web/handlers.go` consumes `FilesForProject` — adding `LastAgent` is additive (non-breaking) but MUST verify `go build ./internal/web/...` after T3
- `cobra.MinimumNArgs(1)` replaces `ExactArgs(1)` BUT when `--project` is provided with 0 file args, need 0 minimum — use custom Args validator
- Agent tag MUST be exactly `\`json:"agent"\`` NOT `\`json:"agent,omitempty"\`` — omitempty would hide empty string
- Dynamic SQL IN(...): construct `strings.Repeat("?,", n)` trimming trailing comma — avoids SQL injection, no sprintf
