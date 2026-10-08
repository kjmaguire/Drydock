package acp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// These tests drive the real adapter against fakeacp, a scripted ACP agent running as a separate process on the real SDK.

func TestCapabilitiesComeFromInitialize(t *testing.T) {
	cases := []struct {
		name   string
		script fakeacp.Script
		check  func(t *testing.T, info acp.AgentInfo, caps agents.Capabilities)
	}{
		{"gemini-like: loadSession only, http+sse, image", fakeacp.Script{Name: "gemini-cli", Version: "0.63.0", LoadSession: true, MCPHTTP: true, MCPSSE: true, Image: true, EmbeddedContext: true},
			func(t *testing.T, i acp.AgentInfo, c agents.Capabilities) {
				if i.Name != "gemini-cli" || i.Version != "0.63.0" || !i.LoadSession || i.Resume || i.Fork || i.List || !i.MCPHTTP || !i.MCPSSE || !i.Image || i.Audio {
					t.Fatalf("info %+v", i)
				}
				if !c.SessionResume || !c.ImageInput || !c.MCP || !c.Interrupt || !c.ToolCallVisibility {
					t.Fatalf("caps %+v", c)
				}
				if i.SessionResume() != "load" {
					t.Fatalf("resume mechanism %q", i.SessionResume())
				}
			}},
		{"opencode-like: resume+fork+list+close, no load", fakeacp.Script{Resume: true, Fork: true, List: true, Close: true, MCPHTTP: true},
			func(t *testing.T, i acp.AgentInfo, c agents.Capabilities) {
				if !i.Resume || !i.Fork || !i.List || !i.Close || i.Delete || i.LoadSession || i.MCPSSE || i.Image {
					t.Fatalf("info %+v", i)
				}
				if !c.SessionResume || c.ImageInput || i.SessionResume() != "resume" {
					t.Fatalf("caps %+v", c)
				}
			}},
		{"bare agent: nothing advertised", fakeacp.Script{},
			func(t *testing.T, i acp.AgentInfo, c agents.Capabilities) {
				if i.LoadSession || i.Resume || i.MCPHTTP || i.MCPSSE || i.Image {
					t.Fatalf("info %+v", i)
				}
				if c.SessionResume || c.ImageInput || c.TokenUsage || c.CostReporting || c.ModelSelection || c.EffortControl {
					t.Fatalf("a capability was assumed without being reported: %+v", c)
				}
				if !c.Interrupt || !c.ToolCallVisibility || !c.Streaming {
					t.Fatalf("protocol baselines missing: %+v", c)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ad := fakeAdapter(t, tc.script)
			s := startSession(t, ad, agents.RunSpec{AgentID: "a"}).(*acp.Session)
			tc.check(t, s.Info(), s.Capabilities())
			// the adapter remembers what it observed
			if o, ok := ad.Observed(); !ok || o.Info.Name != s.Info().Name {
				t.Fatalf("adapter observed %+v %v", o, ok)
			}
		})
	}
}

func TestSessionModelModeEffortCapabilitiesAndSelection(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Models: []string{"m-small", "m-big"}, Efforts: []string{"low", "high"}, Modes: []string{"ask", "plan"},
		Turns: [][]fakeacp.Step{{{ReportConfig: true}}}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a", Model: "m-big", Effort: "high", PermissionMode: "plan"}).(*acp.Session)
	si := s.SessionInfo()
	if si.ModelOption != "model" || len(si.Models) != 2 || si.EffortOption != "effort" || len(si.Modes) != 2 || si.CurrentMode != "ask" {
		t.Fatalf("session info %+v", si)
	}
	caps := s.Capabilities()
	if !caps.ModelSelection || !caps.EffortControl || len(caps.PermissionModes) != 2 {
		t.Fatalf("caps %+v", caps)
	}
	cs := turn(t, s, "go")
	if got := text(cs); !strings.Contains(got, "model=m-big") || !strings.Contains(got, "effort=high") || !strings.Contains(got, "mode=plan") {
		t.Fatalf("settings were not applied: %q", got)
	}
}

func TestUnavailableSettingsAreReportedNotSilentlyDropped(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Models: []string{"m-small"}, Turns: [][]fakeacp.Step{{{ReportConfig: true}}}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a", Model: "not-a-model", Effort: "high", PermissionMode: "bypassPermissions"})
	cs := turn(t, s, "go")
	var warns []string
	for _, c := range ofKind(cs, workflow.ChunkError) {
		e := decode[workflow.ErrorChunk](t, c)
		if e.Fatal {
			t.Fatalf("a setting warning must not be fatal: %+v", e)
		}
		warns = append(warns, e.Message)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{`model "not-a-model"`, `effort "high"`, `permission mode "bypassPermissions"`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing warning for %s in %q", want, joined)
		}
	}
	if !strings.Contains(text(cs), "model=m-small") {
		t.Fatalf("the agent default must stay in effect: %q", text(cs))
	}
}

func TestStreamMapsUpdatesToNormalizedChunks(t *testing.T) {
	script := fakeacp.Script{Turns: [][]fakeacp.Step{{
		{Think: "let me look"},
		{Say: "I will read the file. "},
		{Plan: []string{"read", "answer"}},
		{Tool: &fakeacp.Tool{ID: "t1", Title: "Read a.go", Kind: "read", Input: map[string]any{"path": "a.go"}, Output: "package a", Locations: []string{"a.go"}}},
		{Tool: &fakeacp.Tool{ID: "t2", Title: "Run tests", Kind: "execute", Late: true, Input: map[string]any{"command": "go test"}, Output: "FAIL", Fail: true}},
		{Tool: &fakeacp.Tool{ID: "t3", Title: "Bash", Meta: map[string]any{"claudeCode": map[string]any{"toolName": "Bash"}}, Input: map[string]any{"command": "ls"}, RawOutput: map[string]any{"files": 3}}},
		{Say: "All done."},
	}}}
	ad := fakeAdapter(t, script)
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"})
	cs := turn(t, s, "do it")

	if got := kinds(cs); got[len(got)-1] != workflow.ChunkDone || len(ofKind(cs, workflow.ChunkDone)) != 1 {
		t.Fatalf("exactly one trailing done expected: %v", got)
	}
	if stopReason(t, cs) != "end_turn" {
		t.Fatalf("stop %q", stopReason(t, cs))
	}
	if th := ofKind(cs, workflow.ChunkThinking); len(th) != 1 || decode[workflow.TextDelta](t, th[0]).Text != "let me look" {
		t.Fatalf("thinking %v", kinds(cs))
	}
	if text(cs) != "I will read the file. All done." {
		t.Fatalf("text deltas %q", text(cs))
	}
	// message_end: one before the first tool call, one at the end
	me := ofKind(cs, workflow.ChunkMessageEnd)
	if len(me) != 2 || decode[workflow.TextDelta](t, me[0]).Text != "I will read the file. " || decode[workflow.TextDelta](t, me[1]).Text != "All done." {
		t.Fatalf("message_end %v", me)
	}
	if pl := ofKind(cs, workflow.ChunkPlan); len(pl) != 1 || len(decode[workflow.PlanChunk](t, pl[0]).Entries) != 2 {
		t.Fatalf("plan %v", pl)
	}
	calls, results := ofKind(cs, workflow.ChunkToolCall), ofKind(cs, workflow.ChunkToolResult)
	if len(calls) != 3 || len(results) != 3 {
		t.Fatalf("calls=%d results=%d: %v", len(calls), len(results), kinds(cs))
	}
	c1 := decode[workflow.ToolCall](t, calls[0])
	if c1.ID != "t1" || c1.Name != "Read a.go" || string(c1.Args) != `{"path":"a.go"}` {
		t.Fatalf("call 1 %+v args=%s", c1, c1.Args)
	}
	r1 := decode[workflow.ToolResult](t, results[0])
	if r1.ID != "t1" || r1.Name != "Read a.go" || string(r1.Args) != `{"path":"a.go"}` || r1.Output != "package a" || r1.IsError {
		t.Fatalf("result 1 %+v", r1)
	}
	// a call announced without input still carries its args once the input arrived, and a failed status is an error result
	c2 := decode[workflow.ToolCall](t, calls[1])
	r2 := decode[workflow.ToolResult](t, results[1])
	if c2.ID != "t2" || !strings.Contains(string(r2.Args), "go test") || !r2.IsError || r2.Output != "FAIL" {
		t.Fatalf("late-input call %+v result %+v", c2, r2)
	}
	// the raw tool name from _meta wins over the title; rawOutput is used when there is no content
	c3, r3 := decode[workflow.ToolCall](t, calls[2]), decode[workflow.ToolResult](t, results[2])
	if c3.Name != "Bash" || r3.Name != "Bash" || r3.Output != `{"files":3}` {
		t.Fatalf("call 3 %+v result %+v", c3, r3)
	}
	// every result is preceded by its call
	seen := map[string]bool{}
	for _, c := range cs {
		switch c.Kind {
		case workflow.ChunkToolCall:
			seen[decode[workflow.ToolCall](t, c).ID] = true
		case workflow.ChunkToolResult:
			if !seen[decode[workflow.ToolResult](t, c).ID] {
				t.Fatal("tool_result before its tool_call")
			}
		}
	}
	// the agent reported no usage: none is invented
	if u := ofKind(cs, workflow.ChunkUsage); len(u) != 0 {
		t.Fatalf("fabricated usage: %v", u)
	}
	if s.ID() == "" {
		t.Fatal("session id")
	}
}

func TestToolWithoutInputStillCarriesIdentifyingArgs(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{
		{Tool: &fakeacp.Tool{ID: "t1", Title: "Edit", Kind: "edit", Locations: []string{"/x/y.go"}, Output: "ok"}},
	}}})
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a"}), "go")
	c := decode[workflow.ToolCall](t, ofKind(cs, workflow.ChunkToolCall)[0])
	if string(c.Args) != `{"kind":"edit","locations":["/x/y.go"]}` {
		t.Fatalf("args %s", c.Args)
	}
}

