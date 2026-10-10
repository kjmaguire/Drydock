package connections_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/connections"
)

func manifest(t *testing.T, id string) *manifests.Manifest {
	t.Helper()
	s, err := manifests.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	m, ok := s.Get(id)
	if !ok {
		t.Fatalf("no manifest %s", id)
	}
	return m
}

func compat(t *testing.T) *manifests.Compat {
	t.Helper()
	c, err := manifests.EmbeddedCompat()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func check(t *testing.T, id string, o connections.Options) connections.Connection {
	t.Helper()
	if o.Compat == nil {
		o.Compat = compat(t)
	}
	return connections.Check(context.Background(), manifest(t, id), o)
}

func ok1(stdout string) stubReply { return stubReply{Stdout: stdout} }

// claudeTable: claude 2.1.294 (the version verified in compat.yaml) and a logged-in `auth status`.
func claudeTable(version, authStdout string, authExit int) stubTable {
	return stubTable{"claude": {
		"--version":          ok1(version + " (Claude Code)\n"),
		"auth status --json": {Stdout: authStdout, Exit: authExit},
	}}
}

func TestNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	for _, id := range []string{"claude", "codex", "gemini", "opencode", "goose", "antigravity"} {
		c := check(t, id, connections.Options{})
		if c.Installed || c.Status != agents.StatusNotInstalled || c.Usable || len(c.Problems) == 0 || c.InstallHint == "" || c.Adapter != id {
			t.Errorf("%s: %+v", id, c)
		}
	}
}

