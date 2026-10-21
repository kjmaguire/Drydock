package claude

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
)

// pipeProc is a LABELLED TEST DOUBLE of a process: its stdout is a pipe the test writes to, closing its stdin ends it.
type pipeProc struct {
	stdout *io.PipeReader
	w      *io.PipeWriter
	exit   chan proc.ExitInfo
	once   sync.Once
}

func newPipeProc() *pipeProc {
	r, w := io.Pipe()
	return &pipeProc{stdout: r, w: w, exit: make(chan proc.ExitInfo, 1)}
}

func (p *pipeProc) end() { p.once.Do(func() { _ = p.w.Close(); p.exit <- proc.ExitInfo{} }) }

func (p *pipeProc) PID() int                     { return 1 }
func (p *pipeProc) Stdin() io.WriteCloser        { return stdinEnds{p} }
func (p *pipeProc) Stdout() io.Reader            { return p.stdout }
func (p *pipeProc) Stderr() io.Reader            { return eofReader{} }
func (p *pipeProc) Signal(sig proc.Signal) error { return nil }
func (p *pipeProc) Kill(tree bool) error         { p.end(); return nil }
func (p *pipeProc) Wait() <-chan proc.ExitInfo   { return p.exit }

type stdinEnds struct{ p *pipeProc }

func (s stdinEnds) Write(b []byte) (int, error) { return len(b), nil }
func (s stdinEnds) Close() error                { s.p.end(); return nil }

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

// A session nobody reads any more does not hold its stdout reader (audit 2026-10-16, L26): with more output than the buffer holds
// and no reader of Output(), Stop used to leave the goroutine, and the pipe behind it, waiting for good.
func TestAStoppedSessionDoesNotHoldItsStdoutReader(t *testing.T) {
	p := newPipeProc()
	s := newSession(p, agents.RunSpec{}, nil, nil)
	go func() {
		for i := 0; i < 1000; i++ {
			if _, err := fmt.Fprintf(p.w, "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"line %d\"}]}}\n", i); err != nil {
				return
			}
		}
	}()
	// the buffer fills (256 chunks) and the reader waits for room that nobody will make
	deadline := time.Now().Add(5 * time.Second)
	for len(s.out) < cap(s.out) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(s.out) != cap(s.out) {
		t.Fatalf("the buffer did not fill: %d of %d", len(s.out), cap(s.out))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.readDone:
	case <-time.After(3 * time.Second):
		t.Fatal("the stdout reader still waits for a reader of Output() after Stop")
	}
	// what was already in the buffer is still there for a late reader, and the channel is closed after it
	n := 0
	for range s.Output() {
		n++
	}
	if n != cap(s.out) {
		t.Fatalf("a late reader found %d chunks, want the %d that were buffered", n, cap(s.out))
	}
}

// While someone still reads, every chunk arrives, in order, whatever the buffer holds.
func TestASessionBeingReadLosesNoOutput(t *testing.T) {
	p := newPipeProc()
	s := newSession(p, agents.RunSpec{}, nil, nil)
	const total = 1000
	go func() {
		for i := 0; i < total; i++ {
			fmt.Fprintf(p.w, "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"line %d\"}]}}\n", i)
		}
		p.end()
	}()
	got := 0
	timeout := time.After(10 * time.Second)
	for got < total {
		select {
		case c, ok := <-s.Output():
			if !ok {
				t.Fatalf("the output ended after %d chunks", got)
			}
			var d struct {
				Text string `json:"text"`
			}
			if c.Decode(&d) != nil || d.Text != fmt.Sprintf("line %d", got) {
				t.Fatalf("chunk %d: %+v", got, d)
			}
			got++
		case <-timeout:
			t.Fatalf("only %d of %d chunks arrived", got, total)
		}
	}
}
