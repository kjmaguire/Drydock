package skills

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Event types (docs/expert-packs.md section 9, plus lifecycle extensions).
const (
	EventImported  = "skill.imported"
	EventResolved  = "skill.resolved"
	EventDelivered = "skill.delivered"
	EventBlocked   = "skill.blocked"
	// Extensions.
	EventOffered         = "skill.offered"
	EventAccepted        = "skill.accepted"
	EventRejected        = "skill.rejected"
	EventReleased        = "skill.released"
	EventRegistryChanged = "skill.registry_changed"
)

// Statuses.
const (
	StatusOffered  = "offered"
	StatusAccepted = "accepted"
	StatusRejected = "rejected"
	StatusBlocked  = "blocked"
)

// Origins.
const (
	OriginRepo     = "repo"
	OriginRegistry = "registry"
)

// maxRepoSkills bounds one repository scan.
const maxRepoSkills = 200

// SkillsDir is the repository directory holding a project's own skills.
const SkillsDir = ".aet/skills"

// Errors.
var (
	ErrNotFound = errors.New("skills: not found")
	ErrNotHuman = security.ErrNotHuman
)

// Service resolves, scans, stores and delivers skills.
type Service struct {
	DB      *storage.DB
	Bus     events.Bus
	Scanner security.SecretScanner // required
	// Fetcher reads registries. Nil: registry import is unavailable (repository skills still work).
	Fetcher RegistryFetcher
	Now     func() time.Time
	// ManualAcceptRepoSkills makes a repository scan only OFFER skills, even committed ones, until a human accepts them. By
	// default a skill that is committed and unmodified at HEAD and passes the scan is accepted automatically (the repository is
	// the project's own, and a change reaches it through the human's merge). A project that does not want an agent-authored
	// skill to become active through a merged diff sets this.
	ManualAcceptRepoSkills bool
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) emit(ctx context.Context, typ, projectID, taskID, agentID string, data any) {
	if s.Bus == nil {
		return
	}
	_, _ = events.Emit(ctx, s.Bus, typ, events.Event{ProjectID: projectID, TaskID: taskID, AgentID: agentID}, data)
}

// Skill is a stored skill. Body is filled by Get only.
type Skill struct {
	ID                  string    `json:"id"`
	ProjectID           string    `json:"projectId"`
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	License             string    `json:"license,omitempty"`
	Compatibility       string    `json:"compatibility,omitempty"`
	Globs               []string  `json:"globs"`
	Roles               []string  `json:"roles"`
	Origin              string    `json:"origin"`
	RegistryID          string    `json:"registryId,omitempty"`
	SourcePath          string    `json:"sourcePath,omitempty"`
	PinnedCommit        string    `json:"pinnedCommit,omitempty"`
	ContentHash         string    `json:"contentHash"`
	HasScripts          bool      `json:"hasScripts,omitempty"`
	AllowedToolsIgnored bool      `json:"allowedToolsIgnored,omitempty"`
	Status              string    `json:"status"`
	Risk                int       `json:"risk,omitempty"`
	Flags               []string  `json:"flags,omitempty"`
	BlockReason         string    `json:"blockReason,omitempty"`
	DecidedBy           string    `json:"decidedBy,omitempty"`
	DecidedAt           time.Time `json:"decidedAt,omitempty"`
	Created             time.Time `json:"created"`
	Updated             time.Time `json:"updated"`
	Body                string    `json:"body,omitempty"`
}

const skillCols = `id,project_id,name,description,body,license,compatibility,globs,roles,origin,registry_id,source_path,pinned_commit,content_hash,has_scripts,allowed_tools_ignored,status,risk,flags,block_reason,decided_by,decided_at,created_at,updated_at`

