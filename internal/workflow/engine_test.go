package workflow_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	wf "github.com/kjmaguire/drydock/internal/workflow"
)

// ---- harness ----

const leaseTTL = 300 * time.Millisecond

type env struct {
	dir string
	db  *storage.DB
	bus *events.SQLBus
	eng *wf.Engine
}

func openEnv(t *testing.T, dir string) *env {
	t.Helper()
	return openEnvLease(t, dir, leaseTTL)
}

// openEnvLease is openEnv with a lease of its own length.
func openEnvLease(t *testing.T, dir string, ttl time.Duration) *env {
	t.Helper()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 4096)
	eng := wf.New(wf.Config{DB: db, Bus: bus, DataDir: dir, LeaseTTL: ttl})
	e := &env{dir: dir, db: db, bus: bus, eng: eng}
	t.Cleanup(func() { eng.Close(); bus.Close(); db.Close() })
	return e
}

// effect appends a line to <dir>/effects.log: the observable side effect used to prove exactly-once behaviour.
func effect(dir, name string) {
	f, err := os.OpenFile(filepath.Join(dir, "effects.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	fmt.Fprintln(f, name)
}

func effects(dir string) map[string]int {
	out := map[string]int{}
	b, _ := os.ReadFile(filepath.Join(dir, "effects.log"))
	for _, l := range strings.Fields(string(b)) {
		out[l]++
	}
	return out
}

func val(s string) json.RawMessage { b, _ := json.Marshal(s); return b }

func eff(dir, name string, route string) wf.NodeHandler {
	return func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		effect(dir, name)
		return wf.NodeOutput{Value: val(name), Route: route}, nil
	}
}

func waitStatus(t *testing.T, e *wf.Engine, run wf.RunID, want ...wf.RunStatus) wf.RunInfo {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := e.Await(ctx, run, func(r wf.RunInfo) bool {
		for _, w := range want {
			if r.Status == w {
				return true
			}
		}
		return r.Status.Terminal()
	})
	if err != nil {
		t.Fatalf("await: %v (status %s)", err, info.Status)
	}
	ok := false
	for _, w := range want {
		ok = ok || info.Status == w
	}
	if !ok {
		t.Fatalf("run ended %s (%s), wanted %v", info.Status, info.FailedReason, want)
	}
	return info
}

// ---- child-process scenarios (real processes, killed with SIGKILL / TerminateProcess) ----

func TestMain(m *testing.M) {
	if sc := os.Getenv("DRYDOCK_WF_CHILD"); sc != "" {
		runChild(sc, os.Getenv("DRYDOCK_WF_DIR"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func approvalGraph(dir string) *wf.Graph {
	return wf.NewGraph("approval").
		Add(wf.AgentTask("A", eff(dir, "A", "")), wf.Request("R", wf.RequestSpec{Kind: "approval", Title: "ship it?"}), wf.AgentTask("B", eff(dir, "B", ""))).
		Edge("A", "R").EdgeWhen("R", "B", "approved").Start("A")
}

func partialGraph(dir string, blockB bool) *wf.Graph {
	b := func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		if blockB && in.Previous == nil {
			effect(dir, "B_start")
			time.Sleep(time.Hour) // killed by the parent while here
		}
		effect(dir, "B")
		return wf.NodeOutput{Value: val("B")}, nil
	}
	return wf.NewGraph("partial").
		Add(wf.AgentTask("S", eff(dir, "S", "")), wf.AgentTask("A", eff(dir, "A", "")), wf.AgentTask("B", b), wf.AgentTask("J", eff(dir, "J", "")).Join("A", "B")).
		Edge("S", "A").Edge("S", "B").Edge("A", "J").Edge("B", "J").Start("S")
}

func streamGraph(dir string, crash bool) *wf.Graph {
	h := func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		if crash && in.Previous == nil {
			for i := 0; i < 5; i++ {
				in.Emit(wf.Chunk{Kind: "text", Data: val(fmt.Sprintf("part-%d", i))})
			}
			effect(dir, "chunks_emitted")
			time.Sleep(time.Hour)
		}
		seen := 0
		if in.Previous != nil && in.Previous.PartialRef != "" {
			if f, err := os.Open(in.Previous.PartialRef); err == nil {
				sc := bufio.NewScanner(f)
				for sc.Scan() {
					seen++
				}
				f.Close()
			}
		}
		effect(dir, fmt.Sprintf("retry_saw_%d_chunks", seen))
		return wf.NodeOutput{Value: val("done")}, nil
	}
	return wf.NewGraph("stream").Add(wf.AgentTask("W", h)).Start("W")
}

// interruptGraph is one agent turn, W, that the parent cuts off (blocking) or lets finish.
func interruptGraph(dir string, block bool) *wf.Graph {
	return wf.NewGraph("interrupt").Add(wf.AgentTask("W", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		effect(dir, "W_start")
		if block {
			time.Sleep(time.Hour) // killed by the parent while here
		}
		effect(dir, "W_done")
		return wf.NodeOutput{Value: val("W")}, nil
	})).Start("W")
}

func runChild(sc, dir string) {
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, dir)
	if err != nil {
		fmt.Println("child open:", err)
		os.Exit(2)
	}
	bus := events.NewSQLBus(db, 1024)
	eng := wf.New(wf.Config{DB: db, Bus: bus, DataDir: dir, LeaseTTL: leaseTTL})
	var g *wf.Graph
	switch sc {
	case "approval":
		g = approvalGraph(dir)
	case "partial":
		g = partialGraph(dir, true)
	case "stream":
		g = streamGraph(dir, true)
	case "interrupt", "interrupt-again":
		g = interruptGraph(dir, true)
	}
	if err := eng.Register(g); err != nil {
		fmt.Println("child register:", err)
		os.Exit(2)
	}
	if sc == "interrupt-again" { // the app started again: the run is taken over, not started
		if n, err := eng.Recover(ctx); err != nil || n != 1 {
			fmt.Println("child recover:", n, err)
			os.Exit(2)
		}
		fmt.Println("RUN recovered")
		os.Stdout.Sync()
		time.Sleep(time.Hour)
	}
	id, err := eng.Start(ctx, g.Name, map[string]string{"x": "1"}, wf.StartOptions{})
	if err != nil {
		fmt.Println("child start:", err)
		os.Exit(2)
	}
	fmt.Println("RUN", id)
	os.Stdout.Sync()
	time.Sleep(time.Hour)
}

// startChild launches the test binary as a separate process running scenario sc and returns it plus the run id.
func startChild(t *testing.T, sc, dir string) (*exec.Cmd, wf.RunID) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=NONE")
	cmd.Env = append(os.Environ(), "DRYDOCK_WF_CHILD="+sc, "DRYDOCK_WF_DIR="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "RUN ") {
				line <- strings.TrimPrefix(sc.Text(), "RUN ")
				return
			}
		}
		line <- ""
	}()
	select {
	case id := <-line:
		if id == "" {
			t.Fatal("child exited before starting a run")
		}
		return cmd, wf.RunID(id)
	case <-time.After(30 * time.Second):
		t.Fatal("child did not start a run in time")
	}
	return nil, ""
}

