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

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/security"
)

// Decision P48: the workspace skill library. Drydock itself imports every skill of every enabled registry into
// skill_library (at start and twice a day, SyncRegistry), validated and scanned like a project import; a project then only
// chooses: AddFromLibrary copies a library skill into the project as ACCEPTED, because the person's choice is the acceptance
// (the rule of P27). Nothing in the library reaches an agent; only a project's accepted skills do.

// EventLibrarySynced is published after a registry sync that brought something new into the library.
const EventLibrarySynced = "skill.library_synced"

// Library statuses.
const (
	LibraryAvailable = "available"
	LibraryBlocked   = "blocked"
)

// LibrarySkill is a registry skill as the library holds it (Body is filled by libraryRow only).
type LibrarySkill struct {
	ID           string    `json:"id"`
	RegistryID   string    `json:"registryId"`
	Repo         string    `json:"repo"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	License      string    `json:"license"`
	Globs        []string  `json:"globs"`
	Roles        []string  `json:"roles"`
	SourcePath   string    `json:"sourcePath"`
	PinnedCommit string    `json:"pinnedCommit"`
	ContentHash  string    `json:"contentHash"`
	HasScripts   bool      `json:"hasScripts"`
	Status       string    `json:"status"`
	BlockReason  string    `json:"blockReason"`
	Risk         int       `json:"risk"`
	Flags        []string  `json:"flags"`
	Gone         bool      `json:"gone"`
	Updated      time.Time `json:"updated"`
	body         string
	compat       string
	notes        []string
	toolsIgnored bool
}

const libraryCols = `l.id,l.registry_id,r.repo,l.name,l.description,l.body,l.license,l.compatibility,l.globs,l.roles,l.source_path,l.pinned_commit,l.content_hash,l.has_scripts,l.allowed_tools_ignored,l.status,l.risk,l.flags,l.block_reason,l.parse_notes,l.gone,l.updated_at`

func scanLibrary(sc interface{ Scan(...any) error }) (LibrarySkill, error) {
	var k LibrarySkill
	var globs, roles, flags, notes string
	var scripts, ignored, gone int
	var updated int64
	if err := sc.Scan(&k.ID, &k.RegistryID, &k.Repo, &k.Name, &k.Description, &k.body, &k.License, &k.compat, &globs, &roles, &k.SourcePath, &k.PinnedCommit,
		&k.ContentHash, &scripts, &ignored, &k.Status, &k.Risk, &flags, &k.BlockReason, &notes, &gone, &updated); err != nil {
		return k, err
	}
	_ = json.Unmarshal([]byte(globs), &k.Globs)
	_ = json.Unmarshal([]byte(roles), &k.Roles)
	_ = json.Unmarshal([]byte(notes), &k.notes)
	k.Globs, k.Roles, k.notes = nz(k.Globs), nz(k.Roles), nz(k.notes)
	k.Flags = []string{}
	if flags != "" {
		k.Flags = strings.Split(flags, ",")
	}
	k.HasScripts, k.toolsIgnored, k.Gone = scripts == 1, ignored == 1, gone == 1
	k.Updated = time.UnixMilli(updated).UTC()
	return k, nil
}

// LibraryList lists the library (one registry, or every registry with registryID empty), by registry then name. Blocked and
// gone skills are listed too, marked, so the page can say why one cannot be added.
func (s *Service) LibraryList(ctx context.Context, registryID string) ([]LibrarySkill, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+libraryCols+` FROM skill_library l JOIN skill_registries r ON r.id=l.registry_id
		WHERE (?='' OR l.registry_id=?) ORDER BY l.registry_id, l.name`, registryID, registryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibrarySkill{}
	for rows.Next() {
		k, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		k.body = "" // the list never carries the text
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Service) libraryRow(ctx context.Context, registryID, name string) (LibrarySkill, error) {
	k, err := scanLibrary(s.DB.Reader().QueryRowContext(ctx, `SELECT `+libraryCols+` FROM skill_library l JOIN skill_registries r ON r.id=l.registry_id WHERE l.registry_id=? AND l.name=?`, registryID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return k, fmt.Errorf("%w: the library has no skill %s from %s", ErrNotFound, name, registryID)
	}
	return k, err
}

// librarySync is what one registry sync did.
type librarySync struct {
	added, changed  []RegistryChange
	count, blocked  int
	removedUpstream int
}

// syncLibrary writes what a registry holds at commit into the library: new skills are added, changed text replaces the old
// (the project copies keep theirs until a person updates them), unchanged ones only move their pin, and a skill the
// registry no longer holds is marked gone. Invalid, unlicensed or unsafe skills are stored blocked with the reason.
func (s *Service) syncLibrary(ctx context.Context, reg Registry, commit string, remote []RemoteSkill) (librarySync, error) {
	var res librarySync
	res.added, res.changed = []RegistryChange{}, []RegistryChange{}
	count := map[string]int{}
	for _, rs := range remote {
		count[rs.Name]++
	}
	seen := map[string]bool{}
	now := s.now().UnixMilli()
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, rs := range remote {
			if count[rs.Name] != 1 || ValidateName(rs.Name) != nil {
				continue // an ambiguous or unnameable directory is not a skill the library can hold
			}
			seen[rs.Name] = true
			rec, err := s.evaluateRemote(ctx, reg, commit, rs)
			var status, reason, body, desc, compat, license, hash, source string
			var globs, roles, notes []string
			var risk int
			var flags []string
			var scripts, ignored bool
			if err != nil {
				var bad *invalidError
				if !errors.As(err, &bad) {
					return err
				}
				status, reason = LibraryBlocked, bad.Error()
				hash = packs.HashHex(string(rs.SkillMD))
				source, scripts = rs.Dir, rs.HasScripts
			} else {
				status = LibraryAvailable
				if rec.status == StatusBlocked {
					status = LibraryBlocked
				}
				reason, hash, source, scripts, license, risk, flags = rec.reason, rec.hash, rec.source, rec.scripts, rec.license, rec.verdict.Risk, rec.verdict.Flags
				desc, compat, globs, roles, notes, ignored = rec.ps.Description, rec.ps.Compatibility, rec.ps.Globs, rec.ps.Roles, rec.ps.Notes(), rec.ps.AllowedTools != ""
				if !rec.verdict.SecretBlocked {
					body = string(rs.SkillMD) // the whole SKILL.md, so a project copy can be parsed from the library
				}
			}
			if status == LibraryBlocked {
				res.blocked++
			}
			res.count++
			g, _ := json.Marshal(nz(globs))
			r, _ := json.Marshal(nz(roles))
			n, _ := json.Marshal(nz(notes))
			var oldHash string
			e := tx.QueryRowContext(ctx, `SELECT content_hash FROM skill_library WHERE registry_id=? AND name=?`, reg.ID, rs.Name).Scan(&oldHash)
			switch {
			case errors.Is(e, sql.ErrNoRows):
				res.added = append(res.added, RegistryChange{rs.Name, desc})
				_, e = tx.ExecContext(ctx, `INSERT INTO skill_library(id,registry_id,name,description,body,license,compatibility,globs,roles,source_path,pinned_commit,content_hash,has_scripts,allowed_tools_ignored,status,risk,flags,block_reason,parse_notes,gone,created_at,updated_at)
					VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?,?)`,
					ids.New("lsk"), reg.ID, rs.Name, desc, body, license, compat, string(g), string(r), source, commit, hash, b2i(scripts), b2i(ignored), status, risk, strings.Join(flags, ","), reason, string(n), now, now)
			case e != nil:
				return e
			default:
				if oldHash != hash {
					res.changed = append(res.changed, RegistryChange{rs.Name, desc})
				}
				_, e = tx.ExecContext(ctx, `UPDATE skill_library SET description=?,body=?,license=?,compatibility=?,globs=?,roles=?,source_path=?,pinned_commit=?,content_hash=?,has_scripts=?,allowed_tools_ignored=?,status=?,risk=?,flags=?,block_reason=?,parse_notes=?,gone=0,updated_at=? WHERE registry_id=? AND name=?`,
					desc, body, license, compat, string(g), string(r), source, commit, hash, b2i(scripts), b2i(ignored), status, risk, strings.Join(flags, ","), reason, string(n), now, reg.ID, rs.Name)
			}
			if e != nil {
				return e
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT name FROM skill_library WHERE registry_id=? AND gone=0`, reg.ID)
		if err != nil {
			return err
		}
		var gone []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			if !seen[name] {
				gone = append(gone, name)
			}
		}
		rows.Close()
		for _, name := range gone {
			if _, err := tx.ExecContext(ctx, `UPDATE skill_library SET gone=1,updated_at=? WHERE registry_id=? AND name=?`, now, reg.ID, name); err != nil {
				return err
			}
			res.removedUpstream++
		}
		return nil
	})
	sort.Slice(res.added, func(i, j int) bool { return res.added[i].Name < res.added[j].Name })
	sort.Slice(res.changed, func(i, j int) bool { return res.changed[i].Name < res.changed[j].Name })
	return res, err
}

