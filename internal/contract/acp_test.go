package contract_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/conformance"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/proc"
)

// locateACP finds the command of an ACP target: DRYDOCK_ACP_<NAME> (a full command prefix, which the runner sets from what it
// detected), else the program on PATH. A CLI that speaks ACP itself gets the arguments its manifest starts it with.
func locateACP(t *testing.T, set *manifests.Set, tg contract.ACPTarget) (string, []string) {
	t.Helper()
	var args []string
	if !tg.Bridge {
		m, ok := set.Get(tg.Adapter)
		if !ok || m.Transports.ACP == nil || len(m.Transports.ACP.Argv) == 0 {
			t.Fatalf("manifest %s has no ACP transport", tg.Adapter)
		}
		args = append(args, m.Transports.ACP.Argv[1:]...)
	}
	if v := os.Getenv(tg.OverrideVar()); v != "" {
		f := strings.Fields(v)
		return f[0], append(f[1:], args...)
	}
	if p, err := exec.LookPath(tg.Bin); err == nil {
		return p, args
	}
	t.Skipf("%s is not installed here (not on PATH and %s is unset)", tg.Bin, tg.OverrideVar())
	return "", nil
}

// bridgeEnv is what the manifest passes a bridge: the Claude ACP bridge runs the claude it is given.
func bridgeEnv(tg contract.ACPTarget) []string {
	if tg.Name == "claude-agent-acp" {
		if p, err := exec.LookPath("claude"); err == nil {
			return []string{"CLAUDE_CODE_EXECUTABLE=" + p}
		}
	}
	return nil
}

// throwawayHome points every configuration and credential location at an empty folder, so a handshake reads nothing of the
// user's (the live tests do the same).
func throwawayHome(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil { // codex refuses a CODEX_HOME that does not exist
		t.Fatal(err)
	}
	return []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "CODEX_HOME=" + filepath.Join(home, ".codex")}
}

// ACP-INIT-01 (free): the initialize handshake of every ACP agent installed here. No prompt is sent and no credential is read.
func TestACPInitializeHandshake(t *testing.T) {
	set, err := manifests.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range contract.ACPTargets {
		t.Run(tg.Name, func(t *testing.T) {
			if os.Getenv(contract.EnvNoRealAgents) == "1" {
				t.Skip(contract.EnvNoRealAgents + "=1")
			}
			cmd, args := locateACP(t, set, tg)
			env := append(append(throwawayHome(t), tg.Extra...), bridgeEnv(tg)...)
			ad := acp.New(acp.Config{ID: tg.Name, Command: cmd, Args: args, Env: env, ScrubEnv: true, StartTimeout: 120 * time.Second})
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			h, err := ad.Handshake(ctx, proc.New(), agents.RunSpec{AgentID: "contract-" + tg.Name, Cwd: t.TempDir()})
			if err != nil {
				t.Fatalf("initialize: %v", err)
			}
			i := h.Info
			var methods []string
			for _, m := range i.AuthMethods {
				methods = append(methods, m.ID)
			}
			session := "opened"
			if !h.SessionOK {
				session = "refused: " + firstLineOf(h.SessionErr)
			}
			t.Logf("%s%s %s, protocol %d, auth methods [%s], session/new without credentials %s", contract.NotePrefix, i.Name, i.Version, i.ProtocolVersion,
				strings.Join(methods, ", "), session)
			if i.ProtocolVersion != 1 {
				t.Errorf("initialize answered protocol version %d; Drydock speaks ACP version 1", i.ProtocolVersion)
			}
			if i.Name == "" || i.Version == "" {
				t.Errorf("initialize names no agent or no version: %+v", i)
			}
			switch {
			case len(i.AuthMethods) == 0 && !tg.NoAuthMethods:
				t.Errorf("initialize offers no authentication methods, so Drydock cannot say how to sign in")
			case len(i.AuthMethods) > 0 && tg.NoAuthMethods:
				t.Logf("%s%s now lists authentication methods to a client without terminal-auth: %v", contract.NotePrefix, tg.Name, methods)
			}
		})
	}
}

// ACP-PROMPT-01 (paid): a prompt turn on each ACP agent that is signed in here. The agents are run with the user's own
// configuration and sign-in, so the check runs only for those listed in DRYDOCK_CONTRACT_ACP_CREDENTIALS.
func TestACPPromptTurn(t *testing.T) {
	set, err := manifests.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	signedIn := map[string]bool{}
	for _, n := range strings.Split(os.Getenv(contract.EnvACPCredentials), ",") {
		if n = strings.TrimSpace(n); n != "" {
			signedIn[n] = true
		}
	}
	for _, tg := range contract.ACPTargets {
		t.Run(tg.Name, func(t *testing.T) {
			if tg.Name == "claude-agent-acp" {
				t.Skip("no credentials are used: the Claude ACP bridge stays disabled (decision D1), so no prompt is sent through it")
			}
			if !signedIn[tg.Name] {
				t.Skipf("no credentials: %s is not signed in here (list it in %s where it is)", tg.Name, contract.EnvACPCredentials)
			}
			if os.Getenv(contract.EnvNoRealAgents) == "1" {
				t.Skip(contract.EnvNoRealAgents + "=1")
			}
			cmd, args := locateACP(t, set, tg)
			budgetFor(t, 0.05)
			ad := acp.New(acp.Config{ID: tg.Name, Command: cmd, Args: args, Env: append(append([]string(nil), tg.Extra...), bridgeEnv(tg)...), StartTimeout: 120 * time.Second})
			conformance.Run(t, ad, conformance.Options{Prompt: "Reply with the single word OK.", ExpectText: "OK"})
		})
	}
}

func firstLineOf(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(l); len(r) > 160 {
		l = string(r[:157]) + "..."
	}
	return l
}
