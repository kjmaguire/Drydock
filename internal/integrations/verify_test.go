package integrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

// Check against your account (decision P59), run against the PROTOCOL FIXTURES (test doubles of the vendors' APIs): what the
// check calls and reports, that it writes nothing to the vendor and stores nothing it read, and how it fails on a refused
// credential and on answers in a shape Drydock does not expect.

func verify(t *testing.T, h *harness, id string) integrations.VerifyReport {
	t.Helper()
	rep, err := h.svc.Verify(context.Background(), human, id)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return rep
}

func endpoints(rep integrations.VerifyReport) []string {
	var out []string
	for _, c := range rep.Calls {
		out = append(out, c.Endpoint)
	}
	return out
}

func TestVerifyReadsTheAccountWithoutStoringOrWritingAnything(t *testing.T) {
	h := newHarness(t, nil)
	fx, url := linearFixture(t, 3)
	c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})
	rep := verify(t, h, c.ID)
	if !rep.OK || len(rep.Mismatches) != 0 {
		t.Fatalf("%+v", rep)
	}
	if !rep.Read.AccountNamed || !rep.Read.WorkspaceNamed || rep.Read.Choices != 2 || rep.Read.Items != 3 || rep.Read.Assignments != 1 || rep.Read.MorePages {
		t.Fatalf("%+v", rep.Read)
	}
	want := []string{"POST /graphql (DrydockIdentity)", "POST /graphql (DrydockTeams)", "POST /graphql (DrydockIssues)"}
	if strings.Join(endpoints(rep), "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v", endpoints(rep))
	}
	for _, cl := range rep.Calls {
		if !cl.OK || cl.Status != 200 {
			t.Fatalf("%+v", cl)
		}
	}
	var ids []string
	for _, st := range rep.Steps {
		ids = append(ids, st.ID)
		if !st.OK {
			t.Fatalf("%+v", st)
		}
	}
	if strings.Join(ids, ",") != "credential,identity,choices,read" {
		t.Fatalf("steps %v", ids)
	}
	if !strings.Contains(rep.Summary, "Linear answered every check (3 calls, 3 items read)") {
		t.Fatal(rep.Summary)
	}
	// one page of the assigned scope, nothing written back, nothing stored
	if args := fx.ListArgs(); len(args) != 1 || args[0]["team"] != "ENG" || args[0]["first"] != 25 {
		t.Fatalf("%v", args)
	}
	if len(fx.CommentsSent()) != 0 || len(fx.Updates) != 0 || fx.Calls("commentCreate") != 0 || fx.Calls("issueUpdate") != 0 {
		t.Fatal("the check wrote to the vendor")
	}
	if n := len(h.items(c.ID)); n != 0 {
		t.Fatalf("the check stored %d items", n)
	}
	if n := h.count(`SELECT COUNT(*) FROM integration_cursors WHERE integration_id=?`, c.ID); n != 0 {
		t.Fatalf("the check moved a cursor (%d)", n)
	}
	// counts only: no item text, no names, no credential, in the report, the record or the event
	for _, s := range []string{jsonOf(rep), h.allEventsJSON(), h.diskText()} {
		for _, bad := range []string{linearToken, "Issue number", "Description of issue", "Kyle Fixture", "Fixture Org", "Engineering"} {
			if strings.Contains(s, bad) {
				t.Fatalf("%q leaked:\n%s", bad, s)
			}
		}
	}
	// the card shows when it was verified; the event carries counts
	v, err := h.svc.Get(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Verified == nil || !v.Verified.OK || v.Verified.At != baseTime.UnixMilli() || v.Verified.Calls != 3 || v.Verified.Items != 3 || v.Verified.Summary != rep.Summary {
		t.Fatalf("%+v", v.Verified)
	}
	evs := h.events(integrations.EventVerified)
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), `"ok":true`) || !strings.Contains(string(evs[0].Data), `"items":3`) {
		t.Fatalf("%v", evs)
	}
	// the check is the person's: an agent cannot run it
	if _, err := h.svc.Verify(context.Background(), "agent:dev", c.ID); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatalf("%v", err)
	}
}

