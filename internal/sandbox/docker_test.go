package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/proc"
)

// These tests run real containers on the machine's engine (skipped when there is none). Each uses its own task id and removes
// what it created.

func newTestBox(t *testing.T, e *Engine, img testImages, cfg Config) (*Manager, *Box, string) {
	t.Helper()
	wt := t.TempDir()
	m := NewManager(e, t.TempDir())
	m.EgressBin = func(string) (string, error) { return img.Egress, nil }
	task := fmt.Sprintf("task_test_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = m.Remove(context.Background(), task) })
	b, err := m.Ensure(ctx, Spec{TaskID: task, ProjectID: "prj_test", Worktree: wt, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	// A container running as root leaves root-owned folders in the bind-mounted worktree; a test runner that is not root (a
	// CI runner) could not remove its temporary folder. Before the box goes (cleanups run last-registered first), open them up.
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), time.Minute)
		defer ccancel()
		_, _ = m.Exec(cctx, b, ExecRequest{Command: "chmod -R a+rwX /workspace 2>/dev/null || true"})
	})
	return m, b, wt
}

func run(t *testing.T, m *Manager, b *Box, cmd string) ExecResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := m.Exec(ctx, b, ExecRequest{Command: cmd})
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	return res
}

func TestSandboxRunsCommandsInTheTaskWorktree(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	m, b, wt := newTestBox(t, e, img, Config{Image: img.Base, Network: NetworkNone, Env: map[string]string{"GREETING": "hi there"},
		Setup: []string{"echo setup-ran > setup.txt"}})
	if err := os.WriteFile(filepath.Join(wt, "hello.txt"), []byte("from the host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := run(t, m, b, `cat hello.txt; echo "$GREETING"; pwd; echo made > made.txt`)
	if res.ExitCode != 0 || !strings.Contains(res.Output, "from the host") || !strings.Contains(res.Output, "hi there") || !strings.Contains(res.Output, "/workspace") {
		t.Fatalf("%+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "made.txt")); string(got) != "made\n" {
		t.Fatalf("a file the command wrote is in the worktree: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "setup.txt")); string(got) != "setup-ran\n" {
		t.Fatalf("setup ran: %q", got)
	}
	if len(b.Setup) != 1 || b.Setup[0].ExitCode != 0 {
		t.Fatalf("setup results %+v", b.Setup)
	}
	if res := run(t, m, b, "exit 7"); res.ExitCode != 7 {
		t.Fatalf("exit code %d", res.ExitCode)
	}
	run(t, m, b, "mkdir -p sub/dir")
	ctx := context.Background()
	if res, err := m.Exec(ctx, b, ExecRequest{Command: "pwd", Dir: "sub/dir"}); err != nil || strings.TrimSpace(res.Output) != "/workspace/sub/dir" {
		t.Fatalf("dir: %q %v", res.Output, err)
	}
	if _, err := m.Exec(ctx, b, ExecRequest{Command: "pwd", Dir: "../.."}); err == nil {
		t.Fatal("a folder outside the working folder is refused")
	}
	if res, err := m.Exec(ctx, b, ExecRequest{Command: `echo "$X"`, Env: []string{"X=per-command"}}); err != nil || strings.TrimSpace(res.Output) != "per-command" {
		t.Fatalf("env: %q %v", res.Output, err)
	}

	// a command past its time is stopped with everything it started
	start := time.Now()
	res, err := m.Exec(ctx, b, ExecRequest{Command: "sleep 30 & sleep 31; echo late > late.txt", Timeout: 2 * time.Second})
	if err != nil || !res.TimedOut || time.Since(start) > 20*time.Second {
		t.Fatalf("timeout: %+v %v after %s", res, err, time.Since(start))
	}
	time.Sleep(time.Second)
	if _, err := os.Stat(filepath.Join(wt, "late.txt")); err == nil {
		t.Fatal("the timed-out command went on")
	}
	if ps := run(t, m, b, "ps -eo args"); strings.Contains(ps.Output, "sleep 30") || strings.Contains(ps.Output, "sleep 31") {
		t.Fatalf("processes the command started are still running:\n%s", ps.Output)
	}

	// an interrupted turn stops the command; the files stay and the sandbox keeps working
	ictx, icancel := context.WithCancel(ctx)
	go func() { time.Sleep(1500 * time.Millisecond); icancel() }()
	if _, err := m.Exec(ictx, b, ExecRequest{Command: "sleep 40; echo late2 > late2.txt"}); err == nil {
		t.Fatal("an interrupted command returns the interruption")
	}
	if res := run(t, m, b, "cat made.txt"); strings.TrimSpace(res.Output) != "made" {
		t.Fatalf("after an interruption: %+v", res)
	}
	time.Sleep(time.Second)
	if _, err := os.Stat(filepath.Join(wt, "late2.txt")); err == nil {
		t.Fatal("the interrupted command went on")
	}

	// long output: the agent sees the head and the tail, the human's log has everything
	res = run(t, m, b, `head -c 200000 /dev/zero | tr '\0' a; echo; echo THE-END`)
	if !res.Truncated || res.Bytes < 200000 || !strings.Contains(res.Output, "THE-END") || len(res.Output) > 40<<10 {
		t.Fatalf("long output: truncated=%v bytes=%d len=%d", res.Truncated, res.Bytes, len(res.Output))
	}
	if st, err := os.Stat(res.LogPath); err != nil || st.Size() < 200000 {
		t.Fatalf("full log %s: %v", res.LogPath, err)
	}
}

func TestSandboxServicesAndBackgroundProcesses(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	m, b, _ := newTestBox(t, e, img, Config{Image: img.Base, Network: NetworkNone, Services: map[string]Service{"web": {Image: img.Web}}})
	var res ExecResult
	for i := 0; i < 20; i++ { // the service needs a moment to listen
		if res = run(t, m, b, "curl -sS --max-time 3 http://web/"); res.ExitCode == 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "hello from") {
		t.Fatalf("service by name: %+v", res)
	}

	ctx := context.Background()
	if _, err := m.Start(ctx, b, "ticker", "while :; do echo tick; sleep 0.2; done", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, b, "ticker", "echo again", ""); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("starting a running name again: %v", err)
	}
	time.Sleep(time.Second)
	out, running, err := m.Logs(ctx, b, "ticker", 4096)
	if err != nil || !running || !strings.Contains(out, "tick") {
		t.Fatalf("logs: %q %v %v", out, running, err)
	}
	if err := m.Stop(ctx, b, "ticker"); err != nil {
		t.Fatal(err)
	}
	if _, running, _ := m.Logs(ctx, b, "ticker", 100); running {
		t.Fatal("still running after Stop")
	}
	if _, _, err := m.Logs(ctx, b, "nothing", 100); err == nil {
		t.Fatal("an unknown name is an error")
	}
	if _, err := m.Start(ctx, b, "Bad Name", "true", ""); err == nil {
		t.Fatal("names are checked")
	}
}

func TestSandboxNetworkAllowlist(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	ctx := context.Background()
	// a web server outside the task, standing in for a public host the project allows
	outside := fmt.Sprintf("drydock-test-outside-%d", time.Now().UnixNano())
	if _, err := e.Run(ctx, nil, "network", "create", outside); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.Run(context.Background(), nil, "network", "rm", outside) })
	if _, err := e.Run(ctx, nil, "run", "-d", "--name", outside+"-web", "--network", outside, img.Web); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.Run(context.Background(), nil, "rm", "-f", outside+"-web") })
	ipOut, err := e.Run(ctx, nil, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", outside+"-web")
	webIP := strings.TrimSpace(ipOut)
	if err != nil || webIP == "" {
		t.Fatalf("web ip: %q %v", ipOut, err)
	}

	m, b, _ := newTestBox(t, e, img, Config{Image: img.Base, NoDefaultHosts: true, AllowHosts: []string{webIP}})
	if b.Egress == "" {
		t.Fatal("an allowlist sandbox has an egress proxy")
	}
	// the proxy, and only the proxy, can reach the outside network
	if _, err := e.Run(ctx, nil, "network", "connect", outside, b.Egress); err != nil {
		t.Fatal(err)
	}
	var res ExecResult
	for i := 0; i < 20; i++ {
		if res = run(t, m, b, "curl -sS --max-time 3 http://"+webIP+"/"); res.ExitCode == 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "hello from") {
		t.Fatalf("an allowlisted host through the proxy: %+v", res)
	}
	if res := run(t, m, b, "curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://203.0.113.9/"); strings.TrimSpace(res.Output) != "403" {
		t.Fatalf("a host not on the list is refused by the proxy: %+v", res)
	}
	if res := run(t, m, b, "curl -sS --max-time 3 --noproxy '*' http://"+webIP+"/"); res.ExitCode == 0 {
		t.Fatalf("without the proxy there is no way out: %+v", res)
	}
	if res := run(t, m, b, "curl -sS --max-time 3 --noproxy '*' http://"+b.Egress+":3128/"); res.ExitCode == 0 && strings.Contains(res.Output, "hello") {
		t.Fatalf("unexpected: %+v", res)
	}
	refused := m.Denials(ctx, b, 10)
	found := false
	for _, d := range refused {
		if d.Host == "203.0.113.9" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the refusal is reported: %+v", refused)
	}
	if d := m.Describe(ctx, b); !strings.Contains(d, "Recently refused") || !strings.Contains(d, "203.0.113.9") {
		t.Fatalf("describe:\n%s", d)
	}
}

