package core_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
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

// Audit 2026-10-16, H8: a large Write outside the worktree used to reach the task page as `{"content":"...[N more bytes]`, with
// its destination sorted behind the body and cut off with it. The page now gets the destination apart, the short fields first,
// and says the input is shortened; the person's answer still covers the whole input.
func TestALargeWriteOutsideTheWorktreeShowsItsDestinationOnTheTaskPage(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "elsewhere", "authorized_keys")
	big := strings.Repeat("ssh-ed25519 AAAA-not-a-real-key attacker@example\n", 4000) // ~190 KB
	in, _ := json.Marshal(map[string]string{"content": big, "file_path": outside})
	a := openScripted(t, nil, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{{Tool: "Write", Input: in, Result: "wrote it", Ask: true},
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

	var pl map[string]any
	var id string
	var detail core.TaskDetail
	a.waitFor("the permission request for the write", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		for _, pr := range detail.Pending {
			var m map[string]any
			_ = json.Unmarshal(pr.Spec.Payload, &m)
			if m["gate"] == core.PermissionGate && m["tool"] == "Write" {
				pl, id = m, string(pr.ID)
				return true
			}
		}
		return false
	})
	shown, _ := pl["input"].(string)
	if !strings.HasPrefix(shown, `{"file_path":`) || len(shown) > 70000 || !strings.Contains(shown, "bytes not shown") {
		t.Fatalf("the input on the task page starts %.80q, is %d bytes", shown, len(shown))
	}
	targets, _ := pl["targets"].([]any)
	if len(targets) != 1 || !strings.HasSuffix(filepath.ToSlash(targets[0].(string)), "elsewhere/authorized_keys") || pl["shortened"] != true {
		t.Fatalf("targets %v, shortened %v", pl["targets"], pl["shortened"])
	}
	var title string
	_ = a.c.DB.Reader().QueryRow(`SELECT title FROM approvals WHERE id=?`, id).Scan(&title)
	if !strings.HasPrefix(title, "Permission request: Write: ") || !strings.HasSuffix(filepath.ToSlash(title), "elsewhere/authorized_keys") || len(title) > 260 {
		t.Fatalf("the approval's title is %q", title)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": id, "decision": "rejected", "data": map[string]string{}}, nil)
	a.waitFor("the review request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1 && detail.Pending[0].Spec.Kind == "review"
	})
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("a refused write was made")
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": detail.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Completed
	})
}

