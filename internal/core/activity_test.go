package core_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/agui"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// P87: a task's activity is what its agents did, turn by turn, in the typed vocabulary, read from what the bus stored.
func TestATasksActivityIsItsAgentsTurnsInTheTypedVocabulary(t *testing.T) {
	a := openScripted(t, nil, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{{Tool: "Read", Input: json.RawMessage(`{"file_path":"calc.go"}`), Result: "package gocalc"},
			{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}},
	})
	p, _ := a.setup("gocalc")
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)

	type ev struct {
		Type     string `json:"type"`
		Metadata struct {
			Drydock struct {
				Seq uint64 `json:"seq"`
			} `json:"drydock"`
		} `json:"metadata"`
		ToolCallName string `json:"toolCallName"`
		ToolCallID   string `json:"toolCallId"`
		Content      string `json:"content"`
		Delta        string `json:"delta"`
		Outcome      struct {
			Type string `json:"type"`
		} `json:"outcome"`
	}
	type thread struct {
		ThreadID string `json:"threadId"`
		AgentID  string `json:"agentId"`
		Events   []ev   `json:"events"`
	}
	var act struct {
		Threads []thread `json:"threads"`
	}
	// before any turn there is nothing to show, as an empty list
	a.call("task.activity", map[string]string{"taskId": task.ID}, &act)
	if act.Threads == nil || len(act.Threads) != 0 {
		t.Fatalf("before the start: %+v", act)
	}

	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})
	a.call("task.activity", map[string]string{"taskId": task.ID}, &act)
	byAgent := map[string]thread{}
	for _, th := range act.Threads {
		byAgent[th.AgentID] = th
	}
	w, r := byAgent[worker.ID], byAgent[reviewer.ID]
	if len(act.Threads) != 2 || w.ThreadID == "" || r.ThreadID == "" {
		t.Fatalf("one thread per agent process: %+v", act.Threads)
	}
	var shape []string
	text := ""
	var last uint64
	for _, e := range w.Events {
		if seq := e.Metadata.Drydock.Seq; seq < last || seq == 0 {
			t.Fatalf("events out of order or without their stored seq: %+v", w.Events)
		} else {
			last = seq
		}
		switch e.Type {
		case agui.TypeToolCallStart:
			shape = append(shape, e.Type+" "+e.ToolCallName)
		case agui.TypeToolCallResult:
			shape = append(shape, e.Type+" "+e.Content)
		case agui.TypeRunStarted, agui.TypeRunFinished:
			shape = append(shape, strings.TrimSpace(e.Type+" "+e.Outcome.Type))
		case agui.TypeTextMessageContent:
			text += e.Delta
		}
	}
	got := strings.Join(shape, " | ")
	if !strings.HasPrefix(got, agui.TypeRunStarted+" | ") || !strings.Contains(got, "TOOL_CALL_START Read | TOOL_CALL_RESULT package gocalc") ||
		!strings.HasSuffix(got, agui.TypeRunFinished+" success") || !strings.Contains(text, "implemented Subtract") {
		t.Fatalf("the worker's turn: %s; text %q", got, text)
	}
	if !strings.Contains(func() string {
		s := ""
		for _, e := range r.Events {
			s += e.Delta
		}
		return s
	}(), `"verdict":"SHIP"`) {
		t.Fatalf("the reviewer's turn: %+v", r.Events)
	}

	checkOrder(t, a, task.ID)

	if err := a.callErr("task.activity", map[string]string{"taskId": "task_none"}); err == nil {
		t.Fatal("an unknown task must be refused")
	}
	if err := a.callErr("task.activity", map[string]string{}); err == nil {
		t.Fatal("no task must be refused")
	}
}

// checkOrder fails the test when a thread of the task's activity breaks AG-UI's ordering (agui.CheckOrder).
func checkOrder(t *testing.T, a *app, taskID string) agui.Activity {
	t.Helper()
	act, err := a.c.TaskActivity(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, th := range act.Threads {
		if err := agui.CheckOrder(th.Events); err != nil {
			b, _ := json.Marshal(th.Events)
			t.Fatalf("thread %s breaks AG-UI's ordering: %v\n%s", th.ThreadID, err, b)
		}
	}
	return act
}

// A turn Claude Code reports as failed (a result with is_error: here an overloaded model, through the real claude adapter
// and parser and the labelled fake agent) is a run that errored, with the API's error, in the task's Activity: not a finished
// run. The fallback turn on the next model is a run that finished.
func TestAFailedTurnIsARunThatErroredInTheTasksActivity(t *testing.T) {
	g := newFallbackRig(t, map[string]string{"haiku": overloaded}, []string{"haiku", "sonnet"}, []string{"sonnet"})
	g.start()
	g.a.waitFor("the review of the work", func() bool {
		for _, s := range g.capt.all() {
			if s.Spec.AgentID == g.review.ID {
				return true
			}
		}
		return false
	})
	var ends []string
	for _, th := range checkOrder(t, g.a, g.task.ID).Threads {
		if th.AgentID != g.worker.ID {
			continue
		}
		for _, e := range th.Events {
			switch x := e.(type) {
			case *agui.RunFinished:
				ends = append(ends, "finished "+x.Outcome.Type)
			case *agui.RunError:
				if !strings.Contains(x.Message, "529") || !strings.Contains(x.Message, "Overloaded") {
					t.Fatalf("the failed turn's error: %+v", x)
				}
				ends = append(ends, "error")
			}
		}
	}
	if got := strings.Join(ends, ", "); got != "error, finished success" {
		t.Fatalf("the worker's turns ended %q, want the overloaded turn as an error, then its fallback finished", got)
	}
}
