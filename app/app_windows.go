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

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/proc"
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
	c, err := core.Open(context.Background(), core.Config{DataDir: dir})
	if err != nil {
		return fmt.Errorf("open Drydock: %w", err)
	}
	defer c.Close()

	bridge := &Bridge{Core: c}
	var window *application.WebviewWindow
	wa := application.New(application.Options{
		Name:        "Drydock",
		Description: "A team of AI coding agents you can watch and approve.",
		Services:    []application.Service{application.NewService(bridge)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
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
