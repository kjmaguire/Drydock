package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/safe"
)

// A project made before its clone lands (B12). project.setup with repo {kind: github, later: true} (New project's Clone) makes
// the project at once with no repository and clones in the background, under the core's own lifetime: the person lands on the
// project's Work page while git runs, and the clone is attached to the project when it lands (project.attachRepo's checks,
// project.updated {repoAttached}). github.clone_progress and github.cloneCancel work as for any clone. A clone that fails, is
// cancelled or is cut short by Drydock closing leaves the project as it is, without a repository, and removes its half-made
// folder; project.cloneState says why until the app restarts, and Project settings' Clone and attach tries again. Nothing is
// started: the project's crew waits for the person, as New project's projects always do.

// LandingClone is the clone a made project waits for, or how it ended.
type LandingClone struct {
	ProjectID string `json:"projectId"`
	Repo      string `json:"repo"`
	// Running is true while git runs or the clone is being attached.
	Running bool `json:"running"`
	// Error says why the clone was not attached (it failed, was cancelled, or attaching it was refused); empty while running.
	Error string `json:"error,omitempty"`
}

// landingClones is the clones made projects wait for, by project, and the goroutines running them (Close waits for them).
type landingClones struct {
	mu        sync.Mutex
	wg        sync.WaitGroup
	byProject map[string]LandingClone
}

func (l *landingClones) set(v LandingClone) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byProject == nil {
		l.byProject = map[string]LandingClone{}
	}
	l.byProject[v.ProjectID] = v
}

func (l *landingClones) drop(projectID string) {
	l.mu.Lock()
	delete(l.byProject, projectID)
	l.mu.Unlock()
}

func (l *landingClones) get(projectID string) (LandingClone, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.byProject[projectID]
	return v, ok
}

// checkLaterClone refuses, before the project is made, a clone that could not start: a name that is not owner/name, a folder
// that is not a full path, a target folder that is not empty, or the same repository (or folder) being cloned already.
func (c *Core) checkLaterClone(fullName, parentDir string) (repo, parent string, err error) {
	repo, parent, dest, err := cloneTarget(fullName, parentDir)
	if err != nil {
		return "", "", err
	}
	if ents, err := os.ReadDir(dest); err == nil && len(ents) > 0 {
		return "", "", fmt.Errorf("%s already exists and is not empty: attach it as a local repository instead, or choose another folder", dest)
	}
	remove, busy := c.clones.add(normalizeRepo(repo), cleanCloneDest(dest), func(error) {})
	if busy != nil {
		return "", "", busy
	}
	remove()
	return repo, parent, nil
}

// landClone clones repo into parent in the background and attaches it to the project when it lands.
func (c *Core) landClone(projectID, repo, parent string) LandingClone {
	v := LandingClone{ProjectID: projectID, Repo: repo, Running: true}
	c.landing.set(v)
	c.landing.wg.Add(1)
	safe.Go("core.landClone", func() {
		defer c.landing.wg.Done()
		ctx := c.bg
		err := func() error {
			cl, err := c.GitHubClone(ctx, repo, parent)
			if err != nil {
				return err
			}
			if _, err := c.AttachRepository(ctx, projectID, cl.Path); err != nil {
				// the clone is kept: it is a whole repository the person can attach by hand
				return fmt.Errorf("%s was cloned to %s but could not be attached to the project: %w", repo, cl.Path, err)
			}
			return nil
		}()
		pub := context.WithoutCancel(ctx)
		if err == nil {
			c.landing.drop(projectID)
			c.publish(pub, "project.clone_landed", projectID, map[string]any{"repo": repo})
			return
		}
		why := err.Error()
		if errors.Is(ctx.Err(), context.Canceled) && !strings.Contains(why, "was cancelled") {
			why = fmt.Sprintf("the clone of %s stopped because Drydock closed: nothing was kept", repo)
		}
		c.landing.set(LandingClone{ProjectID: projectID, Repo: repo, Error: why})
		c.publish(pub, "project.clone_failed", projectID, map[string]any{"repo": repo})
	})
	return v
}

// CloneState is the clone the project waits for, or how it ended since the app started; nil when there is none.
func (c *Core) CloneState(projectID string) *LandingClone {
	if v, ok := c.landing.get(projectID); ok {
		return &v
	}
	return nil
}