func scanSkill(sc interface{ Scan(...any) error }, withBody bool) (Skill, error) {
	var k Skill
	var body, globs, roles, flags string
	var scripts, ati int
	var decided, created, updated int64
	if err := sc.Scan(&k.ID, &k.ProjectID, &k.Name, &k.Description, &body, &k.License, &k.Compatibility, &globs, &roles, &k.Origin, &k.RegistryID, &k.SourcePath, &k.PinnedCommit,
		&k.ContentHash, &scripts, &ati, &k.Status, &k.Risk, &flags, &k.BlockReason, &k.DecidedBy, &decided, &created, &updated); err != nil {
		return k, err
	}
	_ = json.Unmarshal([]byte(globs), &k.Globs)
	_ = json.Unmarshal([]byte(roles), &k.Roles)
	if k.Globs == nil {
		k.Globs = []string{}
	}
	if k.Roles == nil {
		k.Roles = []string{}
	}
	if flags != "" {
		k.Flags = strings.Split(flags, ",")
	}
	k.HasScripts, k.AllowedToolsIgnored = scripts == 1, ati == 1
	if decided > 0 {
		k.DecidedAt = time.UnixMilli(decided).UTC()
	}
	k.Created, k.Updated = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	if withBody {
		k.Body = body
	}
	return k, nil
}

