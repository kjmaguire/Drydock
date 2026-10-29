//go:build windows

package backup

import (
	"errors"

	"golang.org/x/sys/windows"
)

// inUse is a refusal because another process has the file open without letting it be moved.
var inUse = func(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
