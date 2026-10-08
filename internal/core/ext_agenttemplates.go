package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Agent templates: define a team member once, drop it into any project. Applying a template creates an ordinary agent in the
// project (through CreateAgent, so every check on creating an agent applies) and grants it the library MCP servers the template
// lists. The agent is not linked to the template afterwards. Templates are the human's own text, held in the workspace; they are
// never part of a project export.

func init() {
	RegisterExtension(Extension{Name: "agenttemplates", After: []string{"agentmgmt", "mcpservers"}, Start: startAgentTemplates})
}

// AgentTemplate is a reusable team member.
type AgentTemplate struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Role           string   `json:"role"`
	Purpose        string   `json:"purpose"`
	Adapter        string   `json:"adapter"`
	Model          string   `json:"model"`
	Effort         string   `json:"effort"`
	PermissionMode string   `json:"permissionMode"`
	ProviderID     string   `json:"providerId"`
	MCPServers     []string `json:"mcpServers"`
	CreatedAt      int64    `json:"createdAt"`
	UpdatedAt      int64    `json:"updatedAt"`
}

// AppliedTemplate is the result of dropping a template into a project.
type AppliedTemplate struct {
	Agent Agent `json:"agent"`
	// Granted are the MCP servers the new agent can use; Skipped says why a listed server was not granted.
	Granted []string `json:"granted"`
	Skipped []string `json:"skipped"`
}

type agentTemplates struct{ c *Core }

func startAgentTemplates(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	t := &agentTemplates{c: c}
	return map[string]Method{
		"agenttemplate.list":   bind(func(ctx context.Context, _ struct{}) (any, error) { return t.list(ctx) }),
		"agenttemplate.save":   bind(func(ctx context.Context, p AgentTemplate) (any, error) { return t.save(ctx, p) }),
		"agenttemplate.delete": bind(func(ctx context.Context, p idParam) (any, error) { return nil, t.delete(ctx, p.ID) }),
		"agenttemplate.fromAgent": bind(func(ctx context.Context, p struct {
			AgentID string `json:"agentId"`
		}) (any, error) {
			return t.fromAgent(ctx, p.AgentID)
		}),
		"agenttemplate.apply": bind(func(ctx context.Context, p struct {
			TemplateID string `json:"templateId"`
			ProjectID  string `json:"projectId"`
			Name       string `json:"name"`
		}) (any, error) {
			return t.apply(ctx, p.TemplateID, p.ProjectID, p.Name)
		}),
	}, nil, nil
}

const templateCols = `id,name,role,purpose,adapter,model,effort,permission_mode,provider_id,mcp_servers,created_at,updated_at`

func scanTemplate(sc interface{ Scan(...any) error }) (AgentTemplate, error) {
	var t AgentTemplate
	var mj string
	if err := sc.Scan(&t.ID, &t.Name, &t.Role, &t.Purpose, &t.Adapter, &t.Model, &t.Effort, &t.PermissionMode, &t.ProviderID, &mj, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	_ = json.Unmarshal([]byte(mj), &t.MCPServers)
	if t.MCPServers == nil {
		t.MCPServers = []string{}
	}
	return t, nil
}

func (t *agentTemplates) list(ctx context.Context) ([]AgentTemplate, error) {
	rows, err := t.c.DB.Reader().QueryContext(ctx, `SELECT `+templateCols+` FROM agent_templates ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentTemplate{}
	for rows.Next() {
		x, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (t *agentTemplates) get(ctx context.Context, id string) (AgentTemplate, error) {
	x, err := scanTemplate(t.c.DB.Reader().QueryRowContext(ctx, `SELECT `+templateCols+` FROM agent_templates WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, fmt.Errorf("there is no agent template %q", id)
	}
	return x, err
}

// save creates (empty ID) or replaces a template after the same checks an agent gets.
func (t *agentTemplates) save(ctx context.Context, in AgentTemplate) (AgentTemplate, error) {
	if err := t.c.RequireLicensed(ctx, "create_agent"); err != nil {
		return AgentTemplate{}, err
	}
	in.Name, in.Role, in.Purpose = strings.TrimSpace(in.Name), strings.TrimSpace(in.Role), strings.TrimSpace(in.Purpose)
	in.Model, in.Effort = strings.TrimSpace(in.Model), strings.TrimSpace(in.Effort)
	if in.Name == "" || in.Role == "" {
		return AgentTemplate{}, errors.New("a template needs a name and a role")
	}
	t.c.mu.Lock()
	_, known := t.c.adapters[in.Adapter]
	t.c.mu.Unlock()
	if !known {
		return AgentTemplate{}, fmt.Errorf("unknown adapter %q", in.Adapter)
	}
	if err := t.c.validateAgentFields(in.Adapter, in.Model, in.Effort, in.Purpose); err != nil {
		return AgentTemplate{}, err
	}
	servers := []string{}
	seen := map[string]bool{}
	for _, id := range in.MCPServers {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if s := t.c.mcpServerSvc(); s != nil {
			if _, err := s.load(ctx, id); err != nil {
				return AgentTemplate{}, err
			}
		}
		seen[id] = true
		servers = append(servers, id)
	}
	sort.Strings(servers)
	mj, _ := json.Marshal(servers)
	now := storage.Now()
	if in.ID == "" {
		in.ID = ids.New("tpl")
		err := t.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO agent_templates(`+templateCols+`,created_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				in.ID, in.Name, in.Role, in.Purpose, in.Adapter, in.Model, in.Effort, in.PermissionMode, in.ProviderID, string(mj), now, now, t.c.Human())
			return err
		})
		if err != nil {
			return AgentTemplate{}, err
		}
	} else {
		if _, err := t.get(ctx, in.ID); err != nil {
			return AgentTemplate{}, err
		}
		err := t.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE agent_templates SET name=?,role=?,purpose=?,adapter=?,model=?,effort=?,permission_mode=?,provider_id=?,mcp_servers=?,updated_at=? WHERE id=?`,
				in.Name, in.Role, in.Purpose, in.Adapter, in.Model, in.Effort, in.PermissionMode, in.ProviderID, string(mj), now, in.ID)
			return err
		})
		if err != nil {
			return AgentTemplate{}, err
		}
	}
	t.c.publish(ctx, "agenttemplate.saved", "", map[string]any{"id": in.ID, "by": t.c.Human()})
	return t.get(ctx, in.ID)
}

func (t *agentTemplates) delete(ctx context.Context, id string) error {
	if _, err := t.get(ctx, id); err != nil {
		return err
	}
	err := t.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM agent_templates WHERE id=?`, id)
		return err
	})
	if err == nil {
		t.c.publish(ctx, "agenttemplate.removed", "", map[string]any{"id": id, "by": t.c.Human()})
	}
	return err
}

