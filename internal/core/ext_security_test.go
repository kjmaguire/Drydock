package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

// A made-up value that matches gitleaks' aws-access-token rule; not a credential.
const plantedKey = "AKIA2W7X3KQ5ZV4YBN6T"

func securityCore(t *testing.T, opts ...string) (*app, string) {
	t.Helper()
	rules := filepath.Join(t.TempDir(), "rules")
	os.MkdirAll(rules, 0o755)
	os.WriteFile(filepath.Join(rules, "go.yaml"), []byte("rules: []\n"), 0o644)
	exe := os.Args[0]
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters:  map[string]agents.Adapter{"claude": &claude.Adapter{Exe: exe}},
		ScanTools: scan.Paths{OSV: scan.Tool{Path: exe, Args: scantest.Args("osv-scanner", opts...)}, Opengrep: scan.Tool{Path: exe, Args: scantest.Args("opengrep", opts...)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}, rules
}

// Decision P44 through the UI methods: the tools, one button, the engines' states as events, the stored report and history.
func TestTheSecurityScanRunsThreeEnginesAndKeepsAReportWithoutTheSecret(t *testing.T) {
	a, rules := securityCore(t)
	repo := newRepo(t)
	os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("key = "+plantedKey+"\n"), 0o644)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)

	// what this machine has: the stand-ins are found, and the built-in rules (decision P77) make it ready without a folder of
	// the person's own
	var tools scan.Tools
	a.call("security.tools", map[string]any{"projectId": p.ID}, &tools)
	if !tools.Gitleaks.BuiltIn || !tools.OSV.Found || !tools.Opengrep.Found || tools.RulesOK || !tools.Ready || !tools.BuiltinRules || tools.BuiltinRuleCount != 34+66+49 || tools.RulesNote != "" {
		t.Fatalf("%+v", tools)
	}
	// a relative folder is refused: Opengrep runs in the repository, Drydock elsewhere
	if _, err := a.c.Call(context.Background(), "security.settings.set", []byte(`{"projectId":"`+p.ID+`","rulesDir":"rules"}`)); err == nil || !strings.Contains(err.Error(), "full path") {
		t.Fatal(err)
	}
	a.call("security.settings.set", map[string]any{"projectId": p.ID, "rulesDir": rules}, &tools)
	if !tools.RulesOK || !tools.Ready || tools.RulesDir != rules {
		t.Fatalf("%+v", tools)
	}

	// nothing scanned yet
	var cur *scan.Report
	a.call("security.scan.get", map[string]any{"projectId": p.ID}, &cur)
	if cur != nil {
		t.Fatalf("%+v", cur)
	}

	sub, stop := a.c.Bus.Subscribe(events.Filter{Types: []string{"security.*"}})
	defer stop()
	var started scan.Report
	a.call("security.scan.start", map[string]any{"projectId": p.ID}, &started)
	if started.Status != scan.StatusRunning || len(started.Engines) != 3 || started.By != "human:kyle" || started.Dir != repo {
		t.Fatalf("%+v", started)
	}
	// a second start while it runs is refused
	if _, err := a.c.Call(context.Background(), "security.scan.start", []byte(`{"projectId":"`+p.ID+`"}`)); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatal(err)
	}
	a.waitFor("the scan to finish", func() bool {
		a.call("security.scan.get", map[string]any{"projectId": p.ID}, &cur)
		return cur != nil && cur.Status == scan.StatusDone
	})
	if cur.ID != started.ID || len(cur.Findings) != 4 || cur.Counts.Total != 4 || cur.Counts.High != 3 || cur.Counts.Medium != 1 || !strings.Contains(cur.Summary, "1 secret") {
		t.Fatalf("%+v", cur)
	}
	for _, e := range cur.Engines {
		if e.Status != scan.Done {
			t.Fatalf("%+v", e)
		}
	}
	b, _ := json.Marshal(cur)
	if strings.Contains(string(b), plantedKey) {
		t.Fatal("the report carries the secret")
	}

	// the events, in order: started, one per engine state change (3 engines, running then done), finished
	seen := map[string]int{}
	var order []string
	deadline := time.After(10 * time.Second)
	for seen["security.scan.finished"] == 0 {
		select {
		case e := <-sub:
			seen[e.Type]++
			order = append(order, e.Type)
			if e.ProjectID != p.ID {
				t.Fatalf("%+v", e)
			}
		case <-deadline:
			t.Fatalf("no finished event: %v", seen)
		}
	}
	if seen["security.scan.started"] != 1 || seen["security.scan.engine"] != 6 || order[0] != "security.scan.started" {
		t.Fatalf("%v %v", seen, order)
	}

	// the history and a report by id
	var list []scan.Summary
	a.call("security.scan.list", map[string]any{"projectId": p.ID, "limit": 10}, &list)
	if len(list) != 1 || list[0].ID != cur.ID || list[0].Counts.Total != 4 || list[0].Status != scan.StatusDone || list[0].By != "human:kyle" {
		t.Fatalf("%+v", list)
	}
	var byID scan.Report
	a.call("security.scan.report", map[string]any{"id": cur.ID}, &byID)
	if byID.ID != cur.ID || len(byID.Findings) != 4 {
		t.Fatalf("%+v", byID)
	}
	// a second scan makes a second row, newest first
	a.call("security.scan.start", map[string]any{"projectId": p.ID}, &started)
	a.waitFor("the second scan", func() bool {
		a.call("security.scan.list", map[string]any{"projectId": p.ID, "limit": 10}, &list)
		return len(list) == 2
	})
	if list[0].ID != started.ID {
		t.Fatalf("%+v", list)
	}
}

