//go:build !windows

package terminal

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func sh() (string, []string) { return "/bin/sh", nil }

// readUntil reads from offset until want appears or the deadline passes, and returns the text and the next offset.
func readUntil(t *testing.T, m *Manager, id string, from int64, want string) (string, int64) {
	t.Helper()
	var all bytes.Buffer
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := m.Read(context.Background(), id, from, 500*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(c.Data)
		from = c.Next
		if strings.Contains(all.String(), want) {
			return all.String(), from
		}
		if c.Exited {
			break
		}
	}
	t.Fatalf("did not see %q in %q", want, all.String())
	return "", 0
}

func TestATerminalRunsAShellInItsFolder(t *testing.T) {
	m := &Manager{Shell: sh}
	dir := t.TempDir()
	in, err := m.Open(dir, "test", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll()
	if in.Cols != 100 || in.Rows != 30 || in.Cwd != dir {
		t.Fatalf("info %+v", in)
	}
	if err := m.Write(in.ID, []byte("pwd; echo done-$((40+2))\n")); err != nil {
		t.Fatal(err)
	}
	out, next := readUntil(t, m, in.ID, 0, "done-42")
	if !strings.Contains(out, dir) {
		t.Fatalf("pwd not in output: %q", out)
	}
	if err := m.Resize(in.ID, 120, 40); err != nil {
		t.Fatal(err)
	}
	m.Write(in.ID, []byte("stty size\n"))
	readUntil(t, m, in.ID, next, "40 120")
	if l := m.List(); len(l) != 1 || l[0].Cols != 120 {
		t.Fatalf("list %+v", l)
	}
	m.Write(in.ID, []byte("exit 3\n"))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, _ := m.Read(context.Background(), in.ID, 1<<40, 200*time.Millisecond)
		if c.Exited {
			if c.ExitCode != 3 {
				t.Fatalf("exit code %d", c.ExitCode)
			}
			return
		}
	}
	t.Fatal("the shell did not exit")
}

