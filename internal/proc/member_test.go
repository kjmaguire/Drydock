package proc

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestInTreeByParents(t *testing.T) {
	parents := map[int]int{10: 1, 20: 10, 30: 20, 40: 1, 50: 50}
	parent := func(p int) (int, error) {
		if pp, ok := parents[p]; ok {
			return pp, nil
		}
		return 0, errNoParent
	}
	for _, c := range []struct {
		root, pid int
		want      bool
	}{{10, 10, true}, {10, 30, true}, {20, 30, true}, {30, 20, false}, {10, 40, false}, {10, 99, false}, {10, 50, false}, {0, 30, false}} {
		if got := inTreeByParents(c.root, c.pid, parent); got != c.want {
			t.Errorf("root %d pid %d: %v", c.root, c.pid, got)
		}
	}
}

func TestParseStatParent(t *testing.T) {
	if p, err := parseStatParent("1234 (my (odd) prog) S 77 1234 1234 0 -1"); err != nil || p != 77 {
		t.Fatalf("%d %v", p, err)
	}
	if _, err := parseStatParent("garbage"); err == nil {
		t.Fatal("garbage parsed")
	}
}

// A process this process started is in its tree; this process is not in the child's.
func TestInTreeFollowsRealProcesses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the parent walk reads /proc; Windows uses the job and is unverified")
	}
	p, err := New().Start(context.Background(), Spec{Path: "sleep", Args: []string{"5"}})
	if err != nil {
		t.Skip("no sleep binary:", err)
	}
	defer func() { _ = p.Kill(true); WaitTimeout(p, 5*time.Second) }()
	if in, _ := InTree(os.Getpid(), p.PID()); !in {
		t.Fatal("a child is not in its parent's tree")
	}
	if in, _ := InTree(p.PID(), os.Getpid()); in {
		t.Fatal("the parent is in its child's tree")
	}
}
