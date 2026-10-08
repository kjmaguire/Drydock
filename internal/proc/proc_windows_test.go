//go:build windows

package proc

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// The Windows release gate for D7: killing an agent kills its grandchildren.
func TestKillTreeReapsGrandchildren(t *testing.T) {
	p, err := New().Start(context.Background(), helperSpec("spawn"))
	if err != nil {
		t.Fatal(err)
	}
	pids := readChildPIDs(t, p.Stdout(), 2)
	for _, pid := range pids {
		if !pidAlive(pid) {
			t.Fatalf("grandchild %d not alive before kill", pid)
		}
	}
	if err := p.Kill(true); err != nil {
		t.Fatal(err)
	}
	if _, ok := WaitTimeout(p, 10*time.Second); !ok {
		t.Fatal("root did not exit")
	}
	time.Sleep(500 * time.Millisecond)
	for _, pid := range pids {
		if pidAlive(pid) {
			t.Errorf("ORPHAN: grandchild %d survived Kill(tree=true)", pid)
			if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid)); err == nil {
				windows.TerminateProcess(h, 1)
				windows.CloseHandle(h)
			}
		}
	}
}

func TestEnterAppJobIsIdempotent(t *testing.T) {
	_ = EnterAppJob() // may fail when the runner already placed us in a restrictive job; must not panic
	_ = EnterAppJob()
}
