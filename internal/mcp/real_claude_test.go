package mcp

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/contract"
)

// TestRealClaudeTransports points the real Claude Code CLI at the gateway with each generated configuration and asks it to
// read a file through the gateway's read_range tool. The proof that a configuration works is the gateway's own audit event for
// the call, not the model's words. It runs only with DRYDOCK_REAL_CLAUDE=1 (it spends tokens) and when claude is installed and
// logged in. Each subtest costs a few thousandths of a dollar on the cheapest model.
//
// Observations on this host (claude 2.1.294, Linux sandbox) are recorded in docs/mcp.md "As built".
func TestRealClaudeTransports(t *testing.T) {
	if why := contract.RealClaudeSkip(); why != "" {
		t.Skip(why)
	}
	exe, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed")
	}
	if out, err := exec.Command(exe, "auth", "status").Output(); err != nil || !bytes.Contains(out, []byte(`"loggedIn": true`)) && !bytes.Contains(out, []byte(`"loggedIn":true`)) {
		t.Skipf("claude is not logged in: %v", err)
	}
	self, _ := os.Executable()
	for _, tc := range []struct {
		name      string
		transport string
		required  bool // the shim route is the one Drydock ships; the header route is an experiment
	}{{"stdio-shim", TransportShim, true}, {"http-bearer-header", TransportDirectHTTP, false}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, func(o *Options) { o.ShimCommand = []string{self, "mcp-shim"} })
			wt := t.TempDir()
			os.WriteFile(filepath.Join(wt, "notes.txt"), []byte("line one\nthe vault code is ZEBRA-4471\nline three\n"), 0o644)
			ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-real-" + strings.ReplaceAll(tc.name, "-", ""), ProjectID: "p", Role: "backend", Worktree: wt},
				Policy: allow(map[string][]string{WorkspaceServerID: {"read_range"}})})
			cfg, err := ConfigFor("claude", *ep, ConfigOptions{Transport: tc.transport})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "mcp.json")
			if err := WriteConfig(path, cfg); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()
			args := append(cfg.ArgsFor(path), "-p", "--model", "haiku", "--no-session-persistence", "--allowedTools", strings.Join(cfg.AllowedTools, ","))
			cmd := exec.CommandContext(ctx, exe, args...)
			cmd.Stdin = strings.NewReader("Call the read_range tool of the MCP server named drydock for the path notes.txt. Reply with only the vault code in the file.")
			cmd.Dir = t.TempDir() // not the worktree: the file can only come through the gateway
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			runErr := cmd.Run()
			reply := strings.TrimSpace(out.String())
			arrived := false
			for _, ev := range e.events(EventCall) {
				d := eventData(t, ev)
				t.Logf("gateway audit: tool=%v decision=%v", d["tool"], d["decision"])
				if d["tool"] == "read_range" && d["decision"] == DecisionAllowed {
					arrived = true
				}
			}
			t.Logf("[%s] claude exit err=%v reply=%q gateway_call_arrived=%v auth_failures=%d", tc.name, runErr, reply, arrived, len(e.events(EventAuthFailed)))
			if strings.Contains(reply, ep.Token) {
				t.Fatal("the run token reached the model")
			}
			if !arrived || !strings.Contains(reply, "ZEBRA-4471") {
				if tc.required {
					t.Fatalf("the %s configuration does not work with the real Claude Code: %s", tc.name, reply)
				}
				t.Logf("[%s] EXPERIMENT RESULT: this route did NOT work (%s)", tc.name, reply)
				return
			}
			t.Logf("[%s] EXPERIMENT RESULT: this route works", tc.name)
		})
	}
}
