package mcp

import (
	"fmt"
	"strings"

	"cel.dev/cel-go/cel"
)

// The per-role tool table of docs/mcp.md section 3, as the gateway applies it (decision P115). A run's policy is the role's
// row of the table (a project may change any cell), then the project's CEL rules and the task's narrowing, which can only take
// tools away (RunPolicy.Refuse). propose_memory is not in the table: every role may propose, a person decides (P92). repo_map is
// a column whose default is yes for every role (P62); a build without the parser offers it to nobody. The act column is
// effective only while the project's mcp.act_tools switch is on.

// Role policy columns. Each names Workspace tools or a kind of upstream server.
const (
	ColReadRange    = "read_range"
	ColRepoMap      = "repo_map"
	ColSkills       = "skills"
	ColMemory       = "search_memory"
	ColRequirements = "search_requirements"
	ColPacks        = "search_pack"
	ColAct          = "act"
	ColDocs         = "docs"
	ColGitHub       = "github"
	ColTrackers     = "trackers"
	ColSentry       = "sentry"
	ColOther        = "other"
)

// RoleOther is the row of every role the table does not name (the team lead, a technical writer, a role a person typed).
const RoleOther = "other"

// RoleColumn is one column of the table.
type RoleColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Help says in a few words what the column gives an agent.
	Help string `json:"help"`
	// Tools are the Workspace tools of the column (none for a server column).
	Tools []string `json:"tools,omitempty"`
}

// RoleColumns are the table's columns, in the order a person reads them.
var RoleColumns = []RoleColumn{
	{Key: ColReadRange, Label: "Read files", Help: "read_range: lines of a file in its working folder", Tools: []string{"read_range"}},
	{Key: ColRepoMap, Label: "Repo map", Help: "repo_map: the ranked map of the code", Tools: []string{"repo_map"}},
	{Key: ColSkills, Label: "Skills", Help: "skills: the project's accepted skills", Tools: []string{"skills"}},
	{Key: ColMemory, Label: "Memory", Help: "search_memory: the project's accepted memory", Tools: []string{"search_memory"}},
	{Key: ColRequirements, Label: "Requirements", Help: "search_requirements: the project's requirement documents", Tools: []string{"search_requirements"}},
	{Key: ColPacks, Label: "Packs", Help: "search_pack: the expert packs attached for it", Tools: []string{"search_pack"}},
	{Key: ColAct, Label: "Act", Help: "task, room, handoff (a reviewer: room and handoff), while the project lets agents act", Tools: []string{"task", "room", "handoff"}},
	{Key: ColDocs, Label: "Docs servers", Help: "documentation servers (Cloudflare docs, Microsoft Learn, Context7), and the live packs' servers"},
	{Key: ColGitHub, Label: "GitHub", Help: "the GitHub server's read tools"},
	{Key: ColTrackers, Label: "Trackers", Help: "Linear and Notion"},
	{Key: ColSentry, Label: "Sentry", Help: "Sentry"},
	{Key: ColOther, Label: "Other servers", Help: "every other server granted to the agent"},
}

// PolicyRoles are the rows, in order: the roles of section 3 (pipeline.PlanRoles) and then every other role.
var PolicyRoles = []string{"pm", "architect", "ux designer", "frontend", "backend", "database", "devops", "qa", "security", "reviewer", RoleOther}

// RoleLabels are the rows' display names.
var RoleLabels = map[string]string{
	"pm": "PM", "architect": "Architect", "ux designer": "UX Designer", "frontend": "Frontend", "backend": "Backend",
	"database": "Database", "devops": "DevOps", "qa": "QA", "security": "Security", "reviewer": "Reviewer", RoleOther: "Other roles",
}

// roleDenied lists, per role, the cells of section 3's table that are "-" (every other cell is yes). PM reads requirements,
// memory and packs only, the UX Designer requirements, packs and files; repo_map stays on for both (P62 gives it to every role).
var roleDenied = map[string][]string{
	"pm":          {ColReadRange, ColSkills, ColDocs, ColGitHub, ColSentry},
	"architect":   {ColSentry},
	"ux designer": {ColSkills, ColMemory, ColGitHub, ColSentry},
	"frontend":    {ColGitHub, ColTrackers, ColSentry},
	"backend":     {ColGitHub, ColTrackers, ColSentry},
	"database":    {ColGitHub, ColTrackers, ColSentry},
	"devops":      {ColTrackers},
	"qa":          {ColDocs, ColTrackers},
	"security":    {ColTrackers},
	"reviewer":    {ColDocs, ColTrackers, ColSentry},
}

// KnownRoleColumn reports whether key names a column.
func KnownRoleColumn(key string) bool {
	for _, c := range RoleColumns {
		if c.Key == key {
			return true
		}
	}
	return false
}

// KnownPolicyRole reports whether role is a row of the table.
func KnownPolicyRole(role string) bool {
	_, ok := RoleLabels[role]
	return ok
}

// RoleDefault is the table's cell for a row (a PolicyRoles entry; anything else reads as RoleOther) and a column.
func RoleDefault(role, col string) bool {
	for _, d := range roleDenied[role] {
		if d == col {
			return false
		}
	}
	return true
}

// RoleTable is one project's table: the defaults with the project's changes. Overrides maps role, then column, to the value
// a person set.
type RoleTable struct {
	Overrides map[string]map[string]bool
}

