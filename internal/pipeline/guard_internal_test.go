package pipeline

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
)

func TestFootprintEntriesCoverFilesDirectoriesAndGlobs(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"calc.go", "calc.go", true},
		{"./calc.go", "calc.go", true},
		{"calc.go", "calc_test.go", false},
		{"internal/auth", "internal/auth/token.go", true},
		{"internal/auth/", "internal/auth/token.go", true},
		{"internal/auth/**", "internal/auth/deep/x.go", true},
		{"internal/auth", "internal/authz/x.go", false},
		{"*.go", "calc.go", true},
		{"*.go", "sub/calc.go", false},
		{"internal/*/token.go", "internal/auth/token.go", true},
		{"internal\\auth", "internal/auth/token.go", true},
	}
	for _, c := range cases {
		if got := covers(c.pattern, c.path); got != c.want {
			t.Errorf("covers(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestOverlapIsSymmetricAndListsMyEntries(t *testing.T) {
	mine := []string{"internal/auth/token.go", "README.md", "ui/src/App.tsx"}
	theirs := []string{"internal/auth/", "docs/guide.md"}
	if got := overlap(mine, theirs); !reflect.DeepEqual(got, []string{"internal/auth/token.go"}) {
		t.Fatalf("overlap = %v", got)
	}
	if got := overlap(theirs, mine); !reflect.DeepEqual(got, []string{"internal/auth/"}) {
		t.Fatalf("reverse overlap = %v", got)
	}
	if got := overlap(mine, []string{"docs/guide.md"}); len(got) != 0 {
		t.Fatalf("no overlap expected: %v", got)
	}
}

// Earlier means started first, with the sequence number breaking a tie, so two tasks never wait for each other.
func TestEarlierIsATotalOrder(t *testing.T) {
	a := tasks.Task{ID: "a", Seq: 1, Started: 100}
	b := tasks.Task{ID: "b", Seq: 2, Started: 100}
	c := tasks.Task{ID: "c", Seq: 3, Started: 50}
	idle := tasks.Task{ID: "i", Seq: 4}
	if !earlier(a, b) || earlier(b, a) {
		t.Fatal("same instant: lower sequence first")
	}
	if !earlier(c, a) || earlier(a, c) {
		t.Fatal("the task that started first is earlier")
	}
	if earlier(idle, a) {
		t.Fatal("a task that has not started is never earlier")
	}
	if !earlier(a, idle) {
		t.Fatal("a started task is earlier than one that has not started")
	}
	for _, x := range []tasks.Task{a, b, c} {
		if earlier(x, x) {
			t.Fatalf("%s is earlier than itself", x.ID)
		}
	}
}

func TestDeclaredCollisionOnlyLooksAtStartedTasks(t *testing.T) {
	cand := tasks.Task{ID: "n", Title: "New", Files: []string{"internal/auth/"}}
	running := tasks.Task{ID: "r", Title: "Running", Started: 10, Files: []string{"internal/auth/token.go"}}
	notStarted := tasks.Task{ID: "q", Title: "Queued", Files: []string{"internal/auth/token.go"}}
	other := tasks.Task{ID: "o", Title: "Other", Started: 11, Files: []string{"ui/"}}
	if c := declaredCollision(cand, []tasks.Task{other, notStarted}); c != nil {
		t.Fatalf("no collision expected: %+v", c)
	}
	c := declaredCollision(cand, []tasks.Task{other, running})
	if c == nil || c.Task.ID != "r" || len(c.Files) != 1 || c.Files[0] != "internal/auth/" {
		t.Fatalf("collision = %+v", c)
	}
	if declaredCollision(tasks.Task{ID: "x"}, []tasks.Task{running}) != nil {
		t.Fatal("a task with no planned files is not held back ahead of time")
	}
}

// A task that waits works out its own footprint once; each look afterwards asks git for the footprint of the earlier tasks only,
// where it used to ask for both, and the waits between looks grow (audit 2026-10-16, L13).
func TestTheGuardAsksForTheWaitingTasksOwnFootprintOnce(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 64)
	t.Cleanup(func() { bus.Close(); db.Close() })
	if _, err := db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','demo','/x',1)`); err != nil {
		t.Fatal(err)
	}
	svc := &tasks.Service{DB: db, Bus: bus}
	mk := func(title string) tasks.Task {
		tk, err := svc.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: title})
		if err != nil {
			t.Fatal(err)
		}
		a, r, files, wt := "a1", "a2", []string{"calc.go"}, "/worktrees/"+title
		tk, err = svc.Patch(ctx, tk.ID, tasks.Update{Assignee: &a, Reviewer: &r, Files: &files, Worktree: &wt})
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	earlier, mine := mk("earlier"), mk("mine")
	for _, to := range []tasks.Status{tasks.Ready, tasks.Claimed, tasks.InProgress} {
		if _, err := svc.Transition(ctx, earlier.ID, to, tasks.TransitionOpts{By: "system"}); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	d := Deps{Tasks: svc, Repo: func(context.Context, string) (*git.Repo, error) {
		calls++
		return nil, errors.New("no repository in this test")
	}}
	footprint := d.footprint(ctx, mine, "main")
	if calls != 1 {
		t.Fatalf("one footprint asked git %d times", calls)
	}
	calls = 0
	for i := 0; i < 6; i++ {
		cs, err := d.collisionsFor(ctx, mine, footprint, "main")
		if err != nil || len(cs) != 1 || cs[0].Task.ID != earlier.ID {
			t.Fatalf("look %d: %+v %v", i, cs, err)
		}
	}
	if calls != 6 {
		t.Fatalf("six looks asked git %d times: the waiting task's own footprint was worked out again", calls)
	}
}

func TestAWaitingTaskLooksLessOftenTheLongerItWaits(t *testing.T) {
	base := 2 * time.Second
	wait := base
	var got []time.Duration
	for polls := 1; polls <= 12; polls++ {
		got = append(got, wait)
		wait = nextGuardWait(wait, base, polls)
	}
	want := []time.Duration{2, 2, 2, 2, 2, 4, 8, 16, 16, 16, 16, 16}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("waits %v, want %v seconds", got, want)
		}
	}
}
