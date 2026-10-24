package winident_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"

	"github.com/kjmaguire/drydock/internal/winident"
	"github.com/kjmaguire/drydock/internal/winident/winidenttest"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		p := filepath.Dir(d)
		if p == d {
			t.Fatal("no go.mod above the test")
		}
		d = p
	}
}

func definition(t *testing.T) *winident.Definition {
	t.Helper()
	d, err := winident.LoadDefinition(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The committed definition is what Drydock's identity needs: the icon file has every size (256 as PNG, the others as 32-bit
// BMP), the manifest has every setting, and the version resource names Drydock and carries its own default version.
func TestTheCommittedDefinitionIsComplete(t *testing.T) {
	d := definition(t)
	if filepath.Base(d.IconPath) != "icon.ico" || filepath.Base(d.ManifestPath) != "drydock.exe.manifest" {
		t.Errorf("winres.json names %s and %s", d.IconPath, d.ManifestPath)
	}
	entries, err := winident.ParseICO(readFile(t, d.IconPath))
	if err != nil {
		t.Fatal(err)
	}
	if p := winident.CheckIconEntries(entries); len(p) > 0 {
		t.Errorf("icon.ico: %v", p)
	}
	if p := winident.CheckManifest(readFile(t, d.ManifestPath)); len(p) > 0 {
		t.Errorf("drydock.exe.manifest: %v", p)
	}
	if d.DefaultVersion() == "" {
		t.Fatal("winres.json has no ProductVersion")
	}
	if p := winident.CheckVersionInfo(&d.Version, d.DefaultVersion()); len(p) > 0 {
		t.Errorf("winres.json RT_VERSION: %v", p)
	}
}

// The icon is drawn from Drydock's mark by build/windows/make-icon.mjs: the script makes the sizes the check wants, from the
// small-size sources that are committed, and the 1024 pixel master is there.
func TestTheIconScriptMakesTheSizesTheCheckWants(t *testing.T) {
	root := repoRoot(t)
	script := string(readFile(t, filepath.Join(root, "build", "windows", "make-icon.mjs")))
	m := regexp.MustCompile(`export const SIZES = \[([0-9, ]+)\]`).FindStringSubmatch(script)
	if m == nil {
		t.Fatal("make-icon.mjs has no SIZES")
	}
	var sizes []int
	for _, s := range strings.Split(m[1], ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, n)
	}
	if !slices.Equal(sizes, winident.IconSizes) {
		t.Errorf("make-icon.mjs makes %v, the check wants %v", sizes, winident.IconSizes)
	}
	for _, src := range regexp.MustCompile(`join\(here, '([\w.-]+\.svg)'\)`).FindAllStringSubmatch(script, -1) {
		if b := readFile(t, filepath.Join(root, "build", "windows", src[1])); !bytes.Contains(b, []byte("<svg")) {
			t.Errorf("%s is not an SVG", src[1])
		}
	}
	if !strings.Contains(script, "'ui', 'public', 'favicon.svg'") {
		t.Error("make-icon.mjs does not draw from ui/public/favicon.svg, Drydock's mark")
	}
	png := readFile(t, filepath.Join(root, "build", "windows", "icon-1024.png"))
	if !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) || binary.BigEndian.Uint32(png[16:]) != 1024 || binary.BigEndian.Uint32(png[20:]) != 1024 {
		t.Error("icon-1024.png is not a 1024 x 1024 PNG")
	}
}

// A real Windows executable linked with the definition's resources (as a .syso, the way drydock.exe is) passes the check, and
// it reads the build's version; one without resources, or with another version, fails with the reasons.
func TestCheckEXEReadsAnExecutableLinkedWithTheResources(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a Windows executable")
	}
	d := definition(t)
	rs, err := d.Resources("1.4.2-beta.3")
	if err != nil {
		t.Fatal(err)
	}
	exe := winidenttest.BuildEXE(t, rs)
	r := checkFile(t, exe, "1.4.2-beta.3")
	if len(r.Problems) > 0 {
		t.Fatalf("problems: %v", r.Problems)
	}
	if r.IconGroup != "3" || len(r.Icon) != len(winident.IconSizes) || !r.Manifest {
		t.Errorf("report: icon group %s, %d images, manifest %v", r.IconGroup, len(r.Icon), r.Manifest)
	}
	if r.Fixed != [4]uint16{1, 4, 2, 0} || r.Strings[version.ProductVersion] != "1.4.2-beta.3" || r.Strings[version.CompanyName] == "" {
		t.Errorf("version: %v %v", r.Fixed, r.Strings)
	}
	// the definition itself is unchanged by a build's version
	if d.DefaultVersion() == "1.4.2-beta.3" {
		t.Error("Resources changed the definition's version")
	}

	r = checkFile(t, exe, "9.9.9")
	if !anyContains(r.Problems, "FileVersion") || !anyContains(r.Problems, "numbers") {
		t.Errorf("another version passed: %v", r.Problems)
	}

	bare := checkFile(t, winidenttest.BuildEXE(t, nil), "1.4.2-beta.3")
	for _, want := range []string{"no icon", "no version resource", "no application manifest"} {
		if !anyContains(bare.Problems, want) {
			t.Errorf("an executable without resources: no %q in %v", want, bare.Problems)
		}
	}
}

