//go:build !windows

package core_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/terminal"
)

// A terminal opens in the project's repository (or a task's worktree), carries input and output through calls, and is gone
// once closed. No path is ever taken from the caller.
func TestTerminalThroughTheCoreCalls(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	a := open(t, "")
	p, repo := a.setup("gocalc")
	if _, err := a.c.Call(t.Context(), "terminal.open", mustJSON(map[string]any{})); err == nil {
		t.Fatal("a terminal opened without a project or task")
	}
	var in terminal.Info
	a.call("terminal.open", map[string]any{"projectId": p.ID, "cols": 90, "rows": 20}, &in)
	if in.Cwd != repo || in.Title != "gocalc" {
		t.Fatalf("opened %+v, want the repository %s", in, repo)
	}
	a.call("terminal.write", map[string]any{"id": in.ID, "data": "echo hi-$((6*7))\n"}, nil)
	var all string
	var from int64
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(all, "hi-42") && time.Now().Before(deadline) {
		var c terminal.Chunk
		a.call("terminal.read", map[string]any{"id": in.ID, "from": from, "waitMs": 500}, &c)
		all += string(c.Data)
		from = c.Next
	}
	if !strings.Contains(all, "hi-42") {
		t.Fatalf("output %q", all)
	}
	var list []terminal.Info
	a.call("terminal.list", nil, &list)
	if len(list) != 1 {
		t.Fatalf("list %+v", list)
	}
	a.call("terminal.close", map[string]string{"id": in.ID}, nil)
	a.call("terminal.list", nil, &list)
	if len(list) != 0 {
		t.Fatalf("still open: %+v", list)
	}

	// a task without a worktree opens in its project's repository
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "x"}, &task)
	a.call("terminal.open", map[string]any{"taskId": task.ID}, &in)
	if in.Cwd != repo {
		t.Fatalf("task terminal %+v", in)
	}
}

// Decision P50: a Connection's sign-in starts the CLI's own login in a terminal; a CLI that is not there, or a Connection the
// core does not list, is refused with the reason, and no terminal opens.
func TestSignInRefusesWhatIsNotThere(t *testing.T) {
	a := open(t, "")
	for _, c := range []struct{ params, want string }{
		{`{"adapter":"nope"}`, "no Connection named nope"},
		{`{"adapter":"claude","distro":"Ubuntu-26.04"}`, "inside the WSL distro Ubuntu-26.04"},
	} {
		_, err := a.c.Call(t.Context(), "connection.signIn", []byte(c.params))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", c.params, err)
		}
	}
	var list []terminal.Info
	a.call("terminal.list", nil, &list)
	if len(list) != 0 {
		t.Fatalf("a terminal opened: %+v", list)
	}
}
