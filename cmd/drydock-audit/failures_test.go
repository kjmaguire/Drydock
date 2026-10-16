package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestMain lets this test binary stand in for programs the audit runs (LABELLED TEST DOUBLES). A copy of it named npm answers
// `npm outdated`, `npm --version` and nothing else, the way AUDIT_FAKE_NPM says; one named git lists the tags of two
// repositories and finds no other; one named go says which Go the project builds with and, asked to run the scanner, answers with
// a finding that carries the toolchain and the arguments it was started with; started with AUDIT_HELPER set it prints a fixed
// text and exits with a fixed code. None touches the network, so the tests run the same on the Windows runner.
func TestMain(m *testing.M) {
	switch base := strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0])); base {
	case "npm":
		os.Exit(fakeNPM(os.Args[1:]))
	case "git":
		os.Exit(fakeGit(os.Args[1:]))
	case "go":
		os.Exit(fakeGo(os.Args[1:]))
	}
	if mode := os.Getenv("AUDIT_HELPER"); mode != "" {
		os.Exit(helperProcess(mode))
	}
	os.Exit(m.Run())
}

func helperProcess(mode string) int {
	switch mode {
	case "ok":
		fmt.Print("fine")
		return 0
	case "partial": // the scanner died part-way through its answer
		fmt.Print(`{"osv":{"id":"GO-2026-0001","summary":"x"}}` + "\n" + `{"finding":{"osv":"GO-2026`)
		fmt.Fprint(os.Stderr, "out of memory")
		return 3
	case "one":
		fmt.Print("{}")
		return 1
	}
	return 99
}

func fakeNPM(args []string) int {
	if len(args) > 0 && args[0] == "--version" {
		fmt.Println("10.9.3")
		return 0
	}
	if len(args) == 0 || args[0] != "outdated" {
		fmt.Fprintln(os.Stderr, "the fake npm answers outdated and --version only")
		return 1
	}
	switch os.Getenv("AUDIT_FAKE_NPM") {
	case "behind": // npm exits 1 when something is behind, and lists it
		fmt.Println(`{"a":{"current":"1.0.0","wanted":"1.0.0","latest":"1.2.0"}}`)
		return 1
	case "current": // exit 0, nothing behind
		fmt.Println("{}")
		return 0
	case "silent": // the registry could not be reached: npm exits 1 and says why on the error stream
		fmt.Fprintln(os.Stderr, "npm error code ENOTFOUND")
		return 1
	case "error-json":
		fmt.Println(`{"error":{"code":"ENOTFOUND","summary":"request to the registry failed"}}`)
		return 1
	case "garbage": // a proxy's error page, with a success code
		fmt.Println("<html>proxy error</html>")
		return 0
	}
	return 99
}

func fakeGo(args []string) int {
	switch {
	case len(args) == 2 && args[0] == "env" && args[1] == "GOVERSION":
		fmt.Println("go1.27.9")
		return 0
	case len(args) > 1 && args[0] == "run":
		// the scanner's output: one vulnerability the code calls, whose summary says how the scanner was started
		fmt.Printf(`{"osv":{"id":"GO-TEST-1","summary":"toolchain=%s args=%s"}}`+"\n", os.Getenv("GOTOOLCHAIN"), strings.Join(args[1:], " "))
		fmt.Println(`{"finding":{"osv":"GO-TEST-1","fixed_version":"v1.0.1","trace":[{"module":"example.com/m","version":"v1.0.0","function":"F"}]}}`)
		return 0
	}
	fmt.Fprintln(os.Stderr, "the fake go answers env GOVERSION and run only")
	return 1
}