func kill(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	time.Sleep(leaseTTL + 200*time.Millisecond) // let the dead owner's lease expire
}

// ---- acceptance tests ----

func TestApprovalResumeAcrossKill(t *testing.T) {
	dir := t.TempDir()
	child, run := startChild(t, "approval", dir)

	// Wait (from this process, reading the same SQLite file) until the child's run is parked at the request port.
	probe := openEnv(t, dir)
	waitStatus(t, probe.eng, run, wf.StatusWaitingHuman)
	kill(t, child)

	// "Restart the app": a fresh engine on the same database.
	e := openEnv(t, dir)
	if err := e.eng.Register(approvalGraph(dir)); err != nil {
		t.Fatal(err)
	}
	if n, err := e.eng.Recover(context.Background()); err != nil || n != 0 {
		t.Fatalf("a WAITING_HUMAN run must not be recovered/re-run: n=%d err=%v", n, err)
	}
	pend, err := e.eng.Pending(context.Background(), run)
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending: %v %v", pend, err)
	}
	if err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	got := effects(dir)
	if got["A"] != 1 || got["B"] != 1 {
		t.Fatalf("exactly-once violated: %v", got)
	}
	// answering again must fail
	err = e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "approved"})
	if !errors.Is(err, wf.ErrRunTerminal) {
		t.Fatalf("second answer should be rejected, got %v", err)
	}
	// checkpoint kinds: entry, step(s), response_entry, step(s)
	cps, _ := e.eng.RunCheckpoints(context.Background(), run)
	kinds := []string{}
	for _, c := range cps {
		kinds = append(kinds, string(c.Kind))
	}
	if kinds[0] != "entry" || !contains(kinds, "response_entry") {
		t.Fatalf("checkpoint kinds %v", kinds)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestPartialSuperstep(t *testing.T) {
	dir := t.TempDir()
	child, run := startChild(t, "partial", dir)

	probe := openEnv(t, dir)
	// wait until A is recorded done and B has started but not finished
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for {
		rows, _ := probe.eng.Executions(ctx, run)
		aDone, bStarted := false, false
		for _, r := range rows {
			if r.NodeID == "A" && r.Status == "done" {
				aDone = true
			}
			if r.NodeID == "B" && r.Status == "started" {
				bStarted = true
			}
		}
		if aDone && bStarted && effects(dir)["B_start"] == 1 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("timed out waiting for the partial superstep state")
		}
		time.Sleep(10 * time.Millisecond)
	}
	kill(t, child)

	e := openEnv(t, dir)
	if err := e.eng.Register(partialGraph(dir, false)); err != nil {
		t.Fatal(err)
	}
	n, err := e.eng.Recover(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("recover n=%d err=%v", n, err)
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	got := effects(dir)
	if got["S"] != 1 || got["A"] != 1 || got["B"] != 1 || got["J"] != 1 {
		t.Fatalf("finished node re-ran or unfinished node ran !=1 times: %v", got)
	}
	rows, _ := e.eng.Executions(context.Background(), run)
	var bAttempts []string
	for _, r := range rows {
		if r.NodeID == "B" {
			bAttempts = append(bAttempts, fmt.Sprintf("%d:%s", r.Attempt, r.Status))
		}
	}
	if len(bAttempts) != 2 || bAttempts[0] != "1:abandoned" || bAttempts[1] != "2:done" {
		t.Fatalf("B attempts %v", bAttempts)
	}
}

func TestDuplicateResume(t *testing.T) {
	dir := t.TempDir()
	// Eight Resumes on two engines queue behind one database writer. With the short test lease, a writer stall on Windows CI
	// (20 s timeout, main 2026-10-31) let the lease expire before the winner's driver first renewed it; a losing Resume on the
	// other engine then took the expired lease, found the answer recorded, released it, and the winner's driver stopped on its
	// lost lease with nothing here to recover the run (the app runs the recovery watch; launch_race_test.go covers stalls past
	// the lease). This test is about one Resume proceeding, so its lease outlasts any stall.
	e1 := openEnvLease(t, dir, 15*time.Second)
	e2 := openEnvLease(t, dir, 15*time.Second) // a second app instance on the same database
	for _, e := range []*env{e1, e2} {
		if err := e.eng.Register(approvalGraph(dir)); err != nil {
			t.Fatal(err)
		}
	}
	run, err := e1.eng.Start(context.Background(), "approval", "go", wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e1.eng, run, wf.StatusWaitingHuman)
	time.Sleep(50 * time.Millisecond) // driver releases its lease on exit
	pend, _ := e1.eng.Pending(context.Background(), run)

	var ok, rejected int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		eng := e1.eng
		if i%2 == 1 {
			eng = e2.eng
		}
		go func(eng *wf.Engine) {
			defer wg.Done()
			err := eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "approved"})
			switch {
			case err == nil:
				atomic.AddInt32(&ok, 1)
			case errors.Is(err, wf.ErrLeaseHeld), errors.Is(err, wf.ErrRequestConsumed), errors.Is(err, wf.ErrRunTerminal):
				atomic.AddInt32(&rejected, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(eng)
	}
	wg.Wait()
	if ok != 1 || rejected != 7 {
		t.Fatalf("exactly one Resume must proceed: ok=%d rejected=%d", ok, rejected)
	}
	waitStatus(t, e1.eng, run, wf.StatusCompleted)
	if got := effects(dir); got["B"] != 1 {
		t.Fatalf("B ran %d times", got["B"])
	}
}

// slowPauseBus is the event bus of a busy machine: recording that a run paused takes longer than a moment.
type slowPauseBus struct {
	events.Bus
	delay time.Duration
}

func (b slowPauseBus) Publish(ctx context.Context, ev events.Event) (uint64, error) {
	if ev.Type == "workflow.paused" {
		time.Sleep(b.delay)
	}
	return b.Bus.Publish(ctx, ev)
}

// A human may answer the moment a run parks. Resume waits for the parking driver to finish winding down, however slow the
// machine, instead of refusing the answer with ErrLeaseHeld (it gave up after about 2 seconds, which a loaded Windows runner
// can exceed; the delay here is well past that).
func TestResumeWaitsForADriverStillParking(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 4096)
	eng := wf.New(wf.Config{DB: db, Bus: slowPauseBus{Bus: bus, delay: 5 * time.Second}, DataDir: dir, LeaseTTL: leaseTTL})
	t.Cleanup(func() { eng.Close(); bus.Close(); db.Close() })
	if err := eng.Register(approvalGraph(dir)); err != nil {
		t.Fatal(err)
	}
	run, err := eng.Start(ctx, "approval", "go", wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, eng, run, wf.StatusWaitingHuman) // written before the slow "paused" event: the driver is still winding down
	pend, err := eng.Pending(ctx, run)
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending %+v %v", pend, err)
	}
	if err := eng.Resume(ctx, run, wf.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:test"}); err != nil {
		t.Fatalf("Resume right after the run parked: %v", err)
	}
	waitStatus(t, eng, run, wf.StatusCompleted)
	if got := effects(dir); got["A"] != 1 || got["B"] != 1 {
		t.Fatalf("effects %v, want A and B once each", got)
	}
}

