package core_test

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/sandbox"
	"github.com/kjmaguire/drydock/internal/sandbox/sandboxtest"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// forwardRig is a core whose window opener records what it was asked to show (a labelled test double for the desktop shell), and a
// task whose sandbox runs on the machine's real engine.
type forwardRig struct {
	a      *app
	task   tasks.Task
	mu     sync.Mutex
	opened []core.SandboxApp
}

func (r *forwardRig) shown() []core.SandboxApp {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.SandboxApp(nil), r.opened...)
}

func newForwardRig(t *testing.T) *forwardRig {
	t.Helper()
	r := &forwardRig{}
	a := open(t, "") // a core with no window opener, to take the project and task from
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Serve the app", Description: "x"}, &r.task)
	r.a = a
	return r
}

func startTaskSandbox(t *testing.T, taskID string) {
	t.Helper()
	bin := sandboxtest.Engine(t)
	t.Setenv("DRYDOCK_EGRESS_BIN", sandboxtest.EgressBinary(t))
	t.Setenv("DRYDOCK_CONTAINER_CLI", bin)
	m := sandbox.NewManager(&sandbox.Engine{Bin: bin}, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	wt := t.TempDir()
	if _, err := m.Ensure(ctx, sandbox.Spec{TaskID: taskID, ProjectID: "prj_x", Worktree: wt, Config: sandbox.Config{Image: sandboxtest.Base(t, bin), Network: sandbox.NetworkNone}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Remove(context.Background(), taskID) })
}

func TestForwardingNeedsARunningSandboxAndAPerson(t *testing.T) {
	r := newForwardRig(t)
	a := r.a
	var l core.SandboxForwards
	a.call("sandbox.forward.list", map[string]any{"taskId": r.task.ID}, &l)
	if l.Running || len(l.Forwards) != 0 || l.Max != sandbox.MaxForwards || l.CanView {
		t.Fatalf("a task that never ran a sandbox forwards nothing, and this core has no viewer: %+v", l)
	}
	if err := a.callErr("sandbox.forward.list", map[string]any{"taskId": "tsk_nope"}); err == nil {
		t.Fatal("an unknown task")
	}
	// no sandbox, or (a machine with the docker client and Docker not running, as on CI's Windows runner) no engine to ask: either
	// way it says so, and opens nothing
	if err := a.callErr("sandbox.forward.open", map[string]any{"taskId": r.task.ID, "port": 3000}); err == nil ||
		!(strings.Contains(err.Error(), "no running sandbox") || strings.Contains(err.Error(), sandbox.ErrNoEngine.Error())) {
		t.Fatalf("%v", err)
	}
	ctx := context.Background()
	if _, err := a.c.SandboxForwardOpen(ctx, r.task.ID, 3000, "agt_1"); err == nil || !strings.Contains(err.Error(), "only a person") {
		t.Fatalf("an agent identity opened a port: %v", err)
	}
	if err := a.c.SandboxForwardClose(ctx, r.task.ID, 3000, "human:"); err == nil {
		t.Fatal("an empty human identity closed a port")
	}
	if err := a.c.SandboxForwardView(ctx, r.task.ID, 3000, a.c.Human()); err == nil || !strings.Contains(err.Error(), "never in a browser") {
		t.Fatalf("a window with no viewer says why it cannot open the app, and does not fall back to a browser: %v", err)
	}
	if err := a.callErr("sandbox.forward.open", map[string]any{"taskId": r.task.ID, "port": 70000}); err == nil {
		t.Fatal("a port out of range")
	}
}

func TestAForwardedPortIsOpenedOnlyInTheViewerAndEverythingIsRecorded(t *testing.T) {
	bin := sandboxtest.Engine(t)
	t.Setenv("DRYDOCK_EGRESS_BIN", sandboxtest.EgressBinary(t))
	t.Setenv("DRYDOCK_CONTAINER_CLI", bin)
	r := &forwardRig{}
	dir := t.TempDir()
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", OpenSandboxApp: func(_ context.Context, app core.SandboxApp) error {
		r.mu.Lock()
		r.opened = append(r.opened, app)
		r.mu.Unlock()
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	r.a = a
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Serve the app", Description: "x"}, &r.task)
	startTaskSandbox(t, r.task.ID)

	var l core.SandboxForwards
	a.call("sandbox.forward.list", map[string]any{"taskId": r.task.ID}, &l)
	if !l.Running || len(l.Forwards) != 0 || !l.CanView {
		t.Fatalf("%+v", l)
	}
	var f sandbox.Forward
	a.call("sandbox.forward.open", map[string]any{"taskId": r.task.ID, "port": 3000}, &f)
	if f.Port != 3000 || f.HostPort == 0 || f.Address != net.JoinHostPort("127.0.0.1", strconv.Itoa(f.HostPort)) {
		t.Fatalf("%+v", f)
	}
	a.call("sandbox.forward.list", map[string]any{"taskId": r.task.ID}, &l)
	if len(l.Forwards) != 1 || l.Forwards[0].HostPort != f.HostPort {
		t.Fatalf("%+v", l)
	}

	// the viewer is shown the address of the engine's record of the forward, whatever else the caller says
	a.call("sandbox.forward.view", map[string]any{"taskId": r.task.ID, "port": 3000}, nil)
	shown := r.shown()
	if len(shown) != 1 || shown[0].URL != "http://127.0.0.1:"+strconv.Itoa(f.HostPort)+"/" || shown[0].Port != 3000 || shown[0].TaskID != r.task.ID {
		t.Fatalf("%+v", shown)
	}
	if err := a.callErr("sandbox.forward.view", map[string]any{"taskId": r.task.ID, "port": 4000}); err == nil || !strings.Contains(err.Error(), "not forwarded") {
		t.Fatalf("a port that is not forwarded is not shown: %v", err)
	}
	if len(r.shown()) != 1 {
		t.Fatal("the refused request opened a window")
	}
	if _, err := c.SandboxForwardOpen(context.Background(), r.task.ID, 3001, "agt_1"); err == nil {
		t.Fatal("an agent forwarded a port")
	}

	a.call("sandbox.forward.close", map[string]any{"taskId": r.task.ID, "port": 3000}, nil)
	a.call("sandbox.forward.list", map[string]any{"taskId": r.task.ID}, &l)
	if len(l.Forwards) != 0 {
		t.Fatalf("%+v", l)
	}

	// what the person did is in the audit log and the task's events: the port and where it went, as them
	for _, action := range []string{core.EventForwardOpened, core.EventForwardViewed, core.EventForwardClosed} {
		var n int
		var actor, detail string
		if err := c.DB.Reader().QueryRow(`SELECT COUNT(*), COALESCE(MAX(actor),''), COALESCE(MAX(detail),'') FROM audit_log WHERE action=? AND subject=?`, action, r.task.ID).Scan(&n, &actor, &detail); err != nil || n != 1 || actor != "human:kyle" {
			t.Fatalf("%s: audit rows %d by %q: %v", action, n, actor, err)
		}
		var d map[string]any
		if json.Unmarshal([]byte(detail), &d) != nil || d["port"] != float64(3000) {
			t.Fatalf("%s: %s", action, detail)
		}
		var ev int
		if err := c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM events WHERE type=? AND task_id=?`, action, r.task.ID).Scan(&ev); err != nil || ev != 1 {
			t.Fatalf("%s: events %d: %v", action, ev, err)
		}
	}
}
