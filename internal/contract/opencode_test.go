package contract_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The OpenCode contracts (paid): what Drydock's ACP adapter assumes about OpenCode beyond a plain prompt turn. They run the
// signed-in opencode with the user's own configuration, as ACP-PROMPT-01 does, and only when it is listed in
// DRYDOCK_CONTRACT_ACP_CREDENTIALS; DRYDOCK_NO_REAL_AGENTS=1 skips them. The adapter is configured from the OpenCode manifest
// (its inline configuration and its client_meta opt-in), as Drydock starts it.

// openCode returns the OpenCode ACP adapter as the manifest configures it, or skips.
func openCode(t *testing.T, est float64) *acp.Adapter {
	t.Helper()
	if os.Getenv(contract.EnvNoRealAgents) == "1" {
		t.Skip(contract.EnvNoRealAgents + "=1")
	}
	signedIn := false
	for _, n := range strings.Split(os.Getenv(contract.EnvACPCredentials), ",") {
		signedIn = signedIn || strings.TrimSpace(n) == "opencode"
	}
	if !signedIn {
		t.Skipf("no credentials: opencode is not signed in here (list it in %s where it is)", contract.EnvACPCredentials)
	}
	set, err := manifests.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	tg, _ := contract.ACPTargetByName("opencode")
	cmd, args := locateACP(t, set, tg)
	budgetFor(t, est)
	m, _ := set.Get("opencode")
	var env []string
	keys := make([]string, 0, len(m.Transports.ACP.Env))
	for k := range m.Transports.ACP.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+m.Transports.ACP.Env[k])
	}
	return acp.New(acp.Config{ID: "opencode", Command: cmd, Args: args, Env: env, ClientMeta: m.Transports.ACP.ClientMeta, StartTimeout: 120 * time.Second})
}

func startOpenCode(t *testing.T, ad *acp.Adapter, spec agents.RunSpec) *acp.Session {
	t.Helper()
	if spec.Cwd == "" {
		spec.Cwd = t.TempDir()
	}
	spec.AgentID = "contract-opencode"
	ctx, cancel := context.WithCancel(context.Background())
	s, err := ad.Start(ctx, proc.New(), spec)
	if err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = s.Stop(sctx)
		cancel()
	})
	return s.(*acp.Session)
}

// openCodeTurn sends prompt and returns the turn's chunks up to its done chunk.
func openCodeTurn(t *testing.T, s *acp.Session, prompt string) []workflow.Chunk {
	t.Helper()
	if err := s.Send(context.Background(), prompt); err != nil {
		t.Fatalf("send: %v", err)
	}
	var cs []workflow.Chunk
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case c, ok := <-s.Output():
			if !ok {
				t.Fatalf("output closed before the turn ended; stderr %s", tail(s.Stderr()))
			}
			cs = append(cs, c)
			if c.Kind == workflow.ChunkDone {
				return cs
			}
		case <-timer.C:
			t.Fatalf("the turn did not end within 5 minutes")
		}
	}
}

// openCodeUsage is the turn's usage chunk (zero when the turn reported none) and the session's cumulative cost it carried.
func openCodeUsage(t *testing.T, cs []workflow.Chunk) (workflow.UsageChunk, float64, bool) {
	t.Helper()
	for _, c := range cs {
		if c.Kind != workflow.ChunkUsage {
			continue
		}
		var u workflow.UsageChunk
		if err := c.Decode(&u); err != nil {
			t.Fatal(err)
		}
		var tot struct {
			CostUSD *float64 `json:"costUsd"`
		}
		if len(u.Totals) > 0 && json.Unmarshal(u.Totals, &tot) == nil && tot.CostUSD != nil {
			return u, *tot.CostUSD, true
		}
		return u, 0, false
	}
	return workflow.UsageChunk{}, 0, false
}

// v1 reports whether the agent is OpenCode 1.x, which is not known to send compaction markers or child-session updates.
func v1(s *acp.Session) bool { return strings.HasPrefix(s.Info().Version, "1.") }

