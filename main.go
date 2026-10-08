//go:build windows

// Command drydock is the Windows desktop application: the Wails shell around the Go core and the embedded UI.
package main

import (
	"fmt"
	"os"

	"github.com/kjmaguire/drydock/app"
	"github.com/kjmaguire/drydock/ui"
)

func main() {
	assets, err := ui.Dist()
	if err != nil {
		fmt.Fprintln(os.Stderr, "drydock: the UI is not built (run `npm run build` in ui/):", err)
		os.Exit(1)
	}
	if err := app.Run(assets); err != nil {
		fmt.Fprintln(os.Stderr, "drydock:", err)
		os.Exit(1)
	}
}
