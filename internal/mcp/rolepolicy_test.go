package mcp

import (
	"slices"
	"strings"
	"testing"
)

// The defaults are docs/mcp.md section 3's table, read cell by cell (repo_map is on for every role, decision P62).
func TestRoleTableDefaultsAreSectionThree(t *testing.T) {
	yes := func(role string, cols ...string) {
		t.Helper()
		for _, c := range cols {
			if !RoleDefault(role, c) {
				t.Errorf("%s: %s should be on by default", role, c)
			}
		}
	}
	no := func(role string, cols ...string) {
		t.Helper()
		for _, c := range cols {
			if RoleDefault(role, c) {
				t.Errorf("%s: %s should be off by default", role, c)
			}
		}
	}
	yes("pm", ColRequirements, ColMemory, ColPacks, ColAct, ColTrackers, ColRepoMap, ColOther)
	no("pm", ColReadRange, ColSkills, ColDocs, ColGitHub, ColSentry)
	yes("architect", ColReadRange, ColSkills, ColMemory, ColAct, ColDocs, ColGitHub, ColTrackers)
	no("architect", ColSentry)
	yes("ux designer", ColRequirements, ColPacks, ColReadRange, ColRepoMap, ColDocs, ColTrackers)
	no("ux designer", ColSkills, ColMemory, ColGitHub, ColSentry)
	for _, r := range []string{"frontend", "backend", "database"} {
		yes(r, ColReadRange, ColSkills, ColMemory, ColDocs)
		no(r, ColGitHub, ColTrackers, ColSentry)
	}
	yes("devops", ColDocs, ColGitHub, ColSentry)
	no("devops", ColTrackers)
	yes("qa", ColGitHub, ColSentry)
	no("qa", ColDocs, ColTrackers)
	yes("security", ColDocs, ColGitHub, ColSentry)
	no("security", ColTrackers)
	yes("reviewer", ColGitHub, ColAct, ColReadRange)
	no("reviewer", ColDocs, ColTrackers, ColSentry)
	for _, c := range RoleColumns {
		if !RoleDefault(RoleOther, c.Key) {
			t.Errorf("a role the table does not name keeps everything a grant gives: %s", c.Key)
		}
	}
	if len(PolicyRoles) != len(RoleLabels) {
		t.Errorf("every row needs a label")
	}
}

func TestRoleTableToolsAndOverrides(t *testing.T) {
	var tab RoleTable
	got := tab.WorkspaceTools("pm", false, true)
	if want := []string{"propose_memory", "repo_map", "search_memory", "search_pack", "search_requirements"}; !slices.Equal(sorted(got), want) {
		t.Errorf("pm: %v", got)
	}
	if got := tab.WorkspaceTools("pm", false, false); slices.Contains(got, "repo_map") {
		t.Errorf("no parser, no repo_map: %v", got)
	}
	if got := tab.WorkspaceTools("reviewer", true, true); !slices.Contains(got, "room") || !slices.Contains(got, "handoff") || slices.Contains(got, "task") {
		t.Errorf("a reviewer acts by room and handoff only: %v", got)
	}
	if got := tab.WorkspaceTools("backend", false, true); slices.Contains(got, "task") {
		t.Errorf("act tools only while the project's switch is on: %v", got)
	}
	// a person changes cells: PM may read files, the backend may not see the requirements
	tab.Overrides = map[string]map[string]bool{"pm": {ColReadRange: true}, "backend": {ColRequirements: false}, RoleOther: {ColAct: false}}
	if got := tab.WorkspaceTools("pm", false, true); !slices.Contains(got, "read_range") {
		t.Errorf("override on: %v", got)
	}
	if got := tab.WorkspaceTools("backend", false, true); slices.Contains(got, "search_requirements") || !slices.Contains(got, "propose_memory") {
		t.Errorf("override off (propose_memory stays, P92): %v", got)
	}
	if tab.Allowed("lead", ColAct) || !tab.Allowed("lead", ColDocs) {
		t.Errorf("an unnamed role reads the other row")
	}
}

