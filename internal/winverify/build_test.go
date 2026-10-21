package winverify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// repoRoot is the source checkout these tests run in. Run from a bundle (even one unpacked inside a checkout, such as
// dist-windows/linux) the tests that need the sources skip: the bundle's own steps cover what they check.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := FindRepoRoot(wd)
	if err != nil {
		t.Skipf("not in a source checkout: %v", err)
	}
	for d := wd; d != root && d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ManifestName)); err == nil {
			t.Skip("running from a verification bundle, not from the source checkout")
		}
	}
	return root
}

func TestManifestRoundTripAndRefusals(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadManifest(dir); err == nil || !strings.Contains(err.Error(), "bundle.json") {
		t.Fatalf("a folder without bundle.json: %v", err)
	}
	m := &Manifest{Commit: "abc", GOOS: "windows", GOARCH: "amd64", Built: time.Now().UTC().Truncate(time.Second),
		Packages: []Package{{Path: "internal/proc", Binary: "tests/internal/proc/proc.test.exe", NeedsGo: []string{"TestX"}, RealClaude: []string{"TestRealClaudeY"}}}}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	back, err := ReadManifest(dir)
	if err != nil || back.Commit != "abc" || back.Packages[0].RealClaude[0] != "TestRealClaudeY" || !back.Built.Equal(m.Built) {
		t.Fatalf("%v %+v", err, back)
	}
	if got := back.Packages[0].Dir(); got != filepath.Join("tests", "internal", "proc") {
		t.Fatalf("package folder %q", got)
	}
	os.WriteFile(filepath.Join(dir, ManifestName), []byte(`{"format": 99}`), 0o644)
	if _, err := ReadManifest(dir); err == nil || !strings.Contains(err.Error(), "rebuild") {
		t.Fatalf("a newer format must be refused: %v", err)
	}
}

func TestFindRepoRoot(t *testing.T) {
	root := repoRoot(t)
	if b, err := os.ReadFile(filepath.Join(root, "go.mod")); err != nil || moduleOf(b) != ModulePath {
		t.Fatalf("%s: %v", root, err)
	}
	if _, err := FindRepoRoot(t.TempDir()); err == nil {
		t.Fatal("a folder outside the checkout has no root")
	}
}

