package responder_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/agents/responder"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The group-collaboration workflow node (decision P118, token-control rule 3), run by the real workflow engine against the
// labelled fake agent (a test double of Claude Code, a real process speaking its stream-json dialect): a bounded discussion
// between named agents in a room, each turn granted by the room's scheduler, ending in a DECISION candidate or a human
// QUESTION, with the outcome delivered to the next node.

// groupEngine registers a graph of a group node whose two routes lead to nodes that keep what they received (by route).
func (h *harness) groupEngine(rm room.Room, agents []string, rounds int) (*workflow.Engine, *sync.Map) {
	h.t.Helper()
	eng := workflow.New(workflow.Config{DB: h.db, Bus: h.bus, DataDir: h.t.TempDir(), LeaseTTL: 2 * time.Second})
	h.t.Cleanup(eng.Close)
	seen := &sync.Map{}
	next := func(route string) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			var res responder.DiscussionResult
			if len(in.Inbox) != 1 || json.Unmarshal(in.Inbox[0].Value, &res) != nil {
				return workflow.NodeOutput{}, nil
			}
			seen.Store(route, res)
			return workflow.NodeOutput{Value: json.RawMessage(`"ok"`)}, nil
		}
	}
	g := workflow.NewGraph("group-test").
		Add(responder.GroupNode("discuss", h.r, func(in workflow.NodeInput) (responder.Discussion, error) {
			var topic string
			if err := json.Unmarshal(in.Input, &topic); err != nil {
				return responder.Discussion{}, err
			}
			return responder.Discussion{RoomID: rm.ID, Topic: topic, Agents: agents, Rounds: rounds}, nil
		}),
			workflow.Route("adopt", next("decision")),
			workflow.Route("ask", next("question"))).
		EdgeWhen("discuss", "adopt", responder.OutcomeDecision).
		EdgeWhen("discuss", "ask", responder.OutcomeQuestion).
		Start("discuss")
	if err := eng.Register(g); err != nil {
		h.t.Fatal(err)
	}
	return eng, seen
}

func (h *harness) runGroup(eng *workflow.Engine, topic string) workflow.RunInfo {
	h.t.Helper()
	id, err := eng.Start(h.ctx, "group-test", topic, workflow.StartOptions{ProjectID: h.project})
	if err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(h.ctx, 60*time.Second)
	defer cancel()
	info, err := eng.Await(ctx, id, func(i workflow.RunInfo) bool { return i.Status.Terminal() })
	if err != nil || info.Status != workflow.StatusCompleted {
		h.t.Fatalf("run %s (%s): %v", info.Status, info.FailedReason, err)
	}
	return info
}

func roundOne(say1 string) fakeagent.Script {
	return fakeagent.Script{Rules: []fakeagent.Rule{{Match: "This is round 1 of", Steps: []fakeagent.Step{{Say: say1}}}}, Default: []fakeagent.Step{{Say: "PASS"}}}
}

func TestAGroupNodeEndsInADecisionCandidateTheNextNodeReceives(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", roundOne("Keep the parser hand-written: it is small, and a generator adds a build step."))
	b := h.agent("agt_b", "Bo", "backend", roundOne("Agreed, the grammar is tiny.\n[DECISION] Keep the hand-written parser"))
	rm := h.channel("design", a, b)
	eng, seen := h.groupEngine(rm, []string{a, b}, 0)
	h.runGroup(eng, "Should the expression parser be generated or hand-written?")

	v, ok := seen.Load("decision")
	if !ok {
		t.Fatal("the next node on the decision route did not run")
	}
	res := v.(responder.DiscussionResult)
	if res.Outcome != responder.OutcomeDecision || res.DecisionID == "" || !strings.Contains(res.Decision, "Keep the hand-written parser") || res.Bound != 3 {
		t.Fatalf("result %+v", res)
	}
	// round 1: Ada speaks, Bo states the decision, and that ends the discussion (P127): nobody is asked for another round
	if res.Ended != room.EndDecision || res.EndedBy != b || res.Converged || len(res.Turns) != 2 || res.Turns[1].DecisionID != res.DecisionID {
		t.Fatalf("turns %+v (ended %s by %s)", res.Turns, res.Ended, res.EndedBy)
	}
	if _, asked := seen.Load("question"); asked {
		t.Fatal("the question route ran too")
	}
	// the discussion is one thread under the topic, in the fixed order, and the bound's resolution is posted there
	thread, _ := h.room.Thread(h.ctx, res.ThreadID)
	var order []string
	bounded := false
	for _, m := range thread {
		if m.Author.Kind == room.AuthorAgent && m.Type == room.TypeChat {
			order = append(order, m.Author.ID)
		}
		bounded = bounded || (m.Type == room.TypeSystem && strings.Contains(m.Body, "ended after 1 rounds: Bo stated a decision") && strings.Contains(m.Body, "stands as the candidate"))
	}
	if strings.Join(order, ",") != strings.Join([]string{a, b}, ",") || !bounded {
		t.Fatalf("thread order %v, bounded %v", order, bounded)
	}
	// every turn ran on its own grant from the scheduler, round by round
	rows, _ := h.db.Reader().Query(`SELECT agent_id, round, used_at FROM room_turn_grants WHERE room_id=? ORDER BY issued_at, rowid`, rm.ID)
	var grants []string
	for rows.Next() {
		var agent string
		var round int
		var used int64
		rows.Scan(&agent, &round, &used)
		if used == 0 {
			t.Fatalf("an unused grant: %s round %d", agent, round)
		}
		grants = append(grants, agent+":"+string(rune('0'+round)))
	}
	rows.Close()
	if strings.Join(grants, " ") != a+":1 "+b+":1" {
		t.Fatalf("grants %v", grants)
	}
	// the agents were told it is a bounded group discussion
	if p := h.prompts(a); len(p) != 1 || !strings.HasPrefix(p[0], "Drydock phase: group-discussion") || !strings.Contains(p[0], "at most 3 rounds between Ada, Bo") ||
		!strings.Contains(p[0], "Should the expression parser be generated") {
		t.Fatalf("prompts %v", p)
	}
}

