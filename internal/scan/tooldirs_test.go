package scan

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A tool that is not on the PATH Drydock started with is found in the folders installers use (or on a PATH changed since
// Drydock started, which ExtraToolDirs reads on Windows), and the page says where it came from; a file that cannot be run
// is not a tool.
func TestAToolOffThePathIsFoundInTheInstallFolders(t *testing.T) {
	dir := t.TempDir()
	name := "drydock-fake-tool-p55"
	file := name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := ExtraToolDirs
	t.Cleanup(func() { ExtraToolDirs = old })
	ExtraToolDirs = func() []string { return []string{"", "relative/dir", dir} }

	path, source, err := FindTool(name)
	if err != nil || source != "folder" || path != filepath.Join(dir, file) {
		t.Fatalf("%q %q %v", path, source, err)
	}
	info := detectTool(context.Background(), nil, nil, Tool{}, name, []string{"--version"}, "install it", nil)
	if !info.Found || info.Source != "folder" || info.Path != filepath.Join(dir, file) || info.Chosen != "" {
		t.Fatalf("%+v", info)
	}
	if _, _, err := FindTool("drydock-no-such-tool-p55"); err == nil {
		t.Fatal("a missing tool was found")
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(dir, file), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := FindTool(name); err == nil {
			t.Fatal("a file that cannot be run was taken for the tool")
		}
	}
}

// A program a person chose is used as chosen; when it is gone the page says which file was chosen and that it is missing.
func TestAChosenProgramThatIsGoneIsReportedMissing(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "osv-scanner.exe")
	info := detectTool(context.Background(), nil, nil, Tool{Path: gone, Chosen: true}, "osv-scanner", []string{"--version"}, "install it", nil)
	if info.Found || info.Chosen != gone || info.Install == "" {
		t.Fatalf("%+v", info)
	}
}