// Closing a terminal kills what the shell started too, not only the shell.
func TestClosingATerminalKillsItsChildren(t *testing.T) {
	m := &Manager{Shell: sh}
	in, err := m.Open(t.TempDir(), "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Write(in.ID, []byte("sleep 300 & echo child=$!\n"))
	var pid int
	deadline0 := time.Now().Add(10 * time.Second)
	var from int64
	var all string
	for pid == 0 && time.Now().Before(deadline0) {
		c, _ := m.Read(context.Background(), in.ID, from, 300*time.Millisecond)
		all += string(c.Data)
		from = c.Next
		if mm := regexp.MustCompile(`child=(\d+)`).FindStringSubmatch(all); mm != nil {
			pid, _ = strconv.Atoi(mm[1])
		}
	}
	if pid == 0 {
		t.Fatalf("no pid in %q", all)
	}
	if err := m.Close(in.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if gone(pid) {
			if _, err := m.Read(context.Background(), in.ID, 0, 0); err != ErrNotFound {
				t.Fatalf("a closed terminal still reads: %v", err)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child %d survived the terminal", pid)
}

// A reader that falls behind loses the oldest output, and is told how much.
func TestAReaderThatFallsBehindIsToldWhatWasDropped(t *testing.T) {
	m := &Manager{Shell: sh}
	in, err := m.Open(t.TempDir(), "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll()
	m.Write(in.ID, []byte("head -c 1500000 /dev/zero | tr '\\0' a; echo; echo end-mark\n"))
	readUntilNoKeep := func() Chunk {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			c, _ := m.Read(context.Background(), in.ID, 1<<40, 200*time.Millisecond)
			if c.Next > Keep+200000 {
				time.Sleep(300 * time.Millisecond)
				c, _ = m.Read(context.Background(), in.ID, 0, 0)
				return c
			}
		}
		t.Fatal("output did not arrive")
		return Chunk{}
	}
	c := readUntilNoKeep()
	if c.Dropped == 0 || len(c.Data) == 0 || len(c.Data) > maxRead {
		t.Fatalf("dropped %d, data %d", c.Dropped, len(c.Data))
	}
}

func TestLimits(t *testing.T) {
	m := &Manager{Shell: sh, Max: 1}
	in, err := m.Open(t.TempDir(), "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll()
	if _, err := m.Open(t.TempDir(), "", 0, 0); err == nil {
		t.Fatal("a second terminal opened past the limit")
	}
	if _, err := (&Manager{Shell: sh}).Open("/nonexistent/folder", "", 0, 0); err == nil {
		t.Fatal("opened in a missing folder")
	}
	if err := m.Write("term_nope", []byte("x")); err != ErrNotFound {
		t.Fatalf("unknown id: %v", err)
	}
	_ = in
}

// gone reports a process that no longer runs: absent, or a zombie nobody has reaped yet.
func gone(pid int) bool {
	if syscall.Kill(pid, 0) == syscall.ESRCH {
		return true
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i >= 0 && strings.HasPrefix(strings.TrimSpace(s[i+1:]), "Z")
}

// OpenProgram runs a program instead of a shell (a CLI's own sign-in, decision P50): found on PATH by its bare name, with the
// environment it is given, and the terminal ends when the program does, telling OnExit its code.
func TestOpenProgramRunsTheProgramAndEndsWithIt(t *testing.T) {
	m := &Manager{Shell: sh}
	dir := t.TempDir()
	codes := make(chan int, 2)
	in, err := m.OpenProgram(dir, "Sign in: claude", 100, 30, Program{Path: "sh", Args: []string{"-c", "echo prog-$((40+2)) mark=$MARK; exit 3"},
		Env: []string{"PATH=/usr/bin:/bin", "MARK=only-this"}, OnExit: func(code int) { codes <- code }})
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll()
	if in.Title != "Sign in: claude" || !filepath.IsAbs(in.Shell) || filepath.Base(in.Shell) != "sh" || in.Cwd != dir {
		t.Fatalf("info %+v (the bare name must be found on PATH: the pseudo-terminal does not search it)", in)
	}
	out, _ := readUntil(t, m, in.ID, 0, "mark=only-this")
	if strings.Contains(out, "$((") || !strings.Contains(out, "prog-42") {
		t.Fatalf("the program's argument was not run by it: %q", out)
	}
	select {
	case c := <-codes:
		if c != 3 {
			t.Fatalf("OnExit got %d", c)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OnExit was not called")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if code, err := m.Exited(ctx, in.ID); err != nil || code != 3 {
		t.Fatalf("the terminal did not end with its program: code %d, %v", code, err)
	}
	if _, err := m.Exited(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("unknown terminal: %v", err)
	}
	select {
	case c := <-codes:
		t.Fatalf("OnExit ran twice (%d)", c)
	default:
	}
}

// Closing the terminal while its program still runs kills it and still tells OnExit (the sign-in's clean-up hangs on it), and
// CloseAll returns only after every OnExit ran.
// The output of a program that ends is still in the pseudo-terminal when the process is reaped. A reader that stops at the exit
// (the sign-in terminal does) must have all of it: the exit is reported once the output has been read.
func TestAProgramsLastOutputIsThereWhenItsExitIsReported(t *testing.T) {
	m := &Manager{Shell: sh}
	defer m.CloseAll()
	for i := 0; i < 25; i++ {
		in, err := m.OpenProgram(t.TempDir(), "burst", 100, 30, Program{Path: "/bin/sh", Args: []string{"-c", "i=0; while [ $i -lt 800 ]; do echo line-$i; i=$((i+1)); done; echo the-end-marker; exit 3"}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		code, err := m.Exited(ctx, in.ID)
		cancel()
		if err != nil || code != 3 {
			t.Fatalf("run %d: code %d, %v", i, code, err)
		}
		c, err := m.Read(context.Background(), in.ID, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !c.Exited || !strings.Contains(string(c.Data), "the-end-marker") || c.Dropped != 0 {
			t.Fatalf("run %d: the exit was reported before the last output was read (%d bytes, dropped %d, ends %q)", i, len(c.Data), c.Dropped, tail(string(c.Data), 40))
		}
		_ = m.Close(in.ID)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func TestOnExitRunsWhenTheTerminalIsClosed(t *testing.T) {
	m := &Manager{Shell: sh}
	ran := make(chan int, 1)
	var finished atomic.Bool
	in, err := m.OpenProgram(t.TempDir(), "x", 80, 24, Program{Path: "/bin/sh", Args: []string{"-c", "echo ready; sleep 600"}, OnExit: func(code int) { ran <- code }})
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, m, in.ID, 0, "ready")
	if err := m.Close(in.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(10 * time.Second):
		t.Fatal("closing the terminal did not reach OnExit")
	}

	in, err = m.OpenProgram(t.TempDir(), "y", 80, 24, Program{Path: "/bin/sh", Args: []string{"-c", "echo ready; sleep 600"}, OnExit: func(int) {
		time.Sleep(300 * time.Millisecond)
		finished.Store(true)
	}})
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, m, in.ID, 0, "ready")
	m.CloseAll()
	if !finished.Load() {
		t.Fatal("CloseAll returned before the program's OnExit finished")
	}
}

// The limit counts terminals whose program still runs; ended ones keep their output for the person until there are more than
// the limit of them, then the oldest is forgotten.
func TestTheLimitCountsRunningTerminals(t *testing.T) {
	m := &Manager{Shell: sh, Max: 2}
	defer m.CloseAll()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var ids []string
	for i := 0; i < 3; i++ {
		in, err := m.OpenProgram(t.TempDir(), "done", 80, 24, Program{Path: "/bin/sh", Args: []string{"-c", "exit 0"}})
		if err != nil {
			t.Fatalf("terminal %d: %v (an ended terminal must not count)", i, err)
		}
		if _, err := m.Exited(ctx, in.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, in.ID)
	}
	if _, err := m.get(ids[0]); err != ErrNotFound {
		t.Fatalf("the oldest ended terminal was kept past the limit: %v", err)
	}
	if _, err := m.get(ids[2]); err != nil {
		t.Fatalf("the newest ended terminal was dropped: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := m.Open(t.TempDir(), "", 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Open(t.TempDir(), "", 0, 0); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("a third running terminal: %v", err)
	}
}

func TestOpenProgramNeedsAProgramItCanFind(t *testing.T) {
	m := &Manager{}
	if _, err := m.OpenProgram(t.TempDir(), "x", 80, 24, Program{}); err == nil {
		t.Fatal("no program")
	}
	if _, err := m.OpenProgram(t.TempDir(), "x", 80, 24, Program{Path: "drydock-no-such-program"}); err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Fatalf("missing program: %v", err)
	}
	// a relative path is the process's, never the terminal folder's
	got, err := resolve(filepath.Join("bin", "tool"))
	wd, _ := os.Getwd()
	if err != nil || got != filepath.Join(wd, "bin", "tool") {
		t.Fatalf("relative: %q %v", got, err)
	}
}
