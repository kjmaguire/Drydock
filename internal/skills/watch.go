package skills

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/security"
)

// Decision P46: a registry is imported whole and watched afterwards. ImportAll brings every valid skill of a registry into a
// project as OFFERED (a person still accepts each one); CheckRegistry reads the registry again (the core does it twice a day
// for every registry a project imports from) and remembers what the registry holds, so RegistryStatuses can say, against
// the project's current skills, which skills are new there and which changed upstream. Nothing is imported or accepted by a
// check: it only tells.

// EventRegistryUpdates is published by the core when a check finds new or changed skills for a project.
const EventRegistryUpdates = "skill.registry_updates"

// NamedReason is a skill name with why it was not imported.
type NamedReason struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ImportAllResult is what ImportAll did, by skill name.
type ImportAllResult struct {
	Commit    string        `json:"commit"`
	Imported  []string      `json:"imported"`  // new in the project, now offered
	Updated   []string      `json:"updated"`   // already in the project, its text changed (an accepted one is offered again)
	Unchanged []string      `json:"unchanged"` // already in the project with the same text (the pin follows the commit)
	Blocked   []NamedReason `json:"blocked"`   // stored as blocked (unlicensed, a secret, injection) or not a valid skill
	Skipped   []NamedReason `json:"skipped"`   // not touched: the repository's own skill wins, or the name is ambiguous
}

// ImportAll fetches a registry once and imports every skill it holds into the project, each pinned to the same commit and
// validated like Import does. A registry marked selective_only (the Microsoft repository: 175 skills, and its own warning
// about loading them all) is refused; its skills are imported one at a time. Human only.
func (s *Service) ImportAll(ctx context.Context, by, projectID, registryID, ref string) (ImportAllResult, error) {
	res := ImportAllResult{Imported: []string{}, Updated: []string{}, Unchanged: []string{}, Blocked: []NamedReason{}, Skipped: []NamedReason{}}
	if err := security.RequireHuman(by); err != nil {
		return res, err
	}
	var n int
	if err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, projectID).Scan(&n); err != nil || n == 0 {
		return res, fmt.Errorf("skills: project %s not found", projectID)
	}
	reg, err := s.registry(ctx, registryID)
	if err != nil {
		return res, err
	}
	if reg.Import == "selective_only" {
		note := strings.TrimSpace(reg.Notes)
		if note != "" {
			note = " (" + note + ")"
		}
		return res, fmt.Errorf("skills: %s asks for selective import%s; pick its skills one at a time", reg.Repo, note)
	}
	reg, commit, remote, err := s.fetchRegistry(ctx, registryID, ref)
	if err != nil {
		return res, err
	}
	res.Commit = commit
	groups := map[string][]RemoteSkill{}
	for _, rs := range remote {
		groups[rs.Name] = append(groups[rs.Name], rs)
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		group := groups[name]
		if len(group) > 1 {
			var dirs []string
			for _, m := range group {
				dirs = append(dirs, m.Dir)
			}
			sort.Strings(dirs)
			res.Skipped = append(res.Skipped, NamedReason{name, "ambiguous in " + reg.ID + " (" + strings.Join(dirs, ", ") + ")"})
			continue
		}
		if err := ValidateName(name); err != nil {
			res.Blocked = append(res.Blocked, NamedReason{name, "not a valid skill: " + err.Error()})
			continue
		}
		before, getErr := s.Get(ctx, projectID, name)
		existed := getErr == nil
		if existed && before.Origin == OriginRepo {
			res.Skipped = append(res.Skipped, NamedReason{name, "the repository already has a skill with this name; the repository's own skill wins"})
			continue
		}
		rs := group[0]
		rec, err := s.evaluateRemote(ctx, reg, commit, rs)
		if err != nil {
			var blockedErr *invalidError
			if errors.As(err, &blockedErr) {
				s.emit(ctx, EventBlocked, projectID, "", "", map[string]any{"skill": name, "registry": reg.ID, "commit": commit, "reason": blockedErr.Error()})
				res.Blocked = append(res.Blocked, NamedReason{name, blockedErr.Error()})
				continue
			}
			return res, err
		}
		k, changed, err := s.upsert(ctx, projectID, rec)
		if err != nil {
			return res, err
		}
		if changed {
			s.emitImported(ctx, projectID, by, reg, commit, rs, k)
		}
		switch {
		case k.Status == StatusBlocked:
			res.Blocked = append(res.Blocked, NamedReason{name, k.BlockReason})
		case !existed:
			res.Imported = append(res.Imported, name)
		case before.ContentHash != k.ContentHash:
			res.Updated = append(res.Updated, name)
		default:
			res.Unchanged = append(res.Unchanged, name) // the same text (the pin may have moved with the commit)
		}
	}
	return res, nil
}

