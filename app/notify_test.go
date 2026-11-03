package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
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

// frontDouble is a labelled test double of the app's window: it records what windowToFront asked of it.
type frontDouble struct {
	minimised bool
	calls     []string
}

func (w *frontDouble) IsMinimised() bool { return w.minimised }
func (w *frontDouble) UnMinimise()       { w.calls = append(w.calls, "unminimise"); w.minimised = false }
func (w *frontDouble) Show()             { w.calls = append(w.calls, "show") }
func (w *frontDouble) Focus()            { w.calls = append(w.calls, "focus") }

// A click on a notification (or a second launch) brings the window in front without changing its layout: a minimised window is
// un-minimised, a maximised or fullscreen one is only shown and focused, never restored to its normal size.
func TestWindowToFrontKeepsAMaximisedWindowMaximised(t *testing.T) {
	for _, c := range []struct {
		minimised bool
		want      string
	}{
		{true, "unminimise show focus"},
		{false, "show focus"},
	} {
		w := &frontDouble{minimised: c.minimised}
		windowToFront(w)
		if got := strings.Join(w.calls, " "); got != c.want {
			t.Errorf("minimised %v: %q, want %q", c.minimised, got, c.want)
		}
	}
}

// goToastDefaultCLSID is go-toast's built-in activation class (wintoast.GUID_ImplNotificationActivationCallback, impl.go). go-toast
// v2.0.3 makes the LocalServer32 path it writes drydock.exe to from it when the package loads, before Wails gives it its own GUID,
// so the running app registers drydock.exe under this class too; the uninstaller removes it when it is still this install's.
const goToastDefaultCLSID = "{0F82E845-CB89-4039-BDBF-67CA33254C76}"

func TestTheUninstallerRemovesGoToastsBuiltInClassOnlyWhenItIsDrydocks(t *testing.T) {
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
	m := regexp.MustCompile(`(?m)^!define TOAST_DEFAULT_CLSID "([^"]*)"`).FindStringSubmatch(nsi)
	if m == nil || m[1] != goToastDefaultCLSID {
		t.Fatalf("drydock.nsi defines TOAST_DEFAULT_CLSID %v, want go-toast's %s", m, goToastDefaultCLSID)
	}
	un := nsi[strings.Index(nsi, `Section "Uninstall"`):]
	// read, compared with this install's drydock.exe, and only then deleted: the class is shared by every go-toast app
	want := regexp.MustCompile(`ReadRegStr \$0 HKCU "Software\\Classes\\CLSID\\\$\{TOAST_DEFAULT_CLSID\}\\LocalServer32" ""\s+` +
		`\$\{If\} \$0 == "\$INSTDIR\\\$\{EXE\}"\s+` +
		`DeleteRegKey HKCU "Software\\Classes\\CLSID\\\$\{TOAST_DEFAULT_CLSID\}"\s+` +
		`\$\{EndIf\}`)
	if !want.MatchString(un) {
		t.Error("the uninstaller does not delete go-toast's built-in class only when it names $INSTDIR\\${EXE}")
	}
	if strings.Count(un, "${TOAST_DEFAULT_CLSID}") != 2 {
		t.Error("the uninstaller touches go-toast's built-in class somewhere other than the checked delete")
	}

	// the GUID is go-toast's own, at the version go.mod selects (read from the module cache when it is there)
	mod, err := os.ReadFile(filepath.Join("..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	v := regexp.MustCompile(`git\.sr\.ht/~jackmordaunt/go-toast/v2 (v\S+)`).FindSubmatch(mod)
	if v == nil {
		t.Fatal("go.mod does not name go-toast")
	}
	cache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Skipf("go env GOMODCACHE: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(cache)), "git.sr.ht", "~jackmordaunt", "go-toast", "v2@"+string(v[1]), "wintoast", "impl.go"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("go-toast is not in the module cache (go mod download)")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `GUID_ImplNotificationActivationCallback = ole.NewGUID("`+goToastDefaultCLSID+`")`) {
		t.Errorf("go-toast %s's built-in activation class is not %s: update TOAST_DEFAULT_CLSID, docs/security.md and release.yml", v[1], goToastDefaultCLSID)
	}
}