// OPENCODE-USAGE-01 (paid): usage_update.cost is the session's cumulative cost, earlier processes included; started from the
// session's last totals (RunSpec.UsageBaseline), the adapter records only the resumed turn.
func TestOpenCodeResumedSessionReportsCumulativeCost(t *testing.T) {
	ad := openCode(t, 0.05)
	cwd := t.TempDir()
	first := startOpenCode(t, ad, agents.RunSpec{Cwd: cwd})
	u1, total1, ok := openCodeUsage(t, openCodeTurn(t, first, "Reply with the single word OK."))
	if !ok {
		t.Fatalf("the turn reported no cumulative cost (usage %+v): Drydock records OpenCode's cost from usage_update.cost", u1)
	}
	if total1 <= 0 {
		t.Skipf("the model reported a cost of 0 (a free model?), so cumulative and per-process totals cannot be told apart")
	}
	sid := first.ID()
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = first.Stop(sctx)
	cancel()

	second := startOpenCode(t, ad, agents.RunSpec{Cwd: cwd, ResumeID: sid, UsageBaseline: u1.Totals})
	u2, total2, ok := openCodeUsage(t, openCodeTurn(t, second, "Reply with the single word AGAIN."))
	recordSpend(t, "OPENCODE-USAGE-01", total2)
	if !ok {
		t.Fatalf("the resumed turn reported no cumulative cost: %+v", u2)
	}
	t.Logf("%sOpenCode %s: first process $%.6f, after the resumed turn $%.6f, the resumed turn recorded $%.6f", contract.NotePrefix,
		second.Info().Version, total1, total2, u2.CostUSD)
	if total2 <= total1 {
		t.Fatalf("after a resume usage_update.cost is $%.6f, not above the first process's $%.6f: it is no longer the session's cumulative cost, so the baseline would under-count", total2, total1)
	}
	if d := total2 - total1; u2.CostUSD <= 0 || u2.CostUSD > d+1e-9 || u2.CostUSD < d-1e-9 {
		t.Fatalf("the resumed turn recorded $%.6f; its own share is $%.6f", u2.CostUSD, d)
	}
}

// OPENCODE-COMPACT-01 (paid): a compaction of the session's context is reported as a session_info_update with
// _meta["opencode/compaction"] {status: completed} to a client that does not advertise ACP's compaction capability, and the
// adapter counts it. The compaction is asked for with OpenCode's /compact command.
func TestOpenCodeReportsACompaction(t *testing.T) {
	ad := openCode(t, 0.05)
	s := startOpenCode(t, ad, agents.RunSpec{})
	openCodeTurn(t, s, "Reply with the single word OK.")
	cs := openCodeTurn(t, s, "/compact")
	_, total, _ := openCodeUsage(t, cs)
	recordSpend(t, "OPENCODE-COMPACT-01", total)
	n := s.Compactions()
	t.Logf("%sOpenCode %s: %d compaction(s) reported for /compact", contract.NotePrefix, s.Info().Version, n)
	switch {
	case n == 0 && v1(s):
		t.Skipf("OpenCode %s reported no compaction; 1.x is not known to report one, and Drydock then reads none (the session policy falls back on occupancy)", s.Info().Version)
	case n == 0:
		t.Fatalf("/compact ended (stop %v) without a completed compaction marker: Drydock no longer sees OpenCode's compactions; stderr %s", stopOf(cs), tail(s.Stderr()))
	}
	if u, _, _ := openCodeUsage(t, cs); u.Source != "" && u.Compactions == 0 {
		t.Errorf("the compaction is not in the turn's usage chunk: %+v", u)
	}
}

// OPENCODE-CHILD-01 (paid): OpenCode advertises opencode/child-session-updates, and with Drydock's opt-in it sends a
// sub-agent's updates on opencode/session/child_update; the adapter turns the child's tool calls into the turn's tool events.
func TestOpenCodeForwardsSubAgentToolCalls(t *testing.T) {
	ad := openCode(t, 0.10)
	cwd := t.TempDir()
	for _, f := range []string{"alpha.txt", "beta.txt"} {
		if err := os.WriteFile(filepath.Join(cwd, f), []byte(f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := startOpenCode(t, ad, agents.RunSpec{Cwd: cwd})
	advertised := strings.Contains(string(s.Info().Meta), acp.ChildSessionUpdatesMeta)
	cs := openCodeTurn(t, s, "Use your task tool to start a general subagent with this prompt: \"List the files in the current "+
		"directory with your glob or list tool, then reply with their names.\" Then reply with the single word FINISHED.")
	_, total, _ := openCodeUsage(t, cs)
	recordSpend(t, "OPENCODE-CHILD-01", total)
	var child []string
	for _, c := range cs {
		if c.Kind != workflow.ChunkToolCall {
			continue
		}
		var tc workflow.ToolCall
		if c.Decode(&tc) == nil && strings.Contains(tc.ID, ":") {
			child = append(child, tc.Name)
		}
	}
	t.Logf("%sOpenCode %s: advertised=%v, %d child update(s), child tool calls %v", contract.NotePrefix, s.Info().Version, advertised, s.ChildUpdates(), child)
	switch {
	case !advertised && v1(s):
		t.Skipf("OpenCode %s does not advertise %s; 1.x is not known to, and its sub-agents then report on the session's own stream", s.Info().Version, acp.ChildSessionUpdatesMeta)
	case !advertised:
		t.Fatalf("agentCapabilities._meta does not advertise %s: %s", acp.ChildSessionUpdatesMeta, s.Info().Meta)
	case s.ChildUpdates() == 0:
		t.Fatalf("no %s notification reached the adapter (did the model use the task tool? tool calls: %d)", acp.ChildUpdateMethod, len(child))
	case len(child) == 0:
		t.Fatalf("child updates arrived but none became a tool event of the turn")
	}
}

func stopOf(cs []workflow.Chunk) string {
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i].Kind == workflow.ChunkDone {
			var d workflow.DoneChunk
			_ = cs[i].Decode(&d)
			return d.StopReason
		}
	}
	return ""
}
