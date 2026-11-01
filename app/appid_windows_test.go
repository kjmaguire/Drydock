//go:build windows

package app

import "testing"

// The process takes Drydock's identity and Windows gives it back as set (runs on CI's Windows runner).
func TestTheProcessCarriesDrydocksAppUserModelID(t *testing.T) {
	if err := setAppUserModelID(AppUserModelID); err != nil {
		t.Fatal(err)
	}
	got, err := currentAppUserModelID()
	if err != nil {
		t.Fatal(err)
	}
	if got != AppUserModelID {
		t.Fatalf("the process carries %q, want %q", got, AppUserModelID)
	}
}