func fakeGit(args []string) int {
	if len(args) == 0 || args[0] != "ls-remote" {
		fmt.Fprintln(os.Stderr, "the fake git answers ls-remote only")
		return 1
	}
	url := args[len(args)-1]
	tags := map[string][]string{
		"https://github.com/github/codeql-action": {"v3.28.0", "v4.0.0", "v4.31.2"},
		"https://github.com/actions/checkout":     {"v6.0.0", "v7.0.1"},
	}[url]
	if tags == nil {
		fmt.Fprintf(os.Stderr, "fatal: repository '%s/' not found\n", url)
		return 128
	}
	for _, tag := range tags {
		fmt.Printf("0123456789abcdef0123456789abcdef01234567\trefs/tags/%s\n", tag)
	}
	return 0
}

// installFakeNPM puts a copy of this test binary named npm first on PATH.
func installFakeNPM(t *testing.T) { installFake(t, "npm") }

// installFake puts a copy of this test binary under the name of the program it stands in for first on PATH.
func installFake(t *testing.T, program string) {
	t.Helper()
	dir := t.TempDir()
	name := program
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), self, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// takeFailures empties the list of failed lookups and returns what it held.
func takeFailures() []string {
	failMu.Lock()
	defer failMu.Unlock()
	got := failed
	failed = nil
	return got
}

// A program that ends with a failure code is an error even when it wrote something first: the half answer used to be taken
// for the whole one (audit 2026-10-16, L51). A code the caller names is how the program says "found something", and anything
// else is still an error.
func TestAProgramThatFailsIsAnErrorEvenWithOutput(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dir := t.TempDir()

	if out, err := run(ctx, dir, []string{"AUDIT_HELPER=ok"}, self); err != nil || out != "fine" {
		t.Fatalf("a program that succeeds: %q, %v", out, err)
	}
	out, err := run(ctx, dir, []string{"AUDIT_HELPER=partial"}, self)
	if err == nil || !strings.Contains(err.Error(), "out of memory") {
		t.Fatalf("a program that died part-way gave %q and %v: want an error that carries what it said", out, err)
	}
	if _, err := run(ctx, dir, nil, filepath.Join(dir, "no-such-program")); err == nil {
		t.Fatal("a program that cannot be started is an error")
	}

	// exit code 1 means "found something" for npm: allowed when the caller says so, an error otherwise
	if out, err := runAllowing(ctx, dir, []string{"AUDIT_HELPER=one"}, []int{1}, self); err != nil || out != "{}" {
		t.Fatalf("an allowed code: %q, %v", out, err)
	}
	if _, err := run(ctx, dir, []string{"AUDIT_HELPER=one"}, self); err == nil {
		t.Fatal("run allows no failure code")
	}
	if _, err := runAllowing(ctx, dir, []string{"AUDIT_HELPER=partial"}, []int{1}, self); err == nil {
		t.Fatal("a code the caller did not name (3) is an error")
	}
	if _, code, err := runCode(ctx, dir, []string{"AUDIT_HELPER=partial"}, self); err == nil || code != 3 {
		t.Fatalf("exit code %d, %v", code, err)
	}
	if _, code, err := runCode(ctx, dir, nil, filepath.Join(dir, "no-such-program")); err == nil || code != -1 {
		t.Fatalf("a program that never ran has code %d, %v", code, err)
	}
}

