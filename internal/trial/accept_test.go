package trial

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// acceptRoot makes a repository root with a one-package Go fixture (Add returns a+b) and a hidden folder with two tests of it,
// and returns the root and the task. The package is at the module root, as the hidden folder mirrors the fixture's layout.
func acceptRoot(t *testing.T) (string, Task) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("fx/go.mod", "module example.com/fx\n\ngo 1.24\n")
	write("fx/add.go", "package fx\n\nfunc Add(a, b int) int { return a + b }\n")
	write("hidden/add_hidden_test.go", `package fx

import "testing"

func TestHiddenAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal(Add(2, 3))
	}
}

func TestHiddenAddNegative(t *testing.T) {
	if Add(-2, -3) != -5 {
		t.Fatal(Add(-2, -3))
	}
}
`)
	x := Task{ID: "fx-add", Fixture: "fx", Acceptance: Acceptance{Files: "hidden", Commands: []string{"go vet ./...", "go test -count=1 ./..."}, TimeoutSeconds: 120}}
	return root, x
}

// armTree copies the fixture to a tree an arm "produced" and writes the extra files into it.
func armTree(t *testing.T, root string, extra map[string]string) string {
	t.Helper()
	tree := filepath.Join(t.TempDir(), "tree")
	if err := copyTree(filepath.Join(root, "fx"), tree, nil); err != nil {
		t.Fatal(err)
	}
	for rel, body := range extra {
		if err := os.WriteFile(filepath.Join(tree, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tree
}

// The commands passing is not enough: every test the hidden files declare has to be seen passing. `go test` exits 0 for a
// package whose TestMain never calls m.Run, or calls it and exits 0 whatever happened, so a result that wrote such a TestMain
// used to be accepted without its hidden tests having run (audit 2026-10-16, L54).
func TestAcceptNeedsEveryHiddenTestToReportAPass(t *testing.T) {
	root, x := acceptRoot(t)
	ctx := context.Background()

	good := Accept(ctx, root, x, armTree(t, root, nil))
	if !good.Passed || good.Error != "" || len(good.Commands) != 3 {
		t.Fatalf("a correct result: %+v", good)
	}
	if last := good.Commands[2]; last.Command != hiddenTestsCommand || last.ExitCode != 0 || !strings.Contains(last.Output, "2 tests of the hidden files") {
		t.Fatalf("the check of the hidden tests: %+v", last)
	}

	for name, extra := range map[string]map[string]string{
		"a TestMain that exits before any test": {"main_test.go": "package fx\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(0) }\n"},
		"a TestMain that exits 0 whatever the tests did": {
			"add.go":       "package fx\n\nfunc Add(a, b int) int { return a - b }\n",
			"main_test.go": "package fx\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { m.Run(); os.Exit(0) }\n",
		},
	} {
		res := Accept(ctx, root, x, armTree(t, root, extra))
		if res.Passed {
			t.Errorf("%s was accepted: %+v", name, res.Commands)
			continue
		}
		last := res.Commands[len(res.Commands)-1]
		if len(res.Commands) != 3 || last.Command != hiddenTestsCommand || last.ExitCode == 0 || !strings.Contains(last.Output, "TestHiddenAdd") || !strings.Contains(last.Output, "TestHiddenAddNegative") {
			t.Errorf("%s: the record says %+v", name, res.Commands)
		}
	}

	// a result whose code is wrong is stopped by the commands, as before, and the check of the hidden tests is not reached
	wrong := Accept(ctx, root, x, armTree(t, root, map[string]string{"add.go": "package fx\n\nfunc Add(a, b int) int { return a * b }\n"}))
	if wrong.Passed || len(wrong.Commands) != 2 || wrong.Commands[1].ExitCode == 0 {
		t.Fatalf("a wrong result: %+v", wrong)
	}
}

// A task whose hidden files declare no Go test (a script, a data file) has nothing to look for: its commands decide, as before.
func TestAcceptWithoutGoTestsInTheHiddenFilesIsTheCommandsAlone(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"fixture/a.txt", "hidden/h.txt"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	x := Task{ID: "t", Fixture: "fixture", Acceptance: Acceptance{Files: "hidden", TimeoutSeconds: 30, Commands: []string{"test -f h.txt"}}}
	res := Accept(context.Background(), root, x, filepath.Join(root, "fixture"))
	if !res.Passed || len(res.Commands) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestHiddenTestsAreFoundTheWayGoTestFindsThem(t *testing.T) {
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"a_test.go":         "package a_test\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {}\nfunc Test_two(t *testing.T) {}\nfunc Testlower(t *testing.T) {}\nfunc TestMain(m *testing.M) {}\nfunc (x X) TestMethod(t *testing.T) {}\nfunc TestTwoParams(t *testing.T, n int) {}\nfunc TestValue(t testing.T) {}\nfunc BenchmarkB(b *testing.B) {}\nfunc Test(t *testing.T) {}\n",
		"sub/dir/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestDeep(t *testing.T) {}\n",
		"sub/dir/data.json": "{}",
		"sub/notgo.txt":     "func TestNot(t *testing.T) {}",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := hiddenTests(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got["."], ",") != "TestOne,Test_two,Test" || strings.Join(got["sub/dir"], ",") != "TestDeep" || len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken_test.go"), []byte("package x\nfunc ("), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := hiddenTests(dir); err == nil {
		t.Fatal("a hidden file that does not parse must be an error, not a test that is never looked for")
	}
}

func TestPassedTestsBelievesNoHalfStream(t *testing.T) {
	stream := `{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"output","Package":"p","Test":"TestA","Output":"=== RUN TestA\n"}
{"Action":"run","Package":"p","Test":"TestA/sub"}
{"Action":"pass","Package":"p","Test":"TestA/sub"}
{"Action":"pass","Package":"p","Test":"TestA"}
{"Action":"run","Package":"p","Test":"TestB"}
{"Action":"fail","Package":"p","Test":"TestB"}
{"Action":"run","Package":"p","Test":"TestC"}
{"Action":"skip","Package":"p","Test":"TestC"}
{"Action":"run","Package":"p","Test":"TestD"}
{"Action":"pass","Package":"q","Test":"TestA"}
{"Action":"pass","Package":"p"}
`
	got, err := passedTests([]byte(stream), false)
	if err != nil {
		t.Fatal(err)
	}
	if !got[testID{"p", "TestA"}] || !got[testID{"q", "TestA"}] || got[testID{"p", "TestB"}] || got[testID{"p", "TestC"}] || got[testID{"p", "TestD"}] || got[testID{"p", "TestA/sub"}] {
		t.Fatalf("%v", got)
	}
	for name, bad := range map[string]string{"cut": stream[:len(stream)-30], "not JSON": "ok  \tp\t0.01s\n"} {
		if got, err := passedTests([]byte(bad), false); err == nil {
			t.Errorf("%s gave %v and no error", name, got)
		}
	}
	if _, err := passedTests([]byte(stream), true); err == nil {
		t.Error("a stream that overflowed its buffer is cut")
	}
	var c capped
	c.max = 10
	if n, err := c.Write([]byte("0123456789abc")); n != 13 || err != nil || c.buf.String() != "0123456789" || !c.over {
		t.Fatalf("capped: %d %v %q %v", n, err, c.buf.String(), c.over)
	}
}

func TestModulePathReadsTheModuleLine(t *testing.T) {
	for in, want := range map[string]string{
		"module example.com/fx\n\ngo 1.24\n":            "example.com/fx",
		"// c\nmodule \"example.com/q\"\n":              "example.com/q",
		"  module   example.com/spaced   // trailing\n": "example.com/spaced",
		"go 1.24\n": "",
	} {
		if got := modulePath([]byte(in)); got != want {
			t.Errorf("modulePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The result's tests are an agent's code. They run with the toolchain's variables and the network settings and without the
// harness's tokens, in a home and a temporary folder of the scratch directory (audit 2026-10-16, M19).
func TestTheAcceptanceEnvironmentHoldsNoTokenAndIsTheScratchDirectorys(t *testing.T) {
	for k, v := range map[string]string{
		"GITHUB_TOKEN":            "ghs_" + "example",
		"GH_TOKEN":                "gh_" + "example",
		"ANTHROPIC_API_KEY":       "sk-" + "example",
		"CLAUDE_CODE_OAUTH_TOKEN": "oauth-" + "example",
		"NPM_TOKEN":               "npm_" + "example",
		"AWS_SECRET_ACCESS_KEY":   "aws-" + "example",
		"HTTPS_PROXY":             "http://proxyuser:proxypass-example@proxy.example:3128",
		"GOFLAGS":                 "-mod=vendor", // the harness's own setting wins
		"GOENV":                   "/somewhere/else",
	} {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	env := acceptEnv(dir)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, dup := got[k]; dup {
			t.Errorf("%s is set twice: the first one would be used by some programs and the last by others", k)
		}
		got[k] = v
	}
	for _, secret := range []string{"GITHUB_TOKEN", "GH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "NPM_TOKEN", "AWS_SECRET_ACCESS_KEY"} {
		if _, ok := got[secret]; ok {
			t.Errorf("%s reaches the code of the result", secret)
		}
	}
	if strings.Contains(strings.Join(env, "\n"), "proxypass-example") || got["HTTPS_PROXY"] != "http://proxy.example:3128" {
		t.Errorf("a proxy's address is passed on, its password is not: %q", got["HTTPS_PROXY"])
	}
	for k, want := range map[string]string{"GOFLAGS": "-mod=mod", "GOPROXY": "off", "GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "CGO_ENABLED": "0"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if got["PATH"] == "" {
		t.Error("no PATH: the toolchain cannot be found")
	}
	for _, k := range []string{"HOME", "USERPROFILE", "TMPDIR", "TMP", "TEMP", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "GOPATH"} {
		if rel, err := filepath.Rel(dir, got[k]); err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("%s is %q, not a folder of the scratch directory %s", k, got[k], dir)
		}
	}
	for _, k := range []string{"HOME", "TMPDIR"} {
		if st, err := os.Stat(got[k]); err != nil || !st.IsDir() {
			t.Errorf("%s: the folder does not exist: %v", k, err)
		}
	}
}

// And the commands of an acceptance run see exactly that.
func TestAcceptRunsTheResultWithoutTheHarnessSecrets(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghs_"+"example")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-"+"example")
	t.Setenv("GOFLAGS", "-mod=vendor")
	root := t.TempDir()
	for _, d := range []string{"fixture", "hidden"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	x := Task{ID: "t", Fixture: "fixture", Acceptance: Acceptance{Files: "hidden", TimeoutSeconds: 30, Commands: []string{"env"}}}
	res := Accept(context.Background(), root, x, filepath.Join(root, "fixture"))
	if !res.Passed || len(res.Commands) != 1 {
		t.Fatalf("%+v", res)
	}
	seen := res.Commands[0].Output
	for _, secret := range []string{"GITHUB_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ghs_example", "oauth-example"} {
		if strings.Contains(seen, secret) {
			t.Errorf("the acceptance commands saw %s:\n%s", secret, seen)
		}
	}
	if !strings.Contains(seen, "GOFLAGS=-mod=mod") || strings.Contains(seen, "vendor") {
		t.Errorf("the harness's Go settings must win:\n%s", seen)
	}
}
