package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/winident"
	"github.com/kjmaguire/drydock/internal/winident/winidenttest"
)

const root = "../.."

// The command passes an executable linked with the committed resources at the definition's version, and fails one without
// them, or with another version than the one it is told.
func TestRunPassesTheRealResourcesAndFailsMissingOnes(t *testing.T) {
	if testing.Short() {
		t.Skip("builds Windows executables")
	}
	d, err := winident.LoadDefinition(root)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := d.Resources("")
	if err != nil {
		t.Fatal(err)
	}
	good := winidenttest.BuildEXE(t, rs)
	if err := run(good, "", root); err != nil {
		t.Errorf("the definition's own resources: %v", err)
	}
	if err := run(good, "2.0.0", root); err == nil || !strings.Contains(err.Error(), "problems") {
		t.Errorf("another version than the build's: %v", err)
	}
	if err := run(winidenttest.BuildEXE(t, nil), "", root); err == nil {
		t.Error("an executable without resources passed")
	}
	text := filepath.Join(t.TempDir(), "x.exe")
	if err := os.WriteFile(text, []byte("not a PE file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(text, "", root); err == nil {
		t.Error("a file that is not an executable passed")
	}
}
