package room

import (
	"errors"
	"reflect"
	"testing"
)

func members(spec ...string) []TurnMember {
	// spec entries: "key:role:state" ; kind is derived from the key
	var out []TurnMember
	for _, s := range spec {
		parts := splitN(s, 3)
		m := TurnMember{Key: parts[0], Role: parts[1], State: MemberState(parts[2])}
		m.Kind = ParseAuthor(m.Key).Kind
		if m.State == "" {
			m.State = StateActive
		}
		out = append(out, m)
	}
	return out
}

func splitN(s string, n int) []string {
	out := make([]string, n)
	i := 0
	cur := ""
	for _, r := range s {
		if r == ':' && i < n-1 && !(i == 0 && (cur == "human" || cur == "tool")) {
			out[i] = cur
			cur = ""
			i++
			continue
		}
		cur += string(r)
	}
	out[i] = cur
	// "human:kyle:owner:" style keys: re-join the prefix
	return out
}

func keysOf(sp []Speaker) []string {
	var out []string
	for _, s := range sp {
		out = append(out, s.Member)
	}
	return out
}

func TestNextSpeakersAuto(t *testing.T) {
	ms := members("a1:dev:", "a2:reviewer:", "a3:reviewer:", "obs:dev:observer", "mut:dev:muted", "human:kyle::", "tool:tests:tool:")
	hum := Human("kyle")
	cases := []struct {
		name    string
		in      TurnInput
		want    []string
		why     GrantReason
		bounded bool
	}{
		{"explicit mention wins over owner", TurnInput{Trigger: TurnTrigger{Author: hum, Type: TypeChat, Mentioned: []string{"a2"}, AssigneeKey: "a1"}}, []string{"a2"}, ReasonMention, false},
		{"owner when nobody is mentioned", TurnInput{Trigger: TurnTrigger{Author: hum, Type: TypeChat, AssigneeKey: "a1"}}, []string{"a1"}, ReasonOwner, false},
		{"role match after owner", TurnInput{Trigger: TurnTrigger{Author: hum, Type: TypeHandoff, RoleHint: "Reviewer"}}, []string{"a2"}, ReasonRole, false},
		{"nobody when nothing matches", TurnInput{Trigger: TurnTrigger{Author: hum, Type: TypeChat}}, nil, "", false},
		{"owner not a member is skipped, falls to role", TurnInput{Trigger: TurnTrigger{Author: hum, Type: TypeChat, AssigneeKey: "ghost", RoleHint: "reviewer"}}, []string{"a2"}, ReasonRole, false},
		{"author is never granted its own message", TurnInput{Trigger: TurnTrigger{Author: Agent("a1"), Type: TypeChat, Mentioned: []string{"a1"}, AssigneeKey: "a1"}}, nil, "", false},
		{"observer only when mentioned", TurnInput{Trigger: TurnTrigger{Author: hum, Type: TypeChat, Mentioned: []string{"obs"}}}, []string{"obs"}, ReasonMention, false},
		{"observer is not the owner fallback", TurnInput{Trigger: TurnTrigger{Author: hum, Type: TypeChat, AssigneeKey: "obs"}}, nil, "", false},
		{"muted never speaks", TurnInput{Trigger: TurnTrigger{Author: hum, Type: TypeChat, Mentioned: []string{"mut"}}}, nil, "", false},
		{"humans and tools are not agents", TurnInput{Trigger: TurnTrigger{Author: hum, Type: TypeChat, Mentioned: []string{"human:kyle", "tool:tests"}}}, nil, "", false},
		{"one speaker by default, rest deferred", TurnInput{Trigger: TurnTrigger{Author: hum, Type: TypeChat, Mentioned: []string{"a2", "a3"}}}, []string{"a2"}, ReasonMention, false},
		{"parallel allowed by the rule set", TurnInput{Params: ModeParams{MaxParallel: 2}, Trigger: TurnTrigger{Author: hum, Type: TypeChat, Mentioned: []string{"a2", "a3", "a1"}}}, []string{"a2", "a3"}, ReasonMention, false},
		{"status traffic does not wake the owner", TurnInput{Trigger: TurnTrigger{Author: Agent("a2"), Type: TypeStatus, AssigneeKey: "a1"}}, nil, "", false},
		{"a mention in a status message still wakes", TurnInput{Trigger: TurnTrigger{Author: Agent("a2"), Type: TypeStatus, Mentioned: []string{"a1"}}}, []string{"a1"}, ReasonMention, false},
		{"system messages never wake", TurnInput{Trigger: TurnTrigger{Author: System, Type: TypeSystem, Mentioned: []string{"a1"}, AssigneeKey: "a1"}}, nil, "", false},
		{"approval messages never wake", TurnInput{Trigger: TurnTrigger{Author: System, Type: TypeApproval, Mentioned: []string{"a1"}}}, nil, "", false},
		{"tool result wakes the owner", TurnInput{Trigger: TurnTrigger{Author: Tool("tests"), Type: TypeError, AssigneeKey: "a1"}}, []string{"a1"}, ReasonOwner, false},
		{"agent reply within the bound", TurnInput{Rounds: 2, Trigger: TurnTrigger{Author: Agent("a1"), Type: TypeChat, Mentioned: []string{"a2"}}}, []string{"a2"}, ReasonMention, false},
		{"agent reply at the bound is stopped", TurnInput{Rounds: 3, Trigger: TurnTrigger{Author: Agent("a1"), Type: TypeChat, Mentioned: []string{"a2"}}}, nil, "", true},
		{"custom bound", TurnInput{Rounds: 2, Params: ModeParams{MaxRounds: 2}, Trigger: TurnTrigger{Author: Agent("a1"), Type: TypeChat, Mentioned: []string{"a2"}}}, nil, "", true},
		{"a human message preempts the bound", TurnInput{Rounds: 9, Trigger: TurnTrigger{Author: hum, Type: TypeChat, Mentioned: []string{"a2"}}}, []string{"a2"}, ReasonMention, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.Mode = ModeAuto
			c.in.Members = ms
			d := NextSpeakers(c.in)
			if !reflect.DeepEqual(keysOf(d.Speakers), c.want) || d.Bounded != c.bounded {
				t.Fatalf("got %v bounded=%v (%s) want %v bounded=%v", keysOf(d.Speakers), d.Bounded, d.Why, c.want, c.bounded)
			}
			for _, s := range d.Speakers {
				if s.Reason != c.why {
					t.Fatalf("reason %s want %s", s.Reason, c.why)
				}
			}
		})
	}
	d := NextSpeakers(TurnInput{Mode: ModeAuto, Members: ms, Trigger: TurnTrigger{Author: hum, Type: TypeChat, Mentioned: []string{"a2", "a3"}}})
	if !reflect.DeepEqual(d.Deferred, []string{"a3"}) {
		t.Fatalf("deferred %v", d.Deferred)
	}
}