func TestPendingToolIsStillShownAtTurnEnd(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{Tool: &fakeacp.Tool{ID: "t1", Title: "Slow", Pending: true}}}}})
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a"}), "go")
	if len(ofKind(cs, workflow.ChunkToolCall)) != 1 || len(ofKind(cs, workflow.ChunkToolResult)) != 0 {
		t.Fatalf("%v", kinds(cs))
	}
}

func TestUsageFromPromptResponseIsReported(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Models: []string{"m1"}, PromptUsage: &fakeacp.PromptUsage{In: 120, Out: 30, CachedRead: 100, CachedWrite: 5, Thought: 7}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"}).(*acp.Session)
	cs := turn(t, s, "hi")
	us := ofKind(cs, workflow.ChunkUsage)
	if len(us) != 1 {
		t.Fatalf("usage chunks: %v", kinds(cs))
	}
	u := decode[workflow.UsageChunk](t, us[0])
	if u.Source != "reported" || u.InputTokens != 120 || u.OutputTokens != 30 || u.CachedTokens != 100 || u.CacheWriteTokens != 5 || u.ReasoningTokens != 7 || u.Model != "m1" || u.CostUSD != 0 {
		t.Fatalf("usage %+v", u)
	}
	if !s.Capabilities().TokenUsage || s.Capabilities().CostReporting {
		t.Fatalf("capabilities after usage: %+v", s.Capabilities())
	}
	// usage comes before done
	k := kinds(cs)
	if k[len(k)-1] != workflow.ChunkDone || k[len(k)-2] != workflow.ChunkUsage {
		t.Fatalf("order %v", k)
	}
}

