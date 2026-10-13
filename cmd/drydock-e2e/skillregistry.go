package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/skills"
)

// skillRegistryFixture is a PROTOCOL FIXTURE (test double) for GitHub as the skill registry sync sees it (decision P48):
// GET /repos/<owner>/<name>/commits/HEAD answers the default branch's commit and GET /<owner>/<name>/tar.gz/<sha> the
// tarball at that commit, as api.github.com and codeload.github.com do. Every curated registry resolves, so the scheduled sync
// records no failure in the browser tests; cloudflare/skills holds three stand-in skills written for the tests (not Cloudflare's
// text), the others are empty. The real GitHub is never contacted by the harness.
type skillRegistryFixture struct {
	srv   *http.Server
	url   string
	mu    sync.Mutex
	trees map[string]map[string]string // "owner/name" -> path -> content
}

const skillFixtureSha = "e2e0000000000000000000000000000000000e2e"

func e2eSkill(name, desc, globs, body string) string {
	meta := ""
	if globs != "" {
		meta = "metadata:\n  drydock.globs: \"" + globs + "\"\n"
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n%s---\n%s\n", name, desc, meta, body)
}

func startSkillRegistryFixture() (*skillRegistryFixture, error) {
	regs, err := skills.CuratedRegistries()
	if err != nil {
		return nil, err
	}
	f := &skillRegistryFixture{trees: map[string]map[string]string{}}
	for _, r := range regs {
		f.trees[r.Repo] = map[string]string{"README.md": "# " + r.Repo + " (test double)\n"}
	}
	f.trees["cloudflare/skills"] = map[string]string{
		"README.md":                              "# cloudflare/skills (test double)\n",
		"skills/wrangler/SKILL.md":               e2eSkill("wrangler", "Stand-in Wrangler rules for the browser tests.", "wrangler.*", "Use the project's Wrangler version; never guess flags."),
		"skills/durable-objects/SKILL.md":        e2eSkill("durable-objects", "Stand-in Durable Objects rules for the browser tests.", "src/**/*.ts", "One object per room; use alarms for timers."),
		"skills/workers-best-practices/SKILL.md": e2eSkill("workers-best-practices", "Stand-in Workers rules for the browser tests.", "", "Keep handlers small; return early."),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f.url = "http://" + ln.Addr().String()
	f.srv = &http.Server{Handler: f, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = f.srv.Serve(ln) }()
	return f, nil
}

func (f *skillRegistryFixture) Close() { _ = f.srv.Close() }

func (f *skillRegistryFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case len(parts) == 5 && parts[0] == "repos" && parts[3] == "commits":
		if _, ok := f.trees[parts[1]+"/"+parts[2]]; !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"sha":%q}`, skillFixtureSha)
	case len(parts) == 4 && parts[2] == "tar.gz" && parts[3] == skillFixtureSha:
		tree, ok := f.trees[parts[0]+"/"+parts[1]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		var names []string
		for n := range tree {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			_ = tw.WriteHeader(&tar.Header{Name: parts[1] + "-" + parts[3] + "/" + n, Mode: 0o644, Size: int64(len(tree[n])), Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte(tree[n]))
		}
		_ = tw.Close()
		_ = zw.Close()
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(buf.Bytes())
	default:
		http.NotFound(w, r)
	}
}

// fetcher is the skills fetcher pointed at the fixture (loopback allowed; the production fetcher allows GitHub's hosts only).
func (f *skillRegistryFixture) fetcher() *skills.GitHubFetcher {
	pf := packs.NewFetcher(packs.FetchPolicy{AllowedHosts: []string{"127.0.0.1"}, NoRobots: true, AllowLoopback: true, RPS: 1000, MaxBytes: 1 << 20, Timeout: 5 * time.Second}, nil)
	return &skills.GitHubFetcher{Fetcher: pf, APIBase: f.url, TarballBase: f.url}
}
