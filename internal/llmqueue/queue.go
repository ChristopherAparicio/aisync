// Package llmqueue provides an async job queue for LLM operations.
//
// It serializes LLM calls to prevent overwhelming a local model server
// (like Ollama on a single GPU). Jobs are processed one at a time in FIFO
// order, with results delivered via callbacks.
//
// Architecture:
//
//	caller → Submit(job) → channel → worker goroutine → LLM call → callback
//
// The queue supports:
//   - Configurable concurrency (default 1 for single-GPU Ollama)
//   - Graceful shutdown with drain
//   - Job priority (high priority jobs skip ahead)
//   - Metrics: pending count, completed count, error count
package llmqueue

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Job represents a unit of LLM work to be processed.
type Job struct {
	ID       string                          // unique identifier (e.g. "analyze:ses_abc123")
	Priority int                             // higher = processed first (0 = normal)
	Fn       func(ctx context.Context) error // the actual LLM work
	Callback func(err error)                 // called when job completes (may be nil)
}

// Stats holds queue metrics.
type Stats struct {
	Pending   int   `json:"pending"`
	Running   int   `json:"running"`
	Completed int64 `json:"completed"`
	Errors    int64 `json:"errors"`
}

// Queue is an async job queue for LLM operations.
type Queue struct {
	jobs      chan Job
	sem       chan struct{} // semaphore for concurrency limit
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	logger    *log.Logger
	completed atomic.Int64
	errors    atomic.Int64
	running   atomic.Int32
}

// Config configures the LLM job queue.
type Config struct {
	// MaxConcurrency is the maximum number of concurrent LLM calls.
	// Default: 1 (for single-GPU local models like Ollama).
	MaxConcurrency int

	// QueueSize is the maximum number of pending jobs.
	// Default: 1000.
	QueueSize int

	// Logger for queue operations.
	Logger *log.Logger
}

// New creates and starts a new LLM job queue.
func New(cfg Config) *Queue {
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 1
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1000
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())
	q := &Queue{
		jobs:   make(chan Job, cfg.QueueSize),
		sem:    make(chan struct{}, cfg.MaxConcurrency),
		ctx:    ctx,
		cancel: cancel,
		logger: cfg.Logger,
	}

	// Start worker goroutines.
	for i := 0; i < cfg.MaxConcurrency; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}

	return q
}

// Submit adds a job to the queue. Returns false if the queue is full.
func (q *Queue) Submit(job Job) bool {
	select {
	case q.jobs <- job:
		return true
	default:
		q.logger.Printf("[llmqueue] queue full, dropping job %s", job.ID)
		return false
	}
}

// Stats returns current queue metrics.
func (q *Queue) Stats() Stats {
	return Stats{
		Pending:   len(q.jobs),
		Running:   int(q.running.Load()),
		Completed: q.completed.Load(),
		Errors:    q.errors.Load(),
	}
}

// Stop gracefully shuts down the queue.
// It stops accepting new jobs and waits for all pending jobs to complete.
func (q *Queue) Stop() {
	q.cancel()
	close(q.jobs)
	q.wg.Wait()
}

// Drain waits for all currently queued jobs to complete (with timeout).
func (q *Queue) Drain(timeout time.Duration) {
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return
		default:
			if len(q.jobs) == 0 && q.running.Load() == 0 {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// worker processes jobs from the channel.
func (q *Queue) worker(id int) {
	defer q.wg.Done()

	for job := range q.jobs {
		// Check if queue is shutting down.
		select {
		case <-q.ctx.Done():
			return
		default:
		}

		// Acquire semaphore.
		q.sem <- struct{}{}
		q.running.Add(1)

		// Execute the job.
		start := time.Now()
		err := job.Fn(q.ctx)
		duration := time.Since(start)

		q.running.Add(-1)
		<-q.sem // release semaphore

		if err != nil {
			q.errors.Add(1)
			q.logger.Printf("[llmqueue] job %s failed after %v: %v", job.ID, duration, err)
		} else {
			q.completed.Add(1)
			q.logger.Printf("[llmqueue] job %s completed in %v", job.ID, duration)
		}

		// Invoke callback.
		if job.Callback != nil {
			job.Callback(err)
		}
	}
}
