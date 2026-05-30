package hermes

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestLinkCronRuns_Match(t *testing.T) {
	jobs := []session.CronJob{{ID: "job123", Name: "My Job"}}
	sessions := []session.Summary{{ID: "cron_job123_1700000000"}}

	links := LinkCronRuns(jobs, sessions)

	if len(links) != 1 {
		t.Fatalf("len(links) = %d, want 1", len(links))
	}
	l := links[0]
	if l.SessionID != "cron_job123_1700000000" {
		t.Errorf("SessionID = %q, want cron_job123_1700000000", l.SessionID)
	}
	if l.JobID != "job123" {
		t.Errorf("JobID = %q, want job123", l.JobID)
	}
	if l.Orphan {
		t.Errorf("Orphan = true, want false")
	}
	if l.Job == nil {
		t.Fatalf("Job = nil, want non-nil")
	}
	if l.Job.ID != "job123" {
		t.Errorf("Job.ID = %q, want job123", l.Job.ID)
	}
}

func TestLinkCronRuns_Orphan(t *testing.T) {
	jobs := []session.CronJob{}
	sessions := []session.Summary{{ID: "cron_ghost_9999999"}}

	links := LinkCronRuns(jobs, sessions)

	if len(links) != 1 {
		t.Fatalf("len(links) = %d, want 1", len(links))
	}
	l := links[0]
	if l.SessionID != "cron_ghost_9999999" {
		t.Errorf("SessionID = %q, want cron_ghost_9999999", l.SessionID)
	}
	if l.JobID != "ghost" {
		t.Errorf("JobID = %q, want ghost", l.JobID)
	}
	if !l.Orphan {
		t.Errorf("Orphan = false, want true")
	}
	if l.Job != nil {
		t.Errorf("Job = %+v, want nil", l.Job)
	}
}

func TestLinkCronRuns_NonCronSkipped(t *testing.T) {
	jobs := []session.CronJob{{ID: "job123"}}
	sessions := []session.Summary{
		{ID: "fixture-parent-001"},
		{ID: "cron_job123_1700000000"},
	}

	links := LinkCronRuns(jobs, sessions)

	if len(links) != 1 {
		t.Fatalf("len(links) = %d, want 1 (non-cron session must be skipped)", len(links))
	}
	if links[0].SessionID != "cron_job123_1700000000" {
		t.Errorf("SessionID = %q, want cron_job123_1700000000", links[0].SessionID)
	}
}

func TestLinkCronRuns_MultiSegment(t *testing.T) {
	jobs := []session.CronJob{{ID: "job_with_underscores", Name: "Underscore Job"}}
	sessions := []session.Summary{{ID: "cron_job_with_underscores_1700"}}

	links := LinkCronRuns(jobs, sessions)

	if len(links) != 1 {
		t.Fatalf("len(links) = %d, want 1", len(links))
	}
	l := links[0]
	if l.JobID != "job_with_underscores" {
		t.Errorf("JobID = %q, want job_with_underscores", l.JobID)
	}
	if l.Orphan {
		t.Errorf("Orphan = true, want false")
	}
	if l.Job == nil || l.Job.ID != "job_with_underscores" {
		t.Errorf("Job = %+v, want job with ID job_with_underscores", l.Job)
	}
}