// An ACP worker (Codex here, stood in for by the scripted ACP agent, a labelled test double that is a real separate process on
// the real ACP SDK) asks over session/request_permission, and its requests go through the same gate as a Claude worker's:
// a plain "ls" is allowed by the rules, a fetch no rule covers waits for the human on the task page and is approved there, and
// reading Claude's credential file is refused outright. The agent is told each answer; nothing is refused or approved on the person's behalf.
func TestACPWorkerPermissionRequestsGoThroughTheAnalyzerAndTheHuman(t *testing.T) {
	perm := func(id, title, kind string, in map[string]any) fakeacp.Step {
		return fakeacp.Step{Perm: &fakeacp.Perm{Tool: fakeacp.Tool{ID: id, Title: title, Kind: kind, Input: in}}}
	}
	script, _ := json.Marshal(fakeacp.Script{Default: []fakeacp.Step{
		perm("t1", "List files", "execute", map[string]any{"command": "ls"}),
		perm("t2", "Fetch the Go docs", "fetch", map[string]any{"url": "https://go.dev/doc/"}),
		{Perm: &fakeacp.Perm{Tool: fakeacp.Tool{ID: "t3", Title: "Read the Claude sign-in", Kind: "read",
			Input: map[string]any{"path": "~/.claude/.credentials.json"}, Locations: []string{"~/.claude/.credentials.json"}}}},
		{FSWrite: &fakeacp.FSOp{Path: "calc.go", Content: impl}},
		{Say: "implemented Subtract"},
	}})
	sp := filepath.Join(t.TempDir(), "acp-script.json")
	if err := os.WriteFile(sp, script, 0o644); err != nil {
		t.Fatal(err)
	}
	codex := acp.New(acp.Config{ID: "codex", Command: os.Args[0], Env: []string{"DRYDOCK_FAKE_ACP=1", "DRYDOCK_FAKE_ACP_SCRIPT=" + sp}})
	a := openWith(t, map[string]agents.Adapter{"codex": codex})
	p, _ := a.setup("gocalc")
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Codex worker", Adapter: "codex", Role: "backend"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)

	var pl map[string]any
	var id string
	a.waitFor("the fetch to wait on the task page", func() bool {
		var detail core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		for _, p := range detail.Pending {
			var m map[string]any
			_ = json.Unmarshal(p.Spec.Payload, &m)
			if m["gate"] == core.PermissionGate {
				pl, id = m, string(p.ID)
				return true
			}
		}
		return false
	})
	if tool, _ := pl["tool"].(string); !strings.Contains(tool, "Fetch the Go docs") || !strings.Contains(pl["input"].(string), "go.dev") || pl["agentId"] != worker.ID {
		t.Fatalf("the task page must show the call as the agent made it: %v", pl)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": id, "decision": "approved", "data": map[string]string{}}, nil)

	var outcomes []string
	a.waitFor("three permission decisions", func() bool {
		outcomes = outcomes[:0]
		for _, e := range a.eventsOf("permission.decided") {
			outcomes = append(outcomes, e["outcome"].(string))
		}
		return len(outcomes) >= 3
	})
	if strings.Join(outcomes, ",") != "allowed_by_rule,approved,denied" {
		t.Fatalf("outcomes %v", outcomes)
	}
	var said []string // the agent's own messages: it says which option each request ended with
	a.waitFor("the worker to finish its turn", func() bool {
		said = said[:0]
		for _, e := range a.eventsOf("agent.output") {
			if d, _ := e["data"].(map[string]any); e["kind"] == "message_end" && d != nil {
				said = append(said, fmt.Sprint(d["text"]))
			}
		}
		return strings.Contains(strings.Join(said, " | "), "implemented Subtract")
	})
	if got := strings.Join(said, " | "); !strings.HasPrefix(got, "permission: selected allow | permission: selected allow | permission: selected reject") {
		t.Fatalf("the agent must be told allow, allow, reject: %s", got)
	}
}

// An ACP call is read as the analyzer's tool vocabulary only where that reading is exact; anything else keeps the agent's name
// and input, which no rule allows, so a person sees it whole.
func TestAnACPCallIsReadForTheAnalyzerOnlyWhereThatIsExact(t *testing.T) {
	for _, c := range []struct {
		q    agents.ToolAsk
		tool string
		in   string
	}{
		{agents.ToolAsk{Kind: "execute", Tool: "Run", Args: json.RawMessage(`{"command":"go test ./...","description":"tests"}`)}, "Bash", `{"command":"go test ./..."}`},
		// a field that changes what runs (a working folder) is not a plain command
		{agents.ToolAsk{Kind: "execute", Tool: "Run", Args: json.RawMessage(`{"command":"ls","cwd":"C:\\"}`)}, "acp:Run", `{"command":"ls","cwd":"C:\\"}`},
		{agents.ToolAsk{Kind: "execute", Tool: "Run", Args: json.RawMessage(`{"command":["bash","-lc","ls"]}`)}, "acp:Run", `{"command":["bash","-lc","ls"]}`},
		{agents.ToolAsk{Kind: "read", Tool: "Read", Locations: []string{"src/a.go"}}, "Read", `{"file_path":"src/a.go"}`},
		// an edit is a Write with what it writes, so a person asked sees it and the approval is bound to it
		{agents.ToolAsk{Kind: "edit", Tool: "Edit", Locations: []string{"src/a.go"}, Args: json.RawMessage(`{"new":"x"}`)}, "Write", `{"content":"{\"new\":\"x\"}","file_path":"src/a.go"}`},
		{agents.ToolAsk{Kind: "edit", Tool: "Edit", Locations: []string{"src/a.go"}}, "Write", `{"file_path":"src/a.go"}`},
		// a path and one text: the text is what the person sees (Gemini's write_file), not the fields as JSON
		{agents.ToolAsk{Kind: "edit", Tool: "write_file", Locations: []string{"src/a.go"}, Args: json.RawMessage(`{"file_path":"src/a.go","content":"package a\n"}`)}, "Write", `{"content":"package a\n","file_path":"src/a.go"}`},
		// fields that write elsewhere than the location reported are not a Write of that location: a person decides
		{agents.ToolAsk{Kind: "edit", Tool: "write_file", Locations: []string{"src/a.go"}, Args: json.RawMessage(`{"file_path":"src/b.go","content":"x"}`)}, "acp:write_file", `{"file_path":"src/b.go","content":"x"}`},
		// a delete is asked: as rm -r it would be destructive, and a Write of its path would let it through inside the worktree
		{agents.ToolAsk{Kind: "delete", Tool: "Delete", Locations: []string{"build"}, Args: json.RawMessage(`{"path":"build"}`)}, "acp:Delete", `{"path":"build"}`},
		{agents.ToolAsk{Kind: "edit", Tool: "Edit", Args: json.RawMessage(`{"new":"x"}`)}, "acp:Edit", `{"new":"x"}`},
		{agents.ToolAsk{Kind: "move", Tool: "Move", Locations: []string{"a", "b"}}, "acp:Move", `null`},
		{agents.ToolAsk{Kind: "fetch", Tool: "Fetch", Args: json.RawMessage(`{"url":"https://x"}`)}, "acp:Fetch", `{"url":"https://x"}`},
		{agents.ToolAsk{}, "acp:tool", `null`},
	} {
		r := core.ACPToolRequest(c.q)
		if r.Tool != c.tool || string(r.Input) != c.in {
			t.Errorf("%+v: got %s %s, want %s %s", c.q, r.Tool, r.Input, c.tool, c.in)
		}
	}
}

// P88: a person can approve a Claude worker's call with input they changed. The changed call is checked by the rules again
// (a refusal stands even for a person's edit), the CLI runs the changed input, and only a digest of it is recorded: the
// edit itself is kept in memory, never in the approvals table, the events or the audit.
func TestAPersonApprovesAWorkersCallWithEditsAndTheRulesStillApply(t *testing.T) {
	ask := func(cmd string) fakeagent.Step {
		in, _ := json.Marshal(map[string]string{"command": cmd, "description": "step"})
		return fakeagent.Step{Tool: "Bash", Input: in, Result: "ran", Ask: true}
	}
	permLog := filepath.Join(t.TempDir(), "answers.jsonl")
	t.Setenv("DRYDOCK_FAKE_PERMISSION_LOG", permLog)
	a := openScripted(t, nil, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{ask("npm install left-pad"), ask("npm install right-pad"),
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
	if pl["editable"] != true || pl["shell"] != true {
		t.Fatalf("a Claude worker's whole shell command can be changed before it is approved: %v", pl)
	}
	// a changed input goes only with an approval, and must be an object
	if err := a.callErr("run.respond", map[string]any{"runId": runID, "requestId": id, "decision": "rejected", "data": map[string]any{"input": map[string]string{"command": "x"}}}); err == nil {
		t.Fatal("a refusal cannot carry a changed input")
	}
	if err := a.callErr("run.respond", map[string]any{"runId": runID, "requestId": id, "decision": "approved", "data": map[string]any{"input": "npm ci"}}); err == nil {
		t.Fatal("a changed input that is not a JSON object must be refused")
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": id, "decision": "approved", "data": map[string]any{"input": map[string]string{"command": "npm install left-pad@1.3.0 --ignore-scripts"}}}, nil)

	// the second call is changed into one the rules refuse outright: the person's edit does not get past them
	_, id2 := permission("npm install right-pad")
	a.call("run.respond", map[string]any{"runId": runID, "requestId": id2, "decision": "approved", "data": map[string]any{"input": map[string]string{"command": "cat ~/.claude/.credentials.json"}}}, nil)

	var detail core.TaskDetail
	a.waitFor("the review request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1 && detail.Pending[0].Spec.Kind == "review"
	})

	raw, _ := os.ReadFile(permLog)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("answers: %s", raw)
	}
	var first, second struct {
		Behavior     string          `json:"behavior"`
		Message      string          `json:"message"`
		UpdatedInput json.RawMessage `json:"updatedInput"`
	}
	_ = json.Unmarshal([]byte(lines[0]), &first)
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if first.Behavior != "allow" || string(first.UpdatedInput) != `{"command":"npm install left-pad@1.3.0 --ignore-scripts"}` {
		t.Fatalf("the CLI must run the changed input: %s", lines[0])
	}
	if second.Behavior != "deny" || !strings.Contains(second.Message, "as a person changed it") {
		t.Fatalf("an edit the rules refuse must be refused: %s", lines[1])
	}
	var outcomes []string
	for _, e := range a.eventsOf("permission.decided") {
		outcomes = append(outcomes, e["outcome"].(string))
		if e["outcome"] == "approved_with_edits" && e["editedInputDigest"] == nil {
			t.Fatalf("an approval with edits records the digest of what ran: %v", e)
		}
	}
	if strings.Join(outcomes, ",") != "approved_with_edits,denied" {
		t.Fatalf("outcomes %v", outcomes)
	}
	// the edits are nowhere in the database: not in the approvals, the events or the audit
	for _, q := range []string{`SELECT COUNT(*) FROM approvals WHERE CAST(payload AS TEXT) LIKE '%1.3.0%' OR response LIKE '%1.3.0%'`,
		`SELECT COUNT(*) FROM events WHERE CAST(data AS TEXT) LIKE '%1.3.0%' AND type <> 'agent.output'`,
		`SELECT COUNT(*) FROM audit_log WHERE CAST(detail AS TEXT) LIKE '%1.3.0%'`} {
		var n int
		if err := a.c.DB.Reader().QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != 0 {
			t.Fatalf("the edit is stored: %s -> %d", q, n)
		}
	}
	var statuses []string
	rows, _ := a.c.DB.Reader().Query(`SELECT status FROM approvals WHERE id IN (?, ?) ORDER BY created_at`, id, id2)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		statuses = append(statuses, s)
	}
	rows.Close()
	if strings.Join(statuses, ",") != "CONSUMED,CANCELLED" {
		t.Fatalf("the approvals: %v", statuses)
	}

	// the task's activity shows each wait for the person in the worker's turn, and how it was answered (P87, P88)
	var act struct {
		Threads []struct {
			AgentID string `json:"agentId"`
			Events  []struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				Value struct {
					ID      string `json:"id"`
					Title   string `json:"title"`
					Outcome string `json:"outcome"`
				} `json:"value"`
			} `json:"events"`
		} `json:"threads"`
	}
	a.call("task.activity", map[string]string{"taskId": task.ID}, &act)
	var waits []string
	for _, th := range act.Threads {
		for _, e := range th.Events {
			if e.Type == "CUSTOM" {
				if th.AgentID != worker.ID {
					t.Fatalf("a wait in another agent's thread: %+v", th)
				}
				waits = append(waits, e.Name+" "+e.Value.ID+" "+e.Value.Title+e.Value.Outcome)
			}
		}
	}
	want := []string{"drydock.interrupt " + id + " Bash: npm install left-pad", "drydock.resume " + id + " approved_with_edits",
		"drydock.interrupt " + id2 + " Bash: npm install right-pad", "drydock.resume " + id2 + " denied"}
	if strings.Join(waits, "\n") != strings.Join(want, "\n") {
		t.Fatalf("waits in the activity:\n%s", strings.Join(waits, "\n"))
	}
}
