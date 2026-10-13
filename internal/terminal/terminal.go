// Package terminal runs the human's interactive shells for the bottom dock (docs/ui.md 6.5): a shell on a pseudo-terminal
// (ConPTY on Windows through go-pty), its output kept in a bounded buffer in memory, read by offset. Output is never written
// to the event log or the database: a terminal is where the human types, and what scrolls past can hold secrets. Closing a
// terminal kills the shell and everything it started.
package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"

	"github.com/kjmaguire/drydock/internal/proc"
)

// Keep is how much output a terminal keeps for a reader that falls behind; older output is dropped and counted.
const Keep = 1 << 20

// maxRead bounds one read so a burst does not become one huge reply.
const maxRead = 256 << 10

// ErrNotFound is returned for an id that is not an open terminal.
var ErrNotFound = errors.New("terminal: no such terminal")

// Info describes an open terminal.
type Info struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Cwd       string    `json:"cwd"`
	Shell     string    `json:"shell"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	StartedAt time.Time `json:"startedAt"`
	Exited    bool      `json:"exited"`
	ExitCode  int       `json:"exitCode"`
}

// Chunk is output read from an offset. Next is the offset to read from next; Dropped counts bytes that were no longer kept.
type Chunk struct {
	Data     []byte `json:"data"`
	Next     int64  `json:"next"`
	Dropped  int64  `json:"dropped,omitempty"`
	Exited   bool   `json:"exited"`
	ExitCode int    `json:"exitCode"`
}

// Manager owns the open terminals.
type Manager struct {
	// Max is the number of terminals that may be open at once (default 8).
	Max int
	// Shell chooses the program and arguments; nil means DefaultShell.
	Shell func() (string, []string)

	mu    sync.Mutex
	terms map[string]*term
}

type term struct {
	info Info
	pty  pty.Pty
	cmd  *pty.Cmd
	kill func() error

	mu      sync.Mutex
	buf     []byte
	start   int64 // offset of buf[0]
	changed chan struct{}
	exited  bool
	code    int
}

// DefaultShell is PowerShell (pwsh when installed) on Windows and $SHELL or /bin/sh elsewhere.
func DefaultShell() (string, []string) {
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("pwsh.exe"); err == nil {
			return p, []string{"-NoLogo"}
		}
		if p, err := exec.LookPath("powershell.exe"); err == nil {
			return p, []string{"-NoLogo"}
		}
		if c := os.Getenv("COMSPEC"); c != "" {
			return c, nil
		}
		return "cmd.exe", nil
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s, nil
	}
	return "/bin/sh", nil
}

// Open starts a shell in dir.
func (m *Manager) Open(dir, title string, cols, rows int) (Info, error) {
	shell, args := DefaultShell()
	if m.Shell != nil {
		shell, args = m.Shell()
	}
	return m.open(dir, title, cols, rows, shell, args)
}

// OpenProgram starts program with args in dir instead of a shell: a command the person watches and answers in the terminal
// (an agent CLI's own sign-in). The terminal ends when the program exits.
func (m *Manager) OpenProgram(dir, title string, cols, rows int, program string, args ...string) (Info, error) {
	if program == "" {
		return Info{}, errors.New("terminal: no program to run")
	}
	return m.open(dir, title, cols, rows, program, args)
}

func (m *Manager) open(dir, title string, cols, rows int, shell string, args []string) (Info, error) {
	m.mu.Lock()
	if m.terms == nil {
		m.terms = map[string]*term{}
	}
	max := m.Max
	if max <= 0 {
		max = 8
	}
	if len(m.terms) >= max {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("terminal: %d terminals are open; close one first", max)
	}
	m.mu.Unlock()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return Info{}, fmt.Errorf("terminal: %s is not a folder", dir)
	}
	cols, rows = clamp(cols, 20, 500, 80), clamp(rows, 5, 200, 24)
	p, err := pty.New()
	if err != nil {
		return Info{}, fmt.Errorf("terminal: %w", err)
	}
	if err := p.Resize(cols, rows); err != nil {
		p.Close()
		return Info{}, fmt.Errorf("terminal: resize: %w", err)
	}
	cmd := p.Command(shell, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	if err := cmd.Start(); err != nil {
		p.Close()
		return Info{}, fmt.Errorf("terminal: start %s: %w", shell, err)
	}
	kill, err := proc.AttachTree(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		p.Close()
		return Info{}, fmt.Errorf("terminal: %w", err)
	}
	t := &term{pty: p, cmd: cmd, kill: kill, changed: make(chan struct{}),
		info: Info{ID: newID(), Title: title, Cwd: dir, Shell: shell, Cols: cols, Rows: rows, StartedAt: time.Now().UTC()}}
	// on Unix the parent's copy of the terminal's slave end would keep reads from ever ending
	if u, ok := p.(pty.UnixPty); ok {
		_ = u.Slave().Close()
	}
	go t.pump()
	go t.wait()
	m.mu.Lock()
	m.terms[t.info.ID] = t
	m.mu.Unlock()
	return t.info, nil
}

func (t *term) pump() {
	b := make([]byte, 32<<10)
	for {
		n, err := t.pty.Read(b)
		if n > 0 {
			t.mu.Lock()
			t.buf = append(t.buf, b[:n]...)
			if over := len(t.buf) - Keep; over > 0 {
				t.buf = append(t.buf[:0:0], t.buf[over:]...)
				t.start += int64(over)
			}
			t.notifyLocked()
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// wait records the shell's exit. It does not wait for the output to end: a ConPTY keeps its output pipe open until it is
// closed, so the end of the output never signals the exit.
func (t *term) wait() {
	code := 0
	if err := t.cmd.Wait(); err != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	t.mu.Lock()
	t.exited, t.code = true, code
	t.notifyLocked()
	t.mu.Unlock()
}

func (t *term) notifyLocked() {
	close(t.changed)
	t.changed = make(chan struct{})
}

func (m *Manager) get(id string) (*term, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.terms[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}

// Exited waits until the terminal's program has ended and returns its exit code.
func (m *Manager) Exited(ctx context.Context, id string) (int, error) {
	t, err := m.get(id)
	if err != nil {
		return 0, err
	}
	for {
		t.mu.Lock()
		if t.exited {
			code := t.code
			t.mu.Unlock()
			return code, nil
		}
		ch := t.changed
		t.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// Read returns output from offset from, waiting up to wait for some to arrive (a long poll).
func (m *Manager) Read(ctx context.Context, id string, from int64, wait time.Duration) (Chunk, error) {
	t, err := m.get(id)
	if err != nil {
		return Chunk{}, err
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		t.mu.Lock()
		end := t.start + int64(len(t.buf))
		if from < end || t.exited || wait <= 0 {
			c := Chunk{Exited: t.exited, ExitCode: t.code, Next: end}
			if from < t.start {
				c.Dropped, from = t.start-from, t.start
			}
			if from > end {
				from = end
			}
			data := t.buf[from-t.start:]
			if len(data) > maxRead {
				data = data[:maxRead]
			}
			c.Data = append([]byte(nil), data...)
			c.Next = from + int64(len(data))
			t.mu.Unlock()
			return c, nil
		}
		ch := t.changed
		t.mu.Unlock()
		select {
		case <-ch:
		case <-deadline.C:
			wait = 0
		case <-ctx.Done():
			return Chunk{}, ctx.Err()
		}
	}
}

// Write sends input to the shell.
func (m *Manager) Write(id string, data []byte) error {
	t, err := m.get(id)
	if err != nil {
		return err
	}
	_, err = t.pty.Write(data)
	return err
}

// Resize changes the terminal size.
func (m *Manager) Resize(id string, cols, rows int) error {
	t, err := m.get(id)
	if err != nil {
		return err
	}
	cols, rows = clamp(cols, 20, 500, 80), clamp(rows, 5, 200, 24)
	if err := t.pty.Resize(cols, rows); err != nil {
		return err
	}
	m.mu.Lock()
	t.info.Cols, t.info.Rows = cols, rows
	m.mu.Unlock()
	return nil
}

// Close kills the shell and everything it started, and forgets the terminal.
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	t, ok := m.terms[id]
	delete(m.terms, id)
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	err := t.kill()
	_ = t.pty.Close()
	return err
}

// CloseAll closes every terminal (on shutdown).
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.terms))
	for id := range m.terms {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Close(id)
	}
}

// List returns the open terminals, oldest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.terms))
	for _, t := range m.terms {
		in := t.info
		t.mu.Lock()
		in.Exited, in.ExitCode = t.exited, t.code
		t.mu.Unlock()
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	return min(max(v, lo), hi)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "term_" + hex.EncodeToString(b)
}
