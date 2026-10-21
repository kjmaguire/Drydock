//go:build !windows

package wsl

// longPath is the identity off Windows (no 8.3 short names).
func longPath(p string) string { return p }
