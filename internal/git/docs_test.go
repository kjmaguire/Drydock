package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/git/gitfixture"
)

func docsReq(t *testing.T, srv *gitfixture.Server, ref, path string) DocsFetch {
	return DocsFetch{URL: srv.RepoURL(), Ref: ref, Path: path, Exts: []string{".md", ".mdx", ".txt"}, TempDir: t.TempDir(),
		MaxRepoBytes: 64 << 20, MaxFileBytes: 1 << 20, MaxFiles: 100, AllowHTTP: true}
}

func TestFetchDocsReadsOnlyTheTextFilesUnderThePath(t *testing.T) {
	srv := gitfixture.New(t, map[string]string{
		"README.md":              "# Not under the path\n",
		"docs/index.md":          "# Index\n\nWelcome.\n",
		"docs/guide/install.mdx": "# Install\n\nRun it.\n",
		"docs/notes.txt":         "notes\n",
		"docs/logo.png":          "\x89PNG not text",
		"docs/big.md":            strings.Repeat("x", 2<<20),
		"src/main.go":            "package main\n",
		".gitmodules":            "[submodule \"x\"]\n\tpath = docs/vendored\n\turl = https://example.invalid/x\n",
	})
	// a symbolic link and a submodule entry under the path are never followed (both written straight into the index, so the
	// test needs no symbolic link on disk, which Windows allows only with a privilege)
	target := filepath.Join(srv.Root, "link-target")
	if err := os.WriteFile(target, []byte("../README.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	oid := strings.TrimSpace(srv.Git("hash-object", "-w", target))
	srv.Git("update-index", "--add", "--cacheinfo", "120000,"+oid+",docs/link.md")
	srv.Git("update-index", "--add", "--cacheinfo", "160000,1111111111111111111111111111111111111111,docs/vendored")
	srv.Git("-c", "user.name=Fixture", "-c", "user.email=fixture@localhost", "commit", "-q", "-m", "link and submodule")
	head := strings.TrimSpace(srv.Git("rev-parse", "HEAD"))

	res, err := FetchDocs(context.Background(), docsReq(t, srv, "main", "docs"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != head {
		t.Fatalf("commit %s, want %s", res.Commit, head)
	}
	var paths []string
	for _, f := range res.Files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	if strings.Join(paths, ",") != "docs/guide/install.mdx,docs/index.md,docs/notes.txt" {
		t.Fatalf("files %v", paths)
	}
	for _, f := range res.Files {
		if f.Path == "docs/index.md" && string(f.Data) != "# Index\n\nWelcome.\n" {
			t.Fatalf("%q", f.Data)
		}
	}
	want := map[string]int{"too large": 1, "not a text document": 1, "symbolic link": 1, "submodule": 1}
	for k, v := range want {
		if res.Skipped[k] != v {
			t.Fatalf("skipped %v, want %v", res.Skipped, want)
		}
	}
}

func TestFetchDocsAtATagAndTheWholeRepository(t *testing.T) {
	srv := gitfixture.New(t, map[string]string{"a.md": "# A\n"})
	srv.Git("tag", "v1")
	srv.Commit(map[string]string{"b.md": "# B\n"}, "second")
	res, err := FetchDocs(context.Background(), docsReq(t, srv, "v1", ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "a.md" {
		t.Fatalf("the tag's tree only: %+v", res.Files)
	}
}

func TestFetchDocsRefusesWhatIsNotPlain(t *testing.T) {
	ctx := context.Background()
	srv := gitfixture.New(t, map[string]string{"a.md": "# A\n"})
	for _, ref := range []string{"--upload-pack=touch /tmp/x", "main..dev", "-main", "main@{1}", "a b", ""} {
		if _, err := FetchDocs(ctx, docsReq(t, srv, ref, "")); !errors.Is(err, ErrDocsRef) {
			t.Fatalf("ref %q: %v", ref, err)
		}
	}
	for _, p := range []string{"../etc", "/abs", "docs/../../x", ".git", "docs/.git/hooks", ":(glob)**", "-x"} {
		if _, err := FetchDocs(ctx, docsReq(t, srv, "main", p)); !errors.Is(err, ErrDocsPath) {
			t.Fatalf("path %q: %v", p, err)
		}
	}
	r := docsReq(t, srv, "main", "")
	r.AllowHTTP = false
	if _, err := FetchDocs(ctx, r); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("plain http: %v", err)
	}
	r = docsReq(t, srv, "main", "")
	r.URL = "file://" + filepath.Join(srv.Root, "repo.git")
	if _, err := FetchDocs(ctx, r); err == nil {
		t.Fatal("a file URL is refused")
	}
	r = docsReq(t, srv, "nope", "")
	if _, err := FetchDocs(ctx, r); err == nil {
		t.Fatal("a ref the repository does not have")
	}
}

func TestFetchDocsStopsAtTheSizeBound(t *testing.T) {
	// the fixture's blobs are random-ish and large: the fetch of the wanted blobs outgrows a small bound
	files := map[string]string{}
	for i := 0; i < 40; i++ {
		files[filepath.ToSlash(filepath.Join("docs", strings.Repeat("p", i+1)+".md"))] = randomText(256<<10, uint32(i+1))
	}
	srv := gitfixture.New(t, files)
	r := docsReq(t, srv, "main", "docs")
	r.MaxRepoBytes = 1 << 20
	_, err := FetchDocs(context.Background(), r)
	if !errors.Is(err, ErrDocsTooLarge) {
		t.Fatalf("%v", err)
	}
	left, _ := os.ReadDir(r.TempDir)
	if len(left) != 0 {
		t.Fatalf("the temporary repository is removed: %v", left)
	}
}

// randomText is text that does not compress (so the fixture's objects are as big as their content).
func randomText(n int, seed uint32) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 \n"
	b := make([]byte, n)
	x := 2463534242 ^ seed*2654435761
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = alphabet[x%uint32(len(alphabet))]
	}
	return string(b)
}

func TestFetchDocsConnectsToThePinnedAddress(t *testing.T) {
	srv := gitfixture.New(t, map[string]string{"a.md": "# A\n"})
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	r := docsReq(t, srv, "main", "")
	// a name nothing resolves: only the pin can take git to the server
	r.URL = "http://docs.drydock.invalid:" + port + "/repo.git"
	if _, err := FetchDocs(context.Background(), r); err == nil {
		t.Fatal("without the pin the name does not resolve")
	}
	r.Resolve = "docs.drydock.invalid:" + port + ":127.0.0.1"
	res, err := FetchDocs(context.Background(), r)
	if err != nil || len(res.Files) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
}
