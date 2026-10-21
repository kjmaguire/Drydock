//go:build windows

package proc

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

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

// The updater starts its helper with CREATE_BREAKAWAY_FROM_JOB; the app's job must allow that (and still kill on close), and
// an agent's job must not (audit 2026-10-16, M18).
func TestTheAppJobAllowsBreakawayAndAnAgentJobDoesNot(t *testing.T) {
	flags := func(h windows.Handle) uint32 {
		t.Helper()
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		if err := windows.QueryInformationJobObject(h, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
			t.Fatal(err)
		}
		return info.BasicLimitInformation.LimitFlags
	}
	if err := EnterAppJob(); err != nil || appJob == 0 {
		t.Skipf("this process could not enter its own job here (the runner's job forbids nesting): %v", err)
	}
	if f := flags(appJob); f&windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK == 0 || f&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatalf("app job flags %#x: it must kill on close and allow breakaway", f)
	}
	agent, err := newKillOnCloseJob()
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(agent)
	if f := flags(agent); f&windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK != 0 || f&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatalf("agent job flags %#x: it must kill on close and forbid breakaway", f)
	}
}

func readChildPIDs(t *testing.T, r io.Reader, n int) []int {
	t.Helper()
	sc := bufio.NewScanner(r)
	var pids []int
	deadline := time.After(15 * time.Second)
	lines := make(chan string)
	go func() {
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	for len(pids) < n {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("stdout closed after %d pids", len(pids))
			}
			if strings.HasPrefix(l, "child:") {
				v, _ := strconv.Atoi(strings.TrimPrefix(l, "child:"))
				pids = append(pids, v)
			}
		case <-deadline:
			t.Fatal("timed out waiting for grandchild pids")
		}
	}
	return pids
}

// An npm launcher on Windows (claude.cmd and the like) is a batch file that runs a bare `node`; cmd.exe looks in the current
// directory first, which for an agent is its worktree (audit 2026-10-16, H2).
func TestABareCommandInTheWorkingDirectoryIsNotRun(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "planted-ran")
	if err := os.WriteFile(filepath.Join(dir, "node.cmd"), []byte("@echo ran> \""+marker+"\"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(t.TempDir(), "shim.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\nnode --version\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// the plant is live: with the setting off, cmd.exe finds node.cmd in the current directory before PATH
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !strings.EqualFold(k, NoCwdSearchVar) {
			env = append(env, kv)
		}
	}
	c := exec.Command(os.Getenv("COMSPEC"), "/c", shim)
	c.Dir, c.Env = dir, env
	_ = c.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("cmd.exe did not run the planted node.cmd without the setting either; nothing to prove on this machine")
	}
	_ = os.Remove(marker)

	p, err := New().Start(context.Background(), Spec{Path: shim, Dir: dir, Label: "test:shim"})
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Stdin().Close()
	go io.Copy(io.Discard, p.Stdout())
	go io.Copy(io.Discard, p.Stderr())
	if _, ok := WaitTimeout(p, 30*time.Second); !ok {
		t.Fatal("the shim did not finish")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a node.cmd in the working directory ran instead of the real node")
	}
}
