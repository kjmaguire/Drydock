package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRunnerRunsJobs(t *testing.T) {
	r := NewRunner(3, 10, nil)
	r.Start()
	var mu sync.Mutex
	done := map[string]bool{}
	var wg sync.WaitGroup
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		wg.Add(1)
		if err := r.Enqueue(Job{Name: name, Run: func(ctx context.Context) error {
			defer wg.Done()
			mu.Lock()
			done[name] = true
			mu.Unlock()
			return nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("jobs did not run")
	}
	if len(done) != 5 {
		t.Fatalf("done = %v", done)
	}
}

func TestEnqueueWhenFull(t *testing.T) {
	r := NewRunner(1, 1, nil) // not started: nothing takes jobs off the queue
	job := Job{Name: "x", Run: func(context.Context) error { return nil }}
	if err := r.Enqueue(job); err != nil {
		t.Fatal(err)
	}
	if err := r.Enqueue(job); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull", err)
	}
}
