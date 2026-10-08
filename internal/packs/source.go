package packs

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Page is one document a source yielded, before it is fetched.
type Page struct {
	URL      string // citation URL, or local:<relative path>
	FetchURL string // what to GET (for markdown_suffix this differs from URL); empty for local files
	Format   string // markdown | html | mdx | "" (decide from content type)
	Local    string // absolute path of a local file
}

// discover expands the manifest's sources into pages. A discovery failure is returned so that a refresh never mistakes a
// partial listing for "the pages were removed". skipped counts entries ignored on purpose (other hosts, llms-full dumps...).
func (s *Store) discover(ctx context.Context, m *Manifest, f *Fetcher, packDir string) (pages []Page, skipped int, err error) {
	seen := map[string]bool{}
	add := func(p Page) bool {
		if seen[p.URL] {
			return false
		}
		if len(pages) >= m.Crawl.MaxPages {
			skipped++
			return false
		}
		seen[p.URL] = true
		pages = append(pages, p)
		return true
	}
	for _, src := range m.Sources {
		switch src.Type {
		case SourceLocalDir:
			ps, sk, e := discoverLocal(src, m, packDir)
			if e != nil {
				return nil, skipped, e
			}
			skipped += sk
			for _, p := range ps {
				add(p)
			}
		case SourceURLList:
			for _, u := range src.URLs {
				add(urlPage(u, src.PageFormat))
			}
		case SourceLlmsTxt:
			ps, sk, e := discoverLlms(ctx, src, m, f)
			if e != nil {
				return nil, skipped, e
			}
			skipped += sk
			for _, p := range ps {
				add(p)
			}
		case SourceSitemap:
			ps, sk, e := discoverSitemap(ctx, src, m, f)
			if e != nil {
				return nil, skipped, e
			}
			skipped += sk
			for _, p := range ps {
				add(p)
			}
		default:
			return nil, skipped, fmt.Errorf("source type %s is not supported", src.Type)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].URL < pages[j].URL })
	return pages, skipped, nil
}

func urlPage(raw, format string) Page {
	p := Page{URL: raw, FetchURL: raw, Format: formatOf(format)}
	if format == "markdown_suffix" {
		canon, fetch := markdownSuffix(raw)
		p.URL, p.FetchURL, p.Format = canon, fetch, "markdown"
	}
	return p
}

func formatOf(f string) string {
	if f == "markdown_suffix" {
		return "markdown"
	}
	return f
}

