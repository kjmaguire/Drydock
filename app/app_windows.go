//go:build windows

// Package app is the Wails v3 shell: a window around the UI, one bridge service, a single-instance lock and the app-level
// kill-on-close Job Object. It is the only package that imports Wails; everything else lives in internal/ and compiles
// without a UI. The release workflow installs and runs it on a clean Windows runner (decision P105).
package app

import (
	"context"
	"fmt"
	"github.com/kjmaguire/drydock/internal/bundle"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"golang.org/x/sys/windows"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/crashreport"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/uiassets"
	"github.com/kjmaguire/drydock/internal/updatepolicy"
)

// DataDir is %LOCALAPPDATA%\Drydock.
func DataDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		d, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		base = d
	}
	return filepath.Join(base, "Drydock"), nil
}

// dialogResult is a dialog's answer with the person's Cancel as "" (Wails reports it as the error "cancelled by user").
func dialogResult(path string, err error) (string, error) {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "cancelled") {
		return "", nil
	}
	return path, err
}

// Early is the first thing main does, before the single-instance check and before the core opens. When this process is the
// updater's helper (the same executable, started to swap the new file in once the app has closed) it does that and exits: it must
// not open the core, which would treat the app that is still shutting down as a crashed one (audit 2026-10-16, H10). A process
// started by "Restart now" waits here for the old one to close (restart_windows.go).
func Early() {
	updater.HandleHelperMode()
	waitForRestartedFrom()
}

// ReportStartupFailure tells the person why Drydock did not start. The executable is built for the Windows GUI subsystem and has no
// console, so a line printed to standard error went nowhere and a failed start looked like a double-click that did nothing (audit
// 2026-10-16, L4). The reason goes to <data>\logs\startup.log and into a message box.
func ReportStartupFailure(err error) {
	text := "Drydock could not start.\r\n\r\n" + err.Error()
	if dir, derr := DataDir(); derr == nil {
		log := filepath.Join(dir, "logs", "startup.log")
		if os.MkdirAll(filepath.Dir(log), 0o755) == nil {
			if f, ferr := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); ferr == nil {
				fmt.Fprintf(f, "%s %v\n", time.Now().UTC().Format(time.RFC3339), err)
				_ = f.Close()
				text += "\r\n\r\nThis is also written to " + log
			}
		}
	}
	fmt.Fprintln(os.Stderr, "drydock:", err)
	msg, _ := windows.UTF16PtrFromString(text)
	title, _ := windows.UTF16PtrFromString("Drydock")
	_, _ = windows.MessageBox(0, msg, title, windows.MB_OK|windows.MB_ICONERROR)
}

