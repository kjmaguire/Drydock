package skills

import (
	"reflect"
	"testing"
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
