package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/bundle"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/scan"
)

// Installing the scan's external tools (decision P66, docs/security-scan.md section 5): a person presses Install (or Update)
// on the Security page and Drydock downloads the tool's newest stable release from its own GitHub repository, checks it
// against the digest GitHub recorded, and keeps it under <data>/tools. The install runs in the background; its progress
// lives here (security.tools shows it) and each change is announced on security.settings so every open Security page
// re-reads. Only a person may install or remove: the program reads the whole project.

// toolJob is one tool's install in progress, or how its last one ended.
type toolJob struct {
	job    scan.ToolJob
	cancel context.CancelFunc
}

// withInstalls completes a tools answer with what the installer knows: whether installs work here, each installed release,
// and each tool's install job.
func (s *securityExt) withInstalls(t scan.Tools) scan.Tools {
	t.CanInstall = s.inst.Supported()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, info := range []*scan.ToolInfo{&t.OSV, &t.Opengrep} {
		if info.Source == "installed" {
			if in, ok := s.inst.Installed(info.Tool); ok {
				info.Tag = in.Tag
				// a newer release than the one installed (the twice-daily check, decision P77): the page offers it
				if l := s.latest[info.Tool]; l != "" && l != in.Tag {
					info.Latest = l
				}
			}
		}
		if j, ok := s.jobs[info.Tool]; ok {
			job := j.job
			info.Job = &job
		}
	}
	return t
}

// installTool starts the install (or update) of a tool in the background and answers with the tools as they are now.
func (s *securityExt) installTool(ctx context.Context, projectID, tool, by string) (scan.Tools, error) {
	if !isPerson(by) {
		return scan.Tools{}, errors.New("only a person may install a scan tool")
	}
	src, ok := scan.SourceOf(tool)
	if !ok {
		return scan.Tools{}, fmt.Errorf("%q is not a scan tool Drydock installs (osv-scanner or opengrep)", tool)
	}
	if !s.inst.Supported() {
		return scan.Tools{}, fmt.Errorf("%s's Windows program cannot run here: install it on Windows", src.Label)
	}
	s.mu.Lock()
	if j, ok := s.jobs[tool]; ok && j.job.State == "running" {
		s.mu.Unlock()
		return s.tools(ctx, projectID), fmt.Errorf("%s is already being installed", src.Label)
	}
	jctx, cancel := context.WithCancel(context.Background())
	s.jobs[tool] = &toolJob{job: scan.ToolJob{State: "running", Phase: "looking up the newest release", Started: time.Now().UTC()}, cancel: cancel}
	s.mu.Unlock()
	s.announce(ctx, projectID, tool, "running", by)
	safe.Go("core.securityExt.installTool", func() { s.runInstall(jctx, projectID, src, by) })
	return s.tools(ctx, projectID), nil
}

// runInstall is the background part: look the release up, skip it when it is the one installed, else download and place it.
func (s *securityExt) runInstall(ctx context.Context, projectID string, src scan.ToolSource, by string) {
	finish := func(err error) {
		s.mu.Lock()
		j := s.jobs[src.Tool]
		j.job.Ended = time.Now().UTC()
		switch {
		case err == nil:
			j.job.State = "done"
			if !strings.HasPrefix(j.job.Phase, "already") {
				j.job.Phase = "done"
			}
		case errors.Is(err, context.Canceled):
			j.job.State, j.job.Error = "cancelled", "cancelled"
		default:
			j.job.State, j.job.Error = "failed", err.Error()
		}
		j.cancel = nil
		st := j.job.State
		s.mu.Unlock()
		s.announce(context.Background(), projectID, src.Tool, st, by)
	}
	rel, err := scan.LatestToolRelease(ctx, s.inst.GitHub, src)
	if err != nil {
		finish(err)
		return
	}
	if in, ok := s.inst.Installed(src.Tool); ok && in.Tag == rel.Tag {
		s.mu.Lock()
		s.jobs[src.Tool].job.Phase, s.jobs[src.Tool].job.Tag = "already the newest release ("+rel.Tag+")", rel.Tag
		s.mu.Unlock()
		finish(nil)
		return
	}
	s.mu.Lock()
	s.jobs[src.Tool].job.Phase, s.jobs[src.Tool].job.Tag = "downloading "+rel.Tag, rel.Tag
	s.mu.Unlock()
	s.announce(context.Background(), projectID, src.Tool, "running", by)
	last := time.Now()
	_, err = s.inst.Install(ctx, rel, func(p scan.InstallProgress) {
		s.mu.Lock()
		j := s.jobs[src.Tool]
		j.job.Phase, j.job.Tag, j.job.Done, j.job.Total = p.Phase+" "+rel.Tag, rel.Tag, p.Done, p.Total
		s.mu.Unlock()
		if time.Since(last) > 700*time.Millisecond || p.Phase != "downloading" {
			last = time.Now()
			s.announce(context.Background(), projectID, src.Tool, "running", by)
		}
	})
	finish(err)
}

// announce tells every Security page the tool's install changed; the event carries the tool, so each page re-reads the tools
// (a tool is the workspace's, whichever project's page asked).
func (s *securityExt) announce(ctx context.Context, projectID, tool, state, by string) {
	s.c.publish(ctx, "security.settings", projectID, map[string]any{"tool": tool, "install": state, "by": by})
}