// emitImported publishes the events of one imported (or re-imported) registry skill.
func (s *Service) emitImported(ctx context.Context, projectID, by string, reg Registry, commit string, rs RemoteSkill, k Skill) {
	s.emit(ctx, EventResolved, projectID, "", "", map[string]any{"skill": k.Name, "origin": OriginRegistry, "registry": reg.ID, "path": rs.Dir, "commit": commit, "status": k.Status})
	s.emit(ctx, EventImported, projectID, "", "", map[string]any{"skill": k.Name, "registry": reg.ID, "commit": commit, "status": k.Status, "scripts_skipped": rs.HasScripts, "by": by})
	if k.Status == StatusBlocked {
		s.emit(ctx, EventBlocked, projectID, "", "", map[string]any{"skill": k.Name, "registry": reg.ID, "commit": commit, "reason": k.BlockReason})
	} else {
		s.emit(ctx, EventOffered, projectID, "", "", map[string]any{"skill": k.Name, "registry": reg.ID, "commit": commit})
	}
}

// RegistryChange names a skill a check found new or changed for a project.
type RegistryChange struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// RegistryProjectChanges is what a registry holds that a project does not have yet (Additions) or has at an older text
// (Updates), as of the registry's last check and the project's skills now.
type RegistryProjectChanges struct {
	Additions []RegistryChange `json:"additions"`
	Updates   []RegistryChange `json:"updates"`
}

// RegistryStatus is a registry's watch state. Projects holds the changes for every project that imports from it.
type RegistryStatus struct {
	RegistryID string `json:"registryId"`
	Repo       string `json:"repo"`
	// Watched: an enabled registry at least one project imported from; only those are checked on the schedule.
	Watched        bool                              `json:"watched"`
	LastCommit     string                            `json:"lastCommit"`
	CheckedAt      *time.Time                        `json:"checkedAt,omitempty"`
	NotifiedCommit string                            `json:"notifiedCommit,omitempty"`
	LastError      string                            `json:"lastError,omitempty"`
	Projects       map[string]RegistryProjectChanges `json:"projects"`
}

// HasChanges reports whether any project has an addition or an update.
func (st RegistryStatus) HasChanges() bool {
	for _, ch := range st.Projects {
		if len(ch.Additions) > 0 || len(ch.Updates) > 0 {
			return true
		}
	}
	return false
}

// remoteSkillSnapshot is what a check remembers about one skill of a registry: enough to compare without the text.
type remoteSkillSnapshot struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hash        string `json:"hash"`
}

type watchRow struct {
	commit, notified, lastErr string
	checkedAt                 int64
	remote                    []remoteSkillSnapshot
}

func (s *Service) watchRow(ctx context.Context, registryID string) (watchRow, bool, error) {
	var r watchRow
	var remoteJSON string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT last_commit,checked_at,notified_commit,remote_json,last_error FROM skill_registry_watch WHERE registry_id=?`, registryID).
		Scan(&r.commit, &r.checkedAt, &r.notified, &remoteJSON, &r.lastErr)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	if remoteJSON != "" {
		_ = json.Unmarshal([]byte(remoteJSON), &r.remote)
	}
	return r, true, nil
}

func (s *Service) saveWatch(ctx context.Context, registryID string, r watchRow) error {
	remoteJSON, _ := json.Marshal(nz(r.remote))
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO skill_registry_watch(registry_id,last_commit,checked_at,notified_commit,remote_json,last_error) VALUES(?,?,?,?,?,?)
			ON CONFLICT(registry_id) DO UPDATE SET last_commit=excluded.last_commit,checked_at=excluded.checked_at,notified_commit=excluded.notified_commit,remote_json=excluded.remote_json,last_error=excluded.last_error`,
			registryID, r.commit, r.checkedAt, r.notified, string(remoteJSON), r.lastErr)
		return err
	})
}

// watchedRegistryIDs are the enabled registries at least one project imported from.
func (s *Service) watchedRegistryIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT DISTINCT k.registry_id FROM skills k JOIN skill_registries r ON r.id=k.registry_id WHERE k.origin='registry' AND r.enabled=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// CheckRegistry reads a registry's default branch once and remembers the skills it holds at that commit. When the commit
// has not moved since the last check the tarball is not fetched again. A failure (the registry unreachable, the tarball
// unreadable) is recorded in LastError and returned as a status, not an error, so a scheduled pass continues with the next
// registry. Nothing is imported.
func (s *Service) CheckRegistry(ctx context.Context, registryID string) (RegistryStatus, error) {
	reg, err := s.registry(ctx, registryID)
	if err != nil {
		return RegistryStatus{}, err
	}
	if !reg.Enabled {
		return RegistryStatus{}, fmt.Errorf("skills: registry %s is disabled", registryID)
	}
	if s.Fetcher == nil {
		return RegistryStatus{}, errors.New("skills: registry import is not configured")
	}
	row, _, err := s.watchRow(ctx, registryID)
	if err != nil {
		return RegistryStatus{}, err
	}
	row.checkedAt = s.now().UnixMilli()
	commit, err := s.Fetcher.Resolve(ctx, reg.Repo, "")
	if err != nil {
		row.lastErr = truncate(err.Error(), 300)
		if e := s.saveWatch(ctx, registryID, row); e != nil {
			return RegistryStatus{}, e
		}
		return s.status(ctx, reg, true)
	}
	if commit != row.commit || len(row.remote) == 0 {
		remote, err := s.Fetcher.Fetch(ctx, reg.Repo, commit, reg.Root)
		if err != nil {
			row.lastErr = truncate(err.Error(), 300)
			if e := s.saveWatch(ctx, registryID, row); e != nil {
				return RegistryStatus{}, e
			}
			return s.status(ctx, reg, true)
		}
		row.commit, row.remote = commit, snapshot(remote)
	}
	row.lastErr = ""
	if err := s.saveWatch(ctx, registryID, row); err != nil {
		return RegistryStatus{}, err
	}
	return s.status(ctx, reg, true)
}

