//go:build windows

package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

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

// openSandboxApp starts a viewer for the forwarded port the core named. The core took the address from the engine's record of the
// forward; the launcher (internal/sandboxview) checks it once more, counts the windows and removes each one's profile when its
// process ends.
func openSandboxApp(dir string) func(context.Context, core.SandboxApp) error {
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
		Logf: func(format string, a ...any) { fmt.Fprintf(os.Stderr, "drydock: "+format+"\n", a...) },
	}
	return func(_ context.Context, a core.SandboxApp) error {
		return l.Open(sandboxview.Request{URL: a.URL, Title: a.Title})
	}
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
