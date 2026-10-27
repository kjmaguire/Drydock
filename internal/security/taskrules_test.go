package security

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
)

// rulesOf is an in-memory TaskRules store, as the core's table would answer it.
type rulesOf struct {
	mu    sync.Mutex
	rules []TaskAllowRule
}

func (s *rulesOf) add(r TaskAllowRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ID = "par_" + string(rune('a'+len(s.rules)))
	s.rules = append(s.rules, r)
}

func (s *rulesOf) get(_ context.Context, scope PermissionScope, tool string) []TaskAllowRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []TaskAllowRule
	for _, r := range s.rules {
		if r.TaskID == scope.TaskID && r.AgentID == scope.AgentID && r.Tool == tool {
			out = append(out, r)
		}
	}
	return out
}

// A person approves a call for the rest of its task: the same call of the same task and agent is approved without asking and
// recorded as such; a different command, the same command of another task and the same call by another agent still ask.
func TestATaskRuleApprovesTheSameCallOfTheTaskWithoutAsking(t *testing.T) {
	r := newPermRig(t)
	store := &rulesOf{}
	r.gate.TaskRules = store.get
	ctx := context.Background()
	curl := func(url, why string) ToolRequest {
		return ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": "curl " + url, "description": why}), Root: r.root}
	}
	ch := r.decideAsync(t, curl("https://example.com", "fetch the page"))
	id := r.waitRequested(t)
	rule, err := r.gate.TaskRuleFor(id)
	if err != nil {
		t.Fatal(err)
	}
	if rule.Tool != "claude:Bash" || rule.Program != "curl" || len(rule.Commands) != 1 || rule.Commands[0].Program != "curl" ||
		len(rule.Commands[0].Args) != 1 || rule.Commands[0].Args[0] != "https://example.com" || rule.Summary != "curl https://example.com" ||
		rule.TaskID != "task_1" || rule.AgentID != "agent_1" || rule.Project != "proj_1" || rule.ApprovalID != id || rule.InputDigest != "" {
		t.Fatalf("rule %+v", rule)
	}
	store.add(rule)
	if err := r.apr.Decide(ctx, id, DecisionApprove, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if o := await(t, ch); !o.Allow || o.Outcome != PermissionApproved {
		t.Fatalf("first: %+v", o)
	}
	if _, err := r.gate.TaskRuleFor(id); !errors.Is(err, ErrOnlyOnce) {
		t.Fatalf("a request nobody waits on any more: %v", err)
	}

	// the same command (with another description) is approved without a request
	o := r.gate.Decide(ctx, permScope, curl("https://example.com", "fetch it again"))
	if !o.Allow || o.Outcome != PermissionApprovedForTask || o.Rule != "par_a" || o.ApprovalID != "" {
		t.Fatalf("second: %+v", o)
	}
	var n int
	_ = r.db.Reader().QueryRow(`SELECT COUNT(*) FROM approvals`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d approvals; the second call must not ask", n)
	}
	got := r.decided(t)
	if len(got) != 2 || got[1]["outcome"] != PermissionApprovedForTask || got[1]["rule"] != "par_a" {
		t.Fatalf("events %v", got)
	}

	// another command, the command in another task, the command by another agent: asked
	for _, tc := range []struct {
		name  string
		scope PermissionScope
		req   ToolRequest
	}{
		{"another command", permScope, curl("https://example.org", "x")},
		{"another task", PermissionScope{Project: "proj_1", TaskID: "task_2", RunID: "arun_1", AgentID: "agent_1"}, curl("https://example.com", "x")},
		{"another agent", PermissionScope{Project: "proj_1", TaskID: "task_1", RunID: "arun_2", AgentID: "agent_2"}, curl("https://example.com", "x")},
		{"the command and one more", permScope, ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": "curl https://example.com && curl https://example.org"}), Root: r.root}},
	} {
		ch := make(chan PermissionOutcome, 1)
		go func() { ch <- r.gate.Decide(ctx, tc.scope, tc.req) }()
		id := r.waitRequested(t)
		if err := r.apr.Decide(ctx, id, DecisionReject, "human:kyle"); err != nil {
			t.Fatal(err)
		}
		if o := await(t, ch); o.Allow || o.Outcome != PermissionRejected {
			t.Fatalf("%s: %+v", tc.name, o)
		}
	}

	// a tool that is not a shell: the same input
	fetch := ToolRequest{Tool: "WebFetch", Input: input(t, map[string]any{"url": "https://example.com", "prompt": "summarize"}), Root: r.root}
	ch = r.decideAsync(t, fetch)
	id = r.waitRequested(t)
	rule, err = r.gate.TaskRuleFor(id)
	if err != nil || rule.InputDigest == "" || len(rule.Commands) != 0 || rule.Summary == "" {
		t.Fatalf("%+v %v", rule, err)
	}
	store.add(rule)
	_ = r.apr.Decide(ctx, id, DecisionApprove, "human:kyle")
	await(t, ch)
	if o := r.gate.Decide(ctx, permScope, fetch); o.Outcome != PermissionApprovedForTask || o.Rule != "par_b" {
		t.Fatalf("same fetch: %+v", o)
	}
	other := fetch
	other.Input = input(t, map[string]any{"url": "https://example.com", "prompt": "translate"})
	ch = r.decideAsync(t, other)
	id = r.waitRequested(t)
	_ = r.apr.Decide(ctx, id, DecisionReject, "human:kyle")
	if o := await(t, ch); o.Outcome != PermissionRejected {
		t.Fatalf("another fetch: %+v", o)
	}
}

// A task rule holds the words the person approved: a quoted "*" or "**" among them is that word, not a wildcard, so the rule
// covers that call only (audit 2026-10-27).
func TestATaskRuleTakesAQuotedStarAsAWordNotAWildcard(t *testing.T) {
	r := newPermRig(t)
	store := &rulesOf{}
	r.gate.TaskRules = store.get
	ctx := context.Background()
	curl := func(cmd string) ToolRequest {
		return ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": cmd}), Root: r.root}
	}
	ch := r.decideAsync(t, curl("curl '**'"))
	id := r.waitRequested(t)
	rule, err := r.gate.TaskRuleFor(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(rule.Commands) != 1 || len(rule.Commands[0].Args) != 1 || rule.Commands[0].Args[0] != "**" {
		t.Fatalf("rule %+v", rule)
	}
	store.add(rule)
	_ = r.apr.Decide(ctx, id, DecisionApprove, "human:kyle")
	await(t, ch)
	if o := r.gate.Decide(ctx, permScope, curl("curl '**'")); o.Outcome != PermissionApprovedForTask {
		t.Fatalf("the same call: %+v", o)
	}
	for _, cmd := range []string{"curl https://example.org", "curl https://example.org https://example.com"} {
		ch := make(chan PermissionOutcome, 1)
		go func() { ch <- r.gate.Decide(ctx, permScope, curl(cmd)) }()
		id := r.waitRequested(t)
		_ = r.apr.Decide(ctx, id, DecisionReject, "human:kyle")
		if o := await(t, ch); o.Outcome != PermissionRejected {
			t.Fatalf("%s was covered by the rule for curl '**': %+v", cmd, o)
		}
	}
}

// What the rules mark as dangerous is asked every time: it cannot be approved for the task, and a rule naming it (one written
// by hand) does not cover it either.
func TestDangerousCallsAreNeverApprovedForTheTask(t *testing.T) {
	r := newPermRig(t)
	store := &rulesOf{}
	r.gate.TaskRules = store.get
	ctx := context.Background()
	bash := func(cmd string) ToolRequest {
		return ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": cmd}), Root: r.root}
	}
	for _, tc := range []struct {
		name string
		req  ToolRequest
	}{
		{"a delete", bash("rm -rf build")},
		{"a push", bash("git push origin main")},
		{"a merge", bash("gh pr merge 12 --squash")},
		{"a command Drydock cannot read", bash("$CMD --x")},
		{"a path outside the worktree", bash("curl -o /tmp/x https://example.com")},
		{"a secret in the command", bash("curl -H 'Authorization: token " + fakeGitHubToken() + "' https://api.github.com")},
		{"a write into .git", ToolRequest{Tool: "Write", Input: input(t, map[string]any{"file_path": filepath.Join(r.root, ".git", "hooks", "pre-commit"), "content": "#!/bin/sh"}), Root: r.root}},
		{"secret material", ToolRequest{Tool: "Edit", Input: input(t, map[string]any{"file_path": ".env", "old_string": "a", "new_string": "b"}), Root: r.root}},
		{"a file outside", ToolRequest{Tool: "Read", Input: input(t, map[string]any{"file_path": "/etc/hostname"}), Root: r.root}},
		{"a merge tool", ToolRequest{Tool: "mcp__github__merge_pull_request", Input: input(t, map[string]any{"pull_number": 12}), Root: r.root}},
	} {
		ch := r.decideAsync(t, tc.req)
		id := r.waitRequested(t)
		if _, err := r.gate.TaskRuleFor(id); !errors.Is(err, ErrOnlyOnce) {
			t.Errorf("%s could be approved for the task: %v", tc.name, err)
		}
		// a rule that names exactly this call does not cover it (a call holding a secret is refused only when the rule would be
		// made: no rule can hold it)
		if td, err := ClassifyToolRequest(ctx, r.gate.Analyzer, nil, tc.req); err == nil && tc.name != "a secret in the command" {
			store.add(taskRuleOf(permScope, td))
			if _, ok := r.gate.taskRule(ctx, permScope, td); ok {
				t.Errorf("%s was covered by a rule", tc.name)
			}
		}
		_ = r.apr.Decide(ctx, id, DecisionReject, "human:kyle")
		if o := await(t, ch); o.Outcome != PermissionRejected {
			t.Errorf("%s: %+v", tc.name, o)
		}
	}
}

// The request a person answers with "approve for this task" is itself recorded as approved for the task, with the rule that
// was stored for it and the approval id, not as a plain approval: the Activity would otherwise say the grant was for one
// call. A mark the decision never consumes (the person refuses after all) is dropped with the request.
func TestTheRequestApprovedForTheTaskIsRecordedWithItsRule(t *testing.T) {
	r := newPermRig(t)
	ctx := context.Background()
	req := ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": "curl https://example.com"}), Root: r.root}
	ch := r.decideAsync(t, req)
	id := r.waitRequested(t)
	r.gate.MarkApprovedForTask(id, "par_first")
	if err := r.apr.Decide(ctx, id, DecisionApprove, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	o := await(t, ch)
	if !o.Allow || o.Outcome != PermissionApprovedForTask || o.Rule != "par_first" || o.ApprovalID != id {
		t.Fatalf("approved for the task: %+v", o)
	}
	got := r.decided(t)
	if len(got) != 1 || got[0]["outcome"] != PermissionApprovedForTask || got[0]["rule"] != "par_first" || got[0]["approval"] != id {
		t.Fatalf("permission.decided: %v", got)
	}
	if _, held := r.gate.forTask.Load(id); held {
		t.Fatal("the mark outlived the answer")
	}

	// a mark cleared before the decision, or left when the person refuses, changes nothing
	ch = r.decideAsync(t, req)
	id = r.waitRequested(t)
	r.gate.MarkApprovedForTask(id, "par_second")
	r.gate.ClearApprovedForTask(id)
	if err := r.apr.Decide(ctx, id, DecisionApprove, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if o := await(t, ch); o.Outcome != PermissionApproved || o.Rule != "" {
		t.Fatalf("a cleared mark: %+v", o)
	}
	ch = r.decideAsync(t, req)
	id = r.waitRequested(t)
	r.gate.MarkApprovedForTask(id, "par_third")
	if err := r.apr.Decide(ctx, id, DecisionReject, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if o := await(t, ch); o.Outcome != PermissionRejected || o.Rule != "" {
		t.Fatalf("refused after all: %+v", o)
	}
	if _, held := r.gate.forTask.Load(id); held {
		t.Fatal("the mark of a refused request was kept")
	}
}

// onPublish is the core's bus with a look at each event as it is published, before Publish returns.
type onPublish struct {
	events.Bus
	seen func(events.Event)
}

func (b onPublish) Publish(ctx context.Context, e events.Event) (uint64, error) {
	seq, err := b.Bus.Publish(ctx, e)
	b.seen(e)
	return seq, err
}

// The moment a request is announced a person can approve it for the rest of its task (the Inbox shows it on
// approval.requested): the call that waits on it is known by then, not a moment later.
func TestARequestCanBeApprovedForItsTaskTheMomentItIsAnnounced(t *testing.T) {
	r := newPermRig(t)
	answered := make(chan error, 1)
	r.apr = NewApprovals(r.db, onPublish{Bus: r.bus, seen: func(e events.Event) {
		if e.Type != "approval.requested" {
			return
		}
		var d struct {
			Request string `json:"request"`
		}
		_ = json.Unmarshal(e.Data, &d)
		_, err := r.gate.TaskRuleFor(d.Request)
		answered <- err
	}}, r.audit, WithApprovalClock(r.clk.Now), WithApprovalRedactor(r.scan))
	r.gate.Approvals = r.apr
	ch := r.decideAsync(t, ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": "curl https://example.com"}), Root: r.root})
	id := r.waitRequested(t)
	select { // the row is written a moment before the announcement: wait for the announcement itself
	case err := <-answered:
		if err != nil {
			t.Fatalf("asked for the task as the request was announced: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("approval.requested was never published")
	}
	if err := r.apr.Decide(context.Background(), id, DecisionApprove, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if o := await(t, ch); !o.Allow {
		t.Fatalf("%+v", o)
	}
}
