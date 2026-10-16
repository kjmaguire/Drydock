package integrations

import (
	"encoding/json"
	"testing"
)

// A rule's priority is used inside the task service's range and nowhere else: a value a saved rule could not hold (it is checked
// when the rule is saved; this is the second look) leaves the integration's mapped priority alone instead of reaching the task
// service as a huge or wrapped number (audit 2026-10-16, CodeQL: a 64-bit value converted to int).
func TestARulesPriorityIsUsedOnlyInsideTheTaskServicesRange(t *testing.T) {
	for name, tc := range map[string]struct {
		params map[string]any
		want   int
	}{
		"none":                 {map[string]any{}, 7},
		"inside":               {map[string]any{"priority": 3}, 3},
		"as a JSON number":     {map[string]any{"priority": json.Number("2")}, 2},
		"as text":              {map[string]any{"priority": " 4 "}, 4},
		"the edge":             {map[string]any{"priority": 1000}, 1000},
		"the other edge":       {map[string]any{"priority": -1000}, -1000},
		"just over":            {map[string]any{"priority": 1001}, 7},
		"far over":             {map[string]any{"priority": int64(1) << 40}, 7},
		"far under":            {map[string]any{"priority": -(int64(1) << 40)}, 7},
		"a float that is huge": {map[string]any{"priority": 1e18}, 7},
		"not a number":         {map[string]any{"priority": "urgent"}, 7},
	} {
		if got := rulePriority(tc.params, 7); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
}
