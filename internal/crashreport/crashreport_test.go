package crashreport_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/crashreport"
)

// TestMain doubles as the crashing program: with DRYDOCK_CRASH_TEST set, the test binary arms crash output in that folder and
// dies the way Drydock can, of a panic on a goroutine nothing recovers.
func TestMain(m *testing.M) {
	if dir := os.Getenv("DRYDOCK_CRASH_TEST"); dir != "" {
		a, err := crashreport.Arm(dir)
		if err != nil {
			panic(err)
		}
		// crash reports turned off for this run (decision P121), or turned off and back on
		switch os.Getenv("DRYDOCK_CRASH_MODE") {
		case "paused":
			if err := crashreport.Pause(); err != nil {
				panic(err)
			}
		case "resumed":
			if err := crashreport.Pause(); err != nil {
				panic(err)
			}
			if err := crashreport.Resume(a.Path()); err != nil {
				panic(err)
			}
		}
		go func() {
			panic("token=sk-ant-api03-" + strings.Repeat("a", 90) + " while doing something")
		}()
		select {} // the panic ends the process
	}
	os.Exit(m.Run())
}

func crashOnce(t *testing.T, dir string) { crashWith(t, dir, "") }

func crashWith(t *testing.T, dir, mode string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "DRYDOCK_CRASH_TEST="+dir, "DRYDOCK_CRASH_MODE="+mode)
	if err := cmd.Run(); err == nil {
		t.Fatal("the crashing process exited cleanly")
	}
}

// A process that dies of an unrecovered panic leaves its crash output in the data folder; the next start makes a report of it,
// with its secrets redacted, and the Support page and support bundles can read it.
func TestACrashLeavesAReportForTheNextStart(t *testing.T) {
	dir := t.TempDir()
	crashOnce(t, dir)

	redact := func(s string) string {
		return strings.ReplaceAll(s, "sk-ant-api03-"+strings.Repeat("a", 90), "[REDACTED:anthropic]")
	}
	self, err := crashreport.Arm(dir) // this start's own file is never collected
	if err != nil {
		t.Fatal(err)
	}
	defer self.Disarm()
	made, err := crashreport.Collect(dir, self.Path(), redact, "0.1.0-beta.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 1 {
		t.Fatalf("made %d reports, want 1", len(made))
	}
	r := made[0]
	if !strings.HasPrefix(r.Summary, "panic: ") || !strings.Contains(r.Summary, "while doing something") {
		t.Fatalf("summary %q", r.Summary)
	}
	text, err := crashreport.Read(dir, r.Name)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "sk-ant-api03-") || !strings.Contains(text, "[REDACTED:anthropic]") {
		t.Fatal("the report holds the secret")
	}
	if !strings.Contains(text, "goroutine ") || !strings.Contains(text, "0.1.0-beta.1") {
		t.Fatalf("the report has no stacks or version:\n%s", text)
	}
	// collected once: the pending file is gone, this start's own is still there
	if again, _ := crashreport.Collect(dir, self.Path(), redact, "x"); len(again) != 0 {
		t.Fatalf("collected again: %v", again)
	}
	if _, err := os.Stat(self.Path()); err != nil {
		t.Fatalf("this start's crash file was touched: %v", err)
	}
	list, _ := crashreport.List(dir)
	if len(list) != 1 || list[0].Name != r.Name {
		t.Fatalf("list %v", list)
	}
}

// A normal exit leaves nothing behind, and only the newest Keep reports are kept.
func TestANormalExitLeavesNoReportAndOldReportsGo(t *testing.T) {
	dir := t.TempDir()
	a, err := crashreport.Arm(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.Disarm()
	if ents, _ := os.ReadDir(crashreport.Dir(dir)); len(ents) != 0 {
		t.Fatalf("a normal exit left %d files", len(ents))
	}
	for i := 0; i < crashreport.Keep+3; i++ {
		name := filepath.Join(crashreport.Dir(dir), "pending-2026010"+string(rune('0'+i%10))+"T00000"+string(rune('0'+i/10))+"Z-1.crash")
		if err := os.WriteFile(name, []byte("fatal error: concurrent map writes\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := crashreport.Collect(dir, "", nil, "v"); err != nil {
		t.Fatal(err)
	}
	list, _ := crashreport.List(dir)
	if len(list) != crashreport.Keep {
		t.Fatalf("kept %d reports, want %d", len(list), crashreport.Keep)
	}
	if list[0].Summary != "fatal error: concurrent map writes" {
		t.Fatalf("summary %q", list[0].Summary)
	}
	if _, err := crashreport.Read(dir, "../drydock.db"); err == nil {
		t.Fatal("a path was read")
	}
}
