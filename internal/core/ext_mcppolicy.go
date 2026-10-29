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

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/repomap"
	"github.com/kjmaguire/drydock/internal/storage"
)

// The per-role MCP tool table (decision P115, docs/mcp.md section 3): each project sees section 3's table on its Integrations
// page and may change any cell (mcp_role_policy, migration 00148); it may add CEL rules that take tools away
// (mcp_policy_rules); a task may narrow its agents' tools to fewer (task_mcp_tools, migration 00149). planMCP (ext_mcp.go)
// reads all three for every run, and the gateway enforces them on tools/list and tools/call (mcp.RunPolicy.Refuse).

func init() {
	RegisterExtension(Extension{Name: "mcppolicy", After: []string{"mcp"}, Start: startMCPPolicy})
}

// Events the page reads again on.
const (
	eventRolePolicyChanged = "mcp.role_policy_changed"
	eventTaskToolsChanged  = "task.mcp_tools_changed"
)

// policyRow is the table's row for a role as an agent has it ("Backend Engineer" is backend, the team lead and any role the
// table does not name is "other"). A review turn runs as reviewer.
func policyRow(role string) string {
	r := pipeline.NormalizePlanRole(role)
	if r == "" || !mcp.KnownPolicyRole(r) {
		return mcp.RoleOther
	}
	return r
}

// roleTable is the project's table: the defaults with the cells a person changed.
func (c *Core) roleTable(ctx context.Context, projectID string) mcp.RoleTable {
	t := mcp.RoleTable{Overrides: map[string]map[string]bool{}}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT role, col, allowed FROM mcp_role_policy WHERE project_id=?`, projectID)
	if err != nil {
		return t
	}
	defer rows.Close()
	for rows.Next() {
		var role, col string
		var on int
		if rows.Scan(&role, &col, &on) == nil {
			if t.Overrides[role] == nil {
				t.Overrides[role] = map[string]bool{}
			}
			t.Overrides[role][col] = on == 1
		}
	}
	return t
}

// policyRules are the project's CEL rules, oldest first.
func (c *Core) policyRules(ctx context.Context, projectID string) ([]mcp.PolicyRule, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id, name, expr FROM mcp_policy_rules WHERE project_id=? ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []mcp.PolicyRule{}
	for rows.Next() {
		var r mcp.PolicyRule
		if err := rows.Scan(&r.ID, &r.Name, &r.Expr); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// taskNarrowing is what a task narrowed its agents' tools to (ok false: it does not narrow).
func (c *Core) taskNarrowing(ctx context.Context, taskID string) (globs []string, ok bool) {
	if taskID == "" {
		return nil, false
	}
	var raw string
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT tools FROM task_mcp_tools WHERE task_id=?`, taskID).Scan(&raw); err != nil {
		return nil, false
	}
	if json.Unmarshal([]byte(raw), &globs) != nil {
		return []string{}, true // unreadable narrowing: narrow to nothing rather than to everything
	}
	return globs, true
}

// workspaceColumn is the table's column of a Workspace tool (the tool's own name for one the table does not hold:
// propose_memory, the sandbox and permission tools).
func workspaceColumn(tool string) string {
	for _, col := range mcp.RoleColumns {
		if slices.Contains(col.Tools, tool) {
			return col.Key
		}
	}
	return tool
}

// mechanismTools are the Workspace tools that carry how a turn works rather than what an agent may reach: the permission
// prompt of a worker turn outside a sandbox and the sandbox's command tools. No rule or task narrowing takes them away (without
// them the turn could not ask, or run its commands at all); the role table does not hold them either.
func mechanismTool(tool string) bool {
	return tool == mcp.ToolPermissionPrompt || slices.Contains(SandboxTools, tool)
}

