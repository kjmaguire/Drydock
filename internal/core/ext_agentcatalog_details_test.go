package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

// Opening a prebuilt agent shows what it comes with (decision P43): each skill's description from its SKILL.md at the pin,
// the source repository, a link to the skill at the pinned commit and its license; each MCP server's full name, publisher and
// logo instead of a bare id. No project is needed, and nothing is fetched.
func TestPrebuiltAgentListSaysWhatEachAgentComesWith(t *testing.T) {
	a := open(t, "")
	var list []core.PrebuiltAgent
	a.call("agentcatalog.list", nil, &list)
	byID := map[string]core.PrebuiltAgent{}
	for _, e := range list {
		byID[e.ID] = e
		for _, s := range e.Availability.Skills {
			if s.Description == "" || s.License == "" || s.Repo == "" || !strings.HasPrefix(s.SourceURL, "https://github.com/"+s.Repo+"/tree/"+s.Ref+"/") {
				t.Errorf("%s: skill %s: %+v", e.ID, s.Name, s)
			}
		}
		for _, m := range e.Availability.MCP {
			if m.Display == "" || m.Display == m.ID || m.Publisher == "" || m.Brand == "" || m.Summary == "" {
				t.Errorf("%s: mcp %s: %+v", e.ID, m.ID, m)
			}
		}
	}
	ui := byID["ui-engineer"]
	if len(ui.Availability.Skills) != 4 || ui.Availability.Skills[0].Name != "frontend-design" || ui.Availability.Skills[0].Repo != "anthropics/skills" ||
		ui.Availability.Skills[0].License != "Apache-2.0" || !strings.Contains(ui.Availability.Skills[0].Description, "visual design") {
		t.Fatalf("ui-engineer skills: %+v", ui.Availability.Skills)
	}
	if m := ui.Availability.MCP; len(m) != 1 || m[0].ID != "ctx7" || m[0].Display != "Context7" || m[0].Publisher != "Upstash" || m[0].Brand != "context7" || m[0].Auth != "bearer" {
		t.Fatalf("ui-engineer mcp: %+v", m)
	}
	if m := byID["microsoft-engineer"].Availability.MCP; len(m) != 1 || m[0].Display != "Microsoft Learn" || m[0].Brand != "microsoft" {
		t.Fatalf("microsoft-engineer mcp: %+v", m)
	}
	if ui.Preferred.Adapter == "" || ui.Preferred.Model == "" {
		t.Fatalf("model and CLI: %+v", ui.Preferred)
	}
}
