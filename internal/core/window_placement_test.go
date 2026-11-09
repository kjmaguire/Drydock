package core_test

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

// G8: the window's place is kept for the next start; nothing kept, or a placement without a size, opens it as before.
func TestWindowPlacementIsKept(t *testing.T) {
	a := open(t, "")
	ctx := t.Context()
	if _, ok := a.c.WindowPlacement(ctx); ok {
		t.Fatal("a placement before any was kept")
	}
	want := core.WindowPlacement{X: -1700, Y: 40, Width: 1500, Height: 900, Maximised: true}
	if err := a.c.SetWindowPlacement(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, ok := a.c.WindowPlacement(ctx); !ok || got != want {
		t.Fatalf("got %+v %v, want %+v", got, ok, want)
	}
	// a window that never had a size (minimised at start) does not overwrite the last good placement
	if err := a.c.SetWindowPlacement(ctx, core.WindowPlacement{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.c.WindowPlacement(ctx); got != want {
		t.Fatalf("overwritten by an empty placement: %+v", got)
	}
}