// Allowed is the effective cell.
func (t RoleTable) Allowed(role, col string) bool {
	if !KnownPolicyRole(role) {
		role = RoleOther
	}
	if v, ok := t.Overrides[role][col]; ok {
		return v
	}
	return RoleDefault(role, col)
}

// WorkspaceTools are the Workspace tools a row gives, from the read columns and (when act is true, the project's switch) the act
// column; propose_memory always, repo_map only when withRepoMap (a build with the parser).
func (t RoleTable) WorkspaceTools(role string, act, withRepoMap bool) []string {
	out := []string{"propose_memory"}
	for _, c := range RoleColumns {
		if len(c.Tools) == 0 || !t.Allowed(role, c.Key) {
			continue
		}
		switch c.Key {
		case ColRepoMap:
			if !withRepoMap {
				continue
			}
		case ColAct:
			if !act {
				continue
			}
			if role == "reviewer" { // a reviewer posts and hands off; it does not change its task (docs/mcp.md section 3)
				out = append(out, "room", "handoff")
				continue
			}
		}
		out = append(out, c.Tools...)
	}
	return out
}

// ServerColumn is the column that governs an upstream server, from the catalog entry it was connected from (catalogID and that
// entry's trust; "" for a server a person described): documentation servers, GitHub, the trackers, Sentry, or other.
func ServerColumn(catalogID, trust string) string {
	switch catalogID {
	case "":
		return ColOther
	case "gh":
		return ColGitHub
	case "linear", "notion":
		return ColTrackers
	case "sentry":
		return ColSentry
	}
	if trust == TrustPublicDocs || trust == TrustThirdPartyDocs {
		return ColDocs
	}
	return ColOther
}

// PolicyRule is a project's CEL rule (decision P115). Its expression is about one tool of a run; when it is true the tool is
// taken from the run. Rules can only take tools away: there is no rule that gives one.
type PolicyRule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Expr string `json:"expr"`
}

// ToolFacts are what a rule can read about a tool of a run.
type ToolFacts struct {
	Role    string // the table's row: pm ... reviewer, or other
	Column  string // the table's column of the tool
	Server  string // "workspace" for a Workspace tool, else the server's id
	Tool    string // the tool's own name (task, query-docs)
	Adapter string // the agent's CLI (claude, codex, ...)
	Task    bool   // the run works on a task
}

const (
	maxRuleExprBytes = 2 << 10
	// MaxPolicyRules is how many rules one project may keep.
	MaxPolicyRules = 32
)

// RuleSet is a project's compiled rules.
type RuleSet struct {
	rules []compiledRule
}

type compiledRule struct {
	PolicyRule
	prg cel.Program
}

func ruleEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("role", cel.StringType),
		cel.Variable("column", cel.StringType),
		cel.Variable("server", cel.StringType),
		cel.Variable("tool", cel.StringType),
		cel.Variable("adapter", cel.StringType),
		cel.Variable("task", cel.BoolType),
	)
}

// CompileRules checks and compiles rules: each needs a name and a boolean expression over role, column, server, tool, adapter
// and task, of at most 2 KiB.
func CompileRules(rules []PolicyRule) (*RuleSet, error) {
	if len(rules) > MaxPolicyRules {
		return nil, fmt.Errorf("a project keeps at most %d tool rules", MaxPolicyRules)
	}
	env, err := ruleEnv()
	if err != nil {
		return nil, err
	}
	rs := &RuleSet{}
	for _, r := range rules {
		if strings.TrimSpace(r.Name) == "" {
			return nil, fmt.Errorf("a tool rule needs a name")
		}
		if strings.TrimSpace(r.Expr) == "" {
			return nil, fmt.Errorf("rule %q: the expression is empty", r.Name)
		}
		if len(r.Expr) > maxRuleExprBytes {
			return nil, fmt.Errorf("rule %q: the expression is longer than %d bytes", r.Name, maxRuleExprBytes)
		}
		ast, iss := env.Compile(r.Expr)
		if iss != nil && iss.Err() != nil {
			return nil, fmt.Errorf("rule %q: %w", r.Name, iss.Err())
		}
		if ast.OutputType() != cel.BoolType {
			return nil, fmt.Errorf("rule %q: the expression must be true or false (it is %s)", r.Name, ast.OutputType())
		}
		prg, err := env.Program(ast, cel.CostLimit(100000))
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", r.Name, err)
		}
		rs.rules = append(rs.rules, compiledRule{r, prg})
	}
	return rs, nil
}

// Refuses reports the first rule that takes the tool away ("" when none does). A rule that fails to evaluate takes it away.
func (rs *RuleSet) Refuses(f ToolFacts) string {
	if rs == nil {
		return ""
	}
	vars := map[string]any{"role": f.Role, "column": f.Column, "server": f.Server, "tool": f.Tool, "adapter": f.Adapter, "task": f.Task}
	for _, r := range rs.rules {
		out, _, err := r.prg.Eval(vars)
		if err != nil {
			return r.Name
		}
		if b, ok := out.Value().(bool); !ok || b {
			return r.Name
		}
	}
	return ""
}

// Narrowed reports whether the exposed tool name (a Workspace tool's own name, or <server>__<tool>) is one of the globs a task
// narrowed its agent's tools to.
func Narrowed(globs []string, exposed string) bool {
	for _, g := range globs {
		if globMatch(g, exposed) {
			return true
		}
	}
	return false
}

// ExposedName is the name a tool of a server is offered under.
func ExposedName(server, tool string) string {
	if server == WorkspaceServerID {
		return tool
	}
	return server + ToolSeparator + tool
}
