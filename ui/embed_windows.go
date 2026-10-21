//go:build windows

// Package ui embeds the built front end (ui/dist, produced by `npm run build`) for the Wails shell. Windows only, like the shell.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built UI rooted at dist/.
func Dist() (fs.FS, error) { return fs.Sub(dist, "dist") }
