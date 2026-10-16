package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/skills"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// recommendScript makes the labelled stand-in agent (a test double) answer a recommendation turn with answer.
func recommendScript(t *testing.T, answer string) {
	t.Helper()
	script, _ := json.Marshal(fakeagent.Script{Rules: []fakeagent.Rule{{Match: "Drydock phase: recommend", Steps: []fakeagent.Step{{Say: answer}}}}})
	sp := filepath.Join(t.TempDir(), "recommend.json")
	if err := os.WriteFile(sp, script, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
}

func (a *app) recommendDone(projectID, id string) core.Recommendation {
	a.t.Helper()
	var r core.Recommendation
	a.waitFor("the recommendation", func() bool {
		var list core.Recommendations
		a.call("recommend.list", map[string]any{"projectId": projectID}, &list)
		for _, x := range list.Runs {
			if x.ID == id && x.Status != "running" {
				r = x
				return true
			}
		}
		return false
	})
	return r
}

// waitGeneral waits for the project's #general to say text: a turn the project asked for by itself is announced there just
// after it is marked done, so reading the room the moment the turn reads done can miss it.
func (a *app) waitGeneral(projectID, text string) {
	a.t.Helper()
	var rooms []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	a.call("room.list", map[string]any{"projectId": projectID}, &rooms)
	general := ""
	for _, rm := range rooms {
		if rm.Name == "general" {
			general = rm.ID
		}
	}
	if general == "" {
		a.t.Fatal("the project has no #general")
	}
	a.waitFor("#general to announce the recommendations", func() bool {
		for _, m := range a.roomMessages(general) {
			if strings.Contains(m.Body, text) {
				return true
			}
		}
		return false
	})
}

// Decision P52: the team lead's read-only turn says which agent gets which skill and MCP server; the core keeps only what
// exists (a library skill, a connected server, the team's agents) and says why it dropped the rest; the person applies or
// dismisses each item, and applying does exactly what the item says through the pages' own methods.
func TestRecommendationsAreCheckedAndAppliedOneByOne(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "cfapp")
	f := newWatchFixture(t)
	f.publish("cloudflare/skills", watchSha1, map[string]string{
		"skills/wrangler/SKILL.md":        fixtureSkill("wrangler", "", "Use the project's Wrangler version."),
		"skills/durable-objects/SKILL.md": fixtureSkill("durable-objects", "", "One object per room."),
	})
	f.use(a)
	if _, err := a.c.RunSkillWatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	var lead, dev core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "fake-1"}, &lead)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &dev)
	var srv core.MCPLibraryServer
	// a server that answers (the test MCP server), because a grant names the tools it lists and cannot be made to one that is not there
	self, _ := os.Executable()
	a.call("mcplib.save", core.MCPServerInput{ID: "cfdocs", Display: "Cloudflare docs", Transport: "stdio", Argv: []string{self}, Env: map[string]string{testMCPEnv: "1"}}, &srv)

	recommendScript(t, `Here is my advice.
{"summary": "Give the backend agent Wrangler and the Cloudflare docs.", "recommendations": [
 {"kind": "skill", "action": "add", "name": "wrangler", "registry": "cloudflare-skills", "agents": ["Backend #1"], "reason": "The backend deploys Workers."},
 {"kind": "skill", "action": "add", "name": "no-such-skill", "agents": [], "reason": "Made up."},
 {"kind": "mcp", "action": "give", "name": "cfdocs", "agents": ["`+dev.ID+`"], "reason": "It answers Workers questions."},
 {"kind": "skill", "action": "give", "name": "wrangler", "agents": ["Nobody"], "reason": "No such agent."},
 {"kind": "integration", "action": "add", "name": "linear", "reason": "Not connected."}
]}`)
	var started core.Recommendation
	a.call("recommend.start", map[string]any{"projectId": p.ID}, &started)
	if started.Status != "running" || started.AgentID != lead.ID || started.Trigger != "manual" {
		t.Fatalf("started %+v", started)
	}
	r := a.recommendDone(p.ID, started.ID)
	if r.Status != "done" || r.Summary == "" || len(r.Items) != 2 || len(r.Dropped) != 3 {
		t.Fatalf("result %+v", r)
	}
	sk, mc := r.Items[0], r.Items[1]
	if sk.Kind != "skill" || sk.Action != "add" || sk.RegistryID != "cloudflare-skills" || !slices.Equal(sk.Agents, []string{dev.ID}) || sk.Status != "open" {
		t.Fatalf("skill item %+v", sk)
	}
	if mc.Kind != "mcp" || mc.Action != "give" || mc.Label != "Cloudflare docs" || !slices.Equal(mc.Agents, []string{dev.ID}) {
		t.Fatalf("mcp item %+v", mc)
	}
	joined := strings.Join(r.Dropped, "\n")
	for _, want := range []string{"no-such-skill", `no agent "Nobody"`, "not a connected integration"} {
		if !strings.Contains(joined, want) {
			t.Errorf("dropped does not say %q:\n%s", want, joined)
		}
	}

	// applying the skill adds it from the library to the one agent; applying the server gives it to that agent
	a.call("recommend.apply", map[string]any{"id": r.ID, "itemId": sk.ID}, &r)
	var got skills.Skill
	a.call("skill.get", map[string]any{"projectId": p.ID, "name": "wrangler"}, &got)
	if got.Status != "accepted" || got.Audience != skills.AudienceAgents || !slices.Equal(got.Agents, []string{dev.ID}) {
		t.Fatalf("the applied skill: %+v", got)
	}
	a.call("recommend.apply", map[string]any{"id": r.ID, "itemId": mc.ID}, &r)
	var servers []core.MCPServerView
	a.call("mcpserver.list", map[string]any{"projectId": p.ID}, &servers)
	if len(servers) != 1 || len(servers[0].Agents) != 1 || servers[0].Agents[0].AgentID != dev.ID {
		t.Fatalf("the applied server: %+v", servers)
	}
	if r.Items[0].Status != "applied" || r.Items[1].Status != "applied" {
		t.Fatalf("statuses %+v", r.Items)
	}
	// an item is applied once
	if _, err := a.c.RecommendApply(context.Background(), r.ID, sk.ID); err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("applied twice: %v", err)
	}
}