func TestNextSpeakersSequential(t *testing.T) {
	ms := members("a:dev:", "b:dev:", "c:dev:", "d:dev:muted", "human:kyle::")
	hum := Human("kyle")
	base := TurnInput{Mode: ModeSequential, Members: ms}
	cases := []struct {
		name      string
		spoken    []string
		completed int
		author    Author
		want      []string
		newRound  bool
		round     int
		bounded   bool
	}{
		{"human starts the first pass", nil, 0, hum, []string{"a"}, false, 1, false},
		{"human preempts mid-debate", []string{"a", "b"}, 2, hum, []string{"a"}, false, 1, false},
		{"after a", []string{"a"}, 0, Agent("a"), []string{"b"}, false, 1, false},
		{"after b", []string{"a", "b"}, 0, Agent("b"), []string{"c"}, false, 1, false},
		{"last speaker completes the pass: new round starts", nil, 1, Agent("c"), []string{"a"}, true, 2, false},
		{"second pass in progress", []string{"a"}, 1, Agent("a"), []string{"b"}, false, 2, false},
		{"third pass complete hits the bound", nil, 3, Agent("c"), nil, false, 3, true},
		{"a passed member is skipped for the round", []string{"b"}, 0, Agent("b"), []string{"a"}, false, 1, false}, // b passed before a spoke (order a,b,c)
		{"tool results do not advance the order", []string{"a"}, 0, Tool("tests"), nil, false, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			in.Spoken, in.RoundsCompleted = c.spoken, c.completed
			in.Trigger = TurnTrigger{Author: c.author, Type: TypeChat, Mentioned: []string{"c"}} // mentions never jump the order
			d := NextSpeakers(in)
			if !reflect.DeepEqual(keysOf(d.Speakers), c.want) || d.Bounded != c.bounded || d.NewRound != c.newRound || (c.round != 0 && d.Round != c.round) {
				t.Fatalf("got %v bounded=%v new=%v round=%d (%s)", keysOf(d.Speakers), d.Bounded, d.NewRound, d.Round, d.Why)
			}
		})
	}
	// explicit order overrides join order and ignores non-members and muted members
	in := base
	in.Params = ModeParams{Order: []string{"c", "ghost", "d", "a"}}
	in.Trigger = TurnTrigger{Author: hum, Type: TypeChat}
	if got := keysOf(NextSpeakers(in).Speakers); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("explicit order: %v", got)
	}
	in.Spoken, in.Trigger = []string{"c"}, TurnTrigger{Author: Agent("c"), Type: TypeChat}
	if got := keysOf(NextSpeakers(in).Speakers); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("explicit order next: %v", got)
	}
	// a single-member order never talks to itself
	solo := TurnInput{Mode: ModeSequential, Members: members("a:dev:"), Trigger: TurnTrigger{Author: Agent("a"), Type: TypeChat}, RoundsCompleted: 1}
	if d := NextSpeakers(solo); len(d.Speakers) != 0 {
		t.Fatalf("solo: %+v", d)
	}
}

