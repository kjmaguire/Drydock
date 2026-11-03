package app

import (
	"encoding/json"
	"strings"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

// wantsAttention says whether an event should make the taskbar button ask for attention and raise a Windows notification
// (decision G1): a new Inbox item (core.EventNeedsYouAdded), the person's switch on, and the window not in front (in front, the
// window's own count and toast say it). Pure, so it is tested on any OS; notify_windows.go acts on it.
func wantsAttention(e events.Event, prefs core.NotifyPrefs, focused bool) bool {
	if e.Type != core.EventNeedsYouAdded || !prefs.NeedsYou || focused {
		return false
	}
	var n core.NeedsYouNote
	return json.Unmarshal(e.Data, &n) == nil && n.Key != ""
}

// toastNote is what one Windows notification shows and opens. It is built only from the event's own words (the kind of request,
// then the project and the task: core.needsNote), never from a request's text, a chat's title, code or a prompt.
type toastNote struct {
	// ID is the Inbox item's key: the same item is one notification.
	ID    string
	Title string
	Body  string
	// Silent: the person turned "Play a sound" off.
	Silent bool
	// Route is the page a click opens: the task's when the item names one, else the Inbox.
	Route string
}

// inboxRoute is the Inbox's page (ui/app/routes.ts, needsYou).
const inboxRoute = "/needs-you"

// toastFor is the notification for an event, or false when none is due (wantsAttention). Pure; notify_windows.go hands it to
// Wails's notifications service.
func toastFor(e events.Event, prefs core.NotifyPrefs, focused bool) (toastNote, bool) {
	if !wantsAttention(e, prefs, focused) {
		return toastNote{}, false
	}
	var n core.NeedsYouNote
	_ = json.Unmarshal(e.Data, &n) // wantsAttention read it
	t := toastNote{ID: n.Key, Title: n.Title, Body: n.Body, Silent: !prefs.Sound, Route: inboxRoute}
	if t.Title == "" {
		t.Title = "Something needs you"
	}
	if r := safeRoute("/task/" + e.TaskID); e.TaskID != "" && r != "" {
		t.Route = r
	}
	return t, true
}

// routeKey is the notification data's field that carries the route back on a click.
const routeKey = "route"

// clickRoute is the page a click on a notification opens: the route the notification carried when safeRoute allows it, else the
// Inbox (data from Windows that does not read is never a reason to open nothing).
func clickRoute(data map[string]any) string {
	if r, ok := data[routeKey].(string); ok {
		if s := safeRoute(r); s != "" {
			return s
		}
	}
	return inboxRoute
}

// safeRoute is the page a notification's click may open, or "" for anything else. The route comes back from Windows in the
// toast's arguments (any program on the PC can activate the app's notification class), so it is checked again before the window
// goes there: only the Inbox or one task's page, a task id of letters, digits, '-' and '_'.
func safeRoute(r string) string {
	if r == inboxRoute {
		return r
	}
	id, ok := strings.CutPrefix(r, "/task/")
	if !ok || id == "" || len(id) > 64 {
		return ""
	}
	for _, ch := range id {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return ""
		}
	}
	return r
}
