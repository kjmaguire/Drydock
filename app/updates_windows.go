//go:build windows

package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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
// version and channel and nothing else; no source code or user data is ever sent.
func (b *Bridge) configureUpdater(wa *application.App, channel string, auto bool) error {
	if UpdateFeedURL == "" || UpdatePublicKey == "" {
		return errors.New("updates are not configured in this build")
	}
	key, err := base64.StdEncoding.DecodeString(UpdatePublicKey)
	if err != nil {
		return fmt.Errorf("update public key: %w", err)
	}
	p, err := endpoint.New(endpoint.Config{URL: UpdateFeedURL, Channel: channel})
	if err != nil {
		return err
	}
	cfg := updater.Config{CurrentVersion: Version, Providers: []updater.Provider{p}, PublicKey: key, Channel: channel}
	if auto {
		cfg.CheckInterval = 6 * time.Hour
	}
	return wa.Updater.Init(cfg)
}

// UpdateCheck looks for a newer version and reports whether it may be applied now, with every blocker.
func (b *Bridge) UpdateCheck() (map[string]any, error) {
	if b.app == nil || b.app.Updater == nil {
		return nil, errors.New("updates are not configured in this build")
	}
	rel, err := b.app.Updater.Check(context.Background())
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
	rel, err := b.app.Updater.Check(context.Background())
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
	if _, err := updatepolicy.Prepare(context.Background(), b.Core.DB, dir, exe, Version, rel.Version, time.Now()); err != nil {
		return err
	}
	return b.app.Updater.DownloadAndInstall(context.Background())
}

// UpdateRestart restarts into the staged update. The app job object ends every agent process tree first.
func (b *Bridge) UpdateRestart() error { return b.app.Updater.Restart(context.Background()) }