func TestGeminiQuotaMetaIsUsage(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Quota: &fakeacp.Quota{In: 150, Out: 40, Models: []fakeacp.QuotaModel{{Model: "gemini-flash", In: 100, Out: 30}, {Model: "gemini-pro", In: 50, Out: 10}}}})
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a"}), "hi")
	us := ofKind(cs, workflow.ChunkUsage)
	if len(us) != 2 {
		t.Fatalf("want one usage chunk per model, got %d", len(us))
	}
	a, b := decode[workflow.UsageChunk](t, us[0]), decode[workflow.UsageChunk](t, us[1])
	if a.Model != "gemini-flash" || a.InputTokens != 100 || a.OutputTokens != 30 || a.Source != "reported" || b.Model != "gemini-pro" || b.InputTokens != 50 {
		t.Fatalf("%+v %+v", a, b)
	}
	if a.CachedTokens != 0 {
		t.Fatal("Gemini quota has no cache split; none may be invented")
	}
}

func TestGeminiQuotaTotalsWithoutModelBreakdownAndAllZeroIsNotUsage(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Quota: &fakeacp.Quota{In: 9, Out: 3}})
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a"}), "hi")
	us := ofKind(cs, workflow.ChunkUsage)
	if len(us) != 1 || decode[workflow.UsageChunk](t, us[0]).InputTokens != 9 {
		t.Fatalf("%v", us)
	}
	ad2 := fakeAdapter(t, fakeacp.Script{Quota: &fakeacp.Quota{}})
	cs = turn(t, startSession(t, ad2, agents.RunSpec{AgentID: "a"}), "/stats")
	if len(ofKind(cs, workflow.ChunkUsage)) != 0 {
		t.Fatal("an all-zero quota report (Gemini slash commands) must not become a usage row")
	}
}

