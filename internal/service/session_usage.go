package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ComputeTokenBucketsRequest configures the token usage computation.
type ComputeTokenBucketsRequest struct {
	Granularity string // "1h" or "1d"
	Incremental bool   // if true, only compute since last run
}

// ComputeTokenBucketsResult contains the computation result.
type ComputeTokenBucketsResult struct {
	BucketsWritten  int
	SessionsScanned int
	MessagesScanned int
	Duration        time.Duration
}

// buildForkPointMap loads all fork relations and builds a map of
// fork session ID → fork point (message index where the fork diverges).
// Messages before the fork point are shared with the original and should
// be skipped during token accounting to avoid double-counting.
func (s *SessionService) buildForkPointMap() map[session.ID]int {
	rels, err := s.store.ListAllForkRelations()
	if err != nil {
		log.Printf("[dedup] failed to load fork relations: %v", err)
		return nil
	}
	if len(rels) == 0 {
		return nil
	}

	m := make(map[session.ID]int, len(rels))
	for _, rel := range rels {
		// If a session appears in multiple fork relations (fork of a fork),
		// keep the maximum fork point (most messages to skip).
		if existing, ok := m[rel.ForkID]; !ok || rel.ForkPoint > existing {
			m[rel.ForkID] = rel.ForkPoint
		}
	}
	log.Printf("[dedup] loaded %d fork relations, %d sessions have dedup offsets", len(rels), len(m))
	return m
}

