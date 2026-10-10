package jobs_test

// Hidden acceptance test of the trial task tally-graceful-shutdown (evals/tasks). The agents never see it.
// Jobs are held with channels; waits that should take milliseconds get seconds before they fail, so the checks hold on
// saturated CPUs. Short waits only ever look for something that must NOT happen.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/tally/internal/jobs"
)

const hiddenWait = 10 * time.Second

// hiddenQueue is the queue size of runners that the probes of hiddenWaitStopped may fill: far more than they can enqueue.
const hiddenQueue = 1 << 16

// hiddenRec records what the test's jobs did.
type hiddenRec struct {
	mu       sync.Mutex
	runs     map[string]int
	returned map[string]bool
	ctxErr   map[string]error // the job's ctx.Err() when it returned
}

func hiddenNewRec() *hiddenRec {
	return &hiddenRec{runs: map[string]int{}, returned: map[string]bool{}, ctxErr: map[string]error{}}
}

func (h *hiddenRec) start(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs[name]++
}

func (h *hiddenRec) end(name string, ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.returned[name] = true
	h.ctxErr[name] = ctx.Err()
}

func (h *hiddenRec) ran(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs[name]
}

func (h *hiddenRec) hasReturned(name string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.returned[name], h.ctxErr[name]
}

// quick is a job that only records that it ran.
func (h *hiddenRec) quick(name string) jobs.Job {
	return jobs.Job{Name: name, Run: func(ctx context.Context) error {
		h.start(name)
		h.end(name, ctx)
		return nil
	}}
}

// held is a job that tells started it runs, then waits for release (and returns onRelease) or for its context to be
// canceled (then it calls beforeCancelReturn, if set, and returns onCancel).
func (h *hiddenRec) held(name string, started chan<- string, release <-chan struct{}, onRelease, onCancel error, beforeCancelReturn func()) jobs.Job {
	return jobs.Job{Name: name, Run: func(ctx context.Context) error {
		h.start(name)
		started <- name
		select {
		case <-release:
			h.end(name, ctx)
			return onRelease
		case <-ctx.Done():
			if beforeCancelReturn != nil {
				beforeCancelReturn()
			}
			h.end(name, ctx)
			return onCancel
		}
	}}
}

func hiddenEnqueue(t *testing.T, r *jobs.Runner, j jobs.Job) (err error) {
	t.Helper()
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("Enqueue(%s) panicked: %v", j.Name, v)
		}
	}()
	return r.Enqueue(j)
}

func hiddenMustEnqueue(t *testing.T, r *jobs.Runner, j jobs.Job) {
	t.Helper()
	if err := hiddenEnqueue(t, r, j); err != nil {
		t.Fatalf("Enqueue(%s) = %v", j.Name, err)
	}
}

func hiddenWaitStarted(t *testing.T, started <-chan string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-started:
		case <-time.After(hiddenWait):
			t.Fatalf("only %d of %d jobs started", i, n)
		}
	}
}

func hiddenShutdownAsync(r *jobs.Runner, ctx context.Context) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- r.Shutdown(ctx) }()
	return ch
}

func hiddenResult(t *testing.T, ch <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(hiddenWait):
		t.Fatalf("%s did not return within %s", what, hiddenWait)
		return nil
	}
}

func hiddenNotYet(t *testing.T, ch <-chan error, d time.Duration, what string) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("%s returned (%v) while a job was still running", what, err)
	case <-time.After(d):
	}
}

