package workflow_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wf "github.com/kjmaguire/drydock/internal/workflow"
)

// A person's pause stops an agent turn in flight and parks the run; only ResumePaused brings it back (not the license's
// ResumeSuspended), from the checkpoint, and the turns a person stopped do not count as turns Drydock cut off: paused twice on
// the same step, the turn runs a third time without the interrupted-turn approval.
func TestAPausedRunResumesOnlyOnAPersonsResumeAndItsStopsAreNotInterruptions(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	var starts atomic.Int32
	started := make(chan struct{}, 4)
	g := wf.NewGraph("pausable").Add(
		wf.AgentTask("W", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			n := starts.Add(1)
			effect(dir, "W_start")
			started <- struct{}{}
			if n < 3 {
				<-ctx.Done()
				return wf.NodeOutput{}, ctx.Err()
			}
			effect(dir, "W_done")
			return wf.NodeOutput{Value: val("W")}, nil
		}),
		wf.Check("B", eff(dir, "B", ""))).Edge("W", "B").Start("W")
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	run, err := e.eng.Start(context.Background(), "pausable", nil, wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		<-started
		if parked, err := e.eng.Pause(context.Background(), run, "by human:test"); err != nil || !parked {
			t.Fatalf("pause %d: %v %v", i, parked, err)
		}
		info, _ := e.eng.Status(context.Background(), run)
		if info.Status != wf.StatusPaused || info.FailedReason != wf.PausedPrefix+"by human:test" {
			t.Fatalf("paused run %+v", info)
		}
		if parked, _ := e.eng.Pause(context.Background(), run, "again"); parked {
			t.Fatal("a paused run is paused only once")
		}
		if n, err := e.eng.ResumeSuspended(context.Background()); err != nil || n != 0 {
			t.Fatalf("the license's resume brought back a person's pause: %d %v", n, err)
		}
		time.Sleep(100 * time.Millisecond)
		if got := effects(dir)["W_start"]; got != i+1 {
			t.Fatalf("the turn ran while paused: %d starts", got)
		}
		if ok, err := e.eng.ResumePaused(context.Background(), run); err != nil || !ok {
			t.Fatalf("resume %d: %v %v", i, ok, err)
		}
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	if got := effects(dir); got["W_start"] != 3 || got["W_done"] != 1 || got["B"] != 1 {
		t.Fatalf("effects %v", got)
	}
	if ok, err := e.eng.ResumePaused(context.Background(), run); err != nil || ok {
		t.Fatalf("a completed run was resumed: %v %v", ok, err)
	}
}

// ResumePaused leaves a run the license suspended alone: that one comes back with the license.
func TestResumePausedLeavesASuspendedRunAlone(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	started := make(chan struct{})
	var once sync.Once
	g := wf.NewGraph("susp2").Add(
		wf.Check("A", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return wf.NodeOutput{}, ctx.Err()
		})).Start("A")
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	run, err := e.eng.Start(context.Background(), "susp2", nil, wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if parked, err := e.eng.Suspend(context.Background(), run, "the licence expired"); err != nil || !parked {
		t.Fatalf("suspend: %v %v", parked, err)
	}
	if ok, err := e.eng.ResumePaused(context.Background(), run); err != nil || ok {
		t.Fatalf("a suspended run was resumed as a person's pause: %v %v", ok, err)
	}
	if info, _ := e.eng.Status(context.Background(), run); info.Status != wf.StatusPaused {
		t.Fatalf("status %s", info.Status)
	}
}
