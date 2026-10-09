package watchdog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/watchdog"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// These tests drive the watchdog through the real engine with a scripted node handler (a labelled test double, not an agent):
// it streams the tool calls an agent would and then waits, or reports what its inbox held.

type rig struct {
	t      *testing.T
	db     *storage.DB
	eng    *workflow.Engine
	wd     *watchdog.Watchdog
	mu     sync.Mutex
	stops  []string
	steers []watchdog.TripRecord
	lead   bool
	steer  watchdog.Steer
	notes  []string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	db, err := storage.OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 256)
	r := &rig{t: t, db: db}
	r.eng = workflow.New(workflow.Config{DB: db, Bus: bus, LeaseTTL: 2 * time.Second})
	t.Cleanup(func() { r.eng.Close(); bus.Close(); db.Close() })
	r.wd = watchdog.New(watchdog.SQLStore{DB: db}, watchdog.Hooks{
		Notify: func(ctx context.Context, typ string, tr watchdog.TripRecord, msg string) {
			r.mu.Lock()
			r.notes = append(r.notes, typ)
			r.mu.Unlock()
		},
		Lead: func(ctx context.Context, n workflow.NodeInfo) bool { return r.lead },
		Supervise: func(ctx context.Context, n workflow.NodeInfo, tr watchdog.TripRecord) (watchdog.Steer, bool) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.steers = append(r.steers, tr)
			return r.steer, true
		},
		Stop: func(ctx context.Context, n workflow.NodeInfo, by string) {
			r.mu.Lock()
			r.stops = append(r.stops, n.TaskID+":"+by)
			r.mu.Unlock()
		},
	}, watchdog.ForNode)
	r.eng.Use(workflow.KindAgentTask, r.wd.Middleware())
	r.eng.Observe(r.wd.Observer())
	r.eng.Use(workflow.KindCheck, r.wd.CheckMiddleware(nil))
	return r
}

func spinChunks(in workflow.NodeInput) {
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("c%d", i)
		in.Emit(workflow.NewChunk(workflow.ChunkToolCall, workflow.ToolCall{ID: id, Name: "Read", Args: json.RawMessage(`{"file_path":"a.go"}`)}))
		in.Emit(workflow.NewChunk(workflow.ChunkToolResult, workflow.ToolResult{ID: id, Name: "Read", Args: json.RawMessage(`{"file_path":"a.go"}`), Output: "same"}))
	}
}

