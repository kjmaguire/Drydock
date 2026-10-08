package runtime_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Integration tests against the REAL Claude Code CLI. They run only when `claude` is installed and logged in on this
// machine (the user's own login, per decision D1) and are skipped, with the reason, otherwise. They use the cheapest model
// and tiny prompts. Set DRYDOCK_NO_REAL_AGENTS=1 to skip them explicitly.
func realClaude(t *testing.T) (*claude.Adapter, string) {
	t.Helper()
	if os.Getenv("DRYDOCK_NO_REAL_AGENTS") == "1" {
		t.Skip("DRYDOCK_NO_REAL_AGENTS=1")
	}
	// the TestMain fake-agent switch must not leak into a real run
	t.Setenv("DRYDOCK_FAKE_AGENT", "")
	ad := claude.New()
	d, err := ad.Detect(context.Background())
	if err != nil || d.Status != agents.StatusReady {
		t.Skipf("real claude not usable here: status=%s problems=%v", d.Status, d.Problems)
	}
	t.Logf("real claude: %s at %s (auth: %s)", d.Version, d.Path, d.AuthDetail)
	return ad, d.Version
}

func realRuntime(t *testing.T, ad agents.Adapter) (*rt.Runtime, *storage.DB, *events.SQLBus, string) {
	dir := t.TempDir()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 4096)
	t.Cleanup(func() { bus.Close(); db.Close() })
	seed(t, db)
	return &rt.Runtime{PM: proc.New(), Bus: bus, DB: db, Adapters: map[string]agents.Adapter{"claude": ad}}, db, bus, dir
}

func TestRealClaudeTurnWithToolUseAndUsage(t *testing.T) {
	ad, _ := realClaude(t)
	r, db, _, dir := realRuntime(t, ad)
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	m, err := r.Spawn(ctx, rt.Config{AgentID: "a1", ProjectID: "p1", Adapter: "claude", Provider: "anthropic", Tier: "premium",
		Spec: agents.RunSpec{AgentID: "a1", Cwd: dir, Model: "haiku", AllowedTools: []string{"Read"}, ExtraArgs: []string{"--no-session-persistence"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	var calls, results int
	var sawDelta bool
	res, err := m.RunTurn(ctx, "Use the Read tool to read hello.txt in the current directory, then reply with its exact content only.", nil, func(c workflow.Chunk) {
		switch c.Kind {
		case workflow.ChunkToolCall:
			calls++
		case workflow.ChunkToolResult:
			results++
		case workflow.ChunkText:
			sawDelta = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "hello from file") || res.StopReason != "end_turn" || res.SessionID == "" {
		t.Fatalf("result %+v", res)
	}
	if calls != 1 || results != 1 {
		t.Fatalf("tool visibility: calls=%d results=%d", calls, results)
	}
	if !sawDelta {
		t.Log("note: no streaming text deltas were observed (include-partial-messages)")
	}
	var n int
	var in, out int64
	var cost float64
	var source, tier string
	if err := db.Reader().QueryRow(`SELECT COUNT(*),SUM(input_tokens+cached_tokens+cache_write_tokens),SUM(output_tokens),SUM(cost_usd),MAX(source),MAX(tier) FROM model_usage`).Scan(&n, &in, &out, &cost, &source, &tier); err != nil {
		t.Fatal(err)
	}
	if n < 1 || in == 0 || out == 0 || cost <= 0 || source != "reported" || tier != "premium" {
		t.Fatalf("usage rows n=%d in=%d out=%d cost=%f source=%s tier=%s", n, in, out, cost, source, tier)
	}
	t.Logf("recorded: %d usage row(s), %d input-side tokens, %d output tokens, $%.4f (reported)", n, in, out, cost)
}

func TestRealClaudeInterruptThenContinueSameSession(t *testing.T) {
	ad, _ := realClaude(t)
	r, _, _, dir := realRuntime(t, ad)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	m, err := r.Spawn(ctx, rt.Config{AgentID: "a1", ProjectID: "p1", Adapter: "claude", Provider: "anthropic", Tier: "premium",
		Spec: agents.RunSpec{AgentID: "a1", Cwd: dir, Model: "haiku", ExtraArgs: []string{"--tools", "", "--no-session-persistence"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	ctl := rt.NewControllerForTest(m)
	type out struct {
		res rt.TurnResult
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := m.RunTurn(ctx, "Count from 1 to 400, one number per line, no other text.", ctl, nil)
		done <- out{res, err}
	}()
	time.Sleep(2500 * time.Millisecond)
	if err := ctl.CancelTurn(); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	ctl.Send(ctx, "Reply with only the single word: alive")
	select {
	case o := <-done:
		if o.err != nil || !strings.Contains(strings.ToLower(o.res.Text), "alive") {
			t.Fatalf("after interrupt the same session must answer the nudge: %+v err=%v", o.res, o.err)
		}
	case <-time.After(120 * time.Second):
		t.Fatal("timed out")
	}
}
