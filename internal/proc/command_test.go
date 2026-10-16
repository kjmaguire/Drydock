package proc

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHardenStopsWaitingForPipesAfterTheContextEnds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	cmd := exec.Command("sh", "-c", "sleep 30 & sleep 30")
	Harden(cmd)
	if cmd.WaitDelay != pipeGrace {
		t.Fatalf("WaitDelay %v", cmd.WaitDelay)
	}
	// a short grace for the test: the command's child holds the output pipe long after the command itself is killed
	cmd.WaitDelay = 200 * time.Millisecond
	var out CapWriter
	out.Max = 1 << 10
	cmd.Stdout = &out
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(200*time.Millisecond, func() { _ = cmd.Process.Kill() })
	_ = cmd.Wait()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Wait returned after %v: a child holding the pipe kept it waiting", d)
	}
}

func TestHardenKeepsAWaitDelayTheCallerSet(t *testing.T) {
	cmd := exec.Command("true")
	cmd.WaitDelay = time.Minute
	Harden(cmd)
	if cmd.WaitDelay != time.Minute {
		t.Fatalf("WaitDelay %v", cmd.WaitDelay)
	}
}

func TestCapWriterKeepsTheFirstBytesAndCallsOverOnce(t *testing.T) {
	calls := 0
	w := &CapWriter{Max: 10, Over: func() { calls++ }}
	for _, s := range []string{"abcd", "efgh", "ijklmnop", "qrst"} {
		if n, err := w.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v: a command must be told all its output was taken", s, n, err)
		}
	}
	if w.String() != "abcdefghij" || !w.Exceeded() || calls != 1 {
		t.Fatalf("kept %q exceeded %v over called %d times", w.String(), w.Exceeded(), calls)
	}
	small := &CapWriter{Max: 10}
	_, _ = small.Write([]byte(strings.Repeat("x", 10)))
	if small.Exceeded() || small.String() != strings.Repeat("x", 10) {
		t.Fatal("exactly Max bytes is not over")
	}
}

func TestHeadTailKeepsTheStartAndTheEndOfWhatIsWritten(t *testing.T) {
	ht := &HeadTail{Head: 10, Tail: 10}
	// it fits: nothing is cut, however it was written
	for _, s := range []string{"0123", "4567", "89abcde", "f"} {
		_, _ = ht.Write([]byte(s))
	}
	if text, total, cut := ht.Text(); text != "0123456789abcdef" || total != 16 || cut {
		t.Fatalf("%q %d %v", text, total, cut)
	}
	// it does not: the first 10 bytes, a note of what was left out, the last 10
	big := &HeadTail{Head: 10, Tail: 10}
	var all strings.Builder
	for i := 0; i < 100; i++ {
		s := strings.Repeat(string(rune('a'+i%26)), 7)
		all.WriteString(s)
		_, _ = big.Write([]byte(s))
	}
	text, total, cut := big.Text()
	want := all.String()
	if !cut || total != int64(len(want)) || !strings.HasPrefix(text, want[:10]) || !strings.HasSuffix(text, want[len(want)-10:]) || !strings.Contains(text, "680 bytes left out") {
		t.Fatalf("%q %d %v", text, total, cut)
	}
	// one write larger than the tail, and the ring wrapping
	one := &HeadTail{Head: 4, Tail: 6}
	_, _ = one.Write([]byte("abcdefghijklmnopqrstuvwxyz"))
	if text, _, _ := one.Text(); !strings.HasPrefix(text, "abcd") || !strings.HasSuffix(text, "uvwxyz") {
		t.Fatalf("%q", text)
	}
	wrap := &HeadTail{Head: 0, Tail: 5}
	for _, s := range []string{"12", "345", "67", "8"} {
		_, _ = wrap.Write([]byte(s))
	}
	if text, _, _ := wrap.Text(); !strings.HasSuffix(text, "45678") {
		t.Fatalf("%q", text)
	}
}

// A line at a time, for a long output: the cost of a write does not grow with the size of the tail.
func TestHeadTailIsCheapForManyShortWrites(t *testing.T) {
	ht := &HeadTail{Head: 1 << 20, Tail: 4 << 20}
	start := time.Now()
	line := []byte(strings.Repeat("x", 80) + "\n")
	for i := 0; i < 400_000; i++ { // 32 MB
		_, _ = ht.Write(line)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("400,000 short writes took %v", d)
	}
	if text, total, cut := ht.Text(); !cut || total != 400_000*81 || len(text) > 5<<20+100 {
		t.Fatalf("%d bytes kept of %d, cut %v", len(text), total, cut)
	}
}
