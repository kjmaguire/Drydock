package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/skills"
)

// Decisions P46, P48 and P49. Every enabled skill registry is synced into the workspace library two minutes after start and
// every twelve hours: one API call for the default branch, the tarball only when the commit moved. A project is told once per
// upstream commit what the sync brought and which of its own skills are now behind, through a notice and a Needs-you item in
// its alerts channel. Nothing a sync does reaches an agent: a project chooses from the library (skill.libraryAdd), and a project
// copy changes only when a person updates it. The extension also records when an agent opens a skill on demand (P49).
const (
	skillWatchFirstPass = 2 * time.Minute
	skillWatchInterval  = 12 * time.Hour
)

func init() {
	RegisterExtension(Extension{Name: "skillwatch", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		h := c.Human
		know := func() (*Knowledge, error) {
			k := c.Knowledge()
			if k == nil {
				return nil, fmt.Errorf("skills are not available")
			}
			return k, nil
		}
		m := map[string]Method{
			"skill.importAll": bind(func(ctx context.Context, p struct {
				ProjectID  string `json:"projectId"`
				RegistryID string `json:"registryId"`
				Ref        string `json:"ref"`
			}) (any, error) {
				k, err := know()
				if err != nil {
					return nil, err
				}
				res, err := k.Skills.ImportAll(ctx, h(), p.ProjectID, p.RegistryID, p.Ref)
				if err == nil {
					k.audit(ctx, "skill.import_all", p.RegistryID, map[string]any{"project": p.ProjectID, "commit": res.Commit, "imported": len(res.Imported), "updated": len(res.Updated), "blocked": len(res.Blocked)})
				}
				return res, err
			}),
			"skill.libraryList": bind(func(ctx context.Context, p struct {
				RegistryID string `json:"registryId"`
			}) (any, error) {
				k, err := know()
				if err != nil {
					return nil, err
				}
				return k.Skills.LibraryList(ctx, p.RegistryID)
			}),
			"skill.libraryAdd": bind(func(ctx context.Context, p struct {
				ProjectID  string   `json:"projectId"`
				RegistryID string   `json:"registryId"`
				Name       string   `json:"name"`
				Audience   string   `json:"audience"`
				Agents     []string `json:"agents"`
			}) (any, error) {
				k, err := know()
				if err != nil {
					return nil, err
				}
				s, err := k.Skills.AddFromLibrary(ctx, h(), p.ProjectID, p.RegistryID, p.Name, p.Audience, p.Agents)
				if err == nil {
					k.audit(ctx, "skill.library_add", p.Name, map[string]any{"project": p.ProjectID, "registry": p.RegistryID, "commit": s.PinnedCommit, "hash": s.ContentHash})
				}
				return s, err
			}),
			"skill.libraryAddAll": bind(func(ctx context.Context, p struct {
				ProjectID  string `json:"projectId"`
				RegistryID string `json:"registryId"`
			}) (any, error) {
				k, err := know()
				if err != nil {
					return nil, err
				}
				res, err := k.Skills.AddAllFromLibrary(ctx, h(), p.ProjectID, p.RegistryID)
				if err == nil {
					k.audit(ctx, "skill.library_add_all", p.RegistryID, map[string]any{"project": p.ProjectID, "added": len(res.Added), "unchanged": len(res.Unchanged), "blocked": len(res.Blocked), "skipped": len(res.Skipped)})
				}
				return res, err
			}),
			"skill.registryStatus": bind(func(ctx context.Context, _ struct{}) (any, error) {
				k, err := know()
				if err != nil {
					return nil, err
				}
				return k.Skills.RegistryStatuses(ctx)
			}),
			// skill.registryCheck syncs one registry (or every enabled one) into the library now, tells the projects what is
			// new, and returns the statuses. Human only: it contacts the network.
			"skill.registryCheck": bind(func(ctx context.Context, p struct {
				RegistryID string `json:"registryId"`
			}) (any, error) {
				k, err := know()
				if err != nil {
					return nil, err
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
			"skill.usage": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				k, err := know()
				if err != nil {
					return nil, err
				}
				return k.Skills.Usage(ctx, p.ProjectID)
			}),
		}
		ctx, cancel := context.WithCancel(ctx)
		// P49: an agent opening a skill it had on demand (Claude reading its file under skills.OnDemandDir) is a use
		ch, unsub := c.Bus.Subscribe(events.Filter{Types: []string{"agent.output"}})
		go func() {
			for ev := range ch {
				if name := skillReadFromOutput(ev.Data); name != "" && ev.ProjectID != "" {
					if k := c.Knowledge(); k != nil {
						k.Skills.RecordUse(ctx, skills.Target{ProjectID: ev.ProjectID, TaskID: ev.TaskID, AgentID: ev.AgentID}, ev.RunID, name, skills.UseFileRead)
					}
				}
			}
		}()
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
					c.logf("skill registry sync: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
		return m, func() { unsub(); cancel() }, nil
	}})
}

// skillReadFromOutput returns the skill name when an agent.output payload is a Read of a skill's on-demand file.
func skillReadFromOutput(data json.RawMessage) string {
	var d struct {
		Kind string `json:"kind"`
		Data struct {
			Name string `json:"name"`
			Args struct {
				FilePath string `json:"file_path"`
			} `json:"args"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &d) != nil || d.Kind != "tool_call" || d.Data.Name != "Read" || d.Data.Args.FilePath == "" {
		return ""
	}
	dir, file := path.Split(strings.ReplaceAll(filepath.ToSlash(d.Data.Args.FilePath), "\\", "/")) // a Windows path from a run on the PC
	inDir := dir == skills.OnDemandDir+"/" || strings.HasSuffix(dir, "/"+skills.OnDemandDir+"/")
	if !inDir || !strings.HasSuffix(file, ".md") {
		return ""
	}
	name := strings.TrimSuffix(file, ".md")
	if skills.ValidateName(name) != nil {
		return ""
	}
	return name
}

// RunSkillWatch is one scheduled pass: sync every enabled registry into the library and tell each project what is new.
func (c *Core) RunSkillWatch(ctx context.Context) ([]skills.RegistryStatus, error) {
	k := c.Knowledge()
	if k == nil {
		return nil, nil
	}
	sts, err := k.Skills.CheckAll(ctx)
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
// item in every project that has skills from it (what the sync added to the library, and which of the project's skills are
// behind), publishes skill.registry_updates, and marks the commit told. A sync that brought nothing is marked told without a
// word; a project with no skills from the registry is not told either (the library page shows the news).
func (c *Core) tellSkillUpdates(ctx context.Context, sts []skills.RegistryStatus) {
	k := c.Knowledge()
	for _, st := range sts {
		if st.LastError != "" || st.LastCommit == "" || st.LastCommit == st.NotifiedCommit {
			continue
		}
		counts := map[string]map[string]int{}
		if st.HasNews() {
			pids := make([]string, 0, len(st.Projects))
			for pid := range st.Projects {
				pids = append(pids, pid)
			}
			sort.Strings(pids)
			for _, pid := range pids {
				ch := st.Projects[pid]
				if len(st.Added) == 0 && len(st.Changed) == 0 && len(ch.Updates) == 0 {
					continue
				}
				counts[pid] = map[string]int{"added": len(st.Added), "changed": len(st.Changed), "updates": len(ch.Updates)}
				c.alertsNotice(ctx, pid, "skillwatch:"+st.RegistryID+":"+st.LastCommit+":"+pid, skillUpdateNotice(st, ch), true)
			}
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
func skillUpdateNotice(st skills.RegistryStatus, ch skills.RegistryProjectChanges) string {
	var parts []string
	if n := len(st.Added); n > 0 {
		parts = append(parts, fmt.Sprintf("%d new %s in the Library (%s)", n, plural(n, "skill", "skills"), names(st.Added)))
	}
	if n := len(st.Changed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d changed upstream (%s)", n, names(st.Changed)))
	}
	if n := len(ch.Updates); n > 0 {
		parts = append(parts, fmt.Sprintf("%d of this project's skills %s behind (%s)", n, plural(n, "is", "are"), names(ch.Updates)))
	}
	return fmt.Sprintf("Skill updates from %s (registry %s): %s. Open Skills to add or update them; nothing changes until you do.", st.Repo, st.RegistryID, strings.Join(parts, "; "))
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
