package repomap

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Each tag query copied from a grammar must be the one of the grammar release go.mod compiles in: an update of the grammar
// module (Dependabot proposes them) fails here until its queries/tags.scm is copied again (THIRD_PARTY_NOTICES.md, decision P62).
func TestCopiedTagQueriesMatchTheGrammarsInGoMod(t *testing.T) {
	gomod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	names, err := filepath.Glob(filepath.Join("queries", "*-tags.scm"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no copied queries: %v", err)
	}
	header := regexp.MustCompile(`Copied unchanged from (github\.com/\S+) v(\S+), queries/tags\.scm`)
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		m := header.FindSubmatch(b)
		if m == nil {
			t.Errorf("%s: no \"Copied unchanged from\" header", n)
			continue
		}
		mod, copied := string(m[1]), string(m[2])
		req := regexp.MustCompile(`(?m)^\s*(?:require\s+)?` + regexp.QuoteMeta(mod) + `\s+v(\S+)`).FindSubmatch(gomod)
		if req == nil {
			t.Errorf("%s: go.mod does not require %s", n, mod)
			continue
		}
		if string(req[1]) != copied {
			t.Errorf("%s was copied from %s v%s, but go.mod compiles v%s: copy that release's queries/tags.scm", n, mod, copied, req[1])
		}
	}
}
