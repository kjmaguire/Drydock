//go:build windows

package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/sandboxview"
)

// The window for a sandbox's app (decision P134). The page is code an agent wrote, so it is shown in a second process of this
// executable: no bridge to the core, no data of Drydock's, and a WebView2 profile in a new folder that is deleted when the window
// closes, so the page's cookies, storage and cache are its own. WebView2 takes one profile folder per process, which is why this is
// a process and not a second window of the app. The viewer is a child of the app and so dies with it (the app's Job Object).

// maxViewers is how many of these windows are open at once.
const maxViewers = 4

// sandboxApps is the viewers' launcher as the core's three hooks: open (or bring to the front, effort review X314), say whether an
// address has its window, and end the window of a forward that stopped.
type sandboxApps struct {
	open  func(context.Context, core.SandboxApp) error
	shown func(url string) bool
	close func(url string) error
}

// openSandboxApp starts a viewer for the forwarded port the core named. The core took the address from the engine's record of the
// forward; the launcher (internal/sandboxview) checks it once more, counts the windows and removes each one's profile when its
// process ends. An address that has its window already brings that window to the front; Stop on the task page ends it.
func openSandboxApp(dir string) sandboxApps {
	exe, exeErr := os.Executable()
	l := &sandboxview.Launcher{
		DataDir: dir,
		Max:     maxViewers,
		Start: func(args []string) (sandboxview.Process, error) {
			if exeErr != nil {
				return nil, exeErr
			}
			cmd := exec.Command(exe, args...)
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			return cmd, nil
		},
		Front: func(p sandboxview.Process) error {
			cmd, ok := p.(*exec.Cmd)
			if !ok || cmd.Process == nil {
				return nil
			}
			bringToFront(uint32(cmd.Process.Pid))
			return nil
		},
		Stop: func(p sandboxview.Process) error {
			if cmd, ok := p.(*exec.Cmd); ok && cmd.Process != nil {
				return cmd.Process.Kill()
			}
			return nil
		},
		Logf: func(format string, a ...any) { fmt.Fprintf(os.Stderr, "drydock: "+format+"\n", a...) },
	}
	return sandboxApps{
		open: func(_ context.Context, a core.SandboxApp) error {
			return l.Open(sandboxview.Request{URL: a.URL, Title: a.Title})
		},
		shown: l.Shown,
		close: l.Close,
	}
}

var (
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procShowWindow               = user32.NewProc("ShowWindow")
	procIsIconic                 = user32.NewProc("IsIconic")
	procAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")

	// one callback for every search (Windows keeps a callback for the life of the process); the search's pid and answer go
	// through frontSearch, one search at a time
	frontMu     sync.Mutex
	frontSearch struct {
		pid  uint32
		hwnd windows.HWND
	}
	frontCallback = windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var pid uint32
		if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err == nil && pid == frontSearch.pid && windows.IsWindowVisible(hwnd) {
			frontSearch.hwnd = hwnd
			return 0 // found: stop
		}
		return 1
	})
)

const swRestore = 9

// bringToFront shows the visible top-level window of the viewer process pid in front, restored if it was minimised. Drydock is the
// foreground process (the person just clicked Open), which is what lets it hand the foreground to another of its windows.
func bringToFront(pid uint32) {
	frontMu.Lock()
	frontSearch.pid, frontSearch.hwnd = pid, 0
	_ = windows.EnumWindows(frontCallback, unsafe.Pointer(nil))
	hwnd := frontSearch.hwnd
	frontMu.Unlock()
	if hwnd == 0 {
		return
	}
	_, _, _ = procAllowSetForegroundWindow.Call(uintptr(pid))
	if r, _, _ := procIsIconic.Call(uintptr(hwnd)); r != 0 {
		_, _, _ = procShowWindow.Call(uintptr(hwnd), swRestore)
	}
	_, _, _ = procSetForegroundWindow.Call(uintptr(hwnd))
}

// MaybeRunSandboxView runs the viewer when this process was started as one (`drydock.exe sandbox-view ...`) and says so. main calls
// it before anything of the app opens: the viewer takes no single-instance lock, opens no core and serves no UI.
func MaybeRunSandboxView(args []string) (code int, ok bool) {
	req, ok, err := sandboxview.Parse(args)
	if !ok {
		return 0, false
	}
	if err != nil {
		ReportStartupFailure(fmt.Errorf("sandbox app window: %w", err))
		return 2, true
	}
	dir, err := DataDir()
	if err != nil {
		ReportStartupFailure(err)
		return 1, true
	}
	// the app that started this window made its profile folder and removes it when this process ends, which WebView2's own
	// processes can take a few seconds to allow; run by hand (a test), the window makes and removes its own
	profile := req.Profile
	if profile != "" {
		if err := sandboxview.ValidProfile(dir, profile); err != nil {
			ReportStartupFailure(fmt.Errorf("sandbox app window: %w", err))
			return 2, true
		}
	} else {
		var err error
		if profile, err = sandboxview.NewProfile(dir); err != nil {
			ReportStartupFailure(fmt.Errorf("sandbox app window: %w", err))
			return 1, true
		}
		defer func() {
			if err := sandboxview.RemoveProfile(profile, 60, 250*time.Millisecond, nil); err != nil {
				fmt.Fprintf(os.Stderr, "drydock: sandbox app window: profile %s: %v\n", profile, err)
			}
		}()
	}
	wa := application.New(application.Options{
		Name:        "Drydock sandbox app",
		Description: "A window for the app a worker started in a sandbox.",
		// no service, so the page has nothing of Drydock's to call, and the asset server refuses every request, Wails' own runtime
		// (/wails/runtime: dialogs, clipboard, opening addresses, quitting) included
		Assets:  application.AssetOptions{Handler: http.NotFoundHandler(), Middleware: sandboxview.ClosedAssets},
		Windows: application.WindowsOptions{WebviewUserDataPath: profile},
	})
	wa.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "sandbox-app",
		Title:  sandboxview.Title(req.Title),
		Width:  1100,
		Height: 760,
		URL:    req.URL,
	})
	if err := wa.Run(); err != nil {
		ReportStartupFailure(fmt.Errorf("sandbox app window: %w", err))
		return 1, true
	}
	return 0, true
}
