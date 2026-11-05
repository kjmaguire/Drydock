package pipeline

import (
	"strings"
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

// C7 review: a grace longer than the longest wait would make "no CI" read as "still running"; Validate refuses it (after the
// defaults: 120 minutes with the running-checks wait empty is 120 > 60) and ciWindow clamps what was saved before.
func TestCIGraceNeverOutlastsTheLongestWait(t *testing.T) {
	for _, s := range []DevSettings{{CIGraceMinutes: 120}, {CIGraceMinutes: 30, CIMaxWaitMinutes: 20}, {CIMaxWaitMinutes: 3}} {
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "must not be longer") {
			t.Errorf("%+v: %v", s, err)
		}
	}
	for _, s := range []DevSettings{{CIGraceMinutes: 60}, {CIGraceMinutes: 20, CIMaxWaitMinutes: 20}, {CIMaxWaitMinutes: 5}} {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	w := ciWindow(PRConfig{}, func() DevSettings { return DevSettings{CIGraceMinutes: 120} })
	if w.Grace != 60*time.Minute || w.MaxWait != 60*time.Minute {
		t.Fatalf("clamped %+v", w)
	}
}