func TestVerifyWithoutAnAssignmentReadsWhatTheCredentialSees(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	_, url := linearFixture(t, 30)
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindLinear, Token: linearToken, BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	rep := verify(t, h, c.ID)
	if !rep.OK || rep.Read.Items != 25 || !rep.Read.MorePages || rep.Read.Assignments != 1 {
		t.Fatalf("%+v", rep)
	}
	if !strings.Contains(strings.Join(rep.Notes, " "), "Not assigned to a project yet: the issues were read from everything the credential can see.") {
		t.Fatalf("%v", rep.Notes)
	}

	// Sentry needs an organization: without an assignment the first one listed is read, and one item's latest event
	sfx, surl := sentryFixture(t)
	putSentry(sfx, "s1", "error", "web", 10, 1)
	putSentry(sfx, "s2", "error", "web", 10, 2)
	sc, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindSentry, Token: sentryToken, BaseURL: surl})
	if err != nil {
		t.Fatal(err)
	}
	srep := verify(t, h, sc.ID)
	if !srep.OK || srep.Read.Items != 2 || !srep.Read.DetailRead || srep.Read.Scopes != 4 || srep.Read.Choices != 1 {
		t.Fatalf("%+v", srep)
	}
	got := strings.Join(endpoints(srep), "|")
	for _, want := range []string{"GET /api/0/", "GET /api/0/organizations/", "GET /api/0/organizations/acme/issues/", "/events/latest/"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%s not in %s", want, got)
		}
	}
	if len(srep.Calls) > 6 {
		t.Fatalf("not a handful of calls: %v", got)
	}
	if strings.Contains(jsonOf(srep), "ZeroDivisionError") || strings.Contains(jsonOf(srep), "TypeError in checkout") {
		t.Fatal("the report carries item text")
	}
	if len(sfx.Updates) != 0 {
		t.Fatal("the check wrote to Sentry")
	}
}

func TestVerifyReportsARefusedCredential(t *testing.T) {
	h := newHarness(t, nil)
	fx, url := linearFixture(t, 3)
	c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})
	fx.Revoke(true)
	rep := verify(t, h, c.ID)
	if rep.OK || len(rep.Calls) != 1 || rep.Calls[0].Status != 401 || rep.Calls[0].OK {
		t.Fatalf("%+v", rep)
	}
	last := rep.Steps[len(rep.Steps)-1]
	if last.ID != "identity" || last.OK || !strings.Contains(last.Detail, "HTTP 401") {
		t.Fatalf("%+v", rep.Steps)
	}
	if !strings.Contains(rep.Summary, "Linear did not pass the check: Linear rejected the credential (HTTP 401") {
		t.Fatal(rep.Summary)
	}
	v, _ := h.svc.Get(context.Background(), c.ID)
	if v.Verified == nil || v.Verified.OK || v.Verified.FailedCalls != 1 {
		t.Fatalf("%+v", v.Verified)
	}
	if evs := h.events(integrations.EventVerified); len(evs) != 1 || !strings.Contains(string(evs[0].Data), `"ok":false`) {
		t.Fatalf("%v", evs)
	}
	// a later check that passes replaces the record
	fx.Revoke(false)
	if rep := verify(t, h, c.ID); !rep.OK {
		t.Fatalf("%+v", rep)
	}
	if v, _ := h.svc.Get(context.Background(), c.ID); v.Verified == nil || !v.Verified.OK {
		t.Fatalf("%+v", v.Verified)
	}
}

