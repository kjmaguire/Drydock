package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// A Claude worker outside a sandbox asks Drydock about the tool uses its rules do not cover (docs/security.md s4 and s5). The
// worker is the scripted stand-in (fakeagent, a labelled test double) asking through the real gateway, the real stdio shim and
// the real analyzer: "ls" is allowed by the rules, "npm install left-pad" waits for the human and is approved on the task page,
// "rm -rf build" waits and is rejected, and reading Claude's credential file is refused outright. Each decision is recorded; the
// requests appear in Needs you and on the task page with the tool and its input; the reviewer turn gets none of this.
func TestWorkerPermissionPromptsGoThroughTheAnalyzerAndTheHuman(t *testing.T) {
	ask := func(cmd string) fakeagent.Step {
		in, _ := json.Marshal(map[string]string{"command": cmd, "description": "step"})
		return fakeagent.Step{Tool: "Bash", Input: in, Result: "ran: " + cmd, Ask: true}
	}
	permLog := filepath.Join(t.TempDir(), "answers.jsonl")
	t.Setenv("DRYDOCK_FAKE_PERMISSION_LOG", permLog)
	argsLog := filepath.Join(t.TempDir(), "args.jsonl")
	t.Setenv("DRYDOCK_FAKE_ARGS_LOG", argsLog)
	a := openScripted(t, nil, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{ask("ls"), ask("npm install left-pad"), ask("rm -rf build"), ask("cat ~/.claude/.credentials.json"),
			{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}},
	})
	p, _ := a.setup("gocalc")
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)

	permission := func(input string) (pr map[string]any, id string) {
		var detail core.TaskDetail
		a.waitFor("the permission request for "+input, func() bool {
			a.call("task.get", map[string]string{"id": task.ID}, &detail)
			for _, p := range detail.Pending {
				var pl map[string]any
				_ = json.Unmarshal(p.Spec.Payload, &pl)
				if pl["gate"] == core.PermissionGate && pl["input"] == input {
					pr, id = pl, string(p.ID)
					return true
				}
			}
			return false
		})
		return pr, id
	}
	pl, id := permission("npm install left-pad")
	if pl["tool"] != "Bash" || pl["kind"] == "" || pl["reason"] == "" || pl["agentId"] != worker.ID {
		t.Fatalf("the task page must show the tool, its input, the kind and why: %v", pl)
	}
	var needs []room.NeedsYouItem
	a.call("needsyou.list", map[string]string{"projectId": p.ID}, &needs)
	var listed bool
	for _, n := range needs {
		if n.ApprovalID == id && n.TaskID == task.ID && strings.HasPrefix(n.Title, "Permission request: Bash: npm install left-pad") {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("Needs you must list the request against the task: %+v", needs)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": id, "decision": "approved", "data": map[string]string{}}, nil)

	_, id2 := permission("rm -rf build")
	a.call("run.respond", map[string]any{"runId": runID, "requestId": id2, "decision": "rejected", "data": map[string]string{}}, nil)

	var detail core.TaskDetail
	a.waitFor("the review request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1 && detail.Pending[0].Spec.Kind == "review"
	})
	a.call("run.respond", map[string]any{"runId": runID, "requestId": detail.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Completed
	})

	// what the worker was told, in order
	raw, _ := os.ReadFile(permLog)
	var answers []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var ans struct {
			Behavior     string          `json:"behavior"`
			Message      string          `json:"message"`
			UpdatedInput json.RawMessage `json:"updatedInput"`
			Error        string          `json:"error"`
		}
		_ = json.Unmarshal([]byte(line), &ans)
		if ans.Error != "" {
			t.Fatalf("the permission tool failed: %s", ans.Error)
		}
		answers = append(answers, ans.Behavior)
		if ans.Behavior == "allow" && !strings.Contains(string(ans.UpdatedInput), `"command"`) {
			t.Fatalf("an allow must carry the exact input back: %s", line)
		}
	}
	if strings.Join(answers, ",") != "allow,allow,deny,deny" {
		t.Fatalf("answers %v (%s)", answers, raw)
	}
	var outcomes []string
	for _, e := range a.eventsOf("permission.decided") {
		outcomes = append(outcomes, e["outcome"].(string))
		if e["_task"] != task.ID || e["_agent"] != worker.ID {
			t.Fatalf("decision event without its task and agent: %v", e)
		}
	}
	if strings.Join(outcomes, ",") != "allowed_by_rule,approved,rejected,denied" {
		t.Fatalf("permission.decided outcomes %v", outcomes)
	}
	var st1, st2 string
	_ = a.c.DB.Reader().QueryRow(`SELECT status FROM approvals WHERE id=?`, id).Scan(&st1)
	_ = a.c.DB.Reader().QueryRow(`SELECT status FROM approvals WHERE id=?`, id2).Scan(&st2)
	if st1 != "CONSUMED" || st2 != "REJECTED" {
		t.Fatalf("approval states %s, %s", st1, st2)
	}

	// only the worker turn was told to ask Drydock; the read-only reviewer turn was not
	argv, _ := os.ReadFile(argsLog)
	var withFlag, starts int
	for _, line := range strings.Split(strings.TrimSpace(string(argv)), "\n") {
		var args []string
		_ = json.Unmarshal([]byte(line), &args)
		starts++
		if i := slices.Index(args, "--permission-prompt-tool"); i >= 0 {
			withFlag++
			if args[i+1] != claude.PermissionPromptTool || !slices.Contains(args, "acceptEdits") {
				t.Fatalf("worker argv %v", args)
			}
		}
	}
	if starts != 2 || withFlag != 1 {
		t.Fatalf("%d starts, %d with the flag; want the worker's only", starts, withFlag)
	}
}

// The worker turn of a project without a sandbox is offered the permission tool, told to ask it and given time to wait for a
// person; the read-only reviewer turn is not (permprompt_internal_test.go covers sandboxed turns and other CLIs).
func TestPermissionToolIsOfferedToTheWorkerTurnOnly(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}, probe: true}
	a := openWith(t, map[string]agents.Adapter{"claude": capt})
	p, _ := a.setup("gocalc")
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the review request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})
	starts := capt.all()
	if len(starts) != 2 {
		t.Fatalf("starts %d", len(starts))
	}
	work, review := starts[0], starts[1]
	if !slices.Contains(work.Tools, "permission_prompt") || work.Spec.PermissionPromptTool != claude.PermissionPromptTool || !slices.Contains(work.Spec.Env, "MCP_TOOL_TIMEOUT=2100000") {
		t.Fatalf("worker: tools %v, prompt tool %q, env %v", work.Tools, work.Spec.PermissionPromptTool, work.Spec.Env)
	}
	if slices.Contains(review.Tools, "permission_prompt") || review.Spec.PermissionPromptTool != "" {
		t.Fatalf("reviewer: tools %v, prompt tool %q", review.Tools, review.Spec.PermissionPromptTool)
	}
}
