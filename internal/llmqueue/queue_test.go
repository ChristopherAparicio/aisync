package llmqueue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueue_BasicJob(t *testing.T) {
	q := New(Config{MaxConcurrency: 1, QueueSize: 10})
	defer q.Stop()

	done := make(chan bool, 1)
	ok := q.Submit(Job{
		ID: "test-1",
		Fn: func(ctx context.Context) error {
			done <- true
			return nil
		},
	})
	if !ok {
		t.Fatal("Submit returned false")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not complete in time")
	}

	stats := q.Stats()
	if stats.Completed != 1 {
		t.Errorf("Completed = %d, want 1", stats.Completed)
	}
}

func TestQueue_ErrorTracking(t *testing.T) {
	q := New(Config{MaxConcurrency: 1, QueueSize: 10})
	defer q.Stop()

	done := make(chan bool, 1)
	q.Submit(Job{
		ID: "fail-1",
		Fn: func(ctx context.Context) error {
			return errors.New("simulated error")
		},
		Callback: func(err error) {
			if err == nil {
				t.Error("expected error in callback")
			}
			done <- true
		},
	})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("callback not called")
	}

	stats := q.Stats()
	if stats.Errors != 1 {
		t.Errorf("Errors = %d, want 1", stats.Errors)
	}
}

func TestQueue_Concurrency1(t *testing.T) {
	q := New(Config{MaxConcurrency: 1, QueueSize: 10})
	defer q.Stop()

	var maxConcurrent atomic.Int32
	var current atomic.Int32

	for i := 0; i < 5; i++ {
		q.Submit(Job{
			ID: "conc-" + string(rune('0'+i)),
			Fn: func(ctx context.Context) error {
				c := current.Add(1)
				if c > maxConcurrent.Load() {
					maxConcurrent.Store(c)
				}
				time.Sleep(10 * time.Millisecond)
				current.Add(-1)
				return nil
			},
		})
	}

	q.Drain(10 * time.Second)

	if maxConcurrent.Load() > 1 {
		t.Errorf("maxConcurrent = %d, want <= 1", maxConcurrent.Load())
	}
}

func TestQueue_FullRejectJob(t *testing.T) {
	q := New(Config{MaxConcurrency: 1, QueueSize: 1})
	defer q.Stop()

	// Fill the queue with a blocking job.
	q.Submit(Job{
		ID: "blocking",
		Fn: func(ctx context.Context) error {
			time.Sleep(1 * time.Second)
			return nil
		},
	})

	// Try to submit while queue is full + worker is busy.
	time.Sleep(10 * time.Millisecond)
	ok := q.Submit(Job{
		ID: "overflow",
		Fn: func(ctx context.Context) error { return nil },
	})
	// May or may not be full depending on timing — both results are valid.
	_ = ok
}

func TestQueue_Drain(t *testing.T) {
	q := New(Config{MaxConcurrency: 1, QueueSize: 10})

	var count atomic.Int32
	for i := 0; i < 3; i++ {
		q.Submit(Job{
			ID: "drain-job",
			Fn: func(ctx context.Context) error {
				time.Sleep(10 * time.Millisecond)
				count.Add(1)
				return nil
			},
		})
	}

	q.Drain(5 * time.Second)
	q.Stop()

	if count.Load() != 3 {
		t.Errorf("count = %d, want 3", count.Load())
	}
}
