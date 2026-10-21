//go:build !windows

package security

// longPathName is the portable stub: off Windows there are no 8.3 aliases, so the long form is the path itself.
func longPathName(p string) (string, error) { return p, nil }
