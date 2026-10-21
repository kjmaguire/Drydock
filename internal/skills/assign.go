package skills

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/security"
)

// Events of per-agent assignment and of the audience.
const (
	EventAssigned   = "skill.assigned"
	EventUnassigned = "skill.unassigned"
	EventAudience   = "skill.audience"
)

// Assign gives a skill to one agent of its project (on) or takes it away. A skill reaches only the agents it is given to
// (decision P76; SetAudience, Reaches), so taking it from its last agent leaves it with nobody. Giving a skill to one more agent never takes
// it from anyone. Assigning never makes a skill deliverable by itself: only accepted skills are ever delivered. Human only.
func (s *Service) Assign(ctx context.Context, by, projectID, name, agentID string, on bool) (Skill, error) {
	if err := security.RequireHuman(by); err != nil {
		return Skill{}, err
	}
	if _, err := s.Get(ctx, projectID, name); err != nil {
		return Skill{}, err
	}
	removed, err := s.teamAgent(ctx, projectID, agentID)
	if err != nil {
		return Skill{}, err
	}
	if on && removed {
		return Skill{}, fmt.Errorf("skills: agent %s was removed from the team", agentID)
	}
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if on {
			_, err := tx.ExecContext(ctx, `INSERT INTO agent_skills(project_id,agent_id,skill_name,assigned_by,assigned_at) VALUES(?,?,?,?,?)
				ON CONFLICT(project_id,agent_id,skill_name) DO NOTHING`, projectID, agentID, name, by, s.now().UnixMilli())
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM agent_skills WHERE project_id=? AND agent_id=? AND skill_name=?`, projectID, agentID, name)
		return err
	}); err != nil {
		return Skill{}, err
	}
	ev := EventAssigned
	if !on {
		ev = EventUnassigned
	}
	s.emit(ctx, ev, projectID, "", agentID, map[string]any{"skill": name, "agent": agentID, "by": by})
	return s.Get(ctx, projectID, name)
}

// SetAudience sets the agents a skill is given to (AudienceAgents, an empty list meaning nobody). The whole team
// (AudienceTeam) is refused since decision P76: each agent is a specialist and gets its own skills. agents replaces the agents the skill is given to; each must be
// an agent of the project, and one removed from the team is left out. Like Assign it never makes a skill deliverable: only
// accepted skills are ever delivered. Human only.
func (s *Service) SetAudience(ctx context.Context, by, projectID, name, audience string, agents []string) (Skill, error) {
	if err := security.RequireHuman(by); err != nil {
		return Skill{}, err
	}
	if audience == AudienceTeam {
		return Skill{}, errors.New("skills: a skill is given to chosen agents, not to the whole team: each agent is a specialist in its role (decision P76)")
	}
	if audience != AudienceAgents {
		return Skill{}, fmt.Errorf("skills: the audience must be %q", AudienceAgents)
	}
	if _, err := s.Get(ctx, projectID, name); err != nil {
		return Skill{}, err
	}
	want := []string{}
	for _, a := range agents {
		if a = strings.TrimSpace(a); a == "" || contains(want, a) {
			continue
		}
		removed, err := s.teamAgent(ctx, projectID, a)
		if err != nil {
			return Skill{}, err
		}
		if !removed {
			want = append(want, a)
		}
	}
	sort.Strings(want)
	had, err := s.agentsOf(ctx, projectID, name)
	if err != nil {
		return Skill{}, err
	}
	now := s.now().UnixMilli()
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE skills SET audience=? WHERE project_id=? AND name=?`, audience, projectID, name); err != nil {
			return err
		}
		for _, a := range had {
			if !contains(want, a) {
				if _, err := tx.ExecContext(ctx, `DELETE FROM agent_skills WHERE project_id=? AND agent_id=? AND skill_name=?`, projectID, a, name); err != nil {
					return err
				}
			}
		}
		for _, a := range want {
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_skills(project_id,agent_id,skill_name,assigned_by,assigned_at) VALUES(?,?,?,?,?)
				ON CONFLICT(project_id,agent_id,skill_name) DO NOTHING`, projectID, a, name, by, now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return Skill{}, err
	}
	s.emit(ctx, EventAudience, projectID, "", "", map[string]any{"skill": name, "audience": audience, "agents": want, "by": by})
	return s.Get(ctx, projectID, name)
}

// giveByRoles gives a skill that names the roles it is for to the project's current agents of those roles (inside tx). A skill
// that names no roles is given to nobody: a person chooses its agents.
func giveByRoles(ctx context.Context, tx *sql.Tx, projectID, name string, roles []string, by string, now int64) error {
	if len(roles) == 0 {
		return nil
	}
	ids, err := agentsByRole(ctx, tx, projectID, func(role string) bool { return RolesMatch(roles, role) })
	if err != nil {
		return err
	}
	for _, a := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_skills(project_id,agent_id,skill_name,assigned_by,assigned_at) VALUES(?,?,?,?,?)
			ON CONFLICT(project_id,agent_id,skill_name) DO NOTHING`, projectID, a, name, by, now); err != nil {
			return err
		}
	}
	return nil
}

