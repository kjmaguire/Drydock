package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/pins"
	"github.com/kjmaguire/drydock/internal/contract"
)

// TestMain lets this test binary stand in for npm (a LABELLED TEST DOUBLE): a copy of it named npm records the environment it
// was started with next to itself, makes the folder `npm install -g --prefix` would make, and succeeds. The tests that install
// put such a copy first on PATH.
func TestMain(m *testing.M) {
	if base := strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0])); base == "npm" {
		os.Exit(fakeNPM(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeNPM(args []string) int {
	self, err := os.Executable() // os.Args[0] is only "npm" when the program was found on PATH
	if err != nil {
		return 1
	}
	dir := filepath.Dir(self)
	appendTo := func(name, text string) {
		if f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString(text)
			_ = f.Close()
		}
	}
	appendTo("npm-env.txt", strings.Join(os.Environ(), "\n")+"\n")
	appendTo("npm-args.txt", strings.Join(args, "\n")+"\n")
	for i, a := range args {
		if a == "--prefix" && i+1 < len(args) {
			bin := npmBin(args[i+1], runtime.GOOS)
			if err := os.MkdirAll(bin, 0o755); err != nil {
				return 1
			}
			return 0
		}
	}
	return 0
}

// installFakeNPM puts a copy of this test binary named npm first on PATH and returns the folder it records into.
func installFakeNPM(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "npm"
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
	return dir
}

func TestParseFlags(t *testing.T) {
	o, err := parseFlags([]string{"--cli", "Claude, goose,", "--budget-usd", "1.5", "--latest", "--check-updates", "--skip-slow", "--out", "r"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(o.clis, ",") != "claude,goose" || o.budget != 1.5 || !o.latest || !o.checkUpdates || !o.skipSlow || o.out != "r" || o.timeout != 20*time.Minute {
		t.Fatalf("%+v", o)
	}
	if o, _ := parseFlags(nil); o.budget != 1.0 || o.out != "contract-report" || len(o.clis) != 0 {
		t.Fatalf("defaults %+v", o)
	}
	for _, bad := range [][]string{{"--budget-usd", "-1"}, {"extra"}, {"--nope"}} {
		if _, err := parseFlags(bad); err == nil {
			t.Errorf("%v must be refused", bad)
		}
	}
}

func TestDescribeChange(t *testing.T) {
	for _, c := range []struct {
		pinned, installed, latest string
		newer                     bool
		want                      []string
	}{
		{"2.1.295", "2.1.296", "2.1.296", false, []string{"the installed version is the newest release", "the pin 2.1.295 is behind the installed 2.1.296"}},
		{"2.1.295", "2.1.296", "2.1.297", true, []string{"2.1.297 is newer than the installed 2.1.296", "run the suite with --latest"}},
		{"0.88.0", "0.88.0", "0.89.0", true, []string{"0.89.0 is newer than the installed 0.88.0"}},
		{"3.0.70", "3.0.70", "3.0.70", false, []string{"the installed version is the newest release"}},
		{"1.18.35", "", "1.18.36", true, []string{"not installed here; 1.18.36 is newer than the pin"}},
		{"1.18.35", "", "1.18.35", false, []string{"not installed here; the pin is the newest release"}},
		{"1.54.0", "1.53.0", "1.54.0", true, []string{"1.54.0 is newer than the installed 1.53.0", "the installed 1.53.0 is older than the pin 1.54.0"}},
	} {
		got, newer := describeChange(c.pinned, c.installed, c.latest)
		if newer != c.newer {
			t.Errorf("%v: newer %v", c, newer)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%v: %q lacks %q", c, got, w)
			}
		}
	}
}

func TestNPMBinFollowsThePlatform(t *testing.T) {
	if got := npmBin(filepath.FromSlash("/tmp/p"), "linux"); got != filepath.Join(filepath.FromSlash("/tmp/p"), "bin") {
		t.Fatal(got)
	}
	if got := npmBin(`C:\p`, "windows"); got != `C:\p` {
		t.Fatal(got)
	}
}

// The tests run without the enclosing Claude Code session, without the switch that skips real agents, and with the budget and
// ledger; an empty sign-in variable (an unset GitHub secret) is removed.
func TestSuiteEnv(t *testing.T) {
	env := suiteEnv([]string{"PATH=/bin", "CLAUDECODE=1", "CLAUDE_CODE_REMOTE_SESSION_ID=s", "DRYDOCK_NO_REAL_AGENTS=1", "ANTHROPIC_API_KEY=",
		"CLAUDE_CODE_OAUTH_TOKEN=tok", "DRYDOCK_REAL_CLAUDE=1"}, "/tmp/l", 1.5)
	got := strings.Join(env, " ")
	if got != "PATH=/bin CLAUDE_CODE_OAUTH_TOKEN=tok GOFLAGS=-mod=mod DRYDOCK_CONTRACT_BUDGET_USD=1.5 DRYDOCK_CONTRACT_LEDGER=/tmp/l" {
		t.Fatalf("%s", got)
	}
	if env := suiteEnv([]string{"GOFLAGS=-mod=readonly"}, "l", 1); !strings.Contains(strings.Join(env, " "), "GOFLAGS=-mod=readonly") || strings.Count(strings.Join(env, " "), "GOFLAGS") != 1 {
		t.Fatalf("a GOFLAGS the caller set is kept: %v", env)
	}
}

const fixtureTest = `package fix

import (
	"os"
	"runtime"
	"testing"
)

func TestFree(t *testing.T) {
	if os.Getenv("CLAUDE_CODE_SESSION_ID") != "" {
		t.Fatal("the enclosing session reached a test that did not ask for it")
	}
	t.Log("contract-note: the free check ran")
}

func TestEnclosing(t *testing.T) {
	if os.Getenv("CLAUDE_CODE_SESSION_ID") != "outer-session" {
		t.Fatal("the test that checks the adapter removes the enclosing session did not get it")
	}
}

// a paid check books its spend in the ledger, as the contract tests do
func TestPaid(t *testing.T) {
	if os.Getenv("DRYDOCK_REAL_CLAUDE") != "1" {
		t.Fatal("the runner did not pass the test's environment")
	}
	f, err := os.OpenFile(os.Getenv("DRYDOCK_CONTRACT_LEDGER"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"test\":\"TestPaid\",\"usd\":0.4}\n")
	f.Close()
}

func TestTooExpensive(t *testing.T) { t.Fatal("the budget guard should have skipped this") }

func TestWindowsOnly(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Fatal("a Windows-only check ran elsewhere")
	}
}

func TestSlow(t *testing.T) { t.Fatal("--skip-slow should have left this out") }

func TestFails(t *testing.T) { t.Error("the assumption broke") }

func TestSubs(t *testing.T) {
	t.Run("a", func(t *testing.T) {})
	t.Run("b", func(t *testing.T) { t.Skip("no b here") })
}
`

// The runner against a small module: free checks first, the budget guard between paid ones, Windows-only and slow checks left
// out, failures, subtests and a test that does not exist, all mapped back to their contracts.
func TestRunnerRunsTheTestsAndMapsTheResults(t *testing.T) {
	mod := t.TempDir()
	goVersion := strings.TrimPrefix(runtime.Version(), "go")
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/fixture\n\ngo "+goVersion+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mod, "fix"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "fix", "fix_test.go"), []byte(fixtureTest), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := func(test string, est float64) contract.TestRef {
		return contract.TestRef{Package: "./fix", Test: test, EstUSD: est}
	}
	paid := ref("TestPaid", 0.3)
	paid.Env = []string{"DRYDOCK_REAL_CLAUDE=1"}
	slow := ref("TestSlow", 0)
	slow.Slow = true
	sub := func(name string) contract.TestRef { r := ref("TestSubs", 0); r.Subtest = name; return r }
	enclosing := ref("TestEnclosing", 0)
	enclosing.Enclosing = true
	cs := []contract.Contract{
		{ID: "A-01", CLI: "x", Tests: []contract.TestRef{ref("TestFree", 0)}},
		{ID: "B-01", CLI: "x", Tests: []contract.TestRef{paid}},
		{ID: "C-01", CLI: "x", Tests: []contract.TestRef{ref("TestTooExpensive", 0.9)}},
		{ID: "D-01", CLI: "x", Platform: "windows", Tests: []contract.TestRef{ref("TestWindowsOnly", 0)}},
		{ID: "E-01", CLI: "x", Tests: []contract.TestRef{slow}},
		{ID: "F-01", CLI: "x", Tests: []contract.TestRef{ref("TestFails", 0)}},
		{ID: "G-01", CLI: "a", Tests: []contract.TestRef{sub("a")}},
		{ID: "G-01", CLI: "b", Tests: []contract.TestRef{sub("b")}},
		{ID: "H-01", CLI: "x", Tests: []contract.TestRef{ref("TestMissing", 0)}},
		{ID: "I-01", CLI: "x", Tests: []contract.TestRef{enclosing}},
	}
	work := t.TempDir()
	var log bytes.Buffer
	r := &runner{root: mod, out: filepath.Join(work, "out"), ledger: filepath.Join(work, "spend.jsonl"), budget: 1.0, timeout: 5 * time.Minute, skipSlow: true, w: &log}
	inside := append(os.Environ(), "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=outer-session") // the runner started inside Claude Code
	r.env, r.enclosing = append(suiteEnv(inside, r.ledger, r.budget), "GOTOOLCHAIN=local"), enclosingVars(inside)
	outcomes, estimated, err := r.runAll(context.Background(), cs)
	if err != nil {
		t.Fatal(err)
	}
	rep := buildReport(cs, []contract.CLIInfo{{Name: "x", Version: "1.2.3"}}, outcomes, nil, options{budget: 1}, mod)
	rep.Finish()
	rows := map[string]contract.Row{}
	for _, row := range rep.Rows {
		rows[row.Contract+" "+row.CLI] = row
	}
	want := map[string][2]string{ // status, and a part of the reason
		"A-01 x": {"pass", ""}, "B-01 x": {"pass", ""}, "C-01 x": {"skip", "TestTooExpensive: budget: $0.4000 reported"},
		"E-01 x": {"skip", "left out by --skip-slow"}, "F-01 x": {"fail", ""}, "G-01 a": {"pass", ""}, "G-01 b": {"skip", "TestSubs/b: no b here"},
		"H-01 x": {"fail", ""}, "D-01 x": {"skip", "TestWindowsOnly: only checked on Windows"}, "I-01 x": {"pass", ""},
	}
	if runtime.GOOS == "windows" {
		want["D-01 x"] = [2]string{"pass", ""}
	}
	for k, w := range want {
		if row := rows[k]; row.Status != w[0] || !strings.Contains(row.Reason, w[1]) {
			t.Errorf("%s: %s %q, want %s with %q", k, row.Status, row.Reason, w[0], w[1])
		}
	}
	if b := outcomes["./fix TestPaid"]; b.SpendUSD == nil || *b.SpendUSD != 0.4 || estimated != 0 {
		t.Fatalf("the paid test's own report is used, not the estimate: %+v estimated %v", b, estimated)
	}
	if rep.Rows[0].Notes[0] != "the free check ran" || rep.Rows[0].CLIVersion != "1.2.3" {
		t.Fatalf("%+v", rep.Rows[0])
	}
	if !strings.Contains(rep.Rows[5].Failure, "the assumption broke") || !strings.Contains(rep.Rows[8].Failure, "no test by that name") {
		t.Fatalf("failures: %q / %q", rep.Rows[5].Failure, rep.Rows[8].Failure)
	}
	if !rep.Failed() {
		t.Fatal("a failed contract fails the run")
	}
	if logs, _ := filepath.Glob(filepath.Join(r.out, "logs", "*.jsonl")); len(logs) == 0 {
		t.Fatal("the raw go test output is kept for diagnosis")
	}
	// free checks run before paid ones
	if i, j := strings.Index(log.String(), "TestFails"), strings.Index(log.String(), "TestPaid"); i < 0 || j < 0 || i > j {
		t.Fatalf("order:\n%s", log.String())
	}
}

func TestListPrintsTheCatalog(t *testing.T) {
	var b bytes.Buffer
	code, err := run(context.Background(), options{list: true, clis: []string{"goose"}}, &b)
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if !strings.Contains(b.String(), "ACP-INIT-01 (goose)") || !strings.Contains(b.String(), "ACP-PROMPT-01 (goose)") || strings.Contains(b.String(), "CLAUDE-") {
		t.Fatalf("%s", b.String())
	}
	if _, err := run(context.Background(), options{clis: []string{"nothing-like-this"}}, &b); err == nil {
		t.Fatal("an unknown CLI selects nothing and is an error")
	}
	if err := installPinned(context.Background(), &b, "nothing-like-this"); err == nil || !strings.Contains(err.Error(), "no npm pin") {
		t.Fatalf("%v", err)
	}
}

// A package installed for the newest-release watch runs its install script, and that script must not find the suite's sign-in, the
// job's token or any other credential in its environment (audit 2026-10-16, M19). npm gets what a build tool needs and its own
// settings.
func TestNPMRunsInstallScriptsWithoutTheCallersSecrets(t *testing.T) {
	parent := []string{"PATH=/bin", "HOME=/home/u", "CLAUDE_CODE_OAUTH_TOKEN=oauth-secret-value", "ANTHROPIC_API_KEY=sk-secret-value", "GH_TOKEN=gh-secret-value",
		"GITHUB_TOKEN=github-secret-value", "NODE_AUTH_TOKEN=npm-secret-value", "NPM_CONFIG_REGISTRY=https://registry.example/", "HTTPS_PROXY=http://user:pass@proxy:8080",
		"NODE_OPTIONS=--max-old-space-size=4096", "AWS_SECRET_ACCESS_KEY=aws-secret-value"}
	got := strings.Join(npmEnv(parent), "\n")
	for _, secret := range []string{"oauth-secret-value", "sk-secret-value", "gh-secret-value", "github-secret-value", "npm-secret-value", "aws-secret-value", "user:pass"} {
		if strings.Contains(got, secret) {
			t.Errorf("npm's environment holds %q:\n%s", secret, got)
		}
	}
	for _, want := range []string{"PATH=/bin", "HOME=/home/u", "NPM_CONFIG_REGISTRY=https://registry.example/", "HTTPS_PROXY=http://proxy:8080", "NODE_OPTIONS=--max-old-space-size=4096"} {
		if !strings.Contains(got, want) {
			t.Errorf("npm's environment lacks %q:\n%s", want, got)
		}
	}
}

// --install-latest and --latest-from are the two halves of --latest, run in two steps so the install happens without the secrets:
// the first installs the newest releases into a prefix (npm, run without the caller's secrets), the second only uses them.
func TestInstallLatestInstallsWithoutSecretsAndLatestFromInstallsNothing(t *testing.T) {
	npmDir := installFakeNPM(t)
	for k, v := range map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "oauth-secret-value", "ANTHROPIC_API_KEY": "sk-secret-value", "GH_TOKEN": "gh-secret-value"} {
		t.Setenv(k, v)
	}
	prefix := filepath.Join(t.TempDir(), "latest")
	var out bytes.Buffer
	o, err := parseFlags([]string{"--cli", "claude", "--install-latest", prefix})
	if err != nil {
		t.Fatal(err)
	}
	code, err := run(context.Background(), o, &out)
	if err != nil || code != 0 {
		t.Fatalf("install: %d %v\n%s", code, err, out.String())
	}
	env, _ := os.ReadFile(filepath.Join(npmDir, "npm-env.txt"))
	args, _ := os.ReadFile(filepath.Join(npmDir, "npm-args.txt"))
	for _, secret := range []string{"oauth-secret-value", "sk-secret-value", "gh-secret-value"} {
		if strings.Contains(string(env), secret) {
			t.Errorf("npm ran with %q in its environment", secret)
		}
	}
	if !strings.Contains(string(args), "install\n-g\n--prefix\n"+prefix) || !strings.Contains(string(args), "@anthropic-ai/claude-code@latest") {
		t.Errorf("npm was asked: %q", args)
	}
	if st, err := os.Stat(npmBin(prefix, runtime.GOOS)); err != nil || !st.IsDir() {
		t.Errorf("the prefix was not made: %v", err)
	}
	if !strings.Contains(out.String(), "installed into") {
		t.Errorf("the step does not say where: %s", out.String())
	}

	// --latest-from needs an install to have happened, and never installs
	_ = os.Remove(filepath.Join(npmDir, "npm-args.txt"))
	missing, err := parseFlags([]string{"--cli", "claude", "--latest-from", filepath.Join(t.TempDir(), "nothing")})
	if err != nil {
		t.Fatal(err)
	}
	code, err = run(context.Background(), missing, &out)
	if err == nil || code != 2 || !strings.Contains(err.Error(), "run --install-latest first") {
		t.Fatalf("--latest-from with nothing installed: %d %v", code, err)
	}
	if _, err := os.Stat(filepath.Join(npmDir, "npm-args.txt")); err == nil {
		t.Error("--latest-from ran npm")
	}
}

func TestParseFlagsRefusesTwoWaysToTheNewestReleases(t *testing.T) {
	for _, bad := range [][]string{{"--latest", "--install-latest", "d"}, {"--latest", "--latest-from", "d"}, {"--install-latest", "d", "--latest-from", "d"}} {
		if _, err := parseFlags(bad); err == nil {
			t.Errorf("%v must be refused", bad)
		}
	}
	if o, err := parseFlags([]string{"--latest-from", "d"}); err != nil || o.latestFrom != "d" {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestLatestPinsAreOnlyWhatASelectedContractRuns(t *testing.T) {
	selected := []pins.Pin{{Bin: "claude", Package: "@anthropic-ai/claude-code"}, {Bin: "codex", Package: "@openai/codex"}}
	got := latestPins(selected, []string{"claude"}, false, nil)
	if len(got) != 1 || got[0].Bin != "claude" {
		t.Fatalf("%+v", got)
	}
	updates := []contract.Update{{CLI: "claude"}, {CLI: "codex", Newer: true}}
	if got := latestPins(selected, []string{"claude", "codex"}, true, updates); len(got) != 1 || got[0].Bin != "codex" || !updates[1].Tested || updates[0].Tested {
		t.Fatalf("with --check-updates only what is newer: %+v %+v", got, updates)
	}
}

// npm 12 runs no install script unless the command allows it, and Claude Code's npm package links its native binary in one:
// every install the tool makes allows the script of the package it installs, and nothing else's.
func TestTheToolsNPMInstallsAllowThePackagesOwnInstallScript(t *testing.T) {
	got := npmInstallArgs("/tmp/p", "@anthropic-ai/claude-code", "@anthropic-ai/claude-code@latest")
	want := []string{"install", "-g", "--prefix", "/tmp/p", "--no-audit", "--no-fund", "--allow-scripts=@anthropic-ai/claude-code", "@anthropic-ai/claude-code@latest"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("into a prefix: %q, want %q", got, want)
	}
	got = npmInstallArgs("", "opencode-ai", "opencode-ai@1.19.3")
	want = []string{"install", "-g", "--no-audit", "--no-fund", "--allow-scripts=opencode-ai", "opencode-ai@1.19.3"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("global: %q, want %q", got, want)
	}
}