// List returns a project's skills by name; statuses filters (empty: all). Bodies are not included.
func (s *Service) List(ctx context.Context, projectID string, statuses ...string) ([]Skill, error) {
	q := `SELECT ` + skillCols + ` FROM skills WHERE project_id=?`
	args := []any{projectID}
	if len(statuses) > 0 {
		ph := make([]string, len(statuses))
		for i, st := range statuses {
			ph[i] = "?"
			args = append(args, st)
		}
		q += ` AND status IN (` + strings.Join(ph, ",") + `)`
	}
	rows, err := s.DB.Reader().QueryContext(ctx, q+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Skill{}
	for rows.Next() {
		k, err := scanSkill(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Get returns one skill with its body.
func (s *Service) Get(ctx context.Context, projectID, name string) (Skill, error) {
	k, err := scanSkill(s.DB.Reader().QueryRowContext(ctx, `SELECT `+skillCols+` FROM skills WHERE project_id=? AND name=?`, projectID, name), true)
	if errors.Is(err, sql.ErrNoRows) {
		return k, fmt.Errorf("%w: skill %s", ErrNotFound, name)
	}
	return k, err
}

// Eligible returns the accepted skills that apply to a role, with bodies, by name. This is the only set that is ever written
// into a worktree or served to an agent.
func (s *Service) Eligible(ctx context.Context, projectID, role string) ([]Skill, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+skillCols+` FROM skills WHERE project_id=? AND status='accepted' ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Skill
	for rows.Next() {
		k, err := scanSkill(rows, true)
		if err != nil {
			return nil, err
		}
		if RolesMatch(k.Roles, role) {
			out = append(out, k)
		}
	}
	return out, rows.Err()
}

// record is a skill ready to store.
type record struct {
	ps        *ParsedSkill
	origin    string
	registry  string
	source    string
	pinned    string
	hash      string
	scripts   bool
	verdict   Verdict
	license   string
	status    string
	decidedBy string
	reason    string
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// upsert writes a record. If a skill of that name exists with the same content hash and pin, its status is kept (a human
// decision survives re-scans); a changed skill goes back to its computed status.
func (s *Service) upsert(ctx context.Context, projectID string, r record) (Skill, bool, error) {
	now := s.now().UnixMilli()
	globs, _ := json.Marshal(nz(r.ps.Globs))
	roles, _ := json.Marshal(nz(r.ps.Roles))
	body := r.ps.Body
	if r.verdict.SecretBlocked {
		body = "" // never store text that contains a secret
	}
	var changed bool
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var oldHash, oldStatus, oldOrigin, oldPin, oldDecidedBy string
		var oldDecidedAt int64
		e := tx.QueryRowContext(ctx, `SELECT content_hash,status,origin,pinned_commit,decided_by,decided_at FROM skills WHERE project_id=? AND name=?`, projectID, r.ps.Name).
			Scan(&oldHash, &oldStatus, &oldOrigin, &oldPin, &oldDecidedBy, &oldDecidedAt)
		status, decidedBy, decidedAt := r.status, r.decidedBy, int64(0)
		if r.decidedBy != "" {
			decidedAt = now
		}
		switch {
		case errors.Is(e, sql.ErrNoRows):
			changed = true
			id := ids.New("skl")
			_, e = tx.ExecContext(ctx, `INSERT INTO skills(`+skillCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				id, projectID, r.ps.Name, r.ps.Description, body, r.license, r.ps.Compatibility, string(globs), string(roles), r.origin, r.registry, r.source, r.pinned,
				r.hash, b2i(r.scripts), b2i(r.ps.AllowedTools != ""), status, r.verdict.Risk, strings.Join(r.verdict.Flags, ","), r.reason, decidedBy, decidedAt, now, now)
			return e
		case e != nil:
			return e
		}
		sameContent := oldHash == r.hash && oldOrigin == r.origin
		humanDecided := security.IsHumanPrincipal(oldDecidedBy)
		if sameContent && (humanDecided || ((oldStatus == StatusAccepted || oldStatus == StatusRejected) && !r.verdict.Blocked)) {
			status, decidedBy, decidedAt = oldStatus, oldDecidedBy, oldDecidedAt // a human decision on identical content stands
		}
		changed = !(sameContent && oldPin == r.pinned)
		_, e = tx.ExecContext(ctx, `UPDATE skills SET description=?,body=?,license=?,compatibility=?,globs=?,roles=?,origin=?,registry_id=?,source_path=?,pinned_commit=?,content_hash=?,
			has_scripts=?,allowed_tools_ignored=?,status=?,risk=?,flags=?,block_reason=?,decided_by=?,decided_at=?,updated_at=? WHERE project_id=? AND name=?`,
			r.ps.Description, body, r.license, r.ps.Compatibility, string(globs), string(roles), r.origin, r.registry, r.source, r.pinned, r.hash,
			b2i(r.scripts), b2i(r.ps.AllowedTools != ""), status, r.verdict.Risk, strings.Join(r.verdict.Flags, ","), r.reason, decidedBy, decidedAt, now, projectID, r.ps.Name)
		return e
	})
	if err != nil {
		return Skill{}, false, err
	}
	k, err := s.Get(ctx, projectID, r.ps.Name)
	return k, changed, err
}

func nz[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ---- repository scan ----

// ScanResult summarizes a repository scan.
type ScanResult struct {
	Found    int      `json:"found"`
	Accepted int      `json:"accepted"`
	Offered  int      `json:"offered"`
	Blocked  int      `json:"blocked"`
	Removed  int      `json:"removed"`
	Errors   []string `json:"errors"`
}

// ScanRepo reads the project's own .aet/skills/ from its main checkout (never from an agent's worktree: an agent could write
// there) and stores what it finds. A skill that is committed and unmodified at HEAD, and passes the scan, is accepted
// automatically and pinned to the commit that last touched it; anything else (uncommitted, modified, or blocked by the scan)
// is not accepted until a human decides.
func (s *Service) ScanRepo(ctx context.Context, projectID string) (ScanResult, error) {
	res := ScanResult{Errors: []string{}}
	if s.Scanner == nil {
		return res, packs.ErrNoScanner
	}
	var repo string
	if err := s.DB.Reader().QueryRowContext(ctx, `SELECT repo_path FROM projects WHERE id=?`, projectID).Scan(&repo); err != nil {
		return res, fmt.Errorf("skills: project %s not found", projectID)
	}
	if repo == "" {
		return res, nil // no repository attached yet: there is nothing to scan (and a relative path would read the app's own folder)
	}
	root := filepath.Join(repo, filepath.FromSlash(SkillsDir))
	seen := map[string]bool{}
	st, err := os.Lstat(root)
	if err == nil && st.Mode()&os.ModeSymlink != 0 {
		res.Errors = append(res.Errors, SkillsDir+" is a symbolic link; it is not followed")
		err = os.ErrNotExist
	}
	if err == nil && st.IsDir() {
		ents, _ := os.ReadDir(root)
		for _, ent := range ents {
			name := ent.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if ent.Type()&os.ModeSymlink != 0 {
				res.Errors = append(res.Errors, name+": symbolic links are skipped")
				continue
			}
			var rel, file, want string
			hasScripts := false
			switch {
			case ent.IsDir():
				rel, want = SkillsDir+"/"+name, name
				file = filepath.Join(root, name, "SKILL.md")
				if sc, e := os.Lstat(filepath.Join(root, name, "scripts")); e == nil && sc.IsDir() {
					hasScripts = true
				}
			case strings.HasSuffix(name, ".md"):
				rel, want = SkillsDir+"/"+name, strings.TrimSuffix(name, ".md")
				file = filepath.Join(root, name)
			default:
				continue
			}
			if res.Found >= maxRepoSkills {
				res.Errors = append(res.Errors, fmt.Sprintf("more than %d skills: the rest are ignored", maxRepoSkills))
				break
			}
			res.Found++
			k, err := s.scanOne(ctx, projectID, repo, rel, file, want, hasScripts)
			if err != nil {
				res.Errors = append(res.Errors, name+": "+err.Error())
				res.Found--
				continue
			}
			seen[k.Name] = true
			switch k.Status {
			case StatusAccepted:
				res.Accepted++
			case StatusOffered:
				res.Offered++
			case StatusBlocked:
				res.Blocked++
			}
		}
	}
	// repository skills that vanished from the checkout stop being delivered
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT name FROM skills WHERE project_id=? AND origin='repo'`, projectID)
	if err != nil {
		return res, err
	}
	var gone []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return res, err
		}
		if !seen[n] {
			gone = append(gone, n)
		}
	}
	rows.Close()
	for _, n := range gone {
		if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM skills WHERE project_id=? AND name=? AND origin='repo'`, projectID, n)
			return err
		}); err != nil {
			return res, err
		}
		res.Removed++
	}
	return res, nil
}

func (s *Service) scanOne(ctx context.Context, projectID, repo, rel, file, want string, hasScripts bool) (Skill, error) {
	fi, err := os.Lstat(file)
	if err != nil {
		return Skill{}, errors.New("no SKILL.md")
	}
	if !fi.Mode().IsRegular() {
		return Skill{}, errors.New("SKILL.md is not a regular file")
	}
	if fi.Size() > MaxSkillBytes {
		return Skill{}, fmt.Errorf("SKILL.md is larger than %d KiB", MaxSkillBytes>>10)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return Skill{}, err
	}
	ps, err := ParseSkill(raw, want)
	if err != nil {
		return Skill{}, err
	}
	verdict, err := Scan(ctx, s.Scanner, string(raw))
	if err != nil {
		return Skill{}, err
	}
	commit, clean := gitState(ctx, repo, rel)
	rec := record{ps: ps, origin: OriginRepo, source: rel, pinned: commit, hash: packs.HashHex(string(raw)), scripts: hasScripts, verdict: verdict, license: ps.License}
	switch {
	case verdict.Blocked:
		rec.status, rec.reason = StatusBlocked, verdict.Reason
	case commit != "" && clean && !s.ManualAcceptRepoSkills:
		rec.status, rec.decidedBy = StatusAccepted, "system:committed"
	default:
		rec.status = StatusOffered
		rec.reason = "not committed at HEAD: a human accepts it, or commit it"
	}
	k, changed, err := s.upsert(ctx, projectID, rec)
	if err != nil {
		return k, err
	}
	if changed {
		s.emit(ctx, EventResolved, projectID, "", "", map[string]any{"skill": k.Name, "origin": OriginRepo, "path": rel, "commit": commit, "status": k.Status})
		if k.Status == StatusBlocked {
			s.emit(ctx, EventBlocked, projectID, "", "", map[string]any{"skill": k.Name, "reason": k.BlockReason})
		}
	}
	return k, nil
}

// gitState returns the commit that last touched rel and whether rel is tracked and unmodified. Both are empty/false when git
// is unavailable, the path is untracked or the repository has no commits. Hooks and fsmonitor are disabled.
func gitState(ctx context.Context, repo, rel string) (commit string, clean bool) {
	run := func(args ...string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		full := append([]string{"-c", "core.fsmonitor=false", "-c", "core.quotepath=false", "-C", repo}, args...)
		cmd := exec.CommandContext(cctx, "git", full...)
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	c, err := run("log", "-1", "--format=%H", "--", rel)
	if err != nil || c == "" {
		return "", false
	}
	st, err := run("status", "--porcelain", "--", rel)
	if err != nil {
		return c, false
	}
	return c, st == ""
}

// ---- decisions (human only) ----

// Accept lets an offered (or rejected) skill be written into worktrees and served to agents. Human only. A blocked skill
// cannot be accepted; release it first if the block was an injection score.
func (s *Service) Accept(ctx context.Context, by, projectID, name string) (Skill, error) {
	return s.decide(ctx, by, projectID, name, StatusAccepted, EventAccepted, []string{StatusOffered, StatusRejected})
}

// Reject stops a skill from being delivered. Human only.
func (s *Service) Reject(ctx context.Context, by, projectID, name string) (Skill, error) {
	return s.decide(ctx, by, projectID, name, StatusRejected, EventRejected, []string{StatusOffered, StatusAccepted})
}

func (s *Service) decide(ctx context.Context, by, projectID, name, to, event string, from []string) (Skill, error) {
	if err := security.RequireHuman(by); err != nil {
		return Skill{}, err
	}
	cur, err := s.Get(ctx, projectID, name)
	if err != nil {
		return Skill{}, err
	}
	ok := false
	for _, f := range from {
		if cur.Status == f {
			ok = true
		}
	}
	if !ok {
		return cur, fmt.Errorf("skills: %s is %s and cannot become %s", name, cur.Status, to)
	}
	now := s.now().UnixMilli()
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE skills SET status=?,decided_by=?,decided_at=?,updated_at=?,block_reason='' WHERE project_id=? AND name=? AND content_hash=?`, to, by, now, now, projectID, name, cur.ContentHash)
		return err
	}); err != nil {
		return cur, err
	}
	s.emit(ctx, event, projectID, "", "", map[string]any{"skill": name, "origin": cur.Origin, "commit": cur.PinnedCommit, "by": by})
	return s.Get(ctx, projectID, name)
}