func sorted(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

func TestServerColumns(t *testing.T) {
	for _, c := range []struct{ id, trust, want string }{
		{"cfdocs", TrustPublicDocs, ColDocs}, {"mslearn", TrustPublicDocs, ColDocs}, {"ctx7", TrustThirdPartyDocs, ColDocs},
		{"gh", TrustPrivateData, ColGitHub}, {"linear", TrustPrivateData, ColTrackers}, {"notion", TrustPrivateData, ColTrackers},
		{"sentry", TrustPrivateData, ColSentry}, {"cfapi", TrustPrivateData, ColOther}, {"", "", ColOther},
	} {
		if got := ServerColumn(c.id, c.trust); got != c.want {
			t.Errorf("%s: %s, want %s", c.id, got, c.want)
		}
	}
	// every catalog entry falls in a column the table names
	cat, err := LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range cat.Servers {
		if col := ServerColumn(s.ID, s.Trust); !KnownRoleColumn(col) {
			t.Errorf("%s: column %q", s.ID, col)
		}
	}
}

func TestPolicyRulesOnlyTakeAway(t *testing.T) {
	rs, err := CompileRules([]PolicyRule{
		{Name: "no trackers for QA", Expr: `role == "qa" && column == "trackers"`},
		{Name: "no writing tools outside a task", Expr: `!task && tool in ["task", "room", "handoff"]`},
		{Name: "only search on github", Expr: `server == "gh" && !tool.startsWith("search_")`},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		f    ToolFacts
		want string
	}{
		{ToolFacts{Role: "qa", Column: ColTrackers, Server: "linear", Tool: "list_issues"}, "no trackers for QA"},
		{ToolFacts{Role: "pm", Column: ColTrackers, Server: "linear", Tool: "list_issues"}, ""},
		{ToolFacts{Role: "backend", Column: ColAct, Server: WorkspaceServerID, Tool: "room"}, "no writing tools outside a task"},
		{ToolFacts{Role: "backend", Column: ColAct, Server: WorkspaceServerID, Tool: "room", Task: true}, ""},
		{ToolFacts{Role: "qa", Column: ColGitHub, Server: "gh", Tool: "get_job_logs"}, "only search on github"},
		{ToolFacts{Role: "qa", Column: ColGitHub, Server: "gh", Tool: "search_code"}, ""},
	} {
		if got := rs.Refuses(c.f); got != c.want {
			t.Errorf("%+v: refused by %q, want %q", c.f, got, c.want)
		}
	}
	if (*RuleSet)(nil).Refuses(ToolFacts{}) != "" {
		t.Error("no rules take nothing away")
	}
	for expr, want := range map[string]string{
		`role`:                    "true or false",
		`role == `:                "Syntax error",
		`unknown_var == "x"`:      "undeclared reference",
		strings.Repeat("a", 3000): "longer than",
		`  `:                      "empty",
	} {
		if _, err := CompileRules([]PolicyRule{{Name: "r", Expr: expr}}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v (want %q)", expr, err, want)
		}
	}
	if _, err := CompileRules([]PolicyRule{{Expr: "true"}}); err == nil {
		t.Error("a rule needs a name")
	}
}

func TestRefuseTakesToolsAwayInTheGatewayPolicy(t *testing.T) {
	p := RunPolicy{Allow: map[string][]string{WorkspaceServerID: {"read_range", "skills"}, "gh": {"*"}}}
	p.Refuse = func(server, tool string) bool {
		return !Narrowed([]string{"read_range", "gh__search_*"}, ExposedName(server, tool))
	}
	for _, c := range []struct {
		server, tool string
		want         bool
	}{
		{WorkspaceServerID, "read_range", true}, {WorkspaceServerID, "skills", false}, {WorkspaceServerID, "task", false},
		{"gh", "search_code", true}, {"gh", "get_me", false}, {"linear", "search_issues", false},
	} {
		if got := p.Allows(c.server, c.tool); got != c.want {
			t.Errorf("%s %s: %v", c.server, c.tool, got)
		}
	}
}
