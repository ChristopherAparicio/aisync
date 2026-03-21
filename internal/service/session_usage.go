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

// ComputeTokenBuckets scans sessions and pre-computes token usage per time bucket.
//
// This is designed to run as a nightly scheduled task. It groups message tokens
// by hour (or day) and project/provider, then persists the aggregates.
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

	// Bucket key: bucket_start + project_path + provider.
	type bucketKey struct {
		start       time.Time
		projectPath string
		provider    string
	}
	buckets := make(map[bucketKey]*session.TokenUsageBucket)

	result := &ComputeTokenBucketsResult{
		SessionsScanned: len(summaries),
	}

	for _, sm := range summaries {
		// Load full session to access per-message timestamps and tokens.
		sess, getErr := s.store.Get(sm.ID)
		if getErr != nil {
			continue
		}

		for i := range sess.Messages {
			msg := &sess.Messages[i]
			if msg.Timestamp.IsZero() {
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

			key := bucketKey{
				start:       bucketStart,
				projectPath: sess.ProjectPath,
				provider:    string(sess.Provider),
			}

			b, ok := buckets[key]
			if !ok {
				b = &session.TokenUsageBucket{
					BucketStart: bucketStart,
					Granularity: gran,
					ProjectPath: sess.ProjectPath,
					Provider:    sess.Provider,
				}
				buckets[key] = b
			}

			b.InputTokens += msg.InputTokens
			b.OutputTokens += msg.OutputTokens
			b.MessageCount++
			result.MessagesScanned++

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
		}

		// Count sessions per bucket (use first message timestamp).
		if len(sess.Messages) > 0 && !sess.Messages[0].Timestamp.IsZero() {
			firstMsg := sess.Messages[0].Timestamp
			var bucketStart time.Time
			switch gran {
			case "1d":
				bucketStart = time.Date(firstMsg.Year(), firstMsg.Month(), firstMsg.Day(), 0, 0, 0, 0, firstMsg.Location())
			default:
				bucketStart = firstMsg.Truncate(time.Hour)
			}
			key := bucketKey{start: bucketStart, projectPath: sess.ProjectPath, provider: string(sess.Provider)}
			if b, ok := buckets[key]; ok {
				b.SessionCount++
			}
		}
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