func TestWindowsReasonsFindFilesConstraintsAndGOOSChecks(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a_windows.go":    "package p\n",
		"b.go":            "//go:build windows && amd64\n\npackage p\n",
		"c.go":            "package p\n\nimport \"runtime\"\n\nvar x = runtime.GOOS != \"windows\"\n",
		"d.go":            "package p\n",
		"e_linux_test.go": "//go:build !windows\n\npackage p\n",
	}
	for n, c := range files {
		os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644)
	}
	got := windowsReasons(listedPackage{Dir: dir, GoFiles: []string{"a_windows.go", "b.go", "c.go", "d.go"}, IgnoredGoFiles: []string{"e_linux_test.go"}})
	joined := strings.Join(got, "; ")
	for _, want := range []string{"has a_windows.go", "windows build constraint in b.go", "runtime.GOOS for windows in c.go", "windows build constraint in e_linux_test.go"} {
		if !strings.Contains(joined, want) {
			t.Errorf("reasons lack %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "d.go") {
		t.Errorf("d.go has nothing Windows-specific: %s", joined)
	}
	if r := windowsReasons(listedPackage{Dir: dir, GoFiles: []string{"d.go"}}); len(r) != 0 {
		t.Errorf("a plain package: %v", r)
	}
}

// The selection on this repository, for Windows: every package with Windows-specific code or tests, the ones asked for by
// name, and the ones with real-Claude tests; packages without tests are listed as untested.
func TestSelectPackagesOnThisRepositoryForWindows(t *testing.T) {
	if testing.Short() {
		t.Skip("lists every package of the repository (a few seconds)")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("needs the go command")
	}
	root := repoRoot(t)
	pkgs, err := listPackages(context.Background(), root, buildEnv("windows", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	sel, untested := selectPackages(root, pkgs)
	got := map[string]Package{}
	for _, p := range sel {
		got[p.Path] = p
		if p.Proves == "" || len(p.Why) == 0 || p.Timeout == "" {
			t.Errorf("%s is missing what it proves, why or its time limit: %+v", p.Path, p)
		}
	}
	for _, want := range append([]string{"internal/providers", "internal/security", "internal/mcp", "internal/core",
		"internal/agents/acp", "internal/agents/conformance", "internal/agents/responder", "internal/backup", "internal/supportbundle",
		"internal/checks", "internal/winverify"}, Requested...) {
		if _, ok := got[want]; !ok {
			t.Errorf("%s is not in the bundle", want)
		}
	}
	// terminal_test.go is //go:build !windows (core-terminal covers ConPTY on Windows), but buffer_test.go, which tests the
	// buffer a terminal keeps for a reader that falls behind (audit 2026-10-16, L16), is portable: on Windows the package is
	// tested for that and still compiled for the rest
	if _, ok := got["internal/terminal"]; !ok || contains(untested, "internal/terminal") {
		t.Errorf("internal/terminal has a portable test file and is tested on Windows: selected=%v untested=%v", ok, untested)
	}
	// the reason names the first file that makes the package Windows-specific (command_other.go, the !windows half of the command
	// helper, comes before proc_windows.go): what matters is that it is asked for because of its Windows code
	if !strings.Contains(strings.Join(got["internal/proc"].Why, " "), "windows build constraint") {
		t.Errorf("internal/proc why: %v", got["internal/proc"].Why)
	}
	if !contains(got["internal/agents/runtime"].RealClaude, "TestRealClaudeTurnWithToolUseAndUsage") ||
		!contains(got["internal/core"].RealClaude, "TestRealTestConnectionPromptsAndIsKeptAcrossARestart") ||
		!contains(got["internal/agents/conformance"].RealClaude, "TestClaudeAdapterAgainstTheRealCLI") {
		t.Errorf("real-Claude tests: runtime %v core %v conformance %v", got["internal/agents/runtime"].RealClaude, got["internal/core"].RealClaude,
			got["internal/agents/conformance"].RealClaude)
	}
	if contains(got["internal/core"].RealClaude, "TestRealMCPClientAgainstTheSharedGatewayOverRealData") {
		t.Error("the real MCP client test uses no Claude: it belongs to the unit tests")
	}
	if !contains(untested, "app") || !contains(untested, ".") {
		t.Errorf("untested %v", untested)
	}
	if sel[0].Path != "internal/proc" || sel[len(sel)-1].Path != "internal/core" {
		t.Errorf("order: first %s, last %s", sel[0].Path, sel[len(sel)-1].Path)
	}
}

// Every name the table lists (tests that need Go) must still be a test in that package: a renamed test would otherwise run
// without Go and fail instead of being skipped with the reason.
func TestTheTableNamesTestsThatExist(t *testing.T) {
	root := repoRoot(t)
	for _, path := range append(sortedKeys(known), Requested...) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Errorf("%s does not exist", path)
			continue
		}
		var src strings.Builder
		tests, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(path), "*_test.go"))
		for _, f := range tests {
			b, _ := os.ReadFile(f)
			src.Write(b)
		}
		for _, name := range known[path].NeedsGo {
			if !regexp.MustCompile(`(?m)^func ` + name + `\(t \*testing\.T\)`).MatchString(src.String()) {
				t.Errorf("%s: %s is not a test there any more", path, name)
			}
		}
	}
	for path, names := range extraRealClaude {
		for _, name := range names {
			tests, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(path), "*_test.go"))
			found := false
			for _, f := range tests {
				if b, _ := os.ReadFile(f); strings.Contains(string(b), "func "+name+"(") {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: real-Claude test %s is gone", path, name)
			}
		}
	}
}

