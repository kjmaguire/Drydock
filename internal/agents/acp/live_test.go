package acp_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/proc"
)

// Live handshakes against the real CLIs. They run `initialize` (and try `session/new`) on the real binary, with a scrubbed
// environment and a throwaway HOME so no user configuration or credential is read, then stop the process. No prompt is sent:
// there are no model credentials here, so a real turn would be a fake pass.
//
// Where the binaries come from: PATH, or DRYDOCK_ACP_<NAME> holding the full command prefix, for example
//
//	DRYDOCK_ACP_GEMINI="node /path/to/gemini.js"   DRYDOCK_ACP_OPENCODE=/path/to/opencode
//	DRYDOCK_ACP_GOOSE=/path/to/goose               DRYDOCK_ACP_CODEX_ACP=/path/to/codex-acp
//
// A missing binary skips the test with that reason.

type liveCase struct {
	name    string
	env     string   // DRYDOCK_ACP_<env>
	bin     string   // PATH lookup name
	args    []string // appended to the located command
	extra   []string // extra environment
	version string   // expected agentInfo.version ("" = just log)
	check   func(t *testing.T, i acp.AgentInfo)
}

var liveCases = []liveCase{
	{name: "gemini", env: "GEMINI", bin: "gemini", args: []string{"--acp"}, extra: []string{"GEMINI_CLI_TRUST_WORKSPACE=true", "NO_BROWSER=1"}, version: "0.63.0",
		check: func(t *testing.T, i acp.AgentInfo) {
			if !i.LoadSession || i.Resume || i.Fork || i.List || !i.MCPHTTP || !i.MCPSSE || !i.Image {
				t.Errorf("gemini capabilities differ from docs/phase0/agent-clis.md section 7.1: %+v", i)
			}
		}},
	{name: "opencode", env: "OPENCODE", bin: "opencode", args: []string{"acp"}, version: "1.18.35",
		check: func(t *testing.T, i acp.AgentInfo) {
			if !i.LoadSession || !i.Resume || !i.Fork || !i.List || !i.Close || !i.MCPHTTP || !i.MCPSSE {
				t.Errorf("opencode capabilities differ from docs/phase0/agent-clis.md section 7.1: %+v", i)
			}
		}},
	{name: "goose", env: "GOOSE", bin: "goose", args: []string{"acp"}, extra: []string{"GOOSE_MODE=approve"}, version: "1.53.0",
		check: func(t *testing.T, i acp.AgentInfo) {
			if !i.LoadSession || i.Resume || i.Fork || !i.List || !i.Delete || !i.Close || !i.MCPHTTP || i.MCPSSE {
				t.Errorf("goose capabilities differ from docs/phase0/agent-clis.md section 7.1: %+v", i)
			}
		}},
	{name: "codex-acp", env: "CODEX_ACP", bin: "codex-acp", version: "",
		check: func(t *testing.T, i acp.AgentInfo) {
			if !i.LoadSession || !i.Resume || !i.Fork || !i.List || !i.Close || !i.Delete || !i.MCPHTTP || i.MCPSSE || i.MCPACP {
				t.Errorf("codex-acp capabilities differ from docs/phase0/agent-clis.md section 7.1: %+v", i)
			}
		}},
}

func locate(t *testing.T, c liveCase) (string, []string) {
	t.Helper()
	if v := os.Getenv("DRYDOCK_ACP_" + c.env); v != "" {
		f := strings.Fields(v)
		return f[0], append(f[1:], c.args...)
	}
	if p, err := exec.LookPath(c.bin); err == nil {
		return p, c.args
	}
	t.Skipf("%s is not installed here (not on PATH and DRYDOCK_ACP_%s is unset); live handshake not run", c.bin, c.env)
	return "", nil
}

func TestLiveACPHandshake(t *testing.T) {
	for _, c := range liveCases {
		t.Run(c.name, func(t *testing.T) {
			cmd, args := locate(t, c)
			home := t.TempDir()
			if err := os.MkdirAll(home+"/.codex", 0o755); err != nil { // codex refuses a CODEX_HOME that does not exist
				t.Fatal(err)
			}
			env := append([]string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home + "/.config", "XDG_DATA_HOME=" + home + "/.local/share",
				"XDG_STATE_HOME=" + home + "/.local/state", "XDG_CACHE_HOME=" + home + "/.cache", "CODEX_HOME=" + home + "/.codex"}, c.extra...)
			ad := acp.New(acp.Config{ID: c.name, Command: cmd, Args: args, Env: env, ScrubEnv: true, StartTimeout: 120 * time.Second})
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			h, err := ad.Handshake(ctx, proc.New(), agents.RunSpec{AgentID: "live-" + c.name, Cwd: t.TempDir()})
			if err != nil {
				t.Fatalf("handshake: %v", err)
			}
			t.Logf("%s: agent=%q version=%q protocol=%d initialize=%s session/new ok=%v err=%q",
				c.name, h.Info.Name, h.Info.Version, h.Info.ProtocolVersion, h.InitElapsed.Round(time.Millisecond), h.SessionOK, h.SessionErr)
			t.Logf("%s matrix: %s", c.name, strings.Join(h.Info.Matrix(), " "))
			if h.SessionOK {
				t.Logf("%s session: modes=%v models=%d efforts=%v currentModel=%q", c.name, h.Session.Modes, len(h.Session.Models), h.Session.Efforts, h.Session.CurrentModel)
			}
			if h.Info.ProtocolVersion != 1 || h.Info.Name == "" || h.PID == 0 {
				t.Fatalf("not a sane initialize result: %+v", h.Info)
			}
			if c.version != "" && h.Info.Version != c.version {
				t.Errorf("version %q, docs/compat expect %q", h.Info.Version, c.version)
			}
			c.check(t, h.Info)
			if len(h.Info.AuthMethods) == 0 {
				t.Logf("%s offered no authMethods", c.name)
			}
			// capabilities merge from what the agent said, not from the name
			caps := ad.Capabilities()
			if caps.SessionResume != (h.Info.LoadSession || h.Info.Resume) || caps.ImageInput != h.Info.Image {
				t.Errorf("capability merge disagrees with initialize: %+v vs %+v", caps, h.Info)
			}
		})
	}
}
