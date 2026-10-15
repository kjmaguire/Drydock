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

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/uiassets"
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

// Run opens the core and the window and blocks until the app exits. assets is the built UI (ui/dist).
func Run(assets fs.FS) error {
	// Agent CLIs and everything they spawn die with the app, even if it crashes (decision D7).
	if err := proc.EnterAppJob(); err != nil {
		return fmt.Errorf("app job object: %w", err)
	}
	dir, err := DataDir()
	if err != nil {
		return err
	}
	core.AppVersion = Version // the build's version for support bundles and project exports
	var wa *application.App
	var window *application.WebviewWindow
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

	bridge := &Bridge{Core: c}
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
