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
// the viewer ends. The operating system part (starting the process) is Start, so the counting and the folders are tested without a
// window.
type Launcher struct {
	DataDir string
	Max     int
	// Start starts a viewer with the arguments Args made.
	Start func(args []string) (Process, error)
	// RemoveTries and RemoveWait bound how long a profile is retried when WebView2's processes still hold files in it.
	RemoveTries int
	RemoveWait  time.Duration
	// Remove is os.RemoveAll except in a test.
	Remove func(string) error
	// Logf receives what could not be cleaned up; nil is silent.
	Logf func(format string, args ...any)

	mu      sync.Mutex
	running int
	wg      sync.WaitGroup
}

// Open starts a viewer for r. The slot it takes is given back when the viewer's process ends, not when the profile has been
// removed, so closing a window and opening another never waits for the cleanup.
func (l *Launcher) Open(r Request) error {
	if err := ValidURL(r.URL); err != nil {
		return err
	}
	l.mu.Lock()
	if l.running >= l.Max {
		l.mu.Unlock()
		return fmt.Errorf("%d windows of sandbox apps are open: close one first", l.Max)
	}
	l.running++
	l.mu.Unlock()
	release := func() {
		l.mu.Lock()
		l.running--
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
