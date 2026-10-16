package winverify

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kjmaguire/drydock/internal/safe"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// BuildOptions configure the bundle builder (cmd/drydock-winbundle).
type BuildOptions struct {
	Root   string // the repository (default: found from the working folder)
	GOOS   string
	GOARCH string
	Out    string // the bundle folder; replaced if it holds an earlier bundle, refused if it holds anything else
	Zip    string // the zip to write (default: drydock-<goos>-verify.zip next to Out); "-" writes none
	Jobs   int    // test binaries linked at once (default 2)
	// WithGo copies the Go toolchain the bundle was built with (for the target system) into go/, so the tests that run go
	// themselves and the core smoke work on a machine without Go. It comes from the Go module proxy (checksum-verified).
	WithGo bool
	Log    io.Writer
}

// Build cross-compiles the bundle and zips it. It returns the manifest it wrote.
func Build(ctx context.Context, o BuildOptions) (*Manifest, error) {
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.Jobs <= 0 {
		o.Jobs = 2
	}
	if o.GOOS == "" || o.GOARCH == "" || o.Out == "" {
		return nil, errors.New("build: GOOS, GOARCH and the output folder are required")
	}
	root := o.Root
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		if root, err = FindRepoRoot(wd); err != nil {
			return nil, err
		}
	}
	out, err := filepath.Abs(o.Out)
	if err != nil {
		return nil, err
	}
	if err := prepareOut(out); err != nil {
		return nil, err
	}
	if err := fenceModule(out); err != nil {
		return nil, err
	}
	env := buildEnv(o.GOOS, o.GOARCH)

	fmt.Fprintf(o.Log, "listing packages for %s/%s\n", o.GOOS, o.GOARCH)
	pkgs, err := listPackages(ctx, root, env)
	if err != nil {
		return nil, err
	}
	sel, untested := selectPackages(root, pkgs)
	m := &Manifest{
		Built: time.Now().UTC().Truncate(time.Second), GOOS: o.GOOS, GOARCH: o.GOARCH,
		Verify: "drydock-verify" + exeSuffix(o.GOOS), E2E: "drydock-e2e" + exeSuffix(o.GOOS),
		Fixture: path.Join(TestsDir, "testdata", "fixtures", "gocalc"), Untested: untested,
	}
	m.Commit, m.Dirty = gitState(ctx, root)
	if v, err := goOutput(ctx, root, env, "env", "GOVERSION"); err == nil {
		m.GoVersion = strings.TrimSpace(v)
	}

	// the test binaries, each in its package's folder so relative testdata paths work
	var mu sync.Mutex
	var errs []error
	sem := make(chan struct{}, o.Jobs)
	var wg sync.WaitGroup
	for i := range sel {
		p := &sel[i]
		p.Binary = path.Join(TestsDir, p.Path, path.Base(p.Path)+".test"+exeSuffix(o.GOOS))
		wg.Add(1)
		safe.Go("winverify.Build", func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			start := time.Now()
			_, err := goOutput(ctx, root, env, "test", "-c", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", filepath.Join(out, filepath.FromSlash(p.Binary)), "./"+p.Path)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("go test -c ./%s: %w", p.Path, err))
				return
			}
			fmt.Fprintf(o.Log, "built %s (%s)\n", p.Binary, time.Since(start).Round(time.Second))
		})
	}
	wg.Wait()
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	// each package's own testdata goes next to its binary; installed dependencies (node_modules) are never copied
	noModules := func(rel string, d os.DirEntry) bool { return d.IsDir() && d.Name() == "node_modules" }
	for _, p := range sel {
		src := filepath.Join(root, filepath.FromSlash(p.Path), "testdata")
		if st, err := os.Stat(src); err == nil && st.IsDir() {
			if err := copyTree(src, filepath.Join(out, TestsDir, filepath.FromSlash(p.Path), "testdata"), noModules); err != nil {
				return nil, fmt.Errorf("copy %s/testdata: %w", p.Path, err)
			}
		}
	}
	// a test binary may read the repository's shared fixtures (../../testdata/fixtures from its package folder)
	if err := copyTree(filepath.Join(root, "testdata", "fixtures"), filepath.Join(out, TestsDir, "testdata", "fixtures"), noModules); err != nil {
		return nil, fmt.Errorf("copy testdata/fixtures: %w", err)
	}
	m.Packages = sel

	for _, c := range []struct{ name, pkg string }{{m.Verify, "./cmd/drydock-verify"}, {m.E2E, "./cmd/drydock-e2e"}} {
		if _, err := goOutput(ctx, root, env, "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", filepath.Join(out, c.name), c.pkg); err != nil {
			return nil, fmt.Errorf("go build %s: %w", c.pkg, err)
		}
		fmt.Fprintf(o.Log, "built %s\n", c.name)
	}
	if o.WithGo {
		if err := addGoToolchain(ctx, out, o.GOOS, o.GOARCH, m.GoVersion); err != nil {
			return nil, fmt.Errorf("copy the Go toolchain into the bundle (build with -with-go=false to leave it out): %w", err)
		}
		m.GoBundled = m.GoVersion
		fmt.Fprintf(o.Log, "copied %s for %s/%s into go/\n", m.GoVersion, o.GOOS, o.GOARCH)
	}
	if err := WriteManifest(out, m); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, ReadmeName), []byte(Readme(m)), 0o644); err != nil {
		return nil, err
	}
	zipPath := o.Zip
	if zipPath == "" {
		zipPath = filepath.Join(filepath.Dir(out), "drydock-"+o.GOOS+"-verify.zip")
	}
	if zipPath != "-" {
		if err := ZipDir(out, zipPath); err != nil {
			return nil, err
		}
		fmt.Fprintf(o.Log, "wrote %s\n", zipPath)
	}
	return m, nil
}

