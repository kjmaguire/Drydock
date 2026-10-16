package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/notices"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The command rewrites the generated parts of the file from the measured module list, the UI lock file and the module cache, says
// nothing needs doing when the file is current, and with -check refuses a stale one without touching it.
func TestTheCommandRewritesAStaleFileAndCheckRefusesIt(t *testing.T) {
	repo, cache := t.TempDir(), t.TempDir()
	write(t, filepath.Join(repo, "go.mod"), "module example.com/x\n")
	write(t, filepath.Join(repo, "cmd", "drydock-notices", "keep"), "")
	write(t, filepath.Join(repo, "docs", "software-versions.json"),
		`{"items":[{"id":"go:example.com/lib","name":"example.com/lib","kind":"direct","used":"1.0.0","role":"Does the thing. More.","inBuild":true}]}`)
	write(t, filepath.Join(repo, "ui", "package-lock.json"), `{"packages":{"":{},"node_modules/pkg":{"version":"2.0.0","license":"MIT"}}}`)
	write(t, filepath.Join(repo, "ui", "package.json"), `{"dependencies":{"pkg":"2.0.0"}}`)
	write(t, filepath.Join(cache, "example.com", "lib@v1.0.0", "LICENSE"), "MIT License\n\nCopyright (c) 2026 Someone\n\nPermission is hereby granted, free of charge, to any person\n")
	doc := "# Notices\n\nhand written\n\n" + notices.GoBegin + "\n" + notices.GoEnd + "\n\n" + notices.MPLBegin + "\n" + notices.MPLEnd + "\n\n" + notices.NPMBegin + "\n" + notices.NPMEnd + "\n\nthe end\n"
	file := filepath.Join(repo, "THIRD_PARTY_NOTICES.md")
	write(t, file, doc)

	t.Setenv("GOMODCACHE", cache)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	if err := run(true); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("-check on a stale file: %v", err)
	}
	if b, _ := os.ReadFile(file); string(b) != doc {
		t.Fatal("-check must not change the file")
	}
	if err := run(false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	for _, want := range []string{"hand written", "the end", "| example.com/lib | v1.0.0 | MIT | Copyright (c) 2026 Someone | Does the thing. |", "| pkg | 2.0.0 | MIT | - |"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the rewritten file lacks %q:\n%s", want, got)
		}
	}
	if err := run(true); err != nil {
		t.Fatalf("-check on a current file: %v", err)
	}
	// a module whose license cannot be read stops the command and leaves the file alone
	write(t, filepath.Join(repo, "docs", "software-versions.json"),
		`{"items":[{"id":"go:example.com/gone","name":"example.com/gone","kind":"direct","used":"1.0.0","inBuild":true}]}`)
	before, _ := os.ReadFile(file)
	if err := run(false); err == nil || !strings.Contains(err.Error(), "example.com/gone") {
		t.Fatalf("a module with no license: %v", err)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Fatal("the file was changed although the generation failed")
	}
}

// A file whose line ends were turned into CRLF by a Windows checkout is still current when its text is.
func TestAFileWithWindowsLineEndsIsJudgedByItsText(t *testing.T) {
	repo, cache := t.TempDir(), t.TempDir()
	write(t, filepath.Join(repo, "go.mod"), "module example.com/x\n")
	write(t, filepath.Join(repo, "cmd", "drydock-notices", "keep"), "")
	write(t, filepath.Join(repo, "docs", "software-versions.json"), `{"items":[]}`)
	write(t, filepath.Join(repo, "ui", "package-lock.json"), `{"packages":{"":{}}}`)
	write(t, filepath.Join(repo, "ui", "package.json"), `{"dependencies":{}}`)
	doc := "# Notices\n\n" + notices.GoBegin + "\n" + notices.GoEnd + "\n\n" + notices.MPLBegin + "\n" + notices.MPLEnd + "\n\n" + notices.NPMBegin + "\n" + notices.NPMEnd + "\n"
	file := filepath.Join(repo, "THIRD_PARTY_NOTICES.md")
	write(t, file, doc)
	t.Setenv("GOMODCACHE", cache)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := run(false); err != nil {
		t.Fatal(err)
	}
	generated, _ := os.ReadFile(file)
	if err := os.WriteFile(file, []byte(strings.ReplaceAll(string(generated), "\n", "\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(true); err != nil {
		t.Fatalf("-check on the same text with CRLF line ends: %v", err)
	}
}