// hiddenWaitStopped enqueues no-op probe jobs until Enqueue returns ErrStopped, which shows that Shutdown has been called.
// It returns the names of the probes queued before that: every worker is busy, so they never start.
func hiddenWaitStopped(t *testing.T, r *jobs.Runner, h *hiddenRec) []string {
	t.Helper()
	var queued []string
	deadline := time.Now().Add(hiddenWait)
	for i := 0; ; i++ {
		name := fmt.Sprintf("probe-%d", i)
		err := hiddenEnqueue(t, r, h.quick(name))
		switch {
		case errors.Is(err, jobs.ErrStopped):
			return queued
		case err == nil:
			queued = append(queued, name)
		case errors.Is(err, jobs.ErrQueueFull):
		default:
			t.Fatalf("Enqueue(%s) = %v while Shutdown was being called, want ErrStopped", name, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Enqueue does not return ErrStopped %s after Shutdown was called", hiddenWait)
		}
		time.Sleep(time.Millisecond)
	}
}

func hiddenNames(js []jobs.Job) []string {
	out := []string{}
	for _, j := range js {
		out = append(out, j.Name)
	}
	return out
}

func hiddenWantPending(t *testing.T, r *jobs.Runner, what string, want []string) {
	t.Helper()
	got := hiddenNames(r.Pending())
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s: Pending() = %v, want %v", what, got, want)
	}
}

// hiddenRunnerGoroutines counts the goroutines that run code of package jobs (a runner's workers and helpers) and returns
// their stacks. The test's own goroutines run package jobs_test code and are not counted.
func hiddenRunnerGoroutines() (int, string) {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var found []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "example.com/tally/internal/jobs.") {
			found = append(found, g)
		}
	}
	return len(found), strings.Join(found, "\n\n")
}

// hiddenGoroutineBase is the number of goroutines running package jobs code before a test makes its runner (other tests
// may have left runners behind).
func hiddenGoroutineBase() int {
	n, _ := hiddenRunnerGoroutines()
	return n
}

