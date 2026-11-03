package core_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// fakeContainerCLI points the core's container client at a shell script (a labelled test double of the engine's command line,
// not of an agent), for what this machine's engine cannot be made to do on request or when no engine runs here. TASK holds the
// task id the script may echo back; every invocation is appended to the returned file.
func fakeContainerCLI(t *testing.T, taskID, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake client is a shell script")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "docker")
	body := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %q\nTASK=%q\n%s\n", calls, taskID, script)
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRYDOCK_CONTAINER_CLI", bin)
	return calls
}

// The task page offers the ports something listens on in the workspace (X31): sandbox.forward.list carries them, a port and the
// name of the background process, read from the workspace; a port bound to the workspace's loopback is left out.
func TestTheForwardListOffersThePortsTheWorkspaceListensOn(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Serve the app", Description: "x"}, &task)
	fakeContainerCLI(t, task.ID, `case "$1" in
inspect) echo "true|$TASK|p1|drydock";;
ps) exit 0;;
exec) cat <<'EOF'
#tcp
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 111 1 0000000000000000 100 0 0 10 0
   1: 0100007F:2328 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 222 1 0000000000000000 100 0 0 10 0
#tcp6
#sock dev [111]
EOF
;;
esac`)
	var l core.SandboxForwards
	a.call("sandbox.forward.list", map[string]any{"taskId": task.ID}, &l)
	if !l.Running || len(l.Listening) != 1 || l.Listening[0].Port != 3000 || l.Listening[0].Name != "dev" {
		t.Fatalf("%+v", l)
	}
}

// While a task's sandbox is created, each step is said (X39): sandbox.starting events with the step, then sandbox.ready, and the
// task's detail carries the steps so the page says where the start is. A start that fails says so too.
func TestCreatingATaskSandboxSaysEachStep(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	commitSandboxDefinition(t, repo, `{"image":"node:24","network":"none","setup":["npm ci"]}`)
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}, nil)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Serve the app", Description: "x"}, &task)
	fakeContainerCLI(t, task.ID, `[ "$FAKE_ENGINE" = gone ] && { echo "cannot connect to the engine" >&2; exit 1; }
case "$1" in
version) echo '{"Version":"29","Os":"linux","Arch":"amd64"}';;
image) exit 1;;
pull) [ "$FAKE_PULL" = fail ] && { echo "pull access denied" >&2; exit 1; };;
inspect) echo "Error: No such object" >&2; exit 1;;
esac
exit 0`)
	ctx := context.Background()
	if err := a.c.TestStartTaskSandbox(ctx, task.ID, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var d core.TaskDetail
	a.call("task.get", map[string]string{"id": task.ID}, &d)
	if d.SandboxStart == nil || !d.SandboxStart.Ready || strings.Join(d.SandboxStart.Steps, "|") != "pulling node:24|starting the workspace|running setup (1 of 1)" {
		t.Fatalf("%+v", d.SandboxStart)
	}
	types := func() []string {
		rows, err := a.c.DB.Reader().Query(`SELECT type FROM events WHERE task_id=? AND type LIKE 'sandbox.%' ORDER BY seq`, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			out = append(out, s)
		}
		return out
	}
	if got := strings.Join(types(), ","); got != "sandbox.starting,sandbox.starting,sandbox.starting,sandbox.ready" {
		t.Fatalf("events %s", got)
	}

	// the engine goes away after a start that went well: a failure before any step ends the "started" record too
	_ = a.c.TestRemoveTaskSandbox(ctx, task.ID)
	t.Setenv("FAKE_ENGINE", "gone")
	if err := a.c.TestStartTaskSandbox(ctx, task.ID, t.TempDir()); err == nil {
		t.Fatal("a sandbox started with no engine")
	}
	var d1 core.TaskDetail
	a.call("task.get", map[string]string{"id": task.ID}, &d1)
	if d1.SandboxStart != nil && (d1.SandboxStart.Ready || len(d1.SandboxStart.Steps) > 0) {
		t.Fatalf("still reads as started: %+v", d1.SandboxStart)
	}
	if ev := types(); ev[len(ev)-1] != "sandbox.start_failed" {
		t.Fatalf("events %v", ev)
	}
	t.Setenv("FAKE_ENGINE", "")

	// a start that fails: the page stops saying it is starting
	t.Setenv("FAKE_PULL", "fail")
	if err := a.c.TestStartTaskSandbox(ctx, task.ID, t.TempDir()); err == nil {
		t.Fatal("a pull that fails started the sandbox")
	}
	var d2 core.TaskDetail
	a.call("task.get", map[string]string{"id": task.ID}, &d2)
	if d2.SandboxStart == nil || !d2.SandboxStart.Failed || d2.SandboxStart.Ready || len(d2.SandboxStart.Steps) != 1 {
		t.Fatalf("%+v", d2.SandboxStart)
	}
	if ev := types(); ev[len(ev)-1] != "sandbox.start_failed" {
		t.Fatalf("events %v", ev)
	}
}