func TestNextSpeakersModerator(t *testing.T) {
	ms := members("mod:pm:", "a:dev:", "b:dev:", "human:kyle::")
	hum := Human("kyle")
	cases := []struct {
		name    string
		params  ModeParams
		mem     []TurnMember
		trig    TurnTrigger
		rounds  int
		want    []string
		bounded bool
	}{
		{"moderator names the next speaker", ModeParams{Moderator: "mod"}, ms, TurnTrigger{Author: Agent("mod"), Type: TypeChat, Mentioned: []string{"b"}}, 0, []string{"b"}, false},
		{"moderator without a name wakes nobody", ModeParams{Moderator: "mod"}, ms, TurnTrigger{Author: Agent("mod"), Type: TypeChat}, 0, nil, false},
		{"moderator cannot name itself", ModeParams{Moderator: "mod"}, ms, TurnTrigger{Author: Agent("mod"), Type: TypeChat, Mentioned: []string{"mod", "a"}}, 0, []string{"a"}, false},
		{"a worker's reply goes back to the moderator", ModeParams{Moderator: "mod"}, ms, TurnTrigger{Author: Agent("a"), Type: TypeChat, Mentioned: []string{"b"}}, 1, []string{"mod"}, false},
		{"workers cannot invite each other", ModeParams{Moderator: "mod"}, members("a:dev:", "b:dev:"), TurnTrigger{Author: Agent("a"), Type: TypeChat, Mentioned: []string{"b"}}, 0, nil, false},
		{"human message goes to the moderator", ModeParams{Moderator: "mod"}, ms, TurnTrigger{Author: hum, Type: TypeChat}, 0, []string{"mod"}, false},
		{"human may invoke a speaker directly", ModeParams{Moderator: "mod"}, ms, TurnTrigger{Author: hum, Type: TypeChat, Mentioned: []string{"a"}}, 0, []string{"a"}, false},
		{"human moderator: agent replies wake nobody", ModeParams{Moderator: "human:kyle"}, ms, TurnTrigger{Author: Agent("a"), Type: TypeChat}, 0, nil, false},
		{"human moderator names speakers", ModeParams{Moderator: "human:kyle"}, ms, TurnTrigger{Author: hum, Type: TypeChat, Mentioned: []string{"a", "b"}}, 0, []string{"a"}, false},
		{"bounded debate stops the moderator loop", ModeParams{Moderator: "mod"}, ms, TurnTrigger{Author: Agent("a"), Type: TypeChat}, 3, nil, true},
		{"tool results do not wake the moderator", ModeParams{Moderator: "mod"}, ms, TurnTrigger{Author: Tool("tests"), Type: TypeError}, 0, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := NextSpeakers(TurnInput{Mode: ModeModerator, Params: c.params, Members: c.mem, Trigger: c.trig, Rounds: c.rounds})
			if !reflect.DeepEqual(keysOf(d.Speakers), c.want) || d.Bounded != c.bounded {
				t.Fatalf("got %v bounded=%v (%s) want %v bounded=%v", keysOf(d.Speakers), d.Bounded, d.Why, c.want, c.bounded)
			}
		})
	}
}

