package core

import "testing"

// UseHostPathsForWSLFiles makes the Claude Code import read a WSL distro's files at their Linux path instead of through
// \\wsl.localhost: the stand-in distro of internal/wsl/wsltest (a LABELLED TEST DOUBLE) keeps its files on the Linux host.
func UseHostPathsForWSLFiles(t testing.TB) {
	keep := wslReadPath
	wslReadPath = func(_, linux string) string { return linux }
	t.Cleanup(func() { wslReadPath = keep })
}
