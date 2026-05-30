package session

import "time"

// CronJob represents a Hermes scheduled job.
type CronJob struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Prompt            string     `json:"prompt"`
	Skills            []string   `json:"skills,omitempty"`
	Model             string     `json:"model,omitempty"`
	Provider          string     `json:"provider,omitempty"`
	BaseURL           string     `json:"base_url,omitempty"`
	Schedule          string     `json:"schedule,omitempty"`
	ScheduleDisplay   string     `json:"schedule_display,omitempty"`
	Repeat            bool       `json:"repeat"`
	Enabled           bool       `json:"enabled"`
	State             string     `json:"state,omitempty"`
	PausedAt          *time.Time `json:"paused_at,omitempty"`
	PausedReason      string     `json:"paused_reason,omitempty"`
	CreatedAt         *time.Time `json:"created_at,omitempty"`
	NextRunAt         *time.Time `json:"next_run_at,omitempty"`
	LastRunAt         *time.Time `json:"last_run_at,omitempty"`
	LastStatus        string     `json:"last_status,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	LastDeliveryError string     `json:"last_delivery_error,omitempty"`
	Origin            string     `json:"origin,omitempty"`
	Workdir           string     `json:"workdir,omitempty"`
	Profile           string     `json:"profile,omitempty"`
}

type CronJobsFile struct {
	Jobs      []CronJob  `json:"jobs"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}