// One turn at a time per project; a project without a lead names the agent to ask; dismissing marks the item.
func TestRecommendationsNeedAnAdvisorAndRunOneAtATime(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "solo")
	var dev core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &dev)
	if _, err := a.c.Recommend(context.Background(), p.ID, "", "manual", nil); err == nil || !strings.Contains(err.Error(), "no team lead") {
		t.Fatalf("no lead: %v", err)
	}
	recommendScript(t, `{"summary": "Nothing to add yet.", "recommendations": []}`)
	var r core.Recommendation
	a.call("recommend.start", map[string]any{"projectId": p.ID, "agentId": dev.ID, "candidates": []map[string]string{{"registryId": "cloudflare-skills", "name": "wrangler"}}}, &r)
	if r.AgentID != dev.ID || r.Trigger != "skills_added" || len(r.Candidates) != 1 {
		t.Fatalf("started %+v", r)
	}
	if _, err := a.c.Recommend(context.Background(), p.ID, dev.ID, "manual", nil); err == nil || !strings.Contains(err.Error(), "already being prepared") {
		t.Fatalf("a second turn while one runs: %v", err)
	}
	r = a.recommendDone(p.ID, r.ID)
	if r.Status != "done" || len(r.Items) != 0 || r.Summary != "Nothing to add yet." {
		t.Fatalf("%+v", r)
	}

	// an answer that is not JSON twice fails the turn with the reason
	recommendScript(t, "I think you should add some skills.")
	a.call("recommend.start", map[string]any{"projectId": p.ID, "agentId": dev.ID}, &r)
	r = a.recommendDone(p.ID, r.ID)
	if r.Status != "failed" || !strings.Contains(r.Error, "not the JSON object") {
		t.Fatalf("a non-JSON answer: %+v", r)
	}
}

