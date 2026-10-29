package packs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/gitfixture"
)

func gitManifest(id, repoURL, ref, path, extra string) []byte {
	return []byte(fmt.Sprintf(`schema: 1
id: %s
title: %s repo docs
kind: indexed
visibility: public
license: {content: CC-BY-4.0}
sources:
  - {type: git_docs, url: %q, ref: %s, path: %q}
crawl: {allowed_hosts: ["127.0.0.1"]}
refresh: {mode: scheduled, interval: 1d}
%s`, id, id, repoURL, ref, path, extra))
}

func TestGitDocsSourceIndexesTheDocsFolderAndRecordsTheCommit(t *testing.T) {
	srv := gitfixture.New(t, map[string]string{
		"README.md":               "# Readme\n\nNot in the pack.\n",
		"docs/workers/deploy.mdx": "import Tabs from '~/components'\n\n# Deploy\n\n<Tabs>\n\nUse wrangler versions upload to stage a version.\n\n</Tabs>\n",
		"docs/d1/limits.md":       "# D1 limits\n\nA database holds at most ten gigabytes.\n",
		"docs/diagram.svg":        "<svg/>",
	})
	e := newEnv(t, loopback)
	e.install(gitManifest("cfgit", srv.RepoURL(), "main", "docs", ""))
	r := e.refresh("cfgit")
	if r.Added != 2 || r.Skipped != 1 || r.Failed != 0 {
		t.Fatalf("%+v", r)
	}
	first := strings.TrimSpace(srv.Git("rev-parse", "HEAD"))
	if r.Sources["sources[0]"] != first {
		t.Fatalf("the commit read is recorded: %+v", r.Sources)
	}
	res := e.search("wrangler versions upload", "cfgit")
	if !res.Found {
		t.Fatalf("%+v", res)
	}
	c := res.Hits[0].Citation
	if c.URL != "git+"+srv.RepoURL()+"@main:docs/workers/deploy.mdx" || c.HeadingPath != "Deploy" || strings.Contains(res.Hits[0].Text, "import Tabs") {
		t.Fatalf("MDX is stripped and the file cited: %+v %q", c, res.Hits[0].Text)
	}
	if res := e.search("readme", "cfgit"); res.Found {
		t.Fatal("only files under the docs path are read")
	}

	// a new commit upstream: the next refresh (the pack's own schedule) brings the change and records the new commit
	second := srv.Commit(map[string]string{"docs/d1/limits.md": "# D1 limits\n\nA database holds at most fifty gigabytes now.\n", "docs/workers/deploy.mdx": ""}, "update")
	*e.now = e.now.Add(25 * time.Hour)
	due, err := e.s.Due(context.Background(), *e.now)
	if err != nil || len(due) != 1 || due[0] != "cfgit" {
		t.Fatalf("due %v %v", due, err)
	}
	ran, errs := e.s.RunDue(context.Background())
	if len(errs) != 0 || len(ran) != 1 || ran["cfgit"].PackID == "" {
		t.Fatalf("%+v %v", ran, errs)
	}
	if ran["cfgit"].Changed != 1 || ran["cfgit"].Removed != 1 || ran["cfgit"].Sources["sources[0]"] != second {
		t.Fatalf("%+v", ran["cfgit"])
	}
	vs, err := e.s.Versions(context.Background(), "cfgit")
	if err != nil || len(vs) != 2 || vs[0].Sources["sources[0]"] != second || vs[1].Sources["sources[0]"] != first {
		t.Fatalf("each version names its commit: %v %+v", err, vs)
	}
	if res := e.search("fifty gigabytes", "cfgit"); !res.Found {
		t.Fatal("the new text is indexed")
	}
}

func TestGitDocsManifestRules(t *testing.T) {
	ok := gitManifest("gd", "https://github.com/cloudflare/cloudflare-docs", "production", "src/content/docs", "")
	ok = []byte(strings.Replace(string(ok), `["127.0.0.1"]`, `["github.com"]`, 1))
	m, err := ParseManifest(ok, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Sources[0].Ref != "production" || m.Crawl.MaxRepoBytes != 256<<20 {
		t.Fatalf("%+v", m)
	}
	if got := gitDocsURL(m.Sources[0], "src/content/docs/workers/index.mdx"); got != "https://github.com/cloudflare/cloudflare-docs/blob/production/src/content/docs/workers/index.mdx" {
		t.Fatalf("a github.com file is cited by its page: %s", got)
	}
	for name, bad := range map[string]string{
		"http":          strings.Replace(string(ok), "https://github.com", "http://github.com", 1),
		"other host":    strings.Replace(string(ok), "https://github.com", "https://gitlab.com", 1),
		"option as ref": strings.Replace(string(ok), "ref: production", "ref: --upload-pack=x", 1),
		"no ref":        strings.Replace(string(ok), "ref: production", "ref: \"\"", 1),
		"escaping path": strings.Replace(string(ok), "src/content/docs", "../../etc", 1),
		"page_format":   strings.Replace(string(ok), "path: \"src/content/docs\"", "path: \"src/content/docs\", page_format: html", 1),
		"ref elsewhere": strings.Replace(string(ok), "type: git_docs, url: \"https://github.com/cloudflare/cloudflare-docs\", ref: production", "type: sitemap, url: \"https://github.com/sitemap.xml\", ref: production", 1),
		"huge repo cap": strings.Replace(string(ok), `allowed_hosts: ["github.com"]`, `allowed_hosts: ["github.com"], max_repo_bytes: 99999999999`, 1),
		"credentials":   strings.Replace(string(ok), "https://github.com", "https://x:y@github.com", 1),
	} {
		if _, err := ParseManifest([]byte(bad), ValidateOptions{}); err == nil {
			t.Fatalf("%s: accepted:\n%s", name, bad)
		}
	}
}

func TestGitDocsRefusesAPrivateAddress(t *testing.T) {
	// even with loopback allowed (tests), a name that resolves to a private address is refused before git runs
	e := newEnv(t, loopback)
	old := lookupHost
	lookupHost = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.7")}, nil }
	t.Cleanup(func() { lookupHost = old })
	m := []byte(strings.NewReplacer(`["127.0.0.1"]`, `["docs.example.com"]`).Replace(string(gitManifest("hd", "https://docs.example.com/repo.git", "main", "docs", ""))))
	e.install(m)
	if _, err := e.s.Refresh(context.Background(), human, "hd", RefreshOptions{}); err == nil || !errors.Is(err, ErrBlockedAddr) {
		t.Fatalf("a private address is refused: %v", err)
	}
}
