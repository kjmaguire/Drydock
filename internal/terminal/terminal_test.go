//go:build !windows

package terminal

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
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