// markdownSuffix maps a docs page to its Markdown form: <page>/index.md (docs/expert-packs.md section 2). A link that
// already names a .md or .txt file is fetched as is and cited without a trailing /index.md.
func markdownSuffix(raw string) (canonical, fetch string) {
	u, err := url.Parse(raw)
	if err != nil {
		return raw, raw
	}
	u.Fragment, u.RawQuery = "", ""
	switch {
	case strings.HasSuffix(u.Path, "/index.md"):
		fetch = u.String()
		u.Path = strings.TrimSuffix(u.Path, "index.md")
		return u.String(), fetch
	case strings.HasSuffix(u.Path, ".md"), strings.HasSuffix(u.Path, ".txt"):
		return u.String(), u.String()
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	canonical = u.String() + "/"
	u.Path += "/index.md"
	return canonical, u.String()
}

var mdLink = regexp.MustCompile(`\[[^\]]*\]\(\s*<?([^)\s>]+)>?[^)]*\)`)

func discoverLlms(ctx context.Context, src Source, m *Manifest, f *Fetcher) ([]Page, int, error) {
	root, err := url.Parse(src.URL)
	if err != nil {
		return nil, 0, err
	}
	resp, err := f.Get(ctx, src.URL, Conditional{})
	if err != nil {
		return nil, 0, fmt.Errorf("fetch %s: %w", src.URL, err)
	}
	skipped := 0
	var pages []Page
	seen := map[string]bool{}
	var walk func(base *url.URL, body string, depth int) error
	walk = func(base *url.URL, body string, depth int) error {
		for _, mm := range mdLink.FindAllStringSubmatch(body, -1) {
			ref, err := base.Parse(mm[1])
			if err != nil {
				skipped++
				continue
			}
			ref.Fragment = ""
			if ref.Scheme != "https" && !(f.policy.AllowLoopback && ref.Scheme == "http") || !HostAllowed(m.Crawl.AllowedHosts, ref.Hostname()) {
				skipped++ // other hosts are never followed
				continue
			}
			u := ref.String()
			if seen[u] {
				continue
			}
			seen[u] = true
			low := strings.ToLower(ref.Path)
			switch {
			case strings.HasSuffix(low, "llms-full.txt"):
				skipped++ // per-product full dumps duplicate the pages and are large
			case strings.HasSuffix(low, "/llms.txt") || low == "llms.txt":
				if src.Follow == "per_product_llms_txt" && depth == 0 {
					sub, err := f.Get(ctx, u, Conditional{})
					if err != nil {
						skipped++
						continue
					}
					if err := walk(ref, string(sub.Body), depth+1); err != nil {
						return err
					}
				} else {
					skipped++
				}
			default:
				format := src.PageFormat
				if format == "" {
					format = "markdown_suffix"
				}
				if len(pages) < m.Crawl.MaxPages {
					pages = append(pages, urlPage(u, format))
				} else {
					skipped++
				}
			}
		}
		return nil
	}
	if err := walk(root, string(resp.Body), 0); err != nil {
		return nil, skipped, err
	}
	if len(pages) == 0 {
		return nil, skipped, fmt.Errorf("%s lists no pages on the allowed hosts", src.URL)
	}
	return pages, skipped, nil
}

type sitemapDoc struct {
	XMLName  xml.Name
	URLs     []struct{ Loc string `xml:"loc"` } `xml:"url"`
	Sitemaps []struct{ Loc string `xml:"loc"` } `xml:"sitemap"`
}

func discoverSitemap(ctx context.Context, src Source, m *Manifest, f *Fetcher) ([]Page, int, error) {
	var pages []Page
	skipped := 0
	seenMap := map[string]bool{}
	var load func(u string, depth int) error
	load = func(u string, depth int) error {
		if seenMap[u] || len(seenMap) > 20 {
			return nil
		}
		seenMap[u] = true
		resp, err := f.Get(ctx, u, Conditional{})
		if err != nil {
			return fmt.Errorf("fetch sitemap %s: %w", u, err)
		}
		var doc sitemapDoc
		if err := xml.Unmarshal(resp.Body, &doc); err != nil {
			return fmt.Errorf("sitemap %s: %w", u, err)
		}
		for _, sm := range doc.Sitemaps {
			if depth == 0 {
				if pu, err := url.Parse(strings.TrimSpace(sm.Loc)); err == nil && HostAllowed(m.Crawl.AllowedHosts, pu.Hostname()) {
					if err := load(pu.String(), depth+1); err != nil {
						return err
					}
					continue
				}
			}
			skipped++
		}
		for _, e := range doc.URLs {
			pu, err := url.Parse(strings.TrimSpace(e.Loc))
			if err != nil || !HostAllowed(m.Crawl.AllowedHosts, pu.Hostname()) || (pu.Scheme != "https" && !(f.policy.AllowLoopback && pu.Scheme == "http")) {
				skipped++
				continue
			}
			if src.Follow != "" && !strings.HasPrefix(pu.Path, src.Follow) {
				skipped++
				continue
			}
			if len(pages) >= m.Crawl.MaxPages {
				skipped++
				continue
			}
			format := src.PageFormat
			pages = append(pages, urlPage(pu.String(), format))
		}
		return nil
	}
	if err := load(src.URL, 0); err != nil {
		return nil, skipped, err
	}
	if len(pages) == 0 {
		return nil, skipped, fmt.Errorf("%s lists no pages under the configured filter", src.URL)
	}
	return pages, skipped, nil
}

var localExts = map[string]string{".md": "markdown", ".markdown": "markdown", ".mdx": "mdx", ".txt": "markdown", ".html": "html", ".htm": "html"}

// discoverLocal lists documents under a directory. Symbolic links are never followed (a link could point at any file the
// user can read), oversized and non-text files are skipped and counted.
func discoverLocal(src Source, m *Manifest, packDir string) ([]Page, int, error) {
	root := src.Path
	if !filepath.IsAbs(root) {
		root = filepath.Join(packDir, root)
	}
	root = filepath.Clean(root)
	st, err := os.Lstat(root)
	if err != nil {
		return nil, 0, fmt.Errorf("local_dir %s: %w", src.Path, err)
	}
	if !st.IsDir() {
		return nil, 0, fmt.Errorf("local_dir %s is not a directory", src.Path)
	}
	var pages []Page
	skipped := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			skipped++
			return nil
		}
		name := d.Name()
		if d.Type()&fs.ModeSymlink != 0 {
			skipped++
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		format, ok := localExts[strings.ToLower(filepath.Ext(name))]
		if !ok || strings.HasPrefix(name, ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > m.Crawl.MaxPageBytes {
			skipped++
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			skipped++
			return nil
		}
		pages = append(pages, Page{URL: "local:" + path.Clean(filepath.ToSlash(rel)), Local: p, Format: format})
		return nil
	})
	if err != nil {
		return nil, skipped, err
	}
	if len(pages) == 0 {
		return nil, skipped, errors.New("local_dir " + src.Path + " holds no Markdown, text or HTML documents")
	}
	return pages, skipped, nil
}

// pageTitleFromURL is the fallback title for a page that has none.
func pageTitleFromURL(u string) string {
	if strings.HasPrefix(u, "local:") {
		return path.Base(strings.TrimPrefix(u, "local:"))
	}
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	p := strings.TrimSuffix(strings.TrimSuffix(pu.Path, "/"), "/index.md")
	if p == "" {
		return pu.Host
	}
	return path.Base(p)
}

var mdxImport = regexp.MustCompile(`^\s*(import|export)\s+.*$`)
var jsxLine = regexp.MustCompile(`^\s*</?[A-Z][A-Za-z0-9.]*(\s[^>]*)?/?>\s*$`)

// StripMDX removes import/export lines and lines that only open, close or self-close a JSX component, outside code fences.
func StripMDX(s string) string {
	var out []string
	inFence := false
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") || strings.HasPrefix(strings.TrimSpace(l), "~~~") {
			inFence = !inFence
			out = append(out, l)
			continue
		}
		if !inFence && (mdxImport.MatchString(l) || jsxLine.MatchString(l)) {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
