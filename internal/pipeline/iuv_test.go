package pipeline_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

const rightImplHack = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\n// HACK: temporary\nfunc Subtract(a, b int) int { return a - b }\n"
const rightImplDoc = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\n// Subtract returns a minus b.\nfunc Subtract(a, b int) int { return a - b }\n"

func projectInfo(context.Context, string) (pipeline.ProjectInfo, error) {
	return pipeline.ProjectInfo{DefaultBranch: "main", MergeStrategy: git.MergeCommit, DeleteBranchAfterMerge: true}, nil
}

type roles struct {
	workLog, reviewLog string
}

// fakeRoles gives the worker and the reviewer separate scripts and prompt logs (they are different agent instances).
func fakeRoles(t *testing.T, work, review fakeagent.Script) (agents.Adapter, func(e *env, role string) rt.Config, roles) {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, s fakeagent.Script) (string, string) {
		b, _ := json.Marshal(s)
		sp := filepath.Join(dir, name+".json")
		os.WriteFile(sp, b, 0o644)
		return sp, filepath.Join(dir, name+".prompts")
	}
	ws, wl := write("work", work)
	rs, rl := write("review", review)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	cfg := func(e *env, role string) rt.Config {
		if role == "review" {
			return rt.Config{AgentID: "a2", Adapter: "claude", Provider: "fake", Tier: "premium",
				Spec: agents.RunSpec{AgentID: "a2", Model: "fake-1", Env: []string{"DRYDOCK_FAKE_SCRIPT=" + rs, "DRYDOCK_FAKE_PROMPT_LOG=" + rl}}}
		}
		return rt.Config{AgentID: "a1", Adapter: "claude", Provider: "fake", Tier: "premium",
			Spec: agents.RunSpec{AgentID: "a1", Model: "fake-1", Env: []string{"DRYDOCK_FAKE_SCRIPT=" + ws, "DRYDOCK_FAKE_PROMPT_LOG=" + wl}}}
	}
	return &claude.Adapter{Exe: os.Args[0]}, cfg, roles{wl, rl}
}

