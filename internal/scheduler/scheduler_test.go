package scheduler

import (
	"context"
	"errors"
	"log"
	"sync/atomic"
	"testing"
	"time"
)

// ── Mock Task ──

type mockTask struct {
	name     string
	runCount atomic.Int32
	err      error
	delay    time.Duration
}

func (m *mockTask) Name() string { return m.name }
func (m *mockTask) Run(ctx context.Context) error {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.runCount.Add(1)
	return m.err
}

// ── Scheduler Tests ──

func TestNew_InvalidSchedule(t *testing.T) {
	_, err := New(Config{
		Entries: []Entry{
			{Schedule: "invalid cron", Task: &mockTask{name: "bad"}},
		},
		Logger: log.Default(),
	})
	if err == nil {
		t.Fatal("expected error for invalid cron expression")
	}
}

func TestNew_EmptyScheduleSkipped(t *testing.T) {
	s, err := New(Config{
		Entries: []Entry{
			{Schedule: "", Task: &mockTask{name: "skipped"}},
		},
		Logger: log.Default(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should create without error — empty schedule is silently skipped.
	s.Start()
	defer s.Stop()
}

func TestScheduler_StartStop(t *testing.T) {
	s, err := New(Config{
		Logger: log.Default(),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	s.Start()
	s.Start() // idempotent
	s.Stop()
	s.Stop() // idempotent
}

func TestScheduler_RunsTask(t *testing.T) {
	task := &mockTask{name: "test-task"}

	// Use a per-second cron — runs every second.
	s, err := New(Config{
		Entries: []Entry{
			{Schedule: "* * * * *", Task: task}, // every minute
		},
		Logger: log.Default(),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Manually trigger via runTask to test without waiting.
	s.runTask(task)

	if task.runCount.Load() != 1 {
		t.Errorf("expected 1 run, got %d", task.runCount.Load())
	}

	results := s.Status()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].TaskName != "test-task" {
		t.Errorf("task name = %q, want %q", results[0].TaskName, "test-task")
	}
	if results[0].Error != "" {
		t.Errorf("unexpected error: %s", results[0].Error)
	}
}

func TestScheduler_TaskError(t *testing.T) {
	task := &mockTask{
		name: "failing-task",
		err:  errors.New("something broke"),
	}

	s, err := New(Config{
		Entries: []Entry{
			{Schedule: "* * * * *", Task: task},
		},
		Logger: log.Default(),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	s.runTask(task)

	results := s.Status()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == "" {
		t.Error("expected error in result")
	}
}

func TestScheduler_StatusEmpty(t *testing.T) {
	s, _ := New(Config{Logger: log.Default()})
	results := s.Status()
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
