//go:build !windows

package winverify

// OwnConsole is false outside Windows: a terminal stays open after a program exits.
func OwnConsole() bool { return false }
