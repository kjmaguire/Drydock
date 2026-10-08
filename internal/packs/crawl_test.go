package packs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fetcherFor(d *docSite, mod func(*FetchPolicy)) *Fetcher {
	p := FetchPolicy{AllowedHosts: []string{"127.0.0.1"}, RPS: 1000, AllowLoopback: true, Timeout: 5 * time.Second, MaxBytes: 1 << 16}
	if mod != nil {
		mod(&p)
	}
	return NewFetcher(p, nil)
}

func TestCrawlIndexesLlmsTxtPerProductAndMarkdownSuffix(t *testing.T) {
	d := newDocSite(t)
	d.set("/llms.txt", "# Fixture\n\n- [Workers](/workers/llms.txt)\n- [KV](/kv/llms.txt)\n- [Everything](/workers/llms-full.txt)\n- [Elsewhere](https://evil.example/x/)\n")
	d.set("/workers/llms.txt", "# Workers\n\n- [Get started](/workers/get-started/)\n- [Bindings](/workers/bindings/index.md)\n")
	d.set("/kv/llms.txt", "# KV\n\n- [KV intro](/kv/intro/)\n")
	d.set("/workers/get-started/index.md", "# Get started\n\nCreate a Worker with `npm create cloudflare`.\n\n## Deploy\n\nUse `wrangler versions upload` to preview.\n")
	d.set("/workers/bindings/index.md", "# Bindings\n\nBindings connect a Worker to resources such as KV.\n")
	d.set("/kv/intro/index.md", "# KV intro\n\nKV is eventually consistent.\n")
	e := newEnv(t, loopback)
	e.install(d.manifest("fx", fmt.Sprintf("  - {type: llms_txt, url: %s/llms.txt, follow: per_product_llms_txt, page_format: markdown_suffix}", d.URL), ", require_content_signal: ai-input"))
	r := e.refresh("fx")
	if r.Pages != 3 || r.Added != 3 || r.Failed != 0 || r.Skipped < 2 {
		t.Fatalf("refresh %+v", r)
	}
	res := e.search("preview wrangler versions upload", "fx")
	if !res.Found || res.Hits[0].Citation.URL != d.URL+"/workers/get-started/" {
		t.Fatalf("citation must be the canonical page URL, got %+v", res.Hits)
	}
	if len(res.Attributions) != 1 || !strings.Contains(res.Attributions[0], "CC BY 4.0") {
		t.Fatalf("attribution: %v", res.Attributions)
	}
	if d.hitCount("/workers/llms-full.txt") != 0 {
		t.Fatal("llms-full dumps are skipped")
	}
	// a second refresh sends validators and downloads nothing new
	before := d.hitCount("/workers/get-started/index.md")
	r2 := e.refresh("fx")
	if r2.NewVersion || r2.Unchanged != 3 {
		t.Fatalf("second refresh %+v", r2)
	}
	if d.hitCount("/workers/get-started/index.md") != before+1 {
		t.Fatal("expected one conditional GET per page")
	}
	// offline: with the site gone the indexed pack still answers from SQLite
	d.Close()
	res = e.search("bindings connect KV", "fx")
	if !res.Found {
		t.Fatalf("offline search failed: %+v", res)
	}
	p, _ := e.s.Get(context.Background(), "fx")
	if !p.Offline {
		t.Fatal("an indexed pack with content is offline-capable")
	}
	if _, err := e.s.Refresh(context.Background(), human, "fx", RefreshOptions{}); err == nil {
		t.Fatal("refresh with the site gone must fail")
	}
	if !e.search("KV eventually consistent", "fx").Found {
		t.Fatal("a failed refresh must not touch the previous version")
	}
}

