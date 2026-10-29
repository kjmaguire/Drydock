package backup

import (
	"errors"
	"testing"
)

// A move refused because the file is in use is tried again until it goes through; any other refusal is reported at once.
func TestAMoveRefusedWhileTheFileIsInUseIsTriedAgain(t *testing.T) {
	busy := errors.New("in use by another process")
	other := errors.New("access is denied")
	oldRename, oldInUse := renameFile, inUse
	t.Cleanup(func() { renameFile, inUse = oldRename, oldInUse })
	inUse = func(err error) bool { return errors.Is(err, busy) }

	tries := 0
	renameFile = func(_, _ string) error { // TEST DOUBLE: a file that is in use for the first two tries
		tries++
		if tries < 3 {
			return busy
		}
		return nil
	}
	if err := renameWhenFree("a", "b"); err != nil || tries != 3 {
		t.Fatalf("%v after %d tries", err, tries)
	}

	tries = 0
	renameFile = func(_, _ string) error { tries++; return other }
	if err := renameWhenFree("a", "b"); !errors.Is(err, other) || tries != 1 {
		t.Fatalf("%v after %d tries", err, tries)
	}
}