func TestSandboxIsReusedAndReplacedWhenItsDefinitionChanges(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	ctx := context.Background()
	cfg := Config{Image: img.Base, Network: NetworkNone}
	m, b, wt := newTestBox(t, e, img, cfg)
	id := func(name string) string {
		out, _ := e.Run(ctx, nil, "inspect", "--format", "{{.Id}}", name)
		return strings.TrimSpace(out)
	}
	first := id(b.Workspace)
	b2, err := m.Ensure(ctx, b.Spec)
	if err != nil || id(b2.Workspace) != first {
		t.Fatalf("the same definition reuses the container: %v", err)
	}
	// a new Drydock start (a new manager) finds the running sandbox and keeps it
	m2 := NewManager(e, "")
	b3, err := m2.Ensure(ctx, Spec{TaskID: b.Spec.TaskID, ProjectID: "prj_test", Worktree: wt, Config: cfg})
	if err != nil || id(b3.Workspace) != first || !b3.reused {
		t.Fatalf("after a restart: %v reused=%v", err, b3.reused)
	}
	if !strings.Contains(m2.Describe(ctx, b3), "first created") {
		t.Fatal("a reused sandbox says its setup ran earlier")
	}
	// a changed definition replaces it
	cfg.Env = map[string]string{"NEW": "1"}
	b4, err := m.Ensure(ctx, Spec{TaskID: b.Spec.TaskID, ProjectID: "prj_test", Worktree: wt, Config: cfg})
	if err != nil || id(b4.Workspace) == first || id(b4.Workspace) == "" {
		t.Fatalf("a changed definition replaces the container: %v", err)
	}
	if res := run(t, m, b4, `echo "$NEW"`); strings.TrimSpace(res.Output) != "1" {
		t.Fatalf("new definition in force: %+v", res)
	}
}