// agentsByRole lists the project's agents on the team (not removed) whose role passes match, sorted.
func agentsByRole(ctx context.Context, tx *sql.Tx, projectID string, match func(role string) bool) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, role FROM agents WHERE project_id=? AND archived_at=0 ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, role string
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		if match(role) {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// GiveTeamSkillsToAgents turns every whole-team skill (made before decision P76, or brought by an older project archive) into
// a skill for chosen agents: the agents it reached, the ones its roles took in and the ones it was given to, keep it, and from
// then on each agent's skills are its own to add and remove. Agents added later no longer receive it by their role. It
// returns how many skills it changed; running it again changes nothing.
func (s *Service) GiveTeamSkillsToAgents(ctx context.Context) (int, error) {
	type teamSkill struct {
		project, name string
		roles         []string
	}
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT project_id, name, roles FROM skills WHERE audience=? ORDER BY project_id, name`, AudienceTeam)
	if err != nil {
		return 0, err
	}
	var todo []teamSkill
	for rows.Next() {
		var t teamSkill
		var roles string
		if err := rows.Scan(&t.project, &t.name, &roles); err != nil {
			rows.Close()
			return 0, err
		}
		_ = json.Unmarshal([]byte(roles), &t.roles)
		todo = append(todo, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	now := s.now().UnixMilli()
	for _, t := range todo {
		var given []string
		if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			ids, err := agentsByRole(ctx, tx, t.project, func(role string) bool { return RolesMatch(t.roles, role) })
			if err != nil {
				return err
			}
			for _, a := range ids {
				if _, err := tx.ExecContext(ctx, `INSERT INTO agent_skills(project_id,agent_id,skill_name,assigned_by,assigned_at) VALUES(?,?,?,?,?)
					ON CONFLICT(project_id,agent_id,skill_name) DO NOTHING`, t.project, a, t.name, "drydock:P76", now); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE skills SET audience=? WHERE project_id=? AND name=?`, AudienceAgents, t.project, t.name); err != nil {
				return err
			}
			given, err = agentsInTx(ctx, tx, t.project, t.name)
			return err
		}); err != nil {
			return 0, err
		}
		s.emit(ctx, EventAudience, t.project, "", "", map[string]any{"skill": t.name, "audience": AudienceAgents, "agents": nz(given), "by": "drydock:P76"})
	}
	return len(todo), nil
}

func agentsInTx(ctx context.Context, tx *sql.Tx, projectID, name string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT agent_id FROM agent_skills WHERE project_id=? AND skill_name=? ORDER BY agent_id`, projectID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// teamAgent checks that agentID is an agent of the project and reports whether it was removed from the team.
func (s *Service) teamAgent(ctx context.Context, projectID, agentID string) (removed bool, err error) {
	var agentProject string
	var archived int64
	err = s.DB.Reader().QueryRowContext(ctx, `SELECT project_id, archived_at FROM agents WHERE id=?`, agentID).Scan(&agentProject, &archived)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && agentProject != projectID) {
		return false, fmt.Errorf("skills: agent %s is not on this project's team", agentID)
	}
	if err != nil {
		return false, err
	}
	return archived != 0, nil
}

// Assignments maps each assigned skill of a project to its agents (sorted).
func (s *Service) Assignments(ctx context.Context, projectID string) (map[string][]string, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT skill_name, agent_id FROM agent_skills WHERE project_id=? ORDER BY skill_name, agent_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var n, a string
		if err := rows.Scan(&n, &a); err != nil {
			return nil, err
		}
		out[n] = append(out[n], a)
	}
	return out, rows.Err()
}

// AgentSkills lists the skills assigned to one agent, by name.
func (s *Service) AgentSkills(ctx context.Context, projectID, agentID string) ([]Skill, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT skill_name FROM agent_skills WHERE project_id=? AND agent_id=? ORDER BY skill_name`, projectID, agentID)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	rows.Close()
	out := []Skill{}
	for _, n := range names {
		k, err := s.Get(ctx, projectID, n)
		if errors.Is(err, ErrNotFound) {
			continue // the skill is no longer in the project
		}
		if err != nil {
			return nil, err
		}
		k.Body = ""
		out = append(out, k)
	}
	return out, nil
}

func (s *Service) agentsOf(ctx context.Context, projectID, name string) ([]string, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT agent_id FROM agent_skills WHERE project_id=? AND skill_name=? ORDER BY agent_id`, projectID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