func prompts(path string) []string {
	b, _ := os.ReadFile(path)
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var s string
		if json.Unmarshal([]byte(l), &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

func (e *env) runIUV(t *testing.T, loops int) workflow.RunID {
	t.Helper()
	g := pipeline.IterateUntilVerified(e.deps, projectInfo, loops)
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	id, err := e.eng.Start(context.Background(), g.Name, pipeline.Input{TaskID: e.task.ID}, workflow.StartOptions{ProjectID: "p1", TaskID: e.task.ID})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) await(t *testing.T, id workflow.RunID, want workflow.RunStatus) workflow.RunInfo {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	info, err := e.eng.Await(ctx, id, func(r workflow.RunInfo) bool { return r.Status == want || r.Status.Terminal() })
	if err != nil {
		t.Fatalf("await %s: %v (the run is %s%s)", want, err, info.Status, describePending(e.eng, id))
	}
	if info.Status != want {
		t.Fatalf("run ended %s (%s), wanted %s", info.Status, info.FailedReason, want)
	}
	return info
}

// describeRunning names the run's node executions that have started and not finished, for a failure message.
func describeRunning(eng *workflow.Engine, id workflow.RunID) string {
	rows, err := eng.Executions(context.Background(), id)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, r := range rows {
		if r.Status == "started" {
			b.WriteString(" " + string(r.NodeID) + "#" + strconv.Itoa(r.Step) + " started " + time.Since(time.UnixMilli(r.Started)).Round(time.Second).String() + " ago")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "; still running:" + b.String()
}

// describePending names the run's open requests for a failure message, so that a run waiting for a human where a test
// expected it to go on says what it asks.
func describePending(eng *workflow.Engine, id workflow.RunID) string {
	p, err := eng.Pending(context.Background(), id)
	if err != nil || len(p) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("; it waits for:")
	for _, r := range p {
		b.WriteString(" [" + string(r.NodeID) + " " + r.Spec.Kind + ": " + r.Spec.Title + "]")
	}
	return b.String()
}

func (e *env) answer(t *testing.T, id workflow.RunID, decision, by, dataJSON string) error {
	t.Helper()
	time.Sleep(60 * time.Millisecond) // the parked driver releases its lease
	pend, _ := e.eng.Pending(context.Background(), id)
	if len(pend) != 1 {
		t.Fatalf("expected one pending request, got %+v", pend)
	}
	var data json.RawMessage
	if dataJSON != "" {
		data = json.RawMessage(dataJSON)
	}
	return e.eng.Resume(context.Background(), id, workflow.Response{RequestID: pend[0].ID, Decision: decision, By: by, Data: data})
}

func gitLog(t *testing.T, dir string) string {
	out, err := exec.Command("git", "-C", dir, "log", "--oneline", "--all").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	return string(out)
}

// The whole loop with scripted agents: work -> checks -> independent REVISE -> rework with only the feedback -> checks ->
// independent SHIP -> human approval -> merge. Real git, real go test, real SQLite, real processes.
func TestIterateUntilVerifiedHappyPathEndsInHumanMerge(t *testing.T) {
	ad, cfg, lg := fakeRoles(t,
		fakeagent.Script{
			Rules: []fakeagent.Rule{{Match: "Reviewer feedback", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "removed the hack"}}}},
			Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImplHack}}, {Say: "implemented"}}},
		},
		fakeagent.Script{
			Rules:   []fakeagent.Rule{{Match: "HACK", Steps: []fakeagent.Step{{Say: `{"verdict":"REVISE","feedback":"remove the HACK comment"}`}}}},
			Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}},
		})
	e := setup(t, cfg, ad)
	id := e.runIUV(t, 4)
	e.await(t, id, workflow.StatusWaitingHuman)

	wp, rp := prompts(lg.workLog), prompts(lg.reviewLog)
	if len(wp) != 2 || len(rp) != 2 {
		t.Fatalf("worker turns=%d reviewer turns=%d", len(wp), len(rp))
	}
	if !strings.Contains(wp[1], "remove the HACK comment") || !strings.Contains(wp[1], "Acceptance criteria") || strings.Contains(wp[1], "implemented") {
		t.Fatalf("rework prompt must carry the task and the reviewer's feedback, and no previous-session history: %q", wp[1])
	}
	if !strings.Contains(rp[0], "independent reviewer") || !strings.Contains(rp[0], "[passed]") || !strings.Contains(rp[0], "```diff") {
		t.Fatalf("review prompt must attach the evidence and diff: %q", rp[0])
	}
	task, _ := e.tasks.Get(context.Background(), e.task.ID)
	if task.Status != tasks.Verification || task.Iteration != 1 {
		t.Fatalf("task %s iteration %d", task.Status, task.Iteration)
	}
	rv, _ := e.tasks.Reviews(context.Background(), task.ID)
	if len(rv) != 2 || rv[0].Verdict != "REVISE" || rv[1].Verdict != "SHIP" || rv[0].Reviewer != "a2" || rv[1].Reviewer == task.Assignee {
		t.Fatalf("reviews %+v", rv)
	}
	// the approval request carries everything a human needs
	pend, _ := e.eng.Pending(context.Background(), id)
	var sum pipeline.ApproveSummary
	json.Unmarshal(pend[0].Spec.Payload, &sum)
	if pend[0].Spec.Kind != "review" || sum.Branch != task.Branch || len(sum.Evidence) < 4 || len(sum.Reviews) != 2 {
		t.Fatalf("approval payload %+v", sum)
	}
	// main is untouched until the human approves
	if m, _ := os.ReadFile(filepath.Join(e.repo.Root, "calc.go")); strings.Contains(string(m), "return a - b") {
		t.Fatal("nothing may reach main before approval")
	}

	if err := e.answer(t, id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusCompleted)

	task, _ = e.tasks.Get(context.Background(), e.task.ID)
	if task.Status != tasks.Completed || task.AcceptedBy != "human:kyle" {
		t.Fatalf("task %+v", task)
	}
	m, _ := os.ReadFile(filepath.Join(e.repo.Root, "calc.go"))
	if !strings.Contains(string(m), "return a - b") || strings.Contains(string(m), "HACK") {
		t.Fatalf("merged content wrong:\n%s", m)
	}
	if _, err := os.Stat(task.Worktree); err == nil {
		t.Fatal("clean worktree should be removed after merge")
	}
	// Drydock's generated instruction files never reach the repository
	for _, f := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		if _, err := os.Stat(filepath.Join(e.repo.Root, f)); err == nil {
			t.Fatalf("%s leaked into the main working tree", f)
		}
	}
	if log := gitLog(t, e.repo.Root); strings.Contains(log, "AGENTS") {
		t.Fatal("instruction files committed")
	}
	// the manifest of the first work attempt explains what the agent was given
	mf, err := e.deps.Context.LoadManifest(context.Background(), string(id), "work", 1)
	if err != nil {
		t.Fatal(err)
	}
	inc, exc := map[string]bool{}, map[string]string{}
	for _, it := range mf.Items {
		if it.Included {
			inc[it.Source] = true
		} else {
			exc[it.Source] = it.Reason
		}
	}
	if !inc["file:AGENTS.md"] || !inc["prompt (work)"] || !strings.Contains(exc["repo map"], "D5") || exc["previous session transcript"] == "" {
		t.Fatalf("manifest %+v", mf.Items)
	}
	// without the MCP wiring (this harness sets none) the manifest says so instead of staying silent
	if !strings.Contains(exc["mcp:drydock"], "not wired") {
		t.Fatalf("manifest must list the MCP entry as excluded: %+v", mf.Items)
	}
	if log := gitLog(t, e.repo.Root); !strings.Contains(log, "Merge Implement Subtract") {
		t.Fatalf("no merge commit:\n%s", log)
	}
	// the run is fully explained by events
	evs, _ := e.bus.Replay(context.Background(), 0, eventsFilter(task.ID), 0)
	have := map[string]bool{}
	for _, x := range evs {
		have[x.Type] = true
	}
	for _, want := range []string{"task.created", "task.started", "task.review", "task.verification", "task.completed", "task.evidence", "git.commit_created", "git.worktree_deleted"} {
		if !have[want] {
			t.Errorf("missing event %s", want)
		}
	}
}

