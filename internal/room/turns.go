package room

import (
	"strings"
)

// This file is turn-taking as pure functions (room-protocol s6, token-control rules 1 to 3 and 15). Nothing here reads
// the database or calls a model: the same input always yields the same speakers. Service.PlanTurns gathers the input.

// TurnMember is the scheduling view of a room member.
type TurnMember struct {
	Key   string
	Kind  AuthorKind
	Role  string
	State MemberState
}

// TurnTrigger is the message that may wake agents.
type TurnTrigger struct {
	MessageID   string
	Author      Author
	Type        MsgType
	Mentioned   []string // resolved member keys in mention order; role mentions already expanded to their members
	AssigneeKey string   // owner of the thread or task, "" if none
	RoleHint    string   // role the message is relevant to, "" if none
}

// TurnInput is everything NextSpeakers needs.
type TurnInput struct {
	Mode            Mode
	Params          ModeParams
	Members         []TurnMember // in join order
	Trigger         TurnTrigger
	Rounds          int      // agent-to-agent rounds since the last human message (CountRounds)
	Spoken          []string // sequential: members that spoke or passed in the current round (SequentialProgress)
	RoundsCompleted int      // sequential: full passes completed since the last human message
}

// GrantReason says why a member was chosen, for the manifest and the UI.
type GrantReason string

const (
	ReasonMention   GrantReason = "mention"
	ReasonOwner     GrantReason = "owner"
	ReasonRole      GrantReason = "role"
	ReasonSequence  GrantReason = "sequence"
	ReasonNamed     GrantReason = "named_by_moderator"
	ReasonModerator GrantReason = "moderator_turn"
)

// Speaker is a member eligible to take a turn now.
type Speaker struct {
	Member string
	Reason GrantReason
}

// TurnDecision is the outcome of one trigger.
type TurnDecision struct {
	Speakers []Speaker
	Deferred []string // eligible members beyond MaxParallel (they get no turn from this trigger)
	Bounded  bool     // the debate bound is reached: no agent turns until a human speaks
	Round    int      // the round this trigger belongs to
	NewRound bool     // sequential: a new pass starts with this speaker
	Why      string
}

// wakes reports message types that can wake an agent without an explicit mention. STATUS and SYSTEM are display
// traffic; approval messages are answered by the engine, not by chat turns.
func wakes(t MsgType) bool {
	switch t {
	case TypeChat, TypeTask, TypeDecision, TypeQuestion, TypeHandoff, TypeResult, TypeError:
		return true
	}
	return false
}

func mentionWakes(t MsgType) bool {
	return t != TypeSystem && !t.isApprovalType()
}

func memberByKey(ms []TurnMember, key string) (TurnMember, bool) {
	for _, m := range ms {
		if m.Key == key {
			return m, true
		}
	}
	return TurnMember{}, false
}

// speakable: an agent member that may take a turn. Observers only when explicitly named; muted never.
func speakable(m TurnMember, author string, named bool) bool {
	if m.Kind != AuthorAgent || m.Key == author {
		return false
	}
	return m.State == StateActive || m.State == "" || (named && m.State == StateObserver)
}

// NextSpeakers decides who may take a turn after a trigger message. There is no LLM in the loop; the answer is a
// function of the mode, the membership and the trigger.
func NextSpeakers(in TurnInput) TurnDecision {
	max := in.Params.rounds()
	par := in.Params.parallel()
	t := in.Trigger
	author := t.Author.Key()
	human := t.Author.Kind == AuthorHuman
	rounds := in.Rounds
	if human { // a human message preempts and resets the counter (room-protocol s6 rule 3)
		rounds = 0
	}
	d := TurnDecision{Round: rounds + 1}
	if t.Author.Kind == AuthorSystem || !mentionWakes(t.Type) {
		d.Why = "no trigger: " + string(t.Type) + " from " + string(t.Author.Kind)
		return d
	}
	if t.Author.Kind == AuthorAgent && in.Mode != ModeSequential && rounds >= max {
		d.Bounded, d.Round = true, rounds
		d.Why = "debate bound reached"
		return d
	}
	pick := func(keys []string, reason GrantReason, named bool) {
		seen := map[string]bool{}
		for _, k := range keys {
			m, ok := memberByKey(in.Members, k)
			if !ok || seen[k] || !speakable(m, author, named) {
				continue
			}
			seen[k] = true
			if len(d.Speakers) >= par {
				d.Deferred = append(d.Deferred, k)
				continue
			}
			d.Speakers = append(d.Speakers, Speaker{Member: k, Reason: reason})
		}
	}

	switch in.Mode {
	case ModeSequential:
		return nextSequential(in, d, max, human)
	case ModeModerator:
		mod := in.Params.Moderator
		mm, modIn := memberByKey(in.Members, mod)
		modAgent := modIn && mm.Kind == AuthorAgent
		switch {
		case author == mod && mod != "":
			pick(t.Mentioned, ReasonNamed, true)
			d.Why = "named by moderator"
		case human:
			pick(t.Mentioned, ReasonMention, true)
			if len(d.Speakers) == 0 && modAgent && wakes(t.Type) {
				pick([]string{mod}, ReasonModerator, false)
			}
			d.Why = "human message in moderated room"
		case t.Author.Kind == AuthorAgent && modAgent && wakes(t.Type):
			pick([]string{mod}, ReasonModerator, false)
			d.Why = "moderator reads agent output"
		default:
			d.Why = "only invited speakers speak"
		}
	default: // auto
		pick(t.Mentioned, ReasonMention, true)
		if len(d.Speakers) == 0 && wakes(t.Type) && t.AssigneeKey != "" {
			pick([]string{t.AssigneeKey}, ReasonOwner, false)
		}
		if len(d.Speakers) == 0 && wakes(t.Type) && t.RoleHint != "" {
			var keys []string
			for _, m := range in.Members {
				if strings.EqualFold(m.Role, t.RoleHint) {
					keys = append(keys, m.Key)
				}
			}
			pick(keys, ReasonRole, false)
		}
		if len(d.Speakers) == 0 {
			d.Why = "nobody addressed"
		}
	}
	return d
}