// toolRefuser is RunPolicy.Refuse for a run: the project's rules, then the task's narrowing. columns names the column of each
// upstream server of the run.
func toolRefuser(rules *mcp.RuleSet, narrow []string, narrowed bool, facts mcp.ToolFacts, columns map[string]string) func(server, tool string) bool {
	if rules == nil && !narrowed {
		return nil
	}
	return func(server, tool string) bool {
		if server == mcp.WorkspaceServerID && mechanismTool(tool) {
			return false
		}
		f := facts
		f.Server, f.Tool = server, tool
		if server == mcp.WorkspaceServerID {
			f.Column = workspaceColumn(tool)
		} else if f.Column = columns[server]; f.Column == "" {
			f.Column = mcp.ColOther
		}
		if rules.Refuses(f) != "" {
			return true
		}
		return narrowed && !mcp.Narrowed(narrow, mcp.ExposedName(server, tool))
	}
}

// serverColumns names the table's column of each library server (by the catalog entry it was connected from).
func (c *Core) serverColumns(ctx context.Context, specs []mcp.ServerSpec) map[string]string {
	out := map[string]string{}
	cat, _ := c.mcpCatalogSource()
	for _, sp := range specs {
		var catalogID string
		_ = c.DB.Reader().QueryRowContext(ctx, `SELECT catalog_id FROM mcp_library WHERE id=?`, sp.ID).Scan(&catalogID)
		trust := ""
		if cat != nil && catalogID != "" {
			if e, ok := cat.Get(catalogID); ok {
				trust = e.Trust
			}
		}
		out[sp.ID] = mcp.ServerColumn(catalogID, trust)
	}
	return out
}

// RolePolicyCell is one cell of the table as the page shows it.
type RolePolicyCell struct {
	Allowed bool `json:"allowed"`
	Default bool `json:"default"`
	// Changed: a person set this cell for the project (it differs from, or was set to, the default).
	Changed bool `json:"changed"`
}

// RolePolicyRow is one role of the table.
type RolePolicyRow struct {
	Role  string                    `json:"role"`
	Label string                    `json:"label"`
	Cells map[string]RolePolicyCell `json:"cells"`
}

// RolePolicyView is mcp.rolePolicy.get: the project's table, its rules, and the switches that decide whether some columns
// give anything.
type RolePolicyView struct {
	ProjectID string           `json:"projectId"`
	Columns   []mcp.RoleColumn `json:"columns"`
	Rows      []RolePolicyRow  `json:"rows"`
	Rules     []mcp.PolicyRule `json:"rules"`
	// ActTools is the project's mcp.act_tools switch: without it the act column gives nothing.
	ActTools bool `json:"actTools"`
	// RepoMap: this build has the repo map's parser (without it the repo map column gives nothing).
	RepoMap bool `json:"repoMap"`
}

func (c *Core) rolePolicyView(ctx context.Context, projectID string) (RolePolicyView, error) {
	if _, err := c.GetProject(ctx, projectID); err != nil {
		return RolePolicyView{}, err
	}
	tab := c.roleTable(ctx, projectID)
	rules, err := c.policyRules(ctx, projectID)
	if err != nil {
		return RolePolicyView{}, err
	}
	v := RolePolicyView{ProjectID: projectID, Columns: mcp.RoleColumns, Rules: rules, ActTools: c.actToolsEnabled(ctx, projectID), RepoMap: repomap.Available()}
	for _, role := range mcp.PolicyRoles {
		row := RolePolicyRow{Role: role, Label: mcp.RoleLabels[role], Cells: map[string]RolePolicyCell{}}
		for _, col := range mcp.RoleColumns {
			_, changed := tab.Overrides[role][col.Key]
			row.Cells[col.Key] = RolePolicyCell{Allowed: tab.Allowed(role, col.Key), Default: mcp.RoleDefault(role, col.Key), Changed: changed}
		}
		v.Rows = append(v.Rows, row)
	}
	return v, nil
}

// TaskToolsView is task.mcpTools.get: the tools a task can narrow to and what it narrowed to.
type TaskToolsView struct {
	TaskID string `json:"taskId"`
	// Choices are the exposed names (a server as <id>__*) the task's project offers its agents at all.
	Choices []TaskToolChoice `json:"choices"`
	// Narrowed is the task's list (nil: it does not narrow).
	Narrowed []string `json:"narrowed"`
	// AgentTools are what the task's agent gets now, with the narrowing ("" agent: the task has none).
	AgentID    string   `json:"agentId,omitempty"`
	AgentTools []string `json:"agentTools"`
}