// govulncheck's JSON stream: an entry per vulnerability database record, and a finding for each place the build is affected.
// A finding whose trace starts at a function is reachable from the code; one that stops at the module is not called.
func TestParseGovulncheckReadsFindingsAndRefusesHalfAnAnswer(t *testing.T) {
	stream := strings.Join([]string{
		`{"config":{"protocol_version":"v1.0.0","scanner_name":"govulncheck"}}`,
		`{"progress":{"message":"Scanning your code and 100 packages across 10 dependent modules for known vulnerabilities..."}}`,
		`{"osv":{"id":"GO-2026-0002","summary":"Second problem"}}`,
		`{"osv":{"id":"GO-2026-0001","summary":"First problem"}}`,
		`{"finding":{"osv":"GO-2026-0001","fixed_version":"v1.2.4","trace":[{"module":"example.com/lib","version":"v1.2.3","package":"example.com/lib/x","function":"Do"}]}}`,
		`{"finding":{"osv":"GO-2026-0001","fixed_version":"v1.2.4","trace":[{"module":"example.com/lib","version":"v1.2.3","package":"example.com/lib/x","function":"Other"}]}}`,
		`{"finding":{"osv":"GO-2026-0002","fixed_version":"v0.5.0","trace":[{"module":"example.com/other","version":"v0.4.0"}]}}`,
	}, "\n")
	vs, err := parseGovulncheck(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].ID != "GO-2026-0001" || vs[1].ID != "GO-2026-0002" {
		t.Fatalf("findings, one per vulnerability and in order: %+v", vs)
	}
	if !vs[0].Called || vs[0].Package != "example.com/lib v1.2.3" || vs[0].Fixed != "1.2.4" || vs[0].Summary != "First problem" || vs[0].Source != "govulncheck" {
		t.Errorf("a reachable finding: %+v", vs[0])
	}
	if vs[1].Called || vs[1].Fixed != "0.5.0" {
		t.Errorf("a finding in a module the code does not call: %+v", vs[1])
	}

	// nothing found is an answer: a stream with a config and progress and no finding
	none, err := parseGovulncheck(`{"config":{"scanner_name":"govulncheck"}}` + "\n" + `{"progress":{"message":"x"}}` + "\n")
	if err != nil || len(none) != 0 {
		t.Fatalf("no findings: %+v, %v", none, err)
	}

	// half an answer is not: the stream is cut inside a finding
	cut := stream[:len(stream)-40]
	if vs, err := parseGovulncheck(cut); err == nil {
		t.Fatalf("a stream cut in the middle gave %+v and no error", vs)
	}
	for name, out := range map[string]string{"empty": "", "blank": " \n\n", "not JSON": "go: downloading golang.org/x/vuln\n"} {
		if vs, err := parseGovulncheck(out); err == nil {
			t.Errorf("%s output gave %+v and no error", name, vs)
		}
	}
}

// npm audit --json: the report, or an error object when the registry's audit endpoint could not be reached. The error is not
// "no advisories".
func TestParseNPMAuditRefusesAnErrorAnswer(t *testing.T) {
	report := `{"auditReportVersion":2,"vulnerabilities":{
	  "zeta":{"name":"zeta","severity":"low","via":["alpha"],"range":"<2.0.0","fixAvailable":true},
	  "alpha":{"name":"alpha","severity":"high","via":[{"source":1,"name":"alpha","title":"Prototype pollution in alpha","severity":"high"}],"range":"<1.5.0","fixAvailable":{"name":"alpha","version":"1.5.0"}}
	 },"metadata":{"vulnerabilities":{"total":2}}}`
	vs, err := parseNPMAudit(report)
	if err != nil || len(vs) != 2 {
		t.Fatalf("%+v, %v", vs, err)
	}
	if vs[0].ID != "alpha" || vs[0].Severity != "high" || vs[0].Summary != "Prototype pollution in alpha" || vs[0].Package != "alpha <1.5.0" || vs[1].ID != "zeta" || vs[1].Summary != "" {
		t.Fatalf("advisories, in order and with their titles: %+v", vs)
	}
	if none, err := parseNPMAudit(`{"auditReportVersion":2,"vulnerabilities":{},"metadata":{"vulnerabilities":{"total":0}}}`); err != nil || len(none) != 0 {
		t.Fatalf("a clean report: %+v, %v", none, err)
	}
	for name, out := range map[string]string{
		"registry error": `{"message":"request failed","error":{"code":"ENOAUDIT","summary":"audit endpoint returned an error"}}`,
		"empty":          ``,
		"not JSON":       `npm error code ENOTFOUND`,
		"another object": `{}`,
	} {
		if vs, err := parseNPMAudit(out); err == nil {
			t.Errorf("%s gave %+v and no error", name, vs)
		}
	}
	if _, err := parseNPMAudit(`{"error":{"code":"ENOAUDIT","summary":"audit endpoint returned an error"}}`); err == nil || !strings.Contains(err.Error(), "audit endpoint") {
		t.Errorf("the error carries npm's own summary: %v", err)
	}
}