func TestTimeTravelCreatesBranchWithoutAlteringHistory(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	g := wf.Sequential("seq", wf.AgentTask("A", eff(dir, "A", "")), wf.AgentTask("B", eff(dir, "B", "")), wf.AgentTask("C", eff(dir, "C", "")))
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "seq", "in", wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusCompleted)

	ctx := context.Background()
	before, _ := e.eng.RunCheckpoints(ctx, run)
	snap, _ := json.Marshal(before)
	if len(before) != 5 { // entry + 3 steps + completion
		t.Fatalf("expected 5 checkpoints, got %d", len(before))
	}
	afterA := before[1] // state after step 1 (A done)
	branch, err := e.eng.ResumeFrom(ctx, afterA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if branch == run {
		t.Fatal("time travel must create a new run")
	}
	waitStatus(t, e.eng, branch, wf.StatusCompleted)

	again, _ := e.eng.RunCheckpoints(ctx, run)
	snap2, _ := json.Marshal(again)
	if string(snap) != string(snap2) {
		t.Fatal("original lineage was modified")
	}
	lin, _ := e.eng.Checkpoints(ctx, branch)
	if lin[0].RunID != run || lin[0].Kind != wf.CheckpointEntry {
		t.Fatalf("branch lineage should start in the original run, got %+v", lin[0])
	}
	// B and C ran again in the branch (A did not): A=1, B=2, C=2
	got := effects(dir)
	if got["A"] != 1 || got["B"] != 2 || got["C"] != 2 {
		t.Fatalf("effects %v", got)
	}
}

func TestReplayFromEntryReproducesNodeSequence(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	g := wf.NewGraph("diamond").
		Add(wf.AgentTask("S", eff(dir, "S", "")), wf.AgentTask("L", eff(dir, "L", "")), wf.AgentTask("R", eff(dir, "R", "")), wf.AgentTask("J", eff(dir, "J", "")).Join("L", "R")).
		Edge("S", "L").Edge("S", "R").Edge("L", "J").Edge("R", "J").Start("S")
	e.eng.Register(g)
	ctx := context.Background()
	run, _ := e.eng.Start(ctx, "diamond", "in", wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	replay, err := e.eng.Replay(ctx, run, "", "human:test")
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, replay, wf.StatusCompleted)
	seq := func(r wf.RunID) string {
		rows, _ := e.eng.Executions(ctx, r)
		var s []string
		for _, x := range rows {
			s = append(s, fmt.Sprintf("%d%s", x.Step, x.NodeID))
		}
		return strings.Join(s, ",")
	}
	if a, b := seq(run), seq(replay); a != b || a != "1S,2L,2R,3J" {
		t.Fatalf("node sequences differ: %q vs %q", a, b)
	}
}

func TestStreamingReachesSubscribersBeforeCompletion(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	release := make(chan struct{})
	var downstream atomic.Value
	g := wf.NewGraph("s").Add(
		wf.AgentTask("W", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			for i := 0; i < 3; i++ {
				in.Emit(wf.Chunk{Kind: "text", Data: val(fmt.Sprintf("c%d", i))})
			}
			<-release
			return wf.NodeOutput{Value: val("c0c1c2")}, nil
		}),
		wf.AgentTask("D", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			downstream.Store(string(in.Inbox[0].Value))
			return wf.NodeOutput{Value: in.Inbox[0].Value}, nil
		}),
	).Edge("W", "D").Start("W")
	e.eng.Register(g)
	ch := make(chan wf.Chunk, 16)
	e.eng.Observe(func(wf.NodeInfo) wf.StreamObserver { return chunkTap(ch) })
	run, _ := e.eng.Start(context.Background(), "s", "in", wf.StartOptions{})
	for i := 0; i < 3; i++ {
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatal("chunk not delivered while the node was still running")
		}
	}
	info, _ := e.eng.Status(context.Background(), run)
	if info.Status != wf.StatusRunning {
		t.Fatalf("run should still be running, got %s", info.Status)
	}
	close(release)
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	if downstream.Load() != `"c0c1c2"` {
		t.Fatalf("downstream received %v, want the complete value", downstream.Load())
	}
	// the chunks were not stored as events a second time (the stream file and the session's agent.output hold them)
	var n int
	if err := e.db.Reader().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM events WHERE type='workflow.node_chunk'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d chunk events stored (%v)", n, err)
	}
}

// chunkTap is a stream observer that hands every chunk to ch.
type chunkTap chan wf.Chunk