func TestClaudeStatusTransitions(t *testing.T) {
	cases := []struct {
		name       string
		table      stubTable
		wantStatus agents.ConnectionStatus
		wantReason string
		wantAuth   *bool
		usable     bool
	}{
		{"ready", claudeTable("2.1.294", `{"loggedIn": true, "authMethod": "oauth_token"}`, 0), agents.StatusReady, "", ptr(true), true},
		{"unsupported", claudeTable("2.1.204", `{"loggedIn": true}`, 0), agents.StatusUnsupportedVersion, connections.ReasonVersionBelowMin, nil, false},
		{"older than recommended but above min is fine", claudeTable("2.1.250", `{"loggedIn": true}`, 0), agents.StatusReady, "", ptr(true), true},
		{"unauthenticated", claudeTable("2.1.294", `{"loggedIn": false}`, 0), agents.StatusUnauthenticated, connections.ReasonAuthProbeFalse, ptr(false), false},
		{"unauthenticated with a non-zero exit and JSON", claudeTable("2.1.294", `{"loggedIn": false}`, 1), agents.StatusUnauthenticated, connections.ReasonAuthProbeFalse, ptr(false), false},
		{"no JSON from the probe is unknown, not unauthenticated", claudeTable("2.1.294", "", 1), agents.StatusReady, "", nil, true},
		{"newer than anything verified is degraded but usable", claudeTable("2.1.999", `{"loggedIn": true}`, 0), agents.StatusDegraded, connections.ReasonVersionUntested, ptr(true), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubEnv(t, tc.table)
			c := check(t, "claude", connections.Options{Compat: claudeCompat(t)})
			if c.Status != tc.wantStatus || !strings.HasPrefix(c.Reason, tc.wantReason) || c.Usable != tc.usable {
				t.Fatalf("status=%s reason=%q usable=%v problems=%v", c.Status, c.Reason, c.Usable, c.Problems)
			}
			switch {
			case tc.wantAuth == nil && c.Authenticated != nil, tc.wantAuth != nil && (c.Authenticated == nil || *c.Authenticated != *tc.wantAuth):
				t.Fatalf("authenticated %v want %v", c.Authenticated, tc.wantAuth)
			}
			if c.Installed != true || c.Path == "" {
				t.Fatalf("%+v", c)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// claudeCompat is the embedded compat plus a claude entry for 2.1.294, so ready is reachable in these tests whatever the
// shipped compat.yaml currently says about claude.
func claudeCompat(t *testing.T) *manifests.Compat {
	t.Helper()
	c, err := manifests.LoadCompat([]byte(`
schema: 1
node: {min: "22"}
adapters:
  claude: {min: "2.1.205", recommended: "2.1.277", entries: [{version: "2.1.294", class: verified_linux, date: 2026-10-08, platform: linux, method: test}]}
  codex: {min: "0.159.1", recommended: "0.161.0", entries: [{version: "0.161.0", class: inspected, date: 2026-10-08, platform: linux, method: test}]}
  gemini: {min: "0.63.0", recommended: "0.63.0", entries: [{version: "0.63.0", class: inspected, date: 2026-10-08, platform: linux, method: test}]}
  antigravity: {min: "1.3.0", recommended: "1.3.1", entries: []}
`))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDetectionRunsOnlyTheDeclaredProbes(t *testing.T) {
	_, calls := stubEnv(t, claudeTable("2.1.294", `{"loggedIn": true}`, 0))
	c := check(t, "claude", connections.Options{Compat: claudeCompat(t)})
	if c.Status != agents.StatusReady {
		t.Fatalf("%+v", c)
	}
	got := calls()
	want := []string{"claude --version", "claude auth status --json"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("invocations %v, want exactly %v (install guidance is never executed, nothing else is run)", got, want)
	}
	if c.InstallHint == "" || !strings.Contains(c.InstallHint, "claude.ai/install") {
		t.Fatalf("hint %q", c.InstallHint)
	}
}

func TestVersionBlockedAndUnreadableVersion(t *testing.T) {
	stubEnv(t, claudeTable("2.1.294", `{"loggedIn": true}`, 0))
	cp, err := manifests.LoadCompat([]byte(`
schema: 1
node: {min: "22"}
adapters: {claude: {min: "2.1.205", entries: []}}
blocked: [{adapter: claude, version: "2.1.294", reason: "crashes"}]
`))
	if err != nil {
		t.Fatal(err)
	}
	c := check(t, "claude", connections.Options{Compat: cp})
	if c.Status != agents.StatusUnsupportedVersion || c.Reason != connections.ReasonVersionBlocked {
		t.Fatalf("%+v", c)
	}
	// a CLI that prints nothing version-like
	stubEnv(t, stubTable{"claude": {"--version": ok1("hello\n")}})
	c = check(t, "claude", connections.Options{})
	if c.Status != agents.StatusDegraded || c.Reason != connections.ReasonVersionProbe || c.Usable || c.Installed != true {
		t.Fatalf("%+v", c)
	}
	// a CLI whose version probe fails to run at all
	stubEnv(t, stubTable{"claude": {}})
	c = check(t, "claude", connections.Options{})
	if c.Status != agents.StatusDegraded || c.Reason != connections.ReasonVersionProbe {
		t.Fatalf("%+v", c)
	}
}

func TestClaudeEnvWarningNamesTheVariableNeverTheValue(t *testing.T) {
	stubEnv(t, claudeTable("2.1.294", `{"loggedIn": true}`, 0))
	env := []string{"PATH=" + os.Getenv("PATH"), "ANTHROPIC_API_KEY=sk-ant-very-secret-value", "DRYDOCK_STUB_CLI=1", "DRYDOCK_STUB_TABLE=" + os.Getenv("DRYDOCK_STUB_TABLE")}
	c := check(t, "claude", connections.Options{Env: env, Compat: claudeCompat(t)})
	joined := strings.Join(c.Warnings, "\n")
	if !strings.Contains(joined, "ANTHROPIC_API_KEY") || strings.Contains(joined, "very-secret") {
		t.Fatalf("warnings %q", joined)
	}
	// it says who pays: a headless agent bills the key per token, not the subscription the person is signed in with
	if !strings.Contains(joined, "billed per token to that API key's account, not to the Claude subscription") {
		t.Fatalf("the warning does not say who pays: %q", joined)
	}
	c = check(t, "claude", connections.Options{Env: []string{"PATH=" + os.Getenv("PATH"), "DRYDOCK_STUB_CLI=1", "DRYDOCK_STUB_TABLE=" + os.Getenv("DRYDOCK_STUB_TABLE")}, Compat: claudeCompat(t)})
	if len(c.Warnings) != 0 {
		t.Fatalf("no variable is set, so no warning: %v", c.Warnings)
	}
}

func TestNodePrerequisite(t *testing.T) {
	geminiOK := map[string]stubReply{"--version": ok1("0.63.0\n")}
	t.Run("missing node blocks gemini (npm is its only install)", func(t *testing.T) {
		stubEnv(t, stubTable{"gemini": geminiOK})
		c := check(t, "gemini", connections.Options{Compat: claudeCompat(t)})
		if c.Status != agents.StatusNotInstalled || c.Reason != "prereq_missing:node" || c.Usable {
			t.Fatalf("%+v", c)
		}
		if len(c.Prereqs) != 1 || c.Prereqs[0].Found || !strings.Contains(strings.Join(c.Problems, " "), "Node 22 or newer is required") {
			t.Fatalf("prereqs %+v problems %v", c.Prereqs, c.Problems)
		}
		if strings.Contains(strings.Join(c.Problems, " "), "npm install") || strings.Contains(strings.Join(c.Problems, " "), "curl") {
			t.Fatal("guidance for a missing prerequisite must not be an installer command")
		}
	})
	t.Run("node 20 is too old (decision D9 sets 22)", func(t *testing.T) {
		stubEnv(t, stubTable{"gemini": geminiOK, "node": {"--version": ok1("v20.11.0\n")}})
		c := check(t, "gemini", connections.Options{Compat: claudeCompat(t)})
		if c.Status != agents.StatusNotInstalled || c.Prereqs[0].Version != "20.11.0" || c.Prereqs[0].OK {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("node 22 satisfies it", func(t *testing.T) {
		stubEnv(t, stubTable{"gemini": geminiOK, "node": {"--version": ok1("v22.22.0\n")}})
		c := check(t, "gemini", connections.Options{Compat: claudeCompat(t)})
		if c.Status != agents.StatusReady || len(c.Prereqs) != 1 || !c.Prereqs[0].OK || c.Prereqs[0].Version != "22.22.0" {
			t.Fatalf("%+v", c)
		}
		// gemini has no auth probe: ready with auth unknown, as the doc says
		if c.Authenticated != nil || !strings.Contains(c.AuthDetail, "no non-interactive auth probe") {
			t.Fatalf("auth %v %q", c.Authenticated, c.AuthDetail)
		}
		if len(c.Warnings) == 0 || !strings.Contains(c.Warnings[0], "API key") {
			t.Fatalf("the D2 warning must be on the Connection: %v", c.Warnings)
		}
	})
}

func TestCodexBridgeNeedsNodeAndNpx(t *testing.T) {
	codex := map[string]stubReply{"--version": ok1("codex-cli 0.161.0\n"), "login status": ok1("Logged in using ChatGPT\n")}
	t.Run("node and npx present", func(t *testing.T) {
		stubEnv(t, stubTable{"codex": codex, "node": {"--version": ok1("v22.22.0\n")}, "npx": {}})
		c := check(t, "codex", connections.Options{Compat: claudeCompat(t)})
		if c.Status != agents.StatusReady || c.Authenticated == nil || !*c.Authenticated {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("without node the exec fallback is declared but cannot start sessions", func(t *testing.T) {
		stubEnv(t, stubTable{"codex": codex})
		c := check(t, "codex", connections.Options{Compat: claudeCompat(t)})
		if c.Status != agents.StatusDegraded || !strings.HasPrefix(c.Reason, connections.ReasonTransport) || c.Usable {
			t.Fatalf("%+v", c)
		}
		if !strings.Contains(strings.Join(c.Problems, " "), "not implemented") {
			t.Fatalf("problems must say the fallback cannot start yet: %v", c.Problems)
		}
	})
	t.Run("node but no npx", func(t *testing.T) {
		stubEnv(t, stubTable{"codex": codex, "node": {"--version": ok1("v22.22.0\n")}})
		c := check(t, "codex", connections.Options{Compat: claudeCompat(t)})
		if c.Status != agents.StatusDegraded || !strings.Contains(strings.Join(c.Problems, " "), "npx") {
			t.Fatalf("%+v", c)
		}
	})
}

func TestCodexAuthProbeUsesTheExitCode(t *testing.T) {
	node := map[string]stubReply{"--version": ok1("v22.22.0\n")}
	for name, tc := range map[string]struct {
		login      stubReply
		wantStatus agents.ConnectionStatus
		wantAuth   *bool
	}{
		"logged out (exit 1, observed)": {stubReply{Stdout: "Not logged in\n", Exit: 1}, agents.StatusUnauthenticated, ptr(false)},
		"logged in (exit 0)":            {stubReply{Stdout: "Logged in using an API key\n"}, agents.StatusReady, ptr(true)},
		"unclassified exit code":        {stubReply{Exit: 7}, agents.StatusReady, nil},
	} {
		t.Run(name, func(t *testing.T) {
			stubEnv(t, stubTable{"codex": {"--version": ok1("codex-cli 0.161.0\n"), "login status": tc.login}, "node": node, "npx": {}})
			c := check(t, "codex", connections.Options{Compat: claudeCompat(t)})
			if c.Status != tc.wantStatus {
				t.Fatalf("%+v", c)
			}
			if (tc.wantAuth == nil) != (c.Authenticated == nil) || (tc.wantAuth != nil && *tc.wantAuth != *c.Authenticated) {
				t.Fatalf("auth %v", c.Authenticated)
			}
		})
	}
}

func TestOpencodeProviderListParsing(t *testing.T) {
	const logged = "\x1b[0m\n┌  Credentials \x1b[90m~/.local/share/opencode/auth.json\x1b[0m\n│\n└  2 credentials\n"
	const none = "\x1b[0m\n┌  Credentials \x1b[90m~/.local/share/opencode/auth.json\x1b[0m\n│\n└  0 credentials\n"
	for name, tc := range map[string]struct {
		out      string
		wantAuth *bool
	}{"two credentials": {logged, ptr(true)}, "none (free models may still work, so unknown)": {none, nil}, "unparseable": {"???", nil}} {
		t.Run(name, func(t *testing.T) {
			stubEnv(t, stubTable{"opencode": {"--version": ok1("1.18.35\n"), "providers list": ok1(tc.out)}})
			c := check(t, "opencode", connections.Options{Compat: compat(t)})
			if c.Status != agents.StatusReady {
				t.Fatalf("%+v", c)
			}
			if (tc.wantAuth == nil) != (c.Authenticated == nil) || (tc.wantAuth != nil && *tc.wantAuth != *c.Authenticated) {
				t.Fatalf("auth %v (%s)", c.Authenticated, c.AuthDetail)
			}
		})
	}
	// the probe output is never copied into the Connection beyond the count
	stubEnv(t, stubTable{"opencode": {"--version": ok1("1.18.35\n"), "providers list": ok1("... sk-live-abcdef 3 credentials")}})
	c := check(t, "opencode", connections.Options{Compat: compat(t)})
	if strings.Contains(c.AuthDetail, "sk-live") {
		t.Fatalf("detail leaked probe text: %q", c.AuthDetail)
	}
}

func TestAntigravityGateControlsUsable(t *testing.T) {
	stubEnv(t, stubTable{"agy": {"--version": ok1("agy 1.3.1\n"), "-p /usage": ok1("quota ...\n")}})
	o := connections.Options{Compat: claudeCompat(t)}
	c := check(t, "antigravity", o)
	if len(c.Gates) != 1 || c.Gates[0].Acked || c.Gates[0].ID != "antigravity-third-party-v1" || !strings.Contains(c.Gates[0].Text, "no Google statement") {
		t.Fatalf("gates %+v", c.Gates)
	}
	if c.Usable {
		t.Fatal("an unacknowledged gate must make the Connection unusable")
	}
	// a gate does not change the status enum (docs/connections.md section 1)
	if c.Status != agents.StatusDegraded || c.Reason != connections.ReasonVersionUntested {
		t.Fatalf("%+v", c) // antigravity has never been run, so any version is untested
	}
	o.Acked = map[string]bool{"antigravity-third-party-v1": true}
	c = check(t, "antigravity", o)
	if !c.Gates[0].Acked || !c.Usable {
		t.Fatalf("after acknowledging: %+v", c)
	}
	// the auth probe for agy is flagged unverified in the detail
	if !strings.Contains(c.AuthDetail, "unverified") {
		t.Fatalf("auth detail %q", c.AuthDetail)
	}
}

func TestLastFailedTestDegrades(t *testing.T) {
	stubEnv(t, claudeTable("2.1.294", `{"loggedIn": true}`, 0))
	o := connections.Options{Compat: claudeCompat(t), Last: map[string]connections.TestOutcome{"claude": {OK: false, Reason: "T3: the turn ended without any assistant text"}}}
	c := check(t, "claude", o)
	if c.Status != agents.StatusDegraded || !strings.HasPrefix(c.Reason, connections.ReasonTestFailed+":") || !c.Usable {
		t.Fatalf("%+v", c)
	}
	o.Last["claude"] = connections.TestOutcome{OK: true}
	if c = check(t, "claude", o); c.Status != agents.StatusReady {
		t.Fatalf("%+v", c)
	}
}

func TestProbeTimeoutDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		// a .cmd shim (how npm installs CLIs) whose child never answers: the probe must kill the whole tree
		ping, err := exec.LookPath("ping")
		if err != nil {
			t.Skip("no ping here to write a hanging CLI")
		}
		if err := os.WriteFile(filepath.Join(dir, "claude.cmd"), []byte("@\""+ping+"\" -n 31 127.0.0.1 >nul\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
	} else {
		sh, err := exec.LookPath("sh")
		if err != nil {
			t.Skip("no sh here to write a hanging CLI")
		}
		if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!"+sh+"\nsleep 30\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+filepath.Dir(sh))
	}
	began := time.Now()
	c := check(t, "claude", connections.Options{Timeout: 400 * time.Millisecond})
	if c.Status != agents.StatusDegraded || c.Reason != connections.ReasonVersionProbe || !strings.Contains(strings.Join(c.Problems, " "), "did not answer") {
		t.Fatalf("%+v", c)
	}
	if time.Since(began) > 10*time.Second {
		t.Fatalf("probe took %s", time.Since(began))
	}
}

func TestConnectionJSONShape(t *testing.T) {
	stubEnv(t, claudeTable("2.1.294", `{"loggedIn": true}`, 0))
	c := check(t, "claude", connections.Options{Compat: claudeCompat(t)})
	if c.CompatClass != "verified_linux" || c.Transport != "headless" || c.Platform == "" || c.Version != "2.1.294" {
		t.Fatalf("%+v", c)
	}
}
