package room

import (
	"strings"
	"testing"
)

// A decision taken in a room shows up in #decisions (X110): one quiet SYSTEM line per DECISION recorded elsewhere (a /decision
// and a [DECISION] marker alike), none for a decision posted in #decisions itself, and the project's candidates are listed with
// the room each was taken in.
func TestDecisionsShowUpInTheDecisionsChannel(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.EnsureDefaultChannels(e.ctx, e.project, kyle); err != nil {
		t.Fatal(err)
	}
	rooms, err := e.svc.ListRooms(e.ctx, e.project, false)
	if err != nil {
		t.Fatal(err)
	}
	var general, decisions Room
	for _, r := range rooms {
		switch r.Name {
		case "general":
			general = r
		case DecisionsChannel:
			decisions = r
		}
	}
	d := e.post(PostParams{RoomID: general.ID, Type: TypeDecision, Author: kyle, Body: "Use SQLite for sessions", Payload: map[string]any{"title": "Sessions in SQLite"}}).Message
	e.say(general, kyle, "Agreed.\n[DECISION] Cache with Redis")
	e.post(PostParams{RoomID: decisions.ID, Type: TypeDecision, Author: kyle, Body: "Taken here", Payload: map[string]any{"title": "Here"}})
	var lines []Message
	for _, m := range e.messagesOf(decisions) {
		if m.Type == TypeSystem && m.PayloadString("event") == "decision_elsewhere" {
			lines = append(lines, m)
		}
	}
	if len(lines) != 2 || lines[0].Body != "A decision was recorded in #general: Sessions in SQLite" || lines[0].PayloadString("message_id") != d.ID ||
		!strings.Contains(lines[1].Body, "Cache with Redis") {
		t.Fatalf("lines in #decisions: %+v", lines)
	}
	// the SYSTEM line is not unread conversation
	us, err := e.svc.UnreadCounts(e.ctx, kyle, e.project)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range us {
		if u.RoomID == decisions.ID && u.Unread != 0 { // SYSTEM lines and the person's own decision are not unread
			t.Fatalf("#decisions unread: %+v", u)
		}
	}
	cs, err := e.svc.ProjectADRCandidates(e.ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]RoomADRCandidate{}
	for _, c := range cs {
		byTitle[c.Title] = c
	}
	if c, ok := byTitle["Sessions in SQLite"]; !ok || c.RoomID != general.ID || c.RoomName != "general" || c.MessageID != d.ID {
		t.Fatalf("candidates: %+v", cs)
	}
	if _, ok := byTitle["Cache with Redis"]; !ok || len(cs) != 3 {
		t.Fatalf("candidates: %+v", cs)
	}
	e.noErrs()
}

// A decision taken in a task room stays in the #decisions list after the task closes and its room is archived, marked
// archived, so the ADR can still be made from it (X110 review).
func TestATaskRoomsDecisionStaysListedAfterTheTaskCloses(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.EnsureDefaultChannels(e.ctx, e.project, kyle); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.CreateTaskChannel(e.ctx, TaskChannelSpec{ProjectID: e.project, TaskID: "task_12", Number: 12, Title: "Login", Owner: e.agent("Ada"), CreatedBy: kyle})
	if err != nil {
		t.Fatal(err)
	}
	e.post(PostParams{RoomID: r.ID, Type: TypeDecision, Author: kyle, Body: "Use sessions, not JWT", Payload: map[string]any{"title": "Sessions"}})
	if err := e.svc.ArchiveTaskChannel(e.ctx, "task_12", "COMPLETED"); err != nil {
		t.Fatal(err)
	}
	cs, err := e.svc.ProjectADRCandidates(e.ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Title != "Sessions" || cs[0].RoomID != r.ID || !cs[0].Archived {
		t.Fatalf("candidates after the task closed: %+v", cs)
	}
	e.noErrs()
}

// A decision in a direct message puts a line in #decisions without its title: agents that are members of #decisions read the
// channel, and never read a direct message (X110 review). The person's own list still shows it.
func TestADirectMessagesDecisionIsNotCopiedIntoDecisions(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.EnsureDefaultChannels(e.ctx, e.project, kyle); err != nil {
		t.Fatal(err)
	}
	dm, err := e.svc.CreateDM(e.ctx, e.project, kyle, Agent(e.agent("Ada")))
	if err != nil {
		t.Fatal(err)
	}
	e.post(PostParams{RoomID: dm.ID, Type: TypeDecision, Author: kyle, Body: "Rotate the staging DB password on Friday", Payload: map[string]any{"title": "Rotate the staging DB password on Friday"}})
	rooms, err := e.svc.ListRooms(e.ctx, e.project, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rooms {
		if r.Name != DecisionsChannel {
			continue
		}
		var n int
		for _, m := range e.messagesOf(r) {
			if strings.Contains(m.Body, "staging") {
				t.Fatalf("the title reached #decisions: %q", m.Body)
			}
			if m.PayloadString("event") == "decision_elsewhere" {
				n++
				if m.Body != "A decision was recorded in a direct message." {
					t.Fatalf("line: %q", m.Body)
				}
			}
		}
		if n != 1 {
			t.Fatalf("%d lines", n)
		}
	}
	e.noErrs()
}

// When a task's room closes, its minutes are kept by rule (X19): one export of kind minutes, scope system:task-archive and the
// task id as receipt, made once however often the room is archived, verifiable against the messages, listed with the room's
// exports.
func TestATaskRoomKeepsItsMinutesWhenItCloses(t *testing.T) {
	e := newEnv(t)
	worker := e.agent("Ada")
	r, err := e.svc.CreateTaskChannel(e.ctx, TaskChannelSpec{ProjectID: e.project, TaskID: "task_m", Number: 3, Title: "Add login", Owner: worker, CreatedBy: kyle})
	if err != nil {
		t.Fatal(err)
	}
	e.say(r, kyle, "Please add a login page")
	e.post(PostParams{RoomID: r.ID, Type: TypeDecision, Author: kyle, Body: "Use sessions, not JWT", Payload: map[string]any{"title": "Sessions"}})
	for _, outcome := range []string{"COMPLETED", "COMPLETED"} {
		if err := e.svc.ArchiveTaskChannel(e.ctx, "task_m", outcome); err != nil {
			t.Fatal(err)
		}
	}
	list, err := e.svc.ListExports(e.ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Kind != ExportMinutes || list[0].Scope != TaskArchiveScope || list[0].ReceiptID != "task_m" || list[0].CreatedBy != "system" {
		t.Fatalf("exports: %+v", list)
	}
	if !strings.Contains(list[0].Content, "Sessions") {
		t.Fatalf("minutes: %s", list[0].Content)
	}
	if err := e.svc.VerifyExport(e.ctx, list[0].ID); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rm, _ := e.svc.GetRoom(e.ctx, r.ID); !rm.Archived {
		t.Fatal("the room stays open")
	}
	e.noErrs()
}
