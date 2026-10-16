package skills

import (
	"path"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSplitGlobsKeepsBraces(t *testing.T) {
	got := SplitGlobs("migrations/**/*.sql, wrangler.jsonc ,src/**/*.{ts,tsx}, ")
	want := []string{"migrations/**/*.sql", "wrangler.jsonc", "src/**/*.{ts,tsx}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"migrations/**/*.sql", "migrations/0001_init.sql", true},
		{"migrations/**/*.sql", "migrations/a/b/c.sql", true},
		{"migrations/**/*.sql", "migrations/a/b/c.txt", false},
		{"migrations/**/*.sql", "other/migrations/0001.sql", false},
		{"wrangler.jsonc", "wrangler.jsonc", true},
		{"wrangler.jsonc", "apps/api/wrangler.jsonc", true}, // no slash: file name at any depth
		{"/wrangler.jsonc", "apps/api/wrangler.jsonc", false},
		{"/wrangler.jsonc", "wrangler.jsonc", true},
		{"*.sql", "db/x/y.sql", true},
		{"src/*.go", "src/a.go", true},
		{"src/*.go", "src/pkg/a.go", false},
		{"src/**", "src/a/b.go", true},
		{"src/**", "src", false},
		{"src/", "src/a/b.go", true},
		{"**/*.test.ts", "a/b/c.test.ts", true},
		{"**/*.test.ts", "c.test.ts", true},
		{"src/**/*.{ts,tsx}", "src/ui/App.tsx", true},
		{"src/**/*.{ts,tsx}", "src/ui/App.js", false},
		{"src/**/{a,b}/*.go", "src/x/b/m.go", true},
		{"docs/?.md", "docs/a.md", true},
		{"docs/?.md", "docs/ab.md", false},
		{"Src/**", "src/a.go", false}, // case-sensitive
		{"src/**", `src\a\b.go`, true},
		{"src/**", "./src/a.go", true},
		{"src/**", "../src/a.go", false},
		{"src/**", "/src/a.go", false},
		{"src/**", "C:/src/a.go", false},
	}
	for _, c := range cases {
		if got := GlobMatch(c.glob, c.path); got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v, want %v", c.glob, c.path, got, c.want)
		}
	}
}

func TestValidateGlob(t *testing.T) {
	for _, g := range []string{"src/**/*.go", "*.md", "/wrangler.jsonc", "a/{b,c}/d", "docs/"} {
		if err := ValidateGlob(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
	for _, g := range []string{"", "../x", "a/../b", "//server/share", "C:/x", "a/{b", "a/b}", "a\nb", "[", "{a,b}{c,d}{e,f}{g,h}{i,j}{k,l}", string(make([]byte, 201))} {
		if err := ValidateGlob(g); err == nil {
			t.Errorf("%q should be invalid", g)
		}
	}
}

func TestAnchorDir(t *testing.T) {
	cases := map[string]string{"src/api/**": "src/api", "migrations/**/*.sql": "migrations", "docs/": "docs", "wrangler.jsonc": "", "**/*.ts": "", "src/a/file.go": "src/a", "*.sql": "", "a/{b,c}/**": ""}
	for g, want := range cases {
		got, ok := AnchorDir(g)
		if got != want || ok != (want != "") {
			t.Errorf("AnchorDir(%q) = %q,%v want %q", g, got, ok, want)
		}
	}
}

// matchSegsExhaustive is the plain recursive matcher this package used before the answers were remembered: it tries every split at
// every "**" again, and is the reference the fast one is compared with on small inputs.
func matchSegsExhaustive(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for len(pat) > 1 && pat[1] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 1 {
				return len(segs) > 0
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegsExhaustive(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if m, err := path.Match(pat[0], segs[0]); err != nil || !m {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// Many "**" in a glob do not make matching exponential (audit 2026-10-16, L34): a glob of sixteen of them against a path of forty
// directories it cannot match took longer than anyone waits before.
func TestManyDoubleStarsDoNotMakeMatchingExponential(t *testing.T) {
	glob := strings.Repeat("**/a/", 16) + "**/b"
	if err := ValidateGlob(glob); err != nil {
		t.Fatalf("the test glob must be one a skill may have: %v", err)
	}
	deep := strings.Repeat("a/", 40) + "c"
	done := make(chan bool, 1)
	go func() { done <- GlobMatch(glob, deep) }()
	select {
	case got := <-done:
		if got {
			t.Fatal("a path that does not end in b matched")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("matching a glob with sixteen ** against a deep path did not finish")
	}
	if !GlobMatch(glob, strings.Repeat("a/", 40)+"b") {
		t.Fatal("the same glob does not match a path it should")
	}
}

// The remembered answers change nothing about what matches: over every glob and path of a small alphabet, the fast matcher says
// what the exhaustive one says.
func TestTheFastGlobMatcherAgreesWithTheExhaustiveOne(t *testing.T) {
	atoms := []string{"**", "a", "b", "*", "?", "a*", "*b"}
	var pats [][]string
	var gen func(prefix []string, n int)
	gen = func(prefix []string, n int) {
		pats = append(pats, append([]string(nil), prefix...))
		if n == 0 {
			return
		}
		for _, a := range atoms {
			gen(append(prefix, a), n-1)
		}
	}
	gen(nil, 4)
	segAtoms := []string{"a", "b", "ab", "ba"}
	var paths [][]string
	var genp func(prefix []string, n int)
	genp = func(prefix []string, n int) {
		paths = append(paths, append([]string(nil), prefix...))
		if n == 0 {
			return
		}
		for _, a := range segAtoms {
			genp(append(prefix, a), n-1)
		}
	}
	genp(nil, 5)
	checked := 0
	for _, p := range pats {
		for _, s := range paths {
			if got, want := matchSegs(p, s), matchSegsExhaustive(p, s); got != want {
				t.Fatalf("matchSegs(%q, %q) = %v, the exhaustive matcher says %v", p, s, got, want)
			}
			checked++
		}
	}
	if checked < 100000 {
		t.Fatalf("only %d pairs were compared", checked)
	}
}
