package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/packs"
)

// Registry sync limits, set from the real registries measured on 2026-10-14 (docs/expert-packs.md section 9): a large
// commit's JSON is over the fetcher's 1 MiB cap (MicrosoftDocs/Agent-Skills), tarballs reach about 51 MB gzipped
// (merill/msgraph) and 177 MB unpacked, and published SKILL.md files reach 105,146 bytes (anthropics/skills claude-api).

func fixtureFetcher(g *ghFixture) *GitHubFetcher {
	f := packs.NewFetcher(packs.FetchPolicy{AllowedHosts: []string{"127.0.0.1"}, NoRobots: true, AllowLoopback: true, RPS: 1000, MaxBytes: 1 << 20, Timeout: 20 * time.Second}, nil)
	return &GitHubFetcher{Fetcher: f, APIBase: g.URL, TarballBase: g.URL}
}

// proseBody is benign SKILL.md text of about n bytes.
func proseBody(n int) string {
	line := "Prefer the documented client library and check the reference page before calling an endpoint.\n"
	return strings.Repeat(line, n/len(line)+1)[:n]
}

func TestResolveAsksForTheCommitIDAloneSoALargeCommitResolves(t *testing.T) {
	ctx := context.Background()
	g := newGHFixture(t)
	g.padDiff = 2 << 20 // the JSON commit is over the fetcher's 1 MiB cap, as MicrosoftDocs/Agent-Skills' was
	g.publish("MicrosoftDocs/Agent-Skills", "HEAD", sha1, map[string]string{"README.md": "x"})
	f := fixtureFetcher(g)
	sha, err := f.Resolve(ctx, "MicrosoftDocs/Agent-Skills", "")
	if err != nil || sha != sha1 {
		t.Fatalf("the sha media type answers the commit id alone: %q %v", sha, err)
	}
	// a server or proxy that ignores the media type and answers the JSON commit still resolves while the answer fits
	g.mu.Lock()
	g.jsonOnly, g.padDiff = true, 0
	g.mu.Unlock()
	if sha, err := f.Resolve(ctx, "MicrosoftDocs/Agent-Skills", "HEAD"); err != nil || sha != sha1 {
		t.Fatalf("JSON fallback: %q %v", sha, err)
	}
	// an answer that is not a commit id is refused
	g.publish("me/odd", "HEAD", "not-a-commit", map[string]string{})
	if _, err := f.Resolve(ctx, "me/odd", "HEAD"); err == nil || !strings.Contains(err.Error(), "unexpected response") {
		t.Fatalf("a malformed id: %v", err)
	}
	g.mu.Lock()
	g.jsonOnly = false
	g.mu.Unlock()
	if _, err := f.Resolve(ctx, "me/odd", "HEAD"); err == nil || !strings.Contains(err.Error(), "unexpected response") {
		t.Fatalf("a malformed id as text: %v", err)
	}
}

