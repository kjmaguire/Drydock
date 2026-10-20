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

// Skills are given to agents, not to the team (decision P76: each agent is a specialist). A new skill starts with the agents
// whose role it names, an agent added later gets nothing by its role, giving a skill to one more agent takes it from nobody,
// and the whole team cannot be chosen.
func TestSkillsAreGivenToAgentsNotToTheTeam(t *testing.T) {
	ctx := context.Background()
	e := acceptedEnv(t) // agt_backend and agt_qa were on the team at the scan; api-style and everywhere were given to agt_backend
	e.addAgent("agt_b", "Backend")
	if _, err := e.db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p2','other','/nowhere',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES('agt_x','p2','x','claude','Backend',1)`); err != nil {
		t.Fatal(err)
	}
	all, _ := e.s.List(ctx, "p1")
	for _, k := range all {
		if k.Audience != AudienceAgents {
			t.Fatalf("a new skill was made for the whole team: %+v", k)
		}
	}
	if got := planned(t, e, "agt_backend", "Backend", "claude"); strings.Join(got, ",") != "api-style,d1-migrations,everywhere" {
		t.Fatalf("the backend agent: %v", got)
	}
	if got := planned(t, e, "agt_qa", "QA", "claude"); strings.Join(got, ",") != "test-names" {
		t.Fatalf("the QA agent gets the skill naming its role only: %v", got)
	}
	if got := planned(t, e, "agt_b", "Backend", "claude"); len(got) != 0 {
		t.Fatalf("an agent added later got skills by its role: %v", got)
	}
	if got := planned(t, e, "", "Backend", "claude"); len(got) != 0 {
		t.Fatalf("no agent known: nothing, whatever the role: %v", got)
	}

	// refused: not human, unknown skill, an agent of another project, the whole team
	if _, err := e.s.Assign(ctx, "agent:agt_b", "p1", "everywhere", "agt_b", true); !errors.Is(err, ErrNotHuman) {
		t.Fatalf("an agent assigned a skill: %v", err)
	}
	if _, err := e.s.Assign(ctx, human, "p1", "nope", "agt_b", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := e.s.Assign(ctx, human, "p1", "everywhere", "agt_x", true); err == nil {
		t.Fatal("an agent of another project was assigned")
	}
	if _, err := e.s.SetAudience(ctx, human, "p1", "everywhere", AudienceTeam, nil); err == nil || !strings.Contains(err.Error(), "specialist") {
		t.Fatalf("the whole team was accepted: %v", err)
	}

	// the QA skill goes to backend agent agt_b as well; QA keeps it, agt_backend does not get it
	k, err := e.s.Assign(ctx, human, "p1", "test-names", "agt_b", true)
	if err != nil || strings.Join(k.Agents, ",") != "agt_b,agt_qa" || k.Audience != AudienceAgents {
		t.Fatalf("%v %+v", err, k)
	}
	if _, err := e.s.Assign(ctx, human, "p1", "test-names", "agt_b", true); err != nil { // idempotent
		t.Fatal(err)
	}
	if got := planned(t, e, "agt_b", "Backend", "claude"); strings.Join(got, ",") != "test-names" {
		t.Fatalf("agt_b: %v", got)
	}
	if got := planned(t, e, "agt_qa", "QA", "claude"); strings.Join(got, ",") != "test-names" {
		t.Fatalf("the QA agent lost a skill to an assignment: %v", got)
	}

	// the skills tool follows the same rule
	p := &Provider{S: e.s}
	list, err := p.List(ctx, mcp.RunInfo{ProjectID: "p1", AgentID: "agt_backend", Role: "Backend"})
	if err != nil || len(list) != 3 {
		t.Fatalf("%v %+v", err, list)
	}
	if _, err := p.Get(ctx, mcp.RunInfo{ProjectID: "p1", AgentID: "agt_backend", Role: "Backend"}, "test-names"); err == nil {
		t.Fatal("the tool served a skill agt_backend was not given")
	}

	// AgentSkills lists one agent's
	mine, _ := e.s.AgentSkills(ctx, "p1", "agt_b")
	if len(mine) != 1 || mine[0].Name != "test-names" || mine[0].Body != "" {
		t.Fatalf("%+v", mine)
	}
	if n := len(e.events(EventAssigned)); n != 4 { // the fixture's two, and agt_b's twice (the second changes nothing but is recorded)
		t.Fatalf("assigned events: %d", n)
	}

	// taking it back from agt_b leaves the QA agent's as it was
	if _, err := e.s.Assign(ctx, human, "p1", "test-names", "agt_b", false); err != nil {
		t.Fatal(err)
	}
	if got := planned(t, e, "agt_b", "Backend", "claude"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := planned(t, e, "agt_qa", "QA", "claude"); strings.Join(got, ",") != "test-names" {
		t.Fatalf("%v", got)
	}
	if len(e.events(EventUnassigned)) != 1 {
		t.Fatal("unassigned event")
	}
}

// Whole-team skills made before P76 (or brought by an older project archive) become skills for the agents they reached: the
// agents their roles took in (not one removed from the team) and the ones they were given to. Nobody loses a skill, and a
// second pass changes nothing.
func TestTeamSkillsFromBeforeP76BecomeSkillsForTheAgentsTheyReached(t *testing.T) {
	ctx := context.Background()
	e := acceptedEnv(t)
	e.addAgent("agt_b", "Backend")
	e.addAgent("agt_db", "Database")
	e.addAgent("agt_gone", "QA")
	// what an older version left: every skill for the whole team, only one given to an agent of its own
	for _, q := range []string{
		`UPDATE agents SET archived_at=5 WHERE id='agt_gone'`,
		`UPDATE skills SET audience='team'`,
		`DELETE FROM agent_skills`,
		`INSERT INTO agent_skills(project_id,agent_id,skill_name,assigned_by,assigned_at) VALUES('p1','agt_qa','api-style','human:kyle',1)`,
	} {
		if _, err := e.db.Writer().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	before := map[string]string{}
	for _, a := range [][2]string{{"agt_backend", "Backend"}, {"agt_b", "Backend"}, {"agt_db", "Database"}, {"agt_qa", "QA"}} {
		before[a[0]] = strings.Join(planned(t, e, a[0], a[1], "claude"), ",")
	}
	if before["agt_db"] != "api-style,d1-migrations,everywhere" || before["agt_qa"] != "api-style,everywhere,test-names" {
		t.Fatalf("the old whole-team delivery: %v", before)
	}
	n, err := e.s.GiveTeamSkillsToAgents(ctx)
	if err != nil || n != 4 {
		t.Fatalf("%v %d", err, n)
	}
	for a, want := range before {
		role := map[string]string{"agt_backend": "Backend", "agt_b": "Backend", "agt_db": "Database", "agt_qa": "QA"}[a]
		if got := strings.Join(planned(t, e, a, role, "claude"), ","); got != want {
			t.Fatalf("%s lost or gained a skill in the change: %s, had %s", a, got, want)
		}
	}
	all, _ := e.s.List(ctx, "p1")
	for _, k := range all {
		if k.Audience != AudienceAgents || contains(k.Agents, "agt_gone") {
			t.Fatalf("%+v", k)
		}
	}
	if k, _ := e.s.Get(ctx, "p1", "test-names"); strings.Join(k.Agents, ",") != "agt_qa" {
		t.Fatalf("the QA skill: %+v", k.Agents)
	}
	// an agent added afterwards gets nothing by its role
	e.addAgent("agt_late", "Backend")
	if got := planned(t, e, "agt_late", "Backend", "claude"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if n, err := e.s.GiveTeamSkillsToAgents(ctx); err != nil || n != 0 {
		t.Fatalf("a second pass changed something: %v %d", err, n)
	}
	if ev := e.events(EventAudience); len(ev) != 4 {
		t.Fatalf("audience events: %d", len(ev))
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
	if k, _ := e.s.Get(ctx, "p1", "everywhere"); k.Audience != AudienceAgents || strings.Join(k.Agents, ",") != "agt_backend" {
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
	if got := planned(t, e, "agt_a", "Backend", "claude"); strings.Join(got, ",") != "everywhere" {
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
	if got := planned(t, e, "agt_q", "QA", "claude"); len(got) != 0 {
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
	// replacing the list drops the agents left out
	if k, err = e.s.SetAudience(ctx, human, "p1", "test-names", AudienceAgents, nil); err != nil || len(k.Agents) != 0 {
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