func notices(inbox []workflow.Message) []string {
	var out []string
	for _, m := range inbox {
		var o map[string]any
		if json.Unmarshal(m.Value, &o) == nil {
			if s, ok := o["watchdog"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// spinner trips on its first call per run (tool spin) and blocks until the watchdog stops it; later calls record the
// notices in their inbox and finish.
type spinner struct {
	calls atomic.Int32
	mu    sync.Mutex
	seen  [][]string
	spins int32 // how many calls spin
}

func (s *spinner) handler(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
	n := s.calls.Add(1)
	if n <= s.spins {
		spinChunks(in)
		<-ctx.Done()
		return workflow.NodeOutput{}, context.Cause(ctx)
	}
	s.mu.Lock()
	s.seen = append(s.seen, notices(in.Inbox))
	s.mu.Unlock()
	b, _ := json.Marshal("done")
	return workflow.NodeOutput{Value: b}, nil
}

func (r *rig) run(name string, h workflow.NodeHandler, task string) workflow.RunID {
	g := workflow.NewGraph(name).Add(workflow.AgentTask("work", h).With("agent", "work"), workflow.Route("after", func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{Value: json.RawMessage(`"after"`)}, nil
	})).Edge("work", "after").Start("work")
	if err := r.eng.Register(g); err != nil {
		r.t.Fatal(err)
	}
	id, err := r.eng.Start(context.Background(), name, "go", workflow.StartOptions{ProjectID: "p1", TaskID: task})
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *rig) await(id workflow.RunID, want workflow.RunStatus) workflow.RunInfo {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := r.eng.Await(ctx, id, func(i workflow.RunInfo) bool { return i.Status == want || i.Status.Terminal() })
	if err != nil || info.Status != want {
		r.t.Fatalf("run %s (%s), want %s: %v", info.Status, info.FailedReason, want, err)
	}
	return info
}

func (r *rig) answer(id workflow.RunID, decision, data string) workflow.PendingRequest {
	r.t.Helper()
	time.Sleep(60 * time.Millisecond)
	pend, _ := r.eng.Pending(context.Background(), id)
	if len(pend) != 1 {
		r.t.Fatalf("pending %+v", pend)
	}
	var raw json.RawMessage
	if data != "" {
		raw = json.RawMessage(data)
	}
	if err := r.eng.Resume(context.Background(), id, workflow.Response{RequestID: pend[0].ID, Decision: decision, By: "human:k", Data: raw}); err != nil {
		r.t.Fatal(err)
	}
	return pend[0]
}

func count(t *testing.T, db *storage.DB, task string) int {
	n, err := watchdog.SQLStore{DB: db}.Count(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Item 4: a human's answer to a watchdog pause on work reaches the agent's next turn as readable guidance, and the trip count
// starts again; it is never routed down work's edges as if it were the work's output.
func TestPauseAnswerReachesTheAgentAndResetsTheCount(t *testing.T) {
	r := newRig(t)
	s := &spinner{spins: 0}
	// a previous trip on the task makes the next one a stage-2 pause
	watchdog.SQLStore{DB: r.db}.Record(context.Background(), watchdog.TripRecord{TaskID: "t1", Signal: watchdog.SignalToolSpin, Stage: 1, Evidence: "{}"})
	var calls atomic.Int32
	h := func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		if calls.Add(1) == 1 {
			spinChunks(in)
			<-ctx.Done()
			return workflow.NodeOutput{}, context.Cause(ctx)
		}
		return s.handler(ctx, in)
	}
	id := r.run("wd-answer", h, "t1")
	r.await(id, workflow.StatusWaitingHuman)
	p := r.answer(id, "answered", `{"text":"read b.go instead"}`)
	if !p.Spec.Retry || !p.Spec.DeliverAnswer || !strings.Contains(p.Spec.Title, "Watchdog paused") || !strings.Contains(string(p.Spec.Payload), "Reject to stop") {
		t.Fatalf("pause request %+v %s", p.Spec, p.Spec.Payload)
	}
	info := r.await(id, workflow.StatusCompleted)
	if !strings.Contains(string(info.Result), "after") {
		t.Fatalf("the work went on normally after the guidance: %s", info.Result)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) != 1 || len(s.seen[0]) != 1 || !strings.Contains(s.seen[0][0], "read b.go instead") || strings.Contains(s.seen[0][0], `{"text"`) {
		t.Fatalf("the agent's next turn must see the guidance as text: %q", s.seen)
	}
	if n := count(t, r.db, "t1"); n != 0 {
		t.Fatalf("a human answer resets the trip count, got %d", n)
	}
}

// Item 4: rejecting a watchdog pause stops the task (the Stop hook) and ends the run cleanly: nothing after work runs.
func TestRejectingAPauseStopsTheRunCleanly(t *testing.T) {
	r := newRig(t)
	watchdog.SQLStore{DB: r.db}.Record(context.Background(), watchdog.TripRecord{TaskID: "t2", Signal: watchdog.SignalToolSpin, Stage: 1, Evidence: "{}"})
	s := &spinner{spins: 1}
	id := r.run("wd-reject", s.handler, "t2")
	r.await(id, workflow.StatusWaitingHuman)
	r.answer(id, "rejected", `{}`)
	info := r.await(id, workflow.StatusCompleted)
	if strings.Contains(string(info.Result), "after") {
		t.Fatalf("a rejected pause must not route on: %s", info.Result)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.stops) != 1 || r.stops[0] != "t2:human:k" {
		t.Fatalf("stop hook %v", r.stops)
	}
}

// Part 2: a stage-1 trip with a team lead stops the turn, asks the lead, and restarts the work with the lead's message.
func TestStageOneTripWithALeadRestartsWithTheLeadsMessage(t *testing.T) {
	r := newRig(t)
	r.lead, r.steer = true, watchdog.Steer{Action: "nudge", Message: "Stop reading a.go; the bug is in b.go."}
	s := &spinner{spins: 1}
	id := r.run("wd-lead", s.handler, "t3")
	r.await(id, workflow.StatusCompleted)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) != 1 || len(s.seen[0]) != 1 || !strings.Contains(s.seen[0][0], "Message from the team lead") || !strings.Contains(s.seen[0][0], "the bug is in b.go") {
		t.Fatalf("restart notice %q", s.seen)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.steers) != 1 || r.steers[0].Signal != watchdog.SignalToolSpin || r.steers[0].Stage != 1 {
		t.Fatalf("supervise calls %+v", r.steers)
	}
}

// Part 2: the lead may escalate a stage-1 trip: the node parks on a question carrying the lead's message.
func TestStageOneTripLeadEscalationPausesForTheHuman(t *testing.T) {
	r := newRig(t)
	r.lead, r.steer = true, watchdog.Steer{Action: "escalate", Message: "This needs a product decision about the API."}
	s := &spinner{spins: 1}
	id := r.run("wd-lead-esc", s.handler, "t4")
	r.await(id, workflow.StatusWaitingHuman)
	pend, _ := r.eng.Pending(context.Background(), id)
	if len(pend) != 1 || !strings.Contains(string(pend[0].Spec.Payload), "product decision") || !pend[0].Spec.DeliverAnswer {
		t.Fatalf("pending %+v", pend)
	}
}

// Without a lead the stage-1 behaviour is unchanged (no supervision call).
func TestStageOneTripWithoutALeadIsUnchanged(t *testing.T) {
	r := newRig(t)
	s := &spinner{spins: 1}
	id := r.run("wd-nolead", s.handler, "t5")
	// the scripted handler has no turn controller, so stage 1 cancels the node, as before: the run fails with the trip
	info := r.await(id, workflow.StatusFailed)
	if !strings.Contains(info.FailedReason, "tool_spin") {
		t.Fatalf("reason %q", info.FailedReason)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.steers) != 0 {
		t.Fatalf("no lead, no supervision: %+v", r.steers)
	}
}

// Steer from outside a turn: with no turn running a nudge is queued for the next turn; an escalation parks a running turn.
func TestSteerQueuesOrEscalates(t *testing.T) {
	r := newRig(t)
	if r.wd.Steer(context.Background(), "t6", "Please summarise and stop.", false) {
		t.Fatal("no turn is running")
	}
	s := &spinner{}
	id := r.run("wd-steer", s.handler, "t6")
	r.await(id, workflow.StatusCompleted)
	s.mu.Lock()
	if len(s.seen) != 1 || len(s.seen[0]) != 1 || s.seen[0][0] != "Please summarise and stop." {
		t.Fatalf("queued nudge %q", s.seen)
	}
	s.mu.Unlock()

	started := make(chan struct{})
	block := func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		close(started)
		<-ctx.Done()
		return workflow.NodeOutput{}, context.Cause(ctx)
	}
	id = r.run("wd-steer-esc", block, "t7")
	<-started
	deadline := time.Now().Add(5 * time.Second)
	for !r.wd.Steer(context.Background(), "t7", "The lead needs you: the task is stalled.", true) {
		if time.Now().After(deadline) {
			t.Fatal("the running turn was not found")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.await(id, workflow.StatusWaitingHuman)
	pend, _ := r.eng.Pending(context.Background(), id)
	if len(pend) != 1 || !strings.Contains(pend[0].Spec.Title, "team lead") || !strings.Contains(string(pend[0].Spec.Payload), "stalled") {
		t.Fatalf("pending %+v", pend)
	}
	if n := count(t, r.db, "t7"); n != 0 {
		t.Fatalf("a lead escalation is not a watchdog trip, count %d", n)
	}
}

// Item 5: templates' thresholds are used: work turns run under the Iterate Until Verified profile.
func TestForNodeUsesTheTemplateProfiles(t *testing.T) {
	iuv := watchdog.IterateUntilVerified()
	for _, n := range []workflow.NodeInfo{
		{Workflow: "iterate-until-verified", Meta: map[string]string{"agent": "review"}},
		{Workflow: "feature", Meta: map[string]string{"agent": "work"}},
		{Workflow: "bug-pr", Meta: map[string]string{"agent": "tests"}},
	} {
		if watchdog.ForNode(n) != iuv {
			t.Errorf("%+v: want the IUV profile", n)
		}
	}
	if watchdog.ForNode(workflow.NodeInfo{Workflow: "feature", Meta: map[string]string{"agent": "pm"}}) != watchdog.Defaults() {
		t.Error("a spec turn runs under the defaults")
	}
}

// Reset writes a reset row only when there is a count to reset.
func TestResetWritesARowOnlyWhenNeeded(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.wd.Reset(ctx, "t8")
	var rows int
	r.db.Reader().QueryRow(`SELECT COUNT(*) FROM watchdog_trips WHERE task_id='t8'`).Scan(&rows)
	if rows != 0 {
		t.Fatalf("rows %d", rows)
	}
	watchdog.SQLStore{DB: r.db}.Record(ctx, watchdog.TripRecord{TaskID: "t8", Signal: watchdog.SignalToolCap, Stage: 1, Evidence: "{}"})
	r.wd.Reset(ctx, "t8")
	if n := count(t, r.db, "t8"); n != 0 {
		t.Fatalf("count after reset %d", n)
	}
}
