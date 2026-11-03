package pipeline

import (
	"testing"
	"time"
)

// C7: slow CI does not page the person: the waits are 5 and 60 minutes by default, a project sets its own, and a configuration
// that names its own (tests) keeps them.
func TestCIWindowUsesTheProjectsWaits(t *testing.T) {
	w := ciWindow(PRConfig{}, DefaultDevSettings)
	if w.Poll != 15*time.Second || w.Grace != 5*time.Minute || w.MaxWait != 60*time.Minute {
		t.Fatalf("defaults %+v", w)
	}
	w = ciWindow(PRConfig{}, func() DevSettings { return DevSettings{CIGraceMinutes: 10, CIMaxWaitMinutes: 120} })
	if w.Grace != 10*time.Minute || w.MaxWait != 120*time.Minute {
		t.Fatalf("project waits %+v", w)
	}
	read := false
	w = ciWindow(PRConfig{CIGrace: time.Second, CIMaxWait: 2 * time.Second}, func() DevSettings { read = true; return DevSettings{} })
	if w.Grace != time.Second || w.MaxWait != 2*time.Second || read {
		t.Fatalf("named waits %+v (settings read: %v)", w, read)
	}
}

func TestDevSettingsRefuseCIWaitsOutOfRange(t *testing.T) {
	for _, s := range []DevSettings{{CIGraceMinutes: -1}, {CIMaxWaitMinutes: MaxCIWaitMinutes + 1}} {
		if err := s.Validate(); err == nil {
			t.Errorf("%+v was accepted", s)
		}
	}
	ok := DevSettings{CIGraceMinutes: 15, CIMaxWaitMinutes: MaxCIWaitMinutes}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}
