package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func timeToNullFloat64(t *time.Time) sql.NullFloat64 {
	if t == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: float64(t.Unix()), Valid: true}
}

func (s *Store) UpsertCronJob(job *session.CronJob) error {
	if job == nil {
		return fmt.Errorf("UpsertCronJob: nil job")
	}

	raw, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("UpsertCronJob: marshal: %w", err)
	}

	repeatInt := 0
	if job.Repeat {
		repeatInt = 1
	}
	enabledInt := 0
	if job.Enabled {
		enabledInt = 1
	}

	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO cron_jobs (
			job_id, provider, name, prompt, schedule, schedule_display,
			repeat, enabled, state, model, next_run_at, last_run_at,
			last_status, last_error, origin, workdir, profile, raw_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		job.ID,
		job.Provider,
		job.Name,
		job.Prompt,
		job.Schedule,
		job.ScheduleDisplay,
		repeatInt,
		enabledInt,
		job.State,
		job.Model,
		timeToNullFloat64(job.NextRunAt),
		timeToNullFloat64(job.LastRunAt),
		job.LastStatus,
		job.LastError,
		job.Origin,
		job.Workdir,
		job.Profile,
		string(raw),
		float64(time.Now().UTC().Unix()),
	)
	if err != nil {
		return fmt.Errorf("upsert cron_job: %w", err)
	}
	return nil
}

func (s *Store) ListCronJobs(provider string) ([]session.CronJob, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if provider == "" {
		rows, err = s.db.Query(`SELECT raw_json FROM cron_jobs ORDER BY name`)
	} else {
		rows, err = s.db.Query(`SELECT raw_json FROM cron_jobs WHERE provider = ? ORDER BY name`, provider)
	}
	if err != nil {
		return nil, fmt.Errorf("list cron_jobs: %w", err)
	}
	defer rows.Close()

	var jobs []session.CronJob
	for rows.Next() {
		var rawJSON string
		if err := rows.Scan(&rawJSON); err != nil {
			return nil, fmt.Errorf("list cron_jobs: scan: %w", err)
		}
		var job session.CronJob
		if err := json.Unmarshal([]byte(rawJSON), &job); err != nil {
			return nil, fmt.Errorf("list cron_jobs: unmarshal: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list cron_jobs: rows: %w", err)
	}
	return jobs, nil
}

func (s *Store) GetCronJob(jobID string) (*session.CronJob, error) {
	var rawJSON string
	err := s.db.QueryRow(`SELECT raw_json FROM cron_jobs WHERE job_id = ?`, jobID).Scan(&rawJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cron_job: %w", err)
	}
	var job session.CronJob
	if err := json.Unmarshal([]byte(rawJSON), &job); err != nil {
		return nil, fmt.Errorf("get cron_job: unmarshal: %w", err)
	}
	return &job, nil
}
