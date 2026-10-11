package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflow struct {
	On struct {
		Schedule         []map[string]string `yaml:"schedule"`
		WorkflowDispatch struct {
			Inputs map[string]struct {
				Default any `yaml:"default"`
			} `yaml:"inputs"`
		} `yaml:"workflow_dispatch"`
		Push struct {
			Paths []string `yaml:"paths"`
		} `yaml:"push"`
	} `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		RunsOn string `yaml:"runs-on"`
		Steps  []struct {
			Uses  string            `yaml:"uses"`
			Run   string            `yaml:"run"`
			If    string            `yaml:"if"`
			Shell string            `yaml:"shell"`
			Env   map[string]string `yaml:"env"`
			With  map[string]any    `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// The workflow is checked here as well as by its runs: it parses, has the triggers and runner expression it
// should, uses only the actions (at the versions) the existing CI already uses, and runs the suite with a budget and the
// version watch.
func TestContractWorkflowIsWhatTheDocsSay(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "contract.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var w workflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatalf("contract.yml does not parse: %v", err)
	}
	if len(w.On.Schedule) != 1 || len(strings.Fields(w.On.Schedule[0]["cron"])) != 5 {
		t.Errorf("a nightly schedule: %v", w.On.Schedule)
	}
	if w.On.WorkflowDispatch.Inputs["budget"].Default != "1.50" {
		t.Errorf("workflow_dispatch with a budget input: %+v", w.On.WorkflowDispatch.Inputs)
	}
	for _, p := range []string{"internal/agents/**", "internal/contract/**"} {
		if !slices.Contains(w.On.Push.Paths, p) {
			t.Errorf("push paths %v lack %s", w.On.Push.Paths, p)
		}
	}
	// a read-only token: contents, and what the gh contracts read (pull requests, check runs, comments); never a write
	for _, scope := range []string{"contents", "pull-requests", "checks", "issues"} {
		if w.Permissions[scope] != "read" {
			t.Errorf("token scope %s: %q, want read", scope, w.Permissions[scope])
		}
	}
	for scope, v := range w.Permissions {
		if v != "read" {
			t.Errorf("token scope %s is %q: the contract job only reads", scope, v)
		}
	}
	job, ok := w.Jobs["contract"]
	if !ok {
		t.Fatal("no contract job")
	}
	if job.RunsOn != `${{ fromJSON(vars.CONTRACT_RUNS_ON || '"windows-latest"') }}` {
		t.Errorf("runs-on %q", job.RunsOn)
	}
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, m := range regexp.MustCompile(`uses:\s*(\S+)`).FindAllStringSubmatch(string(ci), -1) {
		known[m[1]] = true
	}
	var uses []string
	var suite, install string
	for _, s := range job.Steps {
		if s.Uses != "" {
			uses = append(uses, s.Uses)
			if !known[s.Uses] {
				t.Errorf("%s is not an action (at that version) the existing CI uses", s.Uses)
			}
		}
		if strings.Contains(s.Run, "--install-pinned claude") {
			install = s.Run
		}
		if strings.Contains(s.Run, "--check-updates") {
			suite = s.Run
			if s.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}" || s.Env["ANTHROPIC_API_KEY"] != "${{ secrets.ANTHROPIC_API_KEY }}" {
				t.Errorf("the suite step must get the sign-in secrets: %v", s.Env)
			}
			if strings.Contains(s.Run, "${{") {
				t.Errorf("inputs must reach the script through the environment, not be pasted into it: %q", s.Run)
			}
		}
		if s.Uses == "actions/upload-artifact@v7" && (s.If != "always()" || s.With["path"] != "contract-report/") {
			t.Errorf("the report is uploaded even when a contract fails: %+v", s)
		}
	}
	want := []string{"actions/checkout@v7", "actions/setup-go@v7", "actions/setup-node@v7", "actions/upload-artifact@v7"}
	if strings.Join(uses, ",") != strings.Join(want, ",") {
		t.Errorf("actions %v, want %v", uses, want)
	}
	if install == "" || !strings.Contains(suite, "--budget-usd") || !strings.Contains(suite, "--out contract-report") {
		t.Errorf("install step %q, suite step %q", install, suite)
	}
	for _, s := range job.Steps {
		if s.Uses == "actions/setup-go@v7" && s.With["go-version-file"] != "go.mod" {
			t.Errorf("Go comes from go.mod: %v", s.With)
		}
		if s.Uses == "actions/setup-node@v7" && s.With["node-version"] != "24" {
			t.Errorf("Node 24: %v", s.With)
		}
	}
}
