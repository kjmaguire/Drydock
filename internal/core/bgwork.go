package core

import (
	"sync"
	"sync/atomic"

	"github.com/kjmaguire/drydock/internal/safe"
)

// bgWork is the background work of one Core that Close waits for: the watchers, loops and one-off jobs that run on the core's
// context (or an extension's) and end with it. Close cancels those contexts, then waits here before the database closes and the
// data folder is let go, so nothing a goroutine still does (a database write that now fails, the log line that reports it) lands
// in the data folder after Close has returned (a full test run on a loaded machine, 2026-11-09: a task-room watcher still
// handling task.cancelled wrote logs/drydock.log while the test removed the folder).
type bgWork struct {
	mu      sync.Mutex
	stopped bool
	wg      sync.WaitGroup
}

// goBG runs fn in the background; Close waits for it. Once Close has begun waiting, fn is not started (it reports false): the
// app is stopping, and whatever fn would have done is done by the next start.
func (c *Core) goBG(name string, fn func()) bool {
	c.work.mu.Lock()
	if c.work.stopped {
		c.work.mu.Unlock()
		return false
	}
	c.work.wg.Add(1)
	c.work.mu.Unlock()
	safe.Go(name, func() {
		defer c.work.wg.Done()
		fn()
	})
	return true
}

// awaitBG makes Close wait until done is closed: the done channel of an events.Watch, whose handler may still be running after its
// context ended.
func (c *Core) awaitBG(name string, done <-chan struct{}) {
	if !c.goBG(name, func() { <-done }) {
		<-done // Close is already waiting: the caller is on its way out, and so is the watcher
	}
}

// waitBG stops new background work from starting and waits for what runs. Close calls it after cancelling every context that
// work runs on.
func (c *Core) waitBG() {
	c.work.mu.Lock()
	c.work.stopped = true
	c.work.mu.Unlock()
	c.work.wg.Wait()
}

// roomRedactHook, when set, runs before the room scans a message or minutes for secrets: tests use it to hold a room write (the
// archive of a task room) open across Close.
var roomRedactHook atomic.Pointer[func()]
