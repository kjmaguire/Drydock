package tasks

import (
	"context"
	"testing"
)

// A task a person runs again (decision P124) names the ended task it runs again; it is a person's task, not a lead's
// follow-up: it does not wait for a person, does not count toward the lead's limit, and its origin is checked.
func TestATaskRunAgainNamesTheTaskItRunsAgain(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	old, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "first", Origin: AgainOrigin(old.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if again.AgainOf() != old.ID || again.LeadDepth() != 0 || again.WaitsForAHuman() || old.AgainOf() != "" {
		t.Fatalf("again %+v", again)
	}
	for _, o := range []string{"again:", "again:../x", "again:a b", "elsewhere:1"} {
		if _, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "x", Origin: o}); err == nil {
			t.Errorf("origin %q was accepted", o)
		}
	}
}
