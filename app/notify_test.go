package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

func TestTheTaskbarAsksForAttentionOnlyForANewItemWhileTheWindowIsBehind(t *testing.T) {
	data, _ := json.Marshal(core.NeedsYouNote{Key: "watchdog:1", Kind: "watchdog_pause", Title: "A task was paused"})
	added := events.Event{Type: core.EventNeedsYouAdded, Data: data}
	on, off := core.NotifyPrefs{NeedsYou: true}, core.NotifyPrefs{}
	for _, c := range []struct {
		name    string
		e       events.Event
		prefs   core.NotifyPrefs
		focused bool
		want    bool
	}{
		{"a new item, the window behind", added, on, false, true},
		{"the window in front", added, on, true, false},
		{"the switch off", added, off, false, false},
		{"another event", events.Event{Type: "task.completed", Data: data}, on, false, false},
		{"an item without a key", events.Event{Type: core.EventNeedsYouAdded, Data: []byte(`{}`)}, on, false, false},
	} {
		if got := wantsAttention(c.e, c.prefs, c.focused); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// G1's Windows notification: built from the event's words only, silent when "Play a sound" is off, and a click opens the task
// the item is in, else the Inbox.
func TestTheWindowsNotificationCarriesOnlyTheEventsWords(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"key": "approval:ap_1", "kind": "approval", "projectName": "gocalc", "taskKey": "GOC-4",
		"title": "An approval is waiting", "body": "gocalc · GOC-4",
		"request": "rm -rf C:\\secret", // a field the note does not have: never shown
	})
	e := events.Event{Type: core.EventNeedsYouAdded, TaskID: "task_0a1b2c3d4e5f", Data: data}
	n, ok := toastFor(e, core.NotifyPrefs{NeedsYou: true, Sound: true}, false)
	if !ok {
		t.Fatal("no notification for a new item while the window is behind")
	}
	want := toastNote{ID: "approval:ap_1", Title: "An approval is waiting", Body: "gocalc · GOC-4", Route: "/task/task_0a1b2c3d4e5f"}
	if n != want {
		t.Fatalf("%+v, want %+v", n, want)
	}
	if silent, _ := toastFor(e, core.NotifyPrefs{NeedsYou: true}, false); !silent.Silent {
		t.Fatal("sound off still plays a sound")
	}
	if _, ok := toastFor(e, core.NotifyPrefs{NeedsYou: true, Sound: true}, true); ok {
		t.Fatal("a notification while the window is in front")
	}
	if _, ok := toastFor(e, core.NotifyPrefs{Sound: true}, false); ok {
		t.Fatal("a notification with the switch off")
	}
	// no task: the Inbox; a task id that is not one: the Inbox
	inbox := events.Event{Type: core.EventNeedsYouAdded, Data: data}
	if n, _ := toastFor(inbox, core.NotifyPrefs{NeedsYou: true}, false); n.Route != "/needs-you" {
		t.Fatalf("route %q", n.Route)
	}
	odd := events.Event{Type: core.EventNeedsYouAdded, TaskID: "../settings", Data: data}
	if n, _ := toastFor(odd, core.NotifyPrefs{NeedsYou: true}, false); n.Route != "/needs-you" {
		t.Fatalf("route %q", n.Route)
	}
	// a note without words still says something
	bare, _ := json.Marshal(core.NeedsYouNote{Key: "x:1"})
	if n, _ := toastFor(events.Event{Type: core.EventNeedsYouAdded, Data: bare}, core.NotifyPrefs{NeedsYou: true}, false); n.Title != "Something needs you" || n.Body != "" {
		t.Fatalf("%+v", n)
	}
}

func TestAClickOnANotificationOpensOnlyTheInboxOrATask(t *testing.T) {
	for _, c := range []struct {
		data map[string]any
		want string
	}{
		{map[string]any{"route": "/task/task_0a1b"}, "/task/task_0a1b"},
		{map[string]any{"route": "/needs-you"}, "/needs-you"},
		{map[string]any{"route": "/settings"}, "/needs-you"},
		{map[string]any{"route": "/task/a/../b"}, "/needs-you"},
		{map[string]any{"route": "https://example.com"}, "/needs-you"},
		{map[string]any{"route": 7}, "/needs-you"},
		{nil, "/needs-you"},
	} {
		if got := clickRoute(c.data); got != c.want {
			t.Errorf("%v: %q, want %q", c.data, got, c.want)
		}
	}
}

// What Wails's notifications service registers per user is removed by the uninstaller: the toast identity under the app's Name,
// the activation class it names and its icon (G1, notify_windows.go).
func TestTheUninstallerRemovesTheNotificationRegistration(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "packaging", "windows", "drydock.nsi"))
	if errors.Is(err, fs.ErrNotExist) {
		if _, gerr := os.Stat(filepath.Join("..", "go.mod")); gerr != nil {
			t.Skip("not in the repository: packaging/windows/drydock.nsi is not here")
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	nsi := string(b)
	m := regexp.MustCompile(`(?m)^!define TOAST_KEY "([^"]*)"`).FindStringSubmatch(nsi)
	if m == nil || m[1] != `Software\Classes\AppUserModelId\`+AppName {
		t.Fatalf("drydock.nsi defines TOAST_KEY %v, want the app's Name %q under AppUserModelId", m, AppName)
	}
	un := nsi[strings.Index(nsi, `Section "Uninstall"`):]
	for _, want := range []string{
		`ReadRegStr $0 HKCU "${TOAST_KEY}" "CustomActivator"`,
		`DeleteRegKey HKCU "Software\Classes\CLSID\$0"`,
		`Delete "$TEMP\${APP}$0.png"`,
		`DeleteRegKey HKCU "${TOAST_KEY}"`,
	} {
		if !strings.Contains(un, want) {
			t.Errorf("the uninstaller does not %s", want)
		}
	}
}
