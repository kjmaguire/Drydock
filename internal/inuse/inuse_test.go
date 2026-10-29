package inuse

import (
	"errors"
	"testing"
)

// An operation refused while the file is in use is tried again until it goes through; any other refusal is reported at once.
func TestARefusalWhileTheFileIsInUseIsTriedAgain(t *testing.T) {
	busy := errors.New("in use by another process")
	other := errors.New("access is denied")
	old := is
	t.Cleanup(func() { is = old })
	is = func(err error) bool { return errors.Is(err, busy) } // TEST DOUBLE: what Windows says when the file is in use

	tries := 0
	if err := Do(func() error {
		tries++
		if tries < 3 {
			return busy
		}
		return nil
	}); err != nil || tries != 3 {
		t.Fatalf("%v after %d tries", err, tries)
	}
	tries = 0
	if err := Do(func() error { tries++; return other }); !errors.Is(err, other) || tries != 1 {
		t.Fatalf("%v after %d tries", err, tries)
	}
}
