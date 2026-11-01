package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeEngine is a container client that is a shell script, for what a real engine cannot be made to do on request: a daemon that is
// stopped, a "run" that creates its container and then fails. It is a labelled test double of the engine's command line, not of
// an agent; the real engine is used by forward_test.go. calls is the file every invocation is appended to.
func fakeEngine(t *testing.T, taskID, script string) (*Engine, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake client is a shell script")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "docker")
	body := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %q\nWS=%q\nSTATE=%q\n%s\n", calls, BaseName(taskID)+"-ws", filepath.Join(dir, "created"), script)
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Engine{Bin: bin}, calls
}

func TestExistingTellsAMissingSandboxFromAnEngineThatFails(t *testing.T) {
	e, _ := fakeEngine(t, "t1", `case "$1" in
inspect) if [ "$FAKE_MODE" = down ]; then echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?" >&2; else echo "Error: No such object: $4" >&2; fi; exit 1;;
esac`)
	m := NewManager(e, "")
	ctx := context.Background()
	t.Setenv("FAKE_MODE", "")
	if _, err := m.Existing(ctx, "t1"); !errors.Is(err, ErrNoSandbox) {
		t.Fatalf("an engine that has no such container means no sandbox: %v", err)
	}
	t.Setenv("FAKE_MODE", "down")
	_, err := m.Existing(ctx, "t1")
	if err == nil || errors.Is(err, ErrNoSandbox) || !errors.Is(err, ErrNoEngine) || !strings.Contains(err.Error(), "Cannot connect") {
		t.Fatalf("a stopped daemon is no engine, not 'no sandbox': %v", err)
	}
}

// The client's words for "Docker is not running", on Windows (Docker Desktop not started: no named pipe) and on Linux, are
// recognised; an ordinary engine error is not taken for one.
func TestADaemonThatIsNotRunningIsRecognised(t *testing.T) {
	for _, msg := range []string{
		"failed to connect to the docker API at npipe:////./pipe/docker_engine; check if the path is correct and if the daemon is running: open //./pipe/docker_engine: The system cannot find the file specified.",
		"error during connect: this error may indicate that the docker daemon is not running",
		"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
	} {
		if !daemonUnreachable(msg) {
			t.Errorf("not recognised: %s", msg)
		}
	}
	for _, msg := range []string{"Error: No such object: x", "port is already allocated", "permission denied"} {
		if daemonUnreachable(msg) {
			t.Errorf("taken for a stopped daemon: %s", msg)
		}
	}
}

func TestAForwarderThatFailsToStartIsRemoved(t *testing.T) {
	e, calls := fakeEngine(t, "t1", `case "$1" in
version) echo '{"Version":"29","Os":"linux","Arch":"amd64"}';;
image) exit 0;;
network) exit 0;;
ps) exit 0;;
inspect) case "$4" in
  "$WS") echo "true|t1|p1|drydock";;
  *-fwd-*) [ -f "$STATE" ] || { echo "Error: No such object: $4" >&2; exit 1; }; echo abc;;
  *) echo "Error: No such object: $4" >&2; exit 1;;
  esac;;
run) : > "$STATE"; echo "port is already allocated" >&2; exit 1;;
rm) rm -f "$STATE";;
esac`)
	prog := filepath.Join(t.TempDir(), "egress")
	if err := os.WriteFile(prog, []byte("not a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(e, "")
	m.EgressBin = func(string) (string, error) { return prog, nil }
	if _, err := m.OpenForward(context.Background(), "t1", 3000); err == nil || !strings.Contains(err.Error(), "port is already allocated") {
		t.Fatalf("%v", err)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "rm -f -v "+BaseName("t1")+"-fwd-3000") {
		t.Fatalf("the container the failed run created was left behind:\n%s", b)
	}
}