// Release turns a skill that was blocked only by the injection score into an offered one, after a human read it. Skills
// blocked for a secret cannot be released (their text is not stored). Human only.
func (s *Service) Release(ctx context.Context, by, projectID, name string) (Skill, error) {
	if err := security.RequireHuman(by); err != nil {
		return Skill{}, err
	}
	cur, err := s.Get(ctx, projectID, name)
	if err != nil {
		return Skill{}, err
	}
	if cur.Status != StatusBlocked {
		return cur, fmt.Errorf("skills: %s is not blocked", name)
	}
	if strings.HasPrefix(cur.BlockReason, "secret") || cur.Body == "" {
		return cur, errors.New("skills: a skill that contained a secret cannot be released; fix the source and import it again")
	}
	if strings.HasPrefix(cur.BlockReason, "unlicensed") {
		return cur, errors.New("skills: an unlicensed skill cannot be released")
	}
	now := s.now().UnixMilli()
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE skills SET status='offered',block_reason='released by a human after review',decided_by=?,decided_at=?,updated_at=? WHERE project_id=? AND name=?`, by, now, now, projectID, name)
		return err
	}); err != nil {
		return cur, err
	}
	s.emit(ctx, EventReleased, projectID, "", "", map[string]any{"skill": name, "by": by})
	return s.Get(ctx, projectID, name)
}

// ---- registries (the allowlist a human edits) ----

// EnsureRegistries stores the curated registries that are missing. A curated entry a human disabled stays disabled.
func (s *Service) EnsureRegistries(ctx context.Context) error {
	cur, err := CuratedRegistries()
	if err != nil {
		return err
	}
	now := s.now().UnixMilli()
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, r := range cur {
			if _, err := tx.ExecContext(ctx, `INSERT INTO skill_registries(id,repo,root,license,license_scope,trust,import_mode,notes,enabled,curated,added_by,created_at) VALUES(?,?,?,?,?,?,?,?,1,1,'curated',?)
				ON CONFLICT(id) DO NOTHING`, r.ID, r.Repo, r.Root, r.License, r.LicenseScope, r.Trust, r.Import, r.Notes, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Registries lists the allowlist.
func (s *Service) Registries(ctx context.Context) ([]Registry, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id,repo,root,license,license_scope,trust,import_mode,notes,enabled,curated,added_by FROM skill_registries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Registry{}
	for rows.Next() {
		var r Registry
		var en, cu int
		if err := rows.Scan(&r.ID, &r.Repo, &r.Root, &r.License, &r.LicenseScope, &r.Trust, &r.Import, &r.Notes, &en, &cu, &r.AddedBy); err != nil {
			return nil, err
		}
		r.Enabled, r.Curated = en == 1, cu == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) registry(ctx context.Context, id string) (Registry, error) {
	rs, err := s.Registries(ctx)
	if err != nil {
		return Registry{}, err
	}
	for _, r := range rs {
		if r.ID == id {
			return r, nil
		}
	}
	return Registry{}, fmt.Errorf("%w: registry %s is not in the allowlist", ErrNotFound, id)
}

// AddRegistry adds a repository to the allowlist. It is an explicit human action that needs acknowledged=true: skills from a
// repository you add are third-party instructions for your agents. Human only.
func (s *Service) AddRegistry(ctx context.Context, by string, r Registry, acknowledged bool) (Registry, error) {
	if err := security.RequireHuman(by); err != nil {
		return Registry{}, err
	}
	if !acknowledged {
		return Registry{}, errors.New("skills: adding a registry needs your acknowledgement: skills from it become instructions for your agents once you accept them")
	}
	r.Trust = "community"
	if err := r.Validate(); err != nil {
		return Registry{}, err
	}
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO skill_registries(id,repo,root,license,license_scope,trust,import_mode,notes,enabled,curated,added_by,created_at) VALUES(?,?,?,?,?,?,?,?,1,0,?,?)`,
			r.ID, r.Repo, r.Root, r.License, r.LicenseScope, r.Trust, r.Import, r.Notes, by, s.now().UnixMilli())
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Registry{}, fmt.Errorf("skills: registry %s already exists", r.ID)
		}
		return Registry{}, err
	}
	s.emit(ctx, EventRegistryChanged, "", "", "", map[string]any{"registry": r.ID, "repo": r.Repo, "action": "added", "by": by})
	return s.registry(ctx, r.ID)
}