// nextSequential picks the next member in the fixed order. The Spoken and RoundsCompleted state already includes the
// trigger message (SequentialProgress folds the whole transcript).
func nextSequential(in TurnInput, d TurnDecision, max int, human bool) TurnDecision {
	author := in.Trigger.Author.Key()
	if in.Trigger.Author.Kind == AuthorTool {
		d.Why = "sequential rooms ignore tool results"
		return d
	}
	src := in.Params.Order
	if len(src) == 0 {
		for _, m := range in.Members {
			src = append(src, m.Key)
		}
	}
	var order []string
	for _, k := range src {
		if m, ok := memberByKey(in.Members, k); ok && m.Kind == AuthorAgent && (m.State == StateActive || m.State == "") {
			order = append(order, k)
		}
	}
	spoken := map[string]bool{}
	completed := 0
	if !human {
		completed = in.RoundsCompleted
		for _, k := range in.Spoken {
			spoken[k] = true
		}
	}
	if completed >= max {
		d.Bounded, d.Round, d.Why = true, completed, "round bound reached"
		return d
	}
	for _, k := range order {
		if !spoken[k] && k != author {
			d.Round = completed + 1
			d.NewRound = len(spoken) == 0 && completed > 0
			d.Speakers = []Speaker{{Member: k, Reason: ReasonSequence}}
			d.Why = "next in fixed order"
			return d
		}
	}
	d.Why = "no other speaker"
	return d
}

// RoundMsg is the part of a message the round counters need.
type RoundMsg struct {
	Author Author
	Type   MsgType
	Pass   bool // the member passed its turn
}

// CountRounds counts debate rounds in a transcript: each time a different agent follows another agent counts one, and
// a human message resets to zero. System and tool messages are neutral. A full cycle H, A, B, A, B is 3 rounds.
func CountRounds(msgs []RoundMsg) int {
	rounds, prev := 0, ""
	for _, m := range msgs {
		switch m.Author.Kind {
		case AuthorHuman:
			rounds, prev = 0, ""
		case AuthorAgent:
			if m.Pass {
				continue
			}
			if prev != "" && prev != m.Author.Key() {
				rounds++
			}
			prev = m.Author.Key()
		}
	}
	return rounds
}

// SequentialProgress folds a transcript into the sequential-mode state: who has spoken or passed in the current pass
// and how many full passes finished since the last human message.
func SequentialProgress(order []string, msgs []RoundMsg) (spoken []string, completed int) {
	in := map[string]bool{}
	for _, k := range order {
		in[k] = true
	}
	seen := map[string]bool{}
	for _, m := range msgs {
		switch m.Author.Kind {
		case AuthorHuman:
			seen, completed = map[string]bool{}, 0
		case AuthorAgent:
			k := m.Author.Key()
			if !in[k] {
				continue
			}
			seen[k] = true
			if len(seen) == len(in) {
				completed++
				seen = map[string]bool{}
			}
		}
	}
	for _, k := range order {
		if seen[k] {
			spoken = append(spoken, k)
		}
	}
	return spoken, completed
}

// DebateMsg is the part of a message the bound resolution needs.
type DebateMsg struct {
	ID     string
	Author Author
	Type   MsgType
	Status string // DECISION payload status
}

// BoundAction is what the scheduler does when the debate bound is reached (room-protocol s6 rule 2).
type BoundAction string

const (
	BoundAdoptDecision  BoundAction = "decision_candidate" // a DECISION proposal from the debate stands as the candidate
	BoundEscalateHuman  BoundAction = "human_question"     // no decision converged: ask a human
	boundNoDecisionText             = "The agents reached the debate bound without a proposed decision. Please decide, or give direction, to continue."
)

// BoundResolution is the outcome of ResolveBound.
type BoundResolution struct {
	Action     BoundAction
	DecisionID string // BoundAdoptDecision: the message that becomes the candidate
	Question   string // BoundEscalateHuman: the blocking question to post
}

// ResolveBound decides how a bounded debate ends: if an agent proposed a DECISION since the last human message, that is
// the candidate (a human accepts, edits or rejects it as an ADR candidate); otherwise a blocking QUESTION goes to a human.
func ResolveBound(msgs []DebateMsg) BoundResolution {
	cand := ""
	for _, m := range msgs {
		switch {
		case m.Author.Kind == AuthorHuman:
			cand = ""
		case m.Type == TypeDecision && (m.Status == "proposed" || m.Status == "accepted"):
			cand = m.ID
		}
	}
	if cand != "" {
		return BoundResolution{Action: BoundAdoptDecision, DecisionID: cand}
	}
	return BoundResolution{Action: BoundEscalateHuman, Question: boundNoDecisionText}
}
