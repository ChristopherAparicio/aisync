package session

import "time"

type StallRootCause string

const (
	StallRootCauseStreamStall    StallRootCause = "stream_stall"
	StallRootCauseAborted        StallRootCause = "aborted"
	StallRootCauseRateLimit429   StallRootCause = "rate_limit_429"
	StallRootCauseProviderError  StallRootCause = "provider_error"
)

type SessionStall struct {
	ID                int64
	SessionID         ID
	ProviderSessionID string
	DetectedAt        time.Time
	StartedAt         time.Time
	EndedAt           *time.Time
	DurationMs        int64
	RootCause         StallRootCause
	Provider          string
	Model             string
	Agent             string
	ParentSessionID   string
	ToolName          string
	TokensLost        int64
	CostLostUSD       float64
	ErrorMessage      string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (s SessionStall) IsLive() bool {
	return s.EndedAt == nil
}

type StallFilter struct {
	Since        time.Time
	Until        time.Time
	RootCauses   []StallRootCause
	Providers    []string
	OnlyLive     bool
	Limit        int
}

type StallStats struct {
	TotalCount     int
	LiveCount      int
	TokensLost     int64
	CostLostUSD    float64
	TotalDurationMs int64
	ByRootCause    map[StallRootCause]StallStatsRow
	ByProvider     map[string]StallStatsRow
}

type StallStatsRow struct {
	Count       int
	TokensLost  int64
	CostLostUSD float64
}
