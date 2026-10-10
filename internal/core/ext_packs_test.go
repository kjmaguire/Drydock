package core_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/memory"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/requirements"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/skills"
	"github.com/kjmaguire/drydock/internal/tasks"
)

func (a *app) callErr(method string, params any) error {
	a.t.Helper()
	_, err := a.c.Call(context.Background(), method, mustJSON(params))
	return err
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestKnowledgeExtensionIsRegisteredWithItsMethods(t *testing.T) {
	a := open(t, "")
	if a.c.Knowledge() == nil {
		t.Fatal("the knowledge extension did not start")
	}
	methods := a.c.Methods()
	for _, name := range []string{
		"pack.list", "pack.install", "pack.refresh", "pack.search", "pack.consult", "pack.attach", "pack.releaseChunk", "pack.templates",
		"requirement.add", "requirement.search", "requirement.link", "requirement.trace", "milestone.create", "milestone.list",
		"skill.scan", "skill.accept", "skill.import", "skill.registryAdd", "skill.deliveries", "memory.propose", "memory.accept", "memory.delete",
	} {
		if methods[name] == nil {
			t.Errorf("method %s is not registered", name)
		}
	}
	var tpls []struct{ ID string }
	a.call("pack.templates", nil, &tpls)
	if len(tpls) != 2 {
		t.Fatalf("%v", tpls)
	}
	var regs []skills.Registry
	a.call("skill.registries", nil, &regs)
	if len(regs) != 10 {
		t.Fatalf("the curated allowlist is seeded: %d", len(regs))
	}
}

func TestPackRPCInstallRefreshSearchConsultAttachAndAudit(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	docs := filepath.Join(t.TempDir(), "docs")
	os.MkdirAll(docs, 0o755)
	os.WriteFile(filepath.Join(docs, "wrangler.md"), []byte("# Wrangler\n\n## Preview\n\nUse `wrangler versions upload` to preview a Worker.\n"), 0o644)
	yaml := "schema: 1\nid: cf\ntitle: CF docs\nkind: indexed\nvisibility: private\nsources:\n  - {type: local_dir, path: " + `"` + strings.ReplaceAll(docs, `\`, `/`) + `"` + "}\nattach: {instructions: \"Cloudflare notes.\", roles: [Backend]}\n"
	var pk packs.Pack
	a.call("pack.install", map[string]string{"yaml": yaml}, &pk)
	if pk.ID != "cf" || pk.Status != "new" {
		t.Fatalf("%+v", pk)
	}
	if err := a.callErr("pack.install", map[string]string{"yaml": "schema: 1\nid: x\n"}); err == nil {
		t.Fatal("invalid manifest")
	}
	var rr packs.RefreshResult
	a.call("pack.refresh", map[string]any{"id": "cf"}, &rr)
	if rr.Added != 1 || !rr.NewVersion {
		t.Fatalf("%+v", rr)
	}
	var sr packs.SearchResult
	a.call("pack.search", map[string]any{"query": "preview a worker"}, &sr)
	if !sr.Found || sr.Hits[0].Citation.Pack != "cf" {
		t.Fatalf("%+v", sr)
	}
	var cons packs.Consult
	a.call("pack.consult", map[string]any{"packIds": []string{"cf"}, "question": "how do I preview a worker?"}, &cons)
	if !cons.Found || len(cons.Citations) == 0 || cons.Route != packs.RouteRetrievalOnly {
		t.Fatalf("%+v", cons)
	}
	a.call("pack.consult", map[string]any{"question": "kubernetes ingress"}, &cons)
	if cons.Found || !strings.HasPrefix(cons.Answer, "nothing found") {
		t.Fatalf("%+v", cons)
	}
	a.call("pack.attachDefaults", map[string]any{"projectId": p.ID, "packId": "cf"}, nil)
	var atts []packs.Attachment
	a.call("pack.attachments", map[string]any{"projectId": p.ID}, &atts)
	if len(atts) != 1 || atts[0].Role != "Backend" || atts[0].CreatedBy != "human:kyle" {
		t.Fatalf("%+v", atts)
	}
	// the Workspace providers search the attached pack
	ws := a.c.Knowledge().Workspace()
	if ws.Packs == nil || ws.Requirements == nil || ws.Skills == nil || ws.Memory == nil {
		t.Fatalf("workspace providers: %+v", ws)
	}
	// audit log records the security-relevant actions, as the human
	rows, err := security.NewAudit(a.c.DB, a.c.Scanner).Verify(context.Background())
	if err != nil || !rows.OK || rows.Rows < 2 {
		t.Fatalf("audit: %v %+v", err, rows)
	}
	var n int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE actor='human:kyle' AND action IN ('pack.install','pack.attach')`).Scan(&n)
	if n != 2 {
		t.Fatalf("audit rows %d", n)
	}
	a.call("pack.remove", map[string]string{"id": "cf"}, nil)
	var list []packs.Pack
	a.call("pack.list", nil, &list)
	if len(list) != 0 {
		t.Fatal("removed")
	}
}

func TestRequirementsAndMilestonesRPC(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	var r requirements.Requirement
	a.call("requirement.add", map[string]any{"projectId": p.ID, "title": "Calculator PRD", "text": "# Calc\n\n## Subtract\n\nThe calculator shall subtract two integers.\n"}, &r)
	if r.Version != 1 || r.CreatedBy != "human:kyle" {
		t.Fatalf("%+v", r)
	}
	var hits []requirements.Hit
	a.call("requirement.search", map[string]any{"projectId": p.ID, "query": "subtract integers"}, &hits)
	if len(hits) != 1 || !strings.HasPrefix(hits[0].Citation, "REQ:"+r.ID+"@1#") {
		t.Fatalf("%+v", hits)
	}
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Subtract", Acceptance: []string{"Given 5 and 3, When Subtract is called, Then the result is 2"}}, &task)
	var links []requirements.Link
	a.call("requirement.link", map[string]any{"requirementId": r.ID, "taskId": task.ID}, &links)
	if len(links) != 1 {
		t.Fatalf("%+v", links)
	}
	var tr requirements.Trace
	a.call("requirement.trace", map[string]string{"id": r.ID}, &tr)
	if len(tr.Tasks) != 1 || tr.Tasks[0].TaskID != task.ID || tr.Verified {
		t.Fatalf("%+v", tr)
	}
	var upd struct {
		Requirement requirements.Requirement
		Changed     bool
	}
	a.call("requirement.update", map[string]any{"id": r.ID, "text": "# Calc\n\n## Subtract\n\nThe calculator shall subtract two integers and floats.\n"}, &upd)
	if !upd.Changed || upd.Requirement.Version != 2 || upd.Requirement.StaleLinks != 1 {
		t.Fatalf("%+v", upd)
	}
	var stale []requirements.Link
	a.call("requirement.stale", map[string]string{"projectId": p.ID}, &stale)
	if len(stale) != 1 {
		t.Fatalf("%+v", stale)
	}
	var ms requirements.Milestone
	a.call("milestone.create", map[string]any{"projectId": p.ID, "title": "v1", "taskIds": []string{task.ID}}, &ms)
	if len(ms.TaskIDs) != 1 || ms.Progress.Total != 1 {
		t.Fatalf("%+v", ms)
	}
	a.call("milestone.setStatus", map[string]any{"id": ms.ID, "status": "closed", "force": true}, &ms)
	if ms.Status != "closed" {
		t.Fatalf("%+v", ms)
	}
	var list []requirements.Milestone
	a.call("milestone.list", map[string]string{"projectId": p.ID}, &list)
	if len(list) != 1 {
		t.Fatal("list")
	}
	var evs []struct{ Type string }
	a.call("events.replay", map[string]any{"after": 0, "types": []string{"requirement.*", "milestone.*"}}, &evs)
	have := map[string]bool{}
	for _, e := range evs {
		have[e.Type] = true
	}
	for _, w := range []string{"requirement.added", "requirement.changed", "requirement.linked", "milestone.created", "milestone.closed"} {
		if !have[w] {
			t.Errorf("event %s missing", w)
		}
	}
}

func TestMemoryRPCAndHumanOnlyAccept(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	var e memory.Entry
	token := "ghp_1a2B3c4D5e6F7g8H9i0J1k2L3m4N5o6P7q8R"
	a.call("memory.propose", map[string]any{"projectId": p.ID, "kind": "convention", "title": "Use gofmt", "body": "Run gofmt. CI token " + token}, &e)
	if e.Status != memory.Proposed || strings.Contains(e.Body, token) || !strings.Contains(e.Body, "[REDACTED:") {
		t.Fatalf("proposals are never accepted by their author and secrets never reach memory: %+v", e)
	}
	if err := a.callErr("memory.propose", map[string]any{"projectId": p.ID, "kind": "bogus", "title": "x", "body": "y"}); err == nil {
		t.Fatal("kind")
	}
	// proposed entries do not reach agents: not in search for agents, not in generated files
	k := a.c.Knowledge()
	if got, _ := k.Memory.Search(context.Background(), p.ID, "gofmt", 5); len(got) != 0 {
		t.Fatal("agents search accepted memory only")
	}
	// only a human principal can accept
	for _, by := range []string{"agent_1", "tool:tests", "", "integration:notion", "system"} {
		if _, err := k.DecideMemory(context.Background(), by, e.ID, memory.Accepted); err == nil {
			t.Errorf("%q must not be able to accept memory", by)
		}
	}
	var acc memory.Entry
	a.call("memory.accept", map[string]string{"id": e.ID}, &acc)
	if acc.Status != memory.Accepted {
		t.Fatalf("%+v", acc)
	}
	if got, _ := k.Memory.Search(context.Background(), p.ID, "gofmt", 5); len(got) != 1 {
		t.Fatal("accepted memory is searchable")
	}
	if err := a.callErr("memory.reject", map[string]string{"id": e.ID}); err == nil {
		t.Fatal("an accepted entry cannot be rejected, only deleted")
	}
	var found []memory.Entry
	a.call("memory.search", map[string]any{"projectId": p.ID, "query": "gofmt"}, &found)
	if len(found) != 1 {
		t.Fatalf("%+v", found)
	}
	var e2 memory.Entry
	a.call("memory.propose", map[string]any{"projectId": p.ID, "kind": "rejected", "title": "No globals", "body": "Tried global state; abandoned."}, &e2)
	a.call("memory.reject", map[string]string{"id": e2.ID}, &e2)
	if e2.Status != memory.RejectedSt {
		t.Fatalf("%+v", e2)
	}
	var list []memory.Entry
	a.call("memory.list", map[string]any{"projectId": p.ID}, &list)
	if len(list) != 2 {
		t.Fatalf("%+v", list)
	}
	a.call("memory.delete", map[string]string{"id": e.ID}, nil)
	a.call("memory.list", map[string]any{"projectId": p.ID}, &list)
	if len(list) != 1 {
		t.Fatalf("%+v", list)
	}
	if got, _ := k.Memory.Search(context.Background(), p.ID, "gofmt", 5); len(got) != 0 {
		t.Fatal("deleted memory leaves the index")
	}
	var evs []struct{ Type string }
	a.call("events.replay", map[string]any{"after": 0, "types": []string{"memory.*"}}, &evs)
	if len(evs) != 5 { // proposed x2, accepted, rejected, deleted
		t.Fatalf("%+v", evs)
	}
}

// The whole thing inside the real product loop: attached pack instructions land in AGENTS.md, an accepted skill is written as a
// path-scoped Claude rule into the task worktree and kept out of the diff, and the delivery is recorded.
func TestSkillsAndPackInstructionsReachTheWorktreeInTheRealLoop(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	os.MkdirAll(filepath.Join(repo, ".aet", "skills", "go-style"), 0o755)
	os.WriteFile(filepath.Join(repo, ".aet", "skills", "go-style", "SKILL.md"), []byte("---\nname: go-style\ndescription: Go style\nmetadata:\n  drydock.globs: \"**/*.go\"\n  drydock.roles: \"Backend\"\n---\nKeep functions small.\n"), 0o644)
	os.MkdirAll(filepath.Join(repo, ".aet", "skills", "frontend-only"), 0o755)
	os.WriteFile(filepath.Join(repo, ".aet", "skills", "frontend-only", "SKILL.md"), []byte("---\nname: frontend-only\ndescription: UI\nmetadata:\n  drydock.roles: \"Frontend\"\n---\nUI rule.\n"), 0o644)
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "skills")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	var res skills.ScanResult
	a.call("skill.scan", map[string]string{"projectId": p.ID}, &res)
	// repository skills are offered, never accepted automatically (decision P11): the human accepts each one
	if res.Offered != 2 || res.Accepted != 0 {
		t.Fatalf("%+v", res)
	}
	for _, name := range []string{"go-style", "frontend-only"} {
		a.call("skill.accept", map[string]string{"projectId": p.ID, "name": name}, nil)
	}
	docs := filepath.Join(t.TempDir(), "docs")
	os.MkdirAll(docs, 0o755)
	os.WriteFile(filepath.Join(docs, "a.md"), []byte("# Go\n\nGo docs.\n"), 0o644)
	a.call("pack.install", map[string]string{"yaml": "schema: 1\nid: godocs\ntitle: Go docs\nkind: indexed\nvisibility: private\nsources:\n  - {type: local_dir, path: \"" + strings.ReplaceAll(docs, `\`, `/`) + "\"}\nattach: {instructions: \"Go standard library notes.\"}\n"}, nil)
	a.call("pack.refresh", map[string]string{"id": "godocs"}, nil)
	a.call("pack.attach", map[string]string{"projectId": p.ID, "packId": "godocs", "role": "backend"}, nil)

	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	// go-style goes to the reviewer only (chosen agents): the worker, whose role it names, no longer receives it
	var assigned skills.Skill
	a.call("skill.audience", map[string]any{"projectId": p.ID, "name": "go-style", "audience": skills.AudienceAgents, "agents": []string{reviewer.ID}}, &assigned)
	if strings.Join(assigned.Agents, ",") != reviewer.ID || assigned.Audience != skills.AudienceAgents {
		t.Fatalf("%+v", assigned)
	}
	var listed []skills.Skill
	a.call("skill.list", map[string]string{"projectId": p.ID}, &listed)
	if len(listed) != 2 || strings.Join(listed[1].Agents, ",") != reviewer.ID || listed[0].Agents == nil || len(listed[0].Agents) != 0 {
		t.Fatalf("skill.list carries the agents: %+v", listed)
	}
	var plan skills.Plan
	a.call("skill.plan", skills.Target{ProjectID: p.ID, AgentID: worker.ID, Role: "backend", Adapter: "claude"}, &plan)
	if len(plan.Files) != 0 {
		t.Fatalf("the worker still receives a skill assigned to the reviewer: %+v", plan.Files)
	}
	// and then to the worker as well, which still gets it; frontend-only goes to the worker by assignment despite its role
	a.call("skill.assign", map[string]any{"projectId": p.ID, "name": "go-style", "agentId": worker.ID, "on": true}, &assigned)
	a.call("skill.assign", map[string]any{"projectId": p.ID, "name": "frontend-only", "agentId": worker.ID, "on": true}, &assigned)
	if err := a.callErr("skill.assign", map[string]any{"projectId": p.ID, "name": "nope", "agentId": worker.ID, "on": true}); err == nil {
		t.Fatal("an unknown skill was assigned")
	}
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})
	wt := detail.Task.Worktree
	rule, err := os.ReadFile(filepath.Join(wt, ".claude", "rules", "go-style.md"))
	if err != nil || !strings.HasPrefix(string(rule), "---\npaths:\n") || !strings.Contains(string(rule), "**/*.go") || !strings.Contains(string(rule), "Keep functions small.") {
		t.Fatalf("path-scoped rule: %v\n%s", err, rule)
	}
	if _, err := os.Stat(filepath.Join(wt, ".claude", "rules", "frontend-only.md")); err != nil {
		t.Fatal("a skill assigned to the worker is delivered whatever its roles say")
	}
	agents, _ := os.ReadFile(filepath.Join(wt, "AGENTS.md"))
	if !strings.Contains(string(agents), "## Expert packs") || !strings.Contains(string(agents), "Go standard library notes.") || !strings.Contains(string(agents), `search_pack with pack_ids ["godocs"]`) {
		t.Fatalf("pack instructions missing from AGENTS.md:\n%s", agents)
	}
	if strings.Contains(string(agents), "Go docs.") {
		t.Fatal("crawled text never goes into instruction files")
	}
	var diff string
	a.call("task.diff", map[string]string{"id": task.ID}, &diff)
	if strings.Contains(diff, ".claude/rules") || strings.Contains(diff, "AGENTS.md") {
		t.Fatalf("generated files must stay out of the diff:\n%s", diff)
	}
	var ds []skills.Delivery
	a.call("skill.deliveries", map[string]string{"projectId": p.ID, "taskId": task.ID}, &ds)
	if len(ds) != 2 || ds[0].Skill == ds[1].Skill || ds[0].Method != skills.MethodClaudeRules || ds[0].Fidelity != skills.FidelityExact || ds[0].AgentID != worker.ID || ds[1].AgentID != worker.ID {
		t.Fatalf("%+v", ds)
	}
	var evs []struct{ Type string }
	a.call("events.replay", map[string]any{"after": 0, "types": []string{"skill.delivered", "skill.resolved"}, "taskId": ""}, &evs)
	if len(evs) < 2 {
		t.Fatalf("%+v", evs)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": detail.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Completed
	})
}

// With skill delivery "relevant", a Claude worker on a task without file hints gets no skill in its instructions: AGENTS.md
// lists each one and its text is in .claude/drydock-skills, which stays out of the diff like the other generated files.
func TestRelevantSkillDeliveryListsSkillsAndKeepsTheirTextOutOfTheTurn(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	os.MkdirAll(filepath.Join(repo, ".aet", "skills", "go-style"), 0o755)
	os.WriteFile(filepath.Join(repo, ".aet", "skills", "go-style", "SKILL.md"), []byte("---\nname: go-style\ndescription: How Go code is written here\n---\nKeep functions small.\n"), 0o644)
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "skills")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	a.call("skill.scan", map[string]string{"projectId": p.ID}, nil)
	a.call("skill.accept", map[string]string{"projectId": p.ID, "name": "go-style"}, nil)
	var s map[string]any
	a.call("projectdev.get", map[string]any{"projectId": p.ID}, &s)
	s["skillDelivery"] = "sometimes"
	if err := a.callErr("projectdev.set", map[string]any{"projectId": p.ID, "settings": s}); err == nil {
		t.Fatal("an unknown skill delivery was saved")
	}
	s["skillDelivery"] = skills.DeliveryRelevant
	a.call("projectdev.set", map[string]any{"projectId": p.ID, "settings": s}, nil)

	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})
	wt := detail.Task.Worktree
	if rules, _ := filepath.Glob(filepath.Join(wt, ".claude", "rules", "*.md")); len(rules) != 0 {
		t.Fatalf("no skill is loaded into the turn: %v", rules)
	}
	body, err := os.ReadFile(filepath.Join(wt, ".claude", "drydock-skills", "go-style.md"))
	if err != nil || !strings.Contains(string(body), "Keep functions small.") {
		t.Fatalf("%v\n%s", err, body)
	}
	agents, _ := os.ReadFile(filepath.Join(wt, "AGENTS.md"))
	if !strings.Contains(string(agents), "**go-style** (`.claude/drydock-skills/go-style.md`): How Go code is written here") || strings.Contains(string(agents), "Keep functions small.") {
		t.Fatalf("AGENTS.md lists the skill without its text:\n%s", agents)
	}
	var diff string
	a.call("task.diff", map[string]string{"id": task.ID}, &diff)
	if strings.Contains(diff, "drydock-skills") || strings.Contains(diff, "AGENTS.md") {
		t.Fatalf("generated files must stay out of the diff:\n%s", diff)
	}
	items := manifestItems(t, a, task.ID)
	if it := items["file:.claude/drydock-skills/go-style.md"]; it.Included || it.Bytes == 0 || !strings.Contains(it.Reason, "reads it with Read") {
		t.Fatalf("%+v", it)
	}
	if it := items["budget:instructions"]; !strings.Contains(it.Reason, "within budget") {
		t.Fatalf("%+v", it)
	}
	var ds []skills.Delivery
	a.call("skill.deliveries", map[string]string{"projectId": p.ID, "taskId": task.ID}, &ds)
	if len(ds) != 1 || ds[0].Method != skills.MethodClaudeOnDemand || ds[0].Fidelity != skills.FidelityOnDemand {
		t.Fatalf("%+v", ds)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": detail.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Completed
	})
}

// manifestItems returns the context manifest items of a task's worker turns, by source.
func manifestItems(t *testing.T, a *app, taskID string) map[string]ctxeng.Item {
	t.Helper()
	out := map[string]ctxeng.Item{}
	rows, err := a.c.DB.Reader().Query(`SELECT items FROM context_manifests WHERE task_id=?`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var items string
		rows.Scan(&items)
		var its []ctxeng.Item
		json.Unmarshal([]byte(items), &its)
		for _, it := range its {
			out[it.Source] = it
		}
	}
	return out
}

// A turn's manifest records the skills Claude gets in every turn and how much that is, and says so when it is over budget.
func TestTheManifestSaysWhenEveryTurnCarriesTooMuch(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	big := strings.Repeat("A long vendor skill line about a product this task never touches.\n", 600) // about 40 KB
	os.MkdirAll(filepath.Join(repo, ".aet", "skills", "vendor-guide"), 0o755)
	os.WriteFile(filepath.Join(repo, ".aet", "skills", "vendor-guide", "SKILL.md"), []byte("---\nname: vendor-guide\ndescription: A vendor's guide\n---\n"+big), 0o644)
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "skills")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	a.call("skill.scan", map[string]string{"projectId": p.ID}, nil)
	a.call("skill.accept", map[string]string{"projectId": p.ID, "name": "vendor-guide"}, nil)
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})
	items := manifestItems(t, a, task.ID)
	rule, budget := items["file:.claude/rules/vendor-guide.md"], items["budget:instructions"]
	if !rule.Included || rule.Bytes < 35000 {
		t.Fatalf("the rule file: %+v (all: %+v)", rule, items)
	}
	if budget.Bytes < rule.Bytes || !strings.Contains(budget.Reason, "OVER BUDGET") || !strings.Contains(budget.Reason, "skill delivery to relevant") {
		t.Fatalf("%+v", budget)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": detail.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Completed
	})
}
