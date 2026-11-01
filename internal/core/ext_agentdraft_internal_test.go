package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// the environment the drafts are checked against: Claude Code installed (as the claude adapter lists itself), a project
// skill for reviewers and one named go-style, and a GitHub MCP server in the library
var testEnv = draftEnv{
	Adapters: []draftAdapter{
		{ID: "claude", Models: []string{"fable", "opus", "sonnet", "haiku"}, Efforts: []string{"low", "medium", "high", "xhigh", "max"},
			Modes: []string{"default", "acceptEdits", "plan", "dontAsk", "auto", "bypassPermissions"}},
		{ID: "codex", Models: []string{"gpt-x", "gpt-x-mini"}, Efforts: []string{"low", "medium", "high"}},
	},
	Skills: []draftSkill{{Name: "security-review", Description: "Finds security problems", Roles: []string{"security reviewer"}}, {Name: "go-style", Description: "Go house style"}},
	Tools:  []draftTool{{ID: "github", Display: "GitHub"}},
}

func TestRuleDraftReadsTheDescriptionsKeywords(t *testing.T) {
	for _, c := range []struct {
		desc                              string
		name, role, model, effort, mode   string
		skills, tools                     string
		whyHas                            string
	}{
		{"Reviews pull requests for security problems: secrets, SQL injection, unsafe file paths. Should never edit code, just comment. Use the best model.",
			"Security Reviewer", "security reviewer", "opus", "high", "plan", "security-review", "", `"never edit"`},
		{"Reviews every change before merge", "Code Reviewer", "reviewer", "sonnet", "high", "acceptEdits", "", "", "usual choice"},
		{"Writes the tests for the parser, cheap and fast please", "Tester", "tester", "haiku", "low", "acceptEdits", "", "", `"cheap"`},
		{"Keeps the README and docs up to date", "Docs Writer", "docs", "sonnet", "medium", "acceptEdits", "", "", ""},
		{"Plans the next milestone and writes specs", "Planner", "planner", "sonnet", "medium", "acceptEdits", "", "", ""},
		{"A team lead that coordinates the others", "Team Lead", "team lead", "sonnet", "medium", "acceptEdits", "", "", ""},
		{"Builds the React frontend", "Frontend Engineer", "frontend", "sonnet", "medium", "acceptEdits", "", "", ""},
		{"Owns the backend API in go-style, opens PRs on GitHub", "Backend Engineer", "backend", "sonnet", "medium", "acceptEdits", "go-style", "github", ""},
		{"Works on the hardest bugs", "Builder", "developer", "opus", "high", "acceptEdits", "", "", `"hardest"`},
		{"something", "Builder", "developer", "sonnet", "medium", "acceptEdits", "", "", ""},
		{"Read-only helper that explains code", "Builder", "developer", "sonnet", "medium", "plan", "", "", "Read only"},
	} {
		d := ruleDraft(c.desc, testEnv)
		got := []string{d.Name, d.Role, d.Adapter, d.Model, d.Effort, d.PermissionMode, strings.Join(d.Skills, ","), strings.Join(d.Tools, ",")}
		want := []string{c.name, c.role, "claude", c.model, c.effort, c.mode, c.skills, c.tools}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%q:\n got %v\nwant %v", c.desc, got, want)
		}
		if d.Source != "rules" || d.Why == "" || !strings.Contains(d.Why, c.whyHas) {
			t.Errorf("%q: source %q why %q", c.desc, d.Source, d.Why)
		}
		if !strings.Contains(d.Purpose, c.desc) {
			t.Errorf("%q: the instructions should carry the description: %q", c.desc, d.Purpose)
		}
	}

	// a CLI without plan is never given it; one without a model list gets none chosen
	other := draftEnv{Adapters: []draftAdapter{{ID: "gemini", Modes: []string{"default", "yolo"}}}}
	d := ruleDraft("never edit, best model", other)
	if d.Adapter != "gemini" || d.PermissionMode != "" || d.Model != "" {
		t.Fatalf("gemini draft %+v", d)
	}
}

func TestModelDraftIsCheckedAgainstWhatIsInstalled(t *testing.T) {
	ctx := context.Background()
	// TEST DOUBLE (labelled): the model call answers with fixed text; it records what it was asked
	var asked []string
	double := func(answer string, err error) askModel {
		return func(_ context.Context, system, user string) (string, error) {
			asked = append(asked, system+"\n"+user)
			return answer, err
		}
	}

	// a good answer (in a fence, as models do) is kept, with what the PC does not have dropped
	d := draftWith(ctx, double("```json\n"+`{"name":"Security Reviewer","role":"Security Reviewer","adapter":"claude","model":"opus","effort":"high","permissionMode":"plan",
		"purpose":"You review diffs for security problems.","skills":["security-review","nonesuch"],"tools":["github","jira"],"why":"Read only, because you said never edit."}`+"\n```", nil),
		"Reviews PRs for security, never edits, best model", testEnv)
	if d.Source != "model" || d.Name != "Security Reviewer" || d.Role != "security reviewer" || d.Model != "opus" || d.Effort != "high" || d.PermissionMode != "plan" ||
		strings.Join(d.Skills, ",") != "security-review" || strings.Join(d.Tools, ",") != "github" || d.Why == "" {
		t.Fatalf("model draft %+v", d)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "claude: models [fable, opus, sonnet, haiku]") || !strings.Contains(asked[0], "Reviews PRs for security") || !strings.Contains(asked[0], "go-style") {
		t.Fatalf("the prompt lists what is installed and the description: %q", asked)
	}

	// values the CLI does not accept are dropped; a mode that skips approvals is never drafted
	d = draftWith(ctx, double(`{"name":"Fixer","role":"backend","adapter":"claude","model":"--evil","effort":"ultra","permissionMode":"bypassPermissions","purpose":"x"}`, nil), "fix", testEnv)
	if d.Source != "model" || d.Model != "" || d.Effort != "" || d.PermissionMode != "acceptEdits" {
		t.Fatalf("clamped %+v", d)
	}

	// no model, an answer that is not JSON, a draft with no name, or a CLI that is not installed: the rules write it
	for name, ask := range map[string]askModel{
		"no model":      double("", errors.New("no candidate providers or models are registered")),
		"not JSON":      double("Sure! Here is your agent.", nil),
		"no name":       double(`{"name":"","role":"backend"}`, nil),
		"not installed": double(`{"name":"X","role":"backend","adapter":"aider"}`, nil),
		"nil":           nil,
	} {
		d := draftWith(ctx, ask, "Writes the tests, cheap", testEnv)
		if d.Source != "rules" || d.Name != "Tester" || d.Model != "haiku" {
			t.Errorf("%s: %+v", name, d)
		}
	}
}