// buildEnv is the environment for cross-compiling: no CGO, so the bundle builds on a development host without a MinGW
// cross compiler (its repo map tests then check the build without the C parser; CI's go job runs them with the parser on
// Windows, decision P62), and the repository's GOFLAGS convention unless the caller set its own.
func buildEnv(goos, goarch string) []string {
	env := append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if os.Getenv("GOFLAGS") == "" {
		env = append(env, "GOFLAGS=-mod=mod")
	}
	return env
}

func goOutput(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir, cmd.Env = dir, env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// fenceModule writes a go.mod into the bundle folder. The folder usually sits inside Drydock's own module (dist-windows/)
// and can hold Go's source tree (go/src): without a module of its own, `go vet ./...` and `go test ./...` in the repository
// would take the copied standard library for packages of the module, and with -mod=mod add its imports to go.mod.
func fenceModule(out string) error {
	return os.WriteFile(filepath.Join(out, "go.mod"), []byte("// Written by drydock-winbundle: it keeps this bundle out of any Go module around it.\nmodule drydock.invalid/verify-bundle\n"), 0o644)
}

// prepareOut makes sure the output folder is empty. It refuses to empty a folder that is not an earlier bundle, so a
// mistyped -out cannot delete anything else.
func prepareOut(out string) error {
	entries, err := os.ReadDir(out)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(out, 0o755)
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := os.Stat(filepath.Join(out, ManifestName)); err != nil {
		return fmt.Errorf("%s is not empty and holds no %s: refusing to replace it (pick a new folder)", out, ManifestName)
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	return os.MkdirAll(out, 0o755)
}

// FindRepoRoot walks up from dir to the folder whose go.mod declares the Drydock module.
func FindRepoRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && moduleOf(b) == ModulePath {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no Drydock source checkout here: no go.mod declaring %s in this folder or above it", ModulePath)
		}
		dir = parent
	}
}

func moduleOf(gomod []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(gomod))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`)
		}
	}
	return ""
}

// listedPackage is the part of `go list -json` the builder reads.
type listedPackage struct {
	ImportPath     string
	Dir            string
	GoFiles        []string
	CgoFiles       []string
	TestGoFiles    []string
	XTestGoFiles   []string
	IgnoredGoFiles []string
}

func listPackages(ctx context.Context, root string, env []string) ([]listedPackage, error) {
	// -e: a package that cannot build for the target (ui embeds ui/dist, which may not be built) is still listed
	out, err := goOutput(ctx, root, env, "list", "-e", "-json", "./...")
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("go list output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

var (
	buildWindows   = regexp.MustCompile(`(?m)^//go:build\s.*\bwindows\b`)
	goosWindows    = regexp.MustCompile(`GOOS\s*[!=]=\s*"windows"`)
	realClaudeFunc = regexp.MustCompile(`(?m)^func (TestRealClaude\w*)\(`)
)

// windowsReasons says why a package counts as Windows-specific: files named *_windows*.go, build constraints that name
// windows, or code that asks runtime.GOOS about windows. Files excluded for the target system are looked at too.
func windowsReasons(p listedPackage) []string {
	var reasons []string
	seen := map[string]bool{}
	add := func(r string) {
		if !seen[r] {
			seen[r] = true
			reasons = append(reasons, r)
		}
	}
	for _, f := range allFiles(p) {
		if strings.Contains(f, "_windows") {
			add("has " + f)
			continue
		}
		b, err := os.ReadFile(filepath.Join(p.Dir, f))
		if err != nil {
			continue
		}
		if buildWindows.Match(b) {
			add("has a windows build constraint in " + f)
		} else if goosWindows.Match(b) {
			add("checks runtime.GOOS for windows in " + f)
		}
	}
	return reasons
}

