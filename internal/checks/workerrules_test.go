package checks

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWorkerCommandsGoProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := WorkerCommands(Detect(dir))
	want := []string{"go build ./...", "go test ./...", "go vet ./..."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWorkerCommandsNpmScriptsThatExist(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"test":"vitest","build":"vite build","dev":"vite"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := WorkerCommands(Detect(dir))
	want := []string{"npm run build --silent", "npm run test --silent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWorkerCommandsLeaveOutEverythingElse(t *testing.T) {
	for _, argv := range [][]string{
		{"go", "run", "./..."},
		{"go", "test", "./...", "-exec", "x"},
		{"go", "generate", "./..."},
		{"go", "test", "./pkg"},
		{"go test", "./..."},
		{"npx", "vitest"},
		{"npm", "install"},
		{"npm", "run", "deploy", "--silent"},
		{"npm", "run", "test;rm", "--silent"},
		{"sh", "-c", "go test ./..."},
		{"git", "commit"},
		{"task", "deploy"},
		{"task", "build*"},
		nil,
	} {
		if got := WorkerCommands([]Spec{{Argv: argv}}); len(got) != 0 {
			t.Errorf("%v produced %v", argv, got)
		}
	}
}

// Every rule is one exact command line with no wildcard, and every command Detect can produce for a check has a rule.
func TestWorkerRulesAreExactAndCoverDetect(t *testing.T) {
	rules := WorkerRules()
	for _, r := range rules {
		if !strings.HasPrefix(r, "Bash(") || !strings.HasSuffix(r, ")") || strings.ContainsAny(r[5:len(r)-1], "*:;&|<>`$()") {
			t.Errorf("rule %q is not one exact command line", r)
		}
	}
	for _, c := range []string{"go build ./...", "go test ./...", "go vet ./...", "npm run typecheck --silent", "npm run lint --silent", "task test"} {
		found := false
		for _, r := range rules {
			found = found || r == "Bash("+c+")"
		}
		if !found {
			t.Errorf("no rule for %q", c)
		}
	}
}
