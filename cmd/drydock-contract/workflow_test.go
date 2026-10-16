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
			Branches []string `yaml:"branches"`
			Paths    []string `yaml:"paths"`
		} `yaml:"push"`
	} `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		RunsOn      string            `yaml:"runs-on"`
		Environment string            `yaml:"environment"`
		Env         map[string]string `yaml:"env"`
		Steps       []struct {
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
	// a GitHub-hosted runner, named outright: a repository variable that could send this job to a self-hosted runner on a public
	// repository is not there (audit 2026-10-16, M19)
	if job.RunsOn != "windows-latest" {
		t.Errorf("runs-on %q", job.RunsOn)
	}
	// the sign-in secrets belong to an environment that only main may use, and a push runs the workflow on main only
	if job.Environment != "contract" {
		t.Errorf("environment %q: the secrets are the environment contract's", job.Environment)
	}
	if !slices.Equal(w.On.Push.Branches, []string{"main"}) {
		t.Errorf("a push runs it on %v: the environment lets main only read the secrets", w.On.Push.Branches)
	}
	for k, v := range job.Env {
		if strings.Contains(v, "secrets.") || strings.Contains(v, "github.token") {
			t.Errorf("job env %s holds a secret: every step would see it", k)
		}
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
		// no step but the suite holds a secret, and the newest releases are installed by a step that holds none (their install
		// scripts run there)
		if !strings.Contains(s.Run, "--check-updates") {
			for k, v := range s.Env {
				if strings.Contains(v, "secrets.") || strings.Contains(v, "github.token") {
					t.Errorf("step %q holds %s outside the suite step", s.Run, k)
				}
			}
			for k, v := range s.With {
				if str, ok := v.(string); ok && (strings.Contains(str, "secrets.") || strings.Contains(str, "github.token")) {
					t.Errorf("action %s gets %s", s.Uses, k)
				}
			}
		}
		if s.Uses == "actions/checkout@v7" && s.With["persist-credentials"] != false {
			t.Errorf("the checkout keeps the job's token in the workspace: %v", s.With)
		}
		if strings.Contains(s.Run, "--install-latest") {
			if s.If != "${{ inputs.latest }}" {
				t.Errorf("the install of the newest releases runs only when asked for: %q", s.If)
			}
		}
		if strings.Contains(s.Run, "--check-updates") && (strings.Contains(s.Run, "--latest ") || strings.HasSuffix(strings.TrimSpace(s.Run), "--latest") || strings.Contains(s.Run, "npm")) {
			t.Errorf("the suite step, which holds the sign-in, must not install anything: %q", s.Run)
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
