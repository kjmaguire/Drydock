//go:build !windows

package backup

// isRemoteDrive is only meaningful on Windows, where a drive letter can be mapped to a network share.
func isRemoteDrive(volume string) bool { return false }