func TestCrawlRespectsRobotsContentSignalDenyPathsAndHosts(t *testing.T) {
	d := newDocSite(t)
	d.set("/open/a/index.md", "# Open\n\nopen text\n")
	d.set("/private/b/index.md", "# Private\n\nsecret docs\n")
	d.set("/blocked/c/index.md", "# Blocked\n\ndeny path docs\n")
	e := newEnv(t, loopback)
	urls := fmt.Sprintf("  - {type: url_list, page_format: markdown_suffix, urls: [%s/open/a/, %s/private/b/, %s/blocked/c/, \"https://evil.example/x/\"]}", d.URL, d.URL, d.URL)
	_, err := e.s.Install(context.Background(), human, d.manifest("rb", urls, ", deny_paths: [/blocked/]"))
	if err == nil || !strings.Contains(err.Error(), "not in crawl.allowed_hosts") {
		t.Fatalf("a source on a host outside allowed_hosts is rejected at install: %v", err)
	}
	urls = fmt.Sprintf("  - {type: url_list, page_format: markdown_suffix, urls: [%s/open/a/, %s/private/b/, %s/blocked/c/]}", d.URL, d.URL, d.URL)
	e.install(d.manifest("rb", urls, ", deny_paths: [/blocked/], require_content_signal: ai-input"))
	r := e.refresh("rb")
	if r.Added != 1 || r.Skipped != 2 || r.Reasons["robots.txt disallows"] != 1 || r.Reasons["deny_paths"] != 1 {
		t.Fatalf("%+v", r)
	}
	if e.search("secret docs", "rb").Found || e.search("deny path docs", "rb").Found {
		t.Fatal("skipped pages must not be indexed")
	}
	if d.hitCount("/private/b/index.md") != 0 || d.hitCount("/blocked/c/index.md") != 0 {
		t.Fatal("disallowed pages must not even be requested")
	}

	// the content signal is required: robots.txt without ai-input=yes grants nothing
	d.mu.Lock()
	d.robots = "User-agent: *\nDisallow:\nContent-Signal: ai-train=no, ai-input=no\n"
	d.mu.Unlock()
	e.install(d.manifest("rb2", fmt.Sprintf("  - {type: url_list, page_format: markdown_suffix, urls: [%s/open/a/]}", d.URL), ", require_content_signal: ai-input"))
	_, err = e.s.Refresh(context.Background(), human, "rb2", RefreshOptions{})
	if err == nil {
		t.Fatal("expected failure when every page is refused by the content signal")
	}
	p, _ := e.s.Get(context.Background(), "rb2")
	if !strings.Contains(p.LastError, "failed") && !strings.Contains(p.LastError, "pages") {
		t.Logf("last error: %s", p.LastError)
	}
}

func TestRobotsParsing(t *testing.T) {
	r := ParseRobots("# c\nUser-agent: *\nDisallow: /a/\nAllow: /a/ok\nDisallow: /*.pdf$\n\nUser-agent: DrydockBot\nDisallow: /only-us/\nContent-Signal: ai-input=yes, search=yes\n")
	ua := "DrydockBot/1 (+x)"
	if r.Allowed(ua, "/only-us/x") || !r.Allowed(ua, "/a/x") {
		t.Fatal("the named group replaces the * group")
	}
	r2 := ParseRobots("User-agent: *\nDisallow: /a/\nAllow: /a/ok\nDisallow: /*.pdf$\n")
	if r2.Allowed(ua, "/a/x") || !r2.Allowed(ua, "/a/ok/y") || r2.Allowed(ua, "/files/x.pdf") || !r2.Allowed(ua, "/files/x.pdf.md") {
		t.Fatal("longest match, wildcards and anchors")
	}
	if v, ok := r.Signal("ai-input"); !ok || v != "yes" {
		t.Fatal("signal")
	}
	if _, ok := r2.Signal("ai-input"); ok {
		t.Fatal("no signal stated")
	}
	if (&Robots{DisallowAll: true}).Allowed(ua, "/") {
		t.Fatal("unreachable robots disallows all")
	}
}