// ComputeTokenBuckets scans sessions and pre-computes token usage per time bucket.
//
// Buckets are keyed by (bucket_start, project_path, provider, llm_backend), so
// tokens from different LLM backends (e.g. "anthropic" vs "amazon-bedrock") are
// tracked separately. Each bucket also includes estimated and actual cost.
//
// Fork deduplication: for sessions that are forks (appear as fork_id in
// session_forks), messages before the fork_point are skipped to avoid
// double-counting the shared prefix with the original session.
//
// When Incremental=true, only processes sessions captured after the last compute.
func (s *SessionService) ComputeTokenBuckets(ctx context.Context, req ComputeTokenBucketsRequest) (*ComputeTokenBucketsResult, error) {
	start := time.Now()
	gran := req.Granularity
	if gran == "" {
		gran = "1h"
	}

	// Determine the time window for incremental mode.
	var since time.Time
	if req.Incremental {
		lastCompute, _ := s.store.GetLastBucketComputeTime(gran)
		if !lastCompute.IsZero() {
			since = lastCompute.Add(-1 * time.Hour) // overlap by 1h for safety
		}
	}

	// List all sessions (with time filter for incremental).
	listOpts := session.ListOptions{All: true}
	if !since.IsZero() {
		listOpts.Since = since
	}
	summaries, err := s.store.List(listOpts)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	// Load fork dedup map: fork session ID → first message index to count.
	forkPoints := s.buildForkPointMap()

	// Bucket key: bucket_start + project_path + provider + llm_backend.
	type bucketKey struct {
		start       time.Time
		projectPath string
		provider    string
		llmBackend  string
	}
	buckets := make(map[bucketKey]*session.TokenUsageBucket)

	// Track which sessions have been counted per bucket key (avoid double-counting).
	sessionCounted := make(map[bucketKey]map[session.ID]bool)

	result := &ComputeTokenBucketsResult{
		SessionsScanned: len(summaries),
	}

	var dedupSkipped int // messages skipped due to fork dedup

	for _, sm := range summaries {
		// Load full session to access per-message timestamps and tokens.
		sess, getErr := s.store.Get(sm.ID)
		if getErr != nil {
			continue
		}

		// Determine the fork dedup offset for this session.
		// Messages at index < forkOffset are shared with the original and skipped.
		forkOffset := 0
		if forkPoints != nil {
			if fp, isFork := forkPoints[sess.ID]; isFork {
				forkOffset = fp
			}
		}

		// Compute per-model cost for this session (needed for per-message cost estimation).
		var estimate *session.CostEstimate
		if s.pricing != nil {
			estimate = s.pricing.SessionCost(sess)
		}

		// Build a per-model cost-per-token map for distributing costs to individual messages.
		modelCostRate := make(map[string]float64) // model → cost per output token
		if estimate != nil {
			for _, mc := range estimate.PerModel {
				if mc.OutputTokens > 0 {
					modelCostRate[mc.Model] = mc.Cost.TotalCost / float64(mc.OutputTokens)
				} else if mc.InputTokens > 0 {
					modelCostRate[mc.Model] = mc.Cost.TotalCost / float64(mc.InputTokens)
				}
			}
		}

		for i := range sess.Messages {
			msg := &sess.Messages[i]
			if msg.Timestamp.IsZero() {
				continue
			}

			// Fork dedup: skip messages in the shared prefix.
			if i < forkOffset {
				dedupSkipped++
				continue
			}

			// Determine the bucket start for this message.
			var bucketStart time.Time
			switch gran {
			case "1d":
				bucketStart = time.Date(msg.Timestamp.Year(), msg.Timestamp.Month(), msg.Timestamp.Day(), 0, 0, 0, 0, msg.Timestamp.Location())
			default: // "1h"
				bucketStart = msg.Timestamp.Truncate(time.Hour)
			}

			// Use per-message ProviderID as the LLM backend key.
			// Falls back to empty string if not available.
			llmBackend := msg.ProviderID

			key := bucketKey{
				start:       bucketStart,
				projectPath: sess.ProjectPath,
				provider:    string(sess.Provider),
				llmBackend:  llmBackend,
			}

			b, ok := buckets[key]
			if !ok {
				b = &session.TokenUsageBucket{
					BucketStart: bucketStart,
					Granularity: gran,
					ProjectPath: sess.ProjectPath,
					Provider:    sess.Provider,
					LLMBackend:  llmBackend,
				}
				buckets[key] = b
			}

			b.InputTokens += msg.InputTokens
			b.OutputTokens += msg.OutputTokens
			b.MessageCount++
			result.MessagesScanned++

			// Accumulate actual cost from provider-reported data.
			if msg.ProviderCost > 0 {
				b.ActualCost += msg.ProviderCost
			}

			// Estimate API-equivalent cost for this message using the model's rate.
			if rate, hasRate := modelCostRate[msg.Model]; hasRate {
				msgTokens := msg.OutputTokens
				if msgTokens == 0 {
					msgTokens = msg.InputTokens
				}
				b.EstimatedCost += float64(msgTokens) * rate
			}

			// Count by role (human vs agent indicator).
			switch msg.Role {
			case session.RoleUser:
				b.UserMsgCount++
			case session.RoleAssistant:
				b.AssistMsgCount++
			}

			// Count tool calls and errors.
			for j := range msg.ToolCalls {
				b.ToolCallCount++
				if msg.ToolCalls[j].State == session.ToolStateError {
					b.ToolErrorCount++
				}
			}

			// Count images and image tokens.
			for _, img := range msg.Images {
				b.ImageTokens += img.TokensEstimate
				b.ImageCount++
			}

			// Count this session in the bucket (once per session per bucket key).
			if sessionCounted[key] == nil {
				sessionCounted[key] = make(map[session.ID]bool)
			}
			if !sessionCounted[key][sess.ID] {
				sessionCounted[key][sess.ID] = true
				b.SessionCount++
			}
		}
	}

	if dedupSkipped > 0 {
		log.Printf("[token_usage] fork dedup: skipped %d shared-prefix messages", dedupSkipped)
	}

	// Persist all buckets.
	for _, b := range buckets {
		if err := s.store.UpsertTokenBucket(*b); err != nil {
			log.Printf("[token_usage] error upserting bucket: %v", err)
		}
		result.BucketsWritten++
	}

	result.Duration = time.Since(start)
	return result, nil
}

// QueryTokenUsage retrieves pre-computed token buckets for display.
type QueryTokenUsageRequest struct {
	Granularity string    // "1h" or "1d"
	Since       time.Time // start of range
	Until       time.Time // end of range
	ProjectPath string    // filter by project (empty = all)
}

func (s *SessionService) QueryTokenUsage(ctx context.Context, req QueryTokenUsageRequest) ([]session.TokenUsageBucket, error) {
	gran := req.Granularity
	if gran == "" {
		gran = "1h"
	}
	return s.store.QueryTokenBuckets(gran, req.Since, req.Until, req.ProjectPath)
}
