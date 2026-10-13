package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/skills"
)

// Decision P46: every registry a project imports from is checked twice a day for new or changed skills. A check reads the
// registry's default branch (one API call; the tarball only when the commit moved), stores what it holds, and when a project
// has something new or changed that it was not told about yet, posts a notice and a Needs-you item in that project's alerts
// channel. Nothing is imported or accepted by the check: the Skills page shows what is new and the person imports it.
const (
	skillWatchFirstPass = 2 * time.Minute
	skillWatchInterval  = 12 * time.Hour
)

func init() {
	RegisterExtension(Extension{Name: "skillwatch", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		h := c.Human
		m := map[string]Method{
			"skill.importAll": bind(func(ctx context.Context, p struct {
				ProjectID  string `json:"projectId"`
				RegistryID string `json:"registryId"`
				Ref        string `json:"ref"`
			}) (any, error) {
				k := c.Knowledge()
				if k == nil {
					return nil, fmt.Errorf("skills are not available")
				}
				res, err := k.Skills.ImportAll(ctx, h(), p.ProjectID, p.RegistryID, p.Ref)
				if err == nil {
					k.audit(ctx, "skill.import_all", p.RegistryID, map[string]any{"project": p.ProjectID, "commit": res.Commit, "imported": len(res.Imported), "updated": len(res.Updated), "blocked": len(res.Blocked)})
				}
				return res, err
			}),
			"skill.registryStatus": bind(func(ctx context.Context, _ struct{}) (any, error) {
				k := c.Knowledge()
				if k == nil {
					return nil, fmt.Errorf("skills are not available")
				}
				return k.Skills.RegistryStatuses(ctx)
			}),
			// skill.registryCheck checks one registry (or every watched one) now, tells the projects what is new, and returns
			// the statuses. Human only: it contacts the network.
			"skill.registryCheck": bind(func(ctx context.Context, p struct {
				RegistryID string `json:"registryId"`
			}) (any, error) {
				k := c.Knowledge()
				if k == nil {
					return nil, fmt.Errorf("skills are not available")
				}
				if p.RegistryID != "" {
					st, err := k.Skills.CheckRegistry(ctx, p.RegistryID)
					if err != nil {
						return nil, err
					}
					c.tellSkillUpdates(ctx, []skills.RegistryStatus{st})
					k.audit(ctx, "skill.registry_check", p.RegistryID, map[string]any{"commit": st.LastCommit, "error": st.LastError})
					return k.Skills.RegistryStatuses(ctx)
				}
				if _, err := c.RunSkillWatch(ctx); err != nil {
					return nil, err
				}
				return k.Skills.RegistryStatuses(ctx)
			}),
		}
		ctx, cancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(skillWatchFirstPass):
			}
			t := time.NewTicker(skillWatchInterval)
			defer t.Stop()
			for {
				if _, err := c.RunSkillWatch(ctx); err != nil && ctx.Err() == nil {
					c.logf("skill registry check: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
		return m, cancel, nil
	}})
}

// RunSkillWatch is one scheduled pass: check every watched registry and tell each project what is new for it.
func (c *Core) RunSkillWatch(ctx context.Context) ([]skills.RegistryStatus, error) {
	k := c.Knowledge()
	if k == nil {
		return nil, nil
	}
	sts, err := k.Skills.CheckWatched(ctx)
	if err != nil {
		return sts, err
	}
	for _, st := range sts {
		if st.LastError != "" {
			c.logf("skill registry %s: %s", st.RegistryID, st.LastError)
		}
	}
	c.tellSkillUpdates(ctx, sts)
	return sts, nil
}

// tellSkillUpdates posts, for each registry whose commit moved since the projects were last told, a notice and a Needs-you
// item in every project that has something new or changed from it, publishes skill.registry_updates, and marks the commit
// told. A registry with nothing new for anyone is marked told without a word.
func (c *Core) tellSkillUpdates(ctx context.Context, sts []skills.RegistryStatus) {
	k := c.Knowledge()
	for _, st := range sts {
		if st.LastError != "" || st.LastCommit == "" || st.LastCommit == st.NotifiedCommit {
			continue
		}
		counts := map[string]map[string]int{}
		pids := make([]string, 0, len(st.Projects))
		for pid := range st.Projects {
			pids = append(pids, pid)
		}
		sort.Strings(pids)
		for _, pid := range pids {
			ch := st.Projects[pid]
			if len(ch.Additions) == 0 && len(ch.Updates) == 0 {
				continue
			}
			counts[pid] = map[string]int{"additions": len(ch.Additions), "updates": len(ch.Updates)}
			c.alertsNotice(ctx, pid, "skillwatch:"+st.RegistryID+":"+st.LastCommit+":"+pid, skillUpdateNotice(st.Repo, st.RegistryID, ch), true)
		}
		if len(counts) > 0 {
			c.publish(ctx, skills.EventRegistryUpdates, "", map[string]any{"registry": st.RegistryID, "repo": st.Repo, "commit": st.LastCommit, "projects": counts})
		}
		if err := k.Skills.MarkNotified(ctx, st.RegistryID, st.LastCommit); err != nil {
			c.logf("skill registry %s: %v", st.RegistryID, err)
		}
	}
}

// skillUpdateNotice is the alerts message for one project: its first sentence is the Needs-you item's title.
func skillUpdateNotice(repo, registryID string, ch skills.RegistryProjectChanges) string {
	var parts []string
	if n := len(ch.Additions); n > 0 {
		parts = append(parts, fmt.Sprintf("%d new %s (%s)", n, plural(n, "skill", "skills"), names(ch.Additions)))
	}
	if n := len(ch.Updates); n > 0 {
		parts = append(parts, fmt.Sprintf("%d changed (%s)", n, names(ch.Updates)))
	}
	return fmt.Sprintf("Skill updates from %s (registry %s): %s. Open Skills to import them; each stays offered until you accept it.", repo, registryID, strings.Join(parts, ", "))
}

func names(ch []skills.RegistryChange) string {
	out := make([]string, 0, len(ch))
	for i, c := range ch {
		if i == 5 {
			out = append(out, fmt.Sprintf("and %d more", len(ch)-5))
			break
		}
		out = append(out, c.Name)
	}
	return strings.Join(out, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