// snapshot keeps the valid, unambiguous skills of a registry read: name, description and the hash of SKILL.md.
func snapshot(remote []RemoteSkill) []remoteSkillSnapshot {
	count := map[string]int{}
	for _, rs := range remote {
		count[rs.Name]++
	}
	var out []remoteSkillSnapshot
	for _, rs := range remote {
		if count[rs.Name] != 1 || rs.SkillMD == nil || ValidateName(rs.Name) != nil {
			continue
		}
		ps, err := ParseRegistrySkill(rs.SkillMD, rs.Name)
		if err != nil {
			continue
		}
		out = append(out, remoteSkillSnapshot{Name: rs.Name, Description: ps.Description, Hash: packs.HashHex(string(rs.SkillMD))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CheckWatched checks every watched registry (the scheduled pass). One registry's failure is in its status; a database
// error stops the pass.
func (s *Service) CheckWatched(ctx context.Context) ([]RegistryStatus, error) {
	watched, err := s.watchedRegistryIDs(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(watched))
	for id := range watched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := []RegistryStatus{}
	for _, id := range ids {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		st, err := s.CheckRegistry(ctx, id)
		if err != nil {
			return out, err
		}
		out = append(out, st)
	}
	return out, nil
}

// MarkNotified records that the changes at commit were told to the projects, so they are not told again.
func (s *Service) MarkNotified(ctx context.Context, registryID, commit string) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE skill_registry_watch SET notified_commit=? WHERE registry_id=?`, commit, registryID)
		return err
	})
}

// RegistryStatuses returns the watch state of every enabled registry, with the changes computed against each project's
// skills as they are now (an addition disappears once the project imports it).
func (s *Service) RegistryStatuses(ctx context.Context) ([]RegistryStatus, error) {
	regs, err := s.Registries(ctx)
	if err != nil {
		return nil, err
	}
	watched, err := s.watchedRegistryIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := []RegistryStatus{}
	for _, reg := range regs {
		if !reg.Enabled {
			continue
		}
		st, err := s.status(ctx, reg, watched[reg.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *Service) status(ctx context.Context, reg Registry, watched bool) (RegistryStatus, error) {
	st := RegistryStatus{RegistryID: reg.ID, Repo: reg.Repo, Watched: watched, Projects: map[string]RegistryProjectChanges{}}
	row, ok, err := s.watchRow(ctx, reg.ID)
	if err != nil {
		return st, err
	}
	if !ok {
		return st, nil
	}
	st.LastCommit, st.NotifiedCommit, st.LastError = row.commit, row.notified, row.lastErr
	if row.checkedAt > 0 {
		t := time.UnixMilli(row.checkedAt).UTC()
		st.CheckedAt = &t
	}
	if len(row.remote) == 0 {
		return st, nil
	}
	// every skill of every project that imports from this registry, and the names each of those projects already has
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT project_id,name,origin,registry_id,content_hash FROM skills
		WHERE project_id IN (SELECT DISTINCT project_id FROM skills WHERE origin='registry' AND registry_id=?)`, reg.ID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	type have struct{ origin, registry, hash string }
	projects := map[string]map[string]have{}
	for rows.Next() {
		var pid, name, origin, rid, hash string
		if err := rows.Scan(&pid, &name, &origin, &rid, &hash); err != nil {
			return st, err
		}
		if projects[pid] == nil {
			projects[pid] = map[string]have{}
		}
		projects[pid][name] = have{origin, rid, hash}
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	for pid, skillsOf := range projects {
		ch := RegistryProjectChanges{Additions: []RegistryChange{}, Updates: []RegistryChange{}}
		for _, r := range row.remote {
			h, has := skillsOf[r.Name]
			switch {
			case !has:
				ch.Additions = append(ch.Additions, RegistryChange{r.Name, r.Description})
			case h.origin == OriginRegistry && h.registry == reg.ID && h.hash != r.Hash:
				ch.Updates = append(ch.Updates, RegistryChange{r.Name, r.Description})
			}
		}
		st.Projects[pid] = ch
	}
	return st, nil
}