// SetRegistryEnabled enables or disables a registry. Human only.
func (s *Service) SetRegistryEnabled(ctx context.Context, by, id string, enabled bool) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	if _, err := s.registry(ctx, id); err != nil {
		return err
	}
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE skill_registries SET enabled=? WHERE id=?`, b2i(enabled), id)
		return err
	}); err != nil {
		return err
	}
	s.emit(ctx, EventRegistryChanged, "", "", "", map[string]any{"registry": id, "action": map[bool]string{true: "enabled", false: "disabled"}[enabled], "by": by})
	return nil
}

// RemoveRegistry removes a registry a human added. Curated registries can only be disabled. Human only.
func (s *Service) RemoveRegistry(ctx context.Context, by, id string) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	r, err := s.registry(ctx, id)
	if err != nil {
		return err
	}
	if r.Curated {
		return errors.New("skills: a curated registry can be disabled but not removed")
	}
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM skill_registries WHERE id=?`, id)
		return err
	}); err != nil {
		return err
	}
	s.emit(ctx, EventRegistryChanged, "", "", "", map[string]any{"registry": id, "action": "removed", "by": by})
	return nil
}

// Candidate is a skill found in a registry, as listed before any import.
type Candidate struct {
	Name        string `json:"name"`
	Dir         string `json:"dir"`
	Description string `json:"description,omitempty"`
	HasScripts  bool   `json:"hasScripts,omitempty"`
	Valid       bool   `json:"valid"`
	Problem     string `json:"problem,omitempty"`
}

