package pipeline

import (
	"reflect"
	"testing"

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