func (c chunkTap) OnStart(context.Context, wf.NodeInfo)                  {}
func (c chunkTap) OnChunk(_ context.Context, _ wf.NodeInfo, ch wf.Chunk) { c <- ch }
func (c chunkTap) OnEnd(context.Context, wf.NodeInfo, wf.NodeOutput)     {}
func (c chunkTap) OnError(context.Context, wf.NodeInfo, error)           {}

func TestCrashMidStreamLosesNoDurableOutput(t *testing.T) {
	dir := t.TempDir()
	child, run := startChild(t, "stream", dir)
	// wait until the child emitted its chunks, then give the periodic flusher time to make them durable
	deadline := time.Now().Add(20 * time.Second)
	for effects(dir)["chunks_emitted"] == 0 {
		if time.Now().After(deadline) {
			t.Fatal("child never emitted chunks")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)
	kill(t, child)

	e := openEnv(t, dir)
	e.eng.Register(streamGraph(dir, false))
	if n, err := e.eng.Recover(context.Background()); err != nil || n != 1 {
		t.Fatalf("recover n=%d err=%v", n, err)
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	if got := effects(dir)["retry_saw_5_chunks"]; got != 1 {
		t.Fatalf("retry attempt should see all 5 durable chunks via Previous.PartialRef: %v", effects(dir))
	}
}

func TestCancellation(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	started := make(chan struct{})
	sawCancel := make(chan struct{})
	g := wf.NewGraph("c").Add(wf.AgentTask("W", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		close(started)
		<-ctx.Done()
		close(sawCancel)
		return wf.NodeOutput{}, ctx.Err()
	})).Start("W")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "c", nil, wf.StartOptions{})
	<-started
	if err := e.eng.Cancel(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sawCancel:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never saw cancellation")
	}
	waitStatus(t, e.eng, run, wf.StatusCancelled)
}

func TestLoopLimitRoutesToHumanWhenExhausted(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	g := wf.NewGraph("loop").
		Add(wf.AgentTask("work", eff(dir, "work", "")), wf.Check("review", eff(dir, "review", "revise")),
			wf.Request("human", wf.RequestSpec{Kind: "question", Title: "iteration limit reached"})).
		Edge("work", "review").Loop("review", "work", "revise", 3, "human").Start("work")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "loop", "in", wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	got := effects(dir)
	if got["work"] != 4 || got["review"] != 4 { // initial + 3 loops
		t.Fatalf("loop executed work=%d review=%d, want 4/4", got["work"], got["review"])
	}
	pend, _ := e.eng.Pending(context.Background(), run)
	if len(pend) != 1 || pend[0].Spec.Title != "iteration limit reached" {
		t.Fatalf("pending %+v", pend)
	}
}

func TestLoopWithoutExhaustTargetFailsRun(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	g := wf.NewGraph("loopfail").Add(wf.AgentTask("a", eff(dir, "a", "again"))).Loop("a", "a", "again", 2, "").Start("a")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "loopfail", nil, wf.StartOptions{})
	info := waitStatus(t, e.eng, run, wf.StatusFailed)
	if !strings.Contains(info.FailedReason, "loop iteration limit") {
		t.Fatalf("reason %q", info.FailedReason)
	}
}

func TestRetryRecordsAttempts(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	var n int32
	flaky := wf.AgentTask("f", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		if atomic.AddInt32(&n, 1) == 1 {
			return wf.NodeOutput{}, errors.New("transient")
		}
		if in.Previous == nil || in.Previous.Error != "transient" {
			return wf.NodeOutput{}, errors.New("retry did not receive Previous")
		}
		return wf.NodeOutput{Value: val("ok")}, nil
	}).Retries(2)
	e.eng.Register(wf.NewGraph("r").Add(flaky).Start("f"))
	run, _ := e.eng.Start(context.Background(), "r", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	rows, _ := e.eng.Executions(context.Background(), run)
	if len(rows) != 2 || rows[0].Status != "failed" || rows[1].Status != "done" {
		t.Fatalf("rows %+v", rows)
	}
}

func TestNodeFailureFailsRunWithReason(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	e.eng.Register(wf.NewGraph("f").Add(wf.AgentTask("x", func(context.Context, wf.NodeInput) (wf.NodeOutput, error) {
		return wf.NodeOutput{}, errors.New("boom")
	})).Start("x"))
	run, _ := e.eng.Start(context.Background(), "f", nil, wf.StartOptions{})
	info := waitStatus(t, e.eng, run, wf.StatusFailed)
	if !strings.Contains(info.FailedReason, "boom") {
		t.Fatalf("reason %q", info.FailedReason)
	}
}

func TestMiddlewareOrderAndObserver(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	var mu sync.Mutex
	var order []string
	mk := func(name string) wf.Middleware {
		return func(next wf.NodeHandler) wf.NodeHandler {
			return func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
				mu.Lock()
				order = append(order, name+">")
				mu.Unlock()
				out, err := next(ctx, in)
				mu.Lock()
				order = append(order, "<"+name)
				mu.Unlock()
				return out, err
			}
		}
	}
	e.eng.Use(wf.KindAgentTask, mk("outer"), mk("inner"))
	obs := &recObs{}
	e.eng.Observe(func(n wf.NodeInfo) wf.StreamObserver { return obs })
	e.eng.Register(wf.NewGraph("m").Add(wf.AgentTask("x", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		in.Emit(wf.Chunk{Kind: "text"})
		return wf.NodeOutput{Value: val("v")}, nil
	})).Start("x"))
	run, _ := e.eng.Start(context.Background(), "m", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	if strings.Join(order, "") != "outer>inner><inner<outer" {
		t.Fatalf("middleware order %v", order)
	}
	if obs.starts != 1 || obs.chunks != 1 || obs.ends != 1 || obs.errs != 0 {
		t.Fatalf("observer %+v", obs)
	}
}

type recObs struct{ starts, chunks, ends, errs int }

func (r *recObs) OnStart(context.Context, wf.NodeInfo)              { r.starts++ }
func (r *recObs) OnChunk(context.Context, wf.NodeInfo, wf.Chunk)    { r.chunks++ }
func (r *recObs) OnEnd(context.Context, wf.NodeInfo, wf.NodeOutput) { r.ends++ }
func (r *recObs) OnError(context.Context, wf.NodeInfo, error)       { r.errs++ }