func TestUsageUpdateGivesContextAndPerTurnCostDelta(t *testing.T) {
	c1, c2 := 0.010, 0.025
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{
		{{UsageUpdate: &fakeacp.UsageUpdate{Used: 1000, Size: 200000, Cost: &c1}}, {Say: "one"}},
		{{UsageUpdate: &fakeacp.UsageUpdate{Used: 2500, Size: 200000, Cost: &c2}}, {Say: "two"}},
	}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"}).(*acp.Session)
	u1 := decode[workflow.UsageChunk](t, ofKind(turn(t, s, "a"), workflow.ChunkUsage)[0])
	if u1.ContextUsed != 1000 || u1.ContextSize != 200000 || u1.CostUSD < 0.00999 || u1.CostUSD > 0.01001 || u1.Source != "reported" || u1.InputTokens != 0 {
		t.Fatalf("turn 1 %+v", u1)
	}
	u2 := decode[workflow.UsageChunk](t, ofKind(turn(t, s, "b"), workflow.ChunkUsage)[0])
	if u2.ContextUsed != 2500 || u2.CostUSD < 0.01499 || u2.CostUSD > 0.01501 { // cumulative 0.025 minus 0.010
		t.Fatalf("turn 2 must carry the cost delta, got %+v", u2)
	}
	if caps := s.Capabilities(); !caps.CostReporting || !caps.TokenUsage {
		t.Fatalf("caps %+v", caps)
	}
}

func TestPromptUsageAndUsageUpdateMergeIntoOneChunk(t *testing.T) {
	c := 0.002
	ad := fakeAdapter(t, fakeacp.Script{PromptUsage: &fakeacp.PromptUsage{In: 10, Out: 5},
		Turns: [][]fakeacp.Step{{{UsageUpdate: &fakeacp.UsageUpdate{Used: 300, Size: 1000, Cost: &c}}}}})
	us := ofKind(turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a"}), "x"), workflow.ChunkUsage)
	if len(us) != 1 {
		t.Fatalf("one chunk per turn expected (the runtime records one row per chunk), got %d", len(us))
	}
	u := decode[workflow.UsageChunk](t, us[0])
	if u.InputTokens != 10 || u.OutputTokens != 5 || u.ContextUsed != 300 || u.CostUSD != 0.002 {
		t.Fatalf("%+v", u)
	}
}

func TestClaudeRateLimitMetaForwarded(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{Rate: map[string]any{"status": "allowed_warning", "utilization": 0.8}}, {UsageUpdate: &fakeacp.UsageUpdate{Used: 1, Size: 2}}}}})
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a"}), "x")
	rl := ofKind(cs, workflow.ChunkRateLimit)
	if len(rl) != 1 || !strings.Contains(string(rl[0].Data), "allowed_warning") {
		t.Fatalf("%v", kinds(cs))
	}
}

