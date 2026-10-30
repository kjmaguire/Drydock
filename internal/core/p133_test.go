package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

func joinLines(t *testing.T, a *app, roomID string) int {
	t.Helper()
	msgs, err := a.c.Room.Messages(context.Background(), roomID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range msgs {
		if m.Type == room.TypeSystem && strings.Contains(m.Body, "by a join rule") {
			n++
		}
	}
	return n
}

// A task that names its files after its room was made is matched against the join rules then (decision P133): the room is
// told by the pipeline when a work turn named the files. A rule acts once in a room, so calling again adds nothing, and an
// agent a person took out of the room is not brought back.
func TestAJoinRuleIsMatchedAgainWhenATaskNamesItsFilesLater(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	sec := a.agent(p.ID, "Sec", "security")
	var rule room.JoinRule
	a.call("room.joinRule.add", map[string]any{"projectId": p.ID, "agentId": sec.ID, "pathPrefix": "auth/"}, &rule)
	ctx := context.Background()

	var tk, other tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Harden login"}, &tk)
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Fix the table"}, &other)
	tr, err := a.c.Room.RoomByTask(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	or, err := a.c.Room.RoomByTask(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.c.Room.IsMember(ctx, tr.ID, room.Agent(sec.ID)); ok {
		t.Fatal("the rule matched a task that names no files")
	}

	// the pipeline names the files from the diff, then tells the room through the core's hook
	files, otherFiles := []string{"auth/login.go", "README.md"}, []string{"table/table.go"}
	if _, err := a.c.Tasks.Patch(ctx, tk.ID, tasks.Update{Files: &files}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.Tasks.Patch(ctx, other.ID, tasks.Update{Files: &otherFiles}); err != nil {
		t.Fatal(err)
	}
	if a.c.Deps.OnFiles == nil {
		t.Fatal("the pipeline is not wired to tell the room")
	}
	a.c.Deps.OnFiles(ctx, tk)
	a.c.Deps.OnFiles(ctx, other)
	var ms []room.Member
	a.call("room.members", map[string]any{"roomId": tr.ID}, &ms)
	found := false
	for _, m := range ms {
		found = found || (m.Author.ID == sec.ID && m.State == room.StateObserver)
	}
	if !found {
		t.Fatalf("the rule did not add Sec when the files were named: %+v", ms)
	}
	if ok, _ := a.c.Room.IsMember(ctx, or.ID, room.Agent(sec.ID)); ok {
		t.Fatal("a task whose files are not under the prefix got the agent")
	}
	if n := joinLines(t, a, tr.ID); n != 1 {
		t.Fatalf("%d lines say who joined", n)
	}

	// every later turn names the files again: nothing happens twice
	a.c.Deps.OnFiles(ctx, tk)
	if n := joinLines(t, a, tr.ID); n != 1 {
		t.Fatalf("%d lines after a second call", n)
	}

	// a person takes the agent out; the next turn does not bring it back
	if err := a.c.Room.RemoveMember(ctx, tr.ID, room.Agent(sec.ID), room.Human("kyle")); err != nil {
		t.Fatal(err)
	}
	a.c.Deps.OnFiles(ctx, tk)
	if ok, _ := a.c.Room.IsMember(ctx, tr.ID, room.Agent(sec.ID)); ok {
		t.Fatal("an agent a person took out was added again")
	}

	// no room, an archived room and an unknown task change nothing and are not errors
	if err := a.c.Room.ReapplyJoinRules(ctx, "task_nope"); err != nil {
		t.Fatal(err)
	}
	if err := a.c.Room.ArchiveTaskChannel(ctx, other.ID, "CANCELLED"); err != nil {
		t.Fatal(err)
	}
	if err := a.c.Room.ReapplyJoinRules(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
}
