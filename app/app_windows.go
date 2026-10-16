//go:build windows

// Package app is the Wails v3 shell: a window around the UI, one bridge service, a single-instance lock and the app-level
// kill-on-close Job Object. It is the only package that imports Wails; everything else lives in internal/ and compiles
// without a UI. Nothing here has been run on Windows yet (decision P2).
package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"golang.org/x/sys/windows"

	"github.com/kjmaguire/drydock/internal/core"
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
// not open the core, which would treat the app that is still shutting down as a crashed one (audit 2026-10-16, H10).
func Early() { updater.HandleHelperMode() }

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
	wa = application.New(application.Options{
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
	c, err := core.Open(context.Background(), core.Config{DataDir: dir, PickFile: func(_ context.Context, p core.FilePick) (string, error) {
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
		URL:       "/",
	})
	return wa.Run()
}
