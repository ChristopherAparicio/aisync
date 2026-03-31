package sessionevent

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// BucketAggregator computes EventBucket aggregations from raw events.
// It groups events by time window (hourly or daily) and project/provider.
type BucketAggregator struct{}

// NewBucketAggregator creates a new bucket aggregator.
func NewBucketAggregator() *BucketAggregator {
	return &BucketAggregator{}
}

// bucketKey identifies a unique bucket for grouping.
type bucketKey struct {
	Start       time.Time
	ProjectPath string
	RemoteURL   string
	Provider    session.ProviderName
}

// Aggregate groups events into hourly or daily buckets.
// granularity must be "1h" or "1d".
func (a *BucketAggregator) Aggregate(events []Event, granularity string) []EventBucket {
	if len(events) == 0 {
		return nil
	}

	buckets := make(map[bucketKey]*EventBucket)

	for _, e := range events {
		start := truncateTime(e.OccurredAt, granularity)
		end := advanceTime(start, granularity)

		key := bucketKey{
			Start:       start,
			ProjectPath: e.ProjectPath,
			RemoteURL:   e.RemoteURL,
			Provider:    e.Provider,
		}

		b, ok := buckets[key]
		if !ok {
			b = &EventBucket{
				BucketStart:     start,
				BucketEnd:       end,
				Granularity:     granularity,
				ProjectPath:     e.ProjectPath,
				RemoteURL:       e.RemoteURL,
				Provider:        e.Provider,
				TopTools:        make(map[string]int),
				TopMCPServers:   make(map[string]int),
				TopSkills:       make(map[string]int),
				SkillTokens:     make(map[string]int),
				AgentBreakdown:  make(map[string]int),
				TopCommands:     make(map[string]int),
				ErrorByCategory: make(map[session.ErrorCategory]int),
			}
			buckets[key] = b
		}

		a.addEvent(b, &e)
	}

	// Compute unique counts and flatten.
	result := make([]EventBucket, 0, len(buckets))
	for _, b := range buckets {
		b.UniqueTools = len(b.TopTools)
		b.UniqueSkills = len(b.TopSkills)
		result = append(result, *b)
	}

	return result
}

// AggregateForSession aggregates events from a single session into buckets.
// It also tracks the session in SessionCount.
func (a *BucketAggregator) AggregateForSession(sess *session.Session, events []Event, granularity string) []EventBucket {
	buckets := a.Aggregate(events, granularity)

	// Mark session count — deduplicate by tracking which buckets were touched.
	sessionCounted := make(map[int]bool) // bucket index → already counted
	for i := range buckets {
		if !sessionCounted[i] {
			buckets[i].SessionCount = 1
			sessionCounted[i] = true
		}
	}

	return buckets
}

// addEvent adds a single event's data to the bucket counters.
func (a *BucketAggregator) addEvent(b *EventBucket, e *Event) {
	switch e.Type {
	case EventToolCall:
		b.ToolCallCount++
		if e.ToolCall != nil {
			b.TopTools[e.ToolCall.ToolName]++
			if e.ToolCall.State == session.ToolStateError {
				b.ToolErrorCount++
			}
			// Aggregate MCP server calls from classified tool category.
			if server := session.MCPServerName(e.ToolCall.ToolCategory); server != "" {
				b.TopMCPServers[server]++
			}
		}

	case EventSkillLoad:
		b.SkillLoadCount++
		if e.SkillLoad != nil {
			b.TopSkills[e.SkillLoad.SkillName]++
			if e.SkillLoad.EstimatedTokens > 0 {
				if b.SkillTokens == nil {
					b.SkillTokens = make(map[string]int)
				}
				b.SkillTokens[e.SkillLoad.SkillName] += e.SkillLoad.EstimatedTokens
			}
		}

	case EventAgentDetection:
		if e.AgentInfo != nil && e.AgentInfo.Agent != "" {
			b.AgentBreakdown[e.AgentInfo.Agent]++
		}

	case EventCommand:
		b.CommandCount++
		if e.Command != nil {
			b.TopCommands[e.Command.BaseCommand]++
			if e.Command.State == session.ToolStateError {
				b.CommandErrorCount++
			}
		}

	case EventError:
		b.ErrorCount++
		if e.Error != nil {
			b.ErrorByCategory[e.Error.Category]++
		}

	case EventImageUsage:
		b.ImageCount++
		if e.Image != nil {
			b.ImageTokens += e.Image.TokensEstimate
		}

	case EventCompaction:
		b.CompactionCount++
	}
}

// MergeBuckets merges a new bucket into an existing one (for upsert operations).
// The existing bucket is modified in place.
func MergeBuckets(existing, incoming *EventBucket) {
	existing.ToolCallCount += incoming.ToolCallCount
	existing.ToolErrorCount += incoming.ToolErrorCount
	existing.SkillLoadCount += incoming.SkillLoadCount
	existing.SessionCount += incoming.SessionCount
	existing.CommandCount += incoming.CommandCount
	existing.CommandErrorCount += incoming.CommandErrorCount
	existing.ErrorCount += incoming.ErrorCount
	existing.ImageCount += incoming.ImageCount
	existing.ImageTokens += incoming.ImageTokens
	existing.CompactionCount += incoming.CompactionCount

	// Merge maps.
	mergeMaps(existing.TopTools, incoming.TopTools)
	mergeMaps(existing.TopMCPServers, incoming.TopMCPServers)
	mergeMaps(existing.TopSkills, incoming.TopSkills)
	if len(incoming.SkillTokens) > 0 {
		if existing.SkillTokens == nil {
			existing.SkillTokens = make(map[string]int)
		}
		mergeMaps(existing.SkillTokens, incoming.SkillTokens)
	}
	mergeMaps(existing.AgentBreakdown, incoming.AgentBreakdown)
	mergeMaps(existing.TopCommands, incoming.TopCommands)
	mergeErrorMaps(existing.ErrorByCategory, incoming.ErrorByCategory)

	// Recompute unique counts.
	existing.UniqueTools = len(existing.TopTools)
	existing.UniqueSkills = len(existing.TopSkills)
}

// ── Time helpers ──

// truncateTime floors a timestamp to the start of the given granularity window.
func truncateTime(t time.Time, granularity string) time.Time {
	t = t.UTC()
	switch granularity {
	case "1d":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	default: // "1h"
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
	}
}

// advanceTime moves a start time forward by one granularity unit.
func advanceTime(start time.Time, granularity string) time.Time {
	switch granularity {
	case "1d":
		return start.AddDate(0, 0, 1)
	default: // "1h"
		return start.Add(time.Hour)
	}
}

// ── Map helpers ──

func mergeMaps(dst, src map[string]int) {
	for k, v := range src {
		dst[k] += v
	}
}

func mergeErrorMaps(dst map[session.ErrorCategory]int, src map[session.ErrorCategory]int) {
	for k, v := range src {
		dst[k] += v
	}
}
