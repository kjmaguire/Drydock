package proc

import (
	"bytes"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// pipeGrace is how long a command that was cancelled or timed out may keep its output pipes open before Run stops waiting for
// them. Killing a program kills it and not what it started: a git that was fetching over ssh leaves the ssh holding the pipe,
// and Run would not return until that exits (audit 2026-10-16, M6).
const pipeGrace = 5 * time.Second

// Harden prepares a command the app starts itself instead of through a Manager (git, gh, the container client, fsutil):
// on Windows it opens no console window (Drydock is a windowed app, and every console program it starts would flash one), and
// it stops waiting for output pipes pipeGrace after its context ends. The caller still sets the environment.
func Harden(cmd *exec.Cmd) {
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = pipeGrace
	}
	hideWindow(cmd)
}

// CapWriter collects what a command prints, up to Max bytes. Past that it discards the rest, remembers that it did and calls
// Over once, so the owner can end the command instead of letting a runaway producer fill memory. It is safe for the two
// goroutines a command's pipes use.
type CapWriter struct {
	Max  int
	Over func()

	mu   sync.Mutex
	buf  bytes.Buffer
	over bool
}

// Write implements io.Writer. It never fails: a command must not be told its output went nowhere.
func (w *CapWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	room := w.Max - w.buf.Len()
	if room >= len(p) {
		w.buf.Write(p)
		w.mu.Unlock()
		return len(p), nil
	}
	if room > 0 {
		w.buf.Write(p[:room])
	}
	first := !w.over
	w.over = true
	w.mu.Unlock()
	if first && w.Over != nil {
		w.Over()
	}
	return len(p), nil
}

// String is what was kept.
func (w *CapWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// Exceeded reports whether more than Max bytes were written.
func (w *CapWriter) Exceeded() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.over
}

// HeadTail keeps the first Head and the last Tail bytes of what is written to it and counts all of it: the start of a command's
// output says what it was doing and the end says how it failed, and what is between is the part nobody reads. It is safe for the
// two goroutines a command's pipes use. The tail is a ring, so a write costs what it carries and not the size of the tail.
type HeadTail struct {
	Head, Tail int

	mu    sync.Mutex
	head  []byte
	ring  []byte
	pos   int
	full  bool
	total int64
}

// Write implements io.Writer and never fails.
func (h *HeadTail) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total += int64(len(p))
	rest := p
	if room := h.Head - len(h.head); room > 0 {
		n := min(room, len(rest))
		h.head = append(h.head, rest[:n]...)
		rest = rest[n:]
	}
	if len(rest) == 0 || h.Tail <= 0 {
		return len(p), nil
	}
	if h.ring == nil {
		h.ring = make([]byte, h.Tail)
	}
	if len(rest) >= h.Tail {
		copy(h.ring, rest[len(rest)-h.Tail:])
		h.pos, h.full = 0, true
		return len(p), nil
	}
	n := copy(h.ring[h.pos:], rest)
	if n < len(rest) {
		copy(h.ring, rest[n:])
		h.pos, h.full = len(rest)-n, true
	} else if h.pos += n; h.pos == len(h.ring) {
		h.pos, h.full = 0, true
	}
	return len(p), nil
}

// Text returns what was kept: the whole output when it fitted, else the head, a line saying how much was left out, and the tail.
// total is the size of all the output, and cut is true when part of it is not in the text.
func (h *HeadTail) Text() (text string, total int64, cut bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var tail []byte
	switch {
	case h.ring == nil:
	case h.full:
		tail = append(append(tail, h.ring[h.pos:]...), h.ring[:h.pos]...)
	default:
		tail = h.ring[:h.pos]
	}
	kept := int64(len(h.head) + len(tail))
	if kept == h.total {
		return string(h.head) + string(tail), h.total, false
	}
	return string(h.head) + "\n[... " + strconv.FormatInt(h.total-kept, 10) + " bytes left out ...]\n" + string(tail), h.total, true
}
