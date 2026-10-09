package checks

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWorkerRulesGoProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := WorkerRules(Detect(dir))
	want := []string{"Bash(go build ./...)", "Bash(go test ./...)", "Bash(go vet ./...)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWorkerRulesNpmScriptsThatExist(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"test":"vitest","build":"vite build","dev":"vite"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := WorkerRules(Detect(dir))
	want := []string{"Bash(npm run build --silent)", "Bash(npm run test --silent)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWorkerRulesRefuseEverythingElse(t *testing.T) {
	for _, argv := range [][]string{
		{"go", "run", "./..."},
		{"go", "test", "./...", "-exec", "x"},
		{"go", "generate", "./..."},
		{"go", "test", "./pkg"},
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
		if got := WorkerRules([]Spec{{Argv: argv}}); len(got) != 0 {
			t.Errorf("%v produced rules %v", argv, got)
		}
	}
}

func TestWorkerRulesNoDuplicates(t *testing.T) {
	s := Spec{Argv: []string{"go", "test", "./..."}}
	if got := WorkerRules([]Spec{s, s}); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}
