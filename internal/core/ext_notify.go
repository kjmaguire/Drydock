package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Telling the person outside the window that something needs them (decision G1). The core watches its own Inbox (NeedsYou, every
// project) and publishes needsyou.added once for each item that was not in it before: the desktop shell turns that into taskbar
// attention while the window is not in front, as the person's switch in Settings > General says (notify.get, notify.set; on by
// default). An event carries the item's key, its kind, and the project and task it is in, never the request's own words: a
// request's text can hold a command, a path or a prompt, and what leaves the window must not. Free and local: a read of the
// inbox after the events that can change it, nothing sent anywhere.

const (
	settingNotify = "notify.prefs"
	// EventNeedsYouAdded is published once per item that came into the Inbox (NeedsYouNote).
	EventNeedsYouAdded = "needsyou.added"
	// EventNotifyChanged is published when the person changes the switch.
	EventNotifyChanged = "notify.changed"
	// notifySettle is how long the watcher lets a burst of events settle before it reads the Inbox again.
	notifySettle = 250 * time.Millisecond
)

// NotifyPrefs is the person's choice of being told outside the window.
type NotifyPrefs struct {
	// NeedsYou: tell me when something needs me (the taskbar button asks for attention while the window is not in front).
	NeedsYou bool `json:"needsYou"`
}

// NeedsYouNote is what a needsyou.added event says of a new Inbox item: what kind of request it is and where, never its text.
type NeedsYouNote struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	ProjectID   string `json:"projectId,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	TaskKey     string `json:"taskKey,omitempty"` // GOC-41
	// Title and Body are the words a notification shows: the kind of request, then the project and the task.
	Title string `json:"title"`
	Body  string `json:"body"`
}

var needsKindWords = map[room.NeedsKind]string{
	room.NeedsApproval:       "An approval is waiting",
	room.NeedsReview:         "A review is waiting",
	room.NeedsQuestion:       "An agent asks a question",
	room.NeedsFailedCheck:    "A check failed",
	room.NeedsWatchdogPause:  "A task was paused",
	room.NeedsChatPermission: "A chat asks to use a tool",
	room.NeedsBudget:         "A budget needs you",
	room.NeedsTaskWaiting:    "A task is waiting for you",
	room.NeedsMention:        "You were mentioned",
	room.NeedsJoinRequest:    "An agent asks to join",
	room.NeedsAgent:          "An agent needs attention",
	room.NeedsConnection:     "An agent CLI is not ready",
	room.NeedsPRReview:       "Changes were asked on GitHub",
}

// needsNote is the note of one item: its kind in words, then the project's name and the task's key.
func needsNote(it room.NeedsYouItem, projectName, projectKey string) NeedsYouNote {
	n := NeedsYouNote{Key: it.Key, Kind: string(it.Kind), ProjectID: it.ProjectID, ProjectName: projectName}
	if it.TaskSeq > 0 && projectKey != "" {
		n.TaskKey = projectKey + "-" + strconv.Itoa(it.TaskSeq)
	}
	n.Title = needsKindWords[it.Kind]
	if n.Title == "" {
		n.Title = "Something needs you"
	}
	where := []string{}
	for _, s := range []string{projectName, n.TaskKey} {
		if s != "" {
			where = append(where, s)
		}
	}
	n.Body = strings.Join(where, " · ")
	if n.Body == "" {
		n.Body = "Open Drydock's Inbox"
	}
	return n
}

// NotifyPrefs is the person's switch (on when it was never set, or what is kept does not read).
func (c *Core) NotifyPrefs(ctx context.Context) NotifyPrefs {
	out := NotifyPrefs{NeedsYou: true}
	var v string
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingNotify).Scan(&v); err != nil {
		return out
	}
	var kept NotifyPrefs
	if json.Unmarshal([]byte(v), &kept) != nil {
		return out
	}
	return kept
}

// SetNotifyPrefs keeps the person's switch and tells the other windows (notify.changed).
func (c *Core) SetNotifyPrefs(ctx context.Context, p NotifyPrefs) (NotifyPrefs, error) {
	v, err := json.Marshal(p)
	if err != nil {
		return NotifyPrefs{}, err
	}
	if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingNotify, string(v))
		return err
	}); err != nil {
		return NotifyPrefs{}, err
	}
	c.publish(ctx, EventNotifyChanged, "", map[string]any{"needsYou": p.NeedsYou, "by": c.Human()})
	return p, nil
}

// needsWatchTypes are the events after which the Inbox may have changed (the UI's needsChanged, and the agents' and runs' states
// that add stateItems). agent.output streams and never adds an item, so it is not among them.
var needsWatchTypes = []string{"approval.*", "workflow.request*", "task.*", "watchdog.*", "room.message_posted", "room.archived",
	"chat.permission*", "run.*", "agent.state*", "agent.failed", "agent.needs_attention", "agent.waiting", "agent.stopped", "agent.completed",
	"connection.*", "budget.*", "room.join_requested", "room.join_answered", "github.*", "needsyou.acked"}

// needsWatcher reads the Inbox after the events that can change it and publishes needsyou.added for each new key. The first read
// only learns what is there: what waited before Drydock started is the Inbox's count, not news.
type needsWatcher struct {
	c    *Core
	kick chan struct{}
	seen map[string]bool
}

func (w *needsWatcher) poke() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// check reads the Inbox and publishes the new items; the first call only learns them.
func (w *needsWatcher) check(ctx context.Context) {
	items, err := w.c.NeedsYou(ctx, "")
	if err != nil {
		return // the next event reads again
	}
	now := make(map[string]bool, len(items))
	var fresh []room.NeedsYouItem
	for _, it := range items {
		now[it.Key] = true
		if w.seen != nil && !w.seen[it.Key] {
			fresh = append(fresh, it)
		}
	}
	w.seen = now // an item answered and asked again is new again
	if len(fresh) == 0 {
		return
	}
	projects := map[string][2]string{}
	if ps, err := w.c.Projects(ctx); err == nil {
		for _, p := range ps {
			projects[p.ID] = [2]string{p.Name, p.Key}
		}
	}
	for _, it := range fresh {
		p := projects[it.ProjectID]
		n := needsNote(it, p[0], p[1])
		b, _ := json.Marshal(n)
		_, _ = w.c.Bus.Publish(ctx, events.Event{Type: EventNeedsYouAdded, ProjectID: it.ProjectID, TaskID: it.TaskID, Data: b})
	}
}

func (w *needsWatcher) run(ctx context.Context) {
	w.check(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.kick:
		}
		// let a burst settle: one read for the lot
		select {
		case <-ctx.Done():
			return
		case <-time.After(notifySettle):
		}
		select {
		case <-w.kick:
		default:
		}
		w.check(ctx)
	}
}

func init() {
	RegisterExtension(Extension{Name: "notify", After: []string{"chats"}, Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		bg, cancel := context.WithCancel(context.WithoutCancel(ctx))
		w := &needsWatcher{c: c, kick: make(chan struct{}, 1)}
		watched := events.Watch(bg, c.Bus, events.Filter{Types: needsWatchTypes}, func(events.Event) { w.poke() })
		var wg sync.WaitGroup
		wg.Add(1)
		safe.Go("core.notify", func() {
			defer wg.Done()
			w.run(bg)
		})
		return map[string]Method{
			"notify.get": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.NotifyPrefs(ctx), nil }),
			"notify.set": bind(func(ctx context.Context, p NotifyPrefs) (any, error) { return c.SetNotifyPrefs(ctx, p) }),
		}, func() {
			cancel()
			<-watched
			wg.Wait()
		}, nil
	}})
}
