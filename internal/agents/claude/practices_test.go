package claude

import (
	"strings"
	"testing"
)

// The team lead has its own short practices text: it supervises, never writes code, prefers the smallest intervention and
// escalates when unsure. The version moved with the text.
func TestLeadPractices(t *testing.T) {
	for _, role := range []string{"lead", "Team Lead", "team-lead", "manager", "Supervisor"} {
		if FamilyOf(role) != FamilyLead {
			t.Errorf("%q: family %s", role, FamilyOf(role))
		}
	}
	p := Practices("lead")
	for _, want := range []string{"never write or change code", "smallest intervention", "Escalate to the human when you are unsure"} {
		if !strings.Contains(p, want) {
			t.Fatalf("lead practices lack %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "Never skip, delete or weaken a test") || strings.Contains(p, "You do not modify files.\n") {
		t.Fatal("the lead gets neither the worker's nor the reviewer's text")
	}
	if len(p) > len(Practices("backend"))+200 {
		t.Fatalf("keep the lead text short: %d bytes", len(p))
	}
	if PracticesVersion < 2 {
		t.Fatal("the practices text changed: bump PracticesVersion")
	}
	if pr := PresetFor("lead"); pr.Model == "" || pr.Effort == "" {
		t.Fatalf("preset %+v", pr)
	}
	if FamilyOf("backend") != FamilyWorker || FamilyOf("reviewer") != FamilyReviewer || FamilyOf("pm") != FamilyPlanner {
		t.Fatal("the other families are unchanged")
	}
}
