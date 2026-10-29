package mcp

import (
	"bytes"
	"strings"
	"testing"
)

// The call a person approved is the call that goes out (decision P117): the approver is handed its own copy of the bytes, so a
// change it makes to them (or anything holding them does) never reaches the server, and the server receives exactly the
// approved arguments.
func TestTheApprovedCallIsTheCallThatIsMade(t *testing.T) {
	ts, st := startHTTPUpstream(t, "", nil)
	e := newEnv(t, func(o *Options) {
		o.Approver = approverFunc(func(r ApprovalRequest) (bool, error) {
			// approved, then the bytes it was shown are changed
			copy(r.Args, bytes.Replace(r.Args, []byte(`"a"`), []byte(`"z"`), 1))
			return true, nil
		})
	})
	priv := httpSpec("pv", ts.URL, func(s *ServerSpec) {
		s.Trust = TrustPrivateData
		s.DataClass = ContentPrivate
		s.Cache.Scope = "project"
		s.SideEffectsTools = []string{"write"}
	})
	cs := e.connect(e.startRun(RunConfig{Info: RunInfo{RunID: "r-same", ProjectID: "p"}, Servers: []ServerSpec{priv}, Policy: allow(map[string][]string{"pv": {"*"}})}))
	if r := callTool(t, cs, "pv__write", map[string]any{"text": "a"}); r.IsError {
		t.Fatalf("approved write: %s", textOf(r))
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.written) != 1 || st.written[0] != "a" {
		t.Fatalf("the server received %q, not the approved call", st.written)
	}
}

// Right before an approved call is made its target and the hash of the bytes about to be sent are compared with what was
// approved; any difference, or no approval at all, refuses it.
func TestAnApprovedCallIsComparedBeforeItIsMade(t *testing.T) {
	args := []byte(`{"text":"a"}`)
	ok := &approvedCall{server: "pv", tool: "write", hash: sha256Hex(args)}
	if why := ok.mismatch(target{server: "pv", tool: "write"}, args); why != "" {
		t.Fatalf("the approved call: %s", why)
	}
	for name, c := range map[string]struct {
		a    *approvedCall
		t    target
		args []byte
	}{
		"other bytes":  {ok, target{server: "pv", tool: "write"}, []byte(`{"text":"b"}`)},
		"other tool":   {ok, target{server: "pv", tool: "delete"}, args},
		"other server": {ok, target{server: "gh", tool: "write"}, args},
		"no approval":  {nil, target{server: "pv", tool: "write"}, args},
	} {
		if why := c.a.mismatch(c.t, c.args); why == "" {
			t.Errorf("%s was not refused", name)
		} else if c.a != nil && !strings.Contains(why, "changed after it was approved") {
			t.Errorf("%s: %s", name, why)
		}
	}
}