// AddFromLibrary copies a library skill into a project as ACCEPTED: choosing it is the acceptance. A blocked or gone skill
// is refused, as is a name the repository's own skills already use. Adding a skill the project has at an older text updates
// it (the project copy follows the library). Human only.
func (s *Service) AddFromLibrary(ctx context.Context, by, projectID, registryID, name, audience string, agents []string) (Skill, error) {
	if err := security.RequireHuman(by); err != nil {
		return Skill{}, err
	}
	var n int
	if err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, projectID).Scan(&n); err != nil || n == 0 {
		return Skill{}, fmt.Errorf("skills: project %s not found", projectID)
	}
	l, err := s.libraryRow(ctx, registryID, name)
	if err != nil {
		return Skill{}, err
	}
	k, _, err := s.addLibrarySkill(ctx, by, projectID, l)
	if err != nil {
		return k, err
	}
	// the agents a person chose replace the ones the skill's roles suggested; with none chosen the suggestion stands (P76)
	if audience != "" || len(agents) > 0 {
		if audience == "" {
			audience = AudienceAgents
		}
		return s.SetAudience(ctx, by, projectID, name, audience, agents)
	}
	return k, nil
}

func (s *Service) addLibrarySkill(ctx context.Context, by, projectID string, l LibrarySkill) (Skill, bool, error) {
	switch {
	case l.Status == LibraryBlocked:
		return Skill{}, false, fmt.Errorf("skills: %s cannot be added: %s", l.Name, l.BlockReason)
	case l.Gone:
		return Skill{}, false, fmt.Errorf("skills: %s is no longer in %s", l.Name, l.Repo)
	}
	if existing, err := s.Get(ctx, projectID, l.Name); err == nil && existing.Origin == OriginRepo {
		return Skill{}, false, fmt.Errorf("skills: the repository already has a skill named %s; the repository's own skill wins", l.Name)
	}
	ps, err := ParseRegistrySkill([]byte(l.body), l.Name)
	if err != nil {
		return Skill{}, false, fmt.Errorf("skills: the library copy of %s does not parse: %w", l.Name, err)
	}
	rec := record{ps: ps, origin: OriginRegistry, registry: l.RegistryID, source: l.SourcePath, pinned: l.PinnedCommit, hash: l.ContentHash, scripts: l.HasScripts,
		verdict: Verdict{Risk: l.Risk, Flags: l.Flags}, license: l.License, status: StatusAccepted, decidedBy: by}
	before, getErr := s.Get(ctx, projectID, l.Name)
	k, changed, err := s.upsert(ctx, projectID, rec)
	if err != nil {
		return k, false, err
	}
	if k.Status != StatusAccepted {
		// a human decision on identical content stands (a rejected skill stays rejected); the person's add is a new decision
		k, err = s.decide(ctx, by, projectID, l.Name, StatusAccepted, EventAccepted, []string{StatusOffered, StatusRejected})
		if err != nil {
			return k, false, err
		}
		changed = true
	}
	if changed {
		s.emit(ctx, EventResolved, projectID, "", "", map[string]any{"skill": k.Name, "origin": OriginRegistry, "registry": l.RegistryID, "path": l.SourcePath, "commit": l.PinnedCommit, "status": k.Status})
		s.emit(ctx, EventImported, projectID, "", "", map[string]any{"skill": k.Name, "registry": l.RegistryID, "commit": l.PinnedCommit, "status": k.Status, "from": "library", "by": by})
		if getErr != nil || before.ContentHash != k.ContentHash {
			s.emit(ctx, EventAccepted, projectID, "", "", map[string]any{"skill": k.Name, "by": by, "from": "library"})
		}
	}
	return k, changed, nil
}