func TestFetcherLimitsAndRedirects(t *testing.T) {
	d := newDocSite(t)
	d.set("/big.md", strings.Repeat("x", 100000))
	d.set("/small.md", "# ok\n")
	d.set("/bin", "\x00\x01\x02binary")
	d.extra["/to-other"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:"+strings.Split(d.host(), ":")[1]+"/small.md", http.StatusFound)
	}
	d.extra["/to-same"] = func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/small.md", http.StatusFound) }
	d.extra["/loop"] = func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) }
	d.extra["/slow"] = func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }
	d.extra["/html-as-octet"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write([]byte("PK"))
	}
	f := fetcherFor(d, func(p *FetchPolicy) { p.MaxBytes = 5000; p.Timeout = time.Second })
	ctx := context.Background()
	if _, err := f.Get(ctx, d.URL+"/big.md", Conditional{}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("size cap: %v", err)
	}
	if r, err := f.Get(ctx, d.URL+"/small.md", Conditional{}); err != nil || string(r.Body) != "# ok\n" {
		t.Errorf("small: %v", err)
	}
	if _, err := f.Get(ctx, d.URL+"/bin", Conditional{}); !errors.Is(err, ErrContentType) {
		t.Errorf("binary body: %v", err)
	}
	if _, err := f.Get(ctx, d.URL+"/html-as-octet", Conditional{}); !errors.Is(err, ErrContentType) {
		t.Errorf("zip: %v", err)
	}
	if _, err := f.Get(ctx, d.URL+"/to-other", Conditional{}); !errors.Is(err, ErrRedirect) {
		t.Errorf("a redirect to another host name is refused even if the address is the same: %v", err)
	}
	if r, err := f.Get(ctx, d.URL+"/to-same", Conditional{}); err != nil || !strings.HasSuffix(r.URL, "/small.md") {
		t.Errorf("same-host redirect: %v", err)
	}
	if _, err := f.Get(ctx, d.URL+"/loop", Conditional{}); !errors.Is(err, ErrRedirect) {
		t.Errorf("redirect loop: %v", err)
	}
	start := time.Now()
	if _, err := f.Get(ctx, d.URL+"/slow", Conditional{}); err == nil || time.Since(start) > 1800*time.Millisecond {
		t.Errorf("timeout: %v after %v", err, time.Since(start))
	}
	if _, err := f.Get(ctx, d.URL+"/missing", Conditional{}); err == nil {
		t.Error("404 must be an error")
	} else if he := new(*HTTPError); !errors.As(err, he) || (*he).Status != 404 {
		t.Errorf("404: %v", err)
	}
	// conditional requests
	r1, _ := f.Get(ctx, d.URL+"/small.md", Conditional{})
	r2, err := f.Get(ctx, d.URL+"/small.md", Conditional{ETag: r1.ETag})
	if err != nil || !r2.NotModified {
		t.Errorf("304: %v %+v", err, r2)
	}
	// not allowed host, scheme, credentials
	for _, u := range []string{"https://evil.example/x", "ftp://127.0.0.1/x", "http://user:pw@127.0.0.1:1/x", "file:///etc/passwd"} {
		if _, err := f.Get(ctx, u, Conditional{}); err == nil {
			t.Errorf("%s must be refused", u)
		}
	}
	// rate limit: 4 requests at 20 rps take at least 150ms
	slow := fetcherFor(d, func(p *FetchPolicy) { p.RPS = 20 })
	t0 := time.Now()
	for i := 0; i < 4; i++ {
		slow.Get(ctx, d.URL+"/small.md", Conditional{})
	}
	// each Get fetches robots.txt once (counted) plus the page: 5 polite requests
	if el := time.Since(t0); el < 150*time.Millisecond {
		t.Errorf("rate limit not enforced: %v", el)
	}
}

func TestSSRFProtection(t *testing.T) {
	d := newDocSite(t)
	d.set("/small.md", "# ok\n")
	// the allowlist names the host, but the address is loopback and AllowLoopback is off (the product setting)
	f := NewFetcher(FetchPolicy{AllowedHosts: []string{"127.0.0.1", "localhost"}, RPS: 1000, Timeout: 3 * time.Second}, nil)
	if _, err := f.Get(context.Background(), d.URL+"/small.md", Conditional{}); err == nil {
		t.Fatal("loopback must be refused when AllowLoopback is false")
	}
	if _, err := f.Get(context.Background(), "https://127.0.0.1:1/x", Conditional{}); !errors.Is(err, ErrBlockedAddr) {
		t.Fatalf("dial-time check should block the loopback address, got %v", err)
	}
	// even with loopback allowed (tests), private ranges and the metadata address stay blocked
	g := NewFetcher(FetchPolicy{AllowedHosts: []string{"10.1.2.3", "169.254.169.254"}, AllowLoopback: true, RPS: 1000, Timeout: 2 * time.Second}, nil)
	for _, u := range []string{"https://10.1.2.3/x", "https://169.254.169.254/latest/meta-data/"} {
		_, err := g.Get(context.Background(), u, Conditional{})
		if err == nil || !errors.Is(err, ErrBlockedAddr) {
			t.Errorf("%s: want ErrBlockedAddr, got %v", u, err)
		}
	}
	for ipStr, wantBlocked := range map[string]bool{
		"10.0.0.1": true, "172.16.5.4": true, "192.168.1.1": true, "169.254.169.254": true, "127.0.0.1": true, "0.0.0.0": true, "100.64.0.1": true,
		"224.0.0.1": true, "240.0.0.1": true, "::1": true, "fd00::1": true, "fe80::1": true, "::ffff:10.0.0.1": true, "2002:0a00:0001::": true, "::": true,
		"198.18.0.5": true, "8.8.8.8": false, "1.1.1.1": false, "2606:4700:4700::1111": false, "93.184.216.34": false,
	} {
		err := CheckPublicIP(net.ParseIP(ipStr), false)
		if (err != nil) != wantBlocked {
			t.Errorf("CheckPublicIP(%s) blocked=%v want %v", ipStr, err != nil, wantBlocked)
		}
	}
	if CheckPublicIP(net.ParseIP("127.0.0.1"), true) != nil || CheckPublicIP(net.ParseIP("10.0.0.1"), true) == nil {
		t.Error("allowLoopback only opens loopback")
	}
}