func TestCountRoundsAndProgress(t *testing.T) {
	h := RoundMsg{Author: Human("kyle"), Type: TypeChat}
	ag := func(k string) RoundMsg { return RoundMsg{Author: Agent(k), Type: TypeChat} }
	sys := RoundMsg{Author: System, Type: TypeSystem}
	tool := RoundMsg{Author: Tool("tests"), Type: TypeResult}
	rounds := []struct {
		name string
		msgs []RoundMsg
		want int
	}{
		{"empty", nil, 0},
		{"one agent answering a human", []RoundMsg{h, ag("a")}, 0},
		{"a then b", []RoundMsg{h, ag("a"), ag("b")}, 1},
		{"a talking twice is not a round", []RoundMsg{h, ag("a"), ag("a")}, 0},
		{"three rounds", []RoundMsg{h, ag("a"), ag("b"), ag("a"), ag("b")}, 3},
		{"system and tool messages are neutral", []RoundMsg{h, ag("a"), sys, tool, ag("b"), sys, ag("a")}, 2},
		{"a human message resets", []RoundMsg{h, ag("a"), ag("b"), ag("a"), h, ag("b")}, 0},
		{"passes do not count", []RoundMsg{h, ag("a"), {Author: Agent("b"), Type: TypeChat, Pass: true}, ag("a")}, 0},
	}
	for _, c := range rounds {
		if got := CountRounds(c.msgs); got != c.want {
			t.Errorf("%s: %d want %d", c.name, got, c.want)
		}
	}

	order := []string{"a", "b", "c"}
	prog := []struct {
		name      string
		msgs      []RoundMsg
		spoken    []string
		completed int
	}{
		{"nothing yet", []RoundMsg{h}, nil, 0},
		{"a and b", []RoundMsg{h, ag("a"), ag("b")}, []string{"a", "b"}, 0},
		{"a full pass", []RoundMsg{h, ag("a"), ag("b"), ag("c")}, nil, 1},
		{"pass plus one", []RoundMsg{h, ag("a"), ag("b"), ag("c"), ag("a")}, []string{"a"}, 1},
		{"human resets", []RoundMsg{h, ag("a"), ag("b"), ag("c"), h, ag("a")}, []string{"a"}, 0},
		{"outsiders ignored", []RoundMsg{h, ag("x"), ag("a")}, []string{"a"}, 0},
		{"a pass counts as having spoken", []RoundMsg{h, ag("a"), {Author: Agent("b"), Type: TypeChat, Pass: true}, ag("c")}, nil, 1},
	}
	for _, c := range prog {
		sp, done := SequentialProgress(order, c.msgs)
		if !reflect.DeepEqual(sp, c.spoken) || done != c.completed {
			t.Errorf("%s: %v/%d want %v/%d", c.name, sp, done, c.spoken, c.completed)
		}
	}
}

func TestResolveBound(t *testing.T) {
	h := DebateMsg{ID: "h", Author: Human("kyle"), Type: TypeChat}
	c := func(id string) DebateMsg { return DebateMsg{ID: id, Author: Agent("a"), Type: TypeChat} }
	d := func(id, status string) DebateMsg {
		return DebateMsg{ID: id, Author: Agent("a"), Type: TypeDecision, Status: status}
	}
	cases := []struct {
		name string
		msgs []DebateMsg
		act  BoundAction
		id   string
	}{
		{"no decision escalates to a human", []DebateMsg{h, c("1"), c("2")}, BoundEscalateHuman, ""},
		{"the last proposal stands", []DebateMsg{h, d("d1", "proposed"), c("2"), d("d2", "proposed")}, BoundAdoptDecision, "d2"},
		{"a proposal before the last human message is stale", []DebateMsg{d("old", "proposed"), h, c("2")}, BoundEscalateHuman, ""},
		{"superseded decisions do not count", []DebateMsg{h, d("d1", "superseded")}, BoundEscalateHuman, ""},
	}
	for _, tc := range cases {
		r := ResolveBound(tc.msgs)
		if r.Action != tc.act || r.DecisionID != tc.id || (tc.act == BoundEscalateHuman && r.Question == "") {
			t.Errorf("%s: %+v", tc.name, r)
		}
	}
}