// RegistryCandidates resolves ref to a commit and lists the skills the registry holds there. Nothing is stored. Human only
// (it contacts the network).
func (s *Service) RegistryCandidates(ctx context.Context, by, registryID, ref string) (string, []Candidate, error) {
	if err := security.RequireHuman(by); err != nil {
		return "", nil, err
	}
	reg, commit, remote, err := s.fetchRegistry(ctx, registryID, ref)
	if err != nil {
		return "", nil, err
	}
	_ = reg
	out := []Candidate{}
	for _, r := range remote {
		c := Candidate{Name: r.Name, Dir: r.Dir, HasScripts: r.HasScripts}
		if ps, err := ParseSkill(r.SkillMD, r.Name); err != nil {
			c.Problem = err.Error()
		} else {
			c.Valid, c.Description = true, ps.Description
		}
		out = append(out, c)
	}
	return commit, out, nil
}

func (s *Service) fetchRegistry(ctx context.Context, registryID, ref string) (Registry, string, []RemoteSkill, error) {
	reg, err := s.registry(ctx, registryID)
	if err != nil {
		return reg, "", nil, err
	}
	if !reg.Enabled {
		return reg, "", nil, fmt.Errorf("skills: registry %s is disabled", registryID)
	}
	if s.Fetcher == nil {
		return reg, "", nil, errors.New("skills: registry import is not configured")
	}
	commit, err := s.Fetcher.Resolve(ctx, reg.Repo, ref)
	if err != nil {
		return reg, "", nil, err
	}
	remote, err := s.Fetcher.Fetch(ctx, reg.Repo, commit, reg.Root)
	return reg, commit, remote, err
}

