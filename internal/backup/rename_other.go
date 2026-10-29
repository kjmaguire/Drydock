//go:build !windows

package backup

// inUse is never true here: only Windows refuses to move a file another process has open.
var inUse = func(error) bool { return false }
