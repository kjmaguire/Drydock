package localllm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/runners"
)

// Release is one build of a source Drydock can install: the archive of a backend and, for CUDA, the runtime archive beside
// it, each with the SHA-256 digest GitHub recorded at upload.
type Release struct {
	Source  string         `json:"source"`
	Tag     string         `json:"tag"`
	Backend Backend        `json:"backend"`
	Archive runners.Asset  `json:"archive"`
	Cudart  *runners.Asset `json:"cudart,omitempty"`
	// Notes is the first line of the release notes (what the build brings), for the page.
	Notes string `json:"notes,omitempty"`
}

type rawRelease struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"` // "sha256:<hex>", recorded by GitHub at upload (since June 2025)
	} `json:"assets"`
}

// pickRelease finds the newest release in the listing that carries the backend's archive (and its CUDA runtime where one is
// needed) with a SHA-256 digest: an archive Drydock cannot check is never offered. Only the source's release tags count
// (Source.ReleaseTag: its dev builds do not); drafts are skipped, and pre-releases count only for a source whose releases are
// marked so. The newest is the highest by the source's own numbering (Source.Newer), not the first listed, so a late backport
// to an older line is not taken over a newer release. Where a release carries the archive for several toolkit versions, the
// newest toolkit is taken, and its runtime archive must be the same version.
func pickRelease(src Source, b Backend, list []rawRelease) (Release, error) {
	want := src.assets("<tag>", b)
	if want == "" {
		return Release{}, fmt.Errorf("%s has no Windows build for %s", src.Name, Describe(b).Label)
	}
	tried := 0
	skipped := map[string]int{} // why a release of the listing was not a candidate
	var found *Release
	for _, r := range list {
		tag := strings.TrimSpace(r.TagName)
		switch {
		case r.Draft:
			skipped["draft"]++
			continue
		case !src.ReleaseTag(tag):
			skipped["not a release tag ("+src.TagForm()+")"]++
			continue
		case r.Prerelease && !src.Prerelease:
			skipped["pre-release"]++
			continue
		}
		tried++
		rel, ok := releaseOf(src, b, r)
		if !ok {
			continue
		}
		if found == nil {
			found = &rel
		} else if newer, _ := src.Newer(rel.Tag, found.Tag); newer {
			found = &rel
		}
	}
	if found != nil {
		return *found, nil
	}
	if tried == 0 {
		return Release{}, fmt.Errorf("%s lists no release Drydock installs%s", src.Name, skippedText(skipped))
	}
	return Release{}, fmt.Errorf("none of the newest %d releases of %s carries %s with a SHA-256 digest", tried, src.Name, want)
}

// releaseOf is the release r as Drydock would install it for the backend, when it carries the archive with its digest (and
// the CUDA runtime beside it where one is needed).
func releaseOf(src Source, b Backend, r rawRelease) (Release, bool) {
	rel := Release{Source: src.ID, Tag: strings.TrimSpace(r.TagName), Backend: b, Notes: firstLine(r.Body)}
	pattern := src.assets(rel.Tag, b)
	checked := map[string]runners.Asset{} // every asset of the release Drydock could verify
	var best *runners.Asset
	var bestVersion []int
	for _, a := range r.Assets {
		sum, good := digest(a.Digest)
		if !good || !strings.HasPrefix(a.URL, src.DownloadPrefix()) {
			continue
		}
		asset := runners.Asset{Name: a.Name, URL: a.URL, SHA256: sum, Size: a.Size}
		checked[a.Name] = asset
		if ok, _ := path.Match(pattern, a.Name); !ok {
			continue
		}
		if v := toolkitVersion(a.Name); best == nil || newerToolkit(v, bestVersion) {
			c := asset
			best, bestVersion = &c, v
		}
	}
	if best == nil {
		return Release{}, false
	}
	rel.Archive = *best
	if name := src.cudart(best.Name); name != "" {
		c, ok := checked[name]
		if !ok {
			return Release{}, false
		}
		rel.Cudart = &c
	}
	return rel, true
}

// skippedText says what the listing held instead of a release: " (skipped: 3 pre-release, 1 draft)".
func skippedText(skipped map[string]int) string {
	if len(skipped) == 0 {
		return ""
	}
	keys := make([]string, 0, len(skipped))
	for k := range skipped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", skipped[k], k))
	}
	return " (skipped: " + strings.Join(parts, ", ") + ")"
}

func digest(d string) (string, bool) {
	hex, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(d)), "sha256:")
	if !ok || len(hex) != 64 {
		return "", false
	}
	for _, c := range hex {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return hex, true
}

func firstLine(body string) string {
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		l = strings.TrimSpace(strings.TrimLeft(l, "#*- "))
		if l != "" {
			if len(l) > 200 {
				l = l[:200] + "..."
			}
			return l
		}
	}
	return ""
}

// ReleaseByTag reads one release of the source by its tag (the pinned build a download carries, cmd/drydock-bundle) and checks
// it as LatestRelease would: the backend's archive with its SHA-256 digest.
func ReleaseByTag(ctx context.Context, api host.API, src Source, b Backend, tag string) (Release, error) {
	if api == nil {
		return Release{}, errors.New("GitHub cannot be reached")
	}
	var one rawRelease
	if _, err := api.Do(ctx, "GET", "/repos/"+src.Repo+"/releases/tags/"+url.PathEscape(tag), nil, &one, ""); err != nil {
		return Release{}, fmt.Errorf("could not read %s's release %s: %w", src.Repo, tag, err)
	}
	return pickRelease(src, b, []rawRelease{one})
}

// maxReleasePages bounds how far back LatestRelease reads (300 releases).
const maxReleasePages = 3

// LatestRelease reads the source's releases through the GitHub API (no sign-in needed) and picks the newest that has the
// backend's checked archive.
func LatestRelease(ctx context.Context, api host.API, src Source, b Backend) (Release, error) {
	if api == nil {
		return Release{}, errors.New("GitHub cannot be reached")
	}
	// a source with many dev builds (the fork tags one per commit and platform) can push its releases past the first page
	var list []rawRelease
	for page := 1; page <= maxReleasePages; page++ {
		var one []rawRelease
		if _, err := api.Do(ctx, "GET", src.ReleasesPath(page), nil, &one, ""); err != nil {
			return Release{}, fmt.Errorf("could not read the releases of %s: %w", src.Repo, err)
		}
		list = append(list, one...)
		if len(one) < releasesPerPage {
			break
		}
	}
	return pickRelease(src, b, list)
}
