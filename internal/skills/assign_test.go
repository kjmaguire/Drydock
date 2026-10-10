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
	// a skill reaches the agent through a file, the tool, or both (Claude's on-demand files are both): count it once
	seen := map[string]bool{}
	var out []string
	for _, f := range plan.Files {
		for _, n := range f.Skills {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	for _, n := range plan.OnDemand {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Giving a whole-team skill to one more agent adds that agent and takes the skill from nobody (before, the first assignment
// narrowed the skill to the assigned agents, so giving a QA skill to a backend agent took it away from QA).
func TestGivingATeamSkillToOneMoreAgentTakesItFromNobody(t *testing.T) {
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

	if got := planned(t, e, "agt_a", "Backend", "claude"); strings.Join(got, ",") != "api-style,d1-migrations,everywhere" {
		t.Fatalf("role delivery before any assignment: %v", got)
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

	// the QA skill goes to backend agent agt_b as well; QA keeps it, agt_a does not get it
	k, err := e.s.Assign(ctx, human, "p1", "test-names", "agt_b", true)
	if err != nil || strings.Join(k.Agents, ",") != "agt_b" || k.Audience != AudienceTeam {
		t.Fatalf("%v %+v", err, k)
	}
	if _, err := e.s.Assign(ctx, human, "p1", "test-names", "agt_b", true); err != nil { // idempotent
		t.Fatal(err)
	}
	// giving a skill everyone has to one agent changes nothing for the others
	if _, err := e.s.Assign(ctx, human, "p1", "everywhere", "agt_a", true); err != nil {
		t.Fatal(err)
	}
	if got := planned(t, e, "agt_b", "Backend", "claude"); strings.Join(got, ",") != "api-style,d1-migrations,everywhere,test-names" {
		t.Fatalf("agt_b: %v", got)
	}
	if got := planned(t, e, "agt_q", "QA", "claude"); strings.Join(got, ",") != "api-style,everywhere,test-names" {
		t.Fatalf("the QA agent lost a skill to an assignment: %v", got)
	}
	if got := planned(t, e, "agt_a", "Backend", "claude"); strings.Join(got, ",") != "api-style,d1-migrations,everywhere" {
		t.Fatalf("agt_a: %v", got)
	}
	if got := planned(t, e, "", "QA", "claude"); strings.Join(got, ",") != "api-style,everywhere,test-names" {
		t.Fatalf("no agent known: what the role gets: %v", got)
	}

	// the skills tool follows the same rule
	p := &Provider{S: e.s}
	list, err := p.List(ctx, mcp.RunInfo{ProjectID: "p1", AgentID: "agt_b", Role: "Backend"})
	if err != nil || len(list) != 4 {
		t.Fatalf("%v %+v", err, list)
	}
	if _, err := p.Get(ctx, mcp.RunInfo{ProjectID: "p1", AgentID: "agt_a", Role: "Backend"}, "test-names"); err == nil {
		t.Fatal("the tool served a skill agt_a was not given")
	}

	// list and get show the audience and the agents; AgentSkills lists one agent's
	all, _ := e.s.List(ctx, "p1")
	byName := map[string]Skill{}
	for _, s := range all {
		byName[s.Name] = s
	}
	if strings.Join(byName["test-names"].Agents, ",") != "agt_b" || len(byName["api-style"].Agents) != 0 || byName["api-style"].Agents == nil ||
		byName["api-style"].Audience != AudienceTeam {
		t.Fatalf("%+v", all)
	}
	mine, _ := e.s.AgentSkills(ctx, "p1", "agt_b")
	if len(mine) != 1 || mine[0].Name != "test-names" || mine[0].Body != "" {
		t.Fatalf("%+v", mine)
	}
	if len(e.events(EventAssigned)) != 3 {
		t.Fatalf("assigned events: %d", len(e.events(EventAssigned)))
	}

	// taking it back from agt_b leaves the role delivery as it was
	if _, err := e.s.Assign(ctx, human, "p1", "test-names", "agt_b", false); err != nil {
		t.Fatal(err)
	}
	if got := planned(t, e, "agt_b", "Backend", "claude"); strings.Contains(strings.Join(got, ","), "test-names") {
		t.Fatalf("%v", got)
	}
	if got := planned(t, e, "agt_q", "QA", "claude"); strings.Join(got, ",") != "api-style,everywhere,test-names" {
		t.Fatalf("%v", got)
	}
	if len(e.events(EventUnassigned)) != 1 {
		t.Fatal("unassigned event")
	}
}

// A skill for chosen agents reaches exactly those agents (globs still apply), and taking it from its last agent leaves it with
// nobody rather than giving it back to the whole team.
func TestASkillForChosenAgentsReachesOnlyThem(t *testing.T) {
	ctx := context.Background()
	e := acceptedEnv(t)
	e.addAgent("agt_a", "Backend")
	e.addAgent("agt_b", "Backend")
	e.addAgent("agt_q", "QA")
	e.addAgent("agt_gone", "Backend")
	if _, err := e.db.Writer().Exec(`UPDATE agents SET archived_at=5 WHERE id='agt_gone'`); err != nil {
		t.Fatal(err)
	}

	// refused: not human, an unknown audience, an unknown skill, an agent off the team
	if _, err := e.s.SetAudience(ctx, "agent:agt_a", "p1", "everywhere", AudienceAgents, []string{"agt_a"}); !errors.Is(err, ErrNotHuman) {
		t.Fatalf("%v", err)
	}
	if _, err := e.s.SetAudience(ctx, human, "p1", "everywhere", "everyone", nil); err == nil {
		t.Fatal("an unknown audience was accepted")
	}
	if _, err := e.s.SetAudience(ctx, human, "p1", "nope", AudienceAgents, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := e.s.SetAudience(ctx, human, "p1", "everywhere", AudienceAgents, []string{"agt_a", "agt_nobody"}); err == nil {
		t.Fatal("an agent that is not on the team was given a skill")
	}
	if k, _ := e.s.Get(ctx, "p1", "everywhere"); k.Audience != AudienceTeam || len(k.Agents) != 0 {
		t.Fatalf("a refused change was half applied: %+v", k)
	}
	// an agent removed from the team is left out of the list (a stale list does not fail), and Assign refuses it outright
	if _, err := e.s.Assign(ctx, human, "p1", "everywhere", "agt_gone", true); err == nil {
		t.Fatal("a removed agent was given a skill")
	}
	k, err := e.s.SetAudience(ctx, human, "p1", "everywhere", AudienceAgents, []string{" agt_a ", "agt_a", "", "agt_gone"})
	if err != nil || k.Audience != AudienceAgents || strings.Join(k.Agents, ",") != "agt_a" {
		t.Fatalf("%v %+v", err, k)
	}
	if got := planned(t, e, "agt_a", "Backend", "claude"); strings.Join(got, ",") != "api-style,d1-migrations,everywhere" {
		t.Fatalf("agt_a: %v", got)
	}
	for _, x := range []struct{ id, role string }{{"agt_b", "Backend"}, {"agt_q", "QA"}, {"", "Backend"}} {
		if got := planned(t, e, x.id, x.role, "claude"); strings.Contains(strings.Join(got, ","), "everywhere") {
			t.Fatalf("%q got a skill for chosen agents: %v", x.id, got)
		}
	}
	// a skill for chosen agents ignores its roles: the QA skill goes to agt_b only
	if _, err := e.s.SetAudience(ctx, human, "p1", "test-names", AudienceAgents, []string{"agt_b"}); err != nil {
		t.Fatal(err)
	}
	if got := planned(t, e, "agt_q", "QA", "claude"); strings.Join(got, ",") != "api-style" {
		t.Fatalf("QA: %v", got)
	}
	// globs still apply to a chosen-agent skill on a CLI with run-start selection
	if got := planned(t, e, "agt_b", "Backend", "codex", "src/app.test.ts"); !strings.Contains(strings.Join(got, ","), "test-names") {
		t.Fatalf("%v", got)
	}
	if ev := e.events(EventAudience); len(ev) != 2 {
		t.Fatalf("audience events: %d", len(ev))
	}

	// taking it from its last agent leaves it with nobody
	if _, err := e.s.Assign(ctx, human, "p1", "everywhere", "agt_a", false); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ id, role string }{{"agt_a", "Backend"}, {"agt_q", "QA"}} {
		if got := planned(t, e, x.id, x.role, "claude"); strings.Contains(strings.Join(got, ","), "everywhere") {
			t.Fatalf("%s: a skill with no agents left was delivered: %v", x.id, got)
		}
	}
	// back to the whole team, which keeps the agents given: every role again, plus agt_b
	k, err = e.s.SetAudience(ctx, human, "p1", "test-names", AudienceTeam, []string{"agt_b"})
	if err != nil || k.Audience != AudienceTeam || strings.Join(k.Agents, ",") != "agt_b" {
		t.Fatalf("%v %+v", err, k)
	}
	if got := planned(t, e, "agt_q", "QA", "claude"); strings.Join(got, ",") != "api-style,test-names" {
		t.Fatalf("QA: %v", got)
	}
	// replacing the list drops the agents left out
	if k, err = e.s.SetAudience(ctx, human, "p1", "test-names", AudienceTeam, nil); err != nil || len(k.Agents) != 0 {
		t.Fatalf("%v %+v", err, k)
	}
	if got := planned(t, e, "agt_b", "Backend", "claude"); strings.Contains(strings.Join(got, ","), "test-names") {
		t.Fatalf("%v", got)
	}

	// an agent being given a skill never makes it deliverable: a rejected skill reaches nobody
	if _, err := e.s.SetAudience(ctx, human, "p1", "everywhere", AudienceAgents, []string{"agt_a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Reject(ctx, human, "p1", "everywhere"); err != nil {
		t.Fatal(err)
	}
	if got := planned(t, e, "agt_a", "Backend", "claude"); strings.Contains(strings.Join(got, ","), "everywhere") {
		t.Fatalf("a rejected skill was delivered: %v", got)
	}
	// a re-scan keeps the audience a human set
	if _, err := e.s.ScanRepo(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if k, _ := e.s.Get(ctx, "p1", "everywhere"); k.Audience != AudienceAgents || strings.Join(k.Agents, ",") != "agt_a" {
		t.Fatalf("%+v", k)
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