// cancelInstall stops a tool's install; nothing is left half installed (the download is checked and placed as a whole).
func (s *securityExt) cancelInstall(ctx context.Context, projectID, tool string) (scan.Tools, error) {
	if _, ok := scan.SourceOf(tool); !ok {
		return scan.Tools{}, fmt.Errorf("%q is not a scan tool Drydock installs (osv-scanner or opengrep)", tool)
	}
	s.mu.Lock()
	j, ok := s.jobs[tool]
	if ok && j.cancel != nil {
		j.cancel()
	}
	s.mu.Unlock()
	return s.tools(ctx, projectID), nil
}

// removeTool deletes the release Drydock installed; the tool is then looked for on PATH again. Only a person may.
func (s *securityExt) removeTool(ctx context.Context, projectID, tool, by string) (scan.Tools, error) {
	if !isPerson(by) {
		return scan.Tools{}, errors.New("only a person may remove a scan tool")
	}
	src, ok := scan.SourceOf(tool)
	if !ok {
		return scan.Tools{}, fmt.Errorf("%q is not a scan tool Drydock installs (osv-scanner or opengrep)", tool)
	}
	s.mu.Lock()
	if j, ok := s.jobs[tool]; ok && j.job.State == "running" {
		s.mu.Unlock()
		return scan.Tools{}, fmt.Errorf("%s is being installed", src.Label)
	}
	delete(s.jobs, tool)
	s.mu.Unlock()
	if err := s.inst.Remove(tool); err != nil {
		return scan.Tools{}, err
	}
	s.announce(ctx, projectID, tool, "removed", by)
	return s.tools(ctx, projectID), nil
}

// seedBundled installs, in the background, the scan tools the download carries (decision P77: "I want the app to already have
// the dependencies installed"). Each is put in place only when the tool has no release here or an earlier bundled one; a
// release the person installed, or a tool the person removed, is left as it is (scan.Installer.SeedFromBundle). A seed that
// fails is logged and the tool's Install button still works.
func (s *securityExt) seedBundled(ctx context.Context) {
	m, err := bundle.Read(s.c.cfg.BundleDir)
	if err != nil {
		s.c.logf("security: the download's tools: %v", err)
		return
	}
	if m == nil || len(m.Scan) == 0 {
		return
	}
	dir := s.c.cfg.BundleDir
	safe.Go("core.securityExt.seedBundled", func() {
		for _, b := range m.Scan {
			// the seed is an install job like a person's: the page shows it, and an Install pressed meanwhile waits for it
			s.mu.Lock()
			if j, ok := s.jobs[b.Tool]; ok && j.job.State == "running" {
				s.mu.Unlock()
				continue
			}
			job := &toolJob{job: scan.ToolJob{State: "running", Phase: "installing " + b.Tag + " from the download", Tag: b.Tag, Started: time.Now().UTC()}}
			s.jobs[b.Tool] = job
			s.mu.Unlock()
			in, did, err := s.inst.SeedFromBundle(context.Background(), dir, b)
			s.mu.Lock()
			job.job.Ended = time.Now().UTC()
			switch {
			case err != nil:
				job.job.State, job.job.Error = "failed", "from the download: "+err.Error()
			case did:
				job.job.State, job.job.Phase = "done", "installed "+in.Tag+" from the download"
			default:
				delete(s.jobs, b.Tool) // nothing to do: the tool's own state says it all
			}
			s.mu.Unlock()
			switch {
			case err != nil:
				s.c.logf("security: %s from the download: %v", b.Tool, err)
				s.announce(context.Background(), "", b.Tool, "failed", "drydock:bundle")
			case did:
				s.c.logf("security: %s %s installed from the download", b.Tool, in.Tag)
				s.announce(context.Background(), "", b.Tool, "done", "drydock:bundle")
			}
		}
	})
}

// Tool updates (decision P77, on the owner's words: "have the ability to update them if they are out of date"): three minutes
// after start (after the download's tools are in place) and every twelve hours, each installed tool's newest stable release is
// read from its GitHub repository; a newer one than installed is offered on the Security page (security.tools: latest). A check
// that cannot reach GitHub keeps what it knew. Nothing is installed without the person pressing Update.
const (
	toolUpdatesFirst = 3 * time.Minute
	toolUpdatesEvery = 12 * time.Hour
)

func (s *securityExt) watchUpdates(ctx context.Context) {
	safe.Go("core.securityExt.watchUpdates", func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(toolUpdatesFirst):
		}
		t := time.NewTicker(toolUpdatesEvery)
		defer t.Stop()
		for {
			s.checkUpdates(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})
}

// checkUpdates reads the newest release of every installed tool and announces the ones that changed.
func (s *securityExt) checkUpdates(ctx context.Context) {
	for _, src := range scan.ToolSources {
		if _, ok := s.inst.Installed(src.Tool); !ok {
			continue
		}
		rel, err := scan.LatestToolRelease(ctx, s.inst.GitHub, src)
		if err != nil {
			if ctx.Err() == nil {
				s.c.logf("security: %s update check: %v", src.Tool, err)
			}
			continue
		}
		s.mu.Lock()
		changed := s.latest[src.Tool] != rel.Tag
		s.latest[src.Tool] = rel.Tag
		s.mu.Unlock()
		if changed {
			s.announce(context.Background(), "", src.Tool, "checked", "drydock:update-check")
		}
	}
}
