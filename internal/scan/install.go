package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/bundle"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/runners"
)

// Installing the external tools (decision P66): Drydock downloads a tool's newest Windows release from its own GitHub
// repository, accepts it only when its SHA-256 matches the digest GitHub recorded at upload, and keeps it in its own folder
// under the app's data (<data>/tools/<tool>/<tag>/). Nothing unchecked is ever run; no install script from the internet is
// executed (Opengrep's install.ps1 is not run). A tool installed this way is used before one on PATH; a program a person
// chose (P55) still comes first.

// ToolSource says where a tool's Windows program comes from: its GitHub repository and the names its release assets have.
type ToolSource struct {
	Tool  string `json:"tool"`
	Label string `json:"label"`
	Repo  string `json:"repo"`
	// Assets are the release asset names that carry the Windows program, in order of preference: a .zip holds the program
	// (and the DLLs beside it); anything else is the program itself.
	Assets []string `json:"assets"`
	// Program is the file name the program gets here (the asset is renamed to it).
	Program string `json:"program"`
	Licence string `json:"licence"`
}

// ToolSources are the tools Drydock can install: osv-scanner (goreleaser names its binaries <binary>_<os>_<arch>) and
// Opengrep (one self-extracting exe up to 1.x; from 2.0.0 an archive with opengrep.exe and its DLLs, per its install.ps1).
var ToolSources = []ToolSource{
	{Tool: "osv-scanner", Label: "osv-scanner", Repo: "google/osv-scanner", Assets: []string{"osv-scanner_windows_amd64.exe"}, Program: "osv-scanner.exe", Licence: "Apache-2.0"},
	{Tool: "opengrep", Label: "Opengrep", Repo: "opengrep/opengrep", Assets: []string{"opengrep_windows_x86.zip", "opengrep_windows_x86.exe"}, Program: "opengrep.exe", Licence: "LGPL-2.1"},
}

// SourceOf finds a tool's source by name.
func SourceOf(tool string) (ToolSource, bool) {
	for _, s := range ToolSources {
		if s.Tool == tool {
			return s, true
		}
	}
	return ToolSource{}, false
}

// ReleasesPath is the GitHub API path of the repository's release listing.
func (s ToolSource) ReleasesPath() string { return "/repos/" + s.Repo + "/releases?per_page=20" }

// DownloadPrefix is the only place the tool's assets may be fetched from.
func (s ToolSource) DownloadPrefix() string {
	return "https://github.com/" + s.Repo + "/releases/download/"
}

// ToolRelease is one release of a tool Drydock can install: the asset with the Windows program and the SHA-256 digest
// GitHub recorded for it.
type ToolRelease struct {
	Tool  string        `json:"tool"`
	Tag   string        `json:"tag"`
	Asset runners.Asset `json:"asset"`
	Notes string        `json:"notes,omitempty"`
}

type rawToolRelease struct {
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"` // "sha256:<hex>", recorded by GitHub at upload
	} `json:"assets"`
}

// pickToolRelease finds the newest stable release (drafts and pre-releases skipped) that carries one of the source's
// Windows assets with a SHA-256 digest: an asset Drydock cannot check is never offered.
func pickToolRelease(src ToolSource, list []rawToolRelease) (ToolRelease, error) {
	tried := 0
	for _, r := range list {
		if r.Draft || r.Prerelease || strings.TrimSpace(r.TagName) == "" {
			continue
		}
		tried++
		for _, want := range src.Assets {
			for _, a := range r.Assets {
				if a.Name != want || !strings.HasPrefix(a.URL, src.DownloadPrefix()) {
					continue
				}
				sum, ok := sha256Digest(a.Digest)
				if !ok {
					continue
				}
				return ToolRelease{Tool: src.Tool, Tag: r.TagName, Notes: firstNoteLine(r.Body),
					Asset: runners.Asset{Name: a.Name, URL: a.URL, SHA256: sum, Size: a.Size}}, nil
			}
		}
	}
	if tried == 0 {
		return ToolRelease{}, fmt.Errorf("%s lists no release", src.Label)
	}
	return ToolRelease{}, fmt.Errorf("none of the newest %d releases of %s carries %s with a SHA-256 digest", tried, src.Label, strings.Join(src.Assets, " or "))
}

func sha256Digest(d string) (string, bool) {
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

func firstNoteLine(body string) string {
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		l = strings.TrimSpace(strings.TrimLeft(l, "#*- "))
		if l != "" {
			return cleanLine(l, 200)
		}
	}
	return ""
}

// ToolReleaseByTag reads one release of the tool by its tag (the pinned release a download carries, cmd/drydock-bundle) and
// checks it as LatestToolRelease would: a stable release with the Windows asset and its SHA-256 digest.
func ToolReleaseByTag(ctx context.Context, api host.API, src ToolSource, tag string) (ToolRelease, error) {
	if api == nil {
		return ToolRelease{}, errors.New("GitHub cannot be reached")
	}
	var one rawToolRelease
	if _, err := api.Do(ctx, "GET", "/repos/"+src.Repo+"/releases/tags/"+url.PathEscape(tag), nil, &one, ""); err != nil {
		return ToolRelease{}, fmt.Errorf("could not read %s's release %s: %w", src.Repo, tag, err)
	}
	return pickToolRelease(src, []rawToolRelease{one})
}

