package app

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/winident"
)

// drydock.exe's version resource and the version the app reports are one value: a build given none has Version's default, and
// the committed resource definition carries the same one (a build given one passes it to both; Taskfile.yml, ci.yml).
func TestTheResourceDefinitionCarriesTheAppsDefaultVersion(t *testing.T) {
	d, err := winident.LoadDefinition("..")
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
