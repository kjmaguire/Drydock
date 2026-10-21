//go:build windows

package app

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"

	"github.com/kjmaguire/drydock/internal/updatepolicy"
)

// Build-time settings (set with -ldflags "-X github.com/kjmaguire/drydock/app.Version=1.0.0 ..."). With no feed or no public
// key the updater stays unconfigured and the UI says so; Drydock never guesses an update source.
var (
	Version         = "0.0.0-dev"
	UpdateFeedURL   = "" // e.g. https://updates.example.com/{{channel}}/manifest.json
	UpdatePublicKey = "" // base64 Ed25519 public key pinned at build time
)

// configureUpdater wires the Wails updater to Drydock's own static feed (decision D8). The feed receives the client IP,
// version and channel and nothing else; no source code or user data is ever sent. The feed must be https, the pinned key a raw
// 32-byte Ed25519 key, and every release signed over its digest and over its version, channel, file name and size
// (updatepolicy.Audit, audit 2026-10-16, H6): the Wails updater alone installs a release that carries no signature after the
// digest check, even when a key is pinned.
func (b *Bridge) configureUpdater(wa *application.App, channel string, auto bool) error {
	if UpdateFeedURL == "" || UpdatePublicKey == "" {
		return errors.New("updates are not configured in this build")
	}
	key, err := updatepolicy.ParsePublicKey(UpdatePublicKey)
	if err != nil {
		return err
	}
	if err := updatepolicy.RequireHTTPS(UpdateFeedURL); err != nil {
		return err
	}
	p, err := endpoint.New(endpoint.Config{URL: UpdateFeedURL, Channel: channel, HTTPClient: updateHTTPClient()})
	if err != nil {
		return err
	}
	cfg := updater.Config{CurrentVersion: Version, Providers: []updater.Provider{auditedProvider{inner: p, pub: key, channel: channel}}, PublicKey: key, Channel: channel}
	if auto {
		cfg.CheckInterval = 6 * time.Hour
	}
	return wa.Updater.Init(cfg)
}

// updateHTTPClient has no total timeout: the endpoint provider's default of 30 seconds in all would cut a download of the
// installer off on a slow connection. The connection, the TLS handshake and the wait for a response each have one, and the
// caller's context ends everything.
func updateHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}}
}

// auditedProvider puts updatepolicy.Audit in front of a provider: a release that is unsigned, signed by another key, whose
// version, channel, file or size the key did not sign, or whose artifact is not https is an error at Check, before anything is
// downloaded, and is checked again at Download.
type auditedProvider struct {
	inner   updater.Provider
	pub     ed25519.PublicKey
	channel string
}

func (a auditedProvider) Name() string { return a.inner.Name() }

func (a auditedProvider) Check(ctx context.Context, req updater.CheckRequest) (*updater.Release, error) {
	rel, err := a.inner.Check(ctx, req)
	if err != nil || rel == nil {
		return rel, err
	}
	if err := updatepolicy.Audit(offered(rel), a.pub, a.channel); err != nil {
		return nil, err
	}
	return rel, nil
}

func (a auditedProvider) Download(ctx context.Context, rel *updater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	if rel == nil {
		return errors.New("updates: no release to download")
	}
	if err := updatepolicy.Audit(offered(rel), a.pub, a.channel); err != nil {
		return err
	}
	return a.inner.Download(ctx, rel, dst, onProgress)
}

// offered describes a Wails release in the terms updatepolicy.Audit takes.
func offered(rel *updater.Release) updatepolicy.Offered {
	o := updatepolicy.Offered{Version: rel.Version, Channel: rel.Channel, Filename: rel.Artifact.Filename, Size: rel.Artifact.Size, Metadata: rel.Metadata}
	o.ArtifactURL, _ = rel.Metadata["endpoint.artifact.url"].(string)
	if v := rel.Verification; v != nil {
		o.DigestAlgo, o.Digest, o.SignatureAlgo, o.Signature = v.DigestAlgo, v.Digest, v.SignatureAlgo, v.Signature
	}
	return o
}

// UpdateCheck looks for a newer version and reports whether it may be applied now, with every blocker.
func (b *Bridge) UpdateCheck() (map[string]any, error) {
	if b.app == nil || b.app.Updater == nil {
		return nil, errors.New("updates are not configured in this build")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rel, err := b.app.Updater.Check(ctx)
	if err != nil {
		return nil, err
	}
	if rel == nil {
		return map[string]any{"available": false, "current": Version}, nil
	}
	runs, agents, err := b.Core.Activity(context.Background())
	if err != nil {
		return nil, err
	}
	blockers := updatepolicy.Check(updatepolicy.State{Current: Version, Candidate: rel.Version, Channel: rel.Channel, ActiveRuns: runs, LiveAgents: agents,
		AllowedChannels: func(ch string) bool { return ch == "" || ch == "stable" || b.Core.ChannelAllowed(ch) }})
	return map[string]any{"available": true, "current": Version, "release": rel, "blockers": blockers}, nil
}

// UpdateApply takes the pre-update backup, keeps the previous executable, then downloads, verifies and stages the update.
// It never restarts by itself: the human confirms the restart.
func (b *Bridge) UpdateApply() error {
	checkCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rel, err := b.app.Updater.Check(checkCtx)
	if err != nil || rel == nil {
		return fmt.Errorf("no update to apply: %v", err)
	}
	runs, agents, err := b.Core.Activity(context.Background())
	if err != nil {
		return err
	}
	if bl := updatepolicy.Check(updatepolicy.State{Current: Version, Candidate: rel.Version, Channel: rel.Channel, ActiveRuns: runs, LiveAgents: agents,
		AllowedChannels: func(ch string) bool { return ch == "" || ch == "stable" || b.Core.ChannelAllowed(ch) }}); len(bl) > 0 {
		return fmt.Errorf("update blocked: %s", bl[0])
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := DataDir()
	if err != nil {
		return err
	}
	prepared, err := updatepolicy.Prepare(context.Background(), b.Core.DB, dir, exe, Version, rel.Version, time.Now())
	if err != nil {
		return err
	}
	// remembered, so that the next start (the new version) can release the backup's prune exemption once it has run healthy
	if err := updatepolicy.Record(context.Background(), b.Core.DB, prepared, Version, rel.Version, time.Now()); err != nil {
		return fmt.Errorf("the update was not started: %w", err)
	}
	return b.app.Updater.DownloadAndInstall(context.Background())
}

// updateHealthyAfter is how long a freshly updated app must have been running before its pre-update backup is let go.
const updateHealthyAfter = 2 * time.Minute

// UpdateRestart restarts into the staged update. The app job object ends every agent process tree first.
func (b *Bridge) UpdateRestart() error { return b.app.Updater.Restart(context.Background()) }
