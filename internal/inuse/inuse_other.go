//go:build !windows

package inuse

func isInUse(error) bool { return false }
