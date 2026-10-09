package agentcatalog

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/skills"
)

func TestBuiltinCatalogIsValidAndHasTheAgentsTheOwnerAskedFor(t *testing.T) {
	all, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Entry{}
	for _, e := range all {
		byID[e.ID] = e
	}
	type want struct {
		role, adapter, model, effort, mcp, skills string
	}
	for id, w := range map[string]want{
		"team-lead":           {"lead", "claude", "opus", "high", "", ""},
		"product-planner":     {"pm", "claude", "sonnet", "medium", "", ""},
		"software-architect":  {"architect", "claude", "opus", "high", "ctx7", ""},
		"ui-engineer":         {"frontend", "claude", "sonnet", "medium", "ctx7", "frontend-design,web-design-guidelines,react-best-practices,composition-patterns"},
		"ui-reviewer":         {"reviewer", "claude", "sonnet", "high", "", "web-design-guidelines"},
		"cloudflare-engineer": {"backend", "claude", "sonnet", "medium", "cfdocs", "cloudflare,workers-best-practices,wrangler,durable-objects"},
		"microsoft-engineer":  {"backend", "claude", "sonnet", "medium", "mslearn", "microsoft-docs,microsoft-code-reference"},
		"code-reviewer":       {"reviewer", "claude", "sonnet", "high", "", ""},
		"test-engineer":       {"qa", "claude", "sonnet", "medium", "", ""},
		"security-reviewer":   {"security", "claude", "opus", "high", "", ""},
		"technical-writer":    {"docs", "claude", "sonnet", "medium", "", "writing-guidelines"},
		"backend-engineer":    {"backend", "claude", "sonnet", "medium", "ctx7", ""},
		"fast-generalist":     {"backend", "gemini", "", "", "", ""},
	} {
		e, ok := byID[id]
		if !ok {
			t.Errorf("missing %s", id)
			continue
		}
		var names []string
		for _, s := range e.Skills {
			names = append(names, s.Name)
		}
		got := want{e.Role, e.Preferred.Adapter, e.Preferred.Model, e.Preferred.Effort, strings.Join(e.MCP, ","), strings.Join(names, ",")}
		if got != w {
			t.Errorf("%s: got %+v want %+v", id, got, w)
		}
	}
	if lead := byID["team-lead"]; !strings.Contains(lead.Purpose, "loop") || !strings.Contains(lead.Purpose, "Escalate") || len(lead.MCP) != 0 {
		t.Errorf("the Team Lead keeps the team on task, stops loops and escalates: %q", lead.Purpose)
	}
	if fg := byID["fast-generalist"]; fg.Preferred.PracticesOn() && fg.Preferred.Practices != nil {
		t.Error("practices are for Claude only")
	}
}

// Every reference resolves against what ships: registry ids in internal/skills/registry.yaml, MCP ids in
// internal/mcp/catalog.yaml (usable without a secret), roles Drydock recognises, purposes within the agent limit.
func TestEveryEntryReferencesWhatShips(t *testing.T) {
	all, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	regs, _ := skills.CuratedRegistries()
	regIDs := map[string]bool{}
	for _, r := range regs {
		regIDs[r.ID] = true
	}
	cat, _ := mcp.LoadBuiltin()
	cats := map[string]bool{}
	for _, c := range Categories {
		cats[c] = true
	}
	for _, e := range all {
		if !Roles[e.Role] || !cats[e.Category] {
			t.Errorf("%s: role %q category %q", e.ID, e.Role, e.Category)
		}
		if n := utf8.RuneCountInString(e.Purpose); n == 0 || n > MaxPurposeRunes {
			t.Errorf("%s: purpose is %d characters", e.ID, n)
		}
		if strings.Count(e.Description, ".") < 1 || strings.Contains(e.Summary, "\n") {
			t.Errorf("%s: summary/description", e.ID)
		}
		for _, id := range e.MCP {
			s, ok := cat.Get(id)
			if !ok || s.Transport != mcp.TransportHTTP || !(s.Auth.Kind == mcp.AuthNone || s.Auth.Optional) {
				t.Errorf("%s: mcp %s (%+v)", e.ID, id, s.Auth)
			}
		}
		for _, s := range e.Skills {
			if !regIDs[s.Registry] || skills.ValidateName(s.Name) != nil || len(s.Ref) != 40 {
				t.Errorf("%s: skill %+v", e.ID, s)
			}
		}
		if model, effort, ok := e.Setup(""); !ok || model != e.Preferred.Model || effort != e.Preferred.Effort {
			t.Errorf("%s: setup", e.ID)
		}
	}
	if len(all) < 13 {
		t.Fatalf("%d entries", len(all))
	}
}

