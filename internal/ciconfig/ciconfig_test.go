// Package ciconfig holds the test that keeps the repository's own CI definitions to the rules the audit of 2026-10-16 set
// (M19, L49, L50, L52): hosted runners named outright, a read-only token that no checkout keeps, a time limit on every job, no
// step that hides a failed command, and the automatic security checks being there.
package ciconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflowFile struct {
	Permissions any `yaml:"permissions"`
	Jobs        map[string]struct {
		RunsOn         any            `yaml:"runs-on"`
		TimeoutMinutes any            `yaml:"timeout-minutes"`
		Permissions    map[string]any `yaml:"permissions"`
		Defaults       struct {
			Run struct {
				Shell string `yaml:"shell"`
			} `yaml:"run"`
		} `yaml:"defaults"`
		Steps []struct {
			Name  string         `yaml:"name"`
			Uses  string         `yaml:"uses"`
			Run   string         `yaml:"run"`
			Shell string         `yaml:"shell"`
			With  map[string]any `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		if p := filepath.Dir(d); p != d {
			d = p
			continue
		}
		t.Fatal("no go.mod above the test")
	}
}

func workflows(t *testing.T) map[string]workflowFile {
	t.Helper()
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	out := map[string]workflowFile{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var w workflowFile
		if err := yaml.Unmarshal(b, &w); err != nil {
			t.Fatalf("%s does not parse: %v", f, err)
		}
		out[filepath.Base(f)] = w
	}
	return out
}

var hostedRunners = map[string]bool{"windows-latest": true, "ubuntu-latest": true}

// Every job runs on a GitHub-hosted runner named outright (a pull request from a fork runs these workflows, so a self-hosted
// runner or a variable that could name one is an open door), has a time limit, and no checkout leaves the job's token in the
// workspace; every workflow starts from a token that only reads.
func TestEveryWorkflowRunsOnHostedRunnersWithAReadOnlyTokenAndATimeLimit(t *testing.T) {
	for name, w := range workflows(t) {
		perms, ok := w.Permissions.(map[string]any)
		if !ok {
			t.Errorf("%s: no permissions: block at the top (the token's default is wider than any job needs)", name)
		}
		for scope, v := range perms {
			if v != "read" {
				t.Errorf("%s: the token's scope %s is %v at the top level; a job that needs more says so itself", name, scope, v)
			}
		}
		for id, j := range w.Jobs {
			if r, ok := j.RunsOn.(string); !ok || !hostedRunners[r] {
				t.Errorf("%s: job %s runs on %v: only windows-latest or ubuntu-latest, named outright", name, id, j.RunsOn)
			}
			if j.TimeoutMinutes == nil {
				t.Errorf("%s: job %s has no timeout-minutes (a hung job runs for six hours)", name, id)
			}
			for _, s := range j.Steps {
				if strings.HasPrefix(s.Uses, "actions/checkout@") && s.With["persist-credentials"] != false {
					t.Errorf("%s: job %s: the checkout keeps the token in the workspace (persist-credentials: false)", name, id)
				}
			}
		}
	}
}

var nativeLine = regexp.MustCompile(`(?m)^\s*(go|gcc|git|npm|npx|choco|node|wails3|task)\s`)

// A step of several commands in PowerShell, the default shell on Windows, ends with the exit code of its last command: a failed
// `go vet` followed by a passing `go test` is a green step. The steps of ci.yml and verify.yml that run more than one program
// stop at the first failure (audit 2026-10-16, L49). windows-probes.yml records what each probe does and is read from its logs.
func TestAStepOfSeveralCommandsDoesNotHideAFailedOne(t *testing.T) {
	for name, w := range workflows(t) {
		if name == "windows-probes.yml" {
			continue
		}
		for id, j := range w.Jobs {
			windows := j.RunsOn == "windows-latest"
			for _, s := range j.Steps {
				shell := s.Shell
				if shell == "" {
					shell = j.Defaults.Run.Shell
				}
				if !windows || (shell != "" && shell != "pwsh") || s.Run == "" {
					continue
				}
				if len(nativeLine.FindAllString(s.Run, -1)) < 2 {
					continue
				}
				if !strings.Contains(s.Run, "$PSNativeCommandUseErrorActionPreference = $true") {
					t.Errorf("%s: job %s: the step %q runs several programs in PowerShell and a failed one can be hidden by the next:\n%s", name, id, s.Name, s.Run)
				}
			}
		}
	}
}

// The repository's own security checks exist: a vulnerability watch and code scanning in CI, the dependency bot, the policy.
func TestTheSecurityChecksAndPolicyArePresent(t *testing.T) {
	root := repoRoot(t)
	for _, f := range []string{"SECURITY.md", ".github/dependabot.yml", ".github/workflows/security.yml"} {
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil || st.Size() < 200 {
			t.Errorf("%s is missing or empty: %v", f, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"govulncheck", "npm audit", "github/codeql-action/init", "github/codeql-action/analyze"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("security.yml does not run %s", want)
		}
	}
	d, err := os.ReadFile(filepath.Join(root, ".github", "dependabot.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, eco := range []string{"gomod", "npm", "github-actions"} {
		if !strings.Contains(string(d), "package-ecosystem: "+eco) {
			t.Errorf("dependabot does not watch %s", eco)
		}
	}
}
