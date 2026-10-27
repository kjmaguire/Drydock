package agui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// What a process ran as (agent.started: the CLI, its model, the WSL distribution) is on its thread, so the activity can say
// "Builder · Claude Code · sonnet"; the event adds nothing to the stream, which keeps AG-UI's order.
func TestAThreadSaysWhatItsProcessRanAs(t *testing.T) {
	var s stream
	s.add("agent.started", "r1", map[string]any{"pid": 7, "adapter": "claude", "model": "sonnet", "runMode": "wsl", "wslDistro": "Ubuntu"})
	s.add("agent.waiting", "r1", map[string]any{"state": "working"})
	s.out("r1", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "Done."})
	s.add("agent.completed", "r1", map[string]any{"stopReason": "end_turn"})
	s.add("agent.started", "r2", map[string]any{"adapter": "codex", "model": "gpt-5"})
	s.add("agent.waiting", "r2", map[string]any{"state": "working"})
	s.add("agent.stopped", "r2", map[string]any{})

	a := project(t, s.evs)
	if len(a.Threads) != 2 {
		t.Fatalf("threads %+v", a.Threads)
	}
	t1, t2 := a.Threads[0], a.Threads[1]
	if t1.Adapter != "claude" || t1.Model != "sonnet" || t1.RunMode != "wsl" || t1.WSLDistro != "Ubuntu" || t2.Adapter != "codex" || t2.Model != "gpt-5" || t2.RunMode != "" {
		t.Fatalf("threads %+v / %+v", t1, t2)
	}
	eq(t, types(t1), []string{"RUN_STARTED r1:1", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT Done.", "TEXT_MESSAGE_END", "RUN_FINISHED r1:1 success end_turn"})
	b, _ := json.Marshal(t2)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["adapter"] != "codex" || m["model"] != "gpt-5" {
		t.Fatalf("thread JSON %s", b)
	}
	if _, ok := m["wslDistro"]; ok {
		t.Fatalf("a native process has no distro: %s", b)
	}
}

// fakeTurn runs one turn of the labelled fake agent in this process (as `claude -p` would run it), reads its stream-json
// with the real claude parser, and puts the chunks on the bus as the runtime does: agent.waiting, agent.output per chunk,
// agent.completed with the done chunk's stop reason. The thread is what the Activity shows for that turn.
func fakeTurn(t *testing.T, script fakeagent.Script, flags ...string) Thread {
	t.Helper()
	sc, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, sc, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRYDOCK_FAKE_SCRIPT", path)
	var out bytes.Buffer
	if code := fakeagent.Run(append([]string{"-p", "go"}, flags...), strings.NewReader(""), &out); code != 0 {
		t.Fatalf("the fake agent exited %d", code)
	}
	var s stream
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	p := claude.NewParser()
	stop := ""
	for _, line := range strings.Split(out.String(), "\n") {
		cs, _ := p.Parse([]byte(line))
		for _, c := range cs {
			if c.Kind == workflow.ChunkDone {
				var d workflow.DoneChunk
				_ = c.Decode(&d)
				stop = d.StopReason
			}
			s.add("agent.output", "r", map[string]any{"kind": c.Kind, "data": c.Data})
		}
	}
	s.add("agent.completed", "r", map[string]any{"stopReason": stop})
	a := project(t, s.evs)
	if len(a.Threads) != 1 {
		t.Fatalf("threads %+v", a.Threads)
	}
	return a.Threads[0]
}

// A Claude turn that failed is a run that errored, through the real parser and the stand-in's Claude Code shapes. The API
// failed after the agent answered: the answer is its message, the run ends RUN_ERROR with the API's words and a code that says
// the turn failed, and nothing marks it finished. The API failed on the first call: RUN_ERROR, and the error's words are not
// shown as the agent's own message (the CLI writes them as an assistant message, which the Activity used to show as the answer
// and count the turn as answered).
func TestAFailedClaudeTurnEndsWithRunErrorNotFinished(t *testing.T) {
	const apiErr = `API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	ends := func(th Thread) (fail *RunError, finished bool) {
		for _, e := range th.Events {
			switch x := e.(type) {
			case *RunError:
				fail = x
			case *RunFinished:
				finished = true
			}
		}
		return fail, finished
	}

	answered := fakeTurn(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "half way there"}, {Fail: apiErr}}})
	fail, finished := ends(answered)
	if fail == nil || finished {
		t.Fatalf("an answered turn that then failed:\n  %s", strings.Join(types(answered), "\n  "))
	}
	if !strings.Contains(fail.Message, "529") || !strings.Contains(fail.Message, "Overloaded") || fail.Code != "error_api_error" {
		t.Fatalf("RUN_ERROR %+v", fail)
	}
	if got := types(answered); !slices.Contains(got, "TEXT_MESSAGE_CONTENT half way there") && !slices.Contains(got, "TEXT_MESSAGE_CONTENT half ") {
		t.Fatalf("the answer is missing:\n  %s", strings.Join(got, "\n  "))
	}

	first := fakeTurn(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "never said"}}, ModelErrors: map[string]string{"m1": apiErr}}, "--model", "m1")
	fail, finished = ends(first)
	if fail == nil || finished || !strings.Contains(fail.Message, "Overloaded") {
		t.Fatalf("a first call that failed:\n  %s", strings.Join(types(first), "\n  "))
	}
	for _, line := range types(first) {
		if strings.HasPrefix(line, "TEXT_MESSAGE") {
			t.Fatalf("the API error is shown as the agent's words:\n  %s", strings.Join(types(first), "\n  "))
		}
	}
}
