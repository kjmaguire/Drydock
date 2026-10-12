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

// TestReport is the outcome of Test connection: is a credential stored, does the vendor's API accept it and whom does it reach,
// and can it read an assigned project's items.
type TestReport struct {
	IntegrationID string     `json:"integrationId"`
	OK            bool       `json:"ok"`
	Steps         []TestStep `json:"steps"`
	Identity      Identity   `json:"identity"`
	Summary       string     `json:"summary"`
	At            int64      `json:"at"`
}

// Test checks a connection and records its status and the account it reaches. It reads at most one item.
func (s *Service) Test(ctx context.Context, id string) (TestReport, error) {
	conn, err := s.st.GetConnection(ctx, id)
	if err != nil {
		return TestReport{}, err
	}
	rep := TestReport{IntegrationID: id, At: s.nowMS()}
	v := vendor(conn.Kind)
	step := func(sid, name string, ok bool, detail string) {
		rep.Steps = append(rep.Steps, TestStep{ID: sid, Name: name, OK: ok, Detail: s.scrub(detail)})
	}
	finish := func(status, summary string) (TestReport, error) {
		rep.Summary = s.scrub(summary)
		rep.OK = status == StatusOK
		detail := ""
		if !rep.OK {
			detail = rep.Summary
		}
		_ = s.st.SetStatus(ctx, id, status, detail)
		s.emit(ctx, EventTestCompleted, "", "", map[string]any{"integrationId": id, "kind": conn.Kind, "ok": rep.OK, "status": status})
		return rep, nil
	}
	failed := func(sid, name string, err error) (TestReport, error) {
		var f *Failure
		if !errors.As(err, &f) {
			f = Classify(conn.Kind, err, "")
		}
		step(sid, name, false, f.Message)
		return finish(statusFor(f.Code), f.Message)
	}
	ad, ok := AdapterFor(conn.Kind)
	if !ok {
		return rep, errf(ErrBadRequest, "unknown integration %q", conn.Kind)
	}
	// 1. a credential is stored (GitHub: gh holds it)
	if conn.Kind != KindGitHub {
		if !s.hasCredential(ctx, conn) {
			if strings.HasPrefix(conn.CredentialRef, OAuthRefPrefix) {
				step("credential", "Signed in", false, "The sign-in has no usable token. Reconnect "+v+".")
				return finish(StatusUnauthenticated, "The "+v+" sign-in has expired or was removed.")
			}
			step("credential", "Credential stored", false, "No credential is stored. Reconnect "+v+" with a "+tokenNoun(conn.Kind)+".")
			return finish(StatusUnauthenticated, "No credential is stored for "+v+".")
		}
		step("credential", "Credential stored", true, authMethod(conn)+", in the credential store (never shown).")
	}
	// 2. the API accepts it, and says whom it reaches
	api, fail := s.api(ctx, conn)
	if fail != nil {
		return failed("identity", "Accepted by "+v, fail)
	}
	ictx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	ident, ierr := ad.Identity(ictx, api)
	cancel()
	if ierr != nil {
		return failed("identity", "Accepted by "+v, ierr)
	}
	ident.Account = s.san.Line(ctx, ident.Account, 120).Text
	ident.Workspace = s.san.Line(ctx, ident.Workspace, 200).Text
	ident.WorkspaceURL = validHTTPURL(ident.WorkspaceURL)
	if ident.Method == "" {
		ident.Method = authMethod(conn)
	}
	if len(ident.Scopes) == 0 {
		ident.Scopes = s.credentialScopes(ctx, conn)
	}
	for i, sc := range ident.Scopes {
		ident.Scopes[i] = clampText(sc, 60)
	}
	if len(ident.Scopes) > 40 {
		ident.Scopes = ident.Scopes[:40]
	}
	rep.Identity = ident
	_ = s.st.SetIdentity(ctx, id, ident)
	who := strings.TrimSpace(strings.Join(nonEmptyAll(ident.Account, ident.Workspace), " in "))
	if who == "" {
		who = "the account"
	}
	step("identity", "Accepted by "+v, true, fmt.Sprintf("%s accepted the credential: %s.", v, who))
	// 3. one read in an assigned project's scope
	asgs, err := s.st.ListAssignments(ctx, id)
	if err != nil {
		return rep, err
	}
	var asg *Assignment
	for i := range asgs {
		if asgs[i].Enabled {
			asg = &asgs[i]
			break
		}
	}
	if asg == nil {
		step("read", "Read one item", true, "Not assigned to a project yet, so there is nothing to read. Assign it to a project to import "+itemsWord(conn.Kind)+".")
		return finish(StatusOK, v+" is connected.")
	}
	pctx, pcancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer pcancel()
	pg, perr := ad.Page(pctx, api, PageQuery{Config: asg.Config, Limit: 1})
	if perr != nil {
		return failed("read", "Read one item", perr)
	}
	step("read", "Read one item", true, fmt.Sprintf("%s returned %d item(s) for the first assigned project.", v, len(pg.Items)))
	return finish(StatusOK, v+" is connected.")
}

func itemsWord(kind string) string {
	for _, k := range Catalog(nil) {
		if k.Kind == kind {
			return k.Items
		}
	}
	return "items"
}

func nonEmptyAll(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func statusFor(code string) string {
	switch code {
	case CodeUnauthorized:
		return StatusUnauthenticated
	case CodeSchemaDrift, CodeToolMissing, CodeNotFound, CodeForbidden:
		// a forbidden read is a missing scope, not a refused credential: the connection is degraded, not paused
		return StatusDegraded
	}
	return StatusUnreachable
}
