package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestValidForwardPort(t *testing.T) {
	for _, n := range []int{1, 80, 3000, 65535} {
		if err := ValidForwardPort(n); err != nil {
			t.Errorf("%d: %v", n, err)
		}
	}
	for _, n := range []int{-1, 0, 65536, 1 << 20} {
		if err := ValidForwardPort(n); err == nil {
			t.Errorf("%d should be refused", n)
		}
	}
}

// getFrom fetches url until it answers or the time is up.
func getFrom(t *testing.T, url string, within time.Duration) (string, error) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last error
	for {
		c := http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get(url)
		if err == nil {
			b, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr == nil && resp.StatusCode == 200 {
				return string(b), nil
			}
			err = fmt.Errorf("status %d, read %v", resp.StatusCode, rerr)
		}
		last = err
		if time.Now().After(deadline) {
			return "", last
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestForwardPublishesAWorkspacePortOnLoopbackOnly runs the whole thing against the machine's engine: an app a worker started in an
// internal-network sandbox is reached from the host through the forwarder, only on 127.0.0.1, and goes with the sandbox.
func TestForwardPublishesAWorkspacePortOnLoopbackOnly(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	m, b, wt := newTestBox(t, e, img, Config{Image: img.Base, Network: NetworkNone})
	task := b.Spec.TaskID
	data, err := os.ReadFile(webBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "web"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	// the app the worker started: in the workspace, on port 3000
	if res := run(t, m, b, "PORT=3000 ./web >/dev/null 2>&1 </dev/null & sleep 1; echo started"); res.ExitCode != 0 || !strings.Contains(res.Output, "started") {
		t.Fatalf("%+v", res)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	m.EgressBin = func(string) (string, error) { return img.Egress, nil }

	if got, err := m.Forwards(ctx, task); err != nil || len(got) != 0 {
		t.Fatalf("nothing is forwarded yet: %+v %v", got, err)
	}
	f, err := m.OpenForward(ctx, task, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if f.Port != 3000 || f.HostPort == 0 || f.Address != net.JoinHostPort("127.0.0.1", strconv.Itoa(f.HostPort)) {
		t.Fatalf("%+v", f)
	}
	body, err := getFrom(t, "http://"+f.Address+"/", 20*time.Second)
	if err != nil || !strings.HasPrefix(body, "hello from workspace") {
		t.Fatalf("the app answers through the forwarder: %q %v", body, err)
	}

	// published on the loopback address and on no other
	out, err := e.Run(ctx, nil, "port", f.Container, "8080/tcp")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "127.0.0.1:") {
			t.Fatalf("published on %q, which is not the loopback address", line)
		}
	}

	// asking again returns the same forward
	again, err := m.OpenForward(ctx, task, 3000)
	if err != nil || again.HostPort != f.HostPort {
		t.Fatalf("%+v %v", again, err)
	}
	list, err := m.Forwards(ctx, task)
	if err != nil || len(list) != 1 || list[0].Port != 3000 || list[0].HostPort != f.HostPort {
		t.Fatalf("%+v %v", list, err)
	}

	// the forwarder relays to the workspace and has no other way: it holds the task's labels and the sandbox's removal takes it
	if err := m.CloseForward(ctx, task, 3000); err != nil {
		t.Fatal(err)
	}
	if _, err := getFrom(t, "http://"+f.Address+"/", 2*time.Second); err == nil {
		t.Fatal("a stopped forward answers nothing")
	}
	if list, _ := m.Forwards(ctx, task); len(list) != 0 {
		t.Fatalf("%+v", list)
	}
	if err := m.CloseForward(ctx, task, 3000); err != nil {
		t.Fatalf("closing a port that is not forwarded is not an error: %v", err)
	}

	// a port with no app behind it is forwarded and refused at connect, not at open
	dead, err := m.OpenForward(ctx, task, 4999)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := getFrom(t, "http://"+dead.Address+"/", 2*time.Second); err == nil {
		t.Fatal("nothing listens on 4999 in the workspace")
	}

	// removing the sandbox removes the forwarder and its network
	if err := m.Remove(ctx, task); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"container", "network"} {
		ids, err := e.list(ctx, kind, LabelTask+"="+task)
		if err != nil || len(ids) != 0 {
			t.Fatalf("%s left behind: %v %v", kind, ids, err)
		}
	}
	if _, err := m.OpenForward(ctx, task, 3000); !errors.Is(err, ErrNoSandbox) {
		t.Fatalf("a task with no sandbox forwards nothing: %v", err)
	}
}

func TestForwardLimitsAndRefusals(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	m, b, _ := newTestBox(t, e, img, Config{Image: img.Base, Network: NetworkNone})
	task := b.Spec.TaskID
	m.EgressBin = func(string) (string, error) { return img.Egress, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := m.OpenForward(ctx, task, 0); err == nil {
		t.Fatal("port 0 is refused")
	}
	if _, err := m.OpenForward(ctx, "task_that_never_ran", 3000); !errors.Is(err, ErrNoSandbox) {
		t.Fatalf("%v", err)
	}
	for i := 0; i < MaxForwards; i++ {
		if _, err := m.OpenForward(ctx, task, 3000+i); err != nil {
			t.Fatalf("forward %d: %v", i, err)
		}
	}
	if _, err := m.OpenForward(ctx, task, 3999); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("one over the limit is refused: %v", err)
	}
	list, err := m.Forwards(ctx, task)
	if err != nil || len(list) != MaxForwards || list[0].Port != 3000 || list[MaxForwards-1].Port != 3000+MaxForwards-1 {
		t.Fatalf("%+v %v", list, err)
	}
	// the forwarders of one task are not another task's
	if other, _ := m.Forwards(ctx, task+"x"); len(other) != 0 {
		t.Fatalf("%+v", other)
	}
}

func TestForwardWithoutTheProgramSaysWhatItNeeds(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	m, b, _ := newTestBox(t, e, img, Config{Image: img.Base, Network: NetworkNone})
	m.EgressBin = func(string) (string, error) { return "", errors.New("missing") }
	_, err := m.OpenForward(context.Background(), b.Spec.TaskID, 3000)
	if err == nil || !strings.Contains(err.Error(), "port forwarding needs the drydock-egress-linux-") {
		t.Fatalf("%v", err)
	}
	if list, _ := m.Forwards(context.Background(), b.Spec.TaskID); len(list) != 0 {
		t.Fatalf("a refused forward leaves nothing behind: %+v", list)
	}
}
