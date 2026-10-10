// Package jobs runs background work (renewals, webhook deliveries, emails) on a fixed number of workers.
package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
)

// Job is one piece of background work.
type Job struct {
	Name string
	Run  func(ctx context.Context) error
}

// ErrQueueFull is returned by Enqueue when the queue has no room.
var ErrQueueFull = errors.New("the job queue is full")

// Runner runs jobs on its workers.
type Runner struct {
	queue   chan Job
	workers int
	log     *slog.Logger
}

// NewRunner returns a runner with workers workers and room for queueSize waiting jobs. A nil log discards.
func NewRunner(workers, queueSize int, log *slog.Logger) *Runner {
	if workers < 1 {
		workers = 1
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Runner{queue: make(chan Job, queueSize), workers: workers, log: log}
}

// Start starts the workers.
func (r *Runner) Start() {
	for i := 0; i < r.workers; i++ {
		go r.work()
	}
}

func (r *Runner) work() {
	for j := range r.queue {
		if err := j.Run(context.Background()); err != nil {
			r.log.Error("job failed", "job", j.Name, "err", err)
		}
	}
}

// Enqueue adds a job to the queue without waiting. It returns ErrQueueFull when the queue has no room.
func (r *Runner) Enqueue(j Job) error {
	select {
	case r.queue <- j:
		return nil
	default:
		return ErrQueueFull
	}
}

// Stop stops the runner.
func (r *Runner) Stop() {
	close(r.queue)
}