func TestAGroupNodeThatDoesNotConvergeAsksAPersonAtTheBound(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("Use SQLite."))
	b := h.agent("agt_b", "Bo", "backend", say("No: use Postgres."))
	rm := h.channel("design", a, b)
	eng, seen := h.groupEngine(rm, []string{a, b}, 2)
	h.runGroup(eng, "Which database?")
	v, ok := seen.Load("question")
	if !ok {
		t.Fatal("the next node on the question route did not run")
	}
	res := v.(responder.DiscussionResult)
	// two rounds of two turns each, then the bound: nobody proposed a decision, so a person is asked
	if res.Outcome != responder.OutcomeQuestion || res.QuestionID == "" || res.Converged || res.Ended != room.EndBound || len(res.Turns) != 4 || res.Rounds != 2 {
		t.Fatalf("result %+v", res)
	}
	items, err := h.room.NeedsYou(h.ctx, h.project)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		found = found || (it.Kind == room.NeedsQuestion && it.MessageID == res.QuestionID)
	}
	if !found {
		t.Fatalf("the question is in the person's inbox: %+v", items)
	}
}

// A turn that returns SHIP ends the discussion there (room-protocol s6 rule 7, decision P127). SHIP is an agent's word, so it
// decides nothing by itself: with no DECISION written down, a person is asked, and the question says an agent returned SHIP.
func TestAGroupNodeEndsWhenAnAgentReturnsShip(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("Keep the parser hand-written: it is small, and a generator adds a build step."))
	b := h.agent("agt_b", "Bo", "backend", say("Ship."))
	rm := h.channel("design", a, b)
	eng, seen := h.groupEngine(rm, []string{a, b}, 0)
	h.runGroup(eng, "Should the expression parser be generated or hand-written?")
	v, ok := seen.Load("question")
	if !ok {
		t.Fatal("the next node on the question route did not run")
	}
	res := v.(responder.DiscussionResult)
	if res.Ended != room.EndShip || res.EndedBy != b || len(res.Turns) != 2 || !res.Turns[1].Ship || res.Rounds != 1 || res.Converged {
		t.Fatalf("result %+v", res)
	}
	if !strings.Contains(res.Question, "returned SHIP") {
		t.Fatalf("question %q", res.Question)
	}
	if p := h.prompts(a); len(p) != 1 || !strings.Contains(p[0], "reply with exactly: SHIP") {
		t.Fatalf("Ada was asked again, or not told about SHIP: %v", p)
	}
	thread, _ := h.room.Thread(h.ctx, res.ThreadID)
	var ship, said bool
	for _, m := range thread {
		ship = ship || (m.Author.ID == b && m.Body == "SHIP" && m.PayloadBool("ship"))
		said = said || (m.Type == room.TypeSystem && strings.Contains(m.Body, "Bo returned SHIP"))
	}
	if !ship || !said {
		t.Fatalf("the SHIP turn %v, the line that says why it ended %v", ship, said)
	}
}

// A round in which every agent passed ends the discussion before its bound, and the result says that is why.
func TestAGroupNodeEndsWhenEveryAgentPasses(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("PASS"))
	b := h.agent("agt_b", "Bo", "backend", say("PASS"))
	rm := h.channel("design", a, b)
	eng, seen := h.groupEngine(rm, []string{a, b}, 0)
	h.runGroup(eng, "Anything to change in the logging?")
	v, ok := seen.Load("question")
	if !ok {
		t.Fatal("the next node on the question route did not run")
	}
	res := v.(responder.DiscussionResult)
	if res.Ended != room.EndPasses || res.EndedBy != "" || !res.Converged || len(res.Turns) != 2 {
		t.Fatalf("result %+v", res)
	}
	thread, _ := h.room.Thread(h.ctx, res.ThreadID)
	said := false
	for _, m := range thread {
		said = said || (m.Type == room.TypeSystem && strings.Contains(m.Body, "every agent passed"))
	}
	if !said {
		t.Fatal("the line that says why it ended")
	}
}

func TestShipIsTheWholeReply(t *testing.T) {
	for in, want := range map[string]bool{"SHIP": true, " ship. ": true, "Ship!": true, "SHIPPING it would be a mistake": false, "SHIP it, but": false, "": false} {
		if responder.IsShip(in) != want {
			t.Fatalf("%q: want %v", in, want)
		}
	}
}

func TestAGroupNodeRefusesAgentsOutsideTheRoom(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("x"))
	b := h.agent("agt_b", "Bo", "backend", say("y"))
	rm := h.channel("design", a) // Bo was never added: agents do not join on their own
	_, err := h.r.Discuss(h.ctx, responder.Discussion{RoomID: rm.ID, Topic: "t", Agents: []string{a, b}})
	if err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Fatalf("%v", err)
	}
	if _, err := h.r.Discuss(h.ctx, responder.Discussion{RoomID: rm.ID, Topic: "t", Agents: []string{a}}); err == nil {
		t.Fatal("a discussion of one")
	}
	var n int
	h.db.Reader().QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d turns ran", n)
	}
}
