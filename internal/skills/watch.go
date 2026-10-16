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

	"github.com/kjmaguire/drydock/internal/security"
)

// Decisions P46 and P48: every enabled registry is read twice a day (CheckRegistry; the core schedules it) and what it holds
// goes into the workspace library (library.go). A check also remembers what the sync brought (added, changed), so the page and
// the notice can say it, and RegistryStatuses computes, against each project's skills as they are now, which project skills
// are behind the library. Nothing a check does reaches an agent: a project copy changes only when a person updates it.

// EventRegistryUpdates is published by the core when a check brought something new for the projects.
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

// ImportAll fetches a registry once and imports every skill it holds into the project as OFFERED, each pinned to the same
// commit and validated like Import does (the network route; the Skills page adds from the library instead, P48). A registry
// marked selective_only is refused. Human only.
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

// RegistryChange names a skill a sync added or changed, or a project skill that is behind the library.
type RegistryChange struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// RegistryProjectChanges is what a project has from a registry at an older text than the library (Updates).
type RegistryProjectChanges struct {
	Updates []RegistryChange `json:"updates"`
}

// RegistryStatus is a registry's sync state and what its last sync brought.
type RegistryStatus struct {
	RegistryID string     `json:"registryId"`
	Repo       string     `json:"repo"`
	Enabled    bool       `json:"enabled"`
	LastCommit string     `json:"lastCommit"`
	CheckedAt  *time.Time `json:"checkedAt,omitempty"`
	// NotifiedCommit is the commit whose changes were already told to the projects.
	NotifiedCommit string `json:"notifiedCommit,omitempty"`
	LastError      string `json:"lastError,omitempty"`
	// Skills and Blocked count the library's rows for this registry (gone ones excluded).
	Skills  int `json:"skills"`
	Blocked int `json:"blocked"`
	// Added and Changed are what the last sync that found a new commit brought into the library.
	Added   []RegistryChange `json:"added"`
	Changed []RegistryChange `json:"changed"`
	// Projects holds, for every project that has skills from this registry, the ones behind the library.
	Projects map[string]RegistryProjectChanges `json:"projects"`
}

// HasNews reports whether the last sync added or changed something, or any project is behind.
func (st RegistryStatus) HasNews() bool {
	if len(st.Added) > 0 || len(st.Changed) > 0 {
		return true
	}
	for _, ch := range st.Projects {
		if len(ch.Updates) > 0 {
			return true
		}
	}
	return false
}

type watchRow struct {
	commit, notified, lastErr string
	checkedAt                 int64
	added, changed            []RegistryChange
}

type watchChanges struct {
	Added   []RegistryChange `json:"added"`
	Changed []RegistryChange `json:"changed"`
}

func (s *Service) watchRow(ctx context.Context, registryID string) (watchRow, bool, error) {
	var r watchRow
	var changesJSON string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT last_commit,checked_at,notified_commit,remote_json,last_error FROM skill_registry_watch WHERE registry_id=?`, registryID).
		Scan(&r.commit, &r.checkedAt, &r.notified, &changesJSON, &r.lastErr)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	var ch watchChanges
	if changesJSON != "" {
		_ = json.Unmarshal([]byte(changesJSON), &ch)
	}
	r.added, r.changed = nz(ch.Added), nz(ch.Changed)
	return r, true, nil
}

// saveWatch stores the row; the column remote_json (named before P48) holds what the last sync added and changed.
func (s *Service) saveWatch(ctx context.Context, registryID string, r watchRow) error {
	changesJSON, _ := json.Marshal(watchChanges{Added: nz(r.added), Changed: nz(r.changed)})
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO skill_registry_watch(registry_id,last_commit,checked_at,notified_commit,remote_json,last_error) VALUES(?,?,?,?,?,?)
			ON CONFLICT(registry_id) DO UPDATE SET last_commit=excluded.last_commit,checked_at=excluded.checked_at,notified_commit=excluded.notified_commit,remote_json=excluded.remote_json,last_error=excluded.last_error`,
			registryID, r.commit, r.checkedAt, r.notified, string(changesJSON), r.lastErr)
		return err
	})
}

