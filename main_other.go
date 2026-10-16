//go:build !windows

// Command drydock is Windows-only. On other development hosts use cmd/drydock-dev (the core with the development HTTP
// transport) to work on the core and UI.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "drydock: the desktop app is Windows-only. Use `go run ./cmd/drydock-dev` for development on this host.")
	os.Exit(2)
}
