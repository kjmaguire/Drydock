package security

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
)

func permAnalyzer(t *testing.T) *RuleAnalyzer {
	t.Helper()
	an, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	return an
}

func permRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte("package calc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func input(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The analyzer's answer to each kind of request Claude Code can send: shell commands through the command analyzer, file tools
// through the path guard, everything else to a person.
func TestClassifyToolRequestFollowsTheRules(t *testing.T) {
	ctx := context.Background()
	an := permAnalyzer(t)
	root := permRoot(t)
	home, _ := os.UserHomeDir()
	cases := []struct {
		name, tool string
		in         any
		verdict    Verdict
		kind       string
	}{
		{"read-only command", "Bash", map[string]any{"command": "ls -la", "description": "list"}, VerdictAllow, ""},
		{"write inside the worktree", "Bash", map[string]any{"command": "mkdir -p out"}, VerdictAllow, ""},
		{"destructive command", "Bash", map[string]any{"command": "rm -rf build"}, VerdictAsk, KindShellDestructive},
		{"network", "Bash", map[string]any{"command": "curl https://example.com"}, VerdictAsk, KindShellCommand},
		{"code the analyzer cannot see into", "Bash", map[string]any{"command": "python3 -c 'print(1)'"}, VerdictAsk, KindShellCommand},
		{"unanalyzable", "Bash", map[string]any{"command": "$CMD --x"}, VerdictAsk, KindShellUnanalyzable},
		{"no command", "Bash", map[string]any{"description": "nothing"}, VerdictAsk, KindShellUnanalyzable},
		{"credential file by command", "Bash", map[string]any{"command": "cat ~/.claude/.credentials.json"}, VerdictDeny, ""},
		{"production deploy", "Bash", map[string]any{"command": "wrangler deploy"}, VerdictDeny, ""},
		{"read inside", "Read", map[string]any{"file_path": filepath.Join(root, "calc.go")}, VerdictAllow, ""},
		{"relative edit inside", "Edit", map[string]any{"file_path": "calc.go", "old_string": "a", "new_string": "b"}, VerdictAllow, ""},
		{"grep without a path", "Grep", map[string]any{"pattern": "TODO"}, VerdictAllow, ""},
		{"read outside", "Read", map[string]any{"file_path": "/etc/hostname"}, VerdictAsk, KindFSOutsideWorktree},
		{"write escaping with ..", "Write", map[string]any{"file_path": "../outside.txt", "content": "x"}, VerdictAsk, KindFSOutsideWorktree},
		{"write into .git", "Write", map[string]any{"file_path": filepath.Join(root, ".git", "hooks", "pre-commit"), "content": "#!/bin/sh"}, VerdictAsk, KindAgentTool},
		{"secret material", "Edit", map[string]any{"file_path": ".env", "old_string": "a", "new_string": "b"}, VerdictAsk, KindSecretsAccess},
		{"credential file by path", "Read", map[string]any{"file_path": filepath.Join(home, ".claude", ".credentials.json")}, VerdictDeny, ""},
		{"web fetch", "WebFetch", map[string]any{"url": "https://example.com", "prompt": "summarize"}, VerdictAsk, KindAgentTool},
		{"another server's tool", "mcp__github__create_issue", map[string]any{"title": "x"}, VerdictAsk, KindAgentTool},
		{"the permission tool itself", "mcp__drydock__permission_prompt", map[string]any{"tool_name": "Bash"}, VerdictDeny, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := ClassifyToolRequest(ctx, an, nil, ToolRequest{Tool: c.tool, Input: input(t, c.in), Root: root})
			if err != nil {
				t.Fatal(err)
			}
			if d.Verdict != c.verdict || d.Kind != c.kind {
				t.Fatalf("got %s/%q, want %s/%q (reasons %v)", d.Verdict, d.Kind, c.verdict, c.kind, d.Reasons)
			}
			if d.Verdict != VerdictAllow && len(d.Reasons) == 0 {
				t.Fatal("a refusal or a question must say why")
			}
			if !strings.HasPrefix(d.Action.Tool, "claude:"+c.tool) || d.Action.ArgvDigest == "" {
				t.Fatalf("descriptor %+v", d.Action)
			}
		})
	}
	// a request with no worktree cannot be checked
	if d, _ := ClassifyToolRequest(ctx, an, nil, ToolRequest{Tool: "Read", Input: input(t, map[string]any{"file_path": "a"})}); d.Verdict != VerdictDeny {
		t.Fatalf("no root: %s", d.Verdict)
	}
	// input that is not JSON is an error, never an allow
	if _, err := ClassifyToolRequest(ctx, an, nil, ToolRequest{Tool: "Bash", Input: json.RawMessage(`{"command":`), Root: root}); err == nil {
		t.Fatal("broken JSON must not classify")
	}
	// a stored rule of the project turns an ask into an allow, as in the command decision table
	pol, err := NewPolicy(PolicySpec{Stored: []StoredRule{{Program: "curl", Args: []string{"**"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := ClassifyToolRequest(ctx, an, pol, ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": "curl https://example.com"}), Root: root}); d.Verdict != VerdictAllow {
		t.Fatalf("a stored rule must allow: %s %v", d.Verdict, d.Reasons)
	}
}

// The hash an approval is bound to is of exactly the tool and its input: key order does not matter, any other value does.
func TestPermissionBindsExactlyTheToolAndInput(t *testing.T) {
	ctx := context.Background()
	an := permAnalyzer(t)
	root := permRoot(t)
	hash := func(tool, raw string) string {
		d, err := ClassifyToolRequest(ctx, an, nil, ToolRequest{Tool: tool, Input: json.RawMessage(raw), Root: root})
		if err != nil {
			t.Fatal(err)
		}
		return d.Action.Hash()
	}
	a := hash("Bash", `{"command":"rm -rf build","description":"clean"}`)
	if b := hash("Bash", `{ "description":"clean", "command":"rm -rf build" }`); a != b {
		t.Fatal("the same input in another key order must hash the same")
	}
	for _, other := range []struct{ tool, raw string }{
		{"Bash", `{"command":"rm -rf build ","description":"clean"}`},
		{"Bash", `{"command":"rm -rf build","description":"clean","run_in_background":true}`},
		{"Bash", `{"command":"rm -rf build"}`},
		{"PowerShell", `{"command":"rm -rf build","description":"clean"}`},
	} {
		if hash(other.tool, other.raw) == a {
			t.Fatalf("%s %s must not share the approval of the original call", other.tool, other.raw)
		}
	}
}

type permRig struct {
	*rig
	gate *PermissionGate
	root string
}

func newPermRig(t *testing.T) *permRig {
	t.Helper()
	r := newRig(t)
	return &permRig{rig: r, root: permRoot(t), gate: &PermissionGate{Approvals: r.apr, Analyzer: permAnalyzer(t), Audit: r.audit, Bus: r.bus, TTL: 10 * time.Minute, Poll: 5 * time.Millisecond}}
}

var permScope = PermissionScope{Project: "proj_1", TaskID: "task_1", RunID: "arun_1", AgentID: "agent_1"}

// waitRequested returns the id of the one REQUESTED permission approval, once it exists.
func (r *permRig) waitRequested(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var id string
		if err := r.db.Reader().QueryRow(`SELECT id FROM approvals WHERE status='REQUESTED' AND title LIKE ?`, PermissionTitlePrefix+"%").Scan(&id); err == nil {
			return id
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no permission approval was requested")
	return ""
}

func (r *permRig) decided(t *testing.T) []map[string]any {
	t.Helper()
	evs, err := r.bus.Replay(context.Background(), 0, events.Filter{Types: []string{EventPermissionDecided}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range evs {
		m := map[string]any{}
		_ = json.Unmarshal(e.Data, &m)
		out = append(out, m)
	}
	return out
}

func (r *permRig) decideAsync(t *testing.T, req ToolRequest) <-chan PermissionOutcome {
	t.Helper()
	ch := make(chan PermissionOutcome, 1)
	go func() { ch <- r.gate.Decide(context.Background(), permScope, req) }()
	return ch
}

func await(t *testing.T, ch <-chan PermissionOutcome) PermissionOutcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(10 * time.Second):
		t.Fatal("the permission request did not end")
		return PermissionOutcome{}
	}
}

// Allowed by rule and refused by the analyzer are answered at once and recorded; nothing is asked of a person.
func TestPermissionGateAnswersRulesWithoutAPerson(t *testing.T) {
	r := newPermRig(t)
	ctx := context.Background()
	o := r.gate.Decide(ctx, permScope, ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": "ls"}), Root: r.root})
	if !o.Allow || o.Outcome != PermissionAllowedByRule || o.ApprovalID != "" {
		t.Fatalf("allowed by rule: %+v", o)
	}
	o = r.gate.Decide(ctx, permScope, ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": "cat ~/.claude/.credentials.json"}), Root: r.root})
	if o.Allow || o.Outcome != PermissionDenied || !strings.Contains(o.Message, "credential") {
		t.Fatalf("refused: %+v", o)
	}
	var n int
	_ = r.db.Reader().QueryRow(`SELECT COUNT(*) FROM approvals`).Scan(&n)
	if n != 0 {
		t.Fatalf("no approval may be raised for what the rules decide, got %d", n)
	}
	got := r.decided(t)
	if len(got) != 2 || got[0]["outcome"] != PermissionAllowedByRule || got[1]["outcome"] != PermissionDenied || got[0]["tool"] != "Bash" || got[0]["inputDigest"] == "" {
		t.Fatalf("permission.decided events: %v", got)
	}
	if !has(r.auditActions(t), EventPermissionDecided) {
		t.Fatal("every decision is in the audit log")
	}
}

// Anything else waits for a person on an approval bound to the exact call; approving consumes it once and allows.
func TestPermissionGateWaitsForAPersonAndConsumesTheApproval(t *testing.T) {
	r := newPermRig(t)
	ctx := context.Background()
	req := ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": "rm -rf build", "description": "clean the build folder"}), Root: r.root}
	ch := r.decideAsync(t, req)
	id := r.waitRequested(t)
	info, err := r.apr.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != PermissionTitlePrefix+"Bash: rm -rf build" || info.ActionKind != KindShellDestructive || info.Action.Tool != "claude:Bash" ||
		info.Action.Command != "rm -rf build" || info.Action.TaskID != "task_1" || info.Action.RunID != "arun_1" || info.RunID != "" {
		t.Fatalf("approval: %+v", info)
	}
	// the agent cannot answer its own request
	if err := r.apr.Decide(ctx, id, DecisionApprove, "agent:agent_1"); !errors.Is(err, ErrNotHuman) {
		t.Fatalf("an agent decided: %v", err)
	}
	select {
	case o := <-ch:
		t.Fatalf("answered before a person did: %+v", o)
	case <-time.After(50 * time.Millisecond):
	}
	// an approval of this call never covers a different input
	other := req
	other.Input = input(t, map[string]any{"command": "rm -rf /", "description": "clean the build folder"})
	td, _ := ClassifyToolRequest(ctx, r.gate.Analyzer, nil, other)
	td.Action.Kind = KindShellDestructive
	if err := r.apr.Decide(ctx, id, DecisionApprove, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	o := await(t, ch)
	if !o.Allow || o.Outcome != PermissionApproved || o.ApprovalID != id {
		t.Fatalf("approved: %+v", o)
	}
	if info, _ = r.apr.Get(ctx, id); info.Status != "CONSUMED" {
		t.Fatalf("the approval must be consumed by the answer, got %s", info.Status)
	}
	if err := r.apr.Consume(ctx, id, r.gate.bind(permScope, td.Action)); err == nil {
		t.Fatal("a consumed approval was used again for another input")
	}
	// a second identical request needs a second approval
	ch = r.decideAsync(t, req)
	id2 := r.waitRequested(t)
	if id2 == id {
		t.Fatal("the same approval was offered twice")
	}
	if err := r.apr.Decide(ctx, id2, DecisionReject, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if o := await(t, ch); o.Allow || o.Outcome != PermissionRejected || !strings.Contains(o.Message, "refused") {
		t.Fatalf("rejected: %+v", o)
	}
	got := r.decided(t)
	if len(got) != 2 || got[0]["outcome"] != PermissionApproved || got[0]["approval"] != id || got[1]["outcome"] != PermissionRejected {
		t.Fatalf("events: %v", got)
	}
}

// The approval for one input cannot be consumed by another: the descriptor is recomputed from the call being answered.
func TestPermissionApprovalNeverCoversAnotherInput(t *testing.T) {
	r := newPermRig(t)
	ctx := context.Background()
	a, _ := ClassifyToolRequest(ctx, r.gate.Analyzer, nil, ToolRequest{Tool: "WebFetch", Input: input(t, map[string]any{"url": "https://a.example"}), Root: r.root})
	b, _ := ClassifyToolRequest(ctx, r.gate.Analyzer, nil, ToolRequest{Tool: "WebFetch", Input: input(t, map[string]any{"url": "https://b.example"}), Root: r.root})
	id, err := r.apr.Request(ctx, r.gate.bind(permScope, a.Action), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.apr.Decide(ctx, id, DecisionApprove, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if err := r.apr.Consume(ctx, id, r.gate.bind(permScope, b.Action)); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("another input consumed the approval: %v", err)
	}
	other := permScope
	other.RunID = "arun_2"
	if err := r.apr.Consume(ctx, id, r.gate.bind(other, a.Action)); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("another agent run consumed the approval: %v", err)
	}
}

// Nobody answering in time, and a run that ends while it waits, both refuse the call.
func TestPermissionGateTimeoutAndClosedRunDeny(t *testing.T) {
	r := newPermRig(t)
	req := ToolRequest{Tool: "WebFetch", Input: input(t, map[string]any{"url": "https://example.com"}), Root: r.root}
	ch := r.decideAsync(t, req)
	id := r.waitRequested(t)
	r.clk.Advance(11 * time.Minute)
	if o := await(t, ch); o.Allow || o.Outcome != PermissionTimedOut || !strings.Contains(o.Message, "Nobody answered") {
		t.Fatalf("timeout: %+v", o)
	}
	if info, _ := r.apr.Get(context.Background(), id); info.Status != "EXPIRED" {
		t.Fatalf("status %s", info.Status)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch2 := make(chan PermissionOutcome, 1)
	go func() { ch2 <- r.gate.Decide(ctx, permScope, req) }()
	id = r.waitRequested(t)
	cancel()
	if o := await(t, ch2); o.Allow || o.Outcome != PermissionCancelled {
		t.Fatalf("closed run: %+v", o)
	}
	if info, _ := r.apr.Get(context.Background(), id); info.Status != "CANCELLED" {
		t.Fatalf("the open approval must be withdrawn, got %s", info.Status)
	}
	// a late approval of the withdrawn request authorizes nothing
	if err := r.apr.Decide(context.Background(), id, DecisionApprove, "human:kyle"); err == nil {
		t.Fatal("a withdrawn request was approved")
	}
}

// A secret in the input reaches neither the events nor the audit log, and the stored display copy is redacted.
func TestPermissionRecordsNoSecretValues(t *testing.T) {
	r := newPermRig(t)
	tok := fakeGitHubToken()
	req := ToolRequest{Tool: "Bash", Input: input(t, map[string]any{"command": "curl -H 'Authorization: token " + tok + "' https://api.github.com/user"}), Root: r.root}
	ch := r.decideAsync(t, req)
	id := r.waitRequested(t)
	if err := r.apr.Decide(context.Background(), id, DecisionReject, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	await(t, ch)
	rows, err := r.db.Reader().Query(`SELECT CAST(data AS TEXT) FROM events UNION ALL SELECT title || CAST(payload AS TEXT) FROM approvals UNION ALL SELECT CAST(detail AS TEXT) FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		n++
		if strings.Contains(s, tok) {
			t.Fatalf("the secret was stored: %s", s)
		}
	}
	if n == 0 {
		t.Fatal("nothing was recorded")
	}
}

// A write into the worktree's .git is found however the two paths are spelled: a short 8.3 root against an expanded file
// path (the Windows runner's RUNNER~1), backslashes, another letter case. When the root is not a prefix, any .git component
// counts (fail closed); .github is not .git.
func TestTouchesGitDirWhateverTheSpelling(t *testing.T) {
	for _, c := range []struct {
		root, full string
		want       bool
	}{
		{`C:\Users\runneradmin\wt`, `C:\Users\runneradmin\wt\.git\hooks\pre-commit`, true},
		{`C:\Users\runneradmin\wt`, `c:/users/RUNNERADMIN/wt/.GIT/config`, true},
		{`C:\Users\RUNNER~1\wt`, `C:\Users\runneradmin\wt\.git\hooks\pre-commit`, true},
		{`C:\Users\RUNNER~1\wt`, `C:\Users\runneradmin\wt\src\calc.go`, false},
		{`/home/u/wt`, `/home/u/wt/.github/workflows/ci.yml`, false},
		{`/home/u/wt`, `/home/u/wt/src/.git-notes`, false},
		{`/home/u/wt`, `/home/u/wtx/.git/config`, true},
		{`/home/u/wt`, `/home/u/wt/calc.go`, false},
	} {
		if got := touchesGitDir(c.root, c.full); got != c.want {
			t.Errorf("touchesGitDir(%q, %q) = %v, want %v", c.root, c.full, got, c.want)
		}
	}
}
