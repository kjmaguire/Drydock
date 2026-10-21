// Package runners keeps GitHub Actions self-hosted runners for a person's private repositories on their PC (decision P56,
// docs/ci-runners.md). Every runner is ephemeral and registered just in time: Drydock asks GitHub for a one-job registration
// (`generate-jitconfig`), starts the runner with it, and when the job is over (or Drydock stops) the runner's files go and a
// new registration is made for the next job. A Windows runner runs on the PC in a folder of its own (on a Dev Drive when
// there is one); a Linux runner runs in a fresh container on the container engine (Docker Desktop's WSL backend), with no
// access to the PC's drives or to the engine itself. Runners only ever read and report: the code fixes and pushes stay with
// Drydock's own pipeline. Only private repositories are served.
package runners

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// Platform names the runner build for an operating system and architecture, as the release names them ("win-x64",
// "linux-x64", "linux-arm64").
type Platform string

const (
	WinX64     Platform = "win-x64"
	WinARM64   Platform = "win-arm64"
	LinuxX64   Platform = "linux-x64"
	LinuxARM64 Platform = "linux-arm64"
)

// Asset is one runner archive of a release.
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Release is a GitHub Actions runner release: its version and, per platform, the archive with the SHA-256 the release
// notes publish for it.
type Release struct {
	Version string             `json:"version"`
	Assets  map[Platform]Asset `json:"assets"`
}

// DownloadPrefix is the only place runner archives are fetched from.
const DownloadPrefix = "https://github.com/actions/runner/releases/download/"

// ReleasePath is the REST path of the newest runner release.
const ReleasePath = "/repos/actions/runner/releases/latest"

var (
	assetName  = regexp.MustCompile(`^actions-runner-((?:win|linux|osx)-(?:x64|arm64|arm))-([0-9]+\.[0-9]+\.[0-9]+)\.(zip|tar\.gz)$`)
	shaMarker  = regexp.MustCompile(`<!--\s*BEGIN SHA ([a-z0-9_-]+)\s*-->\s*([0-9a-fA-F]{64})\s*<!--\s*END SHA ([a-z0-9_-]+)\s*-->`)
	versionTag = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)$`)
)

type rawRelease struct {
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// parseRelease turns GitHub's release answer into a Release. An archive is kept only when its name is the plain runner
// archive of the release's version, it is served from the runner's own release downloads, and the release notes carry its
// SHA-256 between the BEGIN SHA and END SHA markers: an archive Drydock cannot check is never offered.
func parseRelease(r rawRelease) (Release, error) {
	m := versionTag.FindStringSubmatch(strings.TrimSpace(r.TagName))
	if m == nil {
		return Release{}, fmt.Errorf("the runner release tag %q is not a version", r.TagName)
	}
	if r.Draft || r.Prerelease {
		return Release{}, fmt.Errorf("the runner release %s is not a stable release", r.TagName)
	}
	version := m[1]
	sums := map[string]string{}
	for _, s := range shaMarker.FindAllStringSubmatch(r.Body, -1) {
		if s[1] == s[3] {
			sums[s[1]] = strings.ToLower(s[2])
		}
	}
	rel := Release{Version: version, Assets: map[Platform]Asset{}}
	for _, a := range r.Assets {
		n := assetName.FindStringSubmatch(a.Name)
		if n == nil || n[2] != version || !strings.HasPrefix(a.URL, DownloadPrefix) {
			continue
		}
		sum, ok := sums[n[1]]
		if !ok {
			continue
		}
		rel.Assets[Platform(n[1])] = Asset{Name: a.Name, URL: a.URL, SHA256: sum, Size: a.Size}
	}
	if len(rel.Assets) == 0 {
		return Release{}, fmt.Errorf("the runner release %s lists no archive with a published SHA-256", version)
	}
	return rel, nil
}

// LatestRelease reads the newest runner release through the GitHub API.
func LatestRelease(ctx context.Context, api host.API) (Release, error) {
	if api == nil {
		return Release{}, errors.New("GitHub is not connected")
	}
	var raw rawRelease
	if _, err := api.Do(ctx, "GET", ReleasePath, nil, &raw, ""); err != nil {
		return Release{}, fmt.Errorf("could not read the newest runner release: %w", err)
	}
	return parseRelease(raw)
}
