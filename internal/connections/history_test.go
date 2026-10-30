package connections_test

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/connections"
)

// The capability matrix on an install's card (decision P132): what the adapter declares, with the provenance its manifest gives
// each capability (declared, or unknown where the manifest says nothing), its permission modes, and over them every cell of the
// newest Test connection run.
func TestTheCardMatrixIsTheDeclarationsWithTheNewestRunOverThem(t *testing.T) {
	svc, err := connections.NewService(connections.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := svc.Registry.Set.Get("claude")
	if !ok {
		t.Fatal("no claude manifest")
	}
	caps := manifests.DeclaredCapabilities(m)
	caps.PermissionModes = []string{"default", "plan"}
	declared := connections.CardMatrix(m, caps, nil)
	for _, k := range []string{"streaming", "interrupt", "mcp", "session_resume", "tool_call_visibility", "token_usage"} {
		c, ok := declared[k]
		if !ok || (c.Provenance != "declared" && c.Provenance != "unknown") {
			t.Fatalf("%s: %+v", k, c)
		}
	}
	if c := declared["permission_modes"]; c.Value != "default, plan" || c.Provenance != "declared" {
		t.Fatalf("the permission modes: %+v", c)
	}
	if c := declared["streaming"]; c.Value != caps.Streaming {
		t.Fatalf("streaming: %+v, declared %v", c, caps.Streaming)
	}
	seen := connections.CardMatrix(m, caps, map[string]connections.Cell{"streaming": {Value: true, Provenance: "observed"}, "mcp_http": {Value: false, Provenance: "observed"}})
	if seen["streaming"].Provenance != "observed" || seen["mcp_http"].Provenance != "observed" || seen["permission_modes"].Value != "default, plan" {
		t.Fatalf("over a run: %+v", seen)
	}
	if none := connections.CardMatrix(m, agents.Capabilities{}, nil); none["permission_modes"].Value != nil {
		t.Fatalf("an adapter without permission modes: %+v", none["permission_modes"])
	}
}

// The steps of a report, counted for the history (decision P132): a pass, a fail, anything else is skipped.
func TestAReportCountsItsSteps(t *testing.T) {
	r := connections.Report{Steps: []connections.Step{{ID: "T0", Status: connections.StepPass}, {ID: "T1", Status: connections.StepPass},
		{ID: "T2", Status: connections.StepFail}, {ID: "T3", Status: "skip"}}}
	if p, f, s := r.Counts(); p != 2 || f != 1 || s != 1 {
		t.Fatalf("%d %d %d", p, f, s)
	}
}