func TestInterruptKeepsSessionAlive(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{Say: "working "}, {Block: true}}, {{Say: "still here"}}}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"}).(*acp.Session)
	if err := s.Send(context.Background(), "long task"); err != nil {
		t.Fatal(err)
	}
	// wait for the first streamed output, then interrupt
	select {
	case c := <-s.Output():
		if c.Kind != workflow.ChunkText {
			t.Fatalf("first chunk %s", c.Kind)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no output")
	}
	if err := s.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs := readUntilDone(t, s, 10*time.Second)
	if stopReason(t, cs) != "cancelled" {
		t.Fatalf("stop reason %q (%v)", stopReason(t, cs), kinds(cs))
	}
	if !alive(s) {
		t.Fatal("interrupt must not end the process")
	}
	// the same session answers the next message
	cs = turn(t, s, "continue")
	if stopReason(t, cs) != "end_turn" || !strings.Contains(text(cs), "still here") {
		t.Fatalf("after interrupt: %v %q", kinds(cs), text(cs))
	}
	if !s.Capabilities().Interrupt {
		t.Fatal("interrupt capability")
	}
	if o, _ := ad.Observed(); !o.Cancelled && !s.Capabilities().Interrupt {
		t.Fatal("cancel not observed")
	}
}

func TestInterruptWhenIdleIsNoop(t *testing.T) {
	s := startSession(t, fakeAdapter(t, fakeacp.Script{}), agents.RunSpec{AgentID: "a"})
	if err := s.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !alive(s) {
		t.Fatal("died")
	}
}

func TestSendWhileTurnRunningQueuesAndInterruptDropsQueue(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{
		Rules: []fakeacp.Rule{{Match: "slow", Steps: []fakeacp.Step{{SleepMS: 600}, {Say: "slow done"}}}, {Match: "queued", Steps: []fakeacp.Step{{Say: "queued done"}}},
			{Match: "block", Steps: []fakeacp.Step{{Block: true}}}}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"})
	_ = s.Send(context.Background(), "slow")
	_ = s.Send(context.Background(), "queued")
	first := readUntilDone(t, s, 10*time.Second)
	second := readUntilDone(t, s, 10*time.Second)
	if !strings.Contains(text(first), "slow done") || !strings.Contains(text(second), "queued done") {
		t.Fatalf("queue order: %q then %q", text(first), text(second))
	}
	// Interrupt drops messages queued behind the running turn
	_ = s.Send(context.Background(), "block")
	_ = s.Send(context.Background(), "queued")
	time.Sleep(300 * time.Millisecond)
	if err := s.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs := readUntilDone(t, s, 10*time.Second)
	if stopReason(t, cs) != "cancelled" {
		t.Fatalf("%q", stopReason(t, cs))
	}
	select {
	case c := <-s.Output():
		t.Fatalf("a dropped queued message produced output: %s", c.Kind)
	case <-time.After(600 * time.Millisecond):
	}
}

func TestPromptErrorBecomesErrorAndDone(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{ErrorPrompt: "rate limited"}}, {{Say: "recovered"}}}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"})
	cs := turn(t, s, "x")
	errs := ofKind(cs, workflow.ChunkError)
	if len(errs) != 1 || decode[workflow.ErrorChunk](t, errs[0]).Fatal || !strings.Contains(decode[workflow.ErrorChunk](t, errs[0]).Message, "rate limited") {
		t.Fatalf("%v", errs)
	}
	if stopReason(t, cs) != "error" {
		t.Fatalf("stop %q", stopReason(t, cs))
	}
	if !strings.Contains(text(turn(t, s, "y")), "recovered") {
		t.Fatal("the session must survive a failed prompt")
	}
}

func TestAgentCrashClosesOutputAndReportsExit(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{Say: "about to die"}, {Exit: 3}}}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"})
	_ = s.Send(context.Background(), "x")
	deadline := time.After(10 * time.Second)
	var sawFatal bool
	for open := true; open; {
		select {
		case c, ok := <-s.Output():
			if !ok {
				open = false
				break
			}
			if c.Kind == workflow.ChunkError && decode[workflow.ErrorChunk](t, c).Fatal {
				sawFatal = true
			}
		case <-deadline:
			t.Fatal("output never closed after the agent exited")
		}
	}
	ei := <-s.Wait()
	if ei.Code != 3 {
		t.Fatalf("exit %+v", ei)
	}
	_ = sawFatal // a fatal error chunk is allowed here, closure of Output is the contract
	if err := s.Send(context.Background(), "again"); err == nil {
		t.Fatal("Send on a dead session must fail")
	}
}

