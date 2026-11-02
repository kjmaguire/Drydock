package core

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/terminal"
)

// X27: the shell reused is the newest one still running in the folder, never a sign-in (no project) or one that ended.
func TestRunningShellIn(t *testing.T) {
	list := []terminal.Info{
		{ID: "a", Cwd: "/r", ProjectID: "p"},
		{ID: "b", Cwd: "/r/", ProjectID: "p"},
		{ID: "c", Cwd: "/r", ProjectID: "p", Exited: true},
		{ID: "d", Cwd: "/r"},
		{ID: "e", Cwd: "/other", ProjectID: "p"},
	}
	if in, ok := runningShellIn(list, "/r"); !ok || in.ID != "b" {
		t.Fatalf("got %+v %v, want b", in, ok)
	}
	if _, ok := runningShellIn(list[2:4], "/r"); ok {
		t.Fatal("reused an exited shell or a sign-in")
	}
}
