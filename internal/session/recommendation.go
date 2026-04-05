package session

import "time"

// RecommendationStatus tracks the lifecycle of a recommendation.
type RecommendationStatus string

const (
	RecStatusActive    RecommendationStatus = "active"
	RecStatusDismissed RecommendationStatus = "dismissed"
	RecStatusSnoozed   RecommendationStatus = "snoozed"
	RecStatusExpired   RecommendationStatus = "expired"
)

// RecommendationSource indicates how a recommendation was generated.
type RecommendationSource string

const (
	RecSourceDeterministic RecommendationSource = "deterministic"
	RecSourceAnalysis      RecommendationSource = "analysis"
	RecSourceLLM           RecommendationSource = "llm"
)

// RecommendationRecord is a persisted recommendation with lifecycle tracking.
// The Fingerprint field enables idempotent upserts — if the same recommendation
// is regenerated, it updates the existing record instead of creating a duplicate.
type RecommendationRecord struct {
	ID           string               `json:"id"`
	ProjectPath  string               `json:"project_path"`
	Type         string               `json:"type"`
	Priority     string               `json:"priority"`
	Source       RecommendationSource `json:"source"`
	Icon         string               `json:"icon"`
	Title        string               `json:"title"`
	Message      string               `json:"message"`
	Impact       string               `json:"impact,omitempty"`
	Agent        string               `json:"agent,omitempty"`
	Skill        string               `json:"skill,omitempty"`
	Status       RecommendationStatus `json:"status"`
	Fingerprint  string               `json:"fingerprint"` // hash of type+project+agent+skill for dedup
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
	DismissedAt  *time.Time           `json:"dismissed_at,omitempty"`
	SnoozedUntil *time.Time           `json:"snoozed_until,omitempty"`
}

// RecommendationFilter controls which recommendations to fetch.
type RecommendationFilter struct {
	ProjectPath string
	Status      RecommendationStatus // empty = all
	Priority    string               // empty = all
	Source      RecommendationSource // empty = all
	Limit       int                  // 0 = no limit
}

// RecommendationStats holds aggregate counts by status.
type RecommendationStats struct {
	Active    int `json:"active"`
	Dismissed int `json:"dismissed"`
	Snoozed   int `json:"snoozed"`
	Total     int `json:"total"`
}

// EnrichedRecommendation holds the LLM-enriched fields for a recommendation.
// Only non-empty fields are applied when updating the original record.
type EnrichedRecommendation struct {
	// Title is an improved, more descriptive title (may be empty = keep original).
	Title string `json:"title,omitempty"`

	// Message is an enriched, actionable explanation with context.
	Message string `json:"message"`

	// Impact describes the concrete benefit of following this recommendation.
	Impact string `json:"impact,omitempty"`
}

// RecommendationFingerprint computes a dedup key for a recommendation.
// Same type + project + agent + skill = same recommendation.
func RecommendationFingerprint(recType, projectPath, agent, skill string) string {
	// Simple deterministic hash without importing crypto.
	raw := recType + "|" + projectPath + "|" + agent + "|" + skill
	// FNV-1a hash (inline, no import needed)
	var h uint64 = 14695981039346656037
	for i := 0; i < len(raw); i++ {
		h ^= uint64(raw[i])
		h *= 1099511628211
	}
	// Return hex string
	const hextable = "0123456789abcdef"
	buf := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		buf[i] = hextable[h&0xf]
		h >>= 4
	}
	return string(buf)
}
