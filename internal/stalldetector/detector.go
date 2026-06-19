// Package stalldetector scans the live OpenCode SQLite database for
// "stuck" sessions: tool invocations whose state remains `running` past
// a configurable idle threshold (default 15 minutes), and historical
// messages whose API call was aborted, rate-limited, or rejected with a
// provider error.
//
// The detector is read-only with respect to OpenCode (uses WAL +
// query_only pragmas, never writes). Persistence of detected stalls is
// the caller's responsibility (typically StallDetectorTask in
// internal/scheduler).
//
// Two scanning passes per Detect():
//
//  1. Live stalls — tool parts where state.status = 'running' and
//     state.time.start is older than now - threshold.
//
//  2. Errored messages — messages whose error.name is set (within the
//     lookback window), classified into root causes via classifyError.
package stalldetector

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/session"

	_ "modernc.org/sqlite" // SQLite driver registration
)

// DefaultThreshold is the idle duration after which a `running` tool is
// considered stalled. Chosen to match the user-confirmed product
// requirement (see plan).
const DefaultThreshold = 15 * time.Minute

// DefaultLookback caps how far back the errored-message scan looks.
// Stalls older than this are NOT re-detected. Existing rows in
// session_stalls are unaffected (the table is the source of truth for
// history).
const DefaultLookback = 24 * time.Hour

// Config configures the stall detector.
type Config struct {
	// OpenCodeDBPath is the absolute path to opencode.db.
	OpenCodeDBPath string
	// Threshold is the idle duration after which a `running` tool is
	// considered stalled. Defaults to DefaultThreshold.
	Threshold time.Duration
	// Lookback is the time window for scanning historical errored
	// messages. Defaults to DefaultLookback.
	Lookback time.Duration
	// Pricing is the catalog used to compute cost_lost_usd. May be nil
	// — when nil, CostLostUSD is left at 0 and the detector falls back
	// to the message's own `cost` field when present.
	Pricing pricing.Catalog
	// Logger for non-fatal warnings. Defaults to log.Default().
	Logger *log.Logger
	// Now is the clock for tests. Defaults to time.Now.
	Now func() time.Time
}

// Detector scans the OpenCode SQLite database for stuck sessions.
//
// A Detector is stateless: it opens the database read-only on every
// Detect() call. Safe for concurrent use, though there is no benefit to
// running multiple passes in parallel against the same database.
type Detector struct {
	dbPath    string
	threshold time.Duration
	lookback  time.Duration
	pricing   pricing.Catalog
	logger    *log.Logger
	now       func() time.Time
}

// Result captures the outcome of a single Detect() pass.
type Result struct {
	// Stalls are stalls discovered during this pass (both currently
	// live and historical errored). Each entry is ready to be persisted
	// via storage.SessionStallStore.UpsertStall. Live stalls have
	// EndedAt == nil; errored-message stalls have EndedAt set.
	Stalls []session.SessionStall

	// LiveKeys identifies stalls that are STILL live in OpenCode. The
	// caller uses this set to seal previously-live stalls in aisync's
	// session_stalls table that no longer appear here (the tool either
	// completed, errored out, or was cleared).
	//
	// Keys are formatted as "provider_session_id|started_at_ms" to
	// match the storage uniqueness on (provider_session_id, started_at,
	// root_cause); all live stalls share root_cause = stream_stall so
	// it is omitted from the key.
	LiveKeys map[string]struct{}
}

