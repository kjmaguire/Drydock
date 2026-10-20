// Command drydock-bundle makes the tools folder a Drydock download carries beside drydock.exe (decision P77: osv-scanner,
// Opengrep and llama.cpp with the TurboQuant KV cache types come installed). It reads the pinned releases from
// packaging/bundle.json, reads each release from its own GitHub repository, downloads the Windows file from that release only,
// accepts it only when its SHA-256 matches the digest GitHub recorded at upload, and writes the files with bundle.json (each
// file's release, address and SHA-256). The app installs from that folder at its first start (internal/bundle); nothing in it
// is run by this command.
//
//	go run ./cmd/drydock-bundle -pins packaging/bundle.json -out dist/tools
//
// GITHUB_TOKEN, when set, is sent to the GitHub API only (a higher rate limit on CI); the downloads need no sign-in.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kjmaguire/drydock/internal/bundle"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/runners"
	"github.com/kjmaguire/drydock/internal/scan"
)

// Pins is packaging/bundle.json.
type Pins struct {
	Scan []struct {
		Tool string `json:"tool"`
		Tag  string `json:"tag"`
	} `json:"scan"`
	Llama *struct {
		Source  string `json:"source"`
		Backend string `json:"backend"`
		Tag     string `json:"tag"`
	} `json:"llama"`
}

// Build makes the tools folder in out from the pins. api reads the releases; client downloads the files.
func Build(ctx context.Context, pins Pins, api host.API, client *http.Client, out string, now time.Time) (bundle.Manifest, error) {
	m := bundle.Manifest{Version: 1, Built: now.UTC().Format(time.RFC3339), Scan: []bundle.ScanTool{}}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return m, err
	}
	for _, p := range pins.Scan {
		src, ok := scan.SourceOf(p.Tool)
		if !ok {
			return m, fmt.Errorf("%q is not a tool Drydock installs", p.Tool)
		}
		rel, err := scan.ToolReleaseByTag(ctx, api, src, p.Tag)
		if err != nil {
			return m, err
		}
		if _, err := runners.DownloadWith(ctx, client, rel.Asset, out, runners.DownloadOptions{Prefix: src.DownloadPrefix(), MaxBytes: scan.MaxToolAsset}); err != nil {
			return m, fmt.Errorf("%s %s: %w", p.Tool, p.Tag, err)
		}
		m.Scan = append(m.Scan, bundle.ScanTool{Tool: p.Tool, Tag: rel.Tag, Asset: rel.Asset})
		fmt.Printf("%s %s: %s sha256 %s\n", p.Tool, rel.Tag, rel.Asset.Name, rel.Asset.SHA256)
	}
	if l := pins.Llama; l != nil {
		src, ok := localllm.SourceByID(l.Source)
		if !ok {
			return m, fmt.Errorf("%q is not a llama.cpp build source", l.Source)
		}
		b := localllm.Backend(l.Backend)
		// a pin must name one of the source's releases, not a dev build (the fork marks those pre-release): said here, before
		// anything is fetched, rather than as "no release" from the listing
		if l.Tag != "" && !src.ReleaseTag(l.Tag) {
			return m, fmt.Errorf("%s: %q is not one of its release tags (%s); its other tags are dev builds, which Drydock does not install", src.Name, l.Tag, src.TagForm())
		}
		var rel localllm.Release
		var err error
		if l.Tag == "" {
			rel, err = localllm.LatestRelease(ctx, api, src, b)
		} else {
			rel, err = localllm.ReleaseByTag(ctx, api, src, b, l.Tag)
		}
		if err != nil {
			return m, err
		}
		if rel.Cudart != nil {
			return m, fmt.Errorf("%s needs a CUDA runtime beside it; the download carries a build that does not (pin vulkan or cpu)", rel.Archive.Name)
		}
		if _, err := runners.DownloadWith(ctx, client, rel.Archive, out, runners.DownloadOptions{Prefix: src.DownloadPrefix(), MaxBytes: localllm.MaxRuntimeArchive}); err != nil {
			return m, fmt.Errorf("llama.cpp %s: %w", rel.Tag, err)
		}
		m.Llama = &bundle.LlamaBuild{Source: src.ID, Tag: rel.Tag, Backend: string(rel.Backend), Archive: rel.Archive}
		fmt.Printf("llama.cpp (%s) %s: %s sha256 %s\n", src.ID, rel.Tag, rel.Archive.Name, rel.Archive.SHA256)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	return m, os.WriteFile(filepath.Join(out, bundle.FileName), append(b, '\n'), 0o644)
}

func main() {
	pinsPath := flag.String("pins", filepath.Join("packaging", "bundle.json"), "the pinned releases")
	out := flag.String("out", filepath.Join("dist", "tools"), "the tools folder to write")
	flag.Parse()
	raw, err := os.ReadFile(*pinsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var pins Pins
	if err := json.Unmarshal(raw, &pins); err != nil {
		fmt.Fprintln(os.Stderr, *pinsPath+":", err)
		os.Exit(1)
	}
	api := host.HTTPAPI{BaseURL: "https://api.github.com", Token: os.Getenv("GITHUB_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if _, err := Build(ctx, pins, api, &http.Client{Timeout: 30 * time.Minute}, *out, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