// A human "request changes" sends the task back with their feedback; the second approval merges.
func TestHumanRejectionLoopsBackThenMerges(t *testing.T) {
	ad, cfg, lg := fakeRoles(t,
		fakeagent.Script{
			Rules: []fakeagent.Rule{{Match: "Changes requested by the human", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImplDoc}}, {Say: "added the doc comment"}}}},
			Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "implemented"}}},
		},
		fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	e := setup(t, cfg, ad)
	id := e.runIUV(t, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	if err := e.answer(t, id, "rejected", "human:kyle", `{"feedback":"please add a doc comment to Subtract"}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusWaitingHuman)
	if wp := prompts(lg.workLog); len(wp) != 2 || !strings.Contains(wp[1], "Changes requested by the human:\nplease add a doc comment") || strings.Contains(wp[1], "Reviewer feedback") {
		t.Fatalf("rework prompt (the human's request is labelled as theirs, not the reviewer's) %q", wp)
	}
	if err := e.answer(t, id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusCompleted)
	if m, _ := os.ReadFile(filepath.Join(e.repo.Root, "calc.go")); !strings.Contains(string(m), "Subtract returns a minus b") {
		t.Fatalf("main:\n%s", m)
	}
}

// An agent identity cannot approve the merge: the merge step refuses and nothing reaches main.
func TestAgentCannotApproveTheMerge(t *testing.T) {
	ad, cfg, _ := fakeRoles(t,
		fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "implemented"}}},
		fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	e := setup(t, cfg, ad)
	id := e.runIUV(t, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	if err := e.answer(t, id, "approved", "a1", `{}`); err != nil {
		t.Fatal(err)
	}
	info := e.await(t, id, workflow.StatusFailed)
	if !strings.Contains(info.FailedReason, "requires an approval by a human") {
		t.Fatalf("reason %q", info.FailedReason)
	}
	if m, _ := os.ReadFile(filepath.Join(e.repo.Root, "calc.go")); strings.Contains(string(m), "return a - b") {
		t.Fatal("an agent approval must never merge")
	}
	task, _ := e.tasks.Get(context.Background(), e.task.ID)
	if task.Status == tasks.Completed {
		t.Fatal("task must not complete")
	}
}

// A reviewer that is the assignee is refused (independence is enforced, not assumed).
func TestReviewerMustDifferFromAssignee(t *testing.T) {
	ad, cfg, _ := fakeRoles(t,
		fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "ok"}}},
		fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	same := func(e *env, role string) rt.Config {
		c := cfg(e, role)
		c.AgentID = "a1" // reviewer configured as the assignee itself
		return c
	}
	e := setup(t, same, ad)
	// with reviewer == assignee at the task level Finalize does not even move the task to REVIEW
	a1 := "a1"
	e.tasks.Patch(context.Background(), e.task.ID, tasks.Update{Reviewer: &a1})
	id := e.runIUV(t, 2)
	info := e.await(t, id, workflow.StatusFailed)
	if !strings.Contains(info.FailedReason, "not REVIEW") && !strings.Contains(info.FailedReason, "different agent instance") {
		t.Fatalf("reason %q", info.FailedReason)
	}
}

func TestParseVerdict(t *testing.T) {
	cases := map[string]string{
		`Looks right. {"verdict":"SHIP","feedback":""}`:                  "SHIP",
		"thinking...\n{\"verdict\":\"revise\",\"feedback\":\"fix nil\"}": "REVISE",
		`{"verdict":"REVISE","feedback":"a"} then {"verdict":"SHIP"}`:    "SHIP", // last wins
		"SHIP":                             "SHIP", // the bare word alone is a verdict
		"ship.":                            "SHIP",
		"SHIP\nall good":                   "REVISE", // strict: a first line that starts with SHIP is not a verdict
		"SHIPPING this would be a mistake": "REVISE",
		"I think it is fine":               "REVISE", // unparseable never ships
		`{"verdict":"MAYBE"}`:              "REVISE",
		"":                                 "REVISE",
	}
	for in, want := range cases {
		if got := pipeline.ParseVerdict(in).Verdict; got != want {
			t.Errorf("%q -> %s want %s", in, got, want)
		}
	}
	if v := pipeline.ParseVerdict(`{"verdict":"REVISE"}`); v.Feedback == "" {
		t.Error("REVISE must always carry feedback")
	}
}

// Real Claude as worker AND as an independent reviewer, then a human approval and merge.
func TestRealClaudeWorkerAndReviewerFullLoop(t *testing.T) {
	if os.Getenv("DRYDOCK_NO_REAL_AGENTS") == "1" {
		t.Skip("DRYDOCK_NO_REAL_AGENTS=1")
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "")
	ad := claude.New()
	if d, _ := ad.Detect(context.Background()); d.Status != agents.StatusReady {
		t.Skipf("real claude not usable here: %s %v", d.Status, d.Problems)
	}
	e := setup(t, func(e *env, role string) rt.Config {
		if role == "review" {
			return rt.Config{AgentID: "a2", Adapter: "claude", Provider: "anthropic", Tier: "premium",
				Spec: agents.RunSpec{AgentID: "a2", Model: "haiku", AllowedTools: []string{"Read", "Glob", "Grep"}, ExtraArgs: []string{"--no-session-persistence"}}}
		}
		return rt.Config{AgentID: "a1", Adapter: "claude", Provider: "anthropic", Tier: "premium",
			Spec: agents.RunSpec{AgentID: "a1", Model: "haiku", PermissionMode: "acceptEdits", AllowedTools: []string{"Read", "Edit", "Write", "Glob", "Grep"}, ExtraArgs: []string{"--no-session-persistence"}}}
	}, ad)
	id := e.runIUV(t, 3)
	e.await(t, id, workflow.StatusWaitingHuman)
	task, _ := e.tasks.Get(context.Background(), e.task.ID)
	rv, _ := e.tasks.Reviews(context.Background(), task.ID)
	if len(rv) == 0 {
		t.Fatal("no review recorded")
	}
	t.Logf("real reviewer verdicts: %+v", rv)
	if rv[len(rv)-1].Verdict != "SHIP" {
		t.Skipf("the real reviewer answered %s (%s); the run is parked for a human, which is correct behaviour", rv[len(rv)-1].Verdict, rv[len(rv)-1].Feedback)
	}
	if err := e.answer(t, id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusCompleted)
	if m, _ := os.ReadFile(filepath.Join(e.repo.Root, "calc.go")); !strings.Contains(string(m), "a - b") {
		t.Fatalf("main:\n%s", m)
	}
	done, _ := e.tasks.Get(context.Background(), e.task.ID)
	t.Logf("real two-agent loop complete: cost $%.4f, iterations %d", done.CostUSD, done.Iteration)
}