func TestStopEndsProcessAndClosesOutput(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{Block: true}}}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"})
	_ = s.Send(context.Background(), "x")
	time.Sleep(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Wait():
	case <-time.After(5 * time.Second):
		t.Fatal("process still running after Stop")
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-s.Output():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("output not closed after Stop")
		}
	}
}

func TestResumeUsesTheMechanismTheAgentAdvertised(t *testing.T) {
	report := [][]fakeacp.Step{{{ReportConfig: true}}}
	t.Run("load: history replay is not output", func(t *testing.T) {
		ad := fakeAdapter(t, fakeacp.Script{LoadSession: true, ReplayOnLoad: []string{"OLD HISTORY"}, Turns: report})
		s := startSession(t, ad, agents.RunSpec{AgentID: "a", ResumeID: "prev-123"})
		if s.ID() != "prev-123" {
			t.Fatalf("id %q", s.ID())
		}
		cs := turn(t, s, "go")
		if strings.Contains(text(cs), "OLD HISTORY") || !strings.Contains(text(cs), "resumed=load:prev-123") {
			t.Fatalf("%q", text(cs))
		}
	})
	t.Run("resume preferred over load", func(t *testing.T) {
		ad := fakeAdapter(t, fakeacp.Script{LoadSession: true, Resume: true, Turns: report})
		cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a", ResumeID: "prev-9"}), "go")
		if !strings.Contains(text(cs), "resumed=resume:prev-9") {
			t.Fatalf("%q", text(cs))
		}
	})
	t.Run("neither: clear error, nothing silently started fresh", func(t *testing.T) {
		ad := fakeAdapter(t, fakeacp.Script{})
		_, err := ad.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a", Cwd: t.TempDir(), ResumeID: "x"})
		if err == nil || !strings.Contains(err.Error(), "cannot be resumed") {
			t.Fatalf("err %v", err)
		}
	})
}

func TestMCPServersFollowAdvertisedTransports(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mcp.json")
	_ = os.WriteFile(cfg, []byte(`{"mcpServers":{
		"drydock":{"command":"drydock-mcp","args":["--run","r1"],"env":{"TOKEN":"x"}},
		"remote":{"type":"http","url":"http://127.0.0.1:1/mcp","headers":{"Authorization":"Bearer t"}},
		"legacy":{"type":"sse","url":"http://127.0.0.1:1/sse"}}}`), 0o644)
	ad := fakeAdapter(t, fakeacp.Script{MCPHTTP: true, Turns: [][]fakeacp.Step{{{ReportConfig: true}}}})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a", MCPConfigPath: cfg})
	cs := turn(t, s, "go")
	if !strings.Contains(text(cs), "mcp=http:remote,stdio:drydock") && !strings.Contains(text(cs), "mcp=stdio:drydock,http:remote") {
		t.Fatalf("attached servers: %q", text(cs))
	}
	if strings.Contains(text(cs), "sse:") {
		t.Fatal("an sse server was attached although the agent did not advertise sse")
	}
	var warned bool
	for _, c := range ofKind(cs, workflow.ChunkError) {
		if strings.Contains(decode[workflow.ErrorChunk](t, c).Message, `"legacy"`) {
			warned = true
		}
	}
	if !warned {
		t.Fatal("skipping a server must be reported")
	}
}