func TestParseNPMOutdatedTellsAnErrorFromAList(t *testing.T) {
	got, err := parseNPMOutdated(`{"a":{"current":"1.0.0","wanted":"1.0.0","latest":"1.2.0"},"b":{"current":"2.0.0","wanted":"2.0.0","latest":"3.0.0"}}`)
	if err != nil || got["a"] != "1.2.0" || got["b"] != "3.0.0" || len(got) != 2 {
		t.Fatalf("%v, %v", got, err)
	}
	if got, err := parseNPMOutdated("  \n"); err != nil || len(got) != 0 {
		t.Fatalf("nothing is behind: %v, %v", got, err)
	}
	// a package that happens to be called error is a package
	if got, err := parseNPMOutdated(`{"error":{"current":"1.0.0","wanted":"1.0.0","latest":"1.1.0"}}`); err != nil || got["error"] != "1.1.0" {
		t.Fatalf("a package named error: %v, %v", got, err)
	}
	for name, out := range map[string]string{
		"npm's error object": `{"error":{"code":"ENOTFOUND","summary":"request failed"}}`,
		"code only":          `{"error":{"code":"E500"}}`,
		"an error page":      `<html>502 Bad Gateway</html>`,
		"cut":                `{"a":{"current":"1.0.0","lat`,
	} {
		if got, err := parseNPMOutdated(out); err == nil {
			t.Errorf("%s gave %v and no error", name, got)
		}
	}
	// exit 1 with a list is "something is behind"; exit 1 with nothing is npm failing
	if got, err := npmOutdatedAnswer(`{"a":{"latest":"1.2.0"}}`, 1); err != nil || got["a"] != "1.2.0" {
		t.Fatalf("%v, %v", got, err)
	}
	if got, err := npmOutdatedAnswer("", 1); err == nil {
		t.Fatalf("exit 1 and no list gave %v and no error", got)
	}
	if got, err := npmOutdatedAnswer("", 0); err != nil || len(got) != 0 {
		t.Fatalf("exit 0 and no list is nothing behind: %v, %v", got, err)
	}
}

