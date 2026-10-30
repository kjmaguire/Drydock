package room

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/events"
)

// ContextPlan says what an agent's turn may see from a room (room-protocol s11). SelectContext is the only way room
// content reaches an agent: it picks by rule from a pool and records why each message was kept or left out.
//
// A turn grant carries the plan its turn is selected by (decision P127): it is stored with the grant as this JSON.
type ContextPlan struct {
	RoomID       string   `json:"roomId"`
	AgentKey     string   `json:"agentKey"`
	TriggerID    string   `json:"triggerId"`
	TaskID       string   `json:"taskId,omitempty"` // the task this turn works on, "" if none
	Roles        []string `json:"roles,omitempty"`  // the agent's roles in the room, for role mentions and handoffs
	LastTurnSeq  int64    `json:"lastTurnSeq"`      // seq of the agent's own previous message here; mentions are taken since then
	ThreadWindow int      `json:"threadWindow"`     // K: last messages of the thread (default 12)
	MaxMentions  int      `json:"maxMentions"`      // mention messages since the last turn (default 10)
	MaxTokens    int      `json:"maxTokens"`        // size cap on the whole selection (default 6000, estimated)
}

func (p *ContextPlan) defaults() {
	if p.ThreadWindow <= 0 {
		p.ThreadWindow = 12
	}
	if p.MaxMentions <= 0 {
		p.MaxMentions = 10
	}
	if p.MaxTokens <= 0 {
		p.MaxTokens = 6000
	}
}

// DigestFunc compresses older thread messages into a digest. The default is deterministic; a caller may plug in a
// cheap private model (room-protocol s11 item 4). An error falls back to the deterministic digest.
type DigestFunc func(ctx context.Context, older []Message) (string, error)

// ContextEntry is one manifest line (token-control s2): what was considered, kept or dropped, and why.
type ContextEntry struct {
	MessageID string  `json:"messageId"`
	Seq       int64   `json:"seq"`
	Author    string  `json:"author"`
	Type      MsgType `json:"type"`
	Trust     Trust   `json:"trust"`
	Rule      string  `json:"rule,omitempty"` // trigger, thread, task, decision, handoff, mention
	Priority  int     `json:"priority"`
	EstTokens int     `json:"estTokens"`
	Included  bool    `json:"included"`
	Exclusion string  `json:"exclusion,omitempty"` // over_budget, not_addressed, other_room, superseded, digested
}

// ContextSelection is the selected room context and its manifest.
type ContextSelection struct {
	Messages     []Message      // included, oldest first
	Digest       string         // digest of older thread messages, "" if none
	DigestFrom   []string       // message ids folded into the digest
	Entries      []ContextEntry // every pool message with its verdict
	Tokens       int            // estimated, digest included
	TriggerFound bool
}

func estTokens(m Message) int {
	n := len(m.Body)
	for _, a := range m.Attachments() {
		n += len(a.Text) + len(a.Name) + 40
	}
	return (n+len(m.Author.Key())+len(m.Type)+24)/4 + 1
}

func textTokens(s string) int { return len(s)/4 + 1 }

