package connections_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/connections"
)

// Tests against the REAL CLIs. They run only when the CLI is installed (and, for claude, logged in with the user's own login,
// decision D1) and skip with the reason otherwise (decision P3). Set DRYDOCK_NO_REAL_AGENTS=1 to skip them explicitly.

func TestRealClaudeDetection(t *testing.T) {
	if os.Getenv("DRYDOCK_NO_REAL_AGENTS") == "1" {
		t.Skip("DRYDOCK_NO_REAL_AGENTS=1")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude is not installed here")
	}
	svc, err := connections.NewService(connections.Config{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.Check(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("claude %s at %s: status=%s reason=%q auth=%v (%s) compat=%s warnings=%v", c.Version, c.Path, c.Status, c.Reason, c.Authenticated, c.AuthDetail, c.CompatClass, c.Warnings)
	if !c.Installed || c.Version == "" {
		t.Fatalf("%+v", c)
	}
	if c.Status == agents.StatusUnauthenticated {
		t.Skipf("claude is not logged in here: %v", c.Problems)
	}
	if c.Authenticated == nil || !*c.Authenticated {
		t.Fatalf("auth probe did not answer: %+v", c)
	}
}

func TestRealClaudeConnection(t *testing.T) {
	if os.Getenv("DRYDOCK_NO_REAL_AGENTS") == "1" {
		t.Skip("DRYDOCK_NO_REAL_AGENTS=1")
	}
	svc, err := connections.NewService(connections.Config{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := svc.Check(context.Background(), "claude")
	if !c.Installed || c.Status == agents.StatusUnauthenticated || c.Status == agents.StatusUnsupportedVersion {
		t.Skipf("real claude not usable here: status=%s problems=%v", c.Status, c.Problems)
	}
	opts := connections.TestOptions{Prompt: true, Model: "haiku", Cancel: true, Env: nil}
	t.Log(connections.Describe(opts))
	rep, err := svc.TestConnection(context.Background(), "claude", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + rep.Summary)
	if !rep.OK {
		t.Fatalf("Test connection against real claude %s did not pass:\n%s", rep.CLIVersion, rep.Summary)
	}
	s := stepMap(rep)
	for _, id := range []string{"T1", "T2", "T3", "T4", "T5"} {
		if s[id].Status != connections.StepPass {
			t.Errorf("%s: %+v", id, s[id])
		}
	}
	if rep.TokensUsed == 0 || len(rep.Usage) == 0 || rep.Usage[0].Source != "reported" {
		t.Errorf("usage %+v tokens=%d", rep.Usage, rep.TokensUsed)
	}
	t.Logf("measured %d tokens, usage=%+v", rep.TokensUsed, rep.Usage)
}

// liveCLI points the registry's command name at a located binary by putting it (or a one-line wrapper) first on PATH.
func liveCLI(t *testing.T, name, env string) {
	t.Helper()
	spec := os.Getenv("DRYDOCK_ACP_" + env)
	var argv []string
	if spec != "" {
		argv = strings.Fields(spec)
	} else if p, err := exec.LookPath(name); err == nil {
		argv = []string{p}
	} else {
		t.Skipf("%s is not installed here (not on PATH and DRYDOCK_ACP_%s is unset)", name, env)
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, name)
	if len(argv) == 1 {
		if err := os.Symlink(argv[0], dst); err != nil {
			t.Skipf("cannot link %s: %v", name, err)
		}
	} else { // "node /path/gemini.js": a wrapper script that execs the command line
		sh, err := exec.LookPath("sh")
		if err != nil {
			t.Skip("no sh to wrap " + spec)
		}
		quoted := make([]string, len(argv))
		for i, a := range argv {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		if err := os.WriteFile(dst, []byte("#!"+sh+"\nexec "+strings.Join(quoted, " ")+" \"$@\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// node must stay reachable for the npm-distributed CLIs; keep the original PATH behind the shim directory
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := t.TempDir() // a throwaway HOME: no user configuration or credential is read
	_ = os.MkdirAll(filepath.Join(home, ".codex"), 0o755)
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "CODEX_HOME"} {
		v := home
		switch k {
		case "XDG_CONFIG_HOME":
			v = home + "/.config"
		case "XDG_DATA_HOME":
			v = home + "/.local/share"
		case "XDG_STATE_HOME":
			v = home + "/.local/state"
		case "XDG_CACHE_HOME":
			v = home + "/.cache"
		case "CODEX_HOME":
			v = home + "/.codex"
		}
		t.Setenv(k, v)
	}
}

// Detection and the handshake half of Test connection against the real ACP CLIs, no prompt (no model credentials here).
func TestLiveACPConnections(t *testing.T) {
	for _, tc := range []struct {
		id, env string
		wantT2b string // expected outcome of session/new without credentials, from the live runs
	}{
		{"gemini", "GEMINI", connections.StepFail},
		{"opencode", "OPENCODE", connections.StepPass},
		{"goose", "GOOSE", connections.StepFail},
	} {
		t.Run(tc.id, func(t *testing.T) {
			liveCLI(t, tc.id, tc.env)
			svc, err := connections.NewService(connections.Config{Manifests: manifests.Options{ScrubEnv: true}})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			c, _ := svc.Check(ctx, tc.id)
			t.Logf("%s %s: status=%s reason=%q auth=%v (%s) compat=%s", tc.id, c.Version, c.Status, c.Reason, c.Authenticated, c.AuthDetail, c.CompatClass)
			if !c.Installed || c.Version == "" || c.Status != agents.StatusReady {
				t.Fatalf("a verified version with its prerequisites must be ready: %+v", c)
			}
			rep, err := svc.TestConnection(ctx, tc.id, connections.TestOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Log("\n" + rep.Summary)
			s := stepMap(rep)
			if s["T1"].Status != connections.StepPass || s["T2"].Status != connections.StepPass {
				t.Fatalf("%+v", rep.Steps)
			}
			if s["T2b"].Status != tc.wantT2b {
				t.Errorf("T2b is %s, the live runs saw %s", s["T2b"].Status, tc.wantT2b)
			}
			if s["T3"].Status != connections.StepSkipped || !strings.Contains(s["T3"].Reason, "not requested") {
				t.Errorf("T3 %+v", s["T3"])
			}
			if len(rep.AgentMatrix) == 0 || rep.Matrix["session_resume"].Provenance != "observed" {
				t.Errorf("matrix %+v", rep.Matrix["session_resume"])
			}
		})
	}
}
