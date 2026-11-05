package core

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/pipeline"
)

// "Accept and start" whose scheduler pass fails: the plan was accepted and its tasks exist, so plan.accept answers with the
// accepted plan and the pass's error beside it, never with only the error (the screen would say "Not sent" and offer Accept again).
func TestPlanAcceptStartFailureKeepsTheAcceptedPlan(t *testing.T) {
	pl := pipeline.Plan{ID: "plan_1", Status: "ACCEPTED", Created: map[string]string{"mul": "task_1"}}
	v := withStart(planViewOf(pl, nil), pipeline.TickResult{Promoted: []string{"task_1"}}, errors.New("database is locked"))
	if v.Status != "ACCEPTED" || v.Created["mul"] != "task_1" || v.Start != nil || v.StartError != "database is locked" {
		t.Fatalf("failed pass: %+v", v)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if js := string(b); !strings.Contains(js, `"startError":"database is locked"`) || strings.Contains(js, `"start":`) || !strings.Contains(js, `"status":"ACCEPTED"`) {
		t.Fatalf("json %s", js)
	}

	ok := withStart(planViewOf(pl, nil), pipeline.TickResult{Started: []string{"task_1"}, Max: 2}, nil)
	if ok.Start == nil || len(ok.Start.Started) != 1 || ok.StartError != "" {
		t.Fatalf("passed pass: %+v", ok)
	}
	if b, _ := json.Marshal(ok); strings.Contains(string(b), "startError") {
		t.Fatalf("no error, no startError: %s", b)
	}
}
