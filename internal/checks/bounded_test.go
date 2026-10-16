package checks

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
)

// spammer is a module whose test binary prints a lot: 80 MB in lines of a kilobyte, a failing test's name and a last line.
func spammer(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module spam\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nvar _ = strings.Repeat\n\nfunc main() {\n"+body+"\n}\n"), 0o644)
	return dir
}

// A check that prints without end used to be kept whole in memory and written whole to its log (audit 2026-10-16, M6).
func TestACheckThatPrintsWithoutEndKeepsItsStartItsEndAndAFileThatStopsGrowing(t *testing.T) {
	dir := spammer(t, `	line := strings.Repeat("x", 1000)
	fmt.Println("FIRST LINE")
	for i := 0; i < 80000; i++ {
		fmt.Println(line, i)
	}
	fmt.Println("--- FAIL: TestTheLastOne")
	fmt.Println("FINAL LINE")`)
	res, err := Run(context.Background(), proc.New(), Spec{Name: "spam", Argv: []string{"go", "run", "."}, Dir: dir, Parser: "go", LogDir: t.TempDir(), Timeout: 2 * time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed {
		t.Fatalf("the program finished and the check did not pass: %+v", res)
	}
	if !strings.Contains(res.Output, "FIRST LINE") || !strings.Contains(res.Output, "FINAL LINE") || !strings.Contains(res.Output, "bytes omitted") {
		t.Fatalf("the start and the end of the output are kept, and the cut says so: %.300s ... %s", res.Output, res.Output[max(0, len(res.Output)-200):])
	}
	if len(res.FailingTests) != 1 || res.FailingTests[0] != "TestTheLastOne" {
		t.Fatalf("the failing test, named near the end of 80 MB, was not found: %v", res.FailingTests)
	}
	st, err := os.Stat(res.LogRef)
	if err != nil || st.Size() > maxLogBytes+4096 {
		t.Fatalf("the log is %d bytes (limit %d): %v", st.Size(), maxLogBytes, err)
	}
	b, _ := os.ReadFile(res.LogRef)
	if !strings.Contains(string(b), "the log stops here") || !strings.HasPrefix(string(b), "FIRST LINE") {
		t.Fatal("the log does not say where it stops, or does not start where the output does")
	}
}

// A line longer than the scanner takes stopped the reading, and the command then blocked on a full pipe until it timed out.
func TestALineLongerThanTheScannerDoesNotHangTheCheck(t *testing.T) {
	dir := spammer(t, `	fmt.Println(strings.Repeat("y", 20<<20))
	fmt.Println("AFTER THE LONG LINE")`)
	start := time.Now()
	res, err := Run(context.Background(), proc.New(), Spec{Name: "long", Argv: []string{"go", "run", "."}, Dir: dir, LogDir: t.TempDir(), Timeout: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.TimedOut || !res.Passed {
		t.Fatalf("the check hung on a long line: %+v after %v", res, time.Since(start))
	}
}

func TestTheBoundedLogAndTheFailureCollectorCountWhatTheyKeep(t *testing.T) {
	var sb strings.Builder
	l := &logFile{f: &sb, max: 100}
	for i := 0; i < 50; i++ {
		l.line("0123456789")
	}
	if got := sb.String(); strings.Count(got, "0123456789") != 9 || !strings.Contains(got, "the log stops here") || len(got) > 300 {
		t.Fatalf("%q", got)
	}
	f := &failing{parser: "go"}
	for i := 0; i < maxFailNames+100; i++ {
		f.add("--- FAIL: TestN" + strings.Repeat("x", i%7) + string(rune('A'+i%26)) + strings.Repeat("y", i/26))
	}
	if n := len(f.names()); n == 0 || n > maxFailNames {
		t.Fatalf("%d names", n)
	}
	none := &failing{}
	none.add("--- FAIL: TestX")
	if none.names() != nil {
		t.Fatal("a check without a parser collects names")
	}
}

// A URL the preview command printed is probed only when it names this computer, and a probe of a local server does not follow a
// redirect to another computer (audit 2026-10-16, L11).
func TestOnlyALoopbackURLTheCommandPrintedIsProbed(t *testing.T) {
	for in, want := range map[string]string{
		"http://localhost:5173/":     "http://localhost:5173/",
		"http://127.0.0.1:3000":      "http://127.0.0.1:3000",
		"http://127.1.2.3:8080/x?y":  "http://127.1.2.3:8080/x?y",
		"http://[::1]:4000/":         "http://[::1]:4000/",
		"https://app.localhost:8443": "https://app.localhost:8443",
		"http://0.0.0.0:8000/":       "http://127.0.0.1:8000/",
		"http://[::]:8000/":          "http://[::1]:8000/",
	} {
		got, err := loopbackProbeURL(in)
		if err != nil || got != want {
			t.Errorf("loopbackProbeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"http://192.168.1.5:5173/", "http://10.0.0.7/", "http://169.254.169.254/latest/meta-data/", "http://metadata.google.internal/",
		"https://example.com/", "http://localhost.evil.example/", "http://127.0.0.1.nip.io/", "http://[fd00::1]/", "ftp://localhost/", "localhost:3000", "",
	} {
		if got, err := loopbackProbeURL(in); err == nil {
			t.Errorf("loopbackProbeURL(%q) = %q, want a refusal", in, got)
		}
	}
}

func TestAProbeOfALocalServerDoesNotFollowARedirectToAnotherComputer(t *testing.T) {
	get := func(raw string) *http.Request { r, _ := http.NewRequest("GET", raw, nil); return r }
	local, other := get("http://127.0.0.1:3000/"), get("http://203.0.113.9/")
	if err := refuseLeavingLoopback(other, []*http.Request{local}); err == nil {
		t.Error("a redirect from a local server to another computer was followed")
	}
	if err := refuseLeavingLoopback(get("http://localhost:3000/login"), []*http.Request{local}); err != nil {
		t.Errorf("a redirect within this computer was refused: %v", err)
	}
	// a URL the person set as preview.url on another computer may redirect where it likes: they chose where to look
	if err := refuseLeavingLoopback(get("https://other.example/"), []*http.Request{other}); err != nil {
		t.Errorf("a redirect from a remote preview.url was refused: %v", err)
	}
}