// TaskToolChoice is one tool (or server) a task may keep.
type TaskToolChoice struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Group string `json:"group"` // workspace or server
}

var toolGlobRe = regexp.MustCompile(`^[A-Za-z0-9_.*-]{1,120}$`)

func (c *Core) taskToolsView(ctx context.Context, taskID string) (TaskToolsView, error) {
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return TaskToolsView{}, err
	}
	v := TaskToolsView{TaskID: taskID, AgentTools: []string{}}
	seen := map[string]bool{}
	add := func(name, label, group string) {
		if !seen[name] {
			seen[name] = true
			v.Choices = append(v.Choices, TaskToolChoice{Name: name, Label: label, Group: group})
		}
	}
	add("propose_memory", "propose_memory", "workspace")
	for _, col := range mcp.RoleColumns {
		for _, tool := range col.Tools {
			if tool == "repo_map" && !repomap.Available() {
				continue
			}
			add(tool, tool, "workspace")
		}
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT l.id, l.display FROM project_mcp_servers p JOIN mcp_library l ON l.id=p.server_id WHERE p.project_id=? AND p.enabled=1 ORDER BY l.id`, t.ProjectID)
	if err == nil {
		for rows.Next() {
			var id, display string
			if rows.Scan(&id, &display) == nil {
				add(id+mcp.ToolSeparator+"*", display, "server")
			}
		}
		rows.Close()
	}
	if k := c.Knowledge(); k != nil {
		if al, err := k.Packs.AllowFor(ctx, t.ProjectID, "", ""); err == nil {
			for srv := range al.Live {
				add(srv+mcp.ToolSeparator+"*", srv+" (live pack)", "server")
			}
		}
	}
	if g, ok := c.taskNarrowing(ctx, taskID); ok {
		v.Narrowed = g
	}
	if strings.HasPrefix(t.Assignee, "agt_") {
		if ag, err := c.agent(ctx, t.Assignee); err == nil {
			v.AgentID = ag.ID
			if pl := c.planMCP(ctx, ag.ID, t.ProjectID, t.ID, ag.Adapter, ag.Role, ag.Model, t.Worktree); pl.Attach {
				v.AgentTools = pl.Tools
			}
		}
	}
	return v, nil
}

func startMCPPolicy(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	audit := func(ctx context.Context, action, subject string, detail any) {
		if k := c.Knowledge(); k != nil {
			k.audit(ctx, action, subject, detail)
		}
	}
	m := map[string]Method{
		"mcp.rolePolicy.get": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return c.rolePolicyView(ctx, p.ProjectID)
		}),
		// a person changes one cell; allowed null puts it back to the table's default
		"mcp.rolePolicy.set": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			Role      string `json:"role"`
			Column    string `json:"column"`
			Allowed   *bool  `json:"allowed"`
		}) (any, error) {
			if _, err := c.GetProject(ctx, p.ProjectID); err != nil {
				return nil, err
			}
			if !mcp.KnownPolicyRole(p.Role) {
				return nil, fmt.Errorf("the table has no role %q", p.Role)
			}
			if !mcp.KnownRoleColumn(p.Column) {
				return nil, fmt.Errorf("the table has no column %q", p.Column)
			}
			if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
				if p.Allowed == nil {
					_, err := tx.ExecContext(ctx, `DELETE FROM mcp_role_policy WHERE project_id=? AND role=? AND col=?`, p.ProjectID, p.Role, p.Column)
					return err
				}
				_, err := tx.ExecContext(ctx, `INSERT INTO mcp_role_policy(project_id,role,col,allowed,updated_at,updated_by) VALUES(?,?,?,?,?,?)
ON CONFLICT(project_id,role,col) DO UPDATE SET allowed=excluded.allowed, updated_at=excluded.updated_at, updated_by=excluded.updated_by`, p.ProjectID, p.Role, p.Column, b2i(*p.Allowed), storage.Now(), c.Human())
				return err
			}); err != nil {
				return nil, err
			}
			detail := map[string]any{"role": p.Role, "column": p.Column, "allowed": p.Allowed}
			audit(ctx, "mcp.role_policy", p.ProjectID, detail)
			c.publish(ctx, eventRolePolicyChanged, p.ProjectID, detail)
			return c.rolePolicyView(ctx, p.ProjectID)
		}),
		"mcp.policyRule.add": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			Name      string `json:"name"`
			Expr      string `json:"expr"`
		}) (any, error) {
			if _, err := c.GetProject(ctx, p.ProjectID); err != nil {
				return nil, err
			}
			r := mcp.PolicyRule{ID: ids.New("mpr"), Name: strings.TrimSpace(p.Name), Expr: strings.TrimSpace(p.Expr)}
			if len(r.Name) > 120 {
				return nil, errors.New("a rule's name is at most 120 characters")
			}
			if c.Scanner != nil && c.Scanner.Redact(r.Name+"\n"+r.Expr) != r.Name+"\n"+r.Expr {
				return nil, errors.New("the rule looks like it holds a secret; rules are stored as text")
			}
			have, err := c.policyRules(ctx, p.ProjectID)
			if err != nil {
				return nil, err
			}
			if _, err := mcp.CompileRules(append(have, r)); err != nil {
				return nil, err
			}
			if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO mcp_policy_rules(id,project_id,name,expr,created_at,created_by) VALUES(?,?,?,?,?,?)`, r.ID, p.ProjectID, r.Name, r.Expr, storage.Now(), c.Human())
				return err
			}); err != nil {
				return nil, err
			}
			detail := map[string]any{"rule": r.ID, "name": r.Name, "expr": r.Expr}
			audit(ctx, "mcp.policy_rule_added", p.ProjectID, detail)
			c.publish(ctx, eventRolePolicyChanged, p.ProjectID, map[string]any{"rule": r.ID})
			return c.rolePolicyView(ctx, p.ProjectID)
		}),
		"mcp.policyRule.remove": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			ID        string `json:"id"`
		}) (any, error) {
			var n int64
			if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
				res, err := tx.ExecContext(ctx, `DELETE FROM mcp_policy_rules WHERE id=? AND project_id=?`, p.ID, p.ProjectID)
				if err == nil {
					n, _ = res.RowsAffected()
				}
				return err
			}); err != nil {
				return nil, err
			}
			if n == 0 {
				return nil, fmt.Errorf("the project has no rule %q", p.ID)
			}
			audit(ctx, "mcp.policy_rule_removed", p.ProjectID, map[string]any{"rule": p.ID})
			c.publish(ctx, eventRolePolicyChanged, p.ProjectID, map[string]any{"rule": p.ID})
			return c.rolePolicyView(ctx, p.ProjectID)
		}),
		"task.mcpTools.get": bind(func(ctx context.Context, p struct {
			TaskID string `json:"taskId"`
		}) (any, error) {
			return c.taskToolsView(ctx, p.TaskID)
		}),
		// a person narrows a task's tools to these exposed names (globs); null stops narrowing. Narrowing never widens: a name
		// the agent does not get stays out of its turns.
		"task.mcpTools.set": bind(func(ctx context.Context, p struct {
			TaskID string    `json:"taskId"`
			Tools  *[]string `json:"tools"`
		}) (any, error) {
			t, err := c.Tasks.Get(ctx, p.TaskID)
			if err != nil {
				return nil, err
			}
			var list []string
			if p.Tools != nil {
				if len(*p.Tools) > 64 {
					return nil, errors.New("a task names at most 64 tools")
				}
				list = []string{}
				for _, g := range *p.Tools {
					g = strings.TrimSpace(g)
					if !toolGlobRe.MatchString(g) {
						return nil, fmt.Errorf("%q is not a tool name (letters, digits, _ . - and * only)", g)
					}
					if !slices.Contains(list, g) {
						list = append(list, g)
					}
				}
				sort.Strings(list)
			}
			if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
				if list == nil {
					_, err := tx.ExecContext(ctx, `DELETE FROM task_mcp_tools WHERE task_id=?`, p.TaskID)
					return err
				}
				b, _ := json.Marshal(list)
				_, err := tx.ExecContext(ctx, `INSERT INTO task_mcp_tools(task_id,tools,updated_at,updated_by) VALUES(?,?,?,?)
ON CONFLICT(task_id) DO UPDATE SET tools=excluded.tools, updated_at=excluded.updated_at, updated_by=excluded.updated_by`, p.TaskID, string(b), storage.Now(), c.Human())
				return err
			}); err != nil {
				return nil, err
			}
			detail := map[string]any{"task": p.TaskID, "tools": list}
			audit(ctx, "task.mcp_tools", t.ProjectID, detail)
			b, _ := json.Marshal(detail)
			_, _ = c.Bus.Publish(ctx, events.Event{Type: eventTaskToolsChanged, ProjectID: t.ProjectID, TaskID: p.TaskID, Data: b})
			return c.taskToolsView(ctx, p.TaskID)
		}),
	}
	return m, nil, nil
}