// The project asks again by itself as it evolves: after five completed tasks since the last turn, not twice within a day,
// never when the person switched it off; the lead's new recommendations are announced in #general.
func TestTheProjectAsksForRecommendationsAgainAsItEvolves(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "evolving")
	var lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "fake-1"}, &lead)
	var srv core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "cfdocs", Display: "Cloudflare docs", Transport: "http", URL: "https://docs.mcp.cloudflare.com/mcp/"}, &srv)
	recommendScript(t, `{"summary": "The lead now works on Workers.", "recommendations": [{"kind": "mcp", "action": "give", "name": "cfdocs", "agents": ["Lead"], "reason": "Its tasks moved to Cloudflare."}]}`)
	db := a.c.DB.Writer()
	made := 0
	complete := func(n int) {
		for i := 0; i < n; i++ {
			made++
			if _, err := db.Exec(`INSERT INTO tasks(id,project_id,seq,title,status,created_at,updated_at) VALUES(?,?,?,'done','COMPLETED',1,1)`,
				fmt.Sprintf("task_done%04d", made), p.ID, made); err != nil {
				t.Fatal(err)
			}
		}
	}
	runs := func() core.Recommendations {
		var list core.Recommendations
		a.call("recommend.list", map[string]any{"projectId": p.ID}, &list)
		return list
	}
	ctx := context.Background()
	complete(4)
	a.c.TestMaybeRecommend(ctx, p.ID)
	if n := len(runs().Runs); n != 0 {
		t.Fatalf("four completed tasks asked already: %d", n)
	}
	complete(1)
	a.c.TestMaybeRecommend(ctx, p.ID)
	list := runs()
	if !list.Auto || len(list.Runs) != 1 || list.Runs[0].Trigger != "evolution" {
		t.Fatalf("five completed tasks: %+v", list)
	}
	r := a.recommendDone(p.ID, list.Runs[0].ID)
	if r.Status != "done" || len(r.Items) != 1 {
		t.Fatalf("%+v", r)
	}
	a.waitGeneral(p.ID, "Lead has 1 new recommendation")
	// five more within a day: not again
	complete(5)
	a.c.TestMaybeRecommend(ctx, p.ID)
	if n := len(runs().Runs); n != 1 {
		t.Fatalf("asked twice within a day: %d", n)
	}
	// switched off: never (the day is moved back so only the switch holds it)
	db.Exec(`UPDATE recommendations SET created_at=created_at-2*86400000 WHERE project_id=?`, p.ID)
	a.call("recommend.setAuto", map[string]any{"projectId": p.ID, "on": false}, &list)
	a.c.TestMaybeRecommend(ctx, p.ID)
	if list = runs(); list.Auto || len(list.Runs) != 1 {
		t.Fatalf("switched off: %+v", list)
	}
}

// A skill the project has for chosen agents only, recommended for the whole team, is widened to the team (the agents it was
// given to keep it); recommended again once the whole team has it, it is dropped with that reason. Before, the first was
// dropped as "already in the project for the whole team", which was not true.
func TestARecommendationWidensASkillToTheWholeTeam(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "widen")
	f := newWatchFixture(t)
	f.publish("cloudflare/skills", watchSha1, map[string]string{"skills/wrangler/SKILL.md": fixtureSkill("wrangler", "", "Use the project's Wrangler version.")})
	f.use(a)
	if _, err := a.c.RunSkillWatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	var lead, dev core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "fake-1"}, &lead)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &dev)
	a.call("skill.libraryAdd", map[string]any{"projectId": p.ID, "registryId": "cloudflare-skills", "name": "wrangler", "audience": skills.AudienceAgents, "agents": []string{dev.ID}}, nil)

	everyone := `{"summary": "Everyone deploys now.", "recommendations": [{"kind": "skill", "action": "add", "name": "wrangler", "agents": [], "reason": "The whole team deploys Workers."}]}`
	recommendScript(t, everyone)
	var r core.Recommendation
	a.call("recommend.start", map[string]any{"projectId": p.ID}, &r)
	r = a.recommendDone(p.ID, r.ID)
	if r.Status != "done" || len(r.Items) != 1 || r.Items[0].Action != "team" || len(r.Items[0].Agents) != 0 {
		t.Fatalf("widening to the team: %+v", r)
	}
	a.call("recommend.apply", map[string]any{"id": r.ID, "itemId": r.Items[0].ID}, &r)
	var got skills.Skill
	a.call("skill.get", map[string]any{"projectId": p.ID, "name": "wrangler"}, &got)
	if got.Audience != skills.AudienceTeam || !slices.Equal(got.Agents, []string{dev.ID}) || r.Items[0].Status != "applied" {
		t.Fatalf("after applying: skill %+v, item %+v", got, r.Items[0])
	}

	a.call("recommend.start", map[string]any{"projectId": p.ID}, &r)
	r = a.recommendDone(p.ID, r.ID)
	if r.Status != "done" || len(r.Items) != 0 || len(r.Dropped) != 1 || !strings.Contains(r.Dropped[0], "already in the project for the whole team") {
		t.Fatalf("the team has it already: %+v", r)
	}
}