// LibraryAddAllResult is what AddAllFromLibrary did, by skill name.
type LibraryAddAllResult struct {
	Added     []string      `json:"added"`     // new in the project (or updated to the library's text), accepted
	Unchanged []string      `json:"unchanged"` // already in the project at this text
	Blocked   []NamedReason `json:"blocked"`   // blocked in the library: never added
	Skipped   []NamedReason `json:"skipped"`   // gone upstream, or the repository's own skill wins
}

// AddAllFromLibrary adds every available skill of a registry to a project, each accepted. Human only.
func (s *Service) AddAllFromLibrary(ctx context.Context, by, projectID, registryID string) (LibraryAddAllResult, error) {
	res := LibraryAddAllResult{Added: []string{}, Unchanged: []string{}, Blocked: []NamedReason{}, Skipped: []NamedReason{}}
	if err := security.RequireHuman(by); err != nil {
		return res, err
	}
	var n int
	if err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, projectID).Scan(&n); err != nil || n == 0 {
		return res, fmt.Errorf("skills: project %s not found", projectID)
	}
	if _, err := s.registry(ctx, registryID); err != nil {
		return res, err
	}
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+libraryCols+` FROM skill_library l JOIN skill_registries r ON r.id=l.registry_id WHERE l.registry_id=? ORDER BY l.name`, registryID)
	if err != nil {
		return res, err
	}
	var all []LibrarySkill
	for rows.Next() {
		k, err := scanLibrary(rows)
		if err != nil {
			rows.Close()
			return res, err
		}
		all = append(all, k)
	}
	rows.Close()
	for _, l := range all {
		switch {
		case l.Status == LibraryBlocked:
			res.Blocked = append(res.Blocked, NamedReason{l.Name, l.BlockReason})
			continue
		case l.Gone:
			res.Skipped = append(res.Skipped, NamedReason{l.Name, "no longer in " + l.Repo})
			continue
		}
		if existing, err := s.Get(ctx, projectID, l.Name); err == nil && existing.Origin == OriginRepo {
			res.Skipped = append(res.Skipped, NamedReason{l.Name, "the repository already has a skill with this name; the repository's own skill wins"})
			continue
		}
		_, changed, err := s.addLibrarySkill(ctx, by, projectID, l)
		if err != nil {
			return res, err
		}
		if changed {
			res.Added = append(res.Added, l.Name)
		} else {
			res.Unchanged = append(res.Unchanged, l.Name)
		}
	}
	return res, nil
}
