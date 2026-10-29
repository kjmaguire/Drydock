package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/router"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Slice 2 through the same method registry the UI calls: real SQLite, real workflow engine and pipeline, real git worktrees,
// real processes. The agents are the scripted real-process stand-in the other core tests use (it speaks Claude's stream-json
// dialect and reports a cost of $0.0002 per turn; it is a labeled stand-in, not a real model). HTTP peers are protocol
// fixtures where labeled.

type econTask struct {
	repo           string
	project        core.Project
	worker, review core.Agent
	task           tasks.Task
}

func (a *app) econSetup() econTask {
	a.t.Helper()
	var s econTask
	s.repo = newRepo(a.t)
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": s.repo}, &s.project)
	a.call("agent.create", core.AgentSpec{ProjectID: s.project.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &s.worker)
	a.call("agent.create", core.AgentSpec{ProjectID: s.project.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &s.review)
	a.call("task.create", tasks.CreateReq{ProjectID: s.project.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b.", Acceptance: []string{"Given 5 and 3 When Subtract is called Then 2"}}, &s.task)
	a.call("task.assign", map[string]string{"id": s.task.ID, "assignee": s.worker.ID, "reviewer": s.review.ID}, &s.task)
	return s
}

func (a *app) econPending(taskID string, want int) core.TaskDetail {
	a.t.Helper()
	var d core.TaskDetail
	a.waitFor("a pending request", func() bool {
		a.call("task.get", map[string]string{"id": taskID}, &d)
		// the request is stored a moment before the run's status becomes WAITING_HUMAN; wait for both so callers see one state
		return len(d.Pending) == want && (want == 0 || (d.Run != nil && d.Run.Status == "WAITING_HUMAN"))
	})
	return d
}

func TestEconomicsMethodsAreRegistered(t *testing.T) {
	a := open(t, "")
	m := a.c.Methods()
	for _, name := range []string{"budget.list", "budget.set", "budget.delete", "budget.status", "budget.resume", "evals.report", "evals.export", "route.explain", "route.decisions",
		"route.rules.list", "route.rules.set", "route.rules.delete", "route.preview", "route.previews", "route.cheap.select", "route.cheap.get", "route.settings.get", "route.settings.set", "route.retention.list", "route.retention.clear", "checks.settings.get", "checks.settings.set",
		"telemetry.status", "telemetry.enable", "telemetry.preview", "telemetry.export", "telemetry.otlp.set", "telemetry.otlp.test", "chat.importList", "chat.import",
		"local.model.set", "local.embeddings.start", "local.embeddings.stop", "local.hfToken.set", "local.hfToken.remove",
		// decision P114
		"integration.webhooks.get", "integration.webhooks.set", "integration.mappings.set", "integration.autoApprove.set", "integration.sweep.set", "integration.attachments.download",
		"integration.outbound.retry",
		// decision P115
		"mcp.rolePolicy.get", "mcp.rolePolicy.set", "mcp.policyRule.add", "mcp.policyRule.remove", "task.mcpTools.get", "task.mcpTools.set",
		"connection.custom.list", "connection.custom.template", "connection.custom.add", "connection.custom.remove",
		// decision P118
		"route.level.get", "route.level.set", "budget.workflows"} {
		if m[name] == nil {
			t.Errorf("method %s is not registered", name)
		}
	}
	if core.EconomicsLLM(a.c) == nil {
		t.Fatal("the llm Wrapper was not built")
	}
}

// A task budget crossed by the worker's turn stops the reviewer's turn from starting; only the human resumes it, by raising
// the limit; the loop then completes, the outcome is recorded when the human accepts, and the evals report reads it all from the
// database.
func TestBudgetPausesTheRunAndTheHumanResumesByRaisingTheLimit(t *testing.T) {
	a := open(t, "")
	s := a.econSetup()

	// telemetry is off by default; the human turns it on before the run
	var tst struct {
		Enabled bool `json:"enabled"`
		Spans   int  `json:"spans"`
	}
	a.call("telemetry.status", nil, &tst)
	if tst.Enabled {
		t.Fatal("telemetry must be off by default")
	}
	a.call("telemetry.enable", map[string]any{"enabled": true}, &tst)
	if !tst.Enabled {
		t.Fatal("not enabled")
	}

	if _, err := a.c.Call(context.Background(), "budget.set", mustJSON(map[string]any{"scope": "task", "scopeId": s.task.ID})); err == nil {
		t.Fatal("a budget with no limit must be refused")
	}
	var b struct {
		ID        string `json:"id"`
		Version   int    `json:"version"`
		UpdatedBy string `json:"updatedBy"`
	}
	// $0.00015 is below what one fake-agent turn reports ($0.0002)
	a.call("budget.set", map[string]any{"scope": "task", "scopeId": s.task.ID, "limitUsd": 0.00015, "note": "test ceiling"}, &b)
	if b.ID == "" || b.Version != 1 || b.UpdatedBy != "human:kyle" {
		t.Fatalf("budget %+v", b)
	}

	var runID string
	a.call("task.start", map[string]string{"id": s.task.ID}, &runID)
	d := a.econPending(s.task.ID, 1)
	pend := d.Pending[0]
	if pend.Spec.Kind != "approval" || !strings.HasPrefix(pend.Spec.Title, "Budget limit reached") || !pend.Spec.Retry || pend.NodeID != "review" {
		t.Fatalf("the run must be held before the review turn: %+v", pend)
	}
	if d.Run == nil || d.Run.Status != "WAITING_HUMAN" {
		t.Fatalf("run %+v", d.Run)
	}

	// the reviewer never ran; the worker's turn was recorded with a reported cost
	var usage []core.UsageRow
	a.call("usage.list", map[string]any{"taskId": s.task.ID}, &usage)
	for _, u := range usage {
		if u.AgentID == s.review.ID {
			t.Fatal("the reviewer's turn started although the budget was exceeded")
		}
	}
	// status: exceeded, cost split by source
	var st struct {
		Statuses []struct {
			State        string  `json:"state"`
			Pct          float64 `json:"pct"`
			CostComplete bool    `json:"costComplete"`
			CostLabel    string  `json:"costLabel"`
			Spend        struct {
				ReportedUSD float64 `json:"reportedUsd"`
				Unknown     int     `json:"unknownCostRows"`
			} `json:"spend"`
		} `json:"statuses"`
	}
	a.call("budget.status", map[string]any{"projectId": s.project.ID, "taskId": s.task.ID}, &st)
	if len(st.Statuses) != 1 || st.Statuses[0].State != "exceeded" || st.Statuses[0].Pct < 1 || st.Statuses[0].CostLabel != "reported" || st.Statuses[0].Spend.ReportedUSD <= 0 || st.Statuses[0].Spend.Unknown != 0 {
		t.Fatalf("status %+v", st)
	}
	// events and the task channel
	evs, _ := a.c.Bus.Replay(context.Background(), 0, events.Filter{TaskID: s.task.ID, Types: []string{"budget.*"}}, 50)
	types := map[string]int{}
	for _, e := range evs {
		types[e.Type]++
	}
	if types["budget.exceeded"] != 1 || types["budget.paused"] != 1 {
		t.Fatalf("budget events: %v", types)
	}
	var tr core.TaskDetail
	a.call("task.get", map[string]string{"id": s.task.ID}, &tr)
	var msgs []room.Message
	a.call("room.messages", map[string]any{"roomId": tr.RoomID, "limit": 100}, &msgs)
	var system []string
	for _, m := range msgs {
		if m.Author.Key() == "system" && strings.Contains(m.Body, "Budget") {
			system = append(system, m.Body)
		}
	}
	if len(system) != 2 || !strings.Contains(system[0], "exceeded") || !strings.Contains(system[1], "did not start") {
		t.Fatalf("SYSTEM messages in the task channel: %q", system)
	}
	// Needs you
	var needs []room.NeedsYouItem
	a.call("needsyou.list", map[string]string{"projectId": s.project.ID}, &needs)
	found := false
	for _, n := range needs {
		// a budget item of its own (P118), answered like the approval it is, saying what to do
		found = found || (n.Kind == room.NeedsBudget && n.BudgetState == room.BudgetPaused && n.ApprovalID != "" && strings.HasPrefix(n.Title, "Budget limit reached") &&
			strings.Contains(n.Detail, "raise the limit"))
	}
	if !found {
		t.Fatalf("the pause must be in Needs you: %+v", needs)
	}
	// nothing resumes it by itself
	time.Sleep(300 * time.Millisecond)
	a.call("usage.list", map[string]any{"taskId": s.task.ID}, &usage)
	if len(usage) != 1 {
		t.Fatalf("a paused run stays paused: %d usage rows", len(usage))
	}

	// the human raises the limit and resumes
	var res struct {
		Raised []string `json:"raised"`
		By     string   `json:"by"`
	}
	a.call("budget.resume", map[string]any{"runId": runID, "limitUsd": 0.01}, &res)
	if len(res.Raised) != 1 || res.Raised[0] != b.ID || res.By != "human:kyle" {
		t.Fatalf("resume %+v", res)
	}
	d = a.econPending(s.task.ID, 1)
	if d.Pending[0].Spec.Kind != "review" { // the reviewer ran and the loop reached the human's merge decision
		t.Fatalf("after resuming: %+v", d.Pending[0].Spec)
	}
	a.call("usage.list", map[string]any{"taskId": s.task.ID}, &usage)
	if len(usage) != 2 {
		t.Fatalf("both turns ran: %d", len(usage))
	}
	var bl []struct {
		Budget struct {
			LimitUSD float64 `json:"limitUsd"`
			Version  int     `json:"version"`
		} `json:"budget"`
		Status *struct {
			State string `json:"state"`
		} `json:"status"`
	}
	a.call("budget.list", map[string]any{"projectId": s.project.ID}, &bl)
	if len(bl) != 1 || bl[0].Budget.LimitUSD != 0.01 || bl[0].Budget.Version != 2 || bl[0].Status == nil || bl[0].Status.State != "ok" {
		t.Fatalf("budget list %+v", bl)
	}

	// the human accepts: the outcome is recorded from the task.completed event
	a.call("run.respond", map[string]any{"runId": runID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": s.task.ID}, &d)
		return d.Task.Status == tasks.Completed
	})
	a.waitFor("the routing outcome", func() bool {
		var n int
		a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM routing_outcomes WHERE task_id=? AND accepted=1`, s.task.ID).Scan(&n)
		return n >= 2
	})

	// route.decisions: the static decisions of the two turns, joined to their outcome; route.explain says what they are
	var decs []struct {
		ID        string `json:"id"`
		DecidedBy string `json:"decidedBy"`
		RunKind   string `json:"runKind"`
		Agent     string `json:"agent"`
		Outcome   *struct {
			Accepted   bool    `json:"accepted"`
			CostSource string  `json:"costSource"`
			CostUSD    float64 `json:"costUsd"`
		} `json:"outcome"`
	}
	a.call("route.decisions", map[string]string{"taskId": s.task.ID}, &decs)
	if len(decs) != 2 || decs[0].DecidedBy != "static" || decs[0].RunKind != "agent_task" || decs[1].RunKind != "review" {
		t.Fatalf("decisions %+v", decs)
	}
	for _, x := range decs {
		if x.Outcome == nil || !x.Outcome.Accepted || x.Outcome.CostSource != "reported" || x.Outcome.CostUSD < 0.0004-1e-9 {
			t.Fatalf("outcome %+v", x.Outcome)
		}
	}
	var ex struct {
		Text string `json:"text"`
	}
	a.call("route.explain", map[string]string{"decisionId": decs[0].ID}, &ex)
	if !strings.Contains(ex.Text, "Decided by: static assignment") || !strings.Contains(ex.Text, "fake-1 via claude") {
		t.Fatalf("explain: %q", ex.Text)
	}
	if _, err := a.c.Call(context.Background(), "route.explain", mustJSON(map[string]string{"decisionId": "dec_nonesuch"})); err == nil {
		t.Fatal("an unknown decision is an error")
	}

	// evals: read from the database, nothing invented
	var rep struct {
		Markdown string `json:"markdown"`
		Report   struct {
			Cost struct {
				Accepted        int `json:"accepted"`
				PerAcceptedOnly struct {
					ReportedUSD float64 `json:"reportedUsd"`
					UnknownRows int     `json:"unknownRows"`
				} `json:"perAcceptedOnly"`
			} `json:"cost"`
			Premium struct {
				Turns int `json:"turns"`
			} `json:"premium"`
			Routing struct {
				Decisions   int `json:"decisions"`
				WithOutcome int `json:"withOutcome"`
			} `json:"routing"`
		} `json:"report"`
	}
	a.call("evals.report", map[string]any{"projectId": s.project.ID}, &rep)
	if rep.Report.Cost.Accepted != 1 || rep.Report.Cost.PerAcceptedOnly.ReportedUSD < 0.0004-1e-9 || rep.Report.Cost.PerAcceptedOnly.UnknownRows != 0 || rep.Report.Premium.Turns != 2 ||
		rep.Report.Routing.Decisions != 2 || rep.Report.Routing.WithOutcome != 2 {
		t.Fatalf("report %+v", rep.Report)
	}
	for _, want := range []string{"Accepted tasks: **1**", "reported $0.0004", "# Drydock evaluation report"} {
		if !strings.Contains(rep.Markdown, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	var exp struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	a.call("evals.export", map[string]any{"projectId": s.project.ID}, &exp)
	if b, err := os.ReadFile(exp.Path); err != nil || len(exp.SHA256) != 64 || !strings.Contains(string(b), "drydock.evals/v1") {
		t.Fatalf("export %+v %v", exp, err)
	}

	// telemetry: spans were recorded for the nodes; the export holds no task text, no prompt, no code
	var te struct {
		Spans    int             `json:"spans"`
		Metrics  int             `json:"metrics"`
		Document json.RawMessage `json:"document"`
	}
	a.call("telemetry.export", nil, &te)
	if te.Spans < 4 || te.Metrics == 0 {
		t.Fatalf("telemetry: %d spans %d metrics", te.Spans, te.Metrics)
	}
	for _, forbidden := range []string{"Implement Subtract", "Subtract returns 0", "return a - b", "Given 5 and 3", "calc.go"} {
		if strings.Contains(string(te.Document), forbidden) {
			t.Errorf("%q reached the telemetry document", forbidden)
		}
	}
	for _, want := range []string{"drydock.node.kind", "drydock.run.id", "gen_ai.token.type"} {
		if !strings.Contains(string(te.Document), want) {
			t.Errorf("telemetry document lacks %s", want)
		}
	}
}

// Rejecting a budget pause ends the run: it is not silently resumed and no turn starts.
func TestRejectingABudgetPauseFailsTheRun(t *testing.T) {
	a := open(t, "")
	s := a.econSetup()
	a.call("budget.set", map[string]any{"scope": "project", "scopeId": s.project.ID, "limitTokens": 1}, nil)
	var runID string
	a.call("task.start", map[string]string{"id": s.task.ID}, &runID)
	// the first turn starts below the limit (nothing spent yet), then the review is held
	d := a.econPending(s.task.ID, 1)
	if !strings.HasPrefix(d.Pending[0].Spec.Title, "Budget limit reached") {
		t.Fatalf("%+v", d.Pending[0].Spec)
	}
	a.call("budget.resume", map[string]any{"runId": runID, "decision": "rejected"}, nil)
	a.waitFor("the run to fail", func() bool {
		a.call("task.get", map[string]string{"id": s.task.ID}, &d)
		return d.Run != nil && d.Run.Status == "FAILED"
	})
	if !strings.Contains(d.Run.FailedReason, "rejected") {
		t.Fatalf("reason %q", d.Run.FailedReason)
	}
	var usage []core.UsageRow
	a.call("usage.list", map[string]any{"taskId": s.task.ID}, &usage)
	if len(usage) != 1 {
		t.Fatalf("only the first turn ran: %d", len(usage))
	}
	if _, err := a.c.Call(context.Background(), "budget.resume", mustJSON(map[string]any{"runId": runID})); err == nil {
		t.Fatal("there is nothing left to resume")
	}
}

func TestChecksSettingsAreHumanSettingsWithSafeDefaults(t *testing.T) {
	a := open(t, "")
	s := a.econSetup()
	var cs struct {
		Secrets     bool `json:"secrets"`
		Deps        bool `json:"deps"`
		DepsOffline bool `json:"depsOffline"`
	}
	a.call("checks.settings.get", map[string]string{"projectId": s.project.ID}, &cs)
	if !cs.Secrets || cs.Deps {
		t.Fatalf("defaults are secrets on, deps off: %+v", cs)
	}
	a.call("checks.settings.set", map[string]any{"projectId": s.project.ID, "secrets": true, "deps": true, "depsOffline": true}, &cs)
	if !cs.Deps || !cs.DepsOffline {
		t.Fatalf("%+v", cs)
	}
	if _, err := a.c.Call(context.Background(), "checks.settings.set", mustJSON(map[string]any{"projectId": "prj_nonesuch", "deps": true})); err == nil {
		t.Fatal("an unknown project is refused")
	}
}

// The relay turns the pipeline's inconclusive-check event into a Deps tool-member message with status skipped, never pass or fail.
func TestSkippedChecksReachTheTaskChannelAsSkipped(t *testing.T) {
	a := open(t, "")
	s := a.econSetup()
	// shaped exactly like the event pipeline.Verify publishes (its own test proves the publisher)
	b, _ := json.Marshal(map[string]any{"kind": "deps", "status": "skipped", "reason": "osv-scanner not installed", "logRef": "/tmp/x.log"})
	if _, err := a.c.Bus.Publish(context.Background(), events.Event{Type: "task.check_inconclusive", ProjectID: s.project.ID, TaskID: s.task.ID, Data: b}); err != nil {
		t.Fatal(err)
	}
	var tr core.TaskDetail
	a.call("task.get", map[string]string{"id": s.task.ID}, &tr)
	a.waitFor("the Deps message", func() bool {
		var msgs []room.Message
		a.call("room.messages", map[string]any{"roomId": tr.RoomID, "limit": 100}, &msgs)
		for _, m := range msgs {
			if m.Author.Key() == "tool:deps" && strings.Contains(m.Body, "SKIPPED") && strings.Contains(m.Body, "osv-scanner not installed") && m.Type == room.TypeResult {
				return true
			}
		}
		return false
	})
}

// ---- routing wiring with providers: a protocol fixture speaks the OpenAI-compatible wire format ----

func TestRoutingProbeSelectionPreviewAndRulesThroughTheMethods(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "d"), User: "kyle", LeaseTTL: 3 * time.Second,
		Credentials: providers.NewMemoryCredentialStoreForTests(),
		Adapters:    map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := &app{t: t, c: c}

	// PROTOCOL FIXTURE: the models differ only in how they answer the probe's tool-calling request.
	srv := llmtest.New(t, func(call llmtest.Call, w http.ResponseWriter, r *http.Request) {
		model, _ := call.Body["model"].(string)
		u := &llmtest.Usage{PromptTokens: 100, CompletionTokens: 10}
		switch model {
		case "cheap-good":
			llmtest.Respond(w, call, llmtest.Completion{Model: model, ToolCalls: []llmtest.ToolCall{{ID: "c1", Name: "add_numbers", Arguments: `{"a":17,"b":25}`}}, Usage: u})
		case "cheaper-but-prose":
			llmtest.Respond(w, call, llmtest.Completion{Model: model, Content: "42", Usage: u})
		default:
			llmtest.Respond(w, call, llmtest.Completion{Model: model, Content: "ok", Usage: u})
		}
	})
	var prov providers.Provider
	a.call("provider.create", map[string]any{"Name": "Fixture", "Kind": "direct", "BaseURL": srv.URL + "/v1", "APIKey": "sk-fixture-key-1234567890", "DataClass": "private", "Tier": "cheap"}, &prov)
	if err := c.Providers.UpsertModels(context.Background(), prov.ID, []providers.Model{
		{ID: "cheaper-but-prose", InputPerM: 0.05, OutputPerM: 0.1, ContextLength: 128000, SupportsTools: true, DataClass: providers.DataPrivate, Tier: providers.TierCheap},
		{ID: "cheap-good", InputPerM: 0.2, OutputPerM: 0.4, ContextLength: 128000, SupportsTools: true, DataClass: providers.DataPrivate, Tier: providers.TierCheap},
	}); err != nil {
		t.Fatal(err)
	}

	var sel struct {
		Default string `json:"default"`
		Message string `json:"message"`
	}
	a.call("route.cheap.select", map[string]any{"providerId": prov.ID}, &sel)
	if sel.Default != "cheap-good" {
		t.Fatalf("the cheapest model that PASSES is selected, not the cheapest: %+v", sel)
	}
	var got struct {
		Selected  bool `json:"selected"`
		Selection struct {
			Default string `json:"default"`
		} `json:"selection"`
	}
	a.call("route.cheap.get", map[string]any{"providerId": prov.ID}, &got)
	if !got.Selected || got.Selection.Default != "cheap-good" {
		t.Fatalf("%+v", got)
	}

	// route.preview: a dry run over the real rules and candidates; the probe result steers it and nothing is recorded
	before := count(c, `SELECT COUNT(*) FROM routing_decisions`)
	var pv struct {
		Blocked  bool   `json:"blocked"`
		Recorded bool   `json:"recorded"`
		Explain  string `json:"explain"`
		Decision struct {
			Model     string `json:"Model"`
			DecidedBy string `json:"DecidedBy"`
		} `json:"decision"`
	}
	req := map[string]any{"RunKind": "agent_task", "TaskType": "test_fix", "Level": "balanced", "Role": "Backend", "ChecksPassing": false}
	a.call("route.preview", map[string]any{"request": req}, &pv)
	if pv.Blocked || pv.Recorded || pv.Decision.Model != "cheap-good" || pv.Decision.DecidedBy != "rules" || !strings.Contains(pv.Explain, "cheap-good") {
		t.Fatalf("preview %+v", pv)
	}
	if count(c, `SELECT COUNT(*) FROM routing_decisions`) != before {
		t.Fatal("a preview must not record a decision")
	}
	// route.previews: several dry runs in one call, in the order asked, none recorded (the Routing page's wiring, P108)
	var many []struct {
		Blocked  bool   `json:"blocked"`
		Error    string `json:"error"`
		Decision struct {
			Model string `json:"Model"`
		} `json:"decision"`
	}
	a.call("route.previews", map[string]any{"requests": []any{req, req}}, &many)
	if len(many) != 2 || many[0].Decision.Model != "cheap-good" || many[1].Decision.Model != "cheap-good" || many[0].Error != "" {
		t.Fatalf("previews %+v", many)
	}
	if count(c, `SELECT COUNT(*) FROM routing_decisions`) != before {
		t.Fatal("previews must not record a decision")
	}
	tooMany := make([]any, 33)
	for i := range tooMany {
		tooMany[i] = req
	}
	if err := a.callErr("route.previews", map[string]any{"requests": tooMany}); err == nil || !strings.Contains(err.Error(), "at most 32") {
		t.Fatalf("33 previews: %v", err)
	}
	// a private context never reaches a trainable provider, even through the preview
	var tp providers.Provider
	a.call("provider.create", map[string]any{"Name": "Trainable", "Kind": "direct", "BaseURL": srv.URL + "/v1", "APIKey": "sk-other-key-1234567890", "DataClass": "trainable", "Tier": "free"}, &tp)
	c.Providers.UpsertModels(context.Background(), tp.ID, []providers.Model{{ID: "free-model", SupportsTools: true, IsFree: true, DataClass: providers.DataTrainable, Tier: providers.TierFree}})
	a.call("route.preview", map[string]any{"request": req}, &pv)
	if pv.Decision.Model == "free-model" {
		t.Fatal("the data-class invariant was violated")
	}

	// a budget at its hard limit blocks the preview: budget limits sit above the router's heuristics
	c.DB.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('prjB','b','/b',1)`)
	c.DB.Writer().Exec(`INSERT INTO model_usage(ts,task_id,agent_id,provider,model,tier,cost_usd,source,cost_source) VALUES(?, '', 'agtB', 'p','m','premium',2.0,'reported','reported')`, time.Now().UnixMilli())
	c.DB.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES('agtB','prjB','b','claude','r',1)`)
	a.call("budget.set", map[string]any{"scope": "project", "scopeId": "prjB", "limitUsd": 1.0}, nil)
	req2 := map[string]any{"RunKind": "agent_task", "TaskType": "test_fix", "Level": "balanced", "ProjectID": "prjB"}
	var blocked struct {
		Blocked bool   `json:"blocked"`
		Reason  string `json:"reason"`
	}
	a.call("route.preview", map[string]any{"request": req2}, &blocked)
	if !blocked.Blocked || !strings.Contains(blocked.Reason, "hard limit") {
		t.Fatalf("a budget at 200%% must block routing: %+v", blocked)
	}

	// the recording router applies the same budget priority and records what it decides (and what it refuses)
	rr := core.EconomicsRouter(c)
	d, err := rr.Decide(context.Background(), router.RouteRequest{RunKind: router.RunAgentTask, TaskType: "test_fix", Level: router.LevelBalanced, ProjectID: "prjOK"})
	if err != nil || d.Model != "cheap-good" || d.DecidedBy != "rules" {
		t.Fatalf("decide: %+v %v", d, err)
	}
	if count(c, `SELECT COUNT(*) FROM routing_decisions WHERE id=? AND decided_by='rules'`, d.ID) != 1 {
		t.Fatal("a real decision is recorded")
	}
	if _, err := rr.Decide(context.Background(), router.RouteRequest{RunKind: router.RunAgentTask, TaskType: "test_fix", ProjectID: "prjB"}); !errors.Is(err, router.ErrBlocked) {
		t.Fatalf("a project over its hard budget must block the real router too: %v", err)
	}
	if text := rr.Explain(d); !strings.Contains(text, "cheap-good") {
		t.Fatalf("explain: %s", text)
	}

	// rules: stored defaults, compare-and-swap
	var rl struct {
		Rules []struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"rules"`
	}
	a.call("route.rules.list", nil, &rl)
	if len(rl.Rules) < 10 {
		t.Fatalf("the shipped rules are stored as editable copies: %d", len(rl.Rules))
	}
	rule := map[string]any{"id": "cheap-first-classes", "scope": "workspace", "priority": 50, "when": "task.type == 'docs'", "then": map[string]any{"prefer_tier": "cheap"}, "enabled": true, "version": 1}
	var saved struct{ Version int }
	a.call("route.rules.set", map[string]any{"rule": rule}, &saved)
	if saved.Version != 2 {
		t.Fatalf("version %d", saved.Version)
	}
	if _, err := a.c.Call(context.Background(), "route.rules.set", mustJSON(map[string]any{"rule": rule})); err == nil || !strings.Contains(err.Error(), "changed since you read it") {
		t.Fatalf("a stale version must conflict: %v", err)
	}
	rule["when"] = "task.type == 'nonsense' &&"
	rule["version"] = 2
	if _, err := a.c.Call(context.Background(), "route.rules.set", mustJSON(map[string]any{"rule": rule})); err == nil {
		t.Fatal("a rule that does not compile must be refused")
	}
	// a new rule from the editor has no id yet: the core names it (the UI's "Add a rule" could never save before)
	var added struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
		Enabled bool   `json:"enabled"`
	}
	a.call("route.rules.set", map[string]any{"rule": map[string]any{"id": "", "scope": "workspace", "priority": 0, "when": "true", "then": map[string]any{"prefer_tier": "cheap"}, "enabled": true, "version": 0}}, &added)
	if !strings.HasPrefix(added.ID, "rule_") || added.Version != 1 || !added.Enabled {
		t.Fatalf("new rule %+v", added)
	}

	// the app's own LLM calls go through the Wrapper: guarded and recorded
	cl := core.EconomicsLLM(c)
	resp, err := cl.Complete(context.Background(), llm.Request{Provider: prov.ID, Model: "cheap-good", Purpose: "classify", MaxTokens: 16,
		Messages: []llm.Message{{Role: "user", Content: "hello"}}, Manifest: &llm.Manifest{Entries: []llm.ManifestEntry{{SourceType: "test", Sensitivity: llm.SensitivityPrivate}}}})
	if err != nil || resp.Usage.Input != 100 || !resp.UsageReported {
		t.Fatalf("%+v %v", resp, err)
	}
	if n := count(c, `SELECT COUNT(*) FROM model_usage WHERE purpose='classify' AND provider=?`, prov.ID); n != 1 {
		t.Fatalf("the wrapper must record usage: %d", n)
	}
	if _, err := cl.Complete(context.Background(), llm.Request{Provider: tp.ID, Model: "free-model", Purpose: "classify",
		Messages: []llm.Message{{Role: "user", Content: "private diff"}}, Manifest: &llm.Manifest{Entries: []llm.ManifestEntry{{SourceType: "diff", Sensitivity: llm.SensitivityPrivate}}}}); err == nil {
		t.Fatal("private context must be refused for a trainable provider")
	}
}

func count(c *core.Core, q string, args ...any) int {
	var n int
	c.DB.Reader().QueryRow(q, args...).Scan(&n)
	return n
}
