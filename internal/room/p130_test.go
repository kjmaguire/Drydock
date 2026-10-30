package room

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/storage"
)

// The scheduler completes a granted turn through its own call (decision P130, room-protocol s6 Complete): the grant records
// how the turn ended and its reply, room.turn_completed says so, the reply is the message the next speakers are planned from,
// a grant is completed once, and a grant completed before its agent started can no longer start a turn.
func TestCompleteClosesTheGrantAndSaysWhatFollows(t *testing.T) {
	e := newEnv(t)
	r := e.channel("design")
	a, b := e.agent("Ada"), e.agent("Bo")
	e.join(r, Agent(a), "architect")
	e.join(r, Agent(b), "backend")
	q := e.say(r, kyle, "@"+a+" how should we store sessions?")
	_, grants, err := e.svc.GrantTurns(e.ctx, q.ID, ContextBounds{})
	if err != nil || len(grants) != 1 {
		t.Fatalf("%v %v", grants, err)
	}
	g := grants[a]
	if err := e.svc.RedeemGrant(e.ctx, g, r.ID, a, "arun_1"); err != nil {
		t.Fatal(err)
	}
	reply := e.post(PostParams{RoomID: r.ID, Author: Agent(a), Body: "In SQLite. @" + b + " agree?", ReplyToID: q.ID}).Message
	// a reply that is not the agent's own is refused: the grant stays open
	if _, err := e.svc.Complete(e.ctx, g, TurnResult{Status: OutcomeDone, MessageID: q.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("someone else's message as the reply: %v", err)
	}
	if _, err := e.svc.Complete(e.ctx, g, TurnResult{Status: "great"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unknown outcome: %v", err)
	}
	c, err := e.svc.Complete(e.ctx, g, TurnResult{Status: OutcomeDone, MessageID: reply.ID, RunID: "arun_1"})
	if err != nil || c.Next != reply.ID || c.Round != 1 || c.MaxRounds != DefaultMaxRounds || c.Status != OutcomeDone {
		t.Fatalf("%+v %v", c, err)
	}
	if out, rep, _ := e.svc.GrantOutcome(e.ctx, g.ID); out != OutcomeDone || rep != reply.ID {
		t.Fatalf("stored %q %q", out, rep)
	}
	if _, err := e.svc.Complete(e.ctx, g, TurnResult{Status: OutcomeDone, MessageID: reply.ID}); !errors.Is(err, ErrGrantRefused) {
		t.Fatalf("completed twice: %v", err)
	}
	evs := e.events("room.turn_completed")
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), `"status":"done"`) || !strings.Contains(string(evs[0].Data), reply.ID) || evs[0].ProjectID != e.project {
		t.Fatalf("%+v", evs)
	}
	// the reply goes on to the speaker it names
	_, next, err := e.svc.GrantTurns(e.ctx, c.Next, ContextBounds{})
	if err != nil || next[b].ID == "" {
		t.Fatalf("the next speaker: %v %v", next, err)
	}
	// a turn that fails before its agent starts closes its grant: it can no longer start one
	c, err = e.svc.Complete(e.ctx, next[b], TurnResult{Status: OutcomeFailed, Error: "budget"})
	if err != nil || c.Next != "" {
		t.Fatalf("%+v %v", c, err)
	}
	if err := e.svc.RedeemGrant(e.ctx, next[b], r.ID, b, "arun_2"); !errors.Is(err, ErrGrantRefused) {
		t.Fatalf("a completed grant was redeemed: %v", err)
	}
	if _, err := e.svc.Complete(e.ctx, TurnGrant{}, TurnResult{Status: OutcomeDone}); !errors.Is(err, ErrNoGrant) {
		t.Fatal(err)
	}
	e.noErrs()
}