func TestSandboxRemoveAndPruneLeaveNothingBehind(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	ctx := context.Background()
	cfg := Config{Image: img.Base, Volumes: []string{"/workspace/cache"}, Services: map[string]Service{"web": {Image: img.Web}}, NoDefaultHosts: true, AllowHosts: []string{"example.com"}}
	m, keep, _ := newTestBox(t, e, img, cfg)
	_, gone, _ := newTestBox(t, e, img, cfg)
	left := func(task string) int {
		n := 0
		for _, kind := range []string{"container", "network", "volume"} {
			ids, err := e.list(ctx, kind, LabelTask+"="+task)
			if err != nil {
				t.Fatal(err)
			}
			n += len(ids)
		}
		return n
	}
	if left(gone.Spec.TaskID) < 5 { // workspace, egress, service, two networks, a volume
		t.Fatalf("expected the sandbox objects, found %d", left(gone.Spec.TaskID))
	}
	removed, err := m.Prune(ctx, func(task string) bool {
		return task != gone.Spec.TaskID && strings.HasPrefix(task, "task_test_") || task == keep.Spec.TaskID
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) == 0 || left(gone.Spec.TaskID) != 0 {
		t.Fatalf("prune: removed %v, %d left", removed, left(gone.Spec.TaskID))
	}
	if left(keep.Spec.TaskID) == 0 {
		t.Fatal("prune kept what it was told to keep")
	}
	if err := m.Remove(ctx, keep.Spec.TaskID); err != nil || left(keep.Spec.TaskID) != 0 {
		t.Fatalf("remove: %v, %d left", err, left(keep.Spec.TaskID))
	}
}

func TestSandboxSetupStopsAtTheFirstFailureAndSaysSo(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	m, b, wt := newTestBox(t, e, img, Config{Image: img.Base, Network: NetworkNone, Setup: []string{"echo one", "echo broken >&2; exit 3", "echo never > never.txt"}})
	if len(b.Setup) != 2 || b.Setup[1].ExitCode != 3 || !strings.Contains(b.Setup[1].Output, "broken") {
		t.Fatalf("setup %+v", b.Setup)
	}
	if _, err := os.Stat(filepath.Join(wt, "never.txt")); err == nil {
		t.Fatal("a step after a failure ran")
	}
	if d := m.Describe(context.Background(), b); !strings.Contains(d, "FAILED (exit 3)") || !strings.Contains(d, "broken") {
		t.Fatalf("describe:\n%s", d)
	}
}

func TestSandboxRunsAsTheConfiguredUserWithAVolumeItCanWrite(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	m, b, wt := newTestBox(t, e, img, Config{Image: img.Base, Network: NetworkNone, User: "1000:1000", Volumes: []string{"/workspace/node_modules"}})
	res := run(t, m, b, "id -u; touch node_modules/x && echo wrote")
	if !strings.Contains(res.Output, "1000") || !strings.Contains(res.Output, "wrote") {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(wt, "node_modules", "x")); err == nil {
		t.Fatal("a volume's files stay in the volume, not in the worktree")
	}
}

// A project check rewritten with CommandArgs runs through Drydock's own checks runner, inside the container: /.dockerenv exists
// only there, and the check's environment arrives.
func TestSandboxRunsAProjectCheckThroughTheChecksRunner(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	m, b, wt := newTestBox(t, e, img, Config{Image: img.Base, Network: NetworkNone})
	ctx := context.Background()
	check := func(argv ...string) checks.Result {
		args, err := m.CommandArgs(b, argv, wt, []string{"CI=1"}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		res, err := checks.Run(ctx, proc.New(), checks.Spec{Name: "test", Argv: append([]string{e.Bin}, args...), Dir: wt, Timeout: 2 * time.Minute, Parser: "generic", LogDir: t.TempDir()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := check("sh", "-c", `test -f /.dockerenv && test "$CI" = 1 && echo in-the-container`); !res.Passed || !strings.Contains(res.Output, "in-the-container") {
		t.Fatalf("passing check: %+v", res)
	}
	if res := check("sh", "-c", "echo FAIL-MARK; exit 3"); res.Passed || res.ExitCode != 3 || !strings.Contains(res.Output, "FAIL-MARK") {
		t.Fatalf("failing check: %+v", res)
	}
}
