package connections

import (
	"strings"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

// Counts is how many of the report's steps passed, failed and were skipped (for the conformance history, decision P132).
func (r Report) Counts() (passed, failed, skipped int) {
	for _, s := range r.Steps {
		switch s.Status {
		case StepPass:
			passed++
		case StepFail:
			failed++
		default:
			skipped++
		}
	}
	return passed, failed, skipped
}

// CardMatrix is the capability matrix an install's card shows (decision P132): what the adapter declares (its capabilities,
// with the provenance its manifest gives each, as a Test connection's report starts from), its permission modes, and over them
// the cells of the newest Test connection run, which saw some of them (observed). last is nil when the install was never tested.
func CardMatrix(m *manifests.Manifest, caps agents.Capabilities, last map[string]Cell) map[string]Cell {
	r := Report{Capabilities: caps, Matrix: map[string]Cell{}}
	fillMatrix(&r, m, nil)
	if len(caps.PermissionModes) > 0 {
		r.Matrix["permission_modes"] = Cell{strings.Join(caps.PermissionModes, ", "), "declared"}
	}
	for k, c := range last {
		r.Matrix[k] = c
	}
	return r.Matrix
}
