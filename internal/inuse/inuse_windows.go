//go:build windows

package inuse

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isInUse(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
