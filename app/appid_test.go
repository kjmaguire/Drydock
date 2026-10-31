package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The installer writes the app's own identity on the Start menu shortcut: with two different IDs a pinned Drydock and a running
// one show as two taskbar buttons (docs/windows-identity.md section 6).
func TestTheInstallerGivesTheShortcutTheAppsOwnID(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "packaging", "windows", "drydock.nsi"))
	if errors.Is(err, fs.ErrNotExist) {
		if _, gerr := os.Stat(filepath.Join("..", "go.mod")); gerr != nil {
			// the Windows verification bundle runs this binary without the repository around it
			t.Skip("not in the repository: packaging/windows/drydock.nsi is not here")
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	nsi := string(b)
	m := regexp.MustCompile(`(?m)^!define APPID "([^"]*)"`).FindStringSubmatch(nsi)
	if m == nil || m[1] != AppUserModelID {
		t.Fatalf("drydock.nsi defines APPID %v, want %q", m, AppUserModelID)
	}
	if !strings.Contains(nsi, `!insertmacro SetShortcutAppID "$SMPROGRAMS\${APP}.lnk"`) {
		t.Fatal("drydock.nsi does not write the ID on the Start menu shortcut it creates")
	}
	// Microsoft's rules for an AppUserModelID: at most 128 characters, no spaces, dotted parts
	if len(AppUserModelID) > 128 || strings.ContainsAny(AppUserModelID, " \t") || !strings.Contains(AppUserModelID, ".") {
		t.Fatalf("%q is not a valid AppUserModelID", AppUserModelID)
	}
}
