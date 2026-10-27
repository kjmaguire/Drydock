package core_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/room"
)

// crew.extras answers in one read what the Crew page asked each project and each room for (review 2026-10-27): the accepted
// skills given to each agent, and each channel and group with its agents in the order they joined, the same rooms and members
// room.list and room.members give; one project's, or every project's.
func TestCrewExtrasIsEachAgentsSkillsAndRoomsInOneRead(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	for _, n := range []string{"go-style", "ui-rule"} {
		os.MkdirAll(filepath.Join(repo, ".aet", "skills", n), 0o755)
		os.WriteFile(filepath.Join(repo, ".aet", "skills", n, "SKILL.md"), []byte("---\nname: "+n+"\ndescription: "+n+"\n---\nA rule.\n"), 0o644)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "skills")
	var p1, p2 core.Project
	a.call("project.create", map[string]any{"name": "one", "repoPath": repo}, &p1)
	a.call("project.create", map[string]any{"name": "two", "repoPath": newRepo(t)}, &p2)
	a.call("skill.scan", map[string]string{"projectId": p1.ID}, nil)
	a.call("skill.accept", map[string]string{"projectId": p1.ID, "name": "go-style"}, nil) // ui-rule stays offered
	var w1, w2 core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p1.ID, Name: "Backend", Adapter: "claude", Role: "backend", Model: "fake-1"}, &w1)
	a.call("agent.create", core.AgentSpec{ProjectID: p2.ID, Name: "Other", Adapter: "claude", Role: "backend", Model: "fake-1"}, &w2)
	a.call("skill.assign", map[string]any{"projectId": p1.ID, "name": "go-style", "agentId": w1.ID, "on": true}, nil)

	// what the page used to ask for, room by room
	old := func(pid string) []core.CrewRoom {
		var rs []room.Room
		a.call("room.list", map[string]any{"projectId": pid}, &rs)
		out := []core.CrewRoom{}
		for _, r := range rs {
			if r.Archived || (r.Kind != "channel" && r.Kind != "group") {
				continue
			}
			var ms []room.Member
			a.call("room.members", map[string]any{"roomId": r.ID}, &ms)
			cr := core.CrewRoom{ProjectID: pid, Name: r.Name, Kind: string(r.Kind), Agents: []string{}}
			for _, m := range ms {
				if m.Author.Kind == "agent" {
					cr.Agents = append(cr.Agents, m.Author.ID)
				}
			}
			out = append(out, cr)
		}
		return out
	}

	var one core.CrewExtras
	a.call("crew.extras", map[string]any{"projectId": p1.ID}, &one)
	if !reflect.DeepEqual(one.Skills, []core.CrewSkill{{AgentID: w1.ID, Name: "go-style"}}) {
		t.Fatalf("skills %+v", one.Skills)
	}
	in := 0
	for _, r := range one.Rooms {
		for _, id := range r.Agents {
			if id == w1.ID {
				in++
			}
		}
	}
	if in == 0 {
		t.Fatalf("the agent is in none of its project's rooms: %+v", one.Rooms)
	}
	if want := old(p1.ID); len(want) == 0 || !reflect.DeepEqual(one.Rooms, want) {
		t.Fatalf("rooms\n got %+v\nwant %+v", one.Rooms, want)
	}
	var all core.CrewExtras
	a.call("crew.extras", map[string]any{"projectId": ""}, &all)
	byProject := map[string][]core.CrewRoom{}
	for _, r := range all.Rooms {
		byProject[r.ProjectID] = append(byProject[r.ProjectID], r)
	}
	for _, pid := range []string{p1.ID, p2.ID} {
		if !reflect.DeepEqual(byProject[pid], old(pid)) {
			t.Fatalf("%s: got %+v want %+v", pid, byProject[pid], old(pid))
		}
	}
	if len(all.Skills) != 1 || len(byProject) != 2 {
		t.Fatalf("%+v", all)
	}
}