// A fallback hop's renewal closes the used grant as renewed and can be made once; at start, grants whose turn started and never
// ended are completed as interrupted.
func TestARenewalClosesTheUsedGrantAndOpenGrantsAreInterruptedAtStart(t *testing.T) {
	e := newEnv(t)
	r := e.channel("design")
	a := e.agent("Ada")
	e.join(r, Agent(a), "architect")
	q := e.say(r, kyle, "@"+a+" hello")
	_, grants, _ := e.svc.GrantTurns(e.ctx, q.ID, ContextBounds{})
	g := grants[a]
	if err := e.svc.RedeemGrant(e.ctx, g, r.ID, a, "arun_1"); err != nil {
		t.Fatal(err)
	}
	g2, err := e.svc.RenewGrant(e.ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if out, _, _ := e.svc.GrantOutcome(e.ctx, g.ID); out != OutcomeRenewed {
		t.Fatalf("the used grant: %q", out)
	}
	if _, err := e.svc.RenewGrant(e.ctx, g); !errors.Is(err, ErrGrantRefused) {
		t.Fatalf("renewed twice: %v", err)
	}
	if err := e.svc.RedeemGrant(e.ctx, g2, r.ID, a, "arun_2"); err != nil {
		t.Fatal(err)
	}
	n, err := e.svc.InterruptOpenGrants(e.ctx)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if out, _, _ := e.svc.GrantOutcome(e.ctx, g2.ID); out != OutcomeInterrupted {
		t.Fatalf("the open grant: %q", out)
	}
}

// A muted room raises neither its unread count nor an Inbox item, except a message that names the person (not a role the
// person holds); unmuted, every message counts again.
func TestAMutedRoomCountsOnlyMentionsByName(t *testing.T) {
	e := newEnv(t)
	r := e.channel("noisy")
	a := e.agent("Ada")
	e.join(r, kyle, "owner")
	e.join(r, Agent(a), "architect")
	e.say(r, Agent(a), "chatter one")
	e.say(r, Agent(a), "@role:owner please look") // a role mention reaches the person through the role
	named := e.say(r, Agent(a), "@kyle please look")
	count := func() Unread {
		t.Helper()
		us, err := e.svc.UnreadCounts(e.ctx, kyle, e.project)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range us {
			if u.RoomID == r.ID {
				return u
			}
		}
		t.Fatal("no unread row")
		return Unread{}
	}
	mentionKeys := func() []string {
		t.Helper()
		items, err := e.svc.NeedsYou(e.ctx, e.project)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, it := range items {
			if it.Kind == NeedsMention {
				keys = append(keys, it.Key)
			}
		}
		return keys
	}
	if u := count(); u.Unread != 3 || u.Mentions != 2 || u.Muted {
		t.Fatalf("unmuted: %+v", u)
	}
	if k := mentionKeys(); len(k) != 2 {
		t.Fatalf("unmuted mention items: %v", k)
	}
	if err := e.svc.SetMemberState(e.ctx, r.ID, kyle, StateMuted, kyle); err != nil {
		t.Fatal(err)
	}
	if u := count(); u.Unread != 1 || u.Mentions != 1 || !u.Muted {
		t.Fatalf("muted: %+v", u)
	}
	if k := mentionKeys(); len(k) != 1 || k[0] != "mention:"+named.ID {
		t.Fatalf("muted mention items: %v", k)
	}
	// read: the item goes
	if _, err := e.svc.MarkRead(e.ctx, r.ID, kyle, named.Seq); err != nil {
		t.Fatal(err)
	}
	if k := mentionKeys(); len(k) != 0 {
		t.Fatalf("read mentions stay: %v", k)
	}
	e.noErrs()
}

// The P130 Inbox items: a task waiting for a person (left out while another item of the task says why), an unread mention of
// the person (left out when its message is a blocking question's item already, gone once read), each saying where to act.
func TestTaskWaitingAndMentionItemsFollowTheRuleAgainstDuplicates(t *testing.T) {
	e := newEnv(t)
	now := storage.Now()
	e.exec(`INSERT INTO tasks(id,project_id,seq,title,status,created_at,updated_at,blocked_reason) VALUES('task_w',?,7,'Fix the login','WAITING_HUMAN',?,?,'The iteration limit was reached')`, e.project, now, now)
	e.exec(`INSERT INTO tasks(id,project_id,seq,title,status,created_at,updated_at) VALUES('task_q',?,8,'Add a cache','WAITING_HUMAN',?,?)`, e.project, now, now)
	e.exec(`INSERT INTO tasks(id,project_id,seq,title,status,created_at,updated_at) VALUES('task_r',?,9,'Ready one','READY',?,?)`, e.project, now, now)
	// task_q waits on its own approval row: that item is the cause, and the task's item is left out
	e.exec(`INSERT INTO approvals(id,task_id,project_id,kind,title,action_hash,status,decision,decided_by,created_at) VALUES('ap_q','task_q',?,'review','Accept the spec','h','REQUESTED','','',?)`, e.project, now)
	r := e.channel("general")
	a := e.agent("Ada")
	e.join(r, kyle, "owner")
	e.join(r, Agent(a), "dev")
	m := e.say(r, Agent(a), "@kyle can you check the README?")
	q := e.post(PostParams{RoomID: r.ID, Type: TypeQuestion, Author: Agent(a), Body: "@kyle which port?", Payload: map[string]any{"blocking": true, "to": "human:kyle"}}).Message
	items, err := e.svc.NeedsYou(e.ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]NeedsYouItem{}
	for _, it := range items {
		byKey[it.Key] = it
	}
	w, ok := byKey["task:task_w"]
	if !ok || w.Kind != NeedsTaskWaiting || w.TaskID != "task_w" || w.TaskSeq != 7 || w.Detail != "The iteration limit was reached" || !strings.Contains(w.Title, "Fix the login") {
		t.Fatalf("the waiting task: %+v", items)
	}
	if _, dup := byKey["task:task_q"]; dup {
		t.Fatal("a task waiting on its approval has a second item")
	}
	if _, ok := byKey["approval:ap_q"]; !ok {
		t.Fatal("the approval item")
	}
	if _, ok := byKey["task:task_r"]; ok {
		t.Fatal("a READY task is not waiting")
	}
	mi, ok := byKey["mention:"+m.ID]
	if !ok || mi.Kind != NeedsMention || mi.RoomID != r.ID || mi.AgentName != "Ada" || mi.Detail != "Mentioned you in #general." {
		t.Fatalf("the mention: %+v", items)
	}
	if _, dup := byKey["mention:"+q.ID]; dup {
		t.Fatal("a blocking question has a mention item too")
	}
	if _, ok := byKey["question:"+q.ID]; !ok {
		t.Fatal("the question item")
	}
	// the task moves: its item goes
	e.exec(`UPDATE tasks SET status='IN_PROGRESS' WHERE id='task_w'`)
	items, _ = e.svc.NeedsYou(e.ctx, e.project)
	for _, it := range items {
		if it.Key == "task:task_w" {
			t.Fatal("the item stayed after the task moved")
		}
	}
}

// A join rule adds its agent to the task rooms created after it whose task names a file under its prefix, as an observer
// unless the person chose active; never to a channel; one SYSTEM line says why. Only a person makes or removes a rule.
func TestJoinRulesAddTheirAgentToMatchingTaskRooms(t *testing.T) {
	e := newEnv(t)
	sec := e.agent("Sec")
	rev := e.agent("Rev")
	if _, err := e.svc.AddJoinRule(e.ctx, JoinRule{ProjectID: e.project, AgentID: sec, PathPrefix: "auth/"}, Agent(rev)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an agent made a rule: %v", err)
	}
	if _, err := e.svc.AddJoinRule(e.ctx, JoinRule{ProjectID: e.project, AgentID: sec, PathPrefix: "../etc"}, kyle); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a prefix outside the project: %v", err)
	}
	if _, err := e.svc.AddJoinRule(e.ctx, JoinRule{ProjectID: e.project, AgentID: "agt_other"}, kyle); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("an agent of no project: %v", err)
	}
	r1, err := e.svc.AddJoinRule(e.ctx, JoinRule{ProjectID: e.project, AgentID: sec, PathPrefix: `.\auth\`}, kyle)
	if err != nil || r1.PathPrefix != "auth/" || r1.State != StateObserver {
		t.Fatalf("%+v %v", r1, err)
	}
	if _, err := e.svc.AddJoinRule(e.ctx, JoinRule{ProjectID: e.project, AgentID: rev, State: StateActive}, kyle); err != nil {
		t.Fatal(err)
	}
	task := func(id string, seq int, files []string) Room {
		t.Helper()
		b, _ := json.Marshal(files)
		now := storage.Now()
		e.exec(`INSERT INTO tasks(id,project_id,seq,title,status,files,created_at,updated_at) VALUES(?,?,?,?,'READY',?,?,?)`, id, e.project, seq, "t "+id, string(b), now, now)
		rm, err := e.svc.CreateTaskChannel(e.ctx, TaskChannelSpec{ProjectID: e.project, TaskID: id, Number: seq, Title: "t " + id, CreatedBy: kyle})
		if err != nil {
			t.Fatal(err)
		}
		return rm
	}
	state := func(rm Room, agent string) MemberState {
		ms, _ := e.svc.Members(e.ctx, rm.ID)
		for _, m := range ms {
			if m.Author.ID == agent {
				return m.State
			}
		}
		return ""
	}
	touching := task("task_a", 1, []string{"auth/login.go", "README.md"})
	elsewhere := task("task_b", 2, []string{"web/app.ts"})
	if state(touching, sec) != StateObserver || state(elsewhere, sec) != "" {
		t.Fatalf("the prefix rule: %q %q", state(touching, sec), state(elsewhere, sec))
	}
	if state(touching, rev) != StateActive || state(elsewhere, rev) != StateActive {
		t.Fatal("the rule for every task room")
	}
	var lines int
	for _, m := range e.messagesOf(touching) {
		if m.Type == TypeSystem && strings.Contains(m.Body, "by a join rule") {
			lines++
		}
	}
	if lines != 2 {
		t.Fatalf("%d SYSTEM lines", lines)
	}
	if ch := e.channel("architecture"); state(ch, rev) != "" {
		t.Fatal("a rule reached a channel")
	}
	if err := e.svc.RemoveJoinRule(e.ctx, r1.ID, Agent(rev)); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if err := e.svc.RemoveJoinRule(e.ctx, r1.ID, kyle); err != nil {
		t.Fatal(err)
	}
	if later := task("task_c", 3, []string{"auth/x.go"}); state(later, sec) != "" {
		t.Fatal("a removed rule still applies")
	}
	if rs, _ := e.svc.JoinRules(e.ctx, e.project); len(rs) != 1 {
		t.Fatalf("%+v", rs)
	}
	e.noErrs()
}

// An agent asks to join a channel: the request is one Inbox item (asking again returns it), redacted, until a person lets the
// agent in (an observer) or declines; it never joins by itself, and a task room, a DM or a channel it is in cannot be asked for.
func TestARequestToJoinIsAnInboxItemUntilAPersonAnswers(t *testing.T) {
	e := newEnv(t)
	e.svc.opt.Redact = func(s string) (string, error) { return strings.ReplaceAll(s, "sk-secret", "[REDACTED]"), nil }
	ch := e.channel("architecture")
	a := e.agent("Ada")
	jr, err := e.svc.RequestJoin(e.ctx, ch.ID, a, "I review   designs; my key is sk-secret")
	if err != nil || jr.Status != JoinRequested || jr.Reason != "I review designs; my key is [REDACTED]" {
		t.Fatalf("%+v %v", jr, err)
	}
	again, err := e.svc.RequestJoin(e.ctx, ch.ID, a, "again")
	if err != nil || again.ID != jr.ID {
		t.Fatalf("asked twice: %+v %v", again, err)
	}
	joins := func() []NeedsYouItem {
		items, err := e.svc.NeedsYou(e.ctx, e.project)
		if err != nil {
			t.Fatal(err)
		}
		var out []NeedsYouItem
		for _, it := range items {
			if it.Kind == NeedsJoinRequest {
				out = append(out, it)
			}
		}
		return out
	}
	if js := joins(); len(js) != 1 || js[0].JoinRequestID != jr.ID || js[0].Title != "Asks to join #architecture" || js[0].AgentName != "Ada" || js[0].RoomID != ch.ID {
		t.Fatalf("%+v", js)
	}
	if ok, _ := e.svc.IsMember(e.ctx, ch.ID, Agent(a)); ok {
		t.Fatal("the agent joined by asking")
	}
	if _, err := e.svc.AnswerJoin(e.ctx, jr.ID, true, Agent(a)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an agent answered: %v", err)
	}
	got, err := e.svc.AnswerJoin(e.ctx, jr.ID, true, kyle)
	if err != nil || got.Status != JoinGranted {
		t.Fatalf("%+v %v", got, err)
	}
	ms, _ := e.svc.Members(e.ctx, ch.ID)
	var st MemberState
	for _, m := range ms {
		if m.Author.ID == a {
			st = m.State
		}
	}
	if st != StateObserver || len(joins()) != 0 {
		t.Fatalf("granted: %q %v", st, joins())
	}
	if _, err := e.svc.AnswerJoin(e.ctx, jr.ID, false, kyle); !errors.Is(err, ErrJoinRequest) {
		t.Fatalf("answered twice: %v", err)
	}
	if _, err := e.svc.RequestJoin(e.ctx, ch.ID, a, ""); !errors.Is(err, ErrJoinRequest) {
		t.Fatalf("asked to join a channel it is in: %v", err)
	}
	b := e.agent("Bo")
	jb, _ := e.svc.RequestJoin(e.ctx, ch.ID, b, "")
	if d, err := e.svc.AnswerJoin(e.ctx, jb.ID, false, kyle); err != nil || d.Status != JoinDeclined {
		t.Fatalf("%+v %v", d, err)
	}
	if ok, _ := e.svc.IsMember(e.ctx, ch.ID, Agent(b)); ok || len(joins()) != 0 {
		t.Fatal("a declined agent joined")
	}
	dm, _ := e.svc.CreateDM(e.ctx, e.project, kyle, Agent(b))
	if _, err := e.svc.RequestJoin(e.ctx, dm.ID, a, ""); !errors.Is(err, ErrJoinRequest) {
		t.Fatalf("asked to join a DM: %v", err)
	}
	if dm.AgentID != b {
		t.Fatalf("the DM's agent: %q", dm.AgentID)
	}
	if got, _ := e.svc.GetRoom(e.ctx, dm.ID); got.AgentID != b {
		t.Fatalf("the DM's record: %+v", got)
	}
	if got, _ := e.svc.GetRoom(e.ctx, ch.ID); got.AgentID != "" {
		t.Fatal("a channel has an agent id")
	}
	e.noErrs()
}

func (e *env) messagesOf(r Room) []Message {
	e.t.Helper()
	ms, err := queryMessages(e.ctx, e.db.Reader(), `room_id=? ORDER BY seq`, r.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return ms
}
