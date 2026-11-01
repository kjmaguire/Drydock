package crashreport_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/crashreport"
)

func pending(t *testing.T, dir string) []string {
	t.Helper()
	ents, _ := os.ReadDir(crashreport.Dir(dir))
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "pending-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func size(t *testing.T, dir, name string) int64 {
	t.Helper()
	st, err := os.Stat(filepath.Join(crashreport.Dir(dir), name))
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

// Crash reports turned off (decision P121): a process that paused its crash output dies and leaves an empty file; one that turned
// them back on leaves its crash as before.
func TestPausedCrashOutputKeepsNothingAndResumedKeepsTheCrash(t *testing.T) {
	off := t.TempDir()
	crashWith(t, off, "paused")
	ps := pending(t, off)
	if len(ps) != 1 || size(t, off, ps[0]) != 0 {
		t.Fatalf("a paused process's crash output: %v", ps)
	}
	on := t.TempDir()
	crashWith(t, on, "resumed")
	ps = pending(t, on)
	if len(ps) != 1 || size(t, on, ps[0]) == 0 {
		t.Fatalf("a resumed process's crash output: %v", ps)
	}
	if err := crashreport.Resume(""); err != nil {
		t.Fatalf("a process that was never armed: %v", err)
	}
}

// Discard is what the next start does while crash reports are off: the crash output goes, unread, and no report is made.
func TestDiscardRemovesCrashOutputWithoutMakingAReport(t *testing.T) {
	dir := t.TempDir()
	crashOnce(t, dir)
	self, err := crashreport.Arm(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer self.Disarm()
	n, err := crashreport.Discard(dir, self.Path())
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if ps := pending(t, dir); len(ps) != 1 || filepath.Join(crashreport.Dir(dir), ps[0]) != self.Path() {
		t.Fatalf("only this start's own file is left: %v", ps)
	}
	if rs, _ := crashreport.List(dir); len(rs) != 0 {
		t.Fatalf("no report: %+v", rs)
	}
	if n, err := crashreport.Discard(t.TempDir(), ""); n != 0 || err != nil {
		t.Fatalf("no crashes folder: %d %v", n, err)
	}
}
