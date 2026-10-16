package security_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/security"
)

// The tracked files of the repository hold no secret outside the places that are meant to hold look-alikes (audit 2026-10-16,
// L52): the scan the product runs over agent output (gitleaks' default rules) is run over every tracked text file that is not a
// test, a fixture or a lock file. A real token committed to product code, a workflow or a document fails here, in the go job,
// before it is merged. The test names the file, the line and the rule, never the text.

// fixtureOrData reports whether a tracked file is where look-alike secrets are expected: tests (planted tokens prove the
// scrubbers), recorded evidence and eval fixtures, and lock files (hashes look like keys).
func fixtureOrData(p string) bool {
	switch {
	case strings.HasSuffix(p, "_test.go"), strings.HasSuffix(p, ".test.ts"), strings.HasSuffix(p, ".test.tsx"), strings.HasSuffix(p, ".test.browser.tsx"), strings.HasSuffix(p, ".spec.ts"):
		return true
	case p == "go.sum", strings.HasSuffix(p, "/go.sum"), p == "ui/package-lock.json":
		return true
	case strings.HasPrefix(p, "evidence/"), strings.HasPrefix(p, "evals/"), strings.HasPrefix(p, "testdata/"), strings.Contains(p, "/testdata/"):
		return true
	}
	return false
}

// secretsIn scans the files that are not fixtures and returns "path:line rule" for each finding.
func secretsIn(ctx context.Context, sc *security.Scanner, files map[string]string) ([]string, error) {
	var out []string
	for p, text := range files {
		if fixtureOrData(p) {
			continue
		}
		fs, err := sc.Scan(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for _, f := range fs {
			if f.Source == "gitleaks" {
				out = append(out, fmt.Sprintf("%s:%d %s", p, f.StartLine, f.RuleID))
			}
		}
	}
	return out, nil
}

func TestTheScanFindsATokenInProductCodeAndLeavesFixturesAlone(t *testing.T) {
	sc, err := security.NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	const token = "ghp_1a2B3c4D5e6F7g8H9i0J1k2L3m4N5o6P7q8R"
	got, err := secretsIn(context.Background(), sc, map[string]string{
		"internal/core/leak.go":      "package core\n\nconst key = \"" + token + "\"\n",
		"internal/core/leak_test.go": "package core\n\nconst key = \"" + token + "\"\n",
		"ui/e2e/leak.spec.ts":        "const key = '" + token + "'\n",
		"docs/notes.md":              "nothing secret here\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "internal/core/leak.go:3 ") {
		t.Fatalf("findings %v: want exactly the product file", got)
	}
	if strings.Contains(strings.Join(got, " "), token) {
		t.Fatal("a finding repeats the secret")
	}
}

// sourceRoot is the repository when the test runs from its source folder. The Windows verification bundle runs the compiled test
// from a folder of its own: there is no repository there (on CI the bundle sits inside a checkout, so `git` would answer for that
// one), and the test is skipped; CI's go job runs it from the source.
func sourceRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("not run from the source checkout: there is no repository here to scan")
	}
	return root
}

func TestNoSecretInATrackedFileThatIsNotAFixture(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := sourceRoot(t)
	listed, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	files := map[string]string{}
	for _, p := range strings.Split(string(listed), "\x00") {
		if p == "" || fixtureOrData(p) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || len(b) > 4<<20 || strings.IndexByte(string(b[:min(len(b), 1024)]), 0) >= 0 {
			continue // gone from the working tree, too large for the scanner, or binary
		}
		files[p] = string(b)
	}
	if len(files) < 100 {
		t.Fatalf("only %d tracked files were scanned: the listing is wrong", len(files))
	}
	sc, err := security.NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	found, err := secretsIn(context.Background(), sc, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("secrets in tracked files (the text is not shown): %s\nA real one must be revoked and removed; a look-alike belongs in a test or a fixture", strings.Join(found, "; "))
	}
}

// .gitignore keeps what must not be committed out, and nothing that is committed is a file it says to keep out (audit 2026-10-16,
// L56: a Playwright result was tracked for lack of an ignore rule, and no rule named an env file, a key or a database).
func TestGitignoreKeepsSecretsAndLocalStateOutAndNothingTrackedIsIgnored(t *testing.T) {
	root := sourceRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		rules[strings.TrimSpace(l)] = true
	}
	for _, want := range []string{".env", ".env.*", "!.env.example", "*.db", "*.pem", "*.key", "/test-results/", "/playwright-report/", "*.exe", "/contract-report/"} {
		if !rules[want] {
			t.Errorf(".gitignore has no rule %q", want)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "--cached", "--ignored", "--exclude-standard", "-z").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	if tracked := strings.TrimRight(string(out), "\x00"); tracked != "" {
		t.Errorf("files that are committed although .gitignore says to keep them out: %s\n(git rm --cached each one, or take its rule out if it is meant to be committed)", strings.ReplaceAll(tracked, "\x00", ", "))
	}
}
