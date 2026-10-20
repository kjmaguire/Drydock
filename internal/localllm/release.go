package localllm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
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
// needed) with a SHA-256 digest: an archive Drydock cannot check is never offered. Drafts are skipped; pre-releases count
// only for a source whose builds are marked so. Where a release carries the archive for several toolkit versions, the
// newest toolkit is taken, and its runtime archive must be the same version.
func pickRelease(src Source, b Backend, list []rawRelease) (Release, error) {
	want := src.assets("<tag>", b)
	if want == "" {
		return Release{}, fmt.Errorf("%s has no Windows build for %s", src.Name, Describe(b).Label)
	}
	tried, pre := 0, 0
	for _, r := range list {
		if r.Prerelease && !src.Prerelease && !r.Draft {
			pre++
		}
		if r.Draft || (r.Prerelease && !src.Prerelease) || strings.TrimSpace(r.TagName) == "" {
			continue
		}
		tried++
		rel := Release{Source: src.ID, Tag: r.TagName, Backend: b, Notes: firstLine(r.Body)}
		pattern := src.assets(r.TagName, b)
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
			continue
		}
		rel.Archive = *best
		if name := src.cudart(best.Name); name != "" {
			c, ok := checked[name]
			if !ok {
				continue
			}
			rel.Cudart = &c
		}
		return rel, nil
	}
	if tried == 0 {
		if pre > 0 {
			// the fork's dev builds are marked pre-release: only its releases (tagged bNNNN-X.Y.Z) are installed
			return Release{}, fmt.Errorf("%s lists no release, only %d pre-release build(s), which are not installed", src.Name, pre)
		}
		return Release{}, fmt.Errorf("%s lists no release", src.Name)
	}
	return Release{}, fmt.Errorf("none of the newest %d releases of %s carries %s with a SHA-256 digest", tried, src.Name, want)
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

// LatestRelease reads the source's releases through the GitHub API (no sign-in needed) and picks the newest that has the
// backend's checked archive.
func LatestRelease(ctx context.Context, api host.API, src Source, b Backend) (Release, error) {
	if api == nil {
		return Release{}, errors.New("GitHub cannot be reached")
	}
	var list []rawRelease
	if _, err := api.Do(ctx, "GET", src.ReleasesPath(), nil, &list, ""); err != nil {
		return Release{}, fmt.Errorf("could not read the releases of %s: %w", src.Repo, err)
	}
	return pickRelease(src, b, list)
}