func TestRobotsServerErrorMeansDisallowAll(t *testing.T) {
	d := newDocSite(t)
	d.set("/a.md", "# a\n")
	d.status["/robots.txt"] = http.StatusInternalServerError
	f := fetcherFor(d, nil)
	if _, err := f.Get(context.Background(), d.URL+"/a.md", Conditional{}); !errors.Is(err, ErrRobots) {
		t.Fatalf("5xx robots.txt: %v", err)
	}
	d.status["/robots.txt"] = http.StatusNotFound
	if _, err := fetcherFor(d, nil).Get(context.Background(), d.URL+"/a.md", Conditional{}); err != nil {
		t.Fatalf("404 robots.txt means no rules: %v", err)
	}
}

func TestSitemapAndHTMLPages(t *testing.T) {
	d := newDocSite(t)
	d.set("/sitemap.xml", fmt.Sprintf(`<?xml version="1.0"?><urlset><url><loc>%s/docs/one</loc></url><url><loc>%s/docs/two</loc></url><url><loc>%s/blog/skip</loc></url><url><loc>https://evil.example/docs/x</loc></url></urlset>`, d.URL, d.URL, d.URL))
	d.set("/docs/one", `<html><head><title>One page</title></head><body><nav>menu</nav><main><h1>One page</h1><p>HTML documentation about <code>widgets</code>.</p></main></body></html>`)
	d.set("/docs/two", `<html><head><title>Two</title></head><body><main><h1>Two</h1><p>Second page with gadgets.</p></main><script>alert(1)</script></body></html>`)
	d.set("/blog/skip", "<html><body><main><p>blog</p></main></body></html>")
	e := newEnv(t, loopback)
	e.install(d.manifest("sm", fmt.Sprintf("  - {type: sitemap, url: %s/sitemap.xml, follow: /docs/, page_format: html}", d.URL), ""))
	r := e.refresh("sm")
	if r.Pages != 2 || r.Added != 2 || r.Skipped != 2 {
		t.Fatalf("%+v", r)
	}
	res := e.search("widgets", "sm")
	if !res.Found || res.Hits[0].Citation.URL != d.URL+"/docs/one" || !strings.Contains(res.Hits[0].Text, "`widgets`") || strings.Contains(res.Hits[0].Text, "menu") {
		t.Fatalf("%+v", res.Hits)
	}
	if e.search("alert", "sm").Found {
		t.Fatal("scripts must not be indexed")
	}
}

func TestRefreshAbortsWhenMostPagesFail(t *testing.T) {
	d := newDocSite(t)
	d.set("/a.md", "# a\n\nalpha text\n")
	d.set("/b.md", "# b\n\nbeta text\n")
	e := newEnv(t, loopback)
	urls := fmt.Sprintf("  - {type: url_list, urls: [%s/a.md, %s/b.md]}", d.URL, d.URL)
	e.install(d.manifest("ab", urls, ""))
	e.refresh("ab")
	d.mu.Lock()
	d.status["/a.md"], d.status["/b.md"] = 500, 500
	d.mu.Unlock()
	_, err := e.s.Refresh(context.Background(), human, "ab", RefreshOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "previous version is kept") {
		t.Fatalf("%v", err)
	}
	if !e.search("alpha", "ab").Found {
		t.Fatal("previous version must answer")
	}
	// one failing page of two keeps its old content and the refresh still commits the rest
	d.mu.Lock()
	delete(d.status, "/b.md")
	d.pages["/b.md"] = "# b\n\nbeta text changed\n"
	d.mu.Unlock()
	d.status["/a.md"] = 500
	_, err = e.s.Refresh(context.Background(), human, "ab", RefreshOptions{Force: true})
	if err != nil {
		t.Fatalf("1 of 2 failing is tolerated: %v", err)
	}
	if !e.search("alpha", "ab").Found || !e.search("changed", "ab").Found {
		t.Fatal("failed page keeps its previous content and the other page updates")
	}
}

var _ = httptest.NewServer
