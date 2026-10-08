package checks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
)

// These tests run the real Go toolchain against a real fixture module.

func fixture(t *testing.T, addBody string, testBody string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "calc.go"), []byte("package fixture\n\nfunc Add(a, b int) int { "+addBody+" }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "calc_test.go"), []byte("package fixture\n\nimport \"testing\"\n\n"+testBody), 0o644)
	return dir
}

const goodTests = `func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatalf("Add(2,3)=%d", Add(2, 3))
	}
}
func TestAddNegative(t *testing.T) {
	if Add(-1, 1) != 0 {
		t.Fatal("neg")
	}
}
`

func TestPassingTestRun(t *testing.T) {
	dir := fixture(t, "return a + b", goodTests)
	res, err := Run(context.Background(), proc.New(), Spec{Name: "test", Argv: []string{"go", "test", "./..."}, Dir: dir, Parser: "go", LogDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.ExitCode != 0 || len(res.FailingTests) != 0 || !strings.Contains(res.Output, "ok") {
		t.Fatalf("%+v", res)
	}
	if b, err := os.ReadFile(res.LogRef); err != nil || !strings.Contains(string(b), "ok") {
		t.Fatalf("full log must be on disk: %v", err)
	}
}

func TestFailingTestIsReportedWithNamesNotAsError(t *testing.T) {
	dir := fixture(t, "return a - b", goodTests)
	var lines int
	res, err := Run(context.Background(), proc.New(), Spec{Name: "test", Argv: []string{"go", "test", "./..."}, Dir: dir, Parser: "go", LogDir: t.TempDir()}, func(string) { lines++ })
	if err != nil {
		t.Fatalf("a failing check is a result, not an error: %v", err)
	}
	if res.Passed || res.ExitCode == 0 {
		t.Fatalf("should fail: %+v", res)
	}
	if got := strings.Join(res.FailingTests, ","); got != "TestAdd,TestAddNegative" {
		t.Fatalf("failing tests %q", got)
	}
	if lines == 0 {
		t.Fatal("output lines should stream")
	}
}

func TestBuildFailureOutputIsCapturedFromStderr(t *testing.T) {
	dir := fixture(t, "return a +", goodTests)
	res, _ := Run(context.Background(), proc.New(), Spec{Name: "build", Argv: []string{"go", "build", "./..."}, Dir: dir, Parser: "go", LogDir: t.TempDir()}, nil)
	if res.Passed || !strings.Contains(res.Output, "calc.go") {
		t.Fatalf("%+v", res)
	}
}

func TestTimeoutKillsTheCheck(t *testing.T) {
	dir := fixture(t, "return a + b", goodTests)
	// `go test` with a test that sleeps longer than the timeout
	os.WriteFile(filepath.Join(dir, "slow_test.go"), []byte("package fixture\n\nimport (\"testing\"; \"time\")\n\nfunc TestSlow(t *testing.T) { time.Sleep(time.Minute) }\n"), 0o644)
	start := time.Now()
	res, err := Run(context.Background(), proc.New(), Spec{Name: "test", Argv: []string{"go", "test", "./..."}, Dir: dir, Parser: "go", Timeout: 3 * time.Second, LogDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.Passed || time.Since(start) > 45*time.Second {
		t.Fatalf("%+v after %s", res, time.Since(start))
	}
}

func TestMissingExecutableIsAnError(t *testing.T) {
	_, err := Run(context.Background(), proc.New(), Spec{Name: "x", Argv: []string{"definitely-not-installed-xyz"}, Dir: t.TempDir(), LogDir: t.TempDir()}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTruncateKeepsHeadAndTailAndPointsToTheLog(t *testing.T) {
	long := strings.Repeat("line of output\n", 2000)
	out := Truncate(long+"FINAL LINE\n", 3000, "/logs/x.log")
	if len(out) > 3200 || !strings.Contains(out, "/logs/x.log") || !strings.HasSuffix(out, "FINAL LINE\n") {
		t.Fatalf("len=%d", len(out))
	}
	if short := Truncate("short", 3000, "x"); short != "short" {
		t.Fatal("short output must be untouched")
	}
}

func TestParseFailingTests(t *testing.T) {
	goOut := "=== RUN   TestA\n--- FAIL: TestB (0.00s)\n    x_test.go:5: boom\n--- FAIL: TestA/sub (0.00s)\nFAIL\n"
	if got := strings.Join(ParseFailingTests("go", goOut), ","); got != "TestA/sub,TestB" {
		t.Fatalf("go: %q", got)
	}
	jsOut := " FAIL  src/api.test.ts > items > creates an item\n ✓ other\n × lists items\n"
	got := ParseFailingTests("js", jsOut)
	if len(got) < 2 {
		t.Fatalf("js: %v", got)
	}
	if ParseFailingTests("generic", "FAIL x") != nil {
		t.Fatal("unknown parser returns nil")
	}
}

func TestDetectGoAndNpmProjects(t *testing.T) {
	g := fixture(t, "return a + b", goodTests)
	specs := Detect(g)
	if len(specs) != 3 || specs[1].Name != "test" || specs[1].Argv[0] != "go" {
		t.Fatalf("%+v", specs)
	}
	n := t.TempDir()
	os.WriteFile(filepath.Join(n, "package.json"), []byte(`{"scripts":{"build":"x","test":"y","typecheck":"z"}}`), 0o644)
	specs = Detect(n)
	if len(specs) != 3 || specs[0].Argv[0] != "npm" {
		t.Fatalf("%+v", specs)
	}
	if len(Detect(t.TempDir())) != 0 {
		t.Fatal("empty dir has no checks")
	}
}
