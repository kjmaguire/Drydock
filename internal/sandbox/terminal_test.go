package sandbox

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// ShellJoin keeps each word of an ACP terminal request one word for sh (decision P131): spaces, quotes and the shell's own
// characters reach the command as they were given.
func TestShellJoinQuotesEachWord(t *testing.T) {
	got, err := ShellJoin([]string{"go", "test", "-run", "Test A|B", "it's", "$(id)", "plain-./=:@,+%"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `go test -run 'Test A|B' 'it'"'"'s' '$(id)' plain-./=:@,+%`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if _, err := ShellJoin(nil); err == nil {
		t.Fatal("an empty command is refused")
	}
	if _, err := ShellJoin([]string{"echo", "a\x00b"}); err == nil {
		t.Fatal("a NUL is refused")
	}
}

// lockedBuf is an io.Writer a test reads while the command still writes to it.
type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *lockedBuf) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// An ACP terminal reads a command's output while it runs (ExecRequest.Output), and its quoted arguments arrive whole. Real
// engine only (skipped without one).
func TestExecStreamsOutputWhileTheCommandRuns(t *testing.T) {
	e := dockerOrSkip(t)
	img := buildTestImages(t, e)
	m, b, _ := newTestBox(t, e, img, Config{Image: img.Base, Network: NetworkNone})
	line, err := ShellJoin([]string{"sh", "-c", `echo "first: $1"; sleep 3; echo second`, "x", "it's a test"})
	if err != nil {
		t.Fatal(err)
	}
	out := &lockedBuf{}
	done := make(chan ExecResult, 1)
	go func() {
		res, _ := m.Exec(context.Background(), b, ExecRequest{Command: line, Output: out})
		done <- res
	}()
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(out.String(), "first: it's a test") {
		if time.Now().After(deadline) {
			t.Fatalf("no output while the command runs: %q", out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Contains(out.String(), "second") {
		t.Fatal("the output was read only at the end")
	}
	res := <-done
	if res.ExitCode != 0 || !strings.Contains(out.String(), "second") || !strings.Contains(res.Output, "second") {
		t.Fatalf("%+v %q", res, out.String())
	}
}
