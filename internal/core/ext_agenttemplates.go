package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/agentcatalog"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/skills"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Agent templates: define a team member once, drop it into any project. Applying a template creates an ordinary agent in the
// project (through CreateAgent, so every check on creating an agent applies), grants it the library MCP servers the template
// lists, and installs and assigns the registry skills it carries (as a prebuilt agent does, decision P27). The agent is not
// linked to the template afterwards. Templates are the human's own text, held in the workspace; they are
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
	// Skills are registry skills (pinned to a commit) installed and assigned to agents made from the template.
	Skills []agentcatalog.SkillRef `json:"skills"`
	// Practices turns the Claude best-practices text on or off for agents made from the template; omitted means on.
	Practices *bool `json:"practices"`
	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
	// ConfirmSkipsApprovals is the person's explicit confirmation of a permission mode that lets agents made from the template
	// act without asking; it is read when the template is saved and is not stored.
	ConfirmSkipsApprovals bool `json:"confirmSkipsApprovals,omitempty"`
}

// AppliedTemplate is the result of dropping a template into a project.
type AppliedTemplate struct {
	Agent Agent `json:"agent"`
	// Granted are the MCP servers the new agent can use; Skipped says why a listed server was not granted.
	Granted []string `json:"granted"`
	Skipped []string `json:"skipped"`
	// Skills is what installing the template's skills did (empty lists when it carries none or installSkills was false).
	Skills SkillsInstalled `json:"skills"`
}

type agentTemplates struct{ c *Core }

var commitPinRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

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
		"agenttemplate.skillChoices": bind(func(ctx context.Context, _ struct{}) (any, error) { return t.skillChoices(ctx) }),
		"agenttemplate.apply": bind(func(ctx context.Context, p struct {
			TemplateID string `json:"templateId"`
			ProjectID  string `json:"projectId"`
			Name       string `json:"name"`
			// InstallSkills installs the template's skills; omitted means yes.
			InstallSkills *bool `json:"installSkills"`
			// ConfirmSkipsApprovals confirms a template whose permission mode lets the agent act without asking.
			ConfirmSkipsApprovals bool `json:"confirmSkipsApprovals"`
		}) (any, error) {
			return t.apply(ctx, p.TemplateID, p.ProjectID, p.Name, p.InstallSkills == nil || *p.InstallSkills, p.ConfirmSkipsApprovals)
		}),
	}, nil, nil
}

const templateCols = `id,name,role,purpose,adapter,model,effort,permission_mode,provider_id,mcp_servers,practices,created_at,updated_at,skills`

func scanTemplate(sc interface{ Scan(...any) error }) (AgentTemplate, error) {
	var t AgentTemplate
	var mj, sj string
	var pr int
	if err := sc.Scan(&t.ID, &t.Name, &t.Role, &t.Purpose, &t.Adapter, &t.Model, &t.Effort, &t.PermissionMode, &t.ProviderID, &mj, &pr, &t.CreatedAt, &t.UpdatedAt, &sj); err != nil {
		return t, err
	}
	_ = json.Unmarshal([]byte(sj), &t.Skills)
	if t.Skills == nil {
		t.Skills = []agentcatalog.SkillRef{}
	}
	on := pr != 0
	t.Practices = &on
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
	refs, err := t.cleanSkills(ctx, in.Skills)
	if err != nil {
		return AgentTemplate{}, err
	}
	sj, _ := json.Marshal(refs)
	practices := b2i(in.Practices == nil || *in.Practices)
	now := storage.Now()
	if in.ID == "" {
		if err := t.c.confirmSkip(ctx, "", "", "agents made from this template", in.PermissionMode, in.ConfirmSkipsApprovals); err != nil {
			return AgentTemplate{}, err
		}
		in.ID = ids.New("tpl")
		err := t.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO agent_templates(`+templateCols+`,created_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				in.ID, in.Name, in.Role, in.Purpose, in.Adapter, in.Model, in.Effort, in.PermissionMode, in.ProviderID, string(mj), practices, now, now, string(sj), t.c.Human())
			return err
		})
		if err != nil {
			return AgentTemplate{}, err
		}
	} else {
		old, err := t.get(ctx, in.ID)
		if err != nil {
			return AgentTemplate{}, err
		}
		if in.PermissionMode != old.PermissionMode { // a template that has such a mode keeps it while its other fields are edited
			if err := t.c.confirmSkip(ctx, "", "", "agents made from this template", in.PermissionMode, in.ConfirmSkipsApprovals); err != nil {
				return AgentTemplate{}, err
			}
		}
		if in.Skills == nil { // a caller that does not send the skills keeps them (an edit dropped them before)
			sj, _ = json.Marshal(old.Skills)
		}
		err = t.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE agent_templates SET name=?,role=?,purpose=?,adapter=?,model=?,effort=?,permission_mode=?,provider_id=?,mcp_servers=?,practices=?,skills=?,updated_at=? WHERE id=?`,
				in.Name, in.Role, in.Purpose, in.Adapter, in.Model, in.Effort, in.PermissionMode, in.ProviderID, string(mj), practices, string(sj), now, in.ID)
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

// cleanSkills checks a template's skills: a registry in the allowlist, a valid name, a ref that is empty (the registry's default
// branch, resolved when applied) or a full commit; one entry per name.
func (t *agentTemplates) cleanSkills(ctx context.Context, in []agentcatalog.SkillRef) ([]agentcatalog.SkillRef, error) {
	out := []agentcatalog.SkillRef{}
	if len(in) == 0 {
		return out, nil
	}
	known := map[string]bool{}
	if k := t.c.Knowledge(); k != nil {
		regs, err := k.Skills.Registries(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range regs {
			known[r.ID] = true
		}
	}
	seen := map[string]bool{}
	for _, r := range in {
		r.Registry, r.Name, r.Ref = strings.TrimSpace(r.Registry), strings.TrimSpace(r.Name), strings.TrimSpace(r.Ref)
		if !known[r.Registry] {
			return nil, fmt.Errorf("skill %s: registry %q is not in the skills allowlist", r.Name, r.Registry)
		}
		if err := skills.ValidateName(r.Name); err != nil {
			return nil, fmt.Errorf("skill: %w", err)
		}
		if r.Ref != "" && !commitPinRe.MatchString(r.Ref) {
			return nil, fmt.Errorf("skill %s: the ref must be a full commit id or empty", r.Name)
		}
		if seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SkillChoice is a registry skill, pinned to a commit, that a template can carry: one a prebuilt agent comes with or one a
// project here imported.
type SkillChoice struct {
	agentcatalog.SkillRef
	Description string `json:"description"`
	// From says where Drydock knows the skill from: the prebuilt agents that come with it and the projects that imported it.
	From []string `json:"from"`
}

// skillChoices lists the registry skills a template can carry, one per registry, name and commit: those the prebuilt agents
// come with and those imported into a project here (blocked or rejected ones left out), from allowlisted registries only.
func (t *agentTemplates) skillChoices(ctx context.Context) ([]SkillChoice, error) {
	out := []SkillChoice{}
	k := t.c.Knowledge()
	if k == nil {
		return out, nil
	}
	regs, err := k.Skills.Registries(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, r := range regs {
		allowed[r.ID] = true
	}
	at := map[string]int{}
	add := func(ref agentcatalog.SkillRef, desc, from string) {
		if !allowed[ref.Registry] || !commitPinRe.MatchString(ref.Ref) || skills.ValidateName(ref.Name) != nil {
			return
		}
		key := ref.Registry + "/" + ref.Name + "@" + ref.Ref
		i, ok := at[key]
		if !ok {
			i = len(out)
			at[key] = i
			out = append(out, SkillChoice{SkillRef: ref, From: []string{}})
		}
		if out[i].Description == "" {
			out[i].Description = desc
		}
		if !slices.Contains(out[i].From, from) {
			out[i].From = append(out[i].From, from)
		}
	}
	rows, err := t.c.DB.Reader().QueryContext(ctx, `SELECT s.registry_id, s.name, s.pinned_commit, s.description, p.name FROM skills s JOIN projects p ON p.id = s.project_id
		WHERE s.origin = 'registry' AND s.status IN ('offered', 'accepted') ORDER BY s.name, p.name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ref agentcatalog.SkillRef
		var desc, project string
		if err := rows.Scan(&ref.Registry, &ref.Name, &ref.Ref, &desc, &project); err != nil {
			rows.Close()
			return nil, err
		}
		add(ref, desc, "project "+project)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	entries, err := agentcatalog.Builtin()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		for _, r := range e.Skills {
			add(r, "", "prebuilt "+e.Name)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Registry < out[j].Registry
	})
	return out, nil
}

