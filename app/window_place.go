package app

import "github.com/kjmaguire/drydock/internal/core"

// The Drydock window opens where and how big the person left it (G8): window_windows.go keeps its placement as it changes and
// reads it at the next start. This is the part that needs no window: putting a kept placement back on a monitor that exists now.

// workArea is a monitor's work area (without the taskbar) in device-independent pixels.
type workArea struct{ X, Y, W, H int }

// The smallest window Drydock opens (the window's own minimum, app_windows.go) and its size when nothing is kept.
const (
	minWindowW, minWindowH         = 1000, 600
	defaultWindowW, defaultWindowH = 1360, 860
)

// fitWindow puts a kept placement on the monitors there are now: on the one it overlaps most (the first, the main monitor, when it
// overlaps none, as after unplugging the monitor it was on), no larger than that monitor's work area and wholly inside it. With no
// monitor known the placement is returned as kept.
func fitWindow(p core.WindowPlacement, areas []workArea) core.WindowPlacement {
	p.Width, p.Height = max(p.Width, minWindowW), max(p.Height, minWindowH)
	if len(areas) == 0 {
		return p
	}
	best, most := areas[0], 0
	for _, a := range areas {
		w := min(p.X+p.Width, a.X+a.W) - max(p.X, a.X)
		h := min(p.Y+p.Height, a.Y+a.H) - max(p.Y, a.Y)
		if w > 0 && h > 0 && w*h > most {
			best, most = a, w*h
		}
	}
	p.Width, p.Height = min(p.Width, best.W), min(p.Height, best.H)
	p.X = min(max(p.X, best.X), best.X+best.W-p.Width)
	p.Y = min(max(p.Y, best.Y), best.Y+best.H-p.Height)
	return p
}
