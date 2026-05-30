package sqlite

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func makeTestCronJob(id, provider, name string) *session.CronJob {
	t := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	return &session.CronJob{
		ID:              id,
		Provider:        provider,
		Name:            name,
		Prompt:          "do the thing",
		Schedule:        "0 * * * *",
		ScheduleDisplay: "Every hour",
		Repeat:          true,
		Enabled:         true,
		State:           "idle",
		Model:           "claude-sonnet",
		NextRunAt:       &t,
		LastStatus:      "success",
		Origin:          "test",
		Workdir:         "/tmp",
		Profile:         "default",
	}
}

func TestCronJobStore_UpsertAndGet(t *testing.T) {
	store := mustOpenStore(t)

	job := makeTestCronJob("job-001", "hermes", "hourly-sync")

	if err := store.UpsertCronJob(job); err != nil {
		t.Fatalf("UpsertCronJob() error = %v", err)
	}

	got, err := store.GetCronJob("job-001")
	if err != nil {
		t.Fatalf("GetCronJob() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetCronJob() = nil, want job")
	}

	tests := []struct {
		field string
		got   string
		want  string
	}{
		{"ID", got.ID, "job-001"},
		{"Provider", got.Provider, "hermes"},
		{"Name", got.Name, "hourly-sync"},
		{"Prompt", got.Prompt, "do the thing"},
		{"Schedule", got.Schedule, "0 * * * *"},
		{"ScheduleDisplay", got.ScheduleDisplay, "Every hour"},
		{"State", got.State, "idle"},
		{"Model", got.Model, "claude-sonnet"},
		{"LastStatus", got.LastStatus, "success"},
		{"Origin", got.Origin, "test"},
		{"Workdir", got.Workdir, "/tmp"},
		{"Profile", got.Profile, "default"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}

	if !got.Repeat {
		t.Error("Repeat = false, want true")
	}
	if !got.Enabled {
		t.Error("Enabled = false, want true")
	}
	if got.NextRunAt == nil {
		t.Fatal("NextRunAt = nil, want time")
	}
	if got.NextRunAt.Unix() != job.NextRunAt.Unix() {
		t.Errorf("NextRunAt = %v, want %v", got.NextRunAt, job.NextRunAt)
	}
}

func TestCronJobStore_Upsert_Idempotent(t *testing.T) {
	store := mustOpenStore(t)

	job := makeTestCronJob("job-002", "hermes", "original-name")
	if err := store.UpsertCronJob(job); err != nil {
		t.Fatalf("first UpsertCronJob() error = %v", err)
	}

	job.Name = "updated-name"
	job.LastStatus = "failed"
	job.LastError = "timeout"
	if err := store.UpsertCronJob(job); err != nil {
		t.Fatalf("second UpsertCronJob() error = %v", err)
	}

	got, err := store.GetCronJob("job-002")
	if err != nil {
		t.Fatalf("GetCronJob() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetCronJob() = nil, want job")
	}
	if got.Name != "updated-name" {
		t.Errorf("Name = %q, want %q", got.Name, "updated-name")
	}
	if got.LastStatus != "failed" {
		t.Errorf("LastStatus = %q, want %q", got.LastStatus, "failed")
	}
	if got.LastError != "timeout" {
		t.Errorf("LastError = %q, want %q", got.LastError, "timeout")
	}

	all, err := store.ListCronJobs("")
	if err != nil {
		t.Fatalf("ListCronJobs() error = %v", err)
	}
	if len(all) != 1 {
		t.Errorf("ListCronJobs() len = %d, want 1 (idempotent upsert must not duplicate)", len(all))
	}
}

func TestCronJobStore_ListByProvider(t *testing.T) {
	store := mustOpenStore(t)

	hermesJob := makeTestCronJob("job-hermes-1", "hermes", "hermes-task")
	otherJob := makeTestCronJob("job-other-1", "opencode", "opencode-task")

	if err := store.UpsertCronJob(hermesJob); err != nil {
		t.Fatalf("UpsertCronJob(hermes) error = %v", err)
	}
	if err := store.UpsertCronJob(otherJob); err != nil {
		t.Fatalf("UpsertCronJob(opencode) error = %v", err)
	}

	allJobs, err := store.ListCronJobs("")
	if err != nil {
		t.Fatalf("ListCronJobs(\"\") error = %v", err)
	}
	if len(allJobs) != 2 {
		t.Errorf("ListCronJobs(\"\") len = %d, want 2", len(allJobs))
	}

	hermesJobs, err := store.ListCronJobs("hermes")
	if err != nil {
		t.Fatalf("ListCronJobs(\"hermes\") error = %v", err)
	}
	if len(hermesJobs) != 1 {
		t.Fatalf("ListCronJobs(\"hermes\") len = %d, want 1", len(hermesJobs))
	}
	if hermesJobs[0].ID != "job-hermes-1" {
		t.Errorf("hermes job ID = %q, want %q", hermesJobs[0].ID, "job-hermes-1")
	}
	if hermesJobs[0].Provider != "hermes" {
		t.Errorf("hermes job Provider = %q, want %q", hermesJobs[0].Provider, "hermes")
	}

	opencodeJobs, err := store.ListCronJobs("opencode")
	if err != nil {
		t.Fatalf("ListCronJobs(\"opencode\") error = %v", err)
	}
	if len(opencodeJobs) != 1 {
		t.Fatalf("ListCronJobs(\"opencode\") len = %d, want 1", len(opencodeJobs))
	}
	if opencodeJobs[0].Provider != "opencode" {
		t.Errorf("opencode job Provider = %q, want %q", opencodeJobs[0].Provider, "opencode")
	}
}

func TestCronJobStore_GetNotFound(t *testing.T) {
	store := mustOpenStore(t)

	got, err := store.GetCronJob("nonexistent")
	if err != nil {
		t.Fatalf("GetCronJob(nonexistent) error = %v", err)
	}
	if got != nil {
		t.Errorf("GetCronJob(nonexistent) = %+v, want nil", got)
	}
}

func TestCronJobStore_NullableTimestamp(t *testing.T) {
	store := mustOpenStore(t)

	job := makeTestCronJob("job-nulltime", "hermes", "null-time-job")
	job.NextRunAt = nil
	job.LastRunAt = nil

	if err := store.UpsertCronJob(job); err != nil {
		t.Fatalf("UpsertCronJob() error = %v", err)
	}

	got, err := store.GetCronJob("job-nulltime")
	if err != nil {
		t.Fatalf("GetCronJob() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetCronJob() = nil, want job")
	}
	if got.NextRunAt != nil {
		t.Errorf("NextRunAt = %v, want nil", got.NextRunAt)
	}
	if got.LastRunAt != nil {
		t.Errorf("LastRunAt = %v, want nil", got.LastRunAt)
	}
}