// ImportReq imports one skill from a registry.
type ImportReq struct {
	ProjectID  string `json:"projectId"`
	RegistryID string `json:"registryId"`
	Name       string `json:"name"`
	Ref        string `json:"ref"` // branch, tag or commit; empty: the default branch
}

// Import fetches one skill from an allowlisted registry, pins it to a commit and validates it (spec rules, license, size,
// secrets, injection). The result is stored as OFFERED (or BLOCKED): it reaches no worktree and no agent until a human
// accepts it. Importing a newer commit of an accepted skill puts it back to offered. Human only.
func (s *Service) Import(ctx context.Context, by string, r ImportReq) (Skill, error) {
	if err := security.RequireHuman(by); err != nil {
		return Skill{}, err
	}
	if err := ValidateName(r.Name); err != nil {
		return Skill{}, err
	}
	var n int
	if err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, r.ProjectID).Scan(&n); err != nil || n == 0 {
		return Skill{}, fmt.Errorf("skills: project %s not found", r.ProjectID)
	}
	if existing, err := s.Get(ctx, r.ProjectID, r.Name); err == nil && existing.Origin == OriginRepo {
		return Skill{}, fmt.Errorf("skills: the repository already has a skill named %s; the repository's own skill wins", r.Name)
	}
	reg, commit, remote, err := s.fetchRegistry(ctx, r.RegistryID, r.Ref)
	if err != nil {
		return Skill{}, err
	}
	var matches []RemoteSkill
	for _, rs := range remote {
		if rs.Name == r.Name {
			matches = append(matches, rs)
		}
	}
	switch len(matches) {
	case 0:
		return Skill{}, fmt.Errorf("%w: %s has no skill named %s at %s", ErrNotFound, reg.ID, r.Name, commit[:12])
	case 1:
	default:
		var dirs []string
		for _, m := range matches {
			dirs = append(dirs, m.Dir)
		}
		sort.Strings(dirs)
		return Skill{}, fmt.Errorf("skills: %s is ambiguous in %s (%s)", r.Name, reg.ID, strings.Join(dirs, ", "))
	}
	rs := matches[0]
	rec, err := s.evaluateRemote(ctx, reg, commit, rs)
	if err != nil {
		var blockedErr *invalidError
		if errors.As(err, &blockedErr) {
			s.emit(ctx, EventBlocked, r.ProjectID, "", "", map[string]any{"skill": r.Name, "registry": reg.ID, "commit": commit, "reason": blockedErr.Error()})
		}
		return Skill{}, err
	}
	k, changed, err := s.upsert(ctx, r.ProjectID, rec)
	if err != nil {
		return k, err
	}
	if changed {
		s.emit(ctx, EventResolved, r.ProjectID, "", "", map[string]any{"skill": k.Name, "origin": OriginRegistry, "registry": reg.ID, "path": rs.Dir, "commit": commit, "status": k.Status})
		s.emit(ctx, EventImported, r.ProjectID, "", "", map[string]any{"skill": k.Name, "registry": reg.ID, "commit": commit, "status": k.Status, "scripts_skipped": rs.HasScripts, "by": by})
		if k.Status == StatusBlocked {
			s.emit(ctx, EventBlocked, r.ProjectID, "", "", map[string]any{"skill": k.Name, "registry": reg.ID, "commit": commit, "reason": k.BlockReason})
		} else {
			s.emit(ctx, EventOffered, r.ProjectID, "", "", map[string]any{"skill": k.Name, "registry": reg.ID, "commit": commit})
		}
	}
	return k, nil
}

