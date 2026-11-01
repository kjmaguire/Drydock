package packs

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/git"
)

// git_docs sources (decision P110): a repository URL, a ref and a docs folder. The files are fetched read-only with
// internal/git (FetchDocs: shallow, no checkout, no hooks, no submodules, only the wanted blobs, a size bound) and only
// Markdown, MDX and text files under the folder are read. The address is checked the way the crawler checks it (public,
// on the allowed hosts) and pinned for git, so a name that resolves elsewhere a moment later cannot redirect the fetch. A
// refresh follows the pack's own schedule like any other source; each version records the commit it read.

var gitDocsFormats = map[string]string{".md": "markdown", ".markdown": "markdown", ".mdx": "mdx", ".txt": "markdown"}

// lookupHost resolves a git_docs host (a var so a test can point a name at its fixture server).
var lookupHost = func(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out, nil
}

func (s *Store) discoverGit(ctx context.Context, src Source, m *Manifest, packDir string) ([]Page, int, string, error) {
	u, err := url.Parse(src.URL)
	if err != nil {
		return nil, 0, "", err
	}
	host := u.Hostname()
	if !HostAllowed(m.Crawl.AllowedHosts, host) {
		return nil, 0, "", fmt.Errorf("%w: %s", ErrHostNotAllowed, host)
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	ips := []net.IP{net.ParseIP(strings.Trim(host, "[]"))}
	if ips[0] == nil {
		if ips, err = lookupHost(ctx, host); err != nil {
			return nil, 0, "", fmt.Errorf("resolve %s: %w", host, err)
		}
	}
	var pin net.IP
	var why error
	for _, ip := range ips {
		if why = CheckPublicIP(ip, s.o.AllowLoopbackFetch); why == nil {
			pin = ip
			break
		}
	}
	if pin == nil {
		if why == nil {
			why = fmt.Errorf("%w: %s has no address", ErrBlockedAddr, host)
		}
		return nil, 0, "", why
	}
	addr := pin.String()
	if pin.To4() == nil {
		addr = "[" + addr + "]"
	}
	exts := make([]string, 0, len(gitDocsFormats))
	for e := range gitDocsFormats {
		exts = append(exts, e)
	}
	res, err := git.FetchDocs(ctx, git.DocsFetch{
		URL: src.URL, Ref: src.Ref, Path: src.Path, Exts: exts, Resolve: host + ":" + port + ":" + addr,
		TempDir: filepath.Join(packDir, "tmp"), MaxRepoBytes: m.Crawl.MaxRepoBytes, MaxFileBytes: m.Crawl.MaxPageBytes, MaxFiles: m.Crawl.MaxPages,
		Timeout: m.Crawl.Budget.D(), AllowHTTP: s.o.AllowLoopbackFetch && IsLoopbackHost(pin.String()),
	})
	if err != nil {
		return nil, 0, "", fmt.Errorf("fetch %s at %s: %w", src.URL, src.Ref, err)
	}
	skipped := 0
	for _, n := range res.Skipped {
		skipped += n
	}
	pages := make([]Page, 0, len(res.Files))
	for _, f := range res.Files {
		pages = append(pages, Page{URL: gitDocsURL(src, f.Path), Format: gitDocsFormats[strings.ToLower(path.Ext(f.Path))], Data: f.Data})
	}
	if len(pages) == 0 {
		return nil, skipped, res.Commit, fmt.Errorf("%s at %s has no Markdown, MDX or text files under %q", src.URL, src.Ref, firstNonEmpty(src.Path, "/"))
	}
	return pages, skipped, res.Commit, nil
}

// gitDocsURL is the citation URL of a file: its page on GitHub for a github.com repository (at the ref, so the URL stays the
// same from one commit to the next), otherwise "git+<repository>@<ref>:<path>", which is not a web address and is shown as
// text.
func gitDocsURL(src Source, file string) string {
	u, err := url.Parse(src.URL)
	if err == nil && strings.EqualFold(u.Hostname(), "github.com") {
		repo := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
		if strings.Count(repo, "/") == 1 {
			return "https://github.com/" + repo + "/blob/" + src.Ref + "/" + file
		}
	}
	return "git+" + strings.TrimSuffix(src.URL, "/") + "@" + src.Ref + ":" + file
}
