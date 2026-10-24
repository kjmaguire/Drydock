// Package winidenttest builds small Windows executables for the tests of the Windows identity check: a real PE made by the Go
// toolchain from a two-line program, linked with the resources a test gives it as a .syso, exactly as drydock.exe is linked
// with the one go-winres makes. It runs on any development host (a cross-compile, no CGO).
package winidenttest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tc-hib/winres"
)

// BuildEXE builds a windows/amd64 executable with rs embedded (nil: no resources at all) and returns its path.
func BuildEXE(t testing.TB, rs *winres.ResourceSet) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/winidentfixture\n\ngo 1.22\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	if rs != nil {
		f, err := os.Create(filepath.Join(dir, "rsrc_windows_amd64.syso"))
		if err != nil {
			t.Fatal(err)
		}
		if err := rs.WriteObject(f, winres.ArchAMD64); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(dir, "fixture.exe")
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fixture executable: %v\n%s", err, out)
	}
	return exe
}
