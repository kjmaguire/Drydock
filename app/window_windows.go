//go:build windows

package app

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/kjmaguire/drydock/internal/core"
)

// placeWindow opens the main window where and how big the person left it (G8): the kept size and position go into the window's
// options; once it is shown, the placement is checked against the monitors there are now (fitWindow) and the window is
// maximised when it was.
func placeWindow(c *core.Core, o application.WebviewWindowOptions) (application.WebviewWindowOptions, *core.WindowPlacement) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, ok := c.WindowPlacement(ctx)
	if !ok {
		return o, nil
	}
	o.Width, o.Height = max(p.Width, minWindowW), max(p.Height, minWindowH)
	o.InitialPosition, o.X, o.Y = application.WindowXY, p.X, p.Y
	return o, &p
}

// windowKeeper keeps the window's placement as it changes (written a second after the last move, so a drag is one write) and
// when it closes.
type windowKeeper struct {
	c      *core.Core
	w      *application.WebviewWindow
	mu     sync.Mutex
	p      core.WindowPlacement
	timer  *time.Timer
	closed bool // the window closed: its placement is written and nothing more is (the core closes after it)
}

// keepWindowPlacement follows the window's moves, resizes and maximising; kept is the placement it was opened with (nil: none).
func keepWindowPlacement(wa *application.App, c *core.Core, w *application.WebviewWindow, kept *core.WindowPlacement) {
	k := &windowKeeper{c: c, w: w}
	if kept != nil {
		k.p = *kept
	}
	var first sync.Once
	w.OnWindowEvent(events.Windows.WindowShow, func(*application.WindowEvent) {
		first.Do(func() {
			if kept != nil {
				fit := fitWindow(*kept, workAreas(wa))
				if fit.X != kept.X || fit.Y != kept.Y || fit.Width != kept.Width || fit.Height != kept.Height {
					w.SetBounds(application.Rect{X: fit.X, Y: fit.Y, Width: fit.Width, Height: fit.Height})
				}
				if kept.Maximised {
					w.Maximise()
				}
			}
			k.record()
		})
	})
	for _, e := range []events.WindowEventType{events.Common.WindowDidMove, events.Common.WindowDidResize, events.Common.WindowMaximise,
		events.Common.WindowUnMaximise, events.Common.WindowRestore} {
		w.OnWindowEvent(e, func(*application.WindowEvent) { k.record() })
	}
	// the last placement is written before the window goes (no call into the window here: it is closing)
	w.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		k.save()
		k.mu.Lock()
		k.closed = true
		k.mu.Unlock()
	})
}

// record reads the window's placement: its bounds while it is neither maximised nor minimised (a maximised window keeps the
// bounds it is restored to), and whether it is maximised.
func (k *windowKeeper) record() {
	if k.w.IsMinimised() {
		return
	}
	maximised := k.w.IsMaximised()
	var b application.Rect
	if !maximised {
		b = k.w.Bounds()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return
	}
	k.p.Maximised = maximised
	if !maximised && b.Width > 0 && b.Height > 0 {
		k.p.X, k.p.Y, k.p.Width, k.p.Height = b.X, b.Y, b.Width, b.Height
	}
	if k.timer == nil {
		k.timer = time.AfterFunc(time.Second, k.save)
	} else {
		k.timer.Reset(time.Second)
	}
}

func (k *windowKeeper) save() {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return
	}
	if k.timer != nil {
		k.timer.Stop()
	}
	p := k.p
	k.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := k.c.SetWindowPlacement(ctx, p); err != nil {
		fmt.Fprintf(os.Stderr, "drydock: keeping the window's place: %v\n", err)
	}
}

// workAreas are the monitors' work areas now, the main monitor first.
func workAreas(wa *application.App) []workArea {
	screens := append([]*application.Screen(nil), wa.Screen.GetAll()...)
	sort.SliceStable(screens, func(i, j int) bool { return screens[i].IsPrimary && !screens[j].IsPrimary })
	out := make([]workArea, 0, len(screens))
	for _, s := range screens {
		if s != nil {
			out = append(out, workArea{X: s.WorkArea.X, Y: s.WorkArea.Y, W: s.WorkArea.Width, H: s.WorkArea.Height})
		}
	}
	return out
}
