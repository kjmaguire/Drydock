//go:build windows

// Command drydock is the Windows desktop application: the Wails shell around the Go core and the embedded UI.
package main

import (
	"fmt"
	"os"

	"github.com/kjmaguire/drydock/app"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/ui"
)

func main() {
	// a bare program name must never be found in the working directory (audit 2026-10-16, H2)
	proc.HardenProcess()
	// the updater's helper is this same executable: it swaps the new file in and exits, before anything below opens (H10)
	app.Early()
	// an agent CLI starts `drydock.exe mcp-shim` as its MCP server (docs/mcp.md transport A); it must not open the app
	if code, ok := mcp.MaybeRunShim(os.Args); ok {
		os.Exit(code)
	}
	// the window for a sandbox's app is this same executable started with sandbox-view (decision P134): a page of its own, with no
	// core and none of the app's data, so it must not open the app either
	if code, ok := app.MaybeRunSandboxView(os.Args); ok {
		os.Exit(code)
	}
	// the executable has no console: a start that fails says why in a message box and in <data>\logs\startup.log (L4)
	defer func() {
		if v := recover(); v != nil {
			app.ReportStartupFailure(fmt.Errorf("internal error: %v", v))
			os.Exit(2)
		}
	}()
	assets, err := ui.Dist()
	if err != nil {
		app.ReportStartupFailure(fmt.Errorf("the UI is not built (run `npm run build` in ui/): %w", err))
		os.Exit(1)
	}
	if err := app.Run(assets); err != nil {
		app.ReportStartupFailure(err)
		os.Exit(1)
	}
}
