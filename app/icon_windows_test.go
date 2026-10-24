//go:build windows

package app

import "testing"

// The system's icon sizes are read at a window's DPI: the small one is smaller than the big one, and both grow with the scale.
func TestIconSizesFollowTheDPI(t *testing.T) {
	small, big := metric(smCxSmIcon, defaultDPI), metric(smCxIcon, defaultDPI)
	if small == 0 || big == 0 || small >= big {
		t.Fatalf("at 96 DPI: small %d, big %d", small, big)
	}
	if procGetSystemMetricsForDpi.Find() == nil {
		if s2 := metric(smCxSmIcon, 2*defaultDPI); s2 <= small {
			t.Errorf("at 192 DPI the small icon is %d, at 96 DPI %d", s2, small)
		}
	}
}

// A build without the resources (this test binary, go run) has no icon 3: nothing is loaded and the window keeps Wails' icon.
func TestAnExecutableWithoutTheIconLoadsNone(t *testing.T) {
	if h := loadAppIcon(metric(smCxSmIcon, defaultDPI)); h != 0 {
		destroyIcon(h)
		t.Fatal("the test binary has an icon 3")
	}
	var ic windowIcons
	ic.apply(0) // no window: nothing happens
	if ic.small != 0 || ic.big != 0 {
		t.Error("icons were kept for no window")
	}
}