// SelectContext picks the room messages for one agent turn from a pool the caller loaded. Rules in priority order:
// (0) the triggering message, (1) its thread (root plus the last K, older parts as a digest), (2) the task statement,
// accepted decisions for the task and handoffs addressed to the agent, (3) messages mentioning the agent or its role
// since its last turn. Everything else in the pool is excluded as not addressed, and messages from another room never
// enter. When the size cap bites, lower priorities and older messages go first; the trigger always stays.
func SelectContext(ctx context.Context, plan ContextPlan, pool []Message, digest DigestFunc) (ContextSelection, error) {
	plan.defaults()
	var sel ContextSelection
	byID := map[string]Message{}
	superseded := map[string]bool{}
	for _, m := range pool {
		byID[m.ID] = m
		if m.SupersedesID != "" && m.RoomID == plan.RoomID {
			superseded[m.SupersedesID] = true
		}
	}
	trig, ok := byID[plan.TriggerID]
	if !ok || trig.RoomID != plan.RoomID {
		return sel, fmt.Errorf("%w: trigger %s", ErrNotFound, plan.TriggerID)
	}
	sel.TriggerFound = true
	root := trig.ThreadRootID
	if root == "" {
		root = trig.ID
	}

	type cand struct {
		m    Message
		prio int
		rule string
	}
	chosen := map[string]cand{}
	set := func(m Message, prio int, rule string) {
		if c, ok := chosen[m.ID]; !ok || prio < c.prio {
			chosen[m.ID] = cand{m, prio, rule}
		}
	}
	sorted := append([]Message(nil), pool...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })

	set(trig, 0, "trigger")
	var thread []Message
	for _, m := range sorted {
		if m.RoomID == plan.RoomID && (m.ID == root || m.ThreadRootID == root) {
			thread = append(thread, m)
		}
	}
	older := map[string]bool{}
	var olderMsgs []Message
	for i, m := range thread {
		if m.ID == root || i >= len(thread)-plan.ThreadWindow {
			set(m, 1, "thread")
		} else if _, isTrig := chosen[m.ID]; !isTrig {
			older[m.ID] = true
			olderMsgs = append(olderMsgs, m)
		}
	}
	for _, m := range sorted {
		if m.RoomID != plan.RoomID {
			continue
		}
		switch {
		case m.Type == TypeTask && plan.TaskID != "" && m.TaskID == plan.TaskID && m.ThreadRootID == "" && m.PayloadString("state") != "proposed":
			set(m, 2, "task")
		case m.Type == TypeDecision && plan.TaskID != "" && m.TaskID == plan.TaskID && m.PayloadString("status") == "accepted":
			set(m, 2, "decision")
		case m.Type == TypeHandoff && handoffFor(m, plan):
			set(m, 2, "handoff")
		}
	}
	var mentions []Message
	for _, m := range sorted {
		if m.RoomID == plan.RoomID && m.Seq > plan.LastTurnSeq && m.Author.Key() != plan.AgentKey && mentionsAgent(m, plan) {
			mentions = append(mentions, m)
		}
	}
	if len(mentions) > plan.MaxMentions {
		mentions = mentions[len(mentions)-plan.MaxMentions:] // keep the newest
	}
	for _, m := range mentions {
		set(m, 3, "mention")
	}

	// Budget in priority order; within a priority newest first, so the oldest are dropped first.
	var order []cand
	for _, c := range chosen {
		if c.prio == 1 && older[c.m.ID] {
			continue
		}
		order = append(order, c)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].prio != order[j].prio {
			return order[i].prio < order[j].prio
		}
		return order[i].m.Seq > order[j].m.Seq
	})
	used := 0
	incl := map[string]bool{}
	take := func(c cand) bool {
		t := estTokens(c.m)
		if c.prio > 0 && used+t > plan.MaxTokens {
			return false
		}
		used += t
		incl[c.m.ID] = true
		return true
	}
	digested := false
	for _, c := range order {
		if c.prio >= 2 && !digested { // the digest ranks after the thread and before task-level context
			digested = true
			sel.Digest, sel.DigestFrom, used = makeDigest(ctx, digest, olderMsgs, used, plan.MaxTokens)
		}
		take(c)
	}
	if !digested {
		sel.Digest, sel.DigestFrom, used = makeDigest(ctx, digest, olderMsgs, used, plan.MaxTokens)
	}
	sel.Tokens = used

	digestSet := map[string]bool{}
	for _, id := range sel.DigestFrom {
		digestSet[id] = true
	}
	for _, m := range sorted {
		e := ContextEntry{MessageID: m.ID, Seq: m.Seq, Author: m.Author.Key(), Type: m.Type, Trust: m.Trust, EstTokens: estTokens(m)}
		c, picked := chosen[m.ID]
		switch {
		case m.RoomID != plan.RoomID:
			e.Exclusion = "other_room"
		case incl[m.ID]:
			e.Included, e.Rule, e.Priority = true, c.rule, c.prio
		case picked && older[m.ID]:
			e.Rule, e.Priority, e.Exclusion = "thread", 1, "digested"
		case picked:
			e.Rule, e.Priority, e.Exclusion = c.rule, c.prio, "over_budget"
		case older[m.ID]:
			e.Rule, e.Priority, e.Exclusion = "thread", 1, "digested"
			if !digestSet[m.ID] {
				e.Exclusion = "over_budget"
			}
		default:
			e.Exclusion = "not_addressed"
		}
		if superseded[m.ID] && m.ID != trig.ID {
			e.Included, e.Exclusion = false, "superseded"
			delete(incl, m.ID)
		}
		sel.Entries = append(sel.Entries, e)
	}
	for _, m := range sorted {
		if incl[m.ID] && !(superseded[m.ID] && m.ID != trig.ID) {
			sel.Messages = append(sel.Messages, m)
		}
	}
	return sel, nil
}