// New constructs a Detector. Required: cfg.OpenCodeDBPath. All other
// fields fall back to documented defaults.
func New(cfg Config) *Detector {
	if cfg.Threshold <= 0 {
		cfg.Threshold = DefaultThreshold
	}
	if cfg.Lookback <= 0 {
		cfg.Lookback = DefaultLookback
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Detector{
		dbPath:    cfg.OpenCodeDBPath,
		threshold: cfg.Threshold,
		lookback:  cfg.Lookback,
		pricing:   cfg.Pricing,
		logger:    cfg.Logger,
		now:       cfg.Now,
	}
}

// LiveKey builds the canonical key used in Result.LiveKeys. Exported so
// callers (StallDetectorTask) can match against persisted live rows.
func LiveKey(providerSessionID string, startedAt time.Time) string {
	return fmt.Sprintf("%s|%d", providerSessionID, startedAt.UnixMilli())
}

// Detect opens the OpenCode SQLite database read-only and runs both
// scanning passes. Returns a Result the caller can hand to the storage
// layer.
func (d *Detector) Detect(ctx context.Context) (*Result, error) {
	if d.dbPath == "" {
		return nil, fmt.Errorf("stalldetector: empty OpenCodeDBPath")
	}
	if _, err := os.Stat(d.dbPath); err != nil {
		return nil, fmt.Errorf("stalldetector: opencode.db not found at %s: %w", d.dbPath, err)
	}

	// modernc.org/sqlite DSN — same shape as internal/provider/opencode/dbreader.go.
	dsn := d.dbPath + "?_pragma=journal_mode(WAL)&_pragma=query_only(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("stalldetector: open opencode.db: %w", err)
	}
	defer func() { _ = db.Close() }()

	now := d.now().UTC()
	result := &Result{
		LiveKeys: make(map[string]struct{}),
	}

	cutoffMs := now.Add(-d.threshold).UnixMilli()
	live, err := d.scanLiveStalls(ctx, db, cutoffMs, now)
	if err != nil {
		return nil, fmt.Errorf("stalldetector: scan live stalls: %w", err)
	}
	for _, s := range live {
		result.Stalls = append(result.Stalls, s)
		result.LiveKeys[LiveKey(s.ProviderSessionID, s.StartedAt)] = struct{}{}
	}

	sinceMs := now.Add(-d.lookback).UnixMilli()
	errored, err := d.scanErroredMessages(ctx, db, sinceMs, now)
	if err != nil {
		return nil, fmt.Errorf("stalldetector: scan errored messages: %w", err)
	}
	result.Stalls = append(result.Stalls, errored...)

	return result, nil
}

