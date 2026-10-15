package acp_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// What OpenCode's ACP agent does that these tests stand in for (read from its source, packages/cli/src/acp): usage_update.cost
// is the stored session's cumulative cost, so it includes the processes that served the session before; a compaction of the
// session's context is reported as a session_info_update with _meta["opencode/compaction"]. The scripted agent fakeacp
// reproduces both; the real CLI is checked by the contracts OPENCODE-USAGE-01 and OPENCODE-COMPACT-01.

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func totalsCost(t *testing.T, raw json.RawMessage) float64 {
	t.Helper()
	var v struct {
		CostUSD *float64 `json:"costUsd"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.CostUSD == nil {
		t.Fatalf("usage totals %s: %v", raw, err)
	}
	return *v.CostUSD
}

// A session resumed (or loaded) in a new process reports the session's cumulative cost, earlier processes included. Started
// from the session's last totals (RunSpec.UsageBaseline), the adapter records only the new turn; started from zero it counted
// every earlier turn again.
func TestAResumedSessionRecordsOnlyItsOwnCost(t *testing.T) {
	for _, mode := range []struct {
		name string
		sc   fakeacp.Script
	}{{"session/resume", fakeacp.Script{Resume: true}}, {"session/load", fakeacp.Script{LoadSession: true}}} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv("DRYDOCK_FAKE_ACP_STATE_DIR", t.TempDir())
			turnCost := 0.02
			sc := mode.sc
			sc.Default = []fakeacp.Step{{UsageUpdate: &fakeacp.UsageUpdate{Used: 100, Size: 1000, TurnCost: &turnCost}}, {Say: "ok"}}

			first := startSession(t, fakeAdapter(t, sc), agents.RunSpec{AgentID: "a"})
			u1 := decode[workflow.UsageChunk](t, ofKind(turn(t, first, "one"), workflow.ChunkUsage)[0])
			u2 := decode[workflow.UsageChunk](t, ofKind(turn(t, first, "two"), workflow.ChunkUsage)[0])
			if !near(u1.CostUSD, 0.02) || !near(u2.CostUSD, 0.02) || !near(totalsCost(t, u2.Totals), 0.04) {
				t.Fatalf("first process: %+v %+v", u1, u2)
			}
			sid := first.ID()

			// a new process continues the session, as the next chat turn or work turn does
			second := startSession(t, fakeAdapter(t, sc), agents.RunSpec{AgentID: "a", ResumeID: sid, UsageBaseline: u2.Totals})
			u3 := decode[workflow.UsageChunk](t, ofKind(turn(t, second, "three"), workflow.ChunkUsage)[0])
			if !near(u3.CostUSD, 0.02) {
				t.Fatalf("the resumed turn must record its own cost 0.02, not the session's 0.06 so far: %+v", u3)
			}
			if !near(totalsCost(t, u3.Totals), 0.06) {
				t.Fatalf("the totals passed on to the next process: %s", u3.Totals)
			}
			u4 := decode[workflow.UsageChunk](t, ofKind(turn(t, second, "four"), workflow.ChunkUsage)[0])
			if !near(u4.CostUSD, 0.02) {
				t.Fatalf("a later turn of the resumed process: %+v", u4)
			}
		})
	}
}

// A baseline that does not parse, or none, leaves the accounting as it was (from zero); a new session has no baseline.
func TestAnUnreadableUsageBaselineIsIgnored(t *testing.T) {
	c := 0.05
	sc := fakeacp.Script{Resume: true, Default: []fakeacp.Step{{UsageUpdate: &fakeacp.UsageUpdate{Used: 1, Size: 10, Cost: &c}}}}
	for _, b := range []json.RawMessage{nil, json.RawMessage(`not json`), json.RawMessage(`{"costUsd":-1}`)} {
		s := startSession(t, fakeAdapter(t, sc), agents.RunSpec{AgentID: "a", ResumeID: "prev", UsageBaseline: b})
		if u := decode[workflow.UsageChunk](t, ofKind(turn(t, s, "x"), workflow.ChunkUsage)[0]); !near(u.CostUSD, 0.05) {
			t.Fatalf("baseline %q: %+v", b, u)
		}
	}
	// a baseline is only for a session being continued
	s := startSession(t, fakeAdapter(t, sc), agents.RunSpec{AgentID: "a", UsageBaseline: json.RawMessage(`{"costUsd":0.04}`)})
	if u := decode[workflow.UsageChunk](t, ofKind(turn(t, s, "x"), workflow.ChunkUsage)[0]); !near(u.CostUSD, 0.05) {
		t.Fatalf("a new session ignores a baseline: %+v", u)
	}
}

// A completed compaction of the session's own context is counted into the turn's usage chunk, where the session policy (P39)
// reads it. A compaction that started or failed, and a sub-agent's compaction, are not counted; the next turn starts at zero.
func TestACompactionIsCountedInTheTurnsUsage(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{
		{
			{Compaction: &fakeacp.Compaction{Status: "started"}},
			{Compaction: &fakeacp.Compaction{Status: "completed"}},
			{Compaction: &fakeacp.Compaction{Status: "completed", Child: "child-1"}},
			{Compaction: &fakeacp.Compaction{Status: "failed"}},
			{UsageUpdate: &fakeacp.UsageUpdate{Used: 20_000, Size: 200_000}},
			{Say: "compacted"},
		},
		{{UsageUpdate: &fakeacp.UsageUpdate{Used: 30_000, Size: 200_000}}, {Say: "next"}},
	}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"})
	cs := turn(t, s, "one")
	us := ofKind(cs, workflow.ChunkUsage)
	if len(us) != 1 {
		t.Fatalf("one usage chunk per turn: %v", kinds(cs))
	}
	if u := decode[workflow.UsageChunk](t, us[0]); u.Compactions != 1 || u.ContextUsed != 20_000 || u.ContextSize != 200_000 {
		t.Fatalf("turn 1 %+v", u)
	}
	if len(ofKind(cs, workflow.ChunkError)) != 0 || text(cs) != "compacted" {
		t.Fatalf("the marker is not output: %v %q", kinds(cs), text(cs))
	}
	if u := decode[workflow.UsageChunk](t, ofKind(turn(t, s, "two"), workflow.ChunkUsage)[0]); u.Compactions != 0 {
		t.Fatalf("turn 2 %+v", u)
	}
}