func TestTopologyMismatchRefusesResume(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	e.eng.Register(approvalGraph(dir))
	run, _ := e.eng.Start(context.Background(), "approval", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	pend, _ := e.eng.Pending(context.Background(), run)

	e2 := openEnv(t, dir)
	changed := wf.NewGraph("approval").Add(wf.AgentTask("A", eff(dir, "A", "")), wf.Request("R", wf.RequestSpec{Kind: "approval", Title: "x"})).Edge("A", "R").Start("A")
	e2.eng.Register(changed)
	time.Sleep(50 * time.Millisecond)
	err := e2.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "approved"})
	if !errors.Is(err, wf.ErrTopologyMismatch) {
		t.Fatalf("want ErrTopologyMismatch, got %v", err)
	}
}

func TestRejectedRequestRoutesByDecision(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	g := wf.NewGraph("rej").
		Add(wf.AgentTask("A", eff(dir, "A", "")), wf.Request("R", wf.RequestSpec{Kind: "review", Title: "spec"}), wf.AgentTask("ok", eff(dir, "ok", "")), wf.AgentTask("redo", eff(dir, "redo", ""))).
		Edge("A", "R").EdgeWhen("R", "ok", "approved").EdgeWhen("R", "redo", "rejected").Start("A")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "rej", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	pend, _ := e.eng.Pending(context.Background(), run)
	time.Sleep(50 * time.Millisecond)
	if err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "rejected"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	got := effects(dir)
	if got["redo"] != 1 || got["ok"] != 0 {
		t.Fatalf("effects %v", got)
	}
	apps, _ := e.eng.Approvals(context.Background(), "REJECTED")
	if len(apps) != 1 {
		t.Fatalf("approvals %v", apps)
	}
}

// A request's phase is stored with its approvals row, so the inbox tells a spec gate from a review and merge without its title.
func TestARequestsPhaseIsStoredWithIt(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	g := wf.NewGraph("phase").
		Add(wf.AgentTask("A", eff(dir, "A", "")), wf.Request("R", wf.RequestSpec{Kind: "review", Phase: "spec", Title: "Accept it"})).
		Edge("A", "R").Start("A")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "phase", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	pend, _ := e.eng.Pending(context.Background(), run)
	var phase string
	if err := e.db.Reader().QueryRow(`SELECT phase FROM approvals WHERE id=?`, string(pend[0].ID)).Scan(&phase); err != nil || phase != "spec" || pend[0].Spec.Phase != "spec" {
		t.Fatalf("phase %q (%v), pending %+v", phase, err, pend[0].Spec)
	}
}

func TestGraphValidation(t *testing.T) {
	bad := []*wf.Graph{
		wf.NewGraph("x").Add(wf.AgentTask("a", nil)).Start("a"),
		wf.NewGraph("x").Add(wf.AgentTask("a", eff("", "", ""))),
		wf.NewGraph("x").Add(wf.AgentTask("a", eff("", "", ""))).Edge("a", "ghost").Start("a"),
		wf.NewGraph("x").Add(wf.AgentTask("a", eff("", "", ""))).Loop("a", "a", "", 0, "").Start("a"),
	}
	for i, g := range bad {
		if err := g.Validate(); err == nil {
			t.Errorf("graph %d should be invalid", i)
		}
	}
}

// A request with Retry parks the node; the human's answer makes the node run AGAIN with the inbox it had (the owed work is
// not skipped), exactly once, and its successor runs once. Without Retry the answer would have been the node's output.
func TestRetryRequestRerunsTheNodeAfterTheAnswer(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	var calls atomic.Int32
	var sawInbox atomic.Value
	gate := func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		n := calls.Add(1)
		var v string
		if len(in.Inbox) > 0 {
			_ = json.Unmarshal(in.Inbox[0].Value, &v)
		}
		sawInbox.Store(fmt.Sprintf("%d:%s", n, v))
		if n == 1 {
			return wf.NodeOutput{Request: &wf.RequestSpec{Kind: "approval", Title: "gate", Retry: true}}, nil
		}
		effect(dir, "turn")
		return wf.NodeOutput{Value: val("turn done"), Route: "done"}, nil
	}
	g := wf.NewGraph("retry-req").
		Add(wf.Route("start", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			return wf.NodeOutput{Value: val("payload")}, nil
		}), wf.AgentTask("turn", gate), wf.Route("after", eff(dir, "after", ""))).
		Edge("start", "turn").EdgeWhen("turn", "after", "done").Start("start")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "retry-req", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	pend, _ := e.eng.Pending(context.Background(), run)
	if len(pend) != 1 || !pend[0].Spec.Retry || len(pend[0].Inbox) != 1 {
		t.Fatalf("pending %+v", pend)
	}
	if got := effects(dir); got["turn"] != 0 || got["after"] != 0 {
		t.Fatalf("nothing downstream may run while parked: %v", got)
	}
	time.Sleep(50 * time.Millisecond)
	if err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	got := effects(dir)
	if got["turn"] != 1 || got["after"] != 1 || calls.Load() != 2 {
		t.Fatalf("effects %v calls %d", got, calls.Load())
	}
	if s := sawInbox.Load().(string); s != "2:payload" {
		t.Fatalf("the rerun must see the original inbox, saw %q", s)
	}
	if err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "approved"}); err == nil {
		t.Fatal("a second answer to the same request must be refused")
	}
}

