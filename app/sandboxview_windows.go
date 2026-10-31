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

var (
	viewersMu sync.Mutex
	viewers   int
)

// openSandboxApp starts a viewer for the forwarded port the core named. The core took the address from the engine's record of the
// forward; sandboxview.Args checks it once more.
func openSandboxApp(_ context.Context, a core.SandboxApp) error {
	args, err := sandboxview.Args(sandboxview.Request{URL: a.URL, Title: a.Title})
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	viewersMu.Lock()
	if viewers >= maxViewers {
		viewersMu.Unlock()
		return fmt.Errorf("%d windows of sandbox apps are open: close one first", maxViewers)
	}
	viewers++
	viewersMu.Unlock()
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		viewersMu.Lock()
		viewers--
		viewersMu.Unlock()
		return err
	}
	go func() {
		_ = cmd.Wait()
		viewersMu.Lock()
		viewers--
		viewersMu.Unlock()
	}()
	return nil
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
	profile, err := sandboxview.NewProfile(dir)
	if err != nil {
		ReportStartupFailure(fmt.Errorf("sandbox app window: %w", err))
		return 1, true
	}
	// WebView2's own processes are still closing when the window is: the folder is removed once they let go (15 s at most), and
	// the next start of the app removes what is left
	defer func() {
		if err := sandboxview.RemoveProfile(profile, 60, 250*time.Millisecond, nil); err != nil {
			fmt.Fprintf(os.Stderr, "drydock: sandbox app window: profile %s: %v\n", profile, err)
		}
	}()
	wa := application.New(application.Options{
		Name:        "Drydock sandbox app",
		Description: "A window for the app a worker started in a sandbox.",
		// no service, so the page has nothing of Drydock's to call; the one asset handler answers nothing
		Assets:  application.AssetOptions{Handler: http.NotFoundHandler()},
		Windows: application.WindowsOptions{WebviewUserDataPath: profile},
	})
	wa.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "sandbox-app",
		Title:  req.Title,
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
