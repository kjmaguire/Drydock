package datalock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOneOwnerAtATimeAndReleaseFreesTheFolder(t *testing.T) {
	dir := t.TempDir()
	a, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(dir)
	if !errors.Is(err, ErrHeld) || !strings.Contains(err.Error(), "process "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("a second claim on a held folder: %v", err)
	}
	a.Release()
	a.Release() // twice is fine
	b, err := Acquire(dir)
	if err != nil {
		t.Fatalf("the folder was not free after Release: %v", err)
	}
	b.Release()
	(*Lock)(nil).Release()
	// another folder is another claim
	c, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.Release()
}

// The claim is the operating system's, not a flag in the file: a process that dies without releasing it frees the folder.
func TestAProcessThatEndsFreesTheFolder(t *testing.T) {
	if os.Getenv("DATALOCK_HOLD") != "" {
		l, err := Acquire(os.Getenv("DATALOCK_HOLD"))
		if err != nil {
			os.Exit(3)
		}
		_ = l
		os.Exit(0) // ends without Release
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestAProcessThatEndsFreesTheFolder")
	cmd.Env = append(os.Environ(), "DATALOCK_HOLD="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the child could not claim the folder: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("a folder whose owner ended is still locked: %v", err)
	}
	l.Release()
}
