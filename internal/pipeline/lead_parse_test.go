package pipeline_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
)

var parseTeam = []pipeline.LeadMember{
	{ID: "a1", Name: "Backend #1", Role: "backend", Adapter: "claude", Load: 1},
	{ID: "a2", Name: "Reviewer #1", Role: "reviewer", Adapter: "codex"},
	{ID: "a3", Name: "Backend #2", Role: "backend", Adapter: "gemini"},
	{ID: "l1", Name: "Lead", Role: "lead", Adapter: "claude"},
}

func parseCheck(trigger string) pipeline.LeadCheck {
	return pipeline.LeadCheck{Team: parseTeam, Lead: parseTeam[3], Task: tasks.Task{ID: "t1", Assignee: "a1", Reviewer: "a2"}, Trigger: trigger}
}

func TestParseLeadDecisionAcceptsEachAction(t *testing.T) {
	for in, want := range map[string]string{
		`{"action":"nudge","message":"edit calc.go","reason":"spin"}`:                                                                                      "nudge",
		"```json\n{\"action\":\"escalate\",\"message\":\"which API?\",\"reason\":\"product call\"}\n```":                                                   "escalate",
		`{"action":"continue","message":"","reason":"it is progressing"}`:                                                                                  "continue",
		`{"action":"REASSIGN","agent":"backend #2","message":"take over","reason":"load"}`:                                                                 "reassign",
		`{"action":"split","message":"narrow","tasks":[{"title":"Docs","description":"d","acceptanceCriteria":["Given x, when y, then z"]}],"reason":"r"}`: "split",
	} {
		d, agent, probs := pipeline.ParseLeadDecision(in, parseCheck(pipeline.TriggerIterationLimit))
		if len(probs) != 0 || d.Action != want {
			t.Errorf("%s -> %+v %v", in, d, probs)
		}
		if want == "reassign" && agent != "a3" {
			t.Errorf("reassign resolves the name to an id, got %q", agent)
		}
	}
}

func TestParseLeadDecisionRefusesWhatItCannotApply(t *testing.T) {
	cases := map[string]string{
		`I think nudge`: "not a JSON object",
		`{"action":"nudge","message":"m","reason":"r","extra":1}`:                                                                                  "unknown field",
		`{"action":"nudge","message":"m","reason":"r"} {"action":"continue"}`:                                                                      "more than one JSON value",
		`{"action":"panic","message":"m","reason":"r"}`:                                                                                            "not one of",
		`{"action":"nudge","message":"","reason":"r"}`:                                                                                             "message is required",
		`{"action":"nudge","message":"m","reason":""}`:                                                                                             "reason is required",
		`{"action":"reassign","message":"m","reason":"r"}`:                                                                                         "needs \"agent\"",
		`{"action":"reassign","agent":"Nobody","message":"m","reason":"r"}`:                                                                        "not on this team",
		`{"action":"reassign","agent":"Lead","message":"m","reason":"r"}`:                                                                          "never takes a task",
		`{"action":"reassign","agent":"Reviewer #1","message":"m","reason":"r"}`:                                                                   "must be different agents",
		`{"action":"reassign","agent":"a1","message":"m","reason":"r"}`:                                                                            "changes nothing",
		`{"action":"nudge","agent":"a3","message":"m","reason":"r"}`:                                                                               "only for reassign",
		`{"action":"split","message":"m","reason":"r"}`:                                                                                            "1 to 5 tasks",
		`{"action":"split","message":"m","reason":"r","tasks":[{"title":"T","description":"d","acceptanceCriteria":["it works"]}]}`:                "Given/When/Then",
		`{"action":"nudge","message":"m","reason":"r","tasks":[{"title":"T","description":"d","acceptanceCriteria":["Given a, when b, then c"]}]}`: "only for split",
		`{"action":"nudge","message":"` + strings.Repeat("m", 2100) + `","reason":"r"}`:                                                            "longer than",
	}
	for in, want := range cases {
		_, _, probs := pipeline.ParseLeadDecision(in, parseCheck(pipeline.TriggerIterationLimit))
		if len(probs) == 0 || !strings.Contains(strings.Join(probs, " | "), want) {
			t.Errorf("%.60s: want a problem with %q, got %v", in, want, probs)
		}
	}
	// nobody to nudge on an unassigned task
	if _, _, probs := pipeline.ParseLeadDecision(`{"action":"nudge","message":"m","reason":"r"}`, parseCheck(pipeline.TriggerUnassigned)); len(probs) == 0 {
		t.Error("a nudge on an unassigned task is refused")
	}
}

func TestLeadPromptIsACompactDigest(t *testing.T) {
	p := pipeline.LeadPrompt(pipeline.LeadDigest{
		Trigger: pipeline.TriggerRepeatedRevise, Evidence: "same feedback twice", Task: tasks.Task{Seq: 4, Title: "Fix login", Status: tasks.InProgress, Iteration: 2, MaxIterations: 5, Assignee: "a1", Reviewer: "a2", Acceptance: []string{"Given a, when b, then c"}},
		Brief: "B2B app", ToolCalls: []string{"Read {\"file_path\":\"a.go\"}"}, FailingCheck: "test: TestLogin failed", Reviews: []string{"Handle the nil user."}, LastText: "I changed it.",
		DiffStat: "1 file(s), +3 -1", Team: parseTeam, Lead: parseTeam[3], Interventions: 1, Max: 3,
	})
	for _, want := range []string{"Drydock phase: supervise", "You are Lead, the team lead", "the reviewer repeated the same feedback", "Task #4: Fix login [IN_PROGRESS, review round 2 of 5]",
		"Given a, when b, then c", "Trigger: repeated_revise", "same feedback twice", "Read {\"file_path\":\"a.go\"}", "TestLogin failed", "Handle the nil user.", "I changed it.",
		"1 file(s), +3 -1", "Backend #1: backend, claude, 1 [works on this task]", "Reviewer #1: reviewer, codex, 0 [reviews this task]", "Lead: lead, claude, 0 [you]",
		"intervened 1 of at most 3", "B2B app", `{"action":"nudge|reassign|split|escalate|continue"`} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
	if len(p) > 6000 {
		t.Fatalf("the digest should stay compact: %d bytes", len(p))
	}
}
