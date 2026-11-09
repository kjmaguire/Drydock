package core_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Close waits for the background work it cancels. The task-room watcher archives a task's room when task.cancelled arrives; on a
// loaded machine that archive was still running when Close returned, and the database write it then failed was reported to
// logs/drydock.log while the test removed the data folder (TestIntegrationsP114ThroughTheUIMethods, 2026-11-09: "unlinkat
// .../data: directory not empty"). Here the archive is held open across Close: Close must not return before it has ended, and
// nothing may be written to the data folder afterwards.
func TestCloseWaitsForTheTaskRoomArchive(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			c.Close()
		}
	})
	a := &app{t: t, c: c}
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	task, err := c.CreateTask(context.Background(), tasks.CreateReq{ProjectID: p.ID, Title: "Held archive"})
	if err != nil {
		t.Fatal(err)
	}

	// the first secret scan after this point is the watcher's minutes of the task room: it waits until released
	entered, release := make(chan struct{}), make(chan struct{})
	var held, returned atomic.Bool
	defer core.SetRoomRedactHookForTests(func() {
		if held.CompareAndSwap(false, true) {
			close(entered)
			<-release
			returned.Store(true)
		}
	})()
	if _, err := c.Bus.Publish(context.Background(), events.Event{Type: "task.cancelled", ProjectID: p.ID, TaskID: task.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the task-room watcher did not archive the room")
	}

	// Close while the archive is held; the archive is let go only a while after Close began
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(release)
	}()
	c.Close()
	closed = true
	if !returned.Load() {
		t.Error("Close returned while the task room's archive was still running")
	}
	before := snapshotDir(t, dataDir)
	time.Sleep(500 * time.Millisecond)
	after := snapshotDir(t, dataDir)
	for path, mod := range after {
		if was, ok := before[path]; !ok || !was.Equal(mod) {
			t.Errorf("written after Close returned: %s", path)
		}
	}
}

// snapshotDir is every file and folder under dir with its modification time.
func snapshotDir(t *testing.T, dir string) map[string]time.Time {
	t.Helper()
	out := map[string]time.Time{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[path] = info.ModTime()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