// An agent turn cut off by a stop runs again once on its own; cut off a second time, the run waits for a human instead of
// paying for the turn at every start, and the answer decides: approved runs it once more, rejected fails the run.
func TestAnAgentTurnCutOffTwiceWaitsForAHuman(t *testing.T) {
	for _, decision := range []string{"approved", "rejected"} {
		t.Run(decision, func(t *testing.T) {
			dir := t.TempDir()
			waitStarts := func(n int) {
				deadline := time.Now().Add(20 * time.Second)
				for effects(dir)["W_start"] < n {
					if time.Now().After(deadline) {
						t.Fatalf("W started %d times, want %d", effects(dir)["W_start"], n)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			child, run := startChild(t, "interrupt", dir)
			waitStarts(1)
			kill(t, child)
			again, _ := startChild(t, "interrupt-again", dir) // the first re-run is automatic
			waitStarts(2)
			kill(t, again)

			e := openEnv(t, dir)
			if err := e.eng.Register(interruptGraph(dir, false)); err != nil {
				t.Fatal(err)
			}
			if n, err := e.eng.Recover(context.Background()); err != nil || n != 1 {
				t.Fatalf("recover n=%d err=%v", n, err)
			}
			waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
			if got := effects(dir)["W_start"]; got != 2 {
				t.Fatalf("the turn ran a third time without a human: %d starts", got)
			}
			pend, err := e.eng.Pending(context.Background(), run)
			if err != nil || len(pend) != 1 || pend[0].Spec.Kind != "approval" || !strings.Contains(pend[0].Spec.Title, "cut off 2 times") ||
				!strings.Contains(string(pend[0].Spec.Payload), `"gate":"interrupted"`) {
				t.Fatalf("pending %+v %v", pend, err)
			}
			if err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: decision, By: "human:test"}); err != nil {
				t.Fatal(err)
			}
			if decision == "approved" {
				waitStatus(t, e.eng, run, wf.StatusCompleted)
				if got := effects(dir); got["W_start"] != 3 || got["W_done"] != 1 {
					t.Fatalf("approved: %v", got)
				}
				return
			}
			info := waitStatus(t, e.eng, run, wf.StatusFailed)
			if !strings.Contains(info.FailedReason, "chose not to run it again") || effects(dir)["W_start"] != 2 {
				t.Fatalf("rejected: %q %v", info.FailedReason, effects(dir))
			}
		})
	}
}

// The same when the app is closed normally while the turn runs (the usual case: the window is closed), not killed: an
// attempt whose driver was stopped is cut off, not failed, and counts the same way.
func TestAnAgentTurnStoppedTwiceByClosingTheAppWaitsForAHuman(t *testing.T) {
	dir := t.TempDir()
	starts := func() int { return effects(dir)["W_start"] }
	graph := func(block bool) *wf.Graph {
		return wf.NewGraph("interrupt").Add(wf.AgentTask("W", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			effect(dir, "W_start")
			if block {
				<-ctx.Done() // the app is closed while the turn runs
				return wf.NodeOutput{}, ctx.Err()
			}
			effect(dir, "W_done")
			return wf.NodeOutput{Value: val("W")}, nil
		})).Start("W")
	}
	var run wf.RunID
	for i := 1; i <= 2; i++ {
		db, err := storage.OpenTemp(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		bus := events.NewSQLBus(db, 4096)
		eng := wf.New(wf.Config{DB: db, Bus: bus, DataDir: dir, LeaseTTL: leaseTTL})
		if err := eng.Register(graph(true)); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if run, err = eng.Start(context.Background(), "interrupt", nil, wf.StartOptions{}); err != nil {
				t.Fatal(err)
			}
		} else if n, err := eng.Recover(context.Background()); err != nil || n != 1 {
			t.Fatalf("recover n=%d err=%v", n, err) // the first re-run is automatic
		}
		deadline := time.Now().Add(20 * time.Second)
		for starts() < i {
			if time.Now().After(deadline) {
				t.Fatalf("W started %d times, want %d", starts(), i)
			}
			time.Sleep(10 * time.Millisecond)
		}
		eng.Close()
		bus.Close()
		db.Close()
	}
	e := openEnv(t, dir)
	if err := e.eng.Register(graph(false)); err != nil {
		t.Fatal(err)
	}
	if n, err := e.eng.Recover(context.Background()); err != nil || n != 1 {
		t.Fatalf("recover n=%d err=%v", n, err)
	}
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	if got := starts(); got != 2 {
		t.Fatalf("the turn ran a third time without a human: %d starts", got)
	}
	rows, _ := e.eng.Executions(context.Background(), run)
	for _, r := range rows {
		if r.Status != "abandoned" {
			t.Fatalf("a turn cut off by closing the app is recorded as %q: %+v", r.Status, rows)
		}
	}
	pend, _ := e.eng.Pending(context.Background(), run)
	if len(pend) != 1 || !strings.Contains(pend[0].Spec.Title, "cut off 2 times") {
		t.Fatalf("pending %+v", pend)
	}
}