func TestAScanNeedsARepositoryAndCanBeCancelled(t *testing.T) {
	a, _ := securityCore(t, "--sleep-ms=20000")
	var p core.Project
	a.call("project.create", map[string]any{"name": "norepo"}, &p)
	if _, err := a.c.Call(context.Background(), "security.scan.start", []byte(`{"projectId":"`+p.ID+`"}`)); err == nil || !strings.Contains(err.Error(), "no repository") {
		t.Fatal(err)
	}
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var rep scan.Report
	a.call("security.scan.start", map[string]any{"projectId": p.ID}, &rep)
	time.Sleep(200 * time.Millisecond)
	a.call("security.scan.cancel", map[string]any{"projectId": p.ID}, &rep)
	var cur *scan.Report
	a.waitFor("the cancelled scan to end", func() bool {
		a.call("security.scan.get", map[string]any{"projectId": p.ID}, &cur)
		return cur != nil && cur.Status != scan.StatusRunning
	})
	if cur.Status != scan.StatusCancelled || !strings.HasPrefix(cur.Summary, "Cancelled") {
		t.Fatalf("%+v", cur)
	}
	if _, err := a.c.Call(context.Background(), "security.scan.cancel", []byte(`{"projectId":"`+p.ID+`"}`)); err == nil {
		t.Fatal("cancelling nothing must say so")
	}
}

// A change to a tool's program is answered with one probe of the programs: the answer serves the caller and the reads the
// security.settings event triggers on every open Security page (toolsMemoTTL), so a slow first run of a program is paid once.
func TestAToolChoiceProbesTheProgramsOnce(t *testing.T) {
	count := filepath.Join(t.TempDir(), "runs.log")
	a, _ := securityCore(t, "--count="+count)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	runs := func() int {
		b, _ := os.ReadFile(count)
		return strings.Count(string(b), "\n")
	}
	var tools scan.Tools
	a.call("security.tools", map[string]any{"projectId": p.ID}, &tools)
	if got := runs(); got != 2 { // osv-scanner and opengrep, --version each
		t.Fatalf("the first read probed %d times, want 2", got)
	}
	// choosing a program (the stand-in itself here) probes once more, and the reads that follow the change use that answer
	a.call("security.tool.set", map[string]any{"projectId": p.ID, "tool": "osv-scanner", "path": os.Args[0]}, &tools)
	if tools.OSV.Chosen != os.Args[0] || !tools.OSV.Found {
		t.Fatalf("%+v", tools.OSV)
	}
	a.call("security.tools", map[string]any{"projectId": p.ID}, &tools)
	a.call("security.tools", map[string]any{"projectId": p.ID}, &tools)
	if got := runs(); got != 4 {
		t.Fatalf("after the choice and two reads the programs were probed %d times in all, want 4", got)
	}
	if tools.OSV.Chosen != os.Args[0] {
		t.Fatalf("the memo answered with stale tools: %+v", tools.OSV)
	}
	// forgetting the choice is a change of its own: probed once, then served (a fresh value: an omitted field is not decoded)
	var after scan.Tools
	a.call("security.tool.set", map[string]any{"projectId": p.ID, "tool": "osv-scanner", "path": ""}, nil)
	a.call("security.tools", map[string]any{"projectId": p.ID}, &after)
	if got := runs(); got != 6 || after.OSV.Chosen != "" || !after.OSV.Found {
		t.Fatalf("after Use PATH instead: %d probes (want 6), %+v", got, after.OSV)
	}
}