// fromAgent saves an existing agent as a template, with the library servers it is granted in its project and the registry
// skills assigned to it (pinned to the commit the project has).
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
	refs := []agentcatalog.SkillRef{}
	if k := t.c.Knowledge(); k != nil {
		mine, err := k.Skills.AgentSkills(ctx, a.ProjectID, a.ID)
		if err != nil {
			return AgentTemplate{}, err
		}
		for _, s := range mine {
			if s.Origin == skills.OriginRegistry {
				refs = append(refs, agentcatalog.SkillRef{Registry: s.RegistryID, Name: s.Name, Ref: s.PinnedCommit})
			}
		}
	}
	return t.save(ctx, AgentTemplate{Name: a.Name, Role: a.Role, Purpose: a.Purpose, Adapter: a.Adapter, Model: a.Model, Effort: a.Effort,
		PermissionMode: a.PermissionMode, ProviderID: a.ProviderID, MCPServers: servers, Skills: refs, Practices: &a.Practices})
}

// apply creates the agent in the project. name overrides the template's name; a name already used by an agent of the project
// gets a number appended, so two copies of one template can be told apart.
func (t *agentTemplates) apply(ctx context.Context, templateID, projectID, name string, installSkills, confirmSkips bool) (AppliedTemplate, error) {
	tpl, err := t.get(ctx, templateID)
	if err != nil {
		return AppliedTemplate{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = tpl.Name
	}
	final, err := t.c.uniqueAgentName(ctx, projectID, name)
	if err != nil {
		return AppliedTemplate{}, err
	}
	ag, err := t.c.CreateAgent(ctx, AgentSpec{ProjectID: projectID, Name: final, Adapter: tpl.Adapter, Role: tpl.Role, Model: tpl.Model, Effort: tpl.Effort,
		ProviderID: tpl.ProviderID, PermissionMode: tpl.PermissionMode, Purpose: tpl.Purpose, Practices: tpl.Practices, ConfirmSkipsApprovals: confirmSkips})
	if err != nil {
		return AppliedTemplate{}, err
	}
	res := AppliedTemplate{Agent: ag, Granted: []string{}, Skipped: []string{}, Skills: newSkillsInstalled()}
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
	if len(tpl.Skills) > 0 {
		if installSkills {
			res.Skills = t.c.installSkillsFor(ctx, ag, tpl.Skills, "agent template "+tpl.ID)
		} else {
			for _, s := range tpl.Skills {
				res.Skills.Pending = append(res.Skills.Pending, Skipped{Name: s.Name, Reason: "not installed: you chose not to install skills"})
			}
		}
	}
	t.c.publish(ctx, "agenttemplate.applied", projectID, map[string]any{"template": tpl.ID, "agent": ag.ID, "by": t.c.Human()})
	return res, nil
}