// Run opens the core and the window and blocks until the app exits. assets is the built UI (ui/dist).
func Run(assets fs.FS) error {
	// Agent CLIs and everything they spawn die with the app, even if it crashes (decision D7). If this process is already in a
	// job that forbids nesting, the job cannot be made; each agent still gets a job of its own, so this is not a reason not to start.
	if err := proc.EnterAppJob(); err != nil {
		fmt.Fprintf(os.Stderr, "drydock: app job object: %v (agents are still contained one by one)\n", err)
	}
	dir, err := DataDir()
	if err != nil {
		return err
	}
	core.AppVersion = Version // the build's version for support bundles and project exports
	var wa *application.App
	var window *application.WebviewWindow
	bridge := &Bridge{}
	// The application object comes before the core: its single-instance check hands a second launch over to the first and exits
	// the process inside application.New, so a second launch never opens a core on the first one's data (audit 2026-10-16, H10).
	var browserArgs []string
	if DebugPort != "" {
		// only a test build sets this (release.yml's install-test); a release build has none, so no other program can attach
		browserArgs = []string{"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=" + DebugPort}
	}
	wa = application.New(application.Options{
		Windows:     application.WindowsOptions{AdditionalBrowserArgs: browserArgs},
		Name:        "Drydock",
		Description: "A team of AI coding agents you can watch and approve.",
		Services:    []application.Service{application.NewService(bridge)},
		Assets:      application.AssetOptions{Handler: uiassets.Fallback(assets, application.AssetFileServerFS(assets))},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.drydock.app",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if window != nil {
					window.Restore()
					window.Focus()
				}
			},
		},
	})
	bridge.app = wa
	// past the single-instance check (a second launch has exited by now): what Go prints if this process dies is kept in a file
	// of its own, and the next start makes a crash report of it (internal/crashreport); a normal exit removes the empty file
	crash, err := crashreport.Arm(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drydock: crash reports: %v\n", err)
	}
	defer crash.Disarm()
	// the tools folder the download carries beside drydock.exe (decision P77): installed from there at the first start
	c, err := core.Open(context.Background(), core.Config{DataDir: dir, BundleDir: bundle.Dir(), AutoSetup: true, CrashOutput: crash.Path(), PickFile: func(_ context.Context, p core.FilePick) (string, error) {
		// the window's own Open dialog (the Security page's "Choose file..."); "" when the person cancels
		if wa == nil {
			return "", fmt.Errorf("the window is not open yet")
		}
		d := wa.Dialog.OpenFile().SetTitle(p.Title).CanChooseFiles(true).CanChooseDirectories(false)
		if p.FilterPattern != "" {
			d = d.AddFilter(p.FilterName, p.FilterPattern)
		}
		if window != nil {
			d = d.AttachToWindow(window)
		}
		return dialogResult(d.PromptForSingleSelection())
	}, PickFolder: func(_ context.Context, p core.FolderPick) (string, error) {
		// the window's own folder dialog (a new project's "Choose folder...", decision P58); "" when the person cancels
		if wa == nil {
			return "", fmt.Errorf("the window is not open yet")
		}
		d := wa.Dialog.OpenFile().SetTitle(p.Title).CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true)
		if window != nil {
			d = d.AttachToWindow(window)
		}
		return dialogResult(d.PromptForSingleSelection())
	}, OpenURL: func(_ context.Context, rawURL string) error {
		// a link in the window opens in the person's own browser (audit 2026-10-16, M23); the core has checked the address (a web
		// address or a plain mail address), because what the system opens for an address is anything it has a handler for
		if wa == nil {
			return fmt.Errorf("the window is not open yet")
		}
		return wa.Browser.OpenURL(rawURL)
	}, Restart: func(context.Context) error {
		// "Restart now" after a staged restore (decision P116)
		if wa == nil {
			return fmt.Errorf("the window is not open yet")
		}
		return restart(wa)
	}, WebView2Version: func() string {
		// for the support bundle: the WebView2 runtime the window runs on
		if wa == nil {
			return ""
		}
		v, _ := wa.Env.Info().PlatformInfo["WebView2"].(string)
		return v
	}})
	if err != nil {
		return fmt.Errorf("open Drydock: %w", err)
	}
	defer c.Close()
	bridge.setCore(c)
	// an update that was staged before this start proves itself by this version running for a while: its pre-update backup is
	// then released from the prune exemption (audit 2026-10-16, M13)
	confirm := time.AfterFunc(updateHealthyAfter, func() {
		if _, err := updatepolicy.ConfirmHealthy(context.Background(), c.DB, Version, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "drydock: updates: %v\n", err)
		}
	})
	defer confirm.Stop()
	if err := bridge.configureUpdater(wa, "stable", false); err != nil {
		fmt.Fprintf(os.Stderr, "drydock: updates: %v\n", err)
	}
	window = wa.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "Drydock",
		Width:     1360,
		Height:    860,
		MinWidth:  1000,
		MinHeight: 600,
		URL:       StartURL,
	})
	// the title bar's and the taskbar's icons at the window's DPI, from the executable's icon group (icon_windows.go)
	attachWindowIcons(window)
	return wa.Run()
}
