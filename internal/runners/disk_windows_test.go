//go:build windows

package runners

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

// The Dev Drive query runs without administrator rights against a real volume: C: on GitHub's Windows runner (and on any PC)
// is NTFS, never a Dev Drive. On a Windows that knows the two bits the query answers with them clear; on one that does not
// (an older build, a server SKU that rejects the DEV_VOLUME mask) it answers with an error and the fsutil fallback runs. Either
// way: no panic, and C: is not a Dev Drive.
func TestVolumeStateQueryOnTheSystemDrive(t *testing.T) {
	flags, err := volumeStateFlags("C:")
	switch {
	case err == nil:
		if _, ok := devDriveFromFlags("C:", flags); ok {
			t.Fatalf("C: (flags %#x) is called a Dev Drive", flags)
		}
		t.Logf("FSCTL_QUERY_PERSISTENT_VOLUME_STATE answered: flags %#x", flags)
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER), errors.Is(err, windows.ERROR_NOT_SUPPORTED), errors.Is(err, windows.ERROR_INVALID_FUNCTION):
		t.Logf("this Windows does not answer the Dev Drive query (%v): the fsutil fallback is used", err)
	default:
		// an unexpected error is still not a panic and still not a Dev Drive; say what it was
		t.Logf("the Dev Drive query failed with %v: the fsutil fallback is used", err)
	}
	c := checkVolume(context.Background(), "C:")
	if c.IsDev {
		t.Fatalf("C: is called a Dev Drive: %+v", c)
	}
	if c.QueryErr != nil && c.Fsutil == "" {
		t.Fatalf("the fallback gave no answer: %+v", c)
	}
	for _, d := range DevDrives(context.Background()).Drives {
		if d.Letter == "C:" {
			t.Fatal("C: listed as a Dev Drive")
		}
	}
}
