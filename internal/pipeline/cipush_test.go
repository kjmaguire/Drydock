package pipeline_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/workflow"
)

func TestCIDefinitionPathsNameWhatACIServiceRuns(t *testing.T) {
	got := pipeline.CIDefinitionPaths([]string{
		"src/main.go", ".github/workflows/ci.yml", ".GitHub/Workflows/Deploy.yaml", ".github/actions/setup/action.yml", ".github/dependabot.yml",
		".github/CODEOWNERS", "docs/.github/workflows/x.yml", ".gitlab-ci.yml", "sub/Jenkinsfile", ".circleci/config.yml", "action.yml", "package.json",
		"scripts/build.sh", `.github\workflows\win.yml`, "Taskfile.yml", "my-action.yml",
	})
	want := []string{".GitHub/Workflows/Deploy.yaml", `.github\workflows\win.yml`, ".github/actions/setup/action.yml", ".github/workflows/ci.yml", ".gitlab-ci.yml",
		".circleci/config.yml", "action.yml", "sub/Jenkinsfile"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	have := map[string]bool{}
	for _, p := range got {
		have[p] = true
	}
	for _, p := range want {
		if !have[p] {
			t.Errorf("%q is a CI definition and was not named: %q", p, got)
		}
	}
	if pipeline.CIDefinitionPaths(nil) != nil || len(pipeline.CIDefinitionPaths([]string{"README.md"})) != 0 {
		t.Fatal("no CI paths in, none out")
	}
}

func TestRecordGateApprovalOnlyHandlesTheCIPushGate(t *testing.T) {
	ad, cfg := fakeEnv(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "x"}}})
	e := setup(t, cfg, ad)
	ctx := context.Background()
	if handled, err := pipeline.RecordGateApproval(ctx, e.tasks, e.task.ID, []byte(`{"gate":"repo_config_changed","fingerprint":"abc"}`), "human:kyle"); handled || err != nil {
		t.Fatalf("another gate: %v %v", handled, err)
	}
	if handled, _ := pipeline.RecordGateApproval(ctx, e.tasks, e.task.ID, []byte(`not json`), "human:kyle"); handled {
		t.Fatal("garbage handled")
	}
	if handled, _ := pipeline.RecordGateApproval(ctx, e.tasks, e.task.ID, []byte(`{"gate":"ci_push"}`), "human:kyle"); handled {
		t.Fatal("a payload without a fingerprint was taken as an approval")
	}
	handled, err := pipeline.RecordGateApproval(ctx, e.tasks, e.task.ID, []byte(`{"gate":"ci_push","fingerprint":"abc123"}`), "human:kyle")
	if !handled || err != nil {
		t.Fatalf("the CI push gate: %v %v", handled, err)
	}
	ev, _ := e.tasks.Evidence(ctx, e.task.ID)
	if len(ev) != 1 || ev[0].Kind != pipeline.EvidenceCIPushApproved || !strings.Contains(string(ev[0].Data), "abc123") {
		t.Fatalf("evidence %+v", ev)
	}
}

// A task whose branch adds a workflow is not pushed until a person has approved that push; approved, exactly that head goes.
func TestAPushThatChangesCIWaitsForAPersonAndThenPushesOnlyThatHead(t *testing.T) {
	workflowFile := "name: ci\non: push\njobs:\n  x:\n    runs-on: self-hosted\n    steps:\n      - run: echo hi\n"
	ad, cfg, _ := fakeRoles(t,
		fakeagent.Script{Turns: [][]fakeagent.Step{{
			{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}},
			{Write: &fakeagent.WriteSpec{Path: ".github/workflows/ci.yml", Content: workflowFile}},
			{Say: "implemented"}}}},
		fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	e := setup(t, cfg, ad)
	f := &githubFixture{}
	src, _ := e.prSetup(t, f)

	id := e.runPR(t, src, 2)
	e.await(t, id, workflow.StatusWaitingHuman)

	pending, err := e.eng.Pending(context.Background(), id)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending requests: %v %v", pending, err)
	}
	var gate struct {
		Gate        string   `json:"gate"`
		Kind        string   `json:"kind"`
		Paths       []string `json:"paths"`
		Fingerprint string   `json:"fingerprint"`
		Head        string   `json:"head"`
	}
	if err := json.Unmarshal(pending[0].Spec.Payload, &gate); err != nil {
		t.Fatal(err)
	}
	if gate.Gate != pipeline.GateCIPush || gate.Kind != "git_push" || len(gate.Paths) != 1 || gate.Paths[0] != ".github/workflows/ci.yml" || gate.Fingerprint == "" {
		t.Fatalf("the approval request: %+v (%s)", gate, pending[0].Spec.Title)
	}
	if !strings.Contains(pending[0].Spec.Title, ".github/workflows/ci.yml") || !strings.Contains(pending[0].Spec.Title, "self-hosted") {
		t.Fatalf("the person is not told what pushing does: %s", pending[0].Spec.Title)
	}
	task, _ := e.tasks.Get(context.Background(), e.task.ID)
	if out, err := exec.Command("git", "-C", f.bare, "rev-parse", "--verify", "-q", "refs/heads/"+task.Branch).Output(); err == nil {
		t.Fatalf("the branch was pushed before anyone approved it: %s", out)
	}
	f.mu.Lock()
	opened := f.opened
	f.mu.Unlock()
	if opened != 0 {
		t.Fatalf("a pull request was opened before the approval")
	}

	// the person approves; the core records the answer (RecordGateApproval) and then the engine runs the node again
	if _, err := pipeline.RecordGateApproval(context.Background(), e.tasks, e.task.ID, pending[0].Spec.Payload, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if err := e.answer(t, id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for f.head(t) == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	e.await(t, id, workflow.StatusWaitingHuman) // now the merge approval, after CI
	head := f.head(t)
	if head == "" || head != gate.Head {
		t.Fatalf("the pushed head %q is not the head the person approved %q", head, gate.Head)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opened != 1 {
		t.Fatalf("pull requests opened: %d", f.opened)
	}
}
