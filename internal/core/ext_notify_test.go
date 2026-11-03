package core_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Decision G1: an item that comes into the Inbox is announced once (needsyou.added) with its kind, project and task, never the
// request's own words; what was there before is not announced; the person's switch is on by default and kept across a restart.
func TestANewInboxItemIsAnnouncedOnceWithoutItsText(t *testing.T) {
	a := open(t, "")
	var prefs core.NotifyPrefs
	a.call("notify.get", nil, &prefs)
	if !prefs.NeedsYou {
		t.Fatalf("the switch is off by default: %+v", prefs)
	}
	p, _ := a.setup("gocalc")
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "x"}, &task)

	trip := func(signal string) {
		t.Helper()
		if err := a.c.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO watchdog_trips(task_id,signal,stage,created_at) VALUES(?,?,2,?)`, task.ID, signal, storage.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := events.Emit(context.Background(), a.c.Bus, "watchdog.tripped", events.Event{ProjectID: p.ID, TaskID: task.ID}, map[string]any{"signal": signal}); err != nil {
			t.Fatal(err)
		}
	}
	trip("no_progress rm -rf /secret/path")
	a.waitFor("the new item announced", func() bool { return len(a.eventsOf(core.EventNeedsYouAdded)) == 1 })
	ev := a.eventsOf(core.EventNeedsYouAdded)[0]
	if ev["kind"] != "watchdog_pause" || ev["projectName"] != "gocalc" || ev["taskKey"] != p.Key+"-1" || ev["_task"] != task.ID {
		t.Fatalf("event %v", ev)
	}
	if ev["title"] != "A task was paused" || ev["body"] != "gocalc · "+p.Key+"-1" {
		t.Fatalf("words %q / %q", ev["title"], ev["body"])
	}
	for k, v := range ev {
		if s, ok := v.(string); ok && strings.Contains(s, "secret") {
			t.Fatalf("%s carries the request's text: %q", k, s)
		}
	}
	// another event that changes nothing announces nothing again
	if _, err := events.Emit(context.Background(), a.c.Bus, "task.updated", events.Event{ProjectID: p.ID, TaskID: task.ID}, nil); err != nil {
		t.Fatal(err)
	}
	trip("loop")
	a.waitFor("the second item announced", func() bool { return len(a.eventsOf(core.EventNeedsYouAdded)) == 2 })
	if n := len(a.eventsOf(core.EventNeedsYouAdded)); n != 2 {
		t.Fatalf("%d announcements", n)
	}

	a.call("notify.set", core.NotifyPrefs{NeedsYou: false}, &prefs)
	if prefs.NeedsYou || len(a.eventsOf(core.EventNotifyChanged)) != 1 {
		t.Fatalf("set %+v", prefs)
	}
	dir := a.dataDir()
	a.c.Close()
	r := openAt(t, dir)
	r.call("notify.get", nil, &prefs)
	if prefs.NeedsYou {
		t.Fatal("the switch did not survive a restart")
	}
	// what waited before this start is the Inbox's count, not news
	if _, err := events.Emit(context.Background(), r.c.Bus, "task.updated", events.Event{TaskID: task.ID}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond) // past the watcher's settle time: a read happened
	if n := len(r.eventsOf(core.EventNeedsYouAdded)); n != 2 {
		t.Fatalf("a restart announced the old items again: %d", n)
	}
}

// An agent's request to join a channel is published as room.join_requested: the watcher reads the Inbox after it and announces
// the item, with no other event needed (review of G1: it watched "join.*", which no event is named).
func TestAJoinRequestIsAnnounced(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	sec := a.agent(p.ID, "Sec", "security")
	if _, err := a.c.Room.CreateChannel(context.Background(), p.ID, "architecture", "", room.Human("kyle")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond) // past the watcher's settle time: what is there now is known, not news
	before := len(a.eventsOf(core.EventNeedsYouAdded))
	if _, err := a.c.TestRequestJoin(context.Background(), p.ID, sec.ID, "#architecture", "I record security decisions"); err != nil {
		t.Fatal(err)
	}
	a.waitFor("the join request announced", func() bool { return len(a.eventsOf(core.EventNeedsYouAdded)) > before })
	ev := a.eventsOf(core.EventNeedsYouAdded)[before]
	if ev["kind"] != string(room.NeedsJoinRequest) || ev["title"] != "An agent asks to join" {
		t.Fatalf("event %v", ev)
	}
	for k, v := range ev {
		if s, ok := v.(string); ok && strings.Contains(s, "security decisions") {
			t.Fatalf("%s carries the request's text: %q", k, s)
		}
	}
}

// G1's sound switch: on by default, kept by notify.set and across a restart; a choice kept before the switch existed reads as on,
// and a notify.set that leaves sound out (a window from before the switch) keeps the kept sound.
func TestTheNotificationSoundSwitchRoundTrips(t *testing.T) {
	a := open(t, "")
	var prefs core.NotifyPrefs
	a.call("notify.get", nil, &prefs)
	if !prefs.NeedsYou || !prefs.Sound {
		t.Fatalf("the defaults are %+v, want both on", prefs)
	}
	// what an earlier version kept: only the first switch
	if err := a.c.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO settings(key,value) VALUES('notify.prefs','{"needsYou":true}')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	a.call("notify.get", nil, &prefs)
	if !prefs.Sound {
		t.Fatalf("a choice kept before the sound switch reads %+v", prefs)
	}
	a.call("notify.set", map[string]any{"needsYou": true, "sound": false}, &prefs)
	if !prefs.NeedsYou || prefs.Sound {
		t.Fatalf("set %+v", prefs)
	}
	evs := a.eventsOf(core.EventNotifyChanged)
	if len(evs) != 1 || evs[0]["sound"] != false {
		t.Fatalf("notify.changed %v", evs)
	}
	a.call("notify.set", map[string]any{"needsYou": false}, &prefs)
	if prefs.NeedsYou || prefs.Sound {
		t.Fatalf("a set without sound turned it back on: %+v", prefs)
	}
	dir := a.dataDir()
	a.c.Close()
	r := openAt(t, dir)
	r.call("notify.get", nil, &prefs)
	if prefs.NeedsYou || prefs.Sound {
		t.Fatalf("after a restart %+v", prefs)
	}
}
