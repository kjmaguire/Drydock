package integrations

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// TestStep is one line of a Test connection report.
type TestStep struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// TestReport is the outcome of Test connection: is a token stored, is the server reachable, is the token accepted, are the
// tools Drydock needs present.
type TestReport struct {
	IntegrationID string     `json:"integrationId"`
	OK            bool       `json:"ok"`
	Steps         []TestStep `json:"steps"`
	ToolsFound    []string   `json:"toolsFound"`
	ToolsMissing  []string   `json:"toolsMissing"`
	WritesMissing []string   `json:"writesMissing"` // optional write tools the server does not offer: those outbound operations are unavailable
	Summary       string     `json:"summary"`
	At            int64      `json:"at"`
}

// Test runs the four checks and records the connection status. It reads at most one page (limit 1).
func (s *Service) Test(ctx context.Context, id string) (TestReport, error) {
	conn, err := s.st.GetConnection(ctx, id)
	if err != nil {
		return TestReport{}, err
	}
	rep := TestReport{IntegrationID: id, At: s.nowMS()}
	step := func(sid, name string, ok bool, detail string) {
		rep.Steps = append(rep.Steps, TestStep{ID: sid, Name: name, OK: ok, Detail: s.scrub(detail)})
	}
	finish := func(status, summary string) (TestReport, error) {
		rep.Summary = s.scrub(summary)
		rep.OK = status == StatusOK
		_ = s.st.SetStatus(ctx, id, status, "")
		if status != StatusOK {
			_ = s.st.SetStatus(ctx, id, status, rep.Summary)
		}
		s.emit(ctx, EventTestCompleted, conn.ProjectID, "", map[string]any{"integrationId": id, "kind": conn.Kind, "ok": rep.OK, "status": status})
		return rep, nil
	}
	ad, ok := AdapterFor(conn.Kind)
	if !ok {
		return rep, errf(ErrBadRequest, "unknown integration %q", conn.Kind)
	}
	// 1. a token is stored
	hasToken := s.hasCredential(ctx, conn)
	if !hasToken && strings.HasPrefix(conn.CredentialRef, OAuthRefPrefix) {
		step("token", "Signed in", false, "The OAuth sign-in has no usable token. Sign in to "+vendor(conn.Kind)+" again.")
		return finish(StatusUnauthenticated, "The "+vendor(conn.Kind)+" sign-in has expired or was removed.")
	}
	if !hasToken {
		step("token", "Token stored", false, "No token is stored. Paste the "+vendor(conn.Kind)+" API key or access token.")
		return finish(StatusUnauthenticated, "No token is stored for "+vendor(conn.Kind)+".")
	}
	step("token", "Token stored", true, "A token is stored in the credential store (it is not shown).")
	// 2. the server is reachable and answers tools/list with this token
	if s.o.Callers == nil {
		step("server", "Server reachable", false, "No MCP gateway is configured.")
		return finish(StatusUnreachable, "No MCP gateway is configured.")
	}
	caller, cerr := s.o.Callers.Caller(ctx, conn, false)
	if cerr != nil {
		f := Classify(conn.Kind, cerr, "")
		step("server", "Server reachable", false, f.Message)
		return finish(statusFor(f.Code), f.Message)
	}
	tctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	offered, terr := caller.Tools(tctx)
	cancel()
	if terr != nil {
		f := Classify(conn.Kind, terr, "")
		step("server", "Server reachable", false, f.Message)
		return finish(statusFor(f.Code), f.Message)
	}
	step("server", "Server reachable", true, fmt.Sprintf("%s's MCP server answered and offers %d tools.", vendor(conn.Kind), len(offered)))
	// 3. the tools are present
	resolved, missing, optMissing := resolveTools(ad.Needs(), offered)
	for _, n := range ad.Needs() {
		if t := resolved[n.Role]; t != "" {
			rep.ToolsFound = append(rep.ToolsFound, t)
		}
	}
	rep.ToolsMissing, rep.WritesMissing = missing, optMissing
	if len(missing) > 0 {
		d := "Missing tools: " + strings.Join(missing, ", ") + ". Sync cannot run until the server offers them."
		step("tools", "Tools present", false, d)
		return finish(StatusDegraded, vendor(conn.Kind)+"'s MCP server does not offer "+strings.Join(missing, ", ")+".")
	}
	d := "Found " + strings.Join(rep.ToolsFound, ", ") + "."
	if len(optMissing) > 0 {
		d += " Not offered (these outbound operations are unavailable): " + strings.Join(optMissing, ", ") + "."
	}
	step("tools", "Tools present", true, d)
	// 4. the token is valid for a real read: one item
	cl := &Client{Caller: timeoutCaller{caller, s.o.CallTimeout}, Conn: conn, Tools: resolved}
	pctx, pcancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer pcancel()
	pg, perr := ad.Page(pctx, cl, "", 1)
	if perr != nil {
		var f *Failure
		if !errors.As(perr, &f) {
			f = Classify(conn.Kind, perr, "")
		}
		step("read", "Token valid (one read)", false, f.Message)
		return finish(statusFor(f.Code), f.Message)
	}
	step("read", "Token valid (one read)", true, fmt.Sprintf("%s accepted the token and returned %d item(s).", vendor(conn.Kind), len(pg.Items)))
	return finish(StatusOK, vendor(conn.Kind)+" is connected.")
}

func statusFor(code string) string {
	switch code {
	case CodeUnauthorized:
		return StatusUnauthenticated
	case CodeSchemaDrift, CodeToolMissing:
		return StatusDegraded
	}
	return StatusUnreachable
}