func TestPrepareOutOnlyReplacesAnEarlierBundle(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "new", "verify")
	if err := prepareOut(missing); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(missing); err != nil || !st.IsDir() {
		t.Fatal("a missing folder is created")
	}
	foreign := t.TempDir()
	os.WriteFile(filepath.Join(foreign, "notes.txt"), []byte("mine"), 0o644)
	if err := prepareOut(foreign); err == nil {
		t.Fatal("a folder that is not a bundle must not be emptied")
	}
	if _, err := os.Stat(filepath.Join(foreign, "notes.txt")); err != nil {
		t.Fatal("nothing may be deleted from it")
	}
	old := t.TempDir()
	os.WriteFile(filepath.Join(old, ManifestName), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(old, "stale.exe"), []byte("x"), 0o644)
	if err := prepareOut(old); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(old); len(entries) != 0 {
		t.Fatalf("an earlier bundle is replaced: %v", entries)
	}
}

// A bundle built inside a module (dist-windows/ in Drydock's repository) holds Go's own source tree; its go.mod keeps the
// go command from listing that as packages of the module around it.
func TestTheBundleFolderIsFencedOffFromTheModuleAroundIt(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	mod := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(mod, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/outer\n\ngo 1.24\n")
	write("a/a.go", "package a\n")
	out := filepath.Join(mod, "dist-windows", "verify")
	if err := prepareOut(out); err != nil {
		t.Fatal(err)
	}
	if err := fenceModule(out); err != nil {
		t.Fatal(err)
	}
	write("dist-windows/verify/go/src/weak/pointer.go", "package weak\n")
	cmd := exec.Command("go", "list", "./...")
	cmd.Dir = mod
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, b)
	}
	if got := strings.TrimSpace(string(b)); got != "example.com/outer/a" {
		t.Fatalf("go list ./... = %q, want only the outer module's package", got)
	}
}

func TestCopyTreeSkipsWhatItIsTold(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	os.MkdirAll(filepath.Join(src, "a", "node_modules", "big"), 0o755)
	os.WriteFile(filepath.Join(src, "a", "keep.txt"), []byte("k"), 0o644)
	os.WriteFile(filepath.Join(src, "a", "node_modules", "big", "x.js"), []byte("x"), 0o644)
	err := copyTree(src, dst, func(rel string, d os.DirEntry) bool { return d.IsDir() && d.Name() == "node_modules" })
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "a", "keep.txt")); err != nil || string(b) != "k" {
		t.Fatalf("%v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(dst, "a", "node_modules")); !os.IsNotExist(err) {
		t.Fatal("node_modules must be left out")
	}
}

func TestReadmeGivesTheExactStepsForEachSystem(t *testing.T) {
	m := &Manifest{Commit: "abc", GOOS: "windows", GOARCH: "amd64", GoVersion: "go1.27.2", GoBundled: "go1.27.2", Verify: "drydock-verify.exe",
		Packages: []Package{{Path: "internal/proc", Proves: "Process trees die together.", NeedsGo: []string{"TestA", "TestB"}}}, Untested: []string{"app"}}
	win := Readme(m)
	for _, want := range []string{`.\drydock-verify.exe`, "Extract All", "powershell", "Unblock-File", "--with-claude", "COST", "tests:internal/proc",
		"Process trees die together.", "core-smoke", "real-claude", `go\`, "Credential Manager", "send that zip back", "app"} {
		if !strings.Contains(strings.ToLower(win), strings.ToLower(want)) {
			t.Errorf("Windows README lacks %q", want)
		}
	}
	if strings.Count(win, "\r\n") != strings.Count(win, "\n") {
		t.Error("a Windows README has CRLF line ends")
	}
	m.GOOS, m.Verify, m.GoBundled = "linux", "drydock-verify", ""
	lin := Readme(m)
	if !strings.Contains(lin, "./drydock-verify") || strings.Contains(lin, "\r") || !strings.Contains(lin, "https://go.dev/dl/") {
		t.Errorf("Linux README:\n%s", lin)
	}
}