type invalidError struct{ msg string }

func (e *invalidError) Error() string { return e.msg }

func (s *Service) evaluateRemote(ctx context.Context, reg Registry, commit string, rs RemoteSkill) (record, error) {
	if rs.SkillMD == nil {
		return record{}, &invalidError{fmt.Sprintf("SKILL.md is larger than %d KiB", MaxSkillBytes>>10)}
	}
	ps, err := ParseSkill(rs.SkillMD, rs.Name)
	if err != nil {
		return record{}, &invalidError{"not a valid skill: " + err.Error()}
	}
	verdict, err := Scan(ctx, s.Scanner, string(rs.SkillMD))
	if err != nil {
		return record{}, err
	}
	lic := firstNonEmpty(ps.License, reg.License)
	if reg.LicenseScope == "per_skill" {
		lic = firstNonEmpty(ps.License, strings.TrimSpace(firstLine(rs.LicenseTxt)))
	}
	rec := record{ps: ps, origin: OriginRegistry, registry: reg.ID, source: rs.Dir, pinned: commit, hash: packs.HashHex(string(rs.SkillMD)), scripts: rs.HasScripts, verdict: verdict, license: lic}
	switch {
	case licenseRefused(lic):
		rec.status, rec.reason = StatusBlocked, "unlicensed: no license is recorded for this skill"
		rec.verdict.Blocked = true
	case verdict.Blocked:
		rec.status, rec.reason = StatusBlocked, verdict.Reason
	default:
		rec.status = StatusOffered
		if reg.Import == "per_skill_review" {
			rec.reason = "registry requires a per-skill review"
		}
	}
	return rec, nil
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return truncate(t, 120)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
