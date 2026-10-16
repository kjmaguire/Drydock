package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	go s.runInstall(jctx, projectID, src, by)
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
