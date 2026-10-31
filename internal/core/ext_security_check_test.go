package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/core"
)

// untrustedBody is the JSON inside the gateway's untrusted marking of a tool's answer.
func untrustedBody(t *testing.T, out string) map[string]any {
	t.Helper()
	i, j := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if i < 0 || j < i {
		t.Fatalf("no JSON in %q", out)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out[i:j+1]), &v); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return v
}

// Decision P131: an agent reads its project's last scan through security_findings, as the role table allows: the findings by
// engine, rule, file and line, never the key's value or a line of code; a finding a person dismissed is left out; a PM, whose
// row does not have the column, is not offered the tool.
func TestAnAgentReadsItsProjectsScanFindingsWithoutTheSecret(t *testing.T) {
	dir := t.TempDir()
	script, _ := json.Marshal(fakeagent.Script{Default: []fakeagent.Step{{Say: "ok"}}})
	sp := filepath.Join(dir, "script.json")
	os.WriteFile(sp, script, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	a, p, rep := scanned(t)
	mk := func(name, role string) core.Agent {
		var ag core.Agent
		a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: name, Adapter: "claude", Role: role, Model: "fake-1"}, &ag)
		return ag
	}
	backend, pm := mk("Backend", "backend"), mk("Planner", "pm")

	if _, got := spawnTools(t, a, pm, p.ID, "", p.RepoPath); slices.Contains(got, "security_findings") {
		t.Fatalf("a PM is not offered the findings: %v", got)
	}
	bs, got := spawnTools(t, a, backend, p.ID, "", p.RepoPath)
	if !slices.Contains(got, "security_findings") {
		t.Fatalf("a backend agent reads the findings: %v", got)
	}
	out, isErr := callTool(t, bs, "security_findings", map[string]any{})
	if isErr || strings.Contains(out, plantedKey) || !strings.Contains(out, "<untrusted") {
		t.Fatalf("%v %s", isErr, out)
	}
	body := untrustedBody(t, out)
	if body["scanId"] != rep.ID || body["total"].(float64) != 4 || len(body["findings"].([]any)) != 4 {
		t.Fatalf("%v", body)
	}
	secret := secretOf(t, rep)
	var sawSecret bool
	for _, x := range body["findings"].([]any) {
		f := x.(map[string]any)
		if f["engine"] == "secrets" {
			sawSecret = f["file"] == "notes.txt" && f["line"].(float64) == 3 && f["fingerprint"] == secret.Fingerprint
		}
	}
	if !sawSecret {
		t.Fatalf("the secret's finding names its file and line: %v", body["findings"])
	}
	// narrowed by engine and severity
	body = untrustedBody(t, first(callTool(t, bs, "security_findings", map[string]any{"engine": "deps"})))
	if body["total"].(float64) != 1 || body["findings"].([]any)[0].(map[string]any)["package"] != "golang.org/x/text" {
		t.Fatalf("%v", body)
	}
	// a dismissed finding is not shown to an agent either
	a.call("security.finding.dismiss", map[string]any{"projectId": p.ID, "fingerprint": secret.Fingerprint, "reason": "not_real"}, nil)
	body = untrustedBody(t, first(callTool(t, bs, "security_findings", map[string]any{"engine": "secrets"})))
	if body["total"].(float64) != 0 {
		t.Fatalf("%v", body)
	}
}

func first(s string, _ bool) string { return s }

// The `scan` check is a project setting a person turns on; it is off by default.
func TestTheScanCheckIsAProjectSettingOffByDefault(t *testing.T) {
	a, _ := securityCore(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var s checks.Settings
	a.call("checks.settings.get", map[string]any{"projectId": p.ID}, &s)
	if s.Scan || !s.Secrets {
		t.Fatalf("defaults %+v", s)
	}
	a.call("checks.settings.set", map[string]any{"projectId": p.ID, "secrets": true, "scan": true}, &s)
	if !s.Scan || s.Deps {
		t.Fatalf("saved %+v", s)
	}
	var again checks.Settings
	a.call("checks.settings.get", map[string]any{"projectId": p.ID}, &again)
	if !again.Scan {
		t.Fatalf("read back %+v", again)
	}
}