// When npm could not look the packages up, they are not "already the newest": they have no newest version (status "unknown")
// and the lookup is on the list of failures. The lookup used to be taken as an answer, so an unreachable registry made every
// UI package current (audit 2026-10-16, L51).
func TestAFailedNpmLookupLeavesThePackagesUnknownNotCurrent(t *testing.T) {
	repo := t.TempDir()
	ui := filepath.Join(repo, "ui")
	if err := os.MkdirAll(ui, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"package.json":      `{"dependencies":{"a":"^1.0.0"},"devDependencies":{"b":"^2.0.0"}}`,
		"package-lock.json": `{"packages":{"":{},"node_modules/a":{"version":"1.0.0"},"node_modules/b":{"version":"2.0.0"}}}`,
	} {
		if err := os.WriteFile(filepath.Join(ui, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	installFakeNPM(t)
	old := root
	root = repo
	defer func() { root = old }()
	defer takeFailures()

	latestOf := func(mode string) (map[string]string, []string) {
		t.Helper()
		t.Setenv("AUDIT_FAKE_NPM", mode)
		takeFailures()
		items := npmPackages(context.Background())
		var outdated []string
		for _, f := range takeFailures() {
			if strings.HasPrefix(f, "npm outdated:") {
				outdated = append(outdated, f)
			}
		}
		got := map[string]string{}
		for _, it := range items {
			if it.ID == "npm:a" || it.ID == "npm:b" {
				got[it.Name] = it.Latest
			}
		}
		return got, outdated
	}

	// npm answered: the package it lists is behind, the one it leaves out is the newest
	if got, bad := latestOf("behind"); len(bad) != 0 || got["a"] != "1.2.0" || got["b"] != "2.0.0" {
		t.Fatalf("npm listed a as behind: %v, failures %v", got, bad)
	}
	if got, bad := latestOf("current"); len(bad) != 0 || got["a"] != "1.0.0" || got["b"] != "2.0.0" {
		t.Fatalf("nothing behind: %v, failures %v", got, bad)
	}
	// npm did not answer, in each way it can fail: no package has a newest version and the lookup is a failure
	for _, mode := range []string{"silent", "error-json", "garbage"} {
		got, bad := latestOf(mode)
		if len(bad) != 1 || got["a"] != "" || got["b"] != "" {
			t.Errorf("mode %s: latest %v, failures %v: a lookup that said nothing must leave the packages unknown", mode, got, bad)
		}
	}
	// and they are classified unknown, not current
	if c := classify("1.0.0", ""); c != "unknown" {
		t.Errorf("a package with no newest version is %s", c)
	}
}

// A module whose update lookup failed is listed without a newest version and the failure is recorded; a module the proxy
// answered for, and found no update, is current. Output that stops part-way records the failure too.
func TestAModuleWhoseLookupFailedIsUnknownNotCurrent(t *testing.T) {
	defer takeFailures()
	stream := strings.Join([]string{
		`{"Path":"example.com/main","Main":true}`,
		`{"Path":"example.com/fresh","Version":"v1.2.0","Time":"2026-01-02T00:00:00Z"}`,
		`{"Path":"example.com/behind","Version":"v1.0.0","Time":"2026-01-02T00:00:00Z","Update":{"Version":"v1.1.0","Time":"2026-02-02T00:00:00Z"}}`,
		`{"Path":"example.com/lost","Version":"v0.3.0","Time":"2026-01-02T00:00:00Z","Error":{"Err":"module lookup disabled"}}`,
		`{"Path":"example.com/lostpseudo","Version":"v0.0.0-20260102030405-0123456789ab","Error":{"Err":"proxy unreachable"}}`,
		`{"Path":"example.com/cut","Version":"v1.0`,
	}, "\n")
	takeFailures()
	items := moduleItems(stream, nil)
	by := map[string]Item{}
	for _, it := range items {
		by[it.Name] = it
	}
	if len(items) != 4 || by["example.com/main"].Name != "" {
		t.Fatalf("items: %+v", items)
	}
	if by["example.com/fresh"].Latest != "1.2.0" || by["example.com/behind"].Latest != "1.1.0" {
		t.Errorf("answered lookups: fresh %q, behind %q", by["example.com/fresh"].Latest, by["example.com/behind"].Latest)
	}
	if by["example.com/lost"].Latest != "" || by["example.com/lostpseudo"].Latest != "" || by["example.com/lostpseudo"].Status != "unknown" {
		t.Errorf("lookups that failed: %+v %+v", by["example.com/lost"], by["example.com/lostpseudo"])
	}
	got := takeFailures()
	want := []string{"module lookup disabled", "proxy unreachable", "go list output"}
	for _, w := range want {
		if !slices.ContainsFunc(got, func(f string) bool { return strings.Contains(f, w) }) {
			t.Errorf("failures %v lack %q", got, w)
		}
	}
}

// The scanner is pinned to the release the CI workflow runs, not "latest", and software-notes.json records the version used.
func TestTheScannerIsPinnedToTheReleaseTheCIWorkflowRuns(t *testing.T) {
	if strings.HasSuffix(govulncheckModule, "@latest") || !strings.Contains(govulncheckModule, "@v") {
		t.Fatalf("govulncheck must be pinned to a release: %s", govulncheckModule)
	}
	_, version, _ := strings.Cut(govulncheckModule, "@")
	// the Windows verification bundle runs this compiled test from a folder that holds no workflows or notes
	if _, err := os.Stat(filepath.Join("..", "..", "go.mod")); err != nil {
		t.Skip("not run from the source checkout: the workflow and the notes are not here")
	}
	for _, f := range []string{filepath.Join("..", "..", ".github", "workflows", "security.yml"), filepath.Join("..", "..", "docs", "software-notes.json")} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), version) {
			t.Errorf("%s does not name the scanner's pinned release %s", f, version)
		}
	}
}