// An executable whose first icon group is another one (a named group sorts before every numbered one) fails: Explorer would
// show that one, and Wails would load ID 3.
func TestCheckEXEWantsIconThreeFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a Windows executable")
	}
	d := definition(t)
	rs, err := d.Resources("")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(d.IconPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	icon, err := winres.LoadICO(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.SetIcon(winres.Name("APP"), icon); err != nil {
		t.Fatal(err)
	}
	r := checkFile(t, winidenttest.BuildEXE(t, rs), d.DefaultVersion())
	if !anyContains(r.Problems, "first icon group is APP") {
		t.Errorf("problems: %v", r.Problems)
	}
}

func TestCheckManifestNamesEachMissingSetting(t *testing.T) {
	good := readFile(t, definition(t).ManifestPath)
	cases := map[string]struct{ from, to, want string }{
		"administrator":  {`level="asInvoker"`, `level="requireAdministrator"`, "asInvoker"},
		"no Windows 10":  {winident.Win10SupportedOS, "{35138b9a-5d96-4fbd-8e2d-a2440225f93a}", "Windows 10"},
		"system DPI":     {"PerMonitorV2, PerMonitor", "System", "PerMonitorV2"},
		"no long paths":  {`WindowsSettings">true</longPathAware>`, `WindowsSettings">false</longPathAware>`, "longPathAware"},
		"ANSI code page": {">UTF-8</activeCodePage>", ">Legacy</activeCodePage>", "activeCodePage"},
		"controls v5":    {`version="6.0.0.0"`, `version="5.0.0.0"`, "Common Controls"},
		"not XML":        {"</assembly>", "", "not XML"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			bad := bytes.Replace(good, []byte(c.from), []byte(c.to), 1)
			if bytes.Equal(bad, good) {
				t.Fatalf("%q is not in the manifest", c.from)
			}
			if p := winident.CheckManifest(bad); !anyContains(p, c.want) {
				t.Errorf("problems %v name no %q", p, c.want)
			}
		})
	}
}

func TestCheckIconEntriesNamesMissingSizesAndFormats(t *testing.T) {
	var entries []winident.IconEntry
	for _, s := range winident.IconSizes {
		entries = append(entries, winident.IconEntry{Size: s, BitCount: 32, PNG: s == 256})
	}
	if p := winident.CheckIconEntries(entries); len(p) > 0 {
		t.Fatalf("a complete icon: %v", p)
	}
	if p := winident.CheckIconEntries(entries[1:]); !anyContains(p, "sizes") {
		t.Errorf("no 16 pixel image: %v", p)
	}
	broken := slices.Clone(entries)
	broken[0].BitCount = 8
	broken[len(broken)-1].PNG = false
	broken[1].PNG = true
	p := winident.CheckIconEntries(broken)
	for _, want := range []string{"8-bit", "256 pixel image is not PNG", "20 pixel image is PNG"} {
		if !anyContains(p, want) {
			t.Errorf("no %q in %v", want, p)
		}
	}
	if _, err := winident.ParseICO([]byte("not an icon")); err == nil {
		t.Error("ParseICO read text as an icon")
	}
}

func TestNumericVersionIsWhatWindowsShows(t *testing.T) {
	for in, want := range map[string][4]uint16{"0.0.0-dev": {}, "1.2.3": {1, 2, 3, 0}, "1.5.0-beta.2": {1, 5, 0, 0}, "v2.10.4.7": {2, 10, 4, 7}} {
		if got := winident.NumericVersion(in); got != want {
			t.Errorf("%s: %v, want %v", in, got, want)
		}
	}
}

// The Taskfile and CI make the resources with the same go-winres command, so both builds embed the same thing, and both read
// the result back from the built exe.
func TestTheTaskfileAndCIMakeTheSameResources(t *testing.T) {
	root := repoRoot(t)
	ci := string(readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml")))
	task := string(readFile(t, filepath.Join(root, "Taskfile.yml")))
	cmd := regexp.MustCompile(`go run github\.com/tc-hib/go-winres@v[0-9.]+ make --in build/windows/winres\.json --arch amd64 --out rsrc`)
	inCI, inTask := cmd.FindString(ci), cmd.FindString(task)
	if inCI == "" || inCI != inTask {
		t.Errorf("the go-winres commands differ:\n  ci.yml:      %q\n  Taskfile.yml: %q", inCI, inTask)
	}
	if !strings.Contains(ci, "go run ./cmd/drydock-winident -exe drydock.exe") || !strings.Contains(task, "go run ./cmd/drydock-winident -exe bin/drydock.exe") {
		t.Error("a build does not check the exe it built with cmd/drydock-winident")
	}
	// a version given to the Taskfile reaches the resources and the linker alike
	for _, want := range []string{"--product-version {{.VERSION}} --file-version {{.VERSION}}", "-X github.com/kjmaguire/drydock/app.Version={{.VERSION}}", "-version {{.VERSION}}"} {
		if !strings.Contains(task, want) {
			t.Errorf("Taskfile.yml does not pass the version: no %q", want)
		}
	}
	// the generated object file is never committed
	if !strings.Contains(string(readFile(t, filepath.Join(root, ".gitignore"))), "/rsrc_windows_*.syso") {
		t.Error(".gitignore does not leave out the generated rsrc_windows_*.syso")
	}
}

func checkFile(t *testing.T, path, want string) *winident.Report {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := winident.CheckEXE(f, want)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func anyContains(list []string, sub string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.Contains(s, sub) })
}
