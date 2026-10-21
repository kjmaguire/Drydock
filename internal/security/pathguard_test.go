package security

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsSyntaxTable(t *testing.T) {
	g := Guard{Windows: true, NoFS: true}
	const root = `C:\work\repo`
	tests := []struct {
		name string
		in   string
		want string // expected cleaned result, or "" when an error is expected
		err  error
	}{
		{"relative", `src\main.go`, `C:\work\repo\src\main.go`, nil},
		{"forward slashes", `src/main.go`, `C:\work\repo\src\main.go`, nil},
		{"dot segments", `src\.\a\..\main.go`, `C:\work\repo\src\main.go`, nil},
		{"root itself", `.`, `C:\work\repo`, nil},
		{"absolute inside", `C:\work\repo\x.txt`, `C:\work\repo\x.txt`, nil},
		{"absolute inside, other case", `c:\WORK\Repo\x.txt`, `C:\work\repo\x.txt`, nil},
		{"drive case folded in compare", `C:/Work/REPO/a`, `C:\work\repo\a`, nil},
		{"dotdot escape", `..\other`, "", ErrPathEscape},
		{"deep dotdot escape", `a\..\..\..\Windows`, "", ErrPathEscape},
		{"absolute elsewhere", `C:\Windows\system32`, "", ErrPathEscape},
		{"other drive", `D:\work\repo\x`, "", ErrPathEscape},
		{"sibling prefix", `C:\work\repo2\x`, "", ErrPathEscape},
		{"drive relative", `C:foo`, "", ErrPathDriveRelative},
		{"drive relative bare", `C:`, "", ErrPathDriveRelative},
		{"root relative", `\Windows`, "", ErrPathDriveRelative},
		{"unc", `\\server\share\file`, "", ErrPathUNC},
		{"unc forward", `//server/share/file`, "", ErrPathUNC},
		{"device dot", `\\.\C:\x`, "", ErrPathDevice},
		{"device question", `\\?\C:\work\repo\x`, "", ErrPathDevice},
		{"device globalroot", `\\?\GLOBALROOT\Device\X`, "", ErrPathDevice},
		{"nt prefix", `\??\C:\x`, "", ErrPathDevice},
		{"reserved CON", `CON`, "", ErrPathReserved},
		{"reserved lower nul", `sub\nul`, "", ErrPathReserved},
		{"reserved with ext", `NUL.txt`, "", ErrPathReserved},
		{"reserved COM1", `com1`, "", ErrPathReserved},
		{"reserved LPT9", `LPT9.log`, "", ErrPathReserved},
		{"reserved aux dir", `AUX\file`, "", ErrPathReserved},
		{"reserved trailing space", "CON ", "", ErrPathTrailing},
		{"COM10 is fine", `COM10`, `C:\work\repo\COM10`, nil},
		{"CONSOLE is fine", `console.txt`, `C:\work\repo\console.txt`, nil},
		{"ads", `file.txt:stream`, "", ErrPathADS},
		{"ads default stream", `file.txt::$DATA`, "", ErrPathADS},
		{"ads on dir", `dir:x\file`, "", ErrPathADS},
		{"trailing dot", `file.`, "", ErrPathTrailing},
		{"trailing space", "file ", "", ErrPathTrailing},
		{"trailing dot dir", `dir.\file`, "", ErrPathTrailing},
		{"three dots", `...\x`, "", ErrPathTrailing},
		{"wildcard", `*.go`, "", ErrPathInvalid},
		{"question mark", `a?b`, "", ErrPathInvalid},
		{"pipe char", `a|b`, "", ErrPathInvalid},
		{"control char", "a\x01b", "", ErrPathInvalid},
		{"nul byte", "a\x00b", "", ErrPathInvalid},
		{"empty", ``, "", ErrPathEmpty},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := g.Resolve(root, tc.in)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("Resolve(%q) = %q, %v; want error %v", tc.in, got, err, tc.err)
				}
				var pe *PathError
				if !errors.As(err, &pe) {
					t.Fatalf("error %T is not a *PathError", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Resolve(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestWindowsBadRoots(t *testing.T) {
	g := Guard{Windows: true, NoFS: true}
	for _, root := range []string{``, `work\repo`, `C:work`, `\\server\share`, `\work`} {
		if _, err := g.Resolve(root, "a"); !errors.Is(err, ErrPathRoot) {
			t.Errorf("root %q: err = %v, want ErrPathRoot", root, err)
		}
	}
}

func TestWindowsShortNames(t *testing.T) {
	long := map[string]string{
		`C:\work\repo\PROGRA~1`:        `C:\work\repo\Program Files`,
		`C:\work\repo\sub\NOTES~1.TXT`: `C:\work\repo\sub\notes-long-name.txt`,
		`C:\work\repo\README~1.MD`:     `C:\work\repo\README~1.MD`, // a real file literally named so
		`C:\work\repo\sub\PROGRA~1`:    `C:\work\repo\sub\PROGRA~1`,
	}
	g := Guard{Windows: true, NoFS: true, LongPath: func(p string) (string, error) {
		for k, l := range long {
			if strings.EqualFold(k, p) {
				return l, nil
			}
		}
		if strings.Contains(p, "GONE~1") {
			return "", fs.ErrNotExist
		}
		if strings.Contains(p, "BROKEN~1") {
			return "", errors.New("access denied")
		}
		return p, nil
	}}
	const root = `C:\work\repo`
	for _, bad := range []string{`PROGRA~1`, `sub\NOTES~1.TXT`, `progra~1\x`, `BROKEN~1`} {
		if _, err := g.Resolve(root, bad); !errors.Is(err, ErrPathShortName) {
			t.Errorf("%q: err = %v, want ErrPathShortName", bad, err)
		}
	}
	for _, ok := range []string{`README~1.MD`, `sub\PROGRA~1`, `GONE~1`, `ordinary\file.txt`, `a~b.txt`} {
		if _, err := g.Resolve(root, ok); err != nil {
			t.Errorf("%q: unexpected error %v", ok, err)
		}
	}
}

func TestShortNamePattern(t *testing.T) {
	for _, s := range []string{"PROGRA~1", "PROGRA~2", "NOTES~1.TXT", "A~1", "docume~1.doc"} {
		if !shortNameRE.MatchString(s) {
			t.Errorf("%q should look like an 8.3 name", s)
		}
	}
	for _, s := range []string{"Program Files", "file.txt", "~backup", "toolongname~1x", "a~b", "verylongname~1"} {
		if shortNameRE.MatchString(s) {
			t.Errorf("%q should not look like an 8.3 name", s)
		}
	}
}

func TestPOSIXLexical(t *testing.T) {
	g := Guard{NoFS: true}
	const root = "/work/repo"
	tests := []struct {
		in, want string
		err      error
	}{
		{"a/b.go", "/work/repo/a/b.go", nil},
		{"./a//b/../c", "/work/repo/a/c", nil},
		{".", "/work/repo", nil},
		{"/work/repo/x", "/work/repo/x", nil},
		{"../etc/passwd", "", ErrPathEscape},
		{"a/../../x", "", ErrPathEscape},
		{"/etc/passwd", "", ErrPathEscape},
		{"/work/repo2/x", "", ErrPathEscape},
		{"/work/repo/../../etc", "", ErrPathEscape},
		{"/", "", ErrPathEscape},
		{"", "", ErrPathEmpty},
		{"a\x00b", "", ErrPathInvalid},
		// backslash is an ordinary character in a POSIX filename, so this stays inside
		{`a\..\..\b`, "/work/repo/a\\..\\..\\b", nil},
		// reserved names and ADS are Windows-only rules
		{"nul", "/work/repo/nul", nil},
		{"file:stream", "/work/repo/file:stream", nil},
	}
	for _, tc := range tests {
		got, err := g.Resolve(root, tc.in)
		if tc.err != nil {
			if !errors.Is(err, tc.err) {
				t.Errorf("Resolve(%q) = %q, %v; want %v", tc.in, got, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := g.Resolve("relative/root", "a"); !errors.Is(err, ErrPathRoot) {
		t.Errorf("relative root: %v", err)
	}
}

func TestReservedNameTable(t *testing.T) {
	for _, s := range []string{"con", "CON", "Prn", "aux", "NUL", "com1", "COM9", "lpt1", "LPT9", "con.txt", "nul.tar.gz", "CONIN$", "conout$", "COM¹", "com0"} {
		if !isReservedName(s) {
			t.Errorf("%q should be reserved", s)
		}
	}
	for _, s := range []string{"conf", "console", "COM10", "LPT", "COM", "auxiliary", "nulled", "xcon", "con-1"} {
		if isReservedName(s) {
			t.Errorf("%q should not be reserved", s)
		}
	}
}

// --- filesystem behavior (native OS) ---

func realRoot(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks on this host (%s): %v", runtime.GOOS, err)
	}
}

func TestFSBasics(t *testing.T) {
	root := realRoot(t)
	g := NewGuard()
	if err := os.MkdirAll(filepath.Join(root, "src", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"src/a.go", "src/pkg", "src/pkg/new/deeper/file.txt", "newfile", "."} {
		got, err := g.Resolve(root, in)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", in, err)
		}
		if !strings.HasPrefix(got, root) {
			t.Fatalf("Resolve(%q) = %q, not under %q", in, got, root)
		}
	}
	// a file ancestor of a not-yet-existing target
	if got, err := g.Resolve(root, "src/a.go/child"); err != nil {
		t.Fatalf("file-as-dir ancestor: %v", err)
	} else if !strings.HasSuffix(filepath.ToSlash(got), "src/a.go/child") {
		t.Fatalf("got %q", got)
	}
	if _, err := g.Resolve(root, ".."); !errors.Is(err, ErrPathEscape) {
		t.Fatalf(".. : %v", err)
	}
	if _, err := g.Resolve(filepath.Join(root, "missing"), "x"); !errors.Is(err, ErrPathRoot) {
		t.Fatalf("missing root: %v", err)
	}
	if _, err := g.Resolve(filepath.Join(root, "src", "a.go"), "x"); !errors.Is(err, ErrPathRoot) {
		t.Fatalf("file root: %v", err)
	}
	if !g.Inside(root, "src/a.go") || g.Inside(root, "../x") {
		t.Fatal("Inside is inconsistent with Resolve")
	}
}

func TestFSSymlinkEscapes(t *testing.T) {
	root := realRoot(t)
	outside := realRoot(t)
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, filepath.Join(root, "link"))
	mustSymlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(root, "filelink"))
	mustSymlink(t, filepath.Join(root, "real"), filepath.Join(root, "inlink"))
	mustSymlink(t, filepath.Join(outside, "does-not-exist"), filepath.Join(root, "dangling"))
	mustSymlink(t, "loop2", filepath.Join(root, "loop1"))
	mustSymlink(t, "loop1", filepath.Join(root, "loop2"))
	mustSymlink(t, "..", filepath.Join(root, "parent"))
	g := NewGuard()

	bad := []string{
		"link", "link/secret.txt", "link/newfile", "link/sub/deep/new", "filelink", "dangling", "dangling/child",
		"loop1", "parent", "parent/x", "real/../parent/x", "real/../../x",
	}
	for _, in := range bad {
		got, err := g.Resolve(root, in)
		if !errors.Is(err, ErrSymlinkEscape) && !errors.Is(err, ErrPathEscape) {
			t.Errorf("Resolve(%q) = %q, %v; want an escape error", in, got, err)
		}
	}
	// absolute path through a link
	if _, err := g.Resolve(root, filepath.Join(root, "link", "secret.txt")); !errors.Is(err, ErrSymlinkEscape) {
		t.Errorf("absolute via link: %v", err)
	}
	// a link that stays inside is fine and resolves to the real location
	got, err := g.Resolve(root, "inlink/f.txt")
	if err != nil {
		t.Fatalf("inside link: %v", err)
	}
	if want := filepath.Join(root, "real", "f.txt"); got != want {
		t.Errorf("inside link resolved to %q, want %q", got, want)
	}
}

func TestFSRootBehindSymlink(t *testing.T) {
	base := realRoot(t)
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(base, "alias")
	mustSymlink(t, real, linkRoot)
	g := NewGuard()
	got, err := g.Resolve(linkRoot, "a/b")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(real, "a", "b"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFSDirectoryReplacedBySymlink(t *testing.T) {
	// A directory that is swapped for a link between two Resolve calls is caught by the second call: the guard is
	// stateless, so callers re-resolve right before use (the residual race is the documented TOCTOU window).
	root := realRoot(t)
	outside := realRoot(t)
	if err := os.Mkdir(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := NewGuard()
	if _, err := g.Resolve(root, "d/f"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "d")); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, filepath.Join(root, "d"))
	if _, err := g.Resolve(root, "d/f"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("after swap: %v", err)
	}
}
