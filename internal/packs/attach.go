package packs

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/security"
)

// Attachment attaches a pack to a project, optionally narrowed to one agent or one role. Empty agent and role mean every
// agent of the project.
type Attachment struct {
	ProjectID string    `json:"projectId"`
	PackID    string    `json:"packId"`
	AgentID   string    `json:"agentId,omitempty"`
	Role      string    `json:"role,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy,omitempty"`
}

func normRole(r string) string {
	r = strings.TrimSpace(r)
	if c := CanonicalRole(r); c != "" {
		return c
	}
	return r
}

// Attach attaches a pack (ATTACH mode). Human only. It changes what an agent is told and which pack search_pack may read;
// it never changes the agent's permissions.
func (s *Store) Attach(ctx context.Context, by, projectID, packID, agentID, role string) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	if projectID == "" {
		return fmt.Errorf("packs: a project is required")
	}
	if _, _, err := s.loadRow(ctx, packID); err != nil {
		return err
	}
	role = normRole(role)
	err := s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, projectID).Scan(&n); err != nil || n == 0 {
			return fmt.Errorf("packs: project %s not found", projectID)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO pack_attachments(project_id,pack_id,agent_id,role,created_at,created_by) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
			projectID, packID, agentID, role, s.now().UnixMilli(), by)
		return err
	})
	if err == nil {
		s.emit(ctx, EventAttached, map[string]any{"pack": packID, "project": projectID, "agent": agentID, "role": role, "by": by})
	}
	return err
}

// AttachDefaults attaches a pack for the roles its manifest recommends (attach.roles), or to the whole project if it names none.
func (s *Store) AttachDefaults(ctx context.Context, by, projectID, packID string) error {
	_, m, err := s.loadRow(ctx, packID)
	if err != nil {
		return err
	}
	if len(m.Attach.Roles) == 0 {
		return s.Attach(ctx, by, projectID, packID, "", "")
	}
	for _, r := range m.Attach.Roles {
		if err := s.Attach(ctx, by, projectID, packID, "", r); err != nil {
			return err
		}
	}
	return nil
}

// Detach removes an attachment. Human only.
func (s *Store) Detach(ctx context.Context, by, projectID, packID, agentID, role string) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	role = normRole(role)
	err := s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM pack_attachments WHERE project_id=? AND pack_id=? AND agent_id=? AND role=?`, projectID, packID, agentID, role)
		return err
	})
	if err == nil {
		s.emit(ctx, EventDetached, map[string]any{"pack": packID, "project": projectID, "agent": agentID, "role": role, "by": by})
	}
	return err
}

// Attachments lists a project's attachments.
func (s *Store) Attachments(ctx context.Context, projectID string) ([]Attachment, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT project_id,pack_id,agent_id,role,created_at,created_by FROM pack_attachments WHERE project_id=? ORDER BY pack_id,agent_id,role`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		var ts int64
		if err := rows.Scan(&a.ProjectID, &a.PackID, &a.AgentID, &a.Role, &ts, &a.CreatedBy); err != nil {
			return nil, err
		}
		a.CreatedAt = time.UnixMilli(ts).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// AttachedIDs returns the ids of the packs attached for an agent: attachments to the project as a whole, to the agent, or to
// its role (case-insensitive).
func (s *Store) AttachedIDs(ctx context.Context, projectID, agentID, role string) ([]string, error) {
	if projectID == "" {
		return nil, nil
	}
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT DISTINCT pack_id FROM pack_attachments WHERE project_id=?
		AND (agent_id='' OR agent_id=?) AND (role='' OR LOWER(role)=LOWER(?)) ORDER BY pack_id`, projectID, agentID, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Instruction is the ATTACH text of one pack (about 400 tokens at most; never crawled text).
type Instruction struct {
	PackID  string `json:"packId"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Text    string `json:"text"`
}

// Instructions returns what ATTACH adds to the context package of an agent, one entry per attached pack, in pack id order.
// The text comes from the human-reviewed manifest, so it is not wrapped as untrusted; it ends with how to use the pack.
func (s *Store) Instructions(ctx context.Context, projectID, agentID, role string) ([]Instruction, error) {
	ids, err := s.AttachedIDs(ctx, projectID, agentID, role)
	if err != nil {
		return nil, err
	}
	var out []Instruction
	for _, id := range ids {
		r, m, err := s.loadRow(ctx, id)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(m.Attach.Instructions)
		tail := fmt.Sprintf("Call search_pack with pack_ids [%q] before asserting details, and cite the url and heading of what you use.", id)
		if text != "" {
			text += " "
		}
		out = append(out, Instruction{PackID: id, Title: m.Title, Kind: r.kind, Version: r.version, Text: text + tail})
	}
	return out, nil
}

// Allowance is what attached packs add to a run's MCP policy: the search_pack tool, and for live and hybrid packs their
// docs-server tool. It only names tools; the gateway still applies catalog rules, egress limits and approvals.
type Allowance struct {
	SearchPack bool
	// Live maps a catalog server id to the pack tools it may call.
	Live map[string][]string
}

// AllowFor computes the allowance for an agent.
func (s *Store) AllowFor(ctx context.Context, projectID, agentID, role string) (Allowance, error) {
	ids, err := s.AttachedIDs(ctx, projectID, agentID, role)
	if err != nil {
		return Allowance{}, err
	}
	a := Allowance{Live: map[string][]string{}}
	for _, id := range ids {
		_, m, err := s.loadRow(ctx, id)
		if err != nil {
			continue
		}
		a.SearchPack = true
		if m.Live != nil {
			a.Live[m.Live.MCP] = appendUnique(a.Live[m.Live.MCP], m.Live.Tool)
		}
	}
	for k := range a.Live {
		sort.Strings(a.Live[k])
	}
	return a, nil
}

// Apply adds the allowance to a run policy.
func (a Allowance) Apply(p *mcp.RunPolicy) {
	if p.Allow == nil {
		p.Allow = map[string][]string{}
	}
	if a.SearchPack {
		p.Allow[mcp.WorkspaceServerID] = appendUnique(p.Allow[mcp.WorkspaceServerID], "search_pack")
	}
	for srv, tools := range a.Live {
		for _, t := range tools {
			p.Allow[srv] = appendUnique(p.Allow[srv], t)
		}
	}
}

func appendUnique(list []string, v string) []string {
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}