func TestSetupPicksPreferredAlternativeOrDefaults(t *testing.T) {
	e, ok := Get("fast-generalist")
	if !ok {
		t.Fatal("missing")
	}
	if m, ef, listed := e.Setup("claude"); m != "haiku" || ef != "low" || !listed {
		t.Fatalf("claude alternative: %s %s %v", m, ef, listed)
	}
	if m, ef, listed := e.Setup("goose"); m != "" || ef != "" || listed {
		t.Fatalf("an unlisted CLI runs with its defaults: %s %s %v", m, ef, listed)
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("unknown id found")
	}
}

func TestParseRejectsBadEntries(t *testing.T) {
	refs, err := BuiltinRefs()
	if err != nil {
		t.Fatal(err)
	}
	const pin = "a18ffe2a99448ebff262fc87915b0fd14bab9b70"
	base := func(extra string) string {
		return "schema: 1\nagents:\n  - id: x-agent\n    name: X\n    role: backend\n    category: engineering\n    summary: One line.\n    description: Two words.\n    purpose: Do the thing.\n" + extra
	}
	ok := base("    preferred: {adapter: claude, model: sonnet, effort: medium}\n    mcp: [cfdocs]\n    skills: [{registry: cloudflare-skills, name: wrangler, ref: " + pin + "}]\n")
	if es, err := Parse([]byte(ok), refs); err != nil || len(es) != 1 || es[0].Tags == nil || es[0].Alternatives == nil {
		t.Fatalf("%v %+v", err, es)
	}
	for name, doc := range map[string]string{
		"unknown key":           base("    preferred: {adapter: claude}\n    color: red\n"),
		"schema":                strings.Replace(ok, "schema: 1", "schema: 2", 1),
		"duplicate id":          ok + strings.TrimPrefix(ok, "schema: 1\nagents:\n"),
		"unknown role":          strings.Replace(ok, "role: backend", "role: wizard", 1),
		"unknown category":      strings.Replace(ok, "category: engineering", "category: sales", 1),
		"two-line summary":      strings.Replace(ok, "summary: One line.", "summary: \"One\\nline\"", 1),
		"no purpose":            strings.Replace(ok, "purpose: Do the thing.", "purpose: \"\"", 1),
		"long purpose":          strings.Replace(ok, "purpose: Do the thing.", "purpose: "+strings.Repeat("x", MaxPurposeRunes+1), 1),
		"unknown adapter":       base("    preferred: {adapter: cursor}\n"),
		"claude effort":         base("    preferred: {adapter: claude, effort: ultra}\n"),
		"claude model":          base("    preferred: {adapter: claude, model: gpt-5}\n"),
		"model like a flag":     base("    preferred: {adapter: gemini, model: --yolo}\n"),
		"mode on gemini":        base("    preferred: {adapter: gemini, permission_mode: plan}\n"),
		"practices on gemini":   base("    preferred: {adapter: gemini, practices: true}\n"),
		"alt repeats":           base("    preferred: {adapter: claude}\n    alternatives: [{adapter: claude}]\n"),
		"unknown mcp":           base("    preferred: {adapter: claude}\n    mcp: [nope]\n"),
		"mcp needing a token":   base("    preferred: {adapter: claude}\n    mcp: [gh]\n"),
		"stdio template mcp":    base("    preferred: {adapter: claude}\n    mcp: [aikido]\n"),
		"unknown registry":      base("    preferred: {adapter: claude}\n    skills: [{registry: random/repo, name: x, ref: " + pin + "}]\n"),
		"branch instead of pin": base("    preferred: {adapter: claude}\n    skills: [{registry: cloudflare-skills, name: wrangler, ref: main}]\n"),
		"bad skill name":        base("    preferred: {adapter: claude}\n    skills: [{registry: cloudflare-skills, name: Wrangler, ref: " + pin + "}]\n"),
		"skill twice":           base("    preferred: {adapter: claude}\n    skills: [{registry: cloudflare-skills, name: wrangler, ref: " + pin + "}, {registry: vercel-agent-skills, name: wrangler, ref: " + pin + "}]\n"),
		"bad tag":               base("    preferred: {adapter: claude}\n    tags: [\"Not A Slug\"]\n"),
	} {
		if _, err := Parse([]byte(doc), refs); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