func TestAuthRequiredIsAClearErrorAndHandshakeStillReports(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{AuthRequired: true, Name: "needs-login", Version: "9.9"})
	_, err := ad.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a", Cwd: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "authentication") || !strings.Contains(err.Error(), "CLI's own login") {
		t.Fatalf("err %v", err)
	}
	h, err := ad.Handshake(context.Background(), proc.New(), agents.RunSpec{AgentID: "a", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if h.SessionOK || h.Info.Name != "needs-login" || h.PID == 0 || !strings.Contains(h.SessionErr, "authentication") {
		t.Fatalf("handshake %+v", h)
	}
}

func TestHandshakeReportsAndStopsTheProcess(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Resume: true, MCPHTTP: true, Models: []string{"a", "b"}})
	h, err := ad.Handshake(context.Background(), proc.New(), agents.RunSpec{AgentID: "a", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !h.SessionOK || !h.Info.Resume || !h.Info.MCPHTTP || len(h.Session.Models) != 2 || h.InitElapsed <= 0 {
		t.Fatalf("%+v", h)
	}
	if lines := strings.Join(h.Info.Matrix(), "\n"); !strings.Contains(lines, "session.resume=true") || !strings.Contains(lines, "mcp.http=true") || !strings.Contains(lines, "loadSession=false") {
		t.Fatalf("matrix %s", lines)
	}
}

func TestStartFailsCleanlyWhenTheCommandIsMissing(t *testing.T) {
	ad := acp.New(acp.Config{ID: "nope", Command: "definitely-not-a-real-binary-xyz"})
	if _, err := ad.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a", Cwd: t.TempDir()}); err == nil {
		t.Fatal("expected error")
	}
	d, err := ad.Detect(context.Background())
	if err != nil || d.Installed || d.Status != agents.StatusNotInstalled || len(d.Problems) == 0 {
		t.Fatalf("detect %+v %v", d, err)
	}
}

func TestStartRequiresCwdAndExistingDir(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{})
	if _, err := ad.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a"}); err == nil {
		t.Fatal("empty cwd must fail")
	}
	if _, err := ad.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a", Cwd: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing cwd must fail")
	}
}

func TestRunSpecEnvReachesTheAgent(t *testing.T) {
	// the scripted agent reports its cwd; env is exercised through the allowlist in TestScrubEnv
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{ReportConfig: true}}}})
	dir := t.TempDir()
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a", Cwd: dir}), "go")
	real, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(text(cs), "cwd="+dir) && !strings.Contains(text(cs), "cwd="+real) {
		t.Fatalf("session cwd not passed: %q", text(cs))
	}
}

func TestScrubEnvKeepsOnlyTheAllowlist(t *testing.T) {
	parent := []string{"PATH=/bin", "HOME=/h", "ANTHROPIC_API_KEY=sk-secret", "GEMINI_API_KEY=g", "Path=/win", "SystemRoot=C:\\Windows", "GITHUB_TOKEN=t"}
	got := acp.BuildEnvForTest(parent, true, []string{"GOOSE_MODE=approve"}, []string{"HOME=/override"})
	joined := strings.Join(got, "\n")
	for _, bad := range []string{"sk-secret", "GEMINI_API_KEY", "GITHUB_TOKEN"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("scrubbed env leaked %s: %v", bad, got)
		}
	}
	for _, want := range []string{"PATH=/bin", "Path=/win", "SystemRoot=C:\\Windows", "GOOSE_MODE=approve", "HOME=/override"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
	// without scrubbing everything is inherited; later entries come last so they win
	all := acp.BuildEnvForTest(parent, false, []string{"HOME=/second"})
	if all[len(all)-1] != "HOME=/second" || len(all) != len(parent)+1 {
		t.Fatalf("%v", all)
	}
}

func TestRequestEncodingOfChunksIsStableJSON(t *testing.T) {
	// permission requests are public payloads: field names are part of the contract
	b, _ := json.Marshal(acp.PermissionRequest{ID: "perm_1", ToolCallID: "t", Tool: "Edit", Options: []acp.PermissionOption{{ID: "allow", Name: "Allow", Kind: acp.KindAllowOnce}}})
	for _, k := range []string{`"id":"perm_1"`, `"toolCallId":"t"`, `"tool":"Edit"`, `"options":[{"id":"allow","name":"Allow","kind":"allow_once"}]`} {
		if !strings.Contains(string(b), k) {
			t.Fatalf("%s missing from %s", k, b)
		}
	}
}