// hiddenNoGoroutinesLeft waits until no more goroutines run package jobs code than before the test made its runner. A
// goroutine may still be on its way out for an instant after Shutdown returns, so it polls for a few seconds.
func hiddenNoGoroutinesLeft(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(hiddenWait)
	for {
		n, stacks := hiddenRunnerGoroutines()
		if n <= base {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after Shutdown returned, %d goroutine(s) of the runner are still there %s later (%d running package jobs code before the runner was made, %d now):\n%s", n-base, hiddenWait, base, n, stacks)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHiddenShutdownWaitsForRunningJobs(t *testing.T) {
	base := hiddenGoroutineBase()
	h := hiddenNewRec()
	r := jobs.NewRunner(2, hiddenQueue, nil)
	r.Start()
	started, release := make(chan string, 10), make(chan struct{})
	hiddenMustEnqueue(t, r, h.held("a", started, release, nil, context.Canceled, nil))
	hiddenMustEnqueue(t, r, h.held("b", started, release, errors.New("b failed on its own"), context.Canceled, nil))
	hiddenWaitStarted(t, started, 2) // both workers are busy now
	hiddenMustEnqueue(t, r, h.quick("c"))
	hiddenMustEnqueue(t, r, h.quick("d"))
	hiddenWantPending(t, r, "before Shutdown, with c and d queued", []string{"c", "d"})

	done := hiddenShutdownAsync(r, context.Background())
	probes := hiddenWaitStopped(t, r, h)
	hiddenNotYet(t, done, 100*time.Millisecond, "Shutdown")
	if err := hiddenEnqueue(t, r, h.quick("late")); !errors.Is(err, jobs.ErrStopped) {
		t.Fatalf("Enqueue during Shutdown = %v, want ErrStopped", err)
	}

	close(release)
	if err := hiddenResult(t, done, "Shutdown"); err != nil {
		t.Fatalf("Shutdown = %v, want nil: every running job finished before its context ended", err)
	}
	for _, name := range []string{"a", "b"} {
		returned, ctxErr := h.hasReturned(name)
		if !returned {
			t.Fatalf("Shutdown returned before the running job %s did", name)
		}
		if ctxErr != nil {
			t.Fatalf("the running job %s had its context canceled (%v) although Shutdown's context never ended", name, ctxErr)
		}
	}
	hiddenNoGoroutinesLeft(t, base)
	for _, name := range append([]string{"c", "d", "late"}, probes...) {
		if n := h.ran(name); n != 0 {
			t.Fatalf("job %s ran %d time(s); jobs that had not started before Shutdown must not start", name, n)
		}
	}
	// c, d and the probes never started; a finished and b failed on its own (neither was canceled by Shutdown).
	hiddenWantPending(t, r, "after Shutdown", append([]string{"c", "d"}, probes...))

	if err := hiddenResult(t, hiddenShutdownAsync(r, context.Background()), "a second Shutdown"); err != nil {
		t.Fatalf("a second Shutdown after the first finished = %v, want nil", err)
	}
	stopped := make(chan error, 1)
	go func() { r.Stop(); stopped <- nil }()
	hiddenResult(t, stopped, "Stop after Shutdown")
	if err := hiddenEnqueue(t, r, h.quick("after")); !errors.Is(err, jobs.ErrStopped) {
		t.Fatalf("Enqueue after Shutdown = %v, want ErrStopped", err)
	}
	hiddenWantPending(t, r, "after Enqueue was refused", append([]string{"c", "d"}, probes...))
}

func TestHiddenShutdownCancelsRunningJobsWhenItsContextEnds(t *testing.T) {
	base := hiddenGoroutineBase()
	h := hiddenNewRec()
	r := jobs.NewRunner(3, hiddenQueue, nil)
	r.Start()
	started := make(chan string, 10)
	j3returned := make(chan struct{})
	// j1 and j3 return an error when canceled, j2 returns nil. j3 returns first, so Pending must follow the enqueue order
	// (j1, j3) and not the order the jobs returned in.
	hiddenMustEnqueue(t, r, h.held("j1", started, nil, nil, context.Canceled, func() {
		<-j3returned
		time.Sleep(30 * time.Millisecond)
	}))
	hiddenMustEnqueue(t, r, h.held("j2", started, nil, nil, nil, nil))
	hiddenMustEnqueue(t, r, h.held("j3", started, nil, nil, errors.New("j3 interrupted"), func() { close(j3returned) }))
	hiddenWaitStarted(t, started, 3)
	hiddenMustEnqueue(t, r, h.quick("q1"))
	hiddenMustEnqueue(t, r, h.quick("q2"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := hiddenShutdownAsync(r, ctx)
	probes := hiddenWaitStopped(t, r, h)
	hiddenNotYet(t, done, 50*time.Millisecond, "Shutdown")
	for _, name := range []string{"j1", "j2", "j3"} {
		if returned, _ := h.hasReturned(name); returned {
			t.Fatalf("job %s returned before Shutdown's context ended: its context was canceled too early", name)
		}
	}

	cancel()
	err := hiddenResult(t, done, "Shutdown after its context ended")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown = %v, want ctx.Err() (context.Canceled)", err)
	}
	for _, name := range []string{"j1", "j2", "j3"} {
		returned, ctxErr := h.hasReturned(name)
		if !returned {
			t.Fatalf("Shutdown returned before the canceled job %s returned", name)
		}
		if ctxErr == nil {
			t.Fatalf("job %s returned without its context being canceled", name)
		}
	}
	hiddenNoGoroutinesLeft(t, base)
	for _, name := range append([]string{"q1", "q2"}, probes...) {
		if n := h.ran(name); n != 0 {
			t.Fatalf("job %s ran %d time(s); jobs that had not started before Shutdown must not start", name, n)
		}
	}
	want := append(append([]string{"q1", "q2"}, probes...), "j1", "j3")
	hiddenWantPending(t, r, "after Shutdown canceled j1, j2 and j3 (j2 returned nil)", want)
	for _, name := range []string{"j1", "j2", "j3"} {
		if n := h.ran(name); n != 1 {
			t.Fatalf("job %s ran %d times, want 1", name, n)
		}
	}
}

func TestHiddenShutdownDeadline(t *testing.T) {
	base := hiddenGoroutineBase()
	h := hiddenNewRec()
	r := jobs.NewRunner(1, 10, nil)
	r.Start()
	started := make(chan string, 1)
	hiddenMustEnqueue(t, r, h.held("slow", started, nil, nil, errors.New("slow: stopped"), nil))
	hiddenWaitStarted(t, started, 1)
	hiddenMustEnqueue(t, r, h.quick("queued"))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := hiddenResult(t, hiddenShutdownAsync(r, ctx), "Shutdown with a 150ms deadline")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want ctx.Err() (context.DeadlineExceeded)", err)
	}
	if returned, ctxErr := h.hasReturned("slow"); !returned || ctxErr == nil {
		t.Fatalf("the running job: returned %v, its context's error %v; want it canceled and returned", returned, ctxErr)
	}
	hiddenNoGoroutinesLeft(t, base)
	hiddenWantPending(t, r, "after the deadline", []string{"queued", "slow"})
	if n := h.ran("queued"); n != 0 {
		t.Fatalf("the queued job ran %d time(s) after Shutdown", n)
	}
}

func TestHiddenSecondShutdownAndStopReturnAtOnce(t *testing.T) {
	base := hiddenGoroutineBase()
	h := hiddenNewRec()
	r := jobs.NewRunner(1, hiddenQueue, nil)
	r.Start()
	started, release := make(chan string, 1), make(chan struct{})
	hiddenMustEnqueue(t, r, h.held("busy", started, release, nil, context.Canceled, nil))
	hiddenWaitStarted(t, started, 1)

	first := hiddenShutdownAsync(r, context.Background())
	hiddenWaitStopped(t, r, h)
	// While the first call waits for the running job, a second call and Stop return at once (with any error).
	hiddenResult(t, hiddenShutdownAsync(r, context.Background()), "a second Shutdown while the first was waiting")
	stopped := make(chan error, 1)
	go func() { r.Stop(); stopped <- nil }()
	hiddenResult(t, stopped, "Stop while Shutdown was waiting")
	hiddenNotYet(t, first, 50*time.Millisecond, "the first Shutdown")

	close(release)
	if err := hiddenResult(t, first, "the first Shutdown"); err != nil {
		t.Fatalf("the first Shutdown = %v, want nil", err)
	}
	if err := hiddenResult(t, hiddenShutdownAsync(r, context.Background()), "a third Shutdown"); err != nil {
		t.Fatalf("Shutdown after the first finished = %v, want nil", err)
	}
	hiddenNoGoroutinesLeft(t, base)
}

func TestHiddenStopStillWorks(t *testing.T) {
	base := hiddenGoroutineBase()
	h := hiddenNewRec()

	// Stop on a runner whose jobs are done.
	r := jobs.NewRunner(2, 10, nil)
	r.Start()
	ran := make(chan struct{})
	hiddenMustEnqueue(t, r, jobs.Job{Name: "one", Run: func(ctx context.Context) error { close(ran); return nil }})
	select {
	case <-ran:
	case <-time.After(hiddenWait):
		t.Fatal("the job did not run")
	}
	stopped := make(chan error, 1)
	go func() { r.Stop(); stopped <- nil }()
	hiddenResult(t, stopped, "Stop")
	for i := 0; i < 3; i++ {
		if err := hiddenEnqueue(t, r, h.quick("after-stop")); !errors.Is(err, jobs.ErrStopped) {
			t.Fatalf("Enqueue after Stop = %v, want ErrStopped", err)
		}
	}
	if n := h.ran("after-stop"); n != 0 || len(r.Pending()) != 0 {
		t.Fatalf("a job enqueued after Stop ran %d time(s) or was queued (%v)", n, hiddenNames(r.Pending()))
	}
	if err := hiddenResult(t, hiddenShutdownAsync(r, context.Background()), "Shutdown after Stop"); err != nil {
		t.Fatalf("Shutdown after Stop = %v, want nil", err)
	}

	// Stop waits for a running job.
	r2 := jobs.NewRunner(1, 10, nil)
	r2.Start()
	started, release := make(chan string, 1), make(chan struct{})
	hiddenMustEnqueue(t, r2, h.held("running", started, release, nil, context.Canceled, nil))
	hiddenWaitStarted(t, started, 1)
	stopped2 := make(chan error, 1)
	go func() { r2.Stop(); stopped2 <- nil }()
	hiddenNotYet(t, stopped2, 100*time.Millisecond, "Stop")
	close(release)
	hiddenResult(t, stopped2, "Stop after the running job finished")
	if returned, ctxErr := h.hasReturned("running"); !returned || ctxErr != nil {
		t.Fatalf("the running job: returned %v, its context's error %v; Stop should let it finish", returned, ctxErr)
	}
	if err := hiddenEnqueue(t, r2, h.quick("after-stop-2")); !errors.Is(err, jobs.ErrStopped) {
		t.Fatalf("Enqueue after Stop = %v, want ErrStopped", err)
	}
	hiddenNoGoroutinesLeft(t, base)
}

func TestHiddenPendingBeforeShutdown(t *testing.T) {
	h := hiddenNewRec()
	// A runner that was not started keeps everything queued.
	idle := jobs.NewRunner(1, 10, nil)
	for _, name := range []string{"x", "y", "z"} {
		hiddenMustEnqueue(t, idle, h.quick(name))
	}
	hiddenWantPending(t, idle, "a runner that was not started", []string{"x", "y", "z"})

	base := hiddenGoroutineBase()
	r := jobs.NewRunner(1, 10, nil)
	r.Start()
	started, release := make(chan string, 1), make(chan struct{})
	hiddenMustEnqueue(t, r, h.held("first", started, release, nil, context.Canceled, nil))
	hiddenWaitStarted(t, started, 1)
	for _, name := range []string{"b", "c", "d"} {
		hiddenMustEnqueue(t, r, h.quick(name))
	}
	hiddenWantPending(t, r, "one job running, three queued", []string{"b", "c", "d"})
	close(release)
	deadline := time.Now().Add(hiddenWait)
	for h.ran("b")+h.ran("c")+h.ran("d") < 3 {
		if time.Now().After(deadline) {
			t.Fatal("the queued jobs did not run")
		}
		time.Sleep(time.Millisecond)
	}
	hiddenWantPending(t, r, "every job ran", []string{})
	if err := hiddenResult(t, hiddenShutdownAsync(r, context.Background()), "Shutdown"); err != nil {
		t.Fatalf("Shutdown = %v", err)
	}
	hiddenWantPending(t, r, "after Shutdown, every job had run", []string{})
	hiddenNoGoroutinesLeft(t, base)
}

// hiddenSyncBuffer is a log destination that workers may write to at the same time.
type hiddenSyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *hiddenSyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *hiddenSyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestHiddenPanickingJobIsRecovered(t *testing.T) {
	base := hiddenGoroutineBase()
	logs := &hiddenSyncBuffer{}
	h := hiddenNewRec()
	r := jobs.NewRunner(1, 10, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	r.Start()
	boom := func(name string) jobs.Job {
		return jobs.Job{Name: name, Run: func(ctx context.Context) error {
			h.start(name)
			panic("hidden panic in " + name)
		}}
	}
	// One worker: the jobs after a panicking one only run if the worker goes on.
	hiddenMustEnqueue(t, r, boom("boom-1"))
	hiddenMustEnqueue(t, r, h.quick("after-1"))
	hiddenMustEnqueue(t, r, boom("boom-2"))
	hiddenMustEnqueue(t, r, h.quick("after-2"))
	deadline := time.Now().Add(hiddenWait)
	for h.ran("after-1") == 0 || h.ran("after-2") == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the jobs after a panicking job did not run (after-1: %d, after-2: %d)", h.ran("after-1"), h.ran("after-2"))
		}
		time.Sleep(time.Millisecond)
	}
	for deadline := time.Now().Add(hiddenWait); strings.TrimSpace(logs.String()) == ""; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the worker logged nothing about the panicking jobs")
		}
	}
	if err := hiddenResult(t, hiddenShutdownAsync(r, context.Background()), "Shutdown"); err != nil {
		t.Fatalf("Shutdown = %v", err)
	}
	for _, name := range []string{"boom-1", "boom-2"} {
		if n := h.ran(name); n != 1 {
			t.Fatalf("the panicking job %s ran %d times, want 1 (it is not run again)", name, n)
		}
	}
	hiddenWantPending(t, r, "after Shutdown (the panicking jobs are not run again)", []string{})
	hiddenNoGoroutinesLeft(t, base)
}
