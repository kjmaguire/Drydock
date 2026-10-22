package tasks

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/events"
)

func TestLabelsAreTrimmedLowerCaseAndDedupedWithinTheirLimits(t *testing.T) {
	got, err := NormalizeLabels([]string{"  Auth ", "auth", "", "Flaky   Test", "ci/linux", "v1.2:rc"})
	if err != nil || !reflect.DeepEqual(got, []string{"auth", "flaky test", "ci/linux", "v1.2:rc"}) {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := NormalizeLabels(nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no labels: %#v, %v", got, err)
	}
	if _, err := NormalizeLabels([]string{strings.Repeat("a", MaxLabelLen)}); err != nil {
		t.Fatalf("a label of %d characters is allowed: %v", MaxLabelLen, err)
	}
	for name, in := range map[string][]string{
		"too long":   {strings.Repeat("a", MaxLabelLen+1)},
		"too many":   {"a", "b", "c", "d", "e", "f", "g", "h", "i"},
		"odd char":   {"bad<tag>"},
		"a new line": {"one\x00two"},
	} {
		if _, err := NormalizeLabels(in); err == nil {
			t.Errorf("%s: %q was accepted", name, in)
		}
	}
	// repeats do not count toward the limit
	if got, err := NormalizeLabels([]string{"a", "A", "b", "c", "d", "e", "f", "g", "h", " h "}); err != nil || len(got) != MaxLabels {
		t.Fatalf("eight distinct labels with repeats: %q, %v", got, err)
	}
}

func TestLabelsAreSetOnCreateAndReplacedWithAnUpdateEvent(t *testing.T) {
	s, bus := newSvc(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "x", Labels: []string{strings.Repeat("z", 30)}}); err == nil {
		t.Fatal("a task was made with a label that is too long")
	}
	tk, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Rate limiter", Labels: []string{"Backend", "auth"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tk.Labels, []string{"backend", "auth"}) {
		t.Fatalf("labels on create: %q", tk.Labels)
	}
	plain, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "No labels"})
	if b, _ := json.Marshal(plain); !strings.Contains(string(b), `"labels":[]`) {
		t.Fatalf("a task without labels says so with []: %s", b)
	}
	ch, cancel := bus.Subscribe(events.Filter{Types: []string{"task.updated"}})
	defer cancel()
	got, err := s.SetLabels(ctx, tk.ID, []string{"flaky", "AUTH"})
	if err != nil || !reflect.DeepEqual(got.Labels, []string{"flaky", "auth"}) || got.Version <= tk.Version {
		t.Fatalf("set: %+v, %v", got, err)
	}
	e := <-ch
	if e.TaskID != tk.ID || !strings.Contains(string(e.Data), `"labels":["flaky","auth"]`) {
		t.Fatalf("event %+v %s", e, e.Data)
	}
	if _, err := s.SetLabels(ctx, "task_nope", []string{"a"}); err != ErrNotFound {
		t.Fatalf("an unknown task: %v", err)
	}
	if _, err := s.SetLabels(ctx, tk.ID, []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}); err == nil {
		t.Fatal("nine labels were kept")
	}
	if cur, _ := s.Get(ctx, tk.ID); !reflect.DeepEqual(cur.Labels, []string{"flaky", "auth"}) {
		t.Fatalf("a refused set changed the labels: %q", cur.Labels)
	}
	if got, _ := s.SetLabels(ctx, tk.ID, nil); len(got.Labels) != 0 || got.Labels == nil {
		t.Fatalf("cleared: %#v", got.Labels)
	}
}

func TestTheListFiltersByLabelAssigneeAndWhenATaskEnded(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	if _, err := s.DB.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p2','other','/y',2)`); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "a", Labels: []string{"auth", "backend"}})
	b, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "b", Labels: []string{"backend"}})
	c, _ := s.Create(ctx, CreateReq{ProjectID: "p2", Title: "c", Labels: []string{"auth"}})
	d, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "d"})
	w := "agent_w"
	if _, err := s.Patch(ctx, b.ID, Update{Assignee: &w}); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{a.ID, b.ID, c.ID, d.ID} { // made in this order, a millisecond apart or not
		if _, err := s.DB.Writer().Exec(`UPDATE tasks SET created_at=? WHERE id=?`, 10*(i+1), id); err != nil {
			t.Fatal(err)
		}
	}
	// a ended at 1000, c at 3000; b and d are open
	for id, at := range map[string]int64{a.ID: 1000, c.ID: 3000} {
		if _, err := s.DB.Writer().Exec(`UPDATE tasks SET status='COMPLETED', completed_at=? WHERE id=?`, at, id); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(f Filter) []string {
		t.Helper()
		ts, err := s.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		n, err := s.Count(ctx, f)
		if err != nil || n != len(ts) {
			t.Fatalf("count %d (%v) for %d listed", n, err, len(ts))
		}
		var out []string
		for _, x := range ts {
			out = append(out, x.Title)
		}
		return out
	}
	for name, c := range map[string]struct {
		f    Filter
		want []string
	}{
		"label in a project":       {Filter{ProjectID: "p1", Label: "backend"}, []string{"b", "a"}},
		"label everywhere":         {Filter{Label: " AUTH "}, []string{"c", "a"}},
		"assignee":                 {Filter{Assignee: w}, []string{"b"}},
		"ended since":              {Filter{CompletedSince: 2000}, []string{"c"}},
		"ended since, all":         {Filter{CompletedSince: 1}, []string{"c", "a"}},
		"label and status":         {Filter{Label: "auth", Statuses: []Status{Completed}, ProjectID: "p1"}, []string{"a"}},
		"open or ended since":      {Filter{ProjectID: "p1", OpenOrEndedSince: 2000}, []string{"d", "b"}},
		"open or ended since, all": {Filter{OpenOrEndedSince: 500}, []string{"d", "c", "b", "a"}},
		"ids":                      {Filter{IDs: []string{a.ID, d.ID}}, []string{"d", "a"}},
		"no such label":            {Filter{Label: "nope"}, nil},
	} {
		if got := ids(c.f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

// testdata/steps.json holds the cases ui/app/screens/tasks/steps.test.ts passes too, so the board (task.summaries) and the task
// page put a task at the same step.
func TestTheStepRuleMatchesTheTaskPage(t *testing.T) {
	b, err := os.ReadFile("testdata/steps.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Name string `json:"name"`
			Task struct {
				Status  string `json:"status"`
				Started int64  `json:"started"`
			} `json:"task"`
			Evidence []struct {
				Kind   string `json:"kind"`
				Passed *bool  `json:"passed"`
			} `json:"evidence"`
			Reviews  int  `json:"reviews"`
			SpecGate bool `json:"specGate"`
			Want     struct {
				Step  string `json:"step"`
				Index int    `json:"index"`
			} `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &f); err != nil || len(f.Cases) < 10 {
		t.Fatalf("%d cases: %v", len(f.Cases), err)
	}
	for _, c := range f.Cases {
		var ev []Evidence
		for _, e := range c.Evidence {
			ev = append(ev, Evidence{Kind: e.Kind, Passed: e.Passed})
		}
		i := StepIndex(Task{Status: Status(c.Task.Status), Started: c.Task.Started}, ev, c.Reviews, c.SpecGate)
		if i != c.Want.Index || StepName(i) != c.Want.Step {
			t.Errorf("%s: %d %s, want %d %s", c.Name, i, StepName(i), c.Want.Index, c.Want.Step)
		}
	}
}
