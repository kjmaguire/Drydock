package pipeline_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The secrets and deps checks inside the real Verify node: real git worktree, real Go toolchain, the real gitleaks scanner,
// the scripted real-process agent (labeled stand-in) as the worker.

func tokenForTest() string { return "gh" + "p_" + "Z9q8W7e6R5t4Y3u2I1o0P9a8S7d6F5g4H3j2" }

func evidenceOf(t *testing.T, e *env, kind string) []*bool {
	t.Helper()
	ev, err := e.tasks.Evidence(context.Background(), e.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []*bool
	for _, x := range ev {
		if x.Kind == kind {
			out = append(out, x.Passed)
		}
	}
	return out
}

// A worker that commits a secret is sent back with the finding (rule, file, line) and never the value; the second turn
// removes it and the loop moves on.
func TestVerifySendsAnAddedSecretBackToTheAgentWithoutTheValue(t *testing.T) {
	tok := tokenForTest()
	leak := "package gocalc\n\nvar apiToken = \"" + tok + "\"\n"
	ad, cfg := fakeEnv(t, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "previous attempt needs changes", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "config.go", Content: "package gocalc\n\nvar apiToken = \"from-env\"\n"}}, {Say: "removed the token"}}}},
		Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Write: &fakeagent.WriteSpec{Path: "config.go", Content: leak}}, {Say: "implemented"}}},
	})
	e := setup(t, cfg, ad)
	e.run(t, 3, workflow.StatusCompleted)

	p := promptLog()
	if len(p) != 2 {
		t.Fatalf("expected 2 turns (work, then the secrets feedback), got %d", len(p))
	}
	if !strings.Contains(p[1], `"secrets" check failed`) || !strings.Contains(p[1], "config.go:3") {
		t.Fatalf("the second prompt must carry the finding with its location: %q", p[1])
	}
	for i, prompt := range p {
		if strings.Contains(prompt, tok) || strings.Contains(prompt, "Z9q8W7e6") {
			t.Fatalf("the secret reached agent prompt %d", i)
		}
	}
	sec := evidenceOf(t, e, "secrets")
	if len(sec) != 2 || sec[0] == nil || *sec[0] || sec[1] == nil || !*sec[1] {
		t.Fatalf("secrets evidence must be fail then pass: %v", sec)
	}
	ev, _ := e.tasks.Evidence(context.Background(), e.task.ID)
	for _, x := range ev {
		if strings.Contains(x.Summary, tok) || strings.Contains(string(x.Data), tok) {
			t.Fatalf("the secret reached evidence %s", x.Kind)
		}
	}
	var logs int
	for _, x := range ev {
		if x.Kind == "secrets" {
			b := readFile(t, x.Ref)
			if strings.Contains(b, tok) {
				t.Fatal("the secret reached the check log")
			}
			logs++
		}
	}
	if logs != 2 {
		t.Fatal("each secrets run keeps its log on disk")
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// With the deps check on and no osv-scanner, the check is reported as skipped (evidence with no verdict, an event), the loop
// is not failed by it, and it is never recorded as passed.
func TestVerifyReportsDepsAsSkippedWhenOSVScannerIsMissing(t *testing.T) {
	ad, cfg := fakeEnv(t, fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "done"}}})
	e := setup(t, cfg, ad)
	if err := checks.SaveSettings(context.Background(), e.db, "p1", checks.Settings{Secrets: true, Deps: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	e.deps.DepsBinary = "osv-scanner-that-is-not-installed"
	e.run(t, 3, workflow.StatusCompleted)

	deps := evidenceOf(t, e, "deps")
	if len(deps) != 1 || deps[0] != nil {
		t.Fatalf("a skipped deps check is evidence with no verdict, got %v", deps)
	}
	ev, _ := e.tasks.Evidence(context.Background(), e.task.ID)
	for _, x := range ev {
		if x.Kind == "deps" && !strings.Contains(x.Summary, "skipped") {
			t.Fatalf("summary %q", x.Summary)
		}
	}
	evs, err := e.bus.Replay(context.Background(), 0, events.Filter{Types: []string{"task.check_inconclusive"}, TaskID: e.task.ID}, 10)
	if err != nil || len(evs) != 1 || !strings.Contains(string(evs[0].Data), "osv-scanner not installed") {
		t.Fatalf("events %v %v", evs, err)
	}
	if sec := evidenceOf(t, e, "secrets"); len(sec) != 1 || sec[0] == nil || !*sec[0] {
		t.Fatalf("secrets still ran and passed: %v", sec)
	}
}

func TestVerifyHonoursTheProjectSettingToTurnSecretsOff(t *testing.T) {
	ad, cfg := fakeEnv(t, fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "done"}}})
	e := setup(t, cfg, ad)
	if err := checks.SaveSettings(context.Background(), e.db, "p1", checks.Settings{Secrets: false}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	e.run(t, 3, workflow.StatusCompleted)
	if sec := evidenceOf(t, e, "secrets"); len(sec) != 0 {
		t.Fatalf("secrets was turned off for the project: %v", sec)
	}
	if deps := evidenceOf(t, e, "deps"); len(deps) != 0 {
		t.Fatalf("deps defaults to off: %v", deps)
	}
}