func TestVerifyReportsAnswersInAnUnexpectedShape(t *testing.T) {
	h := newHarness(t, nil)
	fx, url := linearFixture(t, 3)
	c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})

	// a field of another JSON type: the field is named, its value is not quoted
	fx.FailNext(itfixture.Fail{Status: 200, Body: `{"data":{"issues":{"nodes":"SECRET-LOOKING-VALUE","pageInfo":{"hasNextPage":false}}}}`})
	rep := verify(t, h, c.ID)
	if rep.OK || len(rep.Mismatches) != 1 {
		t.Fatalf("%+v", rep)
	}
	if m := rep.Mismatches[0]; !strings.Contains(m, `Read issues: the field "issues.nodes" is a JSON string where Drydock expects a list`) {
		t.Fatal(m)
	}
	if strings.Contains(jsonOf(rep), "SECRET-LOOKING-VALUE") {
		t.Fatal("the report quotes the answer")
	}
	if !strings.Contains(rep.Summary, "answered in a shape Drydock does not recognize") {
		t.Fatal(rep.Summary)
	}

	// an answer without the part Drydock reads
	fx.WrongShape = true
	rep = verify(t, h, c.ID)
	if rep.OK || len(rep.Mismatches) != 1 || !strings.Contains(rep.Mismatches[0], "no issues connection in the answer") {
		t.Fatalf("%+v", rep)
	}
	fx.WrongShape = false

	// items missing what Drydock needs: a title and an update time
	fx.Put(itfixture.LinearIssue{ID: "bare", Identifier: "ENG-1", Team: "ENG", URL: "https://linear.example/ENG-1", Created: baseTime})
	rep = verify(t, h, c.ID)
	joined := strings.Join(rep.Mismatches, "\n")
	if rep.OK || !strings.Contains(joined, "1 of 4 issues have no title") || !strings.Contains(joined, "1 of 4 issues have no update time") {
		t.Fatalf("%+v", rep)
	}
	if !strings.Contains(rep.Summary, "Linear answered, but 2 things did not match what Drydock expects.") {
		t.Fatal(rep.Summary)
	}
	// the connection's own status is Test's and sync's business: the check changes nothing of it
	if v, _ := h.svc.Get(context.Background(), c.ID); v.Status != c.Status || v.Verified == nil || v.Verified.Mismatches != 2 {
		t.Fatalf("%+v", v)
	}
}

func TestVerifyRecordIsForgottenOnReconnectAndDisconnect(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	_, url := linearFixture(t, 1)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	verify(t, h, c.ID)
	if v, _ := h.svc.Get(ctx, c.ID); v.Verified == nil {
		t.Fatal("not recorded")
	}
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{ID: c.ID, Kind: integrations.KindLinear, Token: linearToken, BaseURL: url}); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Verified != nil {
		t.Fatalf("a new credential keeps the old check: %+v", v.Verified)
	}
	verify(t, h, c.ID)
	if err := h.svc.Disconnect(ctx, human, c.ID); err != nil {
		t.Fatal(err)
	}
	if n := h.count(`SELECT COUNT(*) FROM settings WHERE key LIKE 'integration.verified.%'`); n != 0 {
		t.Fatalf("%d records left after a disconnect", n)
	}
}

func TestVerifyAikidoAndNotionThroughTheirFixtures(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	// Notion: search without an assignment, then one page's markdown (the body is read, not kept)
	nfx := itfixture.NewNotion("ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc")
	ts, url := itfixture.Serve(nfx.Server())
	t.Cleanup(ts.Close)
	nfx.Put(itfixture.NotionPage{ID: "p1", Title: "Requirements page", Text: "body text that stays out", Edited: baseTime.Add(-time.Minute)})
	nc, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindNotion, Token: "ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	rep := verify(t, h, nc.ID)
	if !rep.OK || rep.Read.Items != 1 || !rep.Read.DetailRead || nfx.Calls("markdown") != 1 {
		t.Fatalf("%+v", rep)
	}
	if strings.Contains(jsonOf(rep), "body text that stays out") || strings.Contains(jsonOf(rep), "Requirements page") {
		t.Fatal("the report carries page text")
	}
	// Aikido: the token exchange is one of the calls, with its status
	_, ac := aikidoFixture(t, h, []itfixture.AikidoFinding{finding(7, "high", "open_source", "Prototype pollution in lodash", "lodash", 1)}, aikidoSecret)
	arep := verify(t, h, ac.ID)
	if !arep.OK || arep.Read.Items != 1 {
		t.Fatalf("%+v", arep)
	}
	if got := strings.Join(endpoints(arep), "|"); !strings.HasPrefix(got, "POST /api/oauth/token|") {
		t.Fatalf("%s", got)
	}
}