// CheckRegistry reads a registry's default branch once and syncs the library with what it holds. When the commit has not
// moved since the last check nothing is downloaded. A failure (the registry unreachable, the tarball unreadable) is recorded
// in LastError and returned as a status, not an error, so a scheduled pass continues with the next registry.
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
	fail := func(e error) (RegistryStatus, error) {
		row.lastErr = truncate(e.Error(), 300)
		if err := s.saveWatch(ctx, registryID, row); err != nil {
			return RegistryStatus{}, err
		}
		return s.status(ctx, reg)
	}
	commit, err := s.Fetcher.Resolve(ctx, reg.Repo, "")
	if err != nil {
		return fail(err)
	}
	var libCount int
	_ = s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_library WHERE registry_id=?`, reg.ID).Scan(&libCount)
	if commit != row.commit || libCount == 0 {
		remote, err := s.Fetcher.Fetch(ctx, reg.Repo, commit, reg.Root)
		if err != nil {
			return fail(err)
		}
		sync, err := s.syncLibrary(ctx, reg, commit, remote)
		if err != nil {
			return RegistryStatus{}, err
		}
		row.commit, row.added, row.changed = commit, sync.added, sync.changed
		if len(sync.added) > 0 || len(sync.changed) > 0 || sync.removedUpstream > 0 {
			s.emit(ctx, EventLibrarySynced, "", "", "", map[string]any{"registry": reg.ID, "repo": reg.Repo, "commit": commit, "added": len(sync.added), "changed": len(sync.changed), "gone": sync.removedUpstream, "skills": sync.count, "blocked": sync.blocked})
		}
	}
	row.lastErr = ""
	if err := s.saveWatch(ctx, registryID, row); err != nil {
		return RegistryStatus{}, err
	}
	return s.status(ctx, reg)
}

// CheckAll checks every enabled registry (the scheduled pass). One registry's failure is in its status; a database error
// stops the pass.
func (s *Service) CheckAll(ctx context.Context) ([]RegistryStatus, error) {
	regs, err := s.Registries(ctx)
	if err != nil {
		return nil, err
	}
	out := []RegistryStatus{}
	for _, reg := range regs {
		if !reg.Enabled {
			continue
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		st, err := s.CheckRegistry(ctx, reg.ID)
		if err != nil {
			return out, err
		}
		out = append(out, st)
	}
	return out, nil
}

// MarkNotified records that the news at commit was told to the projects, so it is not told again.
func (s *Service) MarkNotified(ctx context.Context, registryID, commit string) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE skill_registry_watch SET notified_commit=? WHERE registry_id=?`, commit, registryID)
		return err
	})
}

// RegistryStatuses returns the state of every registry (disabled ones included, so the page can show them), with each
// project's skills compared to the library as they are now.
func (s *Service) RegistryStatuses(ctx context.Context) ([]RegistryStatus, error) {
	regs, err := s.Registries(ctx)
	if err != nil {
		return nil, err
	}
	out := []RegistryStatus{}
	for _, reg := range regs {
		st, err := s.status(ctx, reg)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *Service) status(ctx context.Context, reg Registry) (RegistryStatus, error) {
	st := RegistryStatus{RegistryID: reg.ID, Repo: reg.Repo, Enabled: reg.Enabled, Added: []RegistryChange{}, Changed: []RegistryChange{}, Projects: map[string]RegistryProjectChanges{}}
	if err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN status='blocked' THEN 1 ELSE 0 END),0) FROM skill_library WHERE registry_id=? AND gone=0`, reg.ID).Scan(&st.Skills, &st.Blocked); err != nil {
		return st, err
	}
	row, ok, err := s.watchRow(ctx, reg.ID)
	if err != nil {
		return st, err
	}
	if ok {
		st.LastCommit, st.NotifiedCommit, st.LastError, st.Added, st.Changed = row.commit, row.notified, row.lastErr, row.added, row.changed
		if row.checkedAt > 0 {
			t := time.UnixMilli(row.checkedAt).UTC()
			st.CheckedAt = &t
		}
	}
	// project skills from this registry whose text is behind the library's
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT k.project_id,k.name,l.description FROM skills k JOIN skill_library l ON l.registry_id=k.registry_id AND l.name=k.name
		WHERE k.origin='registry' AND k.registry_id=? AND l.gone=0 AND l.status='available' AND l.content_hash<>k.content_hash ORDER BY k.project_id,k.name`, reg.ID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, name, desc string
		if err := rows.Scan(&pid, &name, &desc); err != nil {
			return st, err
		}
		ch := st.Projects[pid]
		if ch.Updates == nil {
			ch.Updates = []RegistryChange{}
		}
		ch.Updates = append(ch.Updates, RegistryChange{name, desc})
		st.Projects[pid] = ch
	}
	return st, rows.Err()
}
