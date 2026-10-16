//go:build !windows

package scan

import (
	"os"
	"path/filepath"
)

// extraToolDirs are the folders `go install`, pip and Homebrew put programs in (on the development host; the product is
// Windows-only).
func extraToolDirs() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, "go", "bin"), filepath.Join(home, ".local", "bin"))
	}
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		out = append(out, gobin)
	}
	return append(out, "/usr/local/bin", "/opt/homebrew/bin")
}
