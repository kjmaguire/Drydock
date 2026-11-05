package core

import "testing"

// C3: approval.get names a workflow request's gate, so the inbox can answer it with the task page's panel.
func TestPayloadGateNamesTheWorkflowRequestsGate(t *testing.T) {
	for in, want := range map[string]string{
		`{"gate":"ci","pr":4}`:               "ci",
		`{"gate":"merge","state":"blocked"}`: "merge",
		`{"gate":"repo_config_changed"}`:     "repo_config_changed",
		`{"phase":"limit","attempts":5}`:     "",
		`{"gate":3}`:                         "",
		`not json`:                           "",
		``:                                   "",
	} {
		if got := payloadGate([]byte(in)); got != want {
			t.Errorf("payloadGate(%q) = %q, want %q", in, got, want)
		}
	}
}