func makeDigest(ctx context.Context, f DigestFunc, older []Message, used, max int) (string, []string, int) {
	if len(older) == 0 {
		return "", nil, used
	}
	var text string
	if f != nil {
		if t, err := f(ctx, older); err == nil {
			text = t
		}
	}
	if text == "" {
		text = DeterministicDigest(older)
	}
	if used+textTokens(text) > max {
		return "", nil, used
	}
	ids := make([]string, len(older))
	for i, m := range older {
		ids[i] = m.ID
	}
	return text, ids, used + textTokens(text)
}

func handoffFor(m Message, plan ContextPlan) bool {
	to := m.PayloadString("to")
	if to == plan.AgentKey {
		return true
	}
	for _, r := range plan.Roles {
		if strings.EqualFold(m.PayloadString("to_role"), r) {
			return true
		}
	}
	return mentionsAgent(m, plan)
}

func mentionsAgent(m Message, plan ContextPlan) bool {
	for _, mn := range m.Mentions {
		if mn.ID == plan.AgentKey {
			return true
		}
		for _, k := range mn.Members {
			if k == plan.AgentKey {
				return true
			}
		}
	}
	return false
}

// DeterministicDigest folds older messages into decisions, tasks, questions, check outcomes and a count of the rest.
// It needs no model, and it loses prose context (room-protocol open questions).
func DeterministicDigest(older []Message) string {
	var b strings.Builder
	b.WriteString("Earlier in this thread:\n")
	other := 0
	for _, m := range older {
		line := ""
		switch m.Type {
		case TypeDecision:
			line = fmt.Sprintf("- decision (%s): %s [%s]", m.PayloadString("status"), clip(m.PayloadString("title"), 100), m.ID)
		case TypeTask:
			line = fmt.Sprintf("- task: %s [%s]", clip(m.PayloadString("title"), 100), m.ID)
		case TypeQuestion:
			line = fmt.Sprintf("- question: %s [%s]", clip(m.Body, 100), m.ID)
		case TypeResult, TypeError:
			if k := m.PayloadString("check_kind"); k != "" {
				line = fmt.Sprintf("- check %s: %s [%s]", k, m.PayloadString("check_status"), m.ID)
			}
		}
		if line == "" {
			for _, mk := range m.Markers {
				if mk.Marker == "DECISION" || mk.Marker == "TODO" {
					line = fmt.Sprintf("- %s: %s [%s]", strings.ToLower(mk.Marker), clip(mk.Text, 100), m.ID)
					break
				}
			}
		}
		if line == "" {
			other++
			continue
		}
		if b.Len()+len(line) > 1200 {
			other++
			continue
		}
		b.WriteString(line + "\n")
	}
	if other > 0 {
		fmt.Fprintf(&b, "- %d other messages not shown\n", other)
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderContext renders a selection as prompt text. Messages from agents are wrapped as untrusted data with attribution
// (security.md s10); human messages are plain; nothing here is an instruction channel except human text.
func RenderContext(sel ContextSelection) string {
	var b strings.Builder
	if sel.Digest != "" {
		b.WriteString("<room_digest>\n" + sel.Digest + "\n</room_digest>\n")
	}
	for _, m := range sel.Messages {
		fmt.Fprintf(&b, "[%s seq=%d %s from %s %s]\n", m.ID, m.Seq, m.Type, m.Author.Key(), m.Trust)
		if m.Trust == TrustUntrusted {
			fmt.Fprintf(&b, "<untrusted source=\"room:%s\" ref=\"%s\">\n%s\n</untrusted>\n", m.Author.Key(), m.ID, strings.ReplaceAll(m.Body, "</untrusted>", "</ untrusted>"))
		} else {
			b.WriteString(m.Body + "\n")
		}
		// an attached file is data whoever attached it: its text never becomes an instruction
		for _, a := range m.Attachments() {
			if a.Text != "" {
				fmt.Fprintf(&b, "<untrusted source=\"attachment:%s\" ref=\"%s\">\n%s\n</untrusted>\n", strings.ReplaceAll(a.Name, `"`, "'"), a.ID, strings.ReplaceAll(a.Text, "</untrusted>", "</ untrusted>"))
			} else {
				fmt.Fprintf(&b, "(attached file %q, %d bytes, %s: not text, so not shown)\n", a.Name, a.Size, a.Mime)
			}
		}
	}
	return b.String()
}

// ContextOptions tunes ContextFor.
type ContextOptions struct {
	ThreadWindow, MaxMentions, MaxTokens int
	Digest                               DigestFunc
}

// ContextFor loads the pool for one agent turn and selects from it. The agent must be a member of the room (the same
// check the Workspace room tool makes). Only this room is read: never another room, never the whole history. A turn that
// runs on a grant is selected by the plan its grant carries instead (ContextForGrant).
func (s *Service) ContextFor(ctx context.Context, roomID string, agent Author, triggerID string, opt ContextOptions) (ContextSelection, error) {
	plan, err := s.planContext(ctx, roomID, agent, triggerID, ContextBounds{ThreadWindow: opt.ThreadWindow, MaxMentions: opt.MaxMentions, MaxTokens: opt.MaxTokens})
	if err != nil {
		return ContextSelection{}, err
	}
	return s.selectPlan(ctx, plan, opt.Digest)
}

// planContext is the context plan of an agent's turn after a message, within bounds (the defaults filled in): what the
// scheduler writes into a grant, and what ContextFor selects by.
func (s *Service) planContext(ctx context.Context, roomID string, agent Author, triggerID string, b ContextBounds) (ContextPlan, error) {
	room, err := s.GetRoom(ctx, roomID)
	if err != nil {
		return ContextPlan{}, err
	}
	members, err := s.Members(ctx, roomID)
	if err != nil {
		return ContextPlan{}, err
	}
	var roles []string
	isMember := false
	for _, m := range members {
		if m.Author.Key() == agent.Key() {
			isMember = true
			if m.Role != "" {
				roles = append(roles, m.Role)
			}
		}
	}
	if !isMember {
		return ContextPlan{}, fmt.Errorf("%w: %s in %s", ErrNotMember, agent.Key(), room.Name)
	}
	trig, err := s.GetMessage(ctx, triggerID)
	if err != nil {
		return ContextPlan{}, err
	}
	if trig.RoomID != roomID {
		return ContextPlan{}, fmt.Errorf("%w: trigger %s is not in room %s", ErrNotFound, triggerID, roomID)
	}
	var lastTurn int64
	if err := s.db.Reader().QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM messages WHERE room_id=? AND author=?`, roomID, agent.Key()).Scan(&lastTurn); err != nil {
		return ContextPlan{}, err
	}
	plan := ContextPlan{RoomID: roomID, AgentKey: agent.Key(), TriggerID: triggerID, TaskID: room.TaskID, Roles: roles, LastTurnSeq: lastTurn,
		ThreadWindow: b.ThreadWindow, MaxMentions: b.MaxMentions, MaxTokens: b.MaxTokens}
	plan.defaults()
	return plan, nil
}

// selectPlan loads the pool a plan draws from (the trigger's thread, the plan's task statement and decisions, handoffs,
// mentions since the plan's previous turn, corrections) and selects from it by the plan.
func (s *Service) selectPlan(ctx context.Context, plan ContextPlan, digest DigestFunc) (ContextSelection, error) {
	trig, err := s.GetMessage(ctx, plan.TriggerID)
	if err != nil {
		return ContextSelection{}, err
	}
	if trig.RoomID != plan.RoomID {
		return ContextSelection{}, fmt.Errorf("%w: trigger %s is not in room %s", ErrNotFound, plan.TriggerID, plan.RoomID)
	}
	roomID := plan.RoomID
	root := trig.ThreadRootID
	if root == "" {
		root = trig.ID
	}
	db := s.db.Reader()
	pool, err := queryMessages(ctx, db, `id=? OR thread_id=? ORDER BY seq`, root, root)
	if err != nil {
		return ContextSelection{}, err
	}
	add := func(where string, args ...any) error {
		ms, err := queryMessages(ctx, db, where, args...)
		pool = append(pool, ms...)
		return err
	}
	if plan.TaskID != "" {
		if err := add(`room_id=? AND task_id=? AND type IN ('TASK','DECISION') ORDER BY seq DESC LIMIT 100`, roomID, plan.TaskID); err != nil {
			return ContextSelection{}, err
		}
	}
	if err := add(`room_id=? AND type='HANDOFF' ORDER BY seq DESC LIMIT 20`, roomID); err != nil {
		return ContextSelection{}, err
	}
	if err := add(`room_id=? AND seq>? AND mentions<>'[]' ORDER BY seq DESC LIMIT 200`, roomID, plan.LastTurnSeq); err != nil {
		return ContextSelection{}, err
	}
	if err := add(`room_id=? AND supersedes_id<>'' ORDER BY seq DESC LIMIT 200`, roomID); err != nil {
		return ContextSelection{}, err
	}
	seen := map[string]bool{}
	uniq := pool[:0]
	for _, m := range pool {
		if !seen[m.ID] {
			seen[m.ID] = true
			uniq = append(uniq, m)
		}
	}
	return SelectContext(ctx, plan, uniq, digest)
}

// transcript loads the messages the turn counters look at: the trigger's thread, or the last top-level messages.
func (s *Service) transcript(ctx context.Context, trig Message) ([]Message, string, error) {
	root := trig.ThreadRootID
	if root == "" {
		ms, err := queryMessages(ctx, s.db.Reader(), `id=? OR thread_id=? ORDER BY seq`, trig.ID, trig.ID)
		if err != nil {
			return nil, "", err
		}
		if len(ms) > 1 {
			return ms, trig.ID, nil
		}
		top, err := queryMessages(ctx, s.db.Reader(), `room_id=? AND thread_id='' AND seq<=? ORDER BY seq DESC LIMIT 60`, trig.RoomID, trig.Seq)
		if err != nil {
			return nil, "", err
		}
		for i, j := 0, len(top)-1; i < j; i, j = i+1, j-1 {
			top[i], top[j] = top[j], top[i]
		}
		return top, "", nil
	}
	ms, err := queryMessages(ctx, s.db.Reader(), `(id=? OR thread_id=?) AND seq<=? ORDER BY seq`, root, root, trig.Seq)
	return ms, root, err
}

// roundMsgs are the turns of a transcript for the round counters. A message derived from another's marker (a [DECISION] line
// promoted to a DECISION) is part of the turn that wrote it, not a turn of its own: counted, it made the agent look as if it
// had already spoken in the next round.
func roundMsgs(ms []Message) []RoundMsg {
	out := make([]RoundMsg, 0, len(ms))
	for _, m := range ms {
		if m.PayloadString("source_message_id") != "" {
			continue
		}
		out = append(out, RoundMsg{Author: m.Author, Type: m.Type, Pass: m.PayloadBool("pass")})
	}
	return out
}

// PlanTurns decides who may take a turn after a message, from the room mode, the membership and the transcript. It
// grants nothing by itself: the caller issues TurnGrants for the speakers it returns.
func (s *Service) PlanTurns(ctx context.Context, messageID string) (TurnDecision, error) {
	trig, err := s.GetMessage(ctx, messageID)
	if err != nil {
		return TurnDecision{}, err
	}
	room, err := s.GetRoom(ctx, trig.RoomID)
	if err != nil {
		return TurnDecision{}, err
	}
	if room.Archived {
		return TurnDecision{Why: "room archived"}, nil
	}
	members, err := s.Members(ctx, trig.RoomID)
	if err != nil {
		return TurnDecision{}, err
	}
	tm := make([]TurnMember, len(members))
	for i, m := range members {
		tm[i] = TurnMember{Key: m.Author.Key(), Kind: m.Author.Kind, Role: m.Role, State: m.State}
	}
	trs, _, err := s.transcript(ctx, trig)
	if err != nil {
		return TurnDecision{}, err
	}
	in := TurnInput{Mode: room.Mode, Params: room.ModeParams, Members: tm, Rounds: CountRounds(roundMsgs(trs))}
	tt := TurnTrigger{MessageID: trig.ID, Author: trig.Author, Type: trig.Type, RoleHint: firstNonEmpty(trig.PayloadString("to_role"), trig.PayloadString("role"))}
	for _, mn := range trig.Mentions {
		if mn.Kind == "role" {
			tt.Mentioned = append(tt.Mentioned, mn.Members...)
		} else if mn.Kind == string(AuthorAgent) {
			tt.Mentioned = append(tt.Mentioned, mn.ID)
		}
	}
	for _, m := range trs {
		if m.Type == TypeTask && m.ThreadRootID == "" && m.PayloadString("assignee") != "" {
			tt.AssigneeKey = m.PayloadString("assignee")
		}
	}
	if tt.AssigneeKey == "" {
		for _, role := range []string{"owner", "assignee"} {
			for _, m := range members {
				if m.Author.Kind == AuthorAgent && strings.EqualFold(m.Role, role) && tt.AssigneeKey == "" {
					tt.AssigneeKey = m.Author.Key()
				}
			}
		}
	}
	in.Trigger = tt
	if room.Mode == ModeSequential {
		order := room.ModeParams.Order
		if len(order) == 0 {
			for _, m := range tm {
				if m.Kind == AuthorAgent && m.State == StateActive {
					order = append(order, m.Key)
				}
			}
		}
		in.Spoken, in.RoundsCompleted = SequentialProgress(order, roundMsgs(trs))
	}
	return NextSpeakers(in), nil
}

// ApplyDebateBound ends a bounded debate in the thread (room-protocol s6 rule 2): a SYSTEM message, then either the
// agents' proposed DECISION stands as the candidate or a blocking QUESTION goes to a human. It is idempotent per
// thread and human message, so repeated calls post nothing new. thread may be "" for the room's top level.
func (s *Service) ApplyDebateBound(ctx context.Context, roomID, thread string) (BoundResolution, bool, error) {
	return s.EndDiscussion(ctx, roomID, thread, DiscussionEnd{Why: EndBound})
}

// Why a discussion ended (room-protocol s6 rule 7, decision P127): at its bound, after a round in which every agent passed,
// on a turn that returned SHIP, or on a turn that stated a DECISION.
const (
	EndBound    = "bound"
	EndPasses   = "passes"
	EndShip     = "ship"
	EndDecision = "decision"
)

// DiscussionEnd is why a discussion ended, and the agent whose turn ended it (for SHIP and a DECISION).
type DiscussionEnd struct {
	Why   string // EndBound (the default), EndPasses, EndShip or EndDecision
	Agent string // the agent's name or id
}

// boundShipNoDecisionText is the question when an agent returned SHIP but nobody wrote the outcome down as a DECISION.
const boundShipNoDecisionText = "An agent returned SHIP, but no decision was written down. Please state the decision, or give direction, to continue."

// EndDiscussion is ApplyDebateBound for a discussion that ended for the reason end gives: the SYSTEM line says why, and the
// resolution is the same (the DECISION proposed since the last human message stands as the candidate; otherwise a human is
// asked). Its idempotency key is the bound's, so a discussion is ended once whatever ended it.
func (s *Service) EndDiscussion(ctx context.Context, roomID, thread string, end DiscussionEnd) (BoundResolution, bool, error) {
	var ms []Message
	var err error
	if thread != "" {
		ms, err = queryMessages(ctx, s.db.Reader(), `id=? OR thread_id=? ORDER BY seq`, thread, thread)
	} else {
		ms, err = queryMessages(ctx, s.db.Reader(), `room_id=? AND thread_id='' ORDER BY seq DESC LIMIT 60`, roomID)
		for i, j := 0, len(ms)-1; i < j; i, j = i+1, j-1 {
			ms[i], ms[j] = ms[j], ms[i]
		}
	}
	if err != nil {
		return BoundResolution{}, false, err
	}
	dm := make([]DebateMsg, len(ms))
	var lastHuman int64
	for i, m := range ms {
		dm[i] = DebateMsg{ID: m.ID, Author: m.Author, Type: m.Type, Status: m.PayloadString("status")}
		if m.Author.Kind == AuthorHuman {
			lastHuman = m.Seq
		}
	}
	res := ResolveBound(dm)
	rounds := CountRounds(roundMsgs(ms))
	key := fmt.Sprintf("bound:%s:%s:%d", roomID, thread, lastHuman)
	who := end.Agent
	if who == "" {
		who = "an agent"
	}
	var body string
	switch end.Why {
	case EndPasses:
		body = fmt.Sprintf("The discussion ended after %d rounds: every agent passed. ", rounds)
	case EndShip:
		body = fmt.Sprintf("The discussion ended after %d rounds: %s returned SHIP. ", rounds, who)
		if res.Action == BoundEscalateHuman {
			res.Question = boundShipNoDecisionText
		}
	case EndDecision:
		body = fmt.Sprintf("The discussion ended after %d rounds: %s stated a decision. ", rounds, who)
	default:
		end.Why = EndBound
		body = fmt.Sprintf("Debate bound reached after %d rounds. ", rounds)
	}
	if res.Action == BoundAdoptDecision {
		body += "The proposed decision " + res.DecisionID + " stands as the candidate; a human accepts, edits or rejects it. Further agent turns on this topic need a human message."
	} else {
		body += "No decision was proposed; a human is asked. Further agent turns on this topic need a human message."
	}
	p, err := s.Post(ctx, PostParams{RoomID: roomID, Type: TypeSystem, Author: System, Body: body, ThreadRootID: thread, IdempotencyKey: key,
		Payload: map[string]any{"event": "debate_bounded", "params": map[string]any{"rounds": rounds, "action": res.Action, "decision_id": res.DecisionID, "ended": end.Why}}})
	if err != nil {
		return res, false, err
	}
	if p.Duplicate {
		return res, false, nil
	}
	if res.Action == BoundEscalateHuman {
		if _, err := s.Post(ctx, PostParams{RoomID: roomID, Type: TypeQuestion, Author: System, Body: res.Question, ThreadRootID: thread, IdempotencyKey: key + ":q",
			Payload: map[string]any{"blocking": true, "to": "", "bound": true}}); err != nil {
			return res, true, err
		}
	}
	room, _ := s.GetRoom(ctx, roomID)
	s.flush(ctx, []pendingEvent{{typ: "room.debate_bounded", scope: events.Event{ProjectID: room.ProjectID, TaskID: room.TaskID},
		data: map[string]any{"room": roomID, "thread": thread, "rounds": rounds, "outcome": res.Action, "ended": end.Why}}})
	return res, true, nil
}