// scanLiveStalls finds tool parts that are still `running` and whose
// state.time.start is older than cutoffMs. Each row is joined with its
// owning message + session for provider/model/agent/parent enrichment.
func (d *Detector) scanLiveStalls(ctx context.Context, db *sql.DB, cutoffMs int64, now time.Time) ([]session.SessionStall, error) {
	const q = `
		SELECT
			p.session_id,
			COALESCE(json_extract(p.data, '$.tool'), '')         AS tool_name,
			CAST(json_extract(p.data, '$.state.time.start') AS INTEGER) AS started_ms,
			COALESCE(json_extract(m.data, '$.providerID'), '')   AS provider,
			COALESCE(json_extract(m.data, '$.modelID'),    '')   AS model,
			COALESCE(json_extract(m.data, '$.agent'),      '')   AS agent,
			COALESCE(s.parent_id, '')                            AS parent_id
		FROM part p
		JOIN message m ON m.id = p.message_id
		JOIN session s ON s.id = p.session_id
		WHERE json_extract(p.data, '$.type')         = 'tool'
		  AND json_extract(p.data, '$.state.status') = 'running'
		  AND CAST(json_extract(p.data, '$.state.time.start') AS INTEGER) > 0
		  AND CAST(json_extract(p.data, '$.state.time.start') AS INTEGER) < ?
	`
	rows, err := db.QueryContext(ctx, q, cutoffMs)
	if err != nil {
		return nil, fmt.Errorf("query live stalls: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []session.SessionStall
	for rows.Next() {
		var (
			providerSessionID string
			toolName          string
			startedMs         int64
			provider          string
			model             string
			agent             string
			parentID          string
		)
		if err := rows.Scan(&providerSessionID, &toolName, &startedMs, &provider, &model, &agent, &parentID); err != nil {
			d.logger.Printf("stalldetector: scan live row: %v", err)
			continue
		}
		started := time.UnixMilli(startedMs).UTC()
		out = append(out, session.SessionStall{
			SessionID:         session.ID(providerSessionID),
			ProviderSessionID: providerSessionID,
			DetectedAt:        now,
			StartedAt:         started,
			DurationMs:        now.Sub(started).Milliseconds(),
			RootCause:         session.StallRootCauseStreamStall,
			Provider:          provider,
			Model:             model,
			Agent:             agent,
			ParentSessionID:   parentID,
			ToolName:          toolName,
			// Live stalls have no recorded tokens/cost (the tool never
			// completed) — leave both at 0 and refine via Phase 2.
		})
	}
	return out, rows.Err()
}

// scanErroredMessages finds messages whose error.name is non-NULL and
// time.created >= sinceMs. Each row is classified into a stall root
// cause (aborted / rate_limit_429 / provider_error) and enriched with
// the message's tokens + cost.
func (d *Detector) scanErroredMessages(ctx context.Context, db *sql.DB, sinceMs int64, now time.Time) ([]session.SessionStall, error) {
	const q = `
		SELECT
			m.session_id,
			COALESCE(json_extract(m.data, '$.error.name'), '')              AS err_name,
			COALESCE(json_extract(m.data, '$.error.data.statusCode'), 0)    AS err_status,
			COALESCE(json_extract(m.data, '$.error.data.message'), '')      AS err_message,
			COALESCE(json_extract(m.data, '$.providerID'), '')              AS provider,
			COALESCE(json_extract(m.data, '$.modelID'),    '')              AS model,
			COALESCE(json_extract(m.data, '$.agent'),      '')              AS agent,
			COALESCE(json_extract(m.data, '$.cost'),       0.0)             AS msg_cost,
			COALESCE(json_extract(m.data, '$.tokens.input'),       0)       AS tok_input,
			COALESCE(json_extract(m.data, '$.tokens.output'),      0)       AS tok_output,
			COALESCE(json_extract(m.data, '$.tokens.cache.read'),  0)       AS tok_cache_read,
			COALESCE(json_extract(m.data, '$.tokens.cache.write'), 0)       AS tok_cache_write,
			COALESCE(json_extract(m.data, '$.tokens.reasoning'),   0)       AS tok_reasoning,
			COALESCE(json_extract(m.data, '$.time.created'),   0)           AS created_ms,
			COALESCE(json_extract(m.data, '$.time.completed'), 0)           AS completed_ms,
			COALESCE(s.parent_id, '')                                       AS parent_id
		FROM message m
		JOIN session s ON s.id = m.session_id
		WHERE json_extract(m.data, '$.error.name') IS NOT NULL
		  AND COALESCE(json_extract(m.data, '$.time.created'), 0) >= ?
	`
	rows, err := db.QueryContext(ctx, q, sinceMs)
	if err != nil {
		return nil, fmt.Errorf("query errored messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []session.SessionStall
	for rows.Next() {
		var (
			providerSessionID string
			errName           string
			errStatus         int
			errMessage        string
			provider          string
			model             string
			agent             string
			msgCost           float64
			tokInput          int64
			tokOutput         int64
			tokCacheRead      int64
			tokCacheWrite     int64
			tokReasoning      int64
			createdMs         int64
			completedMs       int64
			parentID          string
		)
		if err := rows.Scan(
			&providerSessionID, &errName, &errStatus, &errMessage,
			&provider, &model, &agent,
			&msgCost,
			&tokInput, &tokOutput, &tokCacheRead, &tokCacheWrite, &tokReasoning,
			&createdMs, &completedMs,
			&parentID,
		); err != nil {
			d.logger.Printf("stalldetector: scan errored row: %v", err)
			continue
		}

		rootCause := classifyError(errName, errStatus)
		started := time.UnixMilli(createdMs).UTC()

		var endedAt *time.Time
		var durationMs int64
		if completedMs > 0 {
			t := time.UnixMilli(completedMs).UTC()
			endedAt = &t
			durationMs = completedMs - createdMs
		}

		tokensLost := tokInput + tokOutput + tokCacheRead + tokCacheWrite + tokReasoning
		cost := msgCost
		if cost == 0 && d.pricing != nil {
			cost = estimateCost(d.pricing, model, tokInput, tokOutput, tokCacheRead, tokCacheWrite)
		}

		out = append(out, session.SessionStall{
			SessionID:         session.ID(providerSessionID),
			ProviderSessionID: providerSessionID,
			DetectedAt:        now,
			StartedAt:         started,
			EndedAt:           endedAt,
			DurationMs:        durationMs,
			RootCause:         rootCause,
			Provider:          provider,
			Model:             model,
			Agent:             agent,
			ParentSessionID:   parentID,
			TokensLost:        tokensLost,
			CostLostUSD:       cost,
			ErrorMessage:      errMessage,
		})
	}
	return out, rows.Err()
}

// classifyError maps an OpenCode error.name (+ optional HTTP status)
// into one of the four StallRootCause constants. Unknown names fall
// back to provider_error.
func classifyError(name string, statusCode int) session.StallRootCause {
	switch name {
	case "MessageAbortedError":
		return session.StallRootCauseAborted
	case "APIError":
		if statusCode == 429 {
			return session.StallRootCauseRateLimit429
		}
		return session.StallRootCauseProviderError
	default:
		// ProviderAuthError, ContextOverflowError, UnknownError, ...
		return session.StallRootCauseProviderError
	}
}