func allFiles(p listedPackage) []string {
	var fs []string
	for _, l := range [][]string{p.GoFiles, p.CgoFiles, p.TestGoFiles, p.XTestGoFiles, p.IgnoredGoFiles} {
		fs = append(fs, l...)
	}
	sort.Strings(fs)
	return fs
}

// realClaudeTests returns the package's real-Claude tests that the target build compiles.
func realClaudeTests(p listedPackage, rel string) []string {
	set := map[string]bool{}
	for _, f := range append(append([]string(nil), p.TestGoFiles...), p.XTestGoFiles...) {
		b, err := os.ReadFile(filepath.Join(p.Dir, f))
		if err != nil {
			continue
		}
		for _, m := range realClaudeFunc.FindAllSubmatch(b, -1) {
			set[string(m[1])] = true
		}
		for _, name := range extraRealClaude[rel] {
			if bytes.Contains(b, []byte("func "+name+"(")) {
				set[name] = true
			}
		}
	}
	return sortedKeys(set)
}

// selectPackages picks the bundle's test packages: every package with Windows-specific code, the requested ones, and
// every package with real-Claude tests. Selected packages without tests on the target are returned as untested.
func selectPackages(root string, pkgs []listedPackage) (sel []Package, untested []string) {
	requested := map[string]bool{}
	for _, r := range Requested {
		requested[r] = true
	}
	for _, p := range pkgs {
		rel, err := filepath.Rel(root, p.Dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		var why []string
		if rs := windowsReasons(p); len(rs) > 0 {
			why = append(why, "Windows-specific code ("+joinMax(rs, 2)+")")
		}
		if requested[rel] {
			why = append(why, "asked for by name")
		}
		real := realClaudeTests(p, rel)
		if len(real) > 0 {
			why = append(why, "has real-Claude tests")
		}
		if len(why) == 0 {
			continue
		}
		if len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
			untested = append(untested, rel)
			continue
		}
		info := infoFor(rel)
		sel = append(sel, Package{Path: rel, Proves: info.Proves, Why: why, Timeout: info.Timeout.String(), NeedsGo: info.NeedsGo, RealClaude: real})
	}
	sort.Slice(sel, func(i, j int) bool { return stepOrder(sel[i].Path) < stepOrder(sel[j].Path) })
	sort.Strings(untested)
	return sel, untested
}

// stepOrder puts the quick, foundational packages first and the long end-to-end suites last, so a report read top-down
// goes from the basics to the whole loop.
func stepOrder(rel string) string {
	order := []string{"internal/proc", "internal/storage", "internal/git", "internal/providers", "internal/security", "internal/terminal",
		"internal/agents/claude", "internal/connections"}
	for i, o := range order {
		if rel == o {
			return fmt.Sprintf("0%02d %s", i, rel)
		}
	}
	switch rel {
	case "internal/pipeline":
		return "2 1"
	case "internal/core":
		return "2 2"
	}
	return "1 " + rel
}

// addGoToolchain copies the Go distribution for the target into <out>/go. It is the toolchain module the go command
// itself uses for GOTOOLCHAIN switching (golang.org/toolchain), fetched with `go mod download`, which checks it against
// the checksum database. It runs outside any module so the repository's go.mod and go.sum are not touched.
func addGoToolchain(ctx context.Context, out, goos, goarch, version string) error {
	if !strings.HasPrefix(version, "go") {
		return fmt.Errorf("unknown Go version %q", version)
	}
	tmp, err := os.MkdirTemp("", "drydock-winbundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	mod := "golang.org/toolchain@v0.0.1-" + version + "." + goos + "-" + goarch
	env := append(os.Environ(), "GO111MODULE=on", "GOFLAGS=-mod=mod")
	js, err := goOutput(ctx, tmp, env, "mod", "download", "-json", mod)
	var info struct{ Dir, Error string }
	if jerr := json.Unmarshal([]byte(js), &info); jerr != nil && err == nil {
		err = jerr
	}
	if info.Error != "" {
		err = errors.New(info.Error)
	}
	if err != nil {
		return fmt.Errorf("go mod download %s: %w", mod, err)
	}
	return copyTree(info.Dir, filepath.Join(out, "go"), nil)
}

// gitState returns the short commit and whether tracked files have uncommitted changes ("unknown" without git).
func gitState(ctx context.Context, root string) (string, bool) {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--short=12", "HEAD").Output()
	if err != nil {
		return "unknown", false
	}
	st, err := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "--untracked-files=no").Output()
	return strings.TrimSpace(string(out)), err == nil && len(bytes.TrimSpace(st)) > 0
}

// copyTree copies a folder. skip, when set, leaves out entries it returns true for.
func copyTree(src, dst string, skip func(rel string, d os.DirEntry) bool) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if skip != nil && rel != "." && skip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // links and devices are not part of a fixture
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
