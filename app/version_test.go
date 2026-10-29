package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/kjmaguire/drydock/internal/winident"
)

// drydock.exe's version resource and the version the app reports are one value: a build given none has Version's default, and
// the committed resource definition carries the same one (a build given one passes it to both; Taskfile.yml, ci.yml).
func TestTheResourceDefinitionCarriesTheAppsDefaultVersion(t *testing.T) {
	d, err := winident.LoadDefinition("..")
	if errors.Is(err, fs.ErrNotExist) {
		if _, gerr := os.Stat(filepath.Join("..", "go.mod")); gerr != nil {
			// the Windows verification bundle runs this binary without the repository around it
			t.Skip("not in the repository: build/windows/winres.json is not here")
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := d.DefaultVersion(); got != Version {
		t.Errorf("build/windows/winres.json says %q, app.Version is %q", got, Version)
	}
	if got, want := d.Version.FileVersion, winident.NumericVersion(Version); got != want || d.Version.ProductVersion != want {
		t.Errorf("build/windows/winres.json's numbers are %v and %v, %q is %v", got, d.Version.ProductVersion, Version, want)
	}
	if p := winident.CheckVersionInfo(&d.Version, Version); len(p) > 0 {
		t.Error(p)
	}
}

// The window opens on Home: the address it starts with is the UI's Home route, not "/" (the chosen project's page).
func TestTheWindowOpensOnHome(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "ui", "app", "routes.ts"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*workspace: get\('([^']+)'\)`).FindSubmatch(b)
	if m == nil {
		t.Fatal("routes.ts has no workspace route")
	}
	if StartURL != string(m[1]) || StartURL == "/" {
		t.Fatalf("the window opens on %q; Home is %q", StartURL, m[1])
	}
}