// Killed and started again at once, within the dead owner's lease: Recover at start cannot take the run, and the recovery
// watch takes it over as soon as the lease expires (before, the run stayed RUNNING with nothing driving it).
func TestARunIsRecoveredWhenTheAppRestartsWithinTheLease(t *testing.T) {
	dir := t.TempDir()
	child, run := startChild(t, "partial", dir)
	probe := openEnv(t, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for {
		rows, _ := probe.eng.Executions(ctx, run)
		started, aDone := false, false
		for _, r := range rows {
			if r.NodeID == "B" && r.Status == "started" {
				started = true
			}
			// A runs beside B; its finish must be on disk before the crash, or recovery rightly runs it again (a slow disk
			// on the Windows runner showed that)
			if r.NodeID == "A" && r.Status == "done" {
				aDone = true
			}
		}
		if started && aDone && effects(dir)["B_start"] == 1 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("timed out waiting for B to start and A to finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait() // no wait for the lease: the app starts again immediately

	e := openEnv(t, dir)
	if err := e.eng.Register(partialGraph(dir, false)); err != nil {
		t.Fatal(err)
	}
	if n, err := e.eng.Recover(context.Background()); err != nil || n != 0 {
		t.Fatalf("the dead owner's lease is still valid, recover took n=%d err=%v", n, err)
	}
	wctx, stop := context.WithCancel(context.Background())
	defer stop()
	go e.eng.WatchRecover(wctx, 50*time.Millisecond)
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	if got := effects(dir); got["A"] != 1 || got["B"] != 1 || got["J"] != 1 {
		t.Fatalf("finished node re-ran or unfinished node ran !=1 times: %v", got)
	}
}

// Cancel and a step finishing race (a person clicks Cancel while an approval they answered starts the run again, or another
// engine cancels a run this one drives): the cancelled run stays cancelled. The step that was running is not recorded and
// nothing after it runs.
func TestACancelledRunIsNeverRevivedByItsDriver(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	started, release := make(chan struct{}), make(chan struct{})
	g := wf.NewGraph("race").Add(
		wf.Check("A", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			close(started)
			<-release // a step that does not watch its context (a check command finishing)
			effect(dir, "A")
			return wf.NodeOutput{Value: val("A")}, nil
		}),
		wf.Check("B", eff(dir, "B", ""))).Edge("A", "B").Start("A")
	e.eng.Register(g)
	run, err := e.eng.Start(context.Background(), "race", nil, wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	// a second engine on the same database has no driver for the run: its Cancel only writes the status
	other := wf.New(wf.Config{DB: e.db, Bus: e.bus, DataDir: dir, LeaseTTL: leaseTTL, Owner: "other"})
	other.Register(g)
	defer other.Close()
	if err := other.Cancel(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	close(release)
	time.Sleep(300 * time.Millisecond) // the driver commits (or would) right after A returns
	info, _ := e.eng.Status(context.Background(), run)
	if info.Status != wf.StatusCancelled {
		t.Fatalf("the driver wrote %s over CANCELLED", info.Status)
	}
	if got := effects(dir); got["B"] != 0 {
		t.Fatalf("the step after the cancel ran: %v", got)
	}
	if err := e.eng.Cancel(context.Background(), run); !errors.Is(err, wf.ErrRunTerminal) {
		t.Fatalf("cancelling an ended run: %v", err)
	}
}

// A turn that finished as the app was closing is recorded as done (with a context that outlives the driver's), so the next
// start reuses it instead of paying for it again.
func TestATurnThatFinishedAsTheAppClosedIsNotRunAgain(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	started := make(chan struct{})
	g := wf.NewGraph("late").Add(
		wf.AgentTask("W", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			effect(dir, "W")
			close(started)
			<-ctx.Done() // the app is closing; the turn's answer arrives anyway
			return wf.NodeOutput{Value: val("W")}, nil
		}),
		wf.Check("after", eff(dir, "after", ""))).Edge("W", "after").Start("W")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "late", nil, wf.StartOptions{})
	<-started
	e.eng.Close()

	e2 := wf.New(wf.Config{DB: e.db, Bus: e.bus, DataDir: dir, LeaseTTL: leaseTTL})
	g2 := wf.NewGraph("late").Add(
		wf.AgentTask("W", eff(dir, "W", "")),
		wf.Check("after", eff(dir, "after", ""))).Edge("W", "after").Start("W")
	e2.Register(g2)
	defer e2.Close()
	time.Sleep(2 * leaseTTL)
	if _, err := e2.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e2, run, wf.StatusCompleted)
	if got := effects(dir); got["W"] != 1 || got["after"] != 1 {
		t.Fatalf("effects %v: the finished turn was run again", got)
	}
}

func TestReadRefReadsOnlyExternalizedOutputs(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "runs", "run_1", "work", "step-3", "output.json")
	os.MkdirAll(filepath.Dir(good), 0o755)
	os.WriteFile(good, []byte(`"big"`), 0o644)
	secret := filepath.Join(dir, "secret.txt")
	os.WriteFile(secret, []byte("token"), 0o644)
	ref := func(p string) json.RawMessage { b, _ := json.Marshal(map[string]any{"$ref": p}); return b }
	if v, err := wf.ReadRef(ref(good)); err != nil || string(v) != `"big"` {
		t.Fatalf("an externalized output: %s %v", v, err)
	}
	for _, p := range []string{secret, filepath.Join(dir, "runs", "r", "n", "step-1", "..", "..", "..", "..", "secret.txt"), "output.json", filepath.Join(dir, "runs", "r", "n", "x", "output.json")} {
		if v, err := wf.ReadRef(ref(p)); err == nil || strings.Contains(string(v), "token") {
			t.Fatalf("%s was read: %s", p, v)
		}
	}
	if v, err := wf.ReadRef(val("plain")); err != nil || string(v) != `"plain"` {
		t.Fatalf("a plain value: %s %v", v, err)
	}
}

// A step that is answered with something none of its edges takes does not end the run as if it had finished (audit 2026-10-16,
// L14): the answer is refused, the request stays open, and the right answer still works. A rejection may end a run that has no
// edge for it, and a step with no edges at all is the end of the workflow whatever it was answered with.
func TestAnAnswerNoEdgeTakesIsRefusedAndTheRequestStaysOpen(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	g := wf.NewGraph("noroute").
		Add(wf.AgentTask("A", eff(dir, "A", "")), wf.Request("R", wf.RequestSpec{Kind: "review", Title: "spec"}), wf.AgentTask("ok", eff(dir, "ok", "")), wf.AgentTask("redo", eff(dir, "redo", ""))).
		Edge("A", "R").EdgeWhen("R", "ok", "approved").EdgeWhen("R", "redo", "rejected").Start("A")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "noroute", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	pend, _ := e.eng.Pending(context.Background(), run)
	time.Sleep(50 * time.Millisecond)

	err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "answered"})
	if !errors.Is(err, wf.ErrNoRoute) || !strings.Contains(err.Error(), "approved") || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("an answer no edge takes: %v", err)
	}
	info, _ := e.eng.Status(context.Background(), run)
	if info.Status != wf.StatusWaitingHuman {
		t.Fatalf("the run went on: %s", info.Status)
	}
	if open, _ := e.eng.Approvals(context.Background(), "REQUESTED"); len(open) != 1 {
		t.Fatalf("the request was closed: %v", open)
	}
	if err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "approved"}); err != nil {
		t.Fatalf("the right answer after the wrong one: %v", err)
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	if got := effects(dir); got["ok"] != 1 || got["redo"] != 0 {
		t.Fatalf("effects %v", got)
	}

	// only an approval has an edge: a rejection ends the run (a plan turned down has nothing to go on to)
	g2 := wf.NewGraph("rejend").
		Add(wf.AgentTask("A", eff(dir, "A2", "")), wf.Request("R", wf.RequestSpec{Kind: "review", Title: "plan"}), wf.AgentTask("ok", eff(dir, "ok2", ""))).
		Edge("A", "R").EdgeWhen("R", "ok", "approved").Start("A")
	e.eng.Register(g2)
	run2, _ := e.eng.Start(context.Background(), "rejend", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run2, wf.StatusWaitingHuman)
	pend2, _ := e.eng.Pending(context.Background(), run2)
	time.Sleep(50 * time.Millisecond)
	if err := e.eng.Resume(context.Background(), run2, wf.Response{RequestID: pend2[0].ID, Decision: "rejected"}); err != nil {
		t.Fatalf("a rejection with no edge of its own: %v", err)
	}
	waitStatus(t, e.eng, run2, wf.StatusCompleted)
	if got := effects(dir); got["ok2"] != 0 {
		t.Fatalf("a rejection went on: %v", got)
	}

	// a request that ends the workflow has no edges: any answer ends it
	g3 := wf.NewGraph("sinkreq").
		Add(wf.AgentTask("A", eff(dir, "A3", "")), wf.Request("R", wf.RequestSpec{Kind: "question", Title: "last word"})).
		Edge("A", "R").Start("A")
	e.eng.Register(g3)
	run3, _ := e.eng.Start(context.Background(), "sinkreq", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run3, wf.StatusWaitingHuman)
	pend3, _ := e.eng.Pending(context.Background(), run3)
	time.Sleep(50 * time.Millisecond)
	if err := e.eng.Resume(context.Background(), run3, wf.Response{RequestID: pend3[0].ID, Decision: "answered"}); err != nil {
		t.Fatalf("an answer at the end of the workflow: %v", err)
	}
	waitStatus(t, e.eng, run3, wf.StatusCompleted)
}

