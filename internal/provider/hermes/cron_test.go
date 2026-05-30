package hermes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func writeCronFile(t *testing.T, dir string, content []byte) {
	t.Helper()
	cronDir := filepath.Join(dir, "cron")
	if err := os.MkdirAll(cronDir, 0o755); err != nil {
		t.Fatalf("mkdir cron: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cronDir, "jobs.json"), content, 0o644); err != nil {
		t.Fatalf("write jobs.json: %v", err)
	}
}

func TestParseCronJobs_Normal(t *testing.T) {
	dir := t.TempDir()

	now := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	nextRun := now.Add(24 * time.Hour)

	jobs := []session.CronJob{
		{
			ID:              "job-001",
			Name:            "Daily summary",
			Prompt:          "Summarize the day",
			Schedule:        "0 9 * * *",
			ScheduleDisplay: "Every day at 9am",
			Repeat:          true,
			Enabled:         true,
			State:           "active",
			Model:           "claude-3-5-sonnet",
			Provider:        "anthropic",
			CreatedAt:       &now,
			NextRunAt:       &nextRun,
		},
		{
			ID:           "job-002",
			Name:         "Weekly report",
			Prompt:       "Generate weekly report",
			Schedule:     "0 8 * * 1",
			Repeat:       true,
			Enabled:      false,
			State:        "paused",
			PausedAt:     &now,
			PausedReason: "manual pause",
			CreatedAt:    &now,
		},
	}

	envelope := session.CronJobsFile{Jobs: jobs, UpdatedAt: &now}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeCronFile(t, dir, data)

	got, err := ParseCronJobs(dir)
	if err != nil {
		t.Fatalf("ParseCronJobs() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(jobs) = %d, want 2", len(got))
	}

	if got[0].ID != "job-001" {
		t.Errorf("jobs[0].ID = %q, want job-001", got[0].ID)
	}
	if got[0].Name != "Daily summary" {
		t.Errorf("jobs[0].Name = %q, want Daily summary", got[0].Name)
	}
	if !got[0].Enabled {
		t.Errorf("jobs[0].Enabled = false, want true")
	}
	if got[0].State != "active" {
		t.Errorf("jobs[0].State = %q, want active", got[0].State)
	}
	if got[0].CreatedAt == nil || !got[0].CreatedAt.Equal(now) {
		t.Errorf("jobs[0].CreatedAt = %v, want %v", got[0].CreatedAt, now)
	}
	if got[0].NextRunAt == nil || !got[0].NextRunAt.Equal(nextRun) {
		t.Errorf("jobs[0].NextRunAt = %v, want %v", got[0].NextRunAt, nextRun)
	}

	if got[1].ID != "job-002" {
		t.Errorf("jobs[1].ID = %q, want job-002", got[1].ID)
	}
	if got[1].Enabled {
		t.Errorf("jobs[1].Enabled = true, want false")
	}
	if got[1].State != "paused" {
		t.Errorf("jobs[1].State = %q, want paused", got[1].State)
	}
	if got[1].PausedReason != "manual pause" {
		t.Errorf("jobs[1].PausedReason = %q, want manual pause", got[1].PausedReason)
	}
	if got[1].PausedAt == nil || !got[1].PausedAt.Equal(now) {
		t.Errorf("jobs[1].PausedAt = %v, want %v", got[1].PausedAt, now)
	}
}

func TestParseCronJobs_MissingFile(t *testing.T) {
	dir := t.TempDir()

	got, err := ParseCronJobs(dir)
	if err != nil {
		t.Fatalf("ParseCronJobs() error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(jobs) = %d, want 0", len(got))
	}
}

func TestParseCronJobs_Truncated(t *testing.T) {
	dir := t.TempDir()

	truncated := []byte(`{"jobs":[{"id":"job-trunc","name":"truncated job","prompt":"do thing","enabled":true`)
	writeCronFile(t, dir, truncated)

	got, err := ParseCronJobs(dir)
	if err != nil {
		t.Fatalf("ParseCronJobs() error = %v, want nil", err)
	}
	_ = got
}
