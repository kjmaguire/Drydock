package app

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

// G8: a kept placement comes back where it was while its monitor is there, and is moved onto a monitor that exists when not.
func TestFitWindow(t *testing.T) {
	main := workArea{0, 0, 1920, 1040}
	left := workArea{-2560, 0, 2560, 1400}
	for _, c := range []struct {
		name  string
		p     core.WindowPlacement
		areas []workArea
		want  core.WindowPlacement
	}{
		{"where it was", core.WindowPlacement{X: 100, Y: 50, Width: 1400, Height: 900}, []workArea{main, left}, core.WindowPlacement{X: 100, Y: 50, Width: 1400, Height: 900}},
		{"on the second monitor", core.WindowPlacement{X: -2000, Y: 100, Width: 1600, Height: 1000, Maximised: true}, []workArea{main, left}, core.WindowPlacement{X: -2000, Y: 100, Width: 1600, Height: 1000, Maximised: true}},
		{"its monitor unplugged: onto the main one", core.WindowPlacement{X: -2000, Y: 100, Width: 1600, Height: 1000}, []workArea{main}, core.WindowPlacement{X: 0, Y: 40, Width: 1600, Height: 1000}},
		{"half off the edge: pulled in", core.WindowPlacement{X: 1500, Y: 900, Width: 1200, Height: 700}, []workArea{main}, core.WindowPlacement{X: 720, Y: 340, Width: 1200, Height: 700}},
		{"larger than the monitor: shrunk to it", core.WindowPlacement{X: 0, Y: 0, Width: 2500, Height: 1400}, []workArea{main}, core.WindowPlacement{X: 0, Y: 0, Width: 1920, Height: 1040}},
		{"smaller than the window's minimum: grown", core.WindowPlacement{X: 10, Y: 10, Width: 300, Height: 200}, []workArea{main}, core.WindowPlacement{X: 10, Y: 10, Width: 1000, Height: 600}},
		{"no monitor known", core.WindowPlacement{X: 5, Y: 5, Width: 1200, Height: 800}, nil, core.WindowPlacement{X: 5, Y: 5, Width: 1200, Height: 800}},
	} {
		if got := fitWindow(c.p, c.areas); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
