package core_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

func needsKeys(a *app, projectID string) map[string]room.NeedsYouItem {
	a.t.Helper()
	var items []room.NeedsYouItem
	a.call("needsyou.list", map[string]any{"projectId": projectID}, &items)
	out := map[string]room.NeedsYouItem{}
	for _, it := range items {
		out[it.Key] = it
	}
	return out
}

func roomNamed(a *app, projectID, name string) room.Room {
	a.t.Helper()
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": projectID}, &rooms)
	for _, r := range rooms {
		if r.Name == name {
			return r
		}
	}
	a.t.Fatalf("no room %s", name)
	return room.Room{}
}

// The Inbox items the core holds in memory (decision P130): an agent that failed is an item until it next starts, and can be
// marked done; an agent CLI an agent uses whose latest check says it is not installed is one item, until the check says
// otherwise, and cannot be marked done.
func TestAgentAndConnectionItemsFollowWhatTheCoreHolds(t *testing.T) {
	// no CLI on PATH (git only) or in the home folder: the checks agent.create runs find Codex not installed
	empty := t.TempDir()
	gitExe, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	if err := os.Symlink(gitExe, filepath.Join(empty, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", empty)
	t.Setenv("HOME", empty)
	t.Setenv("USERPROFILE", empty)
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var cx core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Cx", Adapter: "codex", Role: "backend"}, &cx)
	items := needsKeys(a, p.ID)
	conn, ok := items["connection:codex"]
	if !ok || conn.Kind != room.NeedsConnection || conn.Adapter != "codex" || conn.ConnectionKey != "codex" || !strings.HasSuffix(conn.Title, "is not installed") {
		t.Fatalf("the connection item: %+v", items)
	}
	if _, err := a.c.Call(context.Background(), "needsyou.ack", mustJSON(map[string]any{"key": "connection:codex"})); err == nil {
		t.Fatal("a Connection item was marked done")
	}
	// the agent fails: an item until it starts again
	data, _ := json.Marshal(map[string]any{"error": "exit status 1"})
	if _, err := a.c.Bus.Publish(context.Background(), events.Event{Type: "agent.failed", ProjectID: p.ID, AgentID: cx.ID, Data: data}); err != nil {
		t.Fatal(err)
	}
	var key string
	a.waitFor("the agent item", func() bool {
		for k, it := range needsKeys(a, p.ID) {
			if it.Kind == room.NeedsAgent && it.AgentID == cx.ID && it.AgentName == "Cx" && it.Title == "Failed: exit status 1" {
				key = k
				return true
			}
		}
		return false
	})
	a.call("needsyou.ack", map[string]any{"key": key}, nil)
	if _, ok := needsKeys(a, p.ID)[key]; ok {
		t.Fatal("marked done, still there")
	}
	// a later failure is a new item; the agent's next start clears it
	data, _ = json.Marshal(map[string]any{"error": "again"})
	a.c.Bus.Publish(context.Background(), events.Event{Type: "agent.started", ProjectID: p.ID, AgentID: cx.ID})
	a.c.Bus.Publish(context.Background(), events.Event{Type: "agent.failed", ProjectID: p.ID, AgentID: cx.ID, Data: data})
	a.waitFor("a new item", func() bool {
		it := agentItems(a, p.ID)
		return len(it) == 1 && it[0].Key != key && it[0].Title == "Failed: again"
	})
	a.c.Bus.Publish(context.Background(), events.Event{Type: "agent.started", ProjectID: p.ID, AgentID: cx.ID})
	a.waitFor("the item to go", func() bool { return len(agentItems(a, p.ID)) == 0 })
	// a watchdog pause is the watchdog's item, not the agent's
	a.c.Bus.Publish(context.Background(), events.Event{Type: "watchdog.paused", ProjectID: p.ID, AgentID: cx.ID})
	a.waitFor("presence", func() bool { ps, ok := a.c.Presence.Get(cx.ID); return ok && ps.Label == "watchdog" })
	if n := len(agentItems(a, p.ID)); n != 0 {
		t.Fatalf("%d agent items for a watchdog pause", n)
	}
}

func agentItems(a *app, projectID string) []room.NeedsYouItem {
	var out []room.NeedsYouItem
	for _, it := range needsKeys(a, projectID) {
		if it.Kind == room.NeedsAgent {
			out = append(out, it)
		}
	}
	return out
}

// A person mutes a room and unmutes it (room.setMuted); the DM's record names its agent; room.stop with nobody answering
// stops nothing.
func TestMutingARoomAndTheDMsAgent(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	w := a.agent(p.ID, "W", "backend")
	gen := roomNamed(a, p.ID, "general")
	var u room.Unread
	a.call("room.setMuted", map[string]any{"roomId": gen.ID, "muted": true}, &u)
	if !u.Muted || u.RoomID != gen.ID {
		t.Fatalf("%+v", u)
	}
	var us []room.Unread
	a.call("room.unread", map[string]any{"projectId": p.ID}, &us)
	for _, x := range us {
		if x.RoomID == gen.ID && !x.Muted {
			t.Fatal("room.unread does not say muted")
		}
	}
	a.call("room.setMuted", map[string]any{"roomId": gen.ID, "muted": false}, &u)
	if u.Muted {
		t.Fatalf("%+v", u)
	}
	var dm room.Room
	a.call("room.createDM", map[string]any{"projectId": p.ID, "agentId": w.ID}, &dm)
	var listed room.Room
	for _, r := range listRooms(a, p.ID) {
		if r.ID == dm.ID {
			listed = r
		}
	}
	if dm.AgentID != w.ID || listed.AgentID != w.ID {
		t.Fatalf("the DM's agent: %+v %+v", dm, listed)
	}
	var stopped map[string]int
	a.call("room.stop", map[string]any{"roomId": gen.ID}, &stopped)
	if stopped["stopped"] != 0 {
		t.Fatalf("%v", stopped)
	}
	if _, err := a.c.Call(context.Background(), "room.stop", mustJSON(map[string]any{"roomId": "room_nope"})); err == nil {
		t.Fatal("stopped a room that does not exist")
	}
}

func listRooms(a *app, projectID string) []room.Room {
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": projectID}, &rooms)
	return rooms
}

// A join rule a person makes adds its agent to the task rooms created after it whose task names a file under its prefix; an
// agent's request to join a channel (the Workspace room tool) is an Inbox item until the person lets it in.
func TestJoinRulesAndRequestsThroughTheCore(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	sec := a.agent(p.ID, "Sec", "security")
	var rule room.JoinRule
	a.call("room.joinRule.add", map[string]any{"projectId": p.ID, "agentId": sec.ID, "pathPrefix": "auth/"}, &rule)
	if rule.State != room.StateObserver || rule.PathPrefix != "auth/" {
		t.Fatalf("%+v", rule)
	}
	var rules []room.JoinRule
	a.call("room.joinRules", map[string]any{"projectId": p.ID}, &rules)
	if len(rules) != 1 {
		t.Fatalf("%+v", rules)
	}
	var tk tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Harden login", Files: []string{"auth/login.go"}}, &tk)
	tr, err := a.c.Room.RoomByTask(context.Background(), tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ms []room.Member
	a.call("room.members", map[string]any{"roomId": tr.ID}, &ms)
	found := false
	for _, m := range ms {
		found = found || (m.Author.ID == sec.ID && m.State == room.StateObserver)
	}
	if !found {
		t.Fatalf("the rule did not add Sec: %+v", ms)
	}
	a.call("room.joinRule.remove", map[string]any{"id": rule.ID}, nil)

	// every agent is in the default channels; a channel a person made later is one it can ask for
	arch, err := a.c.Room.CreateChannel(context.Background(), p.ID, "architecture", "", room.Human("kyle"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := a.c.TestRequestJoin(context.Background(), p.ID, sec.ID, "#architecture", "I record security decisions")
	if err != nil || !strings.Contains(out, "a person decides") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := a.c.TestRequestJoin(context.Background(), p.ID, sec.ID, "nope", ""); err == nil {
		t.Fatal("asked to join a channel that does not exist")
	}
	var join room.NeedsYouItem
	for _, it := range needsKeys(a, p.ID) {
		if it.Kind == room.NeedsJoinRequest {
			join = it
		}
	}
	if join.JoinRequestID == "" || join.RoomID != arch.ID || join.AgentName != "Sec" || join.Detail != "Why: I record security decisions" {
		t.Fatalf("%+v", join)
	}
	if _, err := a.c.Call(context.Background(), "needsyou.ack", mustJSON(map[string]any{"key": join.Key})); err == nil {
		t.Fatal("a join request was marked done instead of answered")
	}
	var jr room.JoinRequest
	a.call("room.answerJoin", map[string]any{"id": join.JoinRequestID, "allow": true}, &jr)
	if jr.Status != room.JoinGranted {
		t.Fatalf("%+v", jr)
	}
	if ok, _ := a.c.Room.IsMember(context.Background(), arch.ID, room.Agent(sec.ID)); !ok {
		t.Fatal("not let in")
	}
	if _, ok := needsKeys(a, p.ID)[join.Key]; ok {
		t.Fatal("the answered request stayed")
	}
}
