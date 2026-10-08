package packs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// scriptedSummarizer is a LABELED test double for the optional cheap-model digest step.
type scriptedSummarizer struct {
	mu    sync.Mutex
	asked []bool
	err   error
}

func (s *scriptedSummarizer) Summarize(_ context.Context, d Digest, allowTrainable bool) (string, error) {
	s.mu.Lock()
	s.asked = append(s.asked, allowTrainable)
	s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	return "Summary: " + d.Changes[0].URL, nil
}

func TestDigestSummarizerRouteAndFailureIsolation(t *testing.T) {
	sum := &scriptedSummarizer{}
	e := newEnv(t, func(o *Options) { o.Summarizer = sum })
	dir := filepath.Join(e.dir, "docs")
	writeFiles(t, dir, map[string]string{"a.md": "# A\n\nfirst\n"})
	pub := strings.Replace(string(localManifest("pubp", dir, "refresh: {digest: {enabled: true, data_class: trainable_ok}}\n")), "visibility: private", "visibility: public\nlicense: {content: CC0-1.0}", 1)
	e.install([]byte(pub))
	e.install(localManifest("privp", dir, "refresh: {digest: {enabled: true}}\n"))
	e.install(localManifest("offp", dir, ""))
	for _, id := range []string{"pubp", "privp", "offp"} {
		e.refresh(id)
	}
	if len(sum.asked) != 2 || !sum.asked[0] || sum.asked[1] {
		t.Fatalf("a public pack may use a trainable route for its digest, a private one may not, a disabled digest is not summarized: %v", sum.asked)
	}
	vs, _ := e.s.Versions(context.Background(), "pubp")
	if vs[0].Summary == "" || vs[0].Digest == nil || vs[0].Digest.Summary == "" {
		t.Fatalf("%+v", vs[0])
	}
	// the refresh never depends on the model
	sum.err = errors.New("provider down")
	writeFiles(t, dir, map[string]string{"a.md": "# A\n\nsecond\n"})
	r, err := e.s.Refresh(context.Background(), human, "pubp", RefreshOptions{})
	if err != nil || !r.NewVersion || r.Digest.Summary != "" || len(r.Digest.Changes) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestInstallTemplatesListAndDrift(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ts, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range ts {
		p, err := e.s.Install(ctx, human, []byte(tpl.YAML))
		if err != nil {
			t.Fatalf("%s: %v", tpl.ID, err)
		}
		if p.Status != "new" || p.Chunks != 0 || p.AgeDays != -1 || p.Offline {
			t.Fatalf("a freshly installed pack has fetched nothing: %+v", p)
		}
	}
	// nothing was crawled
	list, _ := e.s.List(ctx)
	if len(list) != 2 || list[0].ID != "cloudflare" || list[0].Attribution == "" || list[1].Kind != KindLive {
		t.Fatalf("%+v", list)
	}
	if _, err := e.s.Refresh(ctx, human, "msdocs", RefreshOptions{}); !errors.Is(err, ErrNotIndexed) {
		t.Fatalf("%v", err)
	}
	// manifest on disk differs from the installed one: reported, never applied
	f := filepath.Join(e.s.o.Dir, "cloudflare", "pack.yaml")
	b, _ := os.ReadFile(f)
	os.WriteFile(f, append(b, []byte("# edited by someone\n")...), 0o644)
	p, _ := e.s.Get(ctx, "cloudflare")
	if !p.ManifestDrift {
		t.Fatal("drift must be reported")
	}
	if p.Manifest.Crawl.AllowedHosts[0] != "developers.cloudflare.com" {
		t.Fatal("the installed manifest is the DB copy")
	}
	// re-installing the same id updates the manifest in place
	p2, err := e.s.Install(ctx, human, b)
	if err != nil || p2.ManifestDrift {
		t.Fatalf("%v %+v", err, p2)
	}
	// an invalid manifest changes nothing
	if _, err := e.s.Install(ctx, human, []byte("schema: 1\nid: x\n")); err == nil {
		t.Fatal("invalid manifest")
	}
}

func TestNoScannerIsRefused(t *testing.T) {
	if _, err := New(Options{DB: nil, Dir: "x"}); err == nil {
		t.Fatal("DB required")
	}
	e := newEnv(t)
	if _, err := New(Options{DB: e.db, Dir: e.dir}); !errors.Is(err, ErrNoScanner) {
		t.Fatalf("a missing secret scanner must fail closed, got %v", err)
	}
}