// A turn the app's end or a crash cut short stayed "running" in the database and refused every later turn of its project for
// good. At start every such row is recorded as interrupted, with a reason the person can read, and the project can ask again.
func TestATurnLeftRunningByACrashIsRecordedAsInterruptedAtStart(t *testing.T) {
	dir := t.TempDir()
	a := open(t, dir)
	p := gitProject(t, a, "crashed")
	var lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "fake-1"}, &lead)
	// what a crash mid-turn leaves behind
	if _, err := a.c.DB.Writer().Exec(`INSERT INTO recommendations(id,project_id,agent_id,trigger,status,candidates,tasks_done,created_at) VALUES('rec_crashed',?,?,'manual','running','[]',0,1)`, p.ID, lead.ID); err != nil {
		t.Fatal(err)
	}
	a.c.Close()

	b := open(t, dir)
	var list core.Recommendations
	b.call("recommend.list", map[string]any{"projectId": p.ID}, &list)
	if len(list.Runs) != 1 || list.Runs[0].Status != "failed" || !strings.Contains(list.Runs[0].Error, "interrupted") || list.Runs[0].Finished == 0 {
		t.Fatalf("the interrupted turn: %+v", list.Runs)
	}
	recommendScript(t, `{"summary": "Fine as it is.", "recommendations": []}`)
	var r core.Recommendation
	b.call("recommend.start", map[string]any{"projectId": p.ID}, &r)
	if r = b.recommendDone(p.ID, r.ID); r.Status != "done" {
		t.Fatalf("asking again after the restart: %+v", r)
	}
}

// Checking that an item is open, doing what it says and marking it are one step: many clicks on one item apply it once, and
// applying one item while dismissing another keeps both marks.
func TestApplyingAndDismissingAtOnceKeepEveryMarkAndApplyOnce(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "clicks")
	var lead, dev core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "fake-1"}, &lead)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &dev)
	var srv core.MCPLibraryServer
	self, _ := os.Executable()
	for _, id := range []string{"one", "two", "three", "four"} {
		// servers that answer: a grant names the tools the server lists
		a.call("mcplib.save", core.MCPServerInput{ID: id, Display: "Server " + id, Transport: "stdio", Argv: []string{self}, Env: map[string]string{testMCPEnv: "1"}}, &srv)
	}
	recommendScript(t, `{"summary": "Four servers.", "recommendations": [
 {"kind": "mcp", "action": "give", "name": "one", "agents": ["Backend #1"], "reason": "a"},
 {"kind": "mcp", "action": "give", "name": "two", "agents": ["Backend #1"], "reason": "b"},
 {"kind": "mcp", "action": "give", "name": "three", "agents": ["Backend #1"], "reason": "c"},
 {"kind": "mcp", "action": "give", "name": "four", "agents": ["Backend #1"], "reason": "d"}]}`)
	var r core.Recommendation
	a.call("recommend.start", map[string]any{"projectId": p.ID}, &r)
	r = a.recommendDone(p.ID, r.ID)
	if len(r.Items) != 4 {
		t.Fatalf("%+v", r)
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	var applied atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(4)
		go func() { // the same item, many times
			defer wg.Done()
			if _, err := a.c.RecommendApply(ctx, r.ID, r.Items[0].ID); err == nil {
				applied.Add(1)
			}
		}()
		go func() { defer wg.Done(); _, _ = a.c.RecommendApply(ctx, r.ID, r.Items[1].ID) }()
		go func() {
			defer wg.Done()
			_, _ = a.c.Call(ctx, "recommend.dismiss", mustJSON(map[string]any{"id": r.ID, "itemId": r.Items[2].ID}))
		}()
		go func() { defer wg.Done(); _, _ = a.c.RecommendApply(ctx, r.ID, r.Items[3].ID) }()
	}
	wg.Wait()
	if n := applied.Load(); n != 1 {
		t.Fatalf("the same item was applied %d times", n)
	}
	var list core.Recommendations
	a.call("recommend.list", map[string]any{"projectId": p.ID}, &list)
	got := map[string]string{}
	for _, it := range list.Runs[0].Items {
		got[it.Name] = it.Status
	}
	if want := map[string]string{"one": "applied", "two": "applied", "three": "dismissed", "four": "applied"}; !maps.Equal(got, want) {
		t.Fatalf("marks %v, want %v", got, want)
	}
}