// fromAgent saves an existing agent as a template, with the library servers it is granted in its project.
func (t *agentTemplates) fromAgent(ctx context.Context, agentID string) (AgentTemplate, error) {
	a, err := t.c.agent(ctx, agentID)
	if err != nil || a.Archived {
		return AgentTemplate{}, fmt.Errorf("agent %s not found", agentID)
	}
	var servers []string
	rows, err := t.c.DB.Reader().QueryContext(ctx, `SELECT server_id FROM agent_mcp_access WHERE project_id=? AND agent_id=? ORDER BY server_id`, a.ProjectID, a.ID)
	if err == nil {
		for rows.Next() {
			var s string
			if rows.Scan(&s) == nil {
				servers = append(servers, s)
			}
		}
		rows.Close()
	}
	return t.save(ctx, AgentTemplate{Name: a.Name, Role: a.Role, Purpose: a.Purpose, Adapter: a.Adapter, Model: a.Model, Effort: a.Effort,
		PermissionMode: a.PermissionMode, ProviderID: a.ProviderID, MCPServers: servers})
}

// apply creates the agent in the project. name overrides the template's name; a name already used by an agent of the project
// gets a number appended, so two copies of one template can be told apart.
func (t *agentTemplates) apply(ctx context.Context, templateID, projectID, name string) (AppliedTemplate, error) {
	tpl, err := t.get(ctx, templateID)
	if err != nil {
		return AppliedTemplate{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = tpl.Name
	}
	team, err := t.c.Agents(ctx, projectID)
	if err != nil {
		return AppliedTemplate{}, err
	}
	taken := map[string]bool{}
	for _, m := range team {
		taken[strings.ToLower(m.Name)] = true
	}
	final := name
	for n := 2; taken[strings.ToLower(final)]; n++ {
		final = fmt.Sprintf("%s %d", name, n)
	}
	ag, err := t.c.CreateAgent(ctx, AgentSpec{ProjectID: projectID, Name: final, Adapter: tpl.Adapter, Role: tpl.Role, Model: tpl.Model, Effort: tpl.Effort,
		ProviderID: tpl.ProviderID, PermissionMode: tpl.PermissionMode, Purpose: tpl.Purpose})
	if err != nil {
		return AppliedTemplate{}, err
	}
	res := AppliedTemplate{Agent: ag, Granted: []string{}, Skipped: []string{}}
	svc := t.c.mcpServerSvc()
	for _, sid := range tpl.MCPServers {
		if svc == nil {
			res.Skipped = append(res.Skipped, sid+": MCP servers are not available")
			continue
		}
		if _, err := svc.grant(ctx, projectID, sid, ag.ID, true); err != nil {
			res.Skipped = append(res.Skipped, sid+": "+err.Error())
			continue
		}
		res.Granted = append(res.Granted, sid)
	}
	t.c.publish(ctx, "agenttemplate.applied", projectID, map[string]any{"template": tpl.ID, "agent": ag.ID, "by": t.c.Human()})
	return res, nil
}
