//go:build windows

package inuse

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// On Windows a file another handle holds open without FILE_SHARE_DELETE cannot be renamed; once that handle closes, the move
// goes through (the start that applies a staged restore, right after the app or an antivirus scan let go of the database).
func TestAFileIsMovedOnceAnotherHandleLetsGo(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "drydock.db"), filepath.Join(dir, "replaced.db")
	if err := os.WriteFile(from, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := windows.UTF16PtrFromString(from)
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err == nil || !Is(err) {
		windows.CloseHandle(h)
		t.Fatalf("a held file was renamed, or the refusal is not read as in use: %v", err)
	}
	time.AfterFunc(300*time.Millisecond, func() { windows.CloseHandle(h) })
	if err := Do(func() error { return os.Rename(from, to) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(to); err != nil {
		t.Fatal(err)
	}
}
