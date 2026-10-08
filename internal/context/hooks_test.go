package context

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHooksAddSectionsAndExtraFilesWithoutChangingTheDefaults(t *testing.T) {
	ctx := context.Background()
	e, ms := newEngine(t)
	seedMemory(t, ms)
	base, _, _ := e.Generate(ctx, "p1", nil)

	var doneCalls []bool
	e.Sections = func(_ context.Context, _, wt string) ([]Section, error) {
		return []Section{{Title: "Expert packs", Body: "Call search_pack.", Source: "pack:cf", Reason: "attached", Priority: 40}}, nil
	}
	dir := t.TempDir()
	e.Files = func(_ context.Context, _, wt string) ([]ExtraFile, error) {
		return []ExtraFile{
			{Path: ".claude/rules/b.md", Content: "---\npaths:\n  - \"a/**\"\n---\n<!-- " + GeneratedMarker + " -->\nbody b\n", Source: "skill:b", Done: func(_ context.Context, w bool) { doneCalls = append(doneCalls, w) }},
			{Path: "own/GEMINI.md", Content: "<!-- " + GeneratedMarker + " -->\nnested\n", Source: "skill:own", Done: func(_ context.Context, w bool) { doneCalls = append(doneCalls, w) }},
			{Path: "../escape.md", Content: "x", Source: "skill:bad", Done: func(_ context.Context, w bool) { doneCalls = append(doneCalls, w) }},
			{Path: ".git/hooks/pre-commit", Content: "x", Source: "skill:bad2"},
		}, nil
	}
	os.MkdirAll(filepath.Join(dir, "own"), 0o755)
	os.WriteFile(filepath.Join(dir, "own", "GEMINI.md"), []byte("the repository's own nested file\n"), 0o644)
	res, m, err := e.Write(ctx, "p1", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Extras) != 1 || res.Extras[0] != ".claude/rules/b.md" || len(res.SkippedExtras) != 3 {
		t.Fatalf("%+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "own", "GEMINI.md")); string(got) != "the repository's own nested file\n" {
		t.Fatal("a user's file must never be overwritten")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.md")); err == nil {
		t.Fatal("path traversal")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "pre-commit")); err == nil {
		t.Fatal(".git is off limits")
	}
	trues := 0
	for _, d := range doneCalls {
		if d {
			trues++
		}
	}
	if len(doneCalls) != 3 || trues != 1 {
		t.Fatalf("Done callbacks (one written, two refused or left alone): %v", doneCalls)
	}
	// extras are listed among the written files so the pipeline keeps them out of git
	found := false
	for _, w := range res.Written {
		if w == ".claude/rules/b.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Written %v", res.Written)
	}
	agents, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(agents), "## Expert packs\n\nCall search_pack.") || strings.Contains(string(base["AGENTS.md"]), "Expert packs") {
		t.Fatalf("section hook:\n%s", agents)
	}
	srcs := map[string]bool{}
	for _, it := range m.Items {
		srcs[it.Source] = it.Included
	}
	if !srcs["pack:cf"] || !srcs["skill:b"] || srcs["skill:own"] || srcs["skill:bad"] {
		t.Fatalf("manifest %v", srcs)
	}

	// rewriting with a smaller set removes a stale generated rule but keeps the user's files
	os.WriteFile(filepath.Join(dir, ".claude", "rules", "mine.md"), []byte("my own rule\n"), 0o644)
	e.Files = func(context.Context, string, string) ([]ExtraFile, error) {
		return []ExtraFile{{Path: ".claude/rules/c.md", Content: "<!-- " + GeneratedMarker + " -->\nbody c\n", Source: "skill:c"}}, nil
	}
	if _, _, err := e.Write(ctx, "p1", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "rules", "b.md")); err == nil {
		t.Fatal("a stale generated rule should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "rules", "mine.md")); err != nil {
		t.Fatal("the user's own rule must stay")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "rules", "c.md")); err != nil {
		t.Fatal("the new rule is written")
	}
}

func TestExtraFileSymlinkOnPathIsRefused(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t)
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".claude")); err != nil {
		t.Skip("symlinks unavailable")
	}
	e.Files = func(context.Context, string, string) ([]ExtraFile, error) {
		return []ExtraFile{{Path: ".claude/rules/x.md", Content: "<!-- " + GeneratedMarker + " -->\nx", Source: "skill:x"}}, nil
	}
	res, _, err := e.Write(ctx, "p1", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Extras) != 0 || len(res.SkippedExtras) != 1 {
		t.Fatalf("%+v", res)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("a generated file escaped through a symlink")
	}
}
