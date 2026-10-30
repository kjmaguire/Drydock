package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// memFindings is a labelled stand-in for the core's security findings (internal/core ext_security_check.go): it records the
// query and answers with one finding.
type memFindings struct{ got []string }

func (m *memFindings) Findings(_ context.Context, run RunInfo, q FindingsQuery) (any, error) {
	m.got = append(m.got, fmt.Sprintf("%s %s %s %s %d", run.ProjectID, q.Engine, q.MinSeverity, q.Path, q.Limit))
	return map[string]any{"total": 1, "findings": []map[string]any{{"engine": "sast", "file": "calc.go", "line": 3}}}, nil
}

// security_findings reads through its provider with the query as given; it is not available without one, and its arguments
// are checked against the schema.
func TestSecurityFindingsToolReadsThroughItsProvider(t *testing.T) {
	_, cs, _, _ := wsEnv(t, Workspace{}, nil)
	if r := callTool(t, cs, "security_findings", map[string]any{}); !r.IsError || !strings.Contains(textOf(r), "not available") {
		t.Fatalf("without a provider: %s", textOf(r))
	}
	mf := &memFindings{}
	_, cs, _, _ = wsEnv(t, Workspace{Security: mf}, nil)
	if r := callTool(t, cs, "security_findings", map[string]any{"engine": "sast", "min_severity": "high", "path": "src", "limit": 5}); r.IsError || !strings.Contains(textOf(r), `"calc.go"`) {
		t.Fatalf("query: %s", textOf(r))
	}
	if r := callTool(t, cs, "security_findings", map[string]any{}); r.IsError {
		t.Fatalf("defaults: %s", textOf(r))
	}
	for _, bad := range []map[string]any{{"limit": 500}, {"engine": "everything"}, {"min_severity": "urgent"}} {
		if r := callTool(t, cs, "security_findings", bad); !r.IsError || !strings.Contains(textOf(r), "invalid arguments") {
			t.Errorf("%v: %s", bad, textOf(r))
		}
	}
	if want := "p sast high src 5|p    50"; strings.Join(mf.got, "|") != want {
		t.Fatalf("provider calls %q", mf.got)
	}
}
