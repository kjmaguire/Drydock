package room

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDefaultChannelsAreIdempotent(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 2; i++ {
		rs, err := e.svc.EnsureDefaultChannels(e.ctx, e.project, kyle)
		if err != nil || len(rs) != 4 {
			t.Fatalf("%v %d", err, len(rs))
		}
	}
	list, _ := e.svc.ListRooms(e.ctx, e.project, false)
	var names []string
	for _, r := range list {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "alerts,decisions,general,reviews" {
		t.Fatalf("got %v", names)
	}
	if n := len(e.events("room.created")); n != 4 {
		t.Fatalf("room.created events: %d", n)
	}
	if _, err := e.svc.CreateChannel(e.ctx, e.project, "Bad Name", "", kyle); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
	if _, err := e.svc.CreateChannel(e.ctx, e.project, "x", "", Agent("a1")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent creating a room: %v", err)
	}
}

func TestTaskChannelLifecycle(t *testing.T) {
	e := newEnv(t)
	worker, reviewer := e.agent("worker"), e.agent("reviewer")
	spec := TaskChannelSpec{ProjectID: e.project, TaskID: "task_9", Number: 9, Title: "Fix the login bug", Owner: worker, Reviewers: []string{reviewer}, CreatedBy: kyle}
	r, err := e.svc.CreateTaskChannel(e.ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "task-9-fix-the-login-bug" || r.Kind != KindTask || r.TaskID != "task_9" {
		t.Fatalf("%+v", r)
	}
	again, err := e.svc.CreateTaskChannel(e.ctx, spec)
	if err != nil || again.ID != r.ID {
		t.Fatalf("not idempotent: %v %s %s", err, again.ID, r.ID)
	}
	ms, _ := e.svc.Members(e.ctx, r.ID)
	got := map[string]string{}
	for _, m := range ms {
		got[m.Author.Key()] = m.Role
	}
	if got[worker] != "owner" || got[reviewer] != "reviewer" || got["tool:tests"] != "tool" || got["tool:deps"] != "tool" || got["human:kyle"] != "owner" {
		t.Fatalf("members: %v", got)
	}
	if len(ms) != 2+1+len(ToolMembers) {
		t.Fatalf("member count %d", len(ms))
	}
	if r2, err := e.svc.RoomByTask(e.ctx, "task_9"); err != nil || r2.ID != r.ID {
		t.Fatal("RoomByTask")
	}

	e.say(r, Agent(worker), "working on it")
	if err := e.svc.ArchiveTaskChannel(e.ctx, "task_9", "completed"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ArchiveTaskChannel(e.ctx, "task_9", "completed"); err != nil {
		t.Fatalf("archive twice: %v", err)
	}
	if _, err := e.svc.Post(e.ctx, PostParams{RoomID: r.ID, Author: kyle, Body: "late"}); !errors.Is(err, ErrArchived) {
		t.Fatalf("post after archive: %v", err)
	}
	msgs, _ := e.svc.Messages(e.ctx, r.ID, 0, 100)
	if len(msgs) != 2 || msgs[1].Type != TypeSystem || !strings.Contains(msgs[1].Body, "task completed") {
		t.Fatalf("messages kept with a system note: %+v", msgs)
	}
	if n := len(e.events("room.archived")); n != 1 {
		t.Fatalf("room.archived events %d", n)
	}
	if l, _ := e.svc.ListRooms(e.ctx, e.project, false); len(l) != 0 {
		t.Fatal("archived room still listed")
	}
}

func TestWatchTasksArchivesOnTaskEvents(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	r, _ := e.svc.CreateTaskChannel(e.ctx, TaskChannelSpec{ProjectID: e.project, TaskID: "task_w", Number: 1, Title: "x", CreatedBy: kyle})
	if err := e.svc.WatchTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.bus.Publish(e.ctx, evTask("task.completed", "task_w")); err != nil {
		t.Fatal(err)
	}
	eventually(t, 3*time.Second, func() bool { g, _ := e.svc.GetRoom(e.ctx, r.ID); return g.Archived })
}

func TestDMAndGroup(t *testing.T) {
	e := newEnv(t)
	a, b := e.agent("a"), e.agent("b")
	dm, err := e.svc.CreateDM(e.ctx, e.project, kyle, Agent(a))
	if err != nil {
		t.Fatal(err)
	}
	if dm2, _ := e.svc.CreateDM(e.ctx, e.project, kyle, Agent(a)); dm2.ID != dm.ID {
		t.Fatal("DM not idempotent")
	}
	if err := e.svc.AddMember(e.ctx, dm.ID, Agent(b), "", StateActive, kyle); !errors.Is(err, ErrDMFull) {
		t.Fatalf("third member of a DM: %v", err)
	}
	if _, err := e.svc.CreateDM(e.ctx, e.project, Agent(a), Agent(b)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("agent-agent DM: %v", err)
	}

	g, err := e.svc.CreateGroup(e.ctx, e.project, "design-chat", []Author{Agent(a), Agent(b)}, kyle)
	if err != nil || g.Kind != KindGroup {
		t.Fatalf("%v %+v", err, g)
	}
	if _, err := e.svc.ConvertGroupToChannel(e.ctx, g.ID, "", g.Version+5, kyle); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	if _, err := e.svc.ConvertGroupToChannel(e.ctx, g.ID, "", g.Version, Agent(a)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent converting: %v", err)
	}
	c, err := e.svc.ConvertGroupToChannel(e.ctx, g.ID, "design", g.Version, kyle)
	if err != nil || c.Kind != KindChannel || c.Name != "design" || c.Version != g.Version+1 {
		t.Fatalf("%v %+v", err, c)
	}
	if _, err := e.svc.ConvertGroupToChannel(e.ctx, g.ID, "", c.Version, kyle); !errors.Is(err, ErrInvalid) {
		t.Fatalf("convert a channel: %v", err)
	}
	if n := len(e.events("room.kind_changed")); n != 1 {
		t.Fatal("kind_changed event")
	}
}

func TestMembershipRules(t *testing.T) {
	e := newEnv(t)
	r := e.channel("arch")
	a := e.agent("a")
	e.exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('proj_other','other','/tmp/o',1)`)
	other := e.agentIn("proj_other", "stranger")
	if err := e.svc.AddMember(e.ctx, r.ID, Agent(a), "dev", StateActive, Agent(a)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an agent adding itself: %v", err)
	}
	if err := e.svc.AddMember(e.ctx, r.ID, Agent("agent_ghost"), "", StateActive, kyle); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("ghost agent: %v", err)
	}
	if err := e.svc.AddMember(e.ctx, r.ID, Agent(other), "", StateActive, kyle); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("agent from another project: %v", err)
	}
	if err := e.svc.AddMember(e.ctx, r.ID, Tool("Nope"), "", StateActive, kyle); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown tool: %v", err)
	}
	if err := e.svc.AddMember(e.ctx, r.ID, Agent(a), "dev", "wild", kyle); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad state: %v", err)
	}
	if err := e.svc.AddMember(e.ctx, r.ID, Agent(a), "dev", StateActive, kyle); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AddMember(e.ctx, r.ID, Agent(a), "dev", StateActive, kyle); err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
	if n := len(e.events("room.member_added")); n != 2 { // kyle (owner) + a
		t.Fatalf("member_added events %d", n)
	}
	e.say(r, Agent(a), "hello")
	if err := e.svc.SetMemberState(e.ctx, r.ID, Agent(a), StateObserver, kyle); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveMember(e.ctx, r.ID, Agent(a), kyle); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.svc.IsMember(e.ctx, r.ID, Agent(a)); ok {
		t.Fatal("still a member")
	}
	if _, err := e.svc.Post(e.ctx, PostParams{RoomID: r.ID, Author: Agent(a), Body: "still here?"}); !errors.Is(err, ErrNotMember) {
		t.Fatalf("removed agent posting: %v", err)
	}
	if _, err := e.svc.ContextFor(e.ctx, r.ID, Agent(a), "msg_x", ContextOptions{}); !errors.Is(err, ErrNotMember) {
		t.Fatalf("removed agent reading: %v", err)
	}
}

func TestPostPermissionsAndPayloads(t *testing.T) {
	e := newEnv(t)
	r := e.channel("work")
	a := e.agent("a")
	e.join(r, Agent(a), "dev")
	e.join(r, Tool("tests"), "tool")

	cases := []struct {
		name string
		p    PostParams
		err  error
	}{
		{"agent chat", PostParams{Author: Agent(a), Body: "hi"}, nil},
		{"agent result", PostParams{Author: Agent(a), Type: TypeResult, Body: "done"}, nil},
		{"agent system", PostParams{Author: Agent(a), Type: TypeSystem, Body: "fake"}, ErrForbidden},
		{"human status", PostParams{Author: kyle, Type: TypeStatus, Body: "x"}, ErrForbidden},
		{"tool chat", PostParams{Author: Tool("tests"), Type: TypeChat, Body: "x"}, ErrForbidden},
		{"human approval", PostParams{Author: kyle, Type: TypeApproval, Body: "approved"}, ErrForbidden},
		{"system approval", PostParams{Author: System, Type: TypeApproval, Body: "approved"}, ErrForbidden},
		{"unknown type", PostParams{Author: kyle, Type: "SHOUT", Body: "x"}, ErrInvalid},
		{"empty", PostParams{Author: kyle, Body: "  "}, ErrInvalid},
		{"too big", PostParams{Author: kyle, Body: strings.Repeat("x", MaxBody+1)}, ErrInvalid},
		{"non-member agent", PostParams{Author: Agent("agent_nobody"), Body: "x"}, ErrNotMember},
		{"bad payload", PostParams{Author: kyle, Type: TypeQuestion, Body: "q", Payload: []int{1}}, ErrInvalid},
		{"bad decision status", PostParams{Author: kyle, Type: TypeDecision, Body: "d", Payload: map[string]any{"status": "maybe"}}, ErrInvalid},
		{"unknown room", PostParams{RoomID: "room_nope", Author: kyle, Body: "x"}, ErrNotFound},
		{"invalid utf8", PostParams{Author: kyle, Body: "bad \xff"}, ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.p.RoomID == "" {
				c.p.RoomID = r.ID
			}
			_, err := e.svc.Post(e.ctx, c.p)
			if c.err == nil && err != nil || c.err != nil && !errors.Is(err, c.err) {
				t.Fatalf("got %v want %v", err, c.err)
			}
		})
	}

	// agent DECISION and TASK are always proposed; a human may accept explicitly; defaults come from the body.
	d := e.post(PostParams{RoomID: r.ID, Author: Agent(a), Type: TypeDecision, Body: "Use WAL\nbecause concurrency", Payload: map[string]any{"status": "accepted"}}).Message
	if d.PayloadString("status") != "proposed" || d.PayloadString("title") != "Use WAL" || d.Trust != TrustUntrusted {
		t.Fatalf("agent decision: %s trust=%s", d.Payload, d.Trust)
	}
	h := e.post(PostParams{RoomID: r.ID, Author: kyle, Type: TypeDecision, Body: "Use WAL", Payload: map[string]any{"status": "accepted", "alternatives": []string{"rollback journal"}}}).Message
	if h.PayloadString("status") != "accepted" || h.Trust != TrustTrusted {
		t.Fatalf("human decision: %s", h.Payload)
	}
	tk := e.post(PostParams{RoomID: r.ID, Author: Agent(a), Type: TypeTask, Body: "Add retries", Payload: map[string]any{"state": "assigned"}}).Message
	if tk.PayloadString("state") != "proposed" {
		t.Fatalf("agent task: %s", tk.Payload)
	}
	q := e.post(PostParams{RoomID: r.ID, Author: Agent(a), Type: TypeQuestion, Body: "which db?"}).Message
	if q.PayloadBool("blocking") {
		t.Fatal("question default")
	}
}

func TestThreadsReplyAndSupersede(t *testing.T) {
	e := newEnv(t)
	r := e.channel("t")
	root := e.say(r, kyle, "root")
	r1 := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "reply", ReplyToID: root.ID}).Message
	if r1.ThreadRootID != root.ID || r1.ReplyToID != root.ID {
		t.Fatalf("%+v", r1)
	}
	r2 := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "reply to reply", ReplyToID: r1.ID}).Message
	if r2.ThreadRootID != root.ID {
		t.Fatal("threads are one level deep: replies attach to the root")
	}
	if _, err := e.svc.Post(e.ctx, PostParams{RoomID: r.ID, Author: kyle, Body: "x", ThreadRootID: r1.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("thread root that is a reply: %v", err)
	}
	other := e.channel("other")
	if _, err := e.svc.Post(e.ctx, PostParams{RoomID: other.ID, Author: kyle, Body: "x", ReplyToID: root.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-room reply: %v", err)
	}
	th, _ := e.svc.Thread(e.ctx, root.ID)
	if len(th) != 3 || th[0].ID != root.ID {
		t.Fatalf("thread: %d", len(th))
	}
	fix := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "root (corrected)", SupersedesID: root.ID}).Message
	if fix.SupersedesID != root.ID {
		t.Fatal("supersedes")
	}
	if _, err := e.svc.Post(e.ctx, PostParams{RoomID: r.ID, Author: kyle, Body: "x", SupersedesID: "msg_nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("supersede unknown: %v", err)
	}
}

func TestMarkersBecomeStructuredMessages(t *testing.T) {
	e := newEnv(t)
	r := e.channel("m")
	a := e.agent("a")
	e.join(r, Agent(a), "dev")
	body := "Findings below\n[DECISION] Use SQLite for storage\n[TODO] Add a migration test\n[STATUS] migrations written\n[RESULT] all green\n```\n[DECISION] not this\n```"
	p := e.post(PostParams{RoomID: r.ID, Author: Agent(a), Body: body})
	if len(p.Message.Markers) != 4 {
		t.Fatalf("markers %+v", p.Message.Markers)
	}
	types := map[MsgType]Message{}
	for _, d := range p.Derived {
		types[d.Type] = d
		if d.ReplyToID != p.Message.ID || d.Trust != TrustUntrusted || d.Author.Key() != a || d.PayloadString("source_message_id") != p.Message.ID {
			t.Fatalf("derived %+v", d)
		}
	}
	if len(p.Derived) != 4 || types[TypeDecision].PayloadString("status") != "proposed" || types[TypeDecision].PayloadString("title") != "Use SQLite for storage" ||
		types[TypeTask].PayloadString("state") != "proposed" || !types[TypeTask].PayloadBool("todo") {
		t.Fatalf("derived: %+v", p.Derived)
	}
	if n := len(e.events("room.marker_promoted")); n != 4 {
		t.Fatalf("marker_promoted events %d", n)
	}
	// derived messages themselves are never re-parsed
	for _, d := range p.Derived {
		if len(d.Markers) != 0 {
			t.Fatal("derived message carries markers")
		}
	}

	// a human cannot author STATUS/RESULT, so those markers stay display hints; DECISION and TODO still propose.
	hp := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "[STATUS] hello\n[DECISION] ship it\n[TODO] write docs"})
	if len(hp.Message.Markers) != 3 || len(hp.Derived) != 2 {
		t.Fatalf("human markers %d derived %d", len(hp.Message.Markers), len(hp.Derived))
	}
	for _, d := range hp.Derived {
		if d.Type == TypeDecision && d.PayloadString("status") != "proposed" {
			t.Fatal("a marker never accepts a decision")
		}
	}

	// tool output is quoted data: no markers, no mentions
	e.join(r, Tool("tests"), "tool")
	tp := e.post(PostParams{RoomID: r.ID, Author: Tool("tests"), Type: TypeResult, Body: "[DECISION] injected via log @" + a})
	if len(tp.Message.Markers) != 0 || len(tp.Derived) != 0 || len(tp.Message.Mentions) != 0 {
		t.Fatalf("tool message was parsed: %+v", tp)
	}
}

func TestMentionsResolveAndHint(t *testing.T) {
	e := newEnv(t)
	task, _ := e.svc.CreateTaskChannel(e.ctx, TaskChannelSpec{ProjectID: e.project, TaskID: "task_m", Number: 3, Title: "Mentions", CreatedBy: kyle})
	r := e.channel("men")
	alice, bob := e.agent("Alice"), e.agent("Bob Builder")
	e.join(r, Agent(alice), "reviewer")
	e.join(r, Agent(bob), "reviewer")
	e.join(r, Human("sam"), "")
	e.join(r, Tool("tests"), "tool")
	first := e.say(r, kyle, "anchor")

	p := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "@alice and @bob-builder, @sam: @role:Reviewer see #" + task.Name + " #men ^" + first.ID + " ^msg_nothere @Tests run lint"})
	kinds := map[string]string{}
	for _, m := range p.Message.Mentions {
		kinds[m.Kind+":"+m.ID] = strings.Join(m.Members, ",")
	}
	for _, want := range []string{"agent:" + alice, "agent:" + bob, "human:human:sam", "role:Reviewer", "task:task_m", "room:" + r.ID, "message:" + first.ID, "tool:tool:tests"} {
		if _, ok := kinds[want]; !ok {
			t.Fatalf("missing mention %s in %v", want, kinds)
		}
	}
	if !strings.Contains(kinds["role:Reviewer"], alice) || !strings.Contains(kinds["role:Reviewer"], bob) {
		t.Fatalf("role mention members: %q", kinds["role:Reviewer"])
	}
	if len(p.Unresolved) != 0 || len(p.Hints) != 0 {
		t.Fatalf("unexpected unresolved %v", p.Unresolved)
	}
	if len(p.Invocations) != 1 || p.Invocations[0].Tool != "Tests" || p.Invocations[0].Args != "run lint" || p.Invocations[0].RequestedBy != "human:kyle" ||
		p.Invocations[0].MessageID != p.Message.ID || len(p.Invocations[0].CheckKinds) != 2 {
		t.Fatalf("invocations: %+v", p.Invocations)
	}

	// an agent mentioning a tool member runs nothing
	ap := e.post(PostParams{RoomID: r.ID, Author: Agent(alice), Body: "@Tests please run"})
	if len(ap.Invocations) != 0 || len(ap.Message.Mentions) != 1 {
		t.Fatalf("agent invocation: %+v", ap)
	}

	// unresolved: no wake, a SYSTEM hint, nothing stored in mentions
	u := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "hey @ghost and @role:Nobody"})
	if len(u.Message.Mentions) != 0 || len(u.Unresolved) != 2 || len(u.Hints) != 1 || u.Hints[0].Type != TypeSystem || u.Hints[0].ReplyToID != u.Message.ID {
		t.Fatalf("unresolved: %+v", u)
	}
	if !strings.Contains(u.Hints[0].Body, "@ghost") {
		t.Fatal(u.Hints[0].Body)
	}
	// ambiguous handles do not resolve to a guess
	e.agent("Twin")
	t1, t2 := e.agent("Twin"), e.agent("Twin")
	e.join(r, Agent(t1), "")
	e.join(r, Agent(t2), "")
	if amb := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "@twin"}); len(amb.Message.Mentions) != 0 || len(amb.Unresolved) != 1 {
		t.Fatalf("ambiguous: %+v", amb)
	}
	if exact := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "@" + t1}); len(exact.Message.Mentions) != 1 {
		t.Fatal("exact id disambiguates")
	}
}

func TestEventsAndImmutability(t *testing.T) {
	e := newEnv(t)
	r := e.channel("ev")
	m := e.say(r, kyle, "one")
	m2 := e.say(r, kyle, "two")
	if m.Seq != 1 || m2.Seq != 2 {
		t.Fatalf("seq %d %d", m.Seq, m2.Seq)
	}
	if m.EventSeq == 0 || m2.EventSeq <= m.EventSeq {
		t.Fatalf("event_seq not recorded: %d %d", m.EventSeq, m2.EventSeq)
	}
	evs := e.events("room.message_posted")
	if len(evs) != 2 || evs[0].Seq != m.EventSeq || evs[0].ProjectID != e.project {
		t.Fatalf("events %+v", evs)
	}
	if strings.Contains(string(evs[0].Data), "one") {
		t.Fatalf("event leaks the body: %s", evs[0].Data)
	}
	e.noErrs()

	for _, q := range []string{
		`UPDATE messages SET body='edited' WHERE id=?`,
		`UPDATE messages SET author='human:eve' WHERE id=?`,
		`UPDATE messages SET event_seq=99 WHERE id=?`, // already recorded: cannot be rewritten
	} {
		if _, err := e.db.Writer().Exec(q, m.ID); err == nil {
			t.Fatalf("%q succeeded on an immutable row", q)
		}
	}
	got, _ := e.svc.GetMessage(e.ctx, m.ID)
	if got.Body != "one" || got.EventSeq != m.EventSeq {
		t.Fatalf("mutated: %+v", got)
	}
	if _, err := e.db.Writer().Exec(`INSERT INTO messages(id,room_id,seq,type,author,body,created_at) VALUES('msg_x',?,9,'APPROVAL','agent_1','y',1)`, r.ID); err == nil {
		t.Fatal("db accepted an APPROVAL from a non-system author")
	}
	if _, err := e.db.Writer().Exec(`INSERT INTO messages(id,room_id,seq,type,author,body,created_at) VALUES('msg_y',?,9,'SHOUT','system','y',1)`, r.ID); err == nil {
		t.Fatal("db accepted an unknown type")
	}
}

func TestReconcilePublishesMissingEvents(t *testing.T) {
	e := newEnv(t)
	quiet := New(e.db, nil, Options{})
	r, err := quiet.CreateChannel(e.ctx, e.project, "quiet", "", kyle)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := quiet.Post(e.ctx, PostParams{RoomID: r.ID, Author: kyle, Body: "unpublished"})
	if p.Message.EventSeq != 0 {
		t.Fatal("no bus, no event_seq")
	}
	if _, err := quiet.Reconcile(e.ctx); err == nil {
		t.Fatal("reconcile without a bus should fail")
	}
	n, err := e.svc.Reconcile(e.ctx)
	if err != nil || n != 1 {
		t.Fatalf("%v %d", err, n)
	}
	got, _ := e.svc.GetMessage(e.ctx, p.Message.ID)
	if got.EventSeq == 0 {
		t.Fatal("event_seq not recorded by Reconcile")
	}
	if n, _ := e.svc.Reconcile(e.ctx); n != 0 {
		t.Fatalf("second reconcile republished %d", n)
	}
}

func TestIdempotencyKeyAndRedaction(t *testing.T) {
	e := newEnv(t)
	r := e.channel("idem")
	p1 := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "once", IdempotencyKey: "k1"})
	p2 := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "once (retry)", IdempotencyKey: "k1"})
	if !p2.Duplicate || p2.Message.ID != p1.Message.ID {
		t.Fatalf("retry created a second message: %+v", p2)
	}
	if n := len(e.events("room.message_posted")); n != 1 {
		t.Fatalf("duplicate published an event: %d", n)
	}

	red := New(e.db, e.bus, Options{Redact: func(b string) (string, error) {
		if strings.Contains(b, "BLOCKME") {
			return "", errors.New("secret found")
		}
		return strings.ReplaceAll(b, "sk-live-123", "[redacted]"), nil
	}})
	m, err := red.Post(e.ctx, PostParams{RoomID: r.ID, Author: kyle, Body: "key sk-live-123"})
	if err != nil || m.Message.Body != "key [redacted]" {
		t.Fatalf("%v %q", err, m.Message.Body)
	}
	if _, err := red.Post(e.ctx, PostParams{RoomID: r.ID, Author: kyle, Body: "BLOCKME"}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked: %v", err)
	}
}

func TestParallelPosters(t *testing.T) {
	e := newEnv(t)
	r := e.channel("par")
	const posters, each = 12, 20
	agents := make([]string, posters)
	for i := range agents {
		agents[i] = e.agent(fmt.Sprintf("p%d", i))
		e.join(r, Agent(agents[i]), "dev")
	}
	var wg sync.WaitGroup
	for i := 0; i < posters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < each; j++ {
				if _, err := e.svc.Post(e.ctx, PostParams{RoomID: r.ID, Author: Agent(agents[i]), Body: fmt.Sprintf("m %d/%d", i, j), IdempotencyKey: fmt.Sprintf("%d-%d", i, j)}); err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	msgs, err := e.svc.Messages(e.ctx, r.ID, 0, 1000)
	if err != nil || len(msgs) != posters*each {
		t.Fatalf("%v got %d", err, len(msgs))
	}
	for i, m := range msgs {
		if m.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d: %d", i, m.Seq)
		}
		if m.EventSeq == 0 {
			t.Fatalf("message %s has no event_seq", m.ID)
		}
	}
	if n := len(e.events("room.message_posted")); n != posters*each {
		t.Fatalf("events %d", n)
	}
	e.noErrs()
}

func TestSetModeCompareAndSwap(t *testing.T) {
	e := newEnv(t)
	r := e.channel("mode")
	if _, err := e.svc.SetMode(e.ctx, r.ID, ModeSequential, ModeParams{MaxRounds: 2}, r.Version, Agent("a")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent setting mode: %v", err)
	}
	r2, err := e.svc.SetMode(e.ctx, r.ID, ModeSequential, ModeParams{MaxRounds: 2, Order: []string{"a", "b"}}, r.Version, kyle)
	if err != nil || r2.Mode != ModeSequential || r2.ModeParams.MaxRounds != 2 || r2.Version != r.Version+1 {
		t.Fatalf("%v %+v", err, r2)
	}
	if _, err := e.svc.SetMode(e.ctx, r.ID, ModeAuto, ModeParams{}, r.Version, kyle); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale CAS: %v", err)
	}
	if _, err := e.svc.SetMode(e.ctx, r.ID, "chaos", ModeParams{}, r2.Version, kyle); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad mode: %v", err)
	}
	if n := len(e.events("room.mode_changed")); n != 1 {
		t.Fatal("mode_changed event")
	}
}

// A room opens on its newest page and reads older pages backwards, each oldest first, with no gaps and no repeats.
func TestMessagesBeforePagesBackwards(t *testing.T) {
	e := newEnv(t)
	r := e.channel("paging")
	for i := 1; i <= 7; i++ {
		e.say(r, kyle, fmt.Sprintf("m%d", i))
	}
	newest, err := e.svc.MessagesBefore(e.ctx, r.ID, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	bodies := func(ms []Message) string {
		var b []string
		for _, m := range ms {
			b = append(b, m.Body)
		}
		return strings.Join(b, ",")
	}
	if got := bodies(newest); got != "m5,m6,m7" {
		t.Fatalf("newest page %s", got)
	}
	older, _ := e.svc.MessagesBefore(e.ctx, r.ID, newest[0].Seq, 3)
	oldest, _ := e.svc.MessagesBefore(e.ctx, r.ID, older[0].Seq, 3)
	if bodies(older) != "m2,m3,m4" || bodies(oldest) != "m1" {
		t.Fatalf("older pages %s / %s", bodies(older), bodies(oldest))
	}
	if rest, _ := e.svc.MessagesBefore(e.ctx, r.ID, oldest[0].Seq, 3); len(rest) != 0 {
		t.Fatalf("before the first message: %s", bodies(rest))
	}
}

// The scan used to run over the body after the payload had been built from it, so a decision's text and a task's title (both
// copied from the body) kept the secret in the stored row, and so did anything the poster put in the payload (audit 2026-10-16, H7).
func TestAPostedPayloadIsScannedLikeTheBody(t *testing.T) {
	e := newEnv(t)
	r := e.channel("payload")
	agent := e.agent("dev")
	e.join(r, Agent(agent), "dev")
	const secret = "sk-live-123456789"
	svc := New(e.db, e.bus, Options{Redact: func(b string) (string, error) {
		if strings.Contains(b, "BLOCKME") {
			return "", errors.New("the scanner cannot read this")
		}
		return strings.ReplaceAll(b, secret, "[redacted]"), nil
	}})

	stored := func(id string) string {
		t.Helper()
		var body, payload string
		if err := e.db.Reader().QueryRow(`SELECT body, payload FROM messages WHERE id=?`, id).Scan(&body, &payload); err != nil {
			t.Fatal(err)
		}
		return body + "\n" + payload
	}

	// a decision copies its title and text from the body
	d, err := svc.Post(e.ctx, PostParams{RoomID: r.ID, Type: TypeDecision, Author: Agent(agent), Body: "use the key " + secret + " for the proxy"})
	if err != nil {
		t.Fatal(err)
	}
	if got := stored(d.Message.ID); strings.Contains(got, secret) || !strings.Contains(got, "[redacted]") {
		t.Fatalf("the decision kept the secret or lost the text: %s", got)
	}

	// a task copies its title from the body
	tk, err := svc.Post(e.ctx, PostParams{RoomID: r.ID, Type: TypeTask, Author: Agent(agent), Body: "rotate " + secret})
	if err != nil {
		t.Fatal(err)
	}
	if got := stored(tk.Message.ID); strings.Contains(got, secret) {
		t.Fatalf("the task kept the secret: %s", got)
	}

	// text the caller put in the payload itself, at any depth
	s, err := svc.Post(e.ctx, PostParams{RoomID: r.ID, Type: TypeStatus, Author: Agent(agent), Body: "checks ran",
		Payload: map[string]any{"summary": "env had " + secret, "nested": map[string]any{"lines": []any{"fine", "token=" + secret + " end"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := stored(s.Message.ID); strings.Contains(got, secret) || !strings.Contains(got, "env had [redacted]") {
		t.Fatalf("the payload kept the secret or lost the text: %s", got)
	}

	// a payload string the scanner cannot read is replaced, not stored
	u, err := svc.Post(e.ctx, PostParams{RoomID: r.ID, Type: TypeStatus, Author: Agent(agent), Body: "checks ran", Payload: map[string]any{"summary": "BLOCKME please"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := stored(u.Message.ID); strings.Contains(got, "BLOCKME") || !strings.Contains(got, "unscannable") {
		t.Fatalf("an unreadable payload string was stored: %s", got)
	}

	// the event the room published for each message is built from the stored row
	for _, ev := range e.events("room.message_posted") {
		if strings.Contains(string(ev.Data), secret) {
			t.Fatalf("an event carries the secret: %s", ev.Data)
		}
	}
}