func TestParseCommitAnswer(t *testing.T) {
	for in, want := range map[string]string{
		sha1 + "\n":                     sha1,
		"  " + sha1:                     sha1,
		`{"sha":"` + sha2 + `"}`:        sha2,
		` {"sha":"` + sha2 + `","x":1}`: sha2,
		`{"sha":`:                       "",
		"":                              "",
	} {
		if got := parseCommitAnswer([]byte(in)); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// stripe/ai and remix-run/remix are about 19 MB gzipped: over the old 16 MB download cap.
func TestFetchReadsATarballOverTheOldDownloadCap(t *testing.T) {
	ctx := context.Background()
	noise := make([]byte, 20<<20)
	rand.New(rand.NewSource(1)).Read(noise) // incompressible: the gzipped tarball stays about 20 MB
	g := newGHFixture(t)
	g.publish("stripe/ai", "HEAD", sha1, map[string]string{
		"assets/noise.bin":           string(noise),
		"skills/stripe-api/SKILL.md": skillMD("stripe-api", "Stand-in skill.", "", "Use the API reference."),
	})
	got, err := fixtureFetcher(g).Fetch(ctx, "stripe/ai", sha1, "skills/")
	if err != nil || len(got) != 1 || got[0].Name != "stripe-api" {
		t.Fatalf("%v %+v", err, got)
	}
}

// remix-run/remix unpacks to 41 MB and MicrosoftDocs/Agent-Skills to 53 MB: past the old 32 MB read of the decompressed
// stream, which cut the archive into an "unexpected EOF". Files outside the root are skipped, never kept, and a root that holds
// many skills (the remix registry's .agents/skills/ since 2026-10-16: the app skill beside the contributor skills) yields each.
func TestExtractSkillsReadsAnArchiveThatUnpacksPastTheOldBound(t *testing.T) {
	tgz := buildTarball("remix-"+sha1, map[string]string{
		"a-docs/filler.bin":               strings.Repeat("\x00", 40<<20), // sorts before the skills, so it is read first
		".agents/skills/remix/SKILL.md":   skillMD("remix", "Stand-in skill.", "", "Use remix/* imports."),
		".agents/skills/make-pr/SKILL.md": skillMD("make-pr", "Contributor skill.", "", "Prepare the change for review."),
	})
	got, err := ExtractSkills(tgz, ".agents/skills/")
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %+v", err, got)
	}
	names := map[string]bool{}
	for _, g := range got {
		names[g.Name] = true
	}
	if !names["remix"] || !names["make-pr"] {
		t.Fatalf("both skills under the root: %+v", got)
	}
}

// bigZeroTarball streams a tarball holding one zero-filled file of size bytes outside any skill root.
func bigZeroTarball(t *testing.T, size int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "repo-abc/a-big/zeros.bin", Mode: 0o644, Size: size, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for left := size; left > 0; left -= int64(len(chunk)) {
		n := min(left, int64(len(chunk)))
		if _, err := tw.Write(chunk[:n]); err != nil {
			t.Fatal(err)
		}
	}
	body := skillMD("ok", "d", "", "x")
	tw.WriteHeader(&tar.Header{Name: "repo-abc/skills/ok/SKILL.md", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func TestExtractSkillsRefusesAnArchiveThatUnpacksPastTheBoundWithAClearMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("unpacks half a gigabyte of zeros")
	}
	tgz := bigZeroTarball(t, maxUnpackedBytes+(1<<20))
	_, err := ExtractSkills(tgz, "skills")
	if err == nil || !strings.Contains(err.Error(), "unpacks to more than 512 MB") || strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("a zip bomb is refused by name: %v", err)
	}
}

func TestExtractSkillsSaysATruncatedArchiveIsTruncated(t *testing.T) {
	tgz := buildTarball("repo-abc", map[string]string{
		"skills/a/SKILL.md": skillMD("a", "d", "", proseBody(20000)),
		"skills/b/SKILL.md": skillMD("b", "d", "", proseBody(20000)),
	})
	_, err := ExtractSkills(tgz[:len(tgz)/2], "skills")
	if err == nil || !strings.Contains(err.Error(), "truncated") || strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("a cut archive: %v", err)
	}
}

// anthropics/skills claude-api is 105,146 bytes and MicrosoftDocs skills reach 90,323: over the old 64 KiB limit.
func TestASkillOfOver64KiBIsReadAndOneOver128KiBIsRefused(t *testing.T) {
	ctx := context.Background()
	large := skillMD("claude-api", "Stand-in for a large published skill.", "", proseBody(105146-80))
	huge := skillMD("huge", "d", "", proseBody(MaxSkillBytes+10))
	if MaxSkillBytes != 128<<10 {
		t.Fatalf("MaxSkillBytes is %d", MaxSkillBytes)
	}
	if _, err := ParseRegistrySkill([]byte(large), "claude-api"); err != nil {
		t.Fatalf("a 105 KB skill parses: %v", err)
	}
	if _, err := ParseRegistrySkill([]byte(huge), "huge"); err == nil || !strings.Contains(err.Error(), "larger than 128 KiB") {
		t.Fatalf("over 128 KiB: %v", err)
	}
	// through the registry: the large one is imported, the huge one is reported as too large
	g := newGHFixture(t)
	g.publish("anthropics/skills", "HEAD", sha1, map[string]string{
		"skills/claude-api/SKILL.md":    large,
		"skills/claude-api/LICENSE.txt": "Apache License 2.0",
		"skills/huge/SKILL.md":          huge,
		"skills/huge/LICENSE.txt":       "Apache License 2.0",
	})
	e := newEnv(t, map[string]string{".aet/skills/big-repo/SKILL.md": skillMD("big-repo", "d", "", proseBody(100000))})
	e.useFixture(g)
	k, err := e.s.Import(ctx, human, ImportReq{ProjectID: "p1", RegistryID: "anthropic-skills", Name: "claude-api"})
	if err != nil || k.Name != "claude-api" || k.Status == StatusBlocked {
		t.Fatalf("a 105 KB registry skill imports: %v %+v", err, k)
	}
	if _, err := e.s.Import(ctx, human, ImportReq{ProjectID: "p1", RegistryID: "anthropic-skills", Name: "huge"}); err == nil || !strings.Contains(err.Error(), "128 KiB") {
		t.Fatalf("a registry skill over 128 KiB: %v", err)
	}
	// a repository skill of 100 KB is found too
	res, err := e.s.ScanRepo(ctx, "p1")
	if err != nil || res.Found != 1 || len(res.Errors) != 0 {
		t.Fatalf("a 100 KB repository skill: %v %+v", err, res)
	}
}
