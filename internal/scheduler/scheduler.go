// Package scheduler provides a lightweight cron-based task scheduler
// for aisync serve. It wraps robfig/cron/v3 with a domain-specific
// Task interface and lifecycle management (Start/Stop).
//
// The scheduler runs inside the aisync serve process and stops
// gracefully when the server shuts down.
package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// Task is the port interface for schedulable work.
// Each task is a named unit of work with a cron expression.
type Task interface {
	// Name returns a human-readable identifier (e.g. "analyze_daily").
	Name() string

	// Run executes the task. Context is cancelled on shutdown.
	Run(ctx context.Context) error
}

// Entry pairs a Task with its cron schedule expression.
type Entry struct {
	Schedule string // cron expression (e.g. "0 22 * * *")
	Task     Task
}

// Scheduler manages periodic task execution.
type Scheduler struct {
	cron    *cron.Cron
	logger  *log.Logger
	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc

	// results tracks the last execution of each task (for status reporting).
	results map[string]*TaskResult
}

// TaskResult records the outcome of the last execution of a task.
type TaskResult struct {
	TaskName string    `json:"task_name"`
	LastRun  time.Time `json:"last_run"`
	Duration string    `json:"duration"`
	Error    string    `json:"error,omitempty"`
	NextRun  time.Time `json:"next_run,omitempty"`
}

// Config holds all parameters for creating a Scheduler.
type Config struct {
	Entries []Entry
	Logger  *log.Logger // optional — defaults to stderr
}

// New creates a Scheduler. Call Start() to begin execution.
func New(cfg Config) (*Scheduler, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}

	s := &Scheduler{
		cron: cron.New(cron.WithParser(cron.NewParser(
			cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
		))),
		logger:  logger,
		results: make(map[string]*TaskResult),
	}

	for _, entry := range cfg.Entries {
		if entry.Schedule == "" {
			continue // skip unconfigured tasks
		}
		task := entry.Task // capture for closure
		schedule := entry.Schedule

		_, err := s.cron.AddFunc(schedule, func() {
			s.runTask(task)
		})
		if err != nil {
			return nil, fmt.Errorf("invalid schedule %q for task %q: %w", schedule, task.Name(), err)
		}

		logger.Printf("[scheduler] registered task %q with schedule %q", task.Name(), schedule)
	}

	return s, nil
}

// Start begins the scheduler. Non-blocking — tasks run in background goroutines.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	_ = ctx // stored via cancel; tasks get fresh contexts per run

	s.cron.Start()
	s.running = true
	s.logger.Println("[scheduler] started")
}

// Stop gracefully stops the scheduler and waits for running tasks to finish.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	if s.cancel != nil {
		s.cancel()
	}

	ctx := s.cron.Stop() // returns context that's done when all jobs finish
	<-ctx.Done()

	s.running = false
	s.logger.Println("[scheduler] stopped")
}

// Status returns the last execution result for each registered task.
func (s *Scheduler) Status() []TaskResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	results := make([]TaskResult, 0, len(s.results))
	for _, r := range s.results {
		results = append(results, *r)
	}
	return results
}

// runTask executes a single task and records the result.
func (s *Scheduler) runTask(task Task) {
	name := task.Name()
	s.logger.Printf("[scheduler] running task %q", name)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	err := task.Run(ctx)
	duration := time.Since(start)

	result := &TaskResult{
		TaskName: name,
		LastRun:  start,
		Duration: duration.Round(time.Millisecond).String(),
	}
	if err != nil {
		result.Error = err.Error()
		s.logger.Printf("[scheduler] task %q failed after %s: %v", name, result.Duration, err)
	} else {
		s.logger.Printf("[scheduler] task %q completed in %s", name, result.Duration)
	}

	s.mu.Lock()
	s.results[name] = result
	s.mu.Unlock()
}
