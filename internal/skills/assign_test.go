package skills

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/mcp"
)

func (e *env) addAgent(id, role string) {
	e.t.Helper()
	if _, err := e.db.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES(?,?,?,?,?,1)`, id, "p1", id, "claude", role); err != nil {
		e.t.Fatal(err)
	}
}

func planned(t *testing.T, e *env, agentID, role, adapter string, hints ...string) []string {
	t.Helper()
	plan, err := e.s.Plan(context.Background(), Target{ProjectID: "p1", AgentID: agentID, Role: role, Adapter: adapter, Hints: hints})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range plan.Files {
		out = append(out, f.Skills...)
	}
	out = append(out, plan.OnDemand...)
	sort.Strings(out)
	return out
}

// A skill assigned to agents reaches exactly those agents (globs still apply); a skill assigned to nobody keeps role delivery.
func TestAssignedSkillReachesOnlyItsAgents(t *testing.T) {
	ctx := context.Background()
	e := acceptedEnv(t)
	e.addAgent("agt_a", "Backend")
	e.addAgent("agt_b", "Backend")
	e.addAgent("agt_q", "QA")
	if _, err := e.db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p2','other','/nowhere',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES('agt_x','p2','x','claude','Backend',1)`); err != nil {
		t.Fatal(err)
	}

	before := planned(t, e, "agt_a", "Backend", "claude")
	if strings.Join(before, ",") != "api-style,d1-migrations,everywhere" {
		t.Fatalf("role delivery before any assignment: %v", before)
	}

	// refused: not human, unknown skill, an agent of another project
	if _, err := e.s.Assign(ctx, "agent:agt_a", "p1", "everywhere", "agt_a", true); !errors.Is(err, ErrNotHuman) {
		t.Fatalf("an agent assigned a skill: %v", err)
	}
	if _, err := e.s.Assign(ctx, human, "p1", "nope", "agt_a", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := e.s.Assign(ctx, human, "p1", "everywhere", "agt_x", true); err == nil {
		t.Fatal("an agent of another project was assigned")
	}

	// "everywhere" goes to agt_a only; the QA skill is assigned to a Backend agent, overriding its drydock.roles
	k, err := e.s.Assign(ctx, human, "p1", "everywhere", "agt_a", true)
	if err != nil || strings.Join(k.Agents, ",") != "agt_a" {
		t.Fatalf("%v %+v", err, k)
	}
	if _, err := e.s.Assign(ctx, human, "p1", "test-names", "agt_b", true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Assign(ctx, human, "p1", "everywhere", "agt_a", true); err != nil { // idempotent
		t.Fatal(err)
	}
	if got := planned(t, e, "agt_a", "Backend", "claude"); strings.Join(got, ",") != "api-style,d1-migrations,everywhere" {
		t.Fatalf("agt_a: %v", got)
	}
	if got := planned(t, e, "agt_b", "Backend", "claude"); strings.Join(got, ",") != "api-style,d1-migrations,test-names" {
		t.Fatalf("agt_b gets its assigned skill and not agt_a's: %v", got)
	}
	if got := planned(t, e, "agt_q", "QA", "claude"); strings.Join(got, ",") != "api-style" {
		t.Fatalf("the QA agent lost test-names to its assignment and everywhere to agt_a: %v", got)
	}
	if got := planned(t, e, "", "Backend", "claude"); strings.Join(got, ",") != "api-style,d1-migrations" {
		t.Fatalf("no agent known: only unassigned skills: %v", got)
	}
	// globs still apply to an assigned skill on a CLI with run-start selection
	if got := planned(t, e, "agt_b", "Backend", "codex", "src/app.test.ts"); !strings.Contains(strings.Join(got, ","), "test-names") {
		t.Fatalf("%v", got)
	}

	// the skills tool follows the same rule
	p := &Provider{S: e.s}
	list, err := p.List(ctx, mcp.RunInfo{ProjectID: "p1", AgentID: "agt_q", Role: "QA"})
	if err != nil || len(list) != 1 || list[0].Name != "api-style" {
		t.Fatalf("%v %+v", err, list)
	}
	if _, err := p.Get(ctx, mcp.RunInfo{ProjectID: "p1", AgentID: "agt_b", Role: "Backend"}, "everywhere"); err == nil {
		t.Fatal("the tool served another agent's skill")
	}

	// list and get show the agents; AgentSkills lists one agent's
	all, _ := e.s.List(ctx, "p1")
	byName := map[string]Skill{}
	for _, s := range all {
		byName[s.Name] = s
	}
	if strings.Join(byName["everywhere"].Agents, ",") != "agt_a" || len(byName["api-style"].Agents) != 0 || byName["api-style"].Agents == nil {
		t.Fatalf("%+v", all)
	}
	mine, _ := e.s.AgentSkills(ctx, "p1", "agt_b")
	if len(mine) != 1 || mine[0].Name != "test-names" || mine[0].Body != "" {
		t.Fatalf("%+v", mine)
	}
	if len(e.events(EventAssigned)) != 3 {
		t.Fatalf("assigned events: %d", len(e.events(EventAssigned)))
	}

	// taking the last assignment away restores role delivery
	if _, err := e.s.Assign(ctx, human, "p1", "test-names", "agt_b", false); err != nil {
		t.Fatal(err)
	}
	if got := planned(t, e, "agt_q", "QA", "claude"); strings.Join(got, ",") != "api-style,test-names" {
		t.Fatalf("%v", got)
	}
	if len(e.events(EventUnassigned)) != 1 {
		t.Fatal("unassigned event")
	}

	// an assignment never makes a skill deliverable: a rejected assigned skill reaches nobody
	if _, err := e.s.Reject(ctx, human, "p1", "everywhere"); err != nil {
		t.Fatal(err)
	}
	if got := planned(t, e, "agt_a", "Backend", "claude"); strings.Contains(strings.Join(got, ","), "everywhere") {
		t.Fatalf("a rejected skill was delivered: %v", got)
	}

	// a repository skill that disappears takes its assignments with it
	e.gitRun("rm", "-rq", ".aet/skills/everywhere")
	e.gitRun("commit", "-qm", "rm")
	if _, err := e.s.ScanRepo(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if as, _ := e.s.Assignments(ctx, "p1"); len(as["everywhere"]) != 0 {
		t.Fatalf("%v", as)
	}
}