// A labeled participant stub for the always-disagree failure boundary (docs/token-control.md rule 3): two agents that
// never converge must be stopped at the bound, then a human question must follow, and a human message re-opens the debate.
func TestDebateStopsAtBoundWithAlwaysDisagreeStub(t *testing.T) {
	e := newEnv(t)
	r := e.channel("debate")
	a, b := e.agent("alice"), e.agent("bob")
	e.join(r, Agent(a), "dev")
	e.join(r, Agent(b), "dev")

	say := func(au Author, body string) Message {
		return e.post(PostParams{RoomID: r.ID, Author: au, Body: body}).Message
	}
	opener := say(kyle, "@alice @bob should we use gRPC?")
	speaker := func(m Message) []string {
		d, err := e.svc.PlanTurns(e.ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Bounded {
			return []string{"BOUND"}
		}
		return keysOf(d.Speakers)
	}
	if got := speaker(opener); len(got) != 1 || got[0] != a {
		t.Fatalf("opener grants one speaker (the first mention): %v", got)
	}
	// the stub: each agent disagrees and @mentions the other
	turn := 0
	last := opener
	author, other := a, b
	for ; turn < 10; turn++ {
		last = say(Agent(author), "No, I disagree. @"+other)
		g := speaker(last)
		if len(g) == 1 && g[0] == "BOUND" {
			break
		}
		if len(g) != 1 || g[0] != other {
			t.Fatalf("turn %d: granted %v want %s", turn, g, other)
		}
		author, other = other, author
	}
	// H, a (answers the human), then b, a, b: three alternations
	if turn != 3 {
		t.Fatalf("bound reached after %d agent messages, want 4 (3 rounds)", turn+1)
	}

	res, applied, err := e.svc.ApplyDebateBound(e.ctx, r.ID, "")
	if err != nil || !applied || res.Action != BoundEscalateHuman {
		t.Fatalf("%v applied=%v %+v", err, applied, res)
	}
	if _, applied, _ := e.svc.ApplyDebateBound(e.ctx, r.ID, ""); applied {
		t.Fatal("bound applied twice for the same human message")
	}
	msgs, _ := e.svc.Messages(e.ctx, r.ID, 0, 100)
	var sys, q int
	for _, m := range msgs {
		if m.Type == TypeSystem && m.PayloadString("event") == "debate_bounded" {
			sys++
		}
		if m.Type == TypeQuestion && m.PayloadBool("blocking") {
			q++
		}
	}
	if sys != 1 || q != 1 {
		t.Fatalf("system=%d question=%d", sys, q)
	}
	if n := len(e.events("room.debate_bounded")); n != 1 {
		t.Fatalf("debate_bounded events %d", n)
	}
	items, _ := e.svc.NeedsYou(e.ctx, e.project)
	found := false
	for _, it := range items {
		found = found || it.Kind == NeedsQuestion
	}
	if !found {
		t.Fatal("the escalation is not in Needs you")
	}
	// the system and question messages do not restart the debate; only a human does
	more := say(Agent(a), "one more thing @"+b)
	if got := speaker(more); len(got) != 1 || got[0] != "BOUND" {
		t.Fatalf("still bounded: %v", got)
	}
	human := say(kyle, "Go with gRPC. @bob")
	if got := speaker(human); len(got) != 1 || got[0] != b {
		t.Fatalf("a human message re-opens the topic: %v", got)
	}

	// a debate that did propose a decision ends with that candidate
	r2 := e.channel("debate2")
	e.join(r2, Agent(a), "dev")
	e.join(r2, Agent(b), "dev")
	e.post(PostParams{RoomID: r2.ID, Author: kyle, Body: "discuss"})
	d := e.post(PostParams{RoomID: r2.ID, Author: Agent(a), Type: TypeDecision, Body: "Use REST", Payload: map[string]any{"title": "Use REST"}}).Message
	res, applied, err = e.svc.ApplyDebateBound(e.ctx, r2.ID, "")
	if err != nil || !applied || res.Action != BoundAdoptDecision || res.DecisionID != d.ID {
		t.Fatalf("%v %v %+v", err, applied, res)
	}
}

func TestSequentialRoomStopsAfterThreePasses(t *testing.T) {
	e := newEnv(t)
	r := e.channel("seq")
	ags := []string{e.agent("a"), e.agent("b"), e.agent("c")}
	for _, a := range ags {
		e.join(r, Agent(a), "dev")
	}
	r, _ = e.svc.GetRoom(e.ctx, r.ID) // membership changes bump the version
	r, err := e.svc.SetMode(e.ctx, r.ID, ModeSequential, ModeParams{Order: ags}, r.Version, kyle)
	if err != nil {
		t.Fatal(err)
	}
	cur := e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "begin"}).Message
	spoke := 0
	for i := 0; i < 20; i++ {
		d, err := e.svc.PlanTurns(e.ctx, cur.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Bounded {
			break
		}
		if len(d.Speakers) != 1 {
			t.Fatalf("step %d: %+v", i, d)
		}
		if want := ags[i%3]; d.Speakers[0].Member != want {
			t.Fatalf("step %d: %s want %s (fixed order)", i, d.Speakers[0].Member, want)
		}
		cur = e.post(PostParams{RoomID: r.ID, Author: Agent(d.Speakers[0].Member), Body: "my view"}).Message
		spoke++
	}
	if spoke != 9 {
		t.Fatalf("%d agent turns, want 3 passes x 3 members", spoke)
	}
	// planning in an archived room grants nothing
	if err := e.svc.ArchiveRoom(e.ctx, r.ID, kyle, "done"); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.svc.PlanTurns(e.ctx, cur.ID); len(d.Speakers) != 0 {
		t.Fatal("archived room granted a turn")
	}
	if _, err := e.svc.PlanTurns(e.ctx, "msg_none"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestPlanTurnsUsesTaskOwnerAndMentions(t *testing.T) {
	e := newEnv(t)
	owner, rev := e.agent("owner"), e.agent("rev")
	r, _ := e.svc.CreateTaskChannel(e.ctx, TaskChannelSpec{ProjectID: e.project, TaskID: "task_p", Number: 2, Title: "plan", Owner: owner, Reviewers: []string{rev}, CreatedBy: kyle})
	m := e.say(r, kyle, "please start")
	d, _ := e.svc.PlanTurns(e.ctx, m.ID)
	if len(d.Speakers) != 1 || d.Speakers[0].Member != owner || d.Speakers[0].Reason != ReasonOwner {
		t.Fatalf("owner: %+v", d)
	}
	m = e.say(r, kyle, "@role:reviewer take a look")
	d, _ = e.svc.PlanTurns(e.ctx, m.ID)
	if len(d.Speakers) != 1 || d.Speakers[0].Member != rev || d.Speakers[0].Reason != ReasonMention {
		t.Fatalf("role mention: %+v", d)
	}
	// a tool result wakes the owner, not the reviewer
	p, _ := e.svc.PostCheckResult(e.ctx, r.ID, CheckResult{Kind: "test", Status: CheckFail, LogRef: "log"})
	d, _ = e.svc.PlanTurns(e.ctx, p.ID)
	if len(d.Speakers) != 1 || d.Speakers[0].Member != owner {
		t.Fatalf("tool result: %+v", d)
	}
	// status chatter wakes nobody
	s := e.post(PostParams{RoomID: r.ID, Author: Agent(owner), Type: TypeStatus, Body: "halfway"}).Message
	if d, _ := e.svc.PlanTurns(e.ctx, s.ID); len(d.Speakers) != 0 {
		t.Fatalf("status woke %+v", d)
	}
}