// The project asks again by itself through the real path: a person accepting a task publishes task.completed, and the fifth
// completed task since the last turn starts one. (TestTheProjectAsksForRecommendationsAgainAsItEvolves drives the check
// directly; this one would catch a renamed event or a lost subscription.)
func TestACompletedTaskMakesTheProjectAskAgain(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "accepted")
	var lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "fake-1"}, &lead)
	recommendScript(t, `{"summary": "Nothing to add.", "recommendations": []}`)
	db := a.c.DB.Writer()
	for i := 1; i <= 4; i++ {
		if _, err := db.Exec(`INSERT INTO tasks(id,project_id,seq,title,status,created_at,updated_at) VALUES(?,?,?,'done','COMPLETED',1,1)`, fmt.Sprintf("task_acc%d", i), p.ID, i); err != nil {
			t.Fatal(err)
		}
	}
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "The fifth"}, &task)
	if _, err := db.Exec(`UPDATE tasks SET status='VERIFICATION' WHERE id=?`, task.ID); err != nil {
		t.Fatal(err)
	}
	passed := true
	ctx := context.Background()
	if _, err := a.c.Tasks.AddEvidence(ctx, task.ID, tasks.Evidence{Kind: "test", Passed: &passed, Summary: "go test ./... passed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.Tasks.Transition(ctx, task.ID, tasks.Completed, tasks.TransitionOpts{By: a.c.Human()}); err != nil {
		t.Fatal(err)
	}
	a.waitFor("the project to ask again", func() bool {
		var list core.Recommendations
		a.call("recommend.list", map[string]any{"projectId": p.ID}, &list)
		return len(list.Runs) == 1 && list.Runs[0].Trigger == "evolution"
	})
}

// A project is not deleted while one of its agents prepares recommendations: the turn would write into a project that is
// gone. The check is in the delete's own transaction, like the other running work.
func TestAProjectIsNotDeletedWhileARecommendationRuns(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "advised")
	var lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "fake-1"}, &lead)
	if _, err := a.c.DB.Writer().Exec(`INSERT INTO recommendations(id,project_id,agent_id,trigger,status,candidates,tasks_done,created_at) VALUES('rec_busy',?,?,'manual','running','[]',0,1)`, p.ID, lead.ID); err != nil {
		t.Fatal(err)
	}
	_, err := a.c.DeleteProject(context.Background(), p.ID, "advised")
	if !errors.Is(err, core.ErrProjectBusy) || !strings.Contains(err.Error(), "preparing recommendations") {
		t.Fatalf("deleting while a recommendation runs: %v", err)
	}
	if _, err := a.c.DB.Writer().Exec(`UPDATE recommendations SET status='done' WHERE id='rec_busy'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.DeleteProject(context.Background(), p.ID, "advised"); err != nil {
		t.Fatalf("deleting once it ended: %v", err)
	}
}