// addLivePackServers gives a run the docs server of each live (or hybrid) pack attached for its agent: exactly the pack's tool
// of that catalog server, when the role's row reaches its column. A library server of the same id (the person connected the
// same catalog entry) keeps its own settings and gets the pack's tool added to the agent's grant.
func (c *Core) addLivePackServers(ctx context.Context, pl *mcpPlan, tab mcp.RoleTable, row string, al packs.Allowance, columns map[string]string) {
	if len(al.Live) == 0 {
		return
	}
	cat, err := c.mcpCatalogSource()
	if err != nil {
		return
	}
	ids := make([]string, 0, len(al.Live))
	for id := range al.Live {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		spec, ok := cat.Get(id)
		if !ok {
			continue // a pack names a server this build's catalog does not have: search_pack reports it unavailable
		}
		col := mcp.ServerColumn(spec.ID, spec.Trust)
		if !tab.Allowed(row, col) {
			continue
		}
		if !slices.ContainsFunc(pl.Servers, func(s mcp.ServerSpec) bool { return s.ID == id }) {
			pl.Servers = append(pl.Servers, spec)
			columns[id] = col
		}
		for _, tool := range al.Live[id] {
			if !slices.Contains(pl.Policy.Allow[id], tool) && !slices.Contains(pl.Policy.Allow[id], "*") {
				pl.Policy.Allow[id] = append(pl.Policy.Allow[id], tool)
			}
		}
	}
}

