package sandboxview

import (
	"fmt"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/safe"
)

// Process is a started viewer.
type Process interface{ Wait() error }

// Launcher starts viewers and keeps count of them: at most Max at once, each with a profile folder the app made and removes when
// the viewer ends. One address has one viewer (effort review X314): opening it again brings that window to the front (Front)
// instead of starting a second, and Close ends it when its forward stops. The operating system part (starting the process,
// raising and ending its window) is Start, Front and Stop, so the counting, the folders and the one viewer per address are tested
// without a window.
type Launcher struct {
	DataDir string
	Max     int
	// Start starts a viewer with the arguments Args made.
	Start func(args []string) (Process, error)
	// Front brings a running viewer's window to the front; nil leaves it where it is.
	Front func(p Process) error
	// Stop ends a running viewer; nil leaves it running.
	Stop func(p Process) error
	// RemoveTries and RemoveWait bound how long a profile is retried when WebView2's processes still hold files in it.
	RemoveTries int
	RemoveWait  time.Duration
	// Remove is os.RemoveAll except in a test.
	Remove func(string) error
	// Logf receives what could not be cleaned up; nil is silent.
	Logf func(format string, args ...any)

	mu      sync.Mutex
	running int
	// byURL is the viewer of each address: nil while it is being started
	byURL map[string]Process
	wg    sync.WaitGroup
}

// Open starts a viewer for r. The slot it takes is given back when the viewer's process ends, not when the profile has been
// removed, so closing a window and opening another never waits for the cleanup.
func (l *Launcher) Open(r Request) error {
	if err := ValidURL(r.URL); err != nil {
		return err
	}
	l.mu.Lock()
	if p, ok := l.byURL[r.URL]; ok {
		l.mu.Unlock()
		// this address has its window (or one is starting): that one comes to the front, no second opens
		if p != nil && l.Front != nil {
			return l.Front(p)
		}
		return nil
	}
	if l.running >= l.Max {
		l.mu.Unlock()
		return fmt.Errorf("%d windows of sandbox apps are open: close one first", l.Max)
	}
	l.running++
	if l.byURL == nil {
		l.byURL = map[string]Process{}
	}
	l.byURL[r.URL] = nil
	l.mu.Unlock()
	var started Process
	release := func() {
		l.mu.Lock()
		l.running--
		if p, ok := l.byURL[r.URL]; ok && p == started {
			delete(l.byURL, r.URL)
		}
		l.mu.Unlock()
	}
	profile, err := NewProfile(l.DataDir)
	if err != nil {
		release()
		return err
	}
	r.Profile = profile
	args, err := Args(r)
	if err != nil {
		release()
		_ = l.removeProfile(profile)
		return err
	}
	p, err := l.Start(args)
	if err != nil {
		release()
		_ = l.removeProfile(profile)
		return err
	}
	l.mu.Lock()
	started = p
	l.byURL[r.URL] = p
	l.mu.Unlock()
	l.wg.Add(1)
	safe.Go("sandboxview.Launcher.wait", func() {
		defer l.wg.Done()
		_ = p.Wait()
		release()
		if err := l.removeProfile(profile); err != nil && l.Logf != nil {
			l.Logf("sandbox app window: profile %s: %v", profile, err)
		}
	})
	return nil
}

// Shown says whether a viewer of the address is open (or starting).
func (l *Launcher) Shown(url string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.byURL[url]
	return ok
}

// Close ends the viewer of the address, when one runs: its forward stopped, so the window would show nothing. Its slot and its
// profile are given back when its process has ended, as for a window the person closed.
func (l *Launcher) Close(url string) error {
	l.mu.Lock()
	p := l.byURL[url]
	l.mu.Unlock()
	if p == nil || l.Stop == nil {
		return nil
	}
	return l.Stop(p)
}

// Running is the number of viewers whose process has not ended.
func (l *Launcher) Running() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

// Wait blocks until every viewer started so far has ended and its profile has been dealt with.
func (l *Launcher) Wait() { l.wg.Wait() }

func (l *Launcher) removeProfile(dir string) error {
	tries, wait := l.RemoveTries, l.RemoveWait
	if tries <= 0 {
		tries = 60
	}
	if wait <= 0 {
		wait = 250 * time.Millisecond
	}
	return RemoveProfile(dir, tries, wait, l.Remove)
}