// A run cancelled while an answer is being recorded stays cancelled, and the answer is not recorded for a run that will never
// read it (L14). The cancellation is made by a trigger that fires in the answer's own transaction, between the early look at
// the run's status and the write that would have set it RUNNING.
func TestAnAnswerDoesNotReviveARunCancelledWhileItWasRecorded(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	g := wf.NewGraph("revive").
		Add(wf.AgentTask("A", eff(dir, "A", "")), wf.Request("R", wf.RequestSpec{Kind: "review", Title: "spec"}), wf.AgentTask("ok", eff(dir, "ok", ""))).
		Edge("A", "R").EdgeWhen("R", "ok", "approved").Start("A")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "revive", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	pend, _ := e.eng.Pending(context.Background(), run)
	time.Sleep(50 * time.Millisecond)
	if err := e.db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER cancel_on_answer AFTER UPDATE ON approvals BEGIN UPDATE workflow_runs SET status='CANCELLED' WHERE id=NEW.run_id; END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "approved"})
	if !errors.Is(err, wf.ErrRunTerminal) {
		t.Fatalf("an answer for a run that was cancelled meanwhile: %v", err)
	}
	if open, _ := e.eng.Approvals(context.Background(), "REQUESTED"); len(open) != 1 {
		t.Fatalf("the answer was recorded for a run that cannot read it: %v", open)
	}
	if got := effects(dir); got["ok"] != 0 {
		t.Fatalf("the step after the answer ran: %v", got)
	}
}

// A run that was suspended and is cancelled before it is resumed is not brought back (L14).
func TestASuspendedRunThatWasCancelledIsNotResumed(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	g := wf.NewGraph("susp").Add(
		wf.Check("A", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			once.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return wf.NodeOutput{}, ctx.Err()
			}
			effect(dir, "A")
			return wf.NodeOutput{Value: val("A")}, nil
		}),
		wf.Check("B", eff(dir, "B", ""))).Edge("A", "B").Start("A")
	e.eng.Register(g)
	run, err := e.eng.Start(context.Background(), "susp", nil, wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if parked, err := e.eng.Suspend(context.Background(), run, "the licence expired"); err != nil || !parked {
		t.Fatalf("suspend: %v %v", parked, err)
	}
	// the run is cancelled after it was read for resuming and before it is brought back: the lease is taken in between, and a
	// trigger on that write cancels the run
	if err := e.db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		for _, ddl := range []string{
			`CREATE TRIGGER cancel_on_lease AFTER INSERT ON run_leases BEGIN UPDATE workflow_runs SET status='CANCELLED' WHERE id=NEW.run_id AND status='PAUSED'; END`,
			`CREATE TRIGGER cancel_on_relet AFTER UPDATE ON run_leases BEGIN UPDATE workflow_runs SET status='CANCELLED' WHERE id=NEW.run_id AND status='PAUSED'; END`,
		} {
			if _, err := tx.Exec(ddl); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	n, err := e.eng.ResumeSuspended(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("a cancelled run was resumed: %d %v", n, err)
	}
	time.Sleep(200 * time.Millisecond)
	info, _ := e.eng.Status(context.Background(), run)
	if info.Status != wf.StatusCancelled {
		t.Fatalf("the run is %s", info.Status)
	}
	if got := effects(dir); got["B"] != 0 {
		t.Fatalf("the cancelled run went on: %v", got)
	}
}

// A run that failed is marked failed even when the write fails the first times (L14): a failed write left the run RUNNING with
// nobody driving it, and its next owner ran the failing step again. A trigger refuses the status write while a marker row exists;
// the marker goes away after a moment, and the step must still have run exactly once.
func TestAFailedRunIsMarkedFailedEvenWhenTheFirstWritesFail(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	if err := e.db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		for _, ddl := range []string{
			`CREATE TABLE hold_failed(x INTEGER)`,
			`INSERT INTO hold_failed VALUES(1)`,
			`CREATE TRIGGER refuse_failed BEFORE UPDATE ON workflow_runs WHEN NEW.status='FAILED' AND EXISTS(SELECT 1 FROM hold_failed) BEGIN SELECT RAISE(ABORT, 'database is busy'); END`,
		} {
			if _, err := tx.Exec(ddl); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e.eng.Register(wf.NewGraph("failretry").Add(wf.AgentTask("x", func(context.Context, wf.NodeInput) (wf.NodeOutput, error) {
		effect(dir, "x")
		return wf.NodeOutput{}, errors.New("boom")
	})).Start("x"))
	run, _ := e.eng.Start(context.Background(), "failretry", nil, wf.StartOptions{})
	time.Sleep(120 * time.Millisecond) // the first write (and the one 50 ms later) is refused
	if err := e.db.WriteTx(context.Background(), func(tx *sql.Tx) error { _, err := tx.Exec(`DELETE FROM hold_failed`); return err }); err != nil {
		t.Fatal(err)
	}
	info := waitStatus(t, e.eng, run, wf.StatusFailed)
	if !strings.Contains(info.FailedReason, "boom") {
		t.Fatalf("reason %q", info.FailedReason)
	}
	time.Sleep(2 * leaseTTL) // a run left RUNNING would be taken over and its step run again by now
	if got := effects(dir); got["x"] != 1 {
		t.Fatalf("the failing step ran %d times", got["x"])
	}
}
