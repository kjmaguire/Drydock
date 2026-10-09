package skills

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kjmaguire/drydock/internal/security"
)

// Events of per-agent assignment.
const (
	EventAssigned   = "skill.assigned"
	EventUnassigned = "skill.unassigned"
)

// Assign gives a skill to one agent of its project (on) or takes it away. While a skill is assigned to at least one agent it
// reaches only those agents (Eligible); with no assignment it goes back to role-based delivery. Assigning never makes a skill
// deliverable by itself: only accepted skills are ever delivered. Human only.
func (s *Service) Assign(ctx context.Context, by, projectID, name, agentID string, on bool) (Skill, error) {
	if err := security.RequireHuman(by); err != nil {
		return Skill{}, err
	}
	if _, err := s.Get(ctx, projectID, name); err != nil {
		return Skill{}, err
	}
	var agentProject string
	var archived int64
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT project_id, archived_at FROM agents WHERE id=?`, agentID).Scan(&agentProject, &archived)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && agentProject != projectID) {
		return Skill{}, fmt.Errorf("skills: agent %s is not on this project's team", agentID)
	}
	if err != nil {
		return Skill{}, err
	}
	if on && archived != 0 {
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