// LatestToolRelease reads the tool's releases through the GitHub API (no sign-in: the repositories are public) and picks the
// newest stable one with a checked Windows asset.
func LatestToolRelease(ctx context.Context, api host.API, src ToolSource) (ToolRelease, error) {
	if api == nil {
		return ToolRelease{}, errors.New("GitHub cannot be reached")
	}
	var list []rawToolRelease
	if _, err := api.Do(ctx, "GET", src.ReleasesPath(), nil, &list, ""); err != nil {
		return ToolRelease{}, fmt.Errorf("could not read the releases of %s: %w", src.Repo, err)
	}
	return pickToolRelease(src, list)
}

// InstalledTool is a tool Drydock installed: the release and where its program is.
type InstalledTool struct {
	Tool        string    `json:"tool"`
	Tag         string    `json:"tag"`
	Path        string    `json:"path"`
	InstalledAt time.Time `json:"installedAt"`
}

// InstallProgress is one step of an install, for the page.
type InstallProgress struct {
	Phase string `json:"phase"` // downloading | unpacking | done
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

// MaxToolAsset is the largest tool asset Drydock downloads (the programs are tens of megabytes).
const MaxToolAsset = 512 << 20

// Installer keeps the installed tools under Root: Root/<tool>/cache holds the checked downloads, Root/<tool>/<tag> the
// program (with the DLLs an archive carried), Root/<tool>/installed.json which release is current.
type Installer struct {
	Root string
	// GitHub reads the release listings (no sign-in). Default: api.github.com directly.
	GitHub host.API
	// HTTP downloads the assets. Default: a client with a 30-minute timeout.
	HTTP *http.Client
	// Program, when set, replaces an installed tool's program (TEST DOUBLE: the labelled stand-in of scantest, so a
	// development host that cannot run a Windows program can still install one); the leading arguments it returns come
	// first. The product leaves it nil.
	Program func(in InstalledTool) (path string, args []string)
}

// NewInstaller wires the defaults under root.
func NewInstaller(root string) *Installer {
	return &Installer{Root: root, GitHub: host.HTTPAPI{BaseURL: "https://api.github.com"}, HTTP: &http.Client{Timeout: 30 * time.Minute}}
}

// Supported says whether a tool installed here can run: Windows (the assets are Windows programs), or anywhere a stand-in
// program is injected.
func (i *Installer) Supported() bool { return runtime.GOOS == "windows" || i.Program != nil }

func (i *Installer) dir(tool string) string { return filepath.Join(i.Root, tool) }

const installedToolFile = "installed.json"

// Installed is the tool's current release, if any (its program must still be there).
func (i *Installer) Installed(tool string) (InstalledTool, bool) {
	b, err := os.ReadFile(filepath.Join(i.dir(tool), installedToolFile))
	if err != nil {
		return InstalledTool{}, false
	}
	var in InstalledTool
	if json.Unmarshal(b, &in) != nil || in.Path == "" || in.Tool != tool {
		return InstalledTool{}, false
	}
	if st, err := os.Stat(in.Path); err != nil || !st.Mode().IsRegular() {
		return InstalledTool{}, false
	}
	return in, true
}

// ToolFor is the Tool the scan runs for an installed tool: its program, or the stand-in that replaces it.
func (i *Installer) ToolFor(in InstalledTool) Tool {
	if i.Program != nil {
		p, args := i.Program(in)
		return Tool{Path: p, Args: args, Installed: true}
	}
	return Tool{Path: in.Path, Installed: true}
}

// Install downloads the release's asset, checks it against its digest, puts the program (unpacked from an archive, or the
// asset itself renamed) in its own folder and makes it the tool's current release. Older releases are removed, except the
// one that was current until now when the new one failed.
func (i *Installer) Install(ctx context.Context, rel ToolRelease, report func(InstallProgress)) (InstalledTool, error) {
	src, ok := SourceOf(rel.Tool)
	if !ok {
		return InstalledTool{}, fmt.Errorf("%q is not a tool Drydock installs", rel.Tool)
	}
	if report == nil {
		report = func(InstallProgress) {}
	}
	if strings.TrimSpace(rel.Tag) == "" || strings.ContainsAny(rel.Tag, `/\`) || rel.Tag == "." || rel.Tag == ".." {
		return InstalledTool{}, fmt.Errorf("bad release tag %q", rel.Tag)
	}
	root := i.dir(src.Tool)
	cache := filepath.Join(root, "cache")
	asset, err := runners.DownloadWith(ctx, i.HTTP, rel.Asset, cache, runners.DownloadOptions{Prefix: src.DownloadPrefix(), MaxBytes: MaxToolAsset,
		Progress: func(done, total int64) { report(InstallProgress{Phase: "downloading", Done: done, Total: total}) }})
	if err != nil {
		return InstalledTool{}, err
	}
	dir := filepath.Join(root, safeDirName(rel.Tag))
	report(InstallProgress{Phase: "unpacking"})
	_ = os.RemoveAll(dir)
	var program string
	if strings.EqualFold(filepath.Ext(asset), ".zip") {
		if err := runners.Unzip(asset, dir); err != nil {
			return InstalledTool{}, fmt.Errorf("unpack %s: %w", rel.Asset.Name, err)
		}
		if program, err = findProgram(dir, src.Program); err != nil {
			_ = os.RemoveAll(dir)
			return InstalledTool{}, err
		}
	} else {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return InstalledTool{}, err
		}
		program = filepath.Join(dir, src.Program)
		if err := copyFile(asset, program, 0o755); err != nil {
			_ = os.RemoveAll(dir)
			return InstalledTool{}, err
		}
	}
	in := InstalledTool{Tool: src.Tool, Tag: rel.Tag, Path: program, InstalledAt: time.Now().UTC()}
	b, _ := json.Marshal(in)
	if err := os.WriteFile(filepath.Join(root, installedToolFile), b, 0o644); err != nil {
		return InstalledTool{}, err
	}
	_ = os.Remove(filepath.Join(i.Root, src.Tool+removedMarker)) // installed again: the person wants it
	i.prune(root, dir)
	_ = os.Remove(asset) // the checked download is in place; the cache keeps nothing
	report(InstallProgress{Phase: "done"})
	return in, nil
}

// Remove forgets the tool's current release and deletes every release and cached download of it. It also records that the
// person removed it, so the copy the download carries is not put back at the next start (SeedFromBundle).
func (i *Installer) Remove(tool string) error {
	if _, ok := SourceOf(tool); !ok {
		return fmt.Errorf("%q is not a tool Drydock installs", tool)
	}
	err := os.RemoveAll(i.dir(tool))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(i.Root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(i.Root, tool+removedMarker), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}

// Removed reports whether a person removed the tool (Remove) and has not installed it again since.
func (i *Installer) Removed(tool string) bool {
	_, err := os.Stat(filepath.Join(i.Root, tool+removedMarker))
	return err == nil
}

// removedMarker beside a tool's folder says a person removed the tool: the bundled copy stays out until they install it again.
const removedMarker = ".removed"

// bundledFile in a tool's folder names the bundled release last put in place from the download (SeedFromBundle).
const bundledFile = "bundled.json"

// SeedFromBundle puts the release the download carries in place (decision P77: the tools come installed). It does so when
// the tool has no release here, or when the one here is an earlier bundled release (the app was updated with a newer one).
// A release the person installed (Install, Update) is left alone, as is a tool the person removed. The bundled file is checked
// against the SHA-256 the bundle recorded, copied into the download cache, and installed by the ordinary Install, which
// checks the digest again and fetches nothing. It reports whether it installed.
func (i *Installer) SeedFromBundle(ctx context.Context, dir string, b bundle.ScanTool) (InstalledTool, bool, error) {
	src, ok := SourceOf(b.Tool)
	if !ok {
		return InstalledTool{}, false, fmt.Errorf("%q is not a tool Drydock installs", b.Tool)
	}
	if _, err := os.Stat(filepath.Join(i.Root, b.Tool+removedMarker)); err == nil {
		return InstalledTool{}, false, nil
	}
	last := ""
	if raw, err := os.ReadFile(filepath.Join(i.dir(b.Tool), bundledFile)); err == nil {
		var v struct {
			Tag string `json:"tag"`
		}
		if json.Unmarshal(raw, &v) == nil {
			last = v.Tag
		}
	}
	if in, ok := i.Installed(b.Tool); ok && (in.Tag == b.Tag || in.Tag != last) {
		return in, false, nil
	}
	if _, err := bundle.Stage(dir, b.Asset, src.DownloadPrefix(), filepath.Join(i.dir(b.Tool), "cache")); err != nil {
		return InstalledTool{}, false, err
	}
	in, err := i.Install(ctx, ToolRelease{Tool: b.Tool, Tag: b.Tag, Asset: b.Asset}, nil)
	if err != nil {
		return InstalledTool{}, false, err
	}
	v, _ := json.Marshal(map[string]string{"tag": b.Tag})
	if err := os.WriteFile(filepath.Join(i.dir(b.Tool), bundledFile), v, 0o644); err != nil {
		return in, true, err
	}
	return in, true, nil
}

// prune removes the tool's releases other than keep.
func (i *Installer) prune(root, keep string) {
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		p := filepath.Join(root, e.Name())
		if !e.IsDir() || p == keep || e.Name() == "cache" {
			continue
		}
		_ = os.RemoveAll(p)
	}
}

// findProgram finds the program in an unpacked archive (Opengrep's keeps it at the top).
func findProgram(dir, name string) (string, error) {
	var found string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), name) {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("the archive has no %s in it", name)
	}
	return found, nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func safeDirName(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.', c == '_':
			b.WriteRune(c)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
