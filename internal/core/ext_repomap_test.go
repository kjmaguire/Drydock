package core_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/repomap"
)

// The repo map through the core (decision P62): the page's repomap.get, and the AGENTS.md section in a repository big enough
// to need one. In a build without CGO both say there is no parser.

// bigRepo is a git repository with a store package every other package uses, enough files for the AGENTS.md section.
func bigRepo(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "shop")
	files := map[string]string{
		"store/store.go": "package store\n\n// Store keeps values.\ntype Store struct{}\n\nfunc NewStore() *Store { return &Store{} }\n\nfunc (s *Store) LookupValue(k string) string { return k }\n",
		".gitignore":     "secret/\n",
		"secret/x.go":    "package secret\n\nfunc HiddenHelper() {}\n",
		"README.md":      "# shop\n",
	}
	for i := 0; i < core.RepoMapAgentsMinFiles+5; i++ {
		files[fmt.Sprintf("handlers/h%02d/handler.go", i)] = fmt.Sprintf("package h%02d\n\nimport \"shop/store\"\n\nfunc Handle%02d() string {\n\ts := store.NewStore()\n\treturn s.LookupValue(\"k\")\n}\n", i, i)
	}
	for rel, body := range files {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := git.Init(context.Background(), repo, "main"); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestRepoMapPageAndAgentsSection(t *testing.T) {
	a := open(t, "")
	repo := bigRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "shop", "repoPath": repo}, &p)

	var v core.RepoMapView
	a.call("repomap.get", map[string]any{"projectId": p.ID, "focus": []string{"handlers/h03"}, "tokenBudget": 512}, &v)
	if !repomap.Available() {
		if v.Available || v.Result != nil || !strings.Contains(v.Message, "built without the C parser") {
			t.Fatalf("without CGO: %+v", v)
		}
		return
	}
	if !v.Available || v.Result == nil || len(v.Languages) != 8 {
		t.Fatalf("view %+v", v)
	}
	res := v.Result
	if res.Ranked[0].Path != "store/store.go" || !strings.HasPrefix(res.Map, "store/store.go:\n") || res.Tokens > 512 || res.Budget != 512 {
		t.Fatalf("map (%d tokens):\n%s\nranked %+v", res.Tokens, res.Map, res.Ranked[:2])
	}
	if strings.Contains(res.Map, "HiddenHelper") || res.Files != core.RepoMapAgentsMinFiles+6 {
		t.Fatalf("ignored files are left out: %d files\n%s", res.Files, res.Map)
	}
	if len(res.Focus) != 1 || res.Focus[0] != "handlers/h03" {
		t.Fatalf("focus %v", res.Focus)
	}
	// the cache lives in the project's data folder
	if ents, err := os.ReadDir(filepath.Join(a.dataDir(), "projects", p.ID, "repomap")); err != nil || len(ents) != 1 {
		t.Fatalf("tag cache: %v %v", ents, err)
	}
	if err := a.callErr("repomap.get", map[string]any{"projectId": p.ID, "focus": []string{"../outside"}}); err == nil {
		t.Fatal("a focus outside the repository is refused")
	}

	// AGENTS.md of a worktree carries a small map; the manifest records it with its size
	wt := t.TempDir()
	// git.Init made the first commit
	gitIn(t, repo, "worktree", "add", "-q", wt, "-b", "task-1")
	_, m, err := a.c.Context.Write(context.Background(), p.ID, wt)
	if err != nil {
		t.Fatal(err)
	}
	agents, _ := os.ReadFile(filepath.Join(wt, "AGENTS.md"))
	if !strings.Contains(string(agents), "\n## "+ctxeng.RepoMapSectionTitle+"\n") || !strings.Contains(string(agents), "│func NewStore() *Store {") {
		t.Fatalf("AGENTS.md:\n%s", agents)
	}
	var item *ctxeng.Item
	for i := range m.Items {
		if m.Items[i].Source == "repo_map" {
			item = &m.Items[i]
		}
	}
	if item == nil || !item.Included || item.Bytes == 0 || item.Bytes > core.RepoMapAgentsBudget*4+1200 {
		t.Fatalf("manifest item %+v", item)
	}

	// a small repository gets no section, and the manifest says why
	small := newRepo(t)
	var sp core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": small}, &sp)
	_, m, err = a.c.Context.Write(context.Background(), sp.ID, small)
	if err != nil {
		t.Fatal(err)
	}
	agents, _ = os.ReadFile(filepath.Join(small, "AGENTS.md"))
	if strings.Contains(string(agents), ctxeng.RepoMapSectionTitle) {
		t.Fatalf("a small repository gets no map:\n%s", agents)
	}
	for _, it := range m.Items {
		if it.Source == "repo_map" && (it.Included || !strings.Contains(it.Reason, "fewer than")) {
			t.Fatalf("manifest item %+v", it)
		}
	}
}
