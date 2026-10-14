package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/skills"
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
	a.call("mcplib.save", core.MCPServerInput{ID: "cfdocs", Display: "Cloudflare docs", Transport: "http", URL: "https://docs.mcp.cloudflare.com/mcp/"}, &srv)

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
	var general string
	var rooms []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	a.call("room.list", map[string]any{"projectId": p.ID}, &rooms)
	for _, rm := range rooms {
		if rm.Name == "general" {
			general = rm.ID
		}
	}
	found := false
	for _, m := range a.roomMessages(general) {
		found = found || strings.Contains(m.Body, "Lead has 1 new recommendation")
	}
	if !found {
		t.Fatal("#general does not announce the recommendations the project asked for by itself")
	}
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