// An action inside a repository (github/codeql-action/init) is measured against the repository's tags; the repository itself,
// and an action that is a whole repository, are unchanged.
func TestAnActionInsideARepositoryIsMeasuredAgainstTheRepositorysTags(t *testing.T) {
	for in, want := range map[string]string{
		"github/codeql-action/init":    "github/codeql-action",
		"github/codeql-action/analyze": "github/codeql-action",
		"actions/checkout":             "actions/checkout",
		"owner/repo/a/b/c":             "owner/repo",
		"single":                       "single",
	} {
		if got := actionRepo(in); got != want {
			t.Errorf("actionRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

// The actions a workflow uses are measured against the newest tag of their repository, and an action that lives inside a
// repository (github/codeql-action/init) is its repository's: asking git for the path itself finds no repository.
func TestEveryActionInTheWorkflowsIsMeasuredAgainstItsRepository(t *testing.T) {
	repo := t.TempDir()
	wf := filepath.Join(repo, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@v7\n      - uses: github/codeql-action/init@v4\n      - uses: github/codeql-action/analyze@v4\n"
	if err := os.WriteFile(filepath.Join(wf, "x.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	installFake(t, "git")
	old := root
	root = repo
	defer func() { root = old }()
	takeFailures()
	defer takeFailures()

	by := map[string]Item{}
	for _, it := range actions(context.Background()) {
		by[it.Name] = it
	}
	if len(by) != 3 {
		t.Fatalf("actions: %+v", by)
	}
	for name, latest := range map[string]string{"actions/checkout": "v7.0.1", "github/codeql-action/init": "v4.31.2", "github/codeql-action/analyze": "v4.31.2"} {
		if it := by[name]; it.Latest != latest || it.Status != "current" || it.Used != "v4" && it.Used != "v7" {
			t.Errorf("%s: used %q latest %q status %q", name, it.Used, it.Latest, it.Status)
		}
	}
	if f := takeFailures(); len(f) != 0 {
		t.Errorf("lookups failed: %v", f)
	}
}

// The scanner is built with the Go the project is built with (an older one cannot load packages that need a newer one), and
// reads the database the person chose, when one was chosen.
func TestTheScannerIsStartedWithTheProjectsGoAndTheChosenDatabase(t *testing.T) {
	installFake(t, "go")
	old := root
	root = t.TempDir()
	defer func() { root, vulnDB = old, "" }()
	takeFailures()
	defer takeFailures()

	vs, status := govulncheck(context.Background())
	if len(vs) != 1 || !vs[0].Called || !strings.Contains(status, "1 known vulnerabilities") {
		t.Fatalf("%+v, %q", vs, status)
	}
	if want := "toolchain=go1.27.9 args=" + govulncheckModule + " -format json ./..."; vs[0].Summary != want {
		t.Errorf("started as %q, want %q", vs[0].Summary, want)
	}
	vulnDB = "https://storage.googleapis.com/go-vulndb"
	vs, _ = govulncheck(context.Background())
	if len(vs) != 1 || !strings.Contains(vs[0].Summary, "args="+govulncheckModule+" -db https://storage.googleapis.com/go-vulndb -format json ./...") {
		t.Errorf("with a chosen database: %+v", vs)
	}
	if f := takeFailures(); len(f) != 0 {
		t.Errorf("failures: %v", f)
	}
}