// exposedTools are the names a run's servers offer, as far as they are known before the run starts: each server's declared or
// listed tools and the tools its grant names, or <id>__* for a server whose tools are not known yet.
func exposedTools(specs []mcp.ServerSpec, allow map[string][]string) []string {
	var out []string
	for _, sp := range specs {
		n := len(out)
		for _, t := range sp.Tools {
			out = append(out, sp.ID+mcp.ToolSeparator+t.Name)
		}
		for _, g := range allow[sp.ID] {
			if !strings.Contains(g, "*") && !slices.Contains(out[n:], sp.ID+mcp.ToolSeparator+g) {
				out = append(out, sp.ID+mcp.ToolSeparator+g)
			}
		}
		if len(out) == n {
			out = append(out, sp.ID+mcp.ToolSeparator+"*")
		}
	}
	return out
}

// keepAllowed keeps the exposed names the policy allows (fetch_range always; <id>__* while the server has a grant).
func keepAllowed(p mcp.RunPolicy, names []string) []string {
	out := []string{}
	for _, n := range names {
		srv, tool, upstream := strings.Cut(n, mcp.ToolSeparator)
		switch {
		case !upstream:
			if n == mcp.ToolFetchRange || p.Allows(mcp.WorkspaceServerID, n) {
				out = append(out, n)
			}
		case tool == "*":
			if len(p.Allow[srv]) > 0 {
				out = append(out, n)
			}
		case p.Allows(srv, tool):
			out = append(out, n)
		}
	}
	return out
}
