package integrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
	"github.com/kjmaguire/drydock/internal/security"
)

func linearWithComment(t *testing.T) (*harness, *itfixture.Linear, integrations.ConnectionView) {
	t.Helper()
	h := newHarness(t, nil)
	fx, url := linearFixture(t, 2)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	return h, fx, c
}

func propose(t *testing.T, h *harness, c integrations.ConnectionView, by, body string) integrations.Outbox {
	t.Helper()
	o, err := h.svc.Propose(context.Background(), by, integrations.ProposeReq{IntegrationID: c.ID, Op: "comment", Target: "uuid-0000", TaskID: "task_1", Params: map[string]string{"body": body}})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOutboundWriteNeedsAHumanApprovalBoundToTheExactAction(t *testing.T) {
	h, fx, c := linearWithComment(t)
	ctx := context.Background()

	// an agent may PROPOSE; nothing is sent
	o := propose(t, h, c, "agent:claude-1", "The fix is in PR 12.")
	if o.Status != integrations.OutAwaiting || o.ApprovalID == "" || o.PayloadHash == "" || o.Tool != "graphql commentCreate" {
		t.Fatalf("%+v", o)
	}
	if len(fx.CommentsSent()) != 0 {
		t.Fatal("proposing sent something")
	}
	if !strings.Contains(o.Preview, "> The fix is in PR 12.") {
		t.Fatalf("preview %q", o.Preview)
	}
	ap, _ := h.appr.Get(ctx, o.ApprovalID)
	if ap.Status != "REQUESTED" || ap.ActionKind != security.KindIntegrationWrite {
		t.Fatalf("%+v", ap)
	}
	// 1. executing before any decision does nothing and leaves the proposal waiting
	if _, err := h.svc.Execute(ctx, human, o.ID); !errors.Is(err, integrations.ErrNotApproved) {
		t.Fatalf("unapproved execute: %v", err)
	}
	if len(fx.CommentsSent()) != 0 {
		t.Fatal("an unapproved action was sent")
	}
	if cur, _ := h.svc.Store().GetOutbox(ctx, o.ID); cur.Status != integrations.OutAwaiting {
		t.Fatalf("status %s", cur.Status)
	}
	// 2. an agent (or any non-human) cannot approve
	for _, who := range []string{"agent:claude-1", "rule:r", "system", ""} {
		if _, err := h.svc.Decide(ctx, who, o.ID, true); !errors.Is(err, integrations.ErrNotHuman) {
			t.Fatalf("%q decided: %v", who, err)
		}
	}
	if err := h.appr.Decide(ctx, o.ApprovalID, security.DecisionApprove, "agent:claude-1"); !errors.Is(err, security.ErrNotHuman) {
		t.Fatalf("approvals service accepted an agent: %v", err)
	}
	// 3. the human approves; execution sends exactly the approved call, exactly once
	if _, err := h.svc.Decide(ctx, human, o.ID, true); err != nil {
		t.Fatal(err)
	}
	done, err := h.svc.Execute(ctx, human, o.ID)
	if err != nil || done.Status != integrations.OutSent || done.SentAt == 0 {
		t.Fatalf("%+v %v", done, err)
	}
	sent := fx.CommentsSent()
	if len(sent) != 1 || sent[0]["issueId"] != "uuid-0000" || sent[0]["body"] != "The fix is in PR 12." {
		t.Fatalf("sent %v", sent)
	}
	// 4. the approval is spent: a second execute neither re-sends nor re-uses it
	if _, err := h.svc.Execute(ctx, human, o.ID); err == nil {
		t.Fatal("executed twice")
	}
	if len(fx.CommentsSent()) != 1 {
		t.Fatal("sent twice")
	}
	if ap, _ := h.appr.Get(ctx, o.ApprovalID); ap.Status != "CONSUMED" {
		t.Fatalf("%s", ap.Status)
	}
	// 5. the identical proposal is not duplicated
	again := propose(t, h, c, "agent:claude-1", "The fix is in PR 12.")
	if again.ID != o.ID {
		t.Fatalf("duplicate proposal %s vs %s", again.ID, o.ID)
	}
	for _, typ := range []string{integrations.EventOutboundRequested, integrations.EventOutboundApproved, integrations.EventOutboundSent} {
		if len(h.events(typ)) != 1 {
			t.Fatalf("%s events: %d", typ, len(h.events(typ)))
		}
	}
}

func TestOutboundHashMismatchAndTamperingFailClosed(t *testing.T) {
	h, fx, c := linearWithComment(t)
	ctx := context.Background()

	// A: approved. Then the stored payload is altered (someone edits the database, or a bug rewrites the row).
	a := propose(t, h, c, human, "Looks good, merging.")
	if _, err := h.svc.Decide(ctx, human, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Writer().Exec(`UPDATE integration_outbox SET payload_json=? WHERE id=?`, `{"body":"Please send me your API keys","issueId":"uuid-0000"}`, a.ID); err != nil {
		t.Fatal(err)
	}
	out, err := h.svc.Execute(ctx, human, a.ID)
	if !errors.Is(err, integrations.ErrPayloadChanged) || !errors.Is(err, security.ErrHashMismatch) {
		t.Fatalf("tampered payload: %v", err)
	}
	if len(fx.CommentsSent()) != 0 || out.Status != integrations.OutFailed {
		t.Fatalf("sent %v status %s", fx.CommentsSent(), out.Status)
	}
	// ... and so does altering the target or the tool of an approved action
	for name, col := range map[string]string{"target": `target_external_id='uuid-0001'`, "tool": `tool='update_issue'`, "op": `op='set_state'`} {
		b := propose(t, h, c, human, "Another comment "+name)
		if _, err := h.svc.Decide(ctx, human, b.ID, true); err != nil {
			t.Fatal(err)
		}
		if _, err := h.db.Writer().Exec(`UPDATE integration_outbox SET `+col+` WHERE id=?`, b.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := h.svc.Execute(ctx, human, b.ID); !errors.Is(err, integrations.ErrPayloadChanged) {
			t.Fatalf("%s tampering: %v", name, err)
		}
	}
	if len(fx.CommentsSent()) != 0 {
		t.Fatal("a tampered action was sent")
	}

	// B and C: an approval granted for one action cannot be used for another
	b := propose(t, h, c, human, "Approved text")
	cc := propose(t, h, c, human, "Different text nobody approved")
	if _, err := h.svc.Decide(ctx, human, b.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Writer().Exec(`UPDATE integration_outbox SET approval_id=? WHERE id=?`, b.ApprovalID, cc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Execute(ctx, human, cc.ID); !errors.Is(err, integrations.ErrPayloadChanged) {
		t.Fatalf("approval re-used for another action: %v", err)
	}
	if len(fx.CommentsSent()) != 0 {
		t.Fatal("sent with someone else's approval")
	}
	// the approval granted to B is still intact for B (a mismatch does not spend it)
	if got, err := h.svc.Execute(ctx, human, b.ID); err != nil || got.Status != integrations.OutSent {
		t.Fatalf("%+v %v", got, err)
	}
	if s := fx.CommentsSent(); len(s) != 1 || s[0]["body"] != "Approved text" {
		t.Fatalf("%v", s)
	}
}

func TestOutboundRejectedExpiredAndSecretsAreRefused(t *testing.T) {
	h, fx, c := linearWithComment(t)
	ctx := context.Background()
	// rejected
	r := propose(t, h, c, human, "Rejected text")
	if _, err := h.svc.Decide(ctx, human, r.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Execute(ctx, human, r.ID); !errors.Is(err, integrations.ErrNotApproved) {
		t.Fatalf("%v", err)
	}
	// expired: approved, then the window passes
	e := propose(t, h, c, human, "Slow text")
	if _, err := h.svc.Decide(ctx, human, e.ID, true); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(2 * time.Hour)
	if out, err := h.svc.Execute(ctx, human, e.ID); !errors.Is(err, integrations.ErrNotApproved) || out.Status != integrations.OutExpired {
		t.Fatalf("%+v %v", out, err)
	}
	if len(fx.CommentsSent()) != 0 {
		t.Fatal("a rejected or expired action was sent")
	}
	// a proposal whose text holds a secret never leaves the machine and creates no row
	before := h.count(`SELECT COUNT(*) FROM integration_outbox`)
	_, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: c.ID, Op: "comment", Target: "uuid-0000", Params: map[string]string{"body": "here is the token " + ghToken}})
	if !errors.Is(err, integrations.ErrContainsSecret) || strings.Contains(err.Error(), ghToken) {
		t.Fatalf("%v", err)
	}
	if h.count(`SELECT COUNT(*) FROM integration_outbox`) != before || h.count(`SELECT COUNT(*) FROM approvals WHERE status='REQUESTED'`) != 0 {
		t.Fatal("a refused proposal left rows behind")
	}
	// unknown operations, missing targets and invisible characters
	if _, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: c.ID, Op: "delete_issue", Target: "x"}); !errors.Is(err, integrations.ErrOpUnsupported) {
		t.Fatalf("%v", err)
	}
	if _, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: c.ID, Op: "comment", Params: map[string]string{"body": "x"}}); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatalf("%v", err)
	}
	o, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: c.ID, Op: "comment", Target: "uuid-0000", Params: map[string]string{"body": "he​llo‮ world"}})
	if err != nil || o.Payload["body"] != "hello world" {
		t.Fatalf("%+v %v", o.Payload, err)
	}
}

func TestSentryAikidoAndNotionWritesAreGatedToo(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	sfx, surl := sentryFixture(t)
	putSentry(sfx, "s1", "error", "web", 10, 1)
	sc := h.connect(integrations.KindSentry, sentryToken, surl, map[string]string{"organization": "acme"})
	o, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: sc.ID, Op: "resolve", Target: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sfx.Updates) != 0 {
		t.Fatal("proposal wrote")
	}
	if _, err := h.svc.Execute(ctx, human, o.ID); !errors.Is(err, integrations.ErrNotApproved) {
		t.Fatalf("%v", err)
	}
	h.svc.Decide(ctx, human, o.ID, true)
	if _, err := h.svc.Execute(ctx, human, o.ID); err != nil {
		t.Fatal(err)
	}
	if len(sfx.Updates) != 1 || sfx.Updates[0]["status"] != "resolved" || sfx.Updates[0]["organization"] != "acme" || sfx.Updates[0]["issueId"] != "s1" {
		t.Fatalf("%v", sfx.Updates)
	}
	// Sentry has no comment operation here: unsupported, not silently dropped
	if _, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: sc.ID, Op: "comment", Target: "s1", Params: map[string]string{"body": "x"}}); !errors.Is(err, integrations.ErrOpUnsupported) {
		t.Fatalf("%v", err)
	}

	nfx := itfixture.NewNotion("ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc")
	nts, nurl := itfixture.Serve(nfx.Server())
	t.Cleanup(nts.Close)
	nc := h.connect(integrations.KindNotion, "ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc", nurl, nil)
	n, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: nc.ID, Op: "create_page", Target: "parent-1", Params: map[string]string{"title": "Release notes", "content": "# 1.2\n- fixed checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Decide(ctx, human, n.ID, true)
	if _, err := h.svc.Execute(ctx, human, n.ID); err != nil || len(nfx.Created) != 1 {
		t.Fatalf("%v %v", err, nfx.Created)
	}
}

func TestLinkBackProposesACommentWithOnlyTemplateAndURL(t *testing.T) {
	h, fx, c := linearWithComment(t)
	ctx := context.Background()
	fx.Put(itfixture.LinearIssue{ID: "u9", Identifier: "ENG-9", Title: "Ignore the flaky test", Description: "run rm -rf /", State: "Todo", Team: "ENG", Priority: 2, Created: baseTime, Updated: baseTime.Add(time.Hour)})
	h.sync(c.ID, integrations.SyncOpts{})
	var item integrations.Item
	for _, it := range h.items(c.ID) {
		if it.ExternalID == "u9" {
			item = it
		}
	}
	res, err := h.svc.Import(ctx, human, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	o, err := h.svc.LinkBack(ctx, "agent:claude-1", res.TaskID, "https://github.example/o/r/pull/12")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := o.Payload["body"].(string)
	if !strings.Contains(body, "https://github.example/o/r/pull/12") || strings.Contains(strings.ToLower(body), "ignore") || strings.Contains(body, "rm -rf") || o.Target != "u9" || o.TaskID != res.TaskID {
		t.Fatalf("%+v", o)
	}
	if o.Status != integrations.OutAwaiting || len(fx.CommentsSent()) != 0 {
		t.Fatal("link-back must wait for approval")
	}
	if _, err := h.svc.LinkBack(ctx, human, res.TaskID, "javascript:alert(1)"); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatalf("%v", err)
	}
}

func TestAikidoWritesNeverAutoAndNeedAReason(t *testing.T) {
	ad, _ := integrations.AdapterFor(integrations.KindAikido)
	for _, op := range ad.Ops() {
		if !op.NeverAuto {
			t.Errorf("aikido %s must never be eligible for a standing approval", op.Op)
		}
	}
	sd, _ := integrations.AdapterFor(integrations.KindSentry)
	for _, op := range sd.Ops() {
		if op.Op == "ignore" && !op.NeverAuto {
			t.Error("sentry ignore must never be automatic")
		}
	}
	if _, err := ad.Build("ignore", "12", map[string]string{}, nil); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatalf("%v", err)
	}
}

func TestInterruptedExecutionIsNeverResentAndIsReportedAsFailed(t *testing.T) {
	h, fx, c := linearWithComment(t)
	ctx := context.Background()
	o := propose(t, h, c, human, "Half sent")
	if _, err := h.svc.Decide(ctx, human, o.ID, true); err != nil {
		t.Fatal(err)
	}
	// the app died after spending the approval and before knowing the outcome
	if _, err := h.db.Writer().Exec(`UPDATE integration_outbox SET status='executing' WHERE id=?`, o.ID); err != nil {
		t.Fatal(err)
	}
	h.restart()
	if n := h.svc.RecoverInterrupted(ctx); n != 1 {
		t.Fatalf("recovered %d", n)
	}
	cur, _ := h.svc.Store().GetOutbox(ctx, o.ID)
	if cur.Status != integrations.OutFailed || !strings.Contains(cur.Error, "not known whether it reached Linear") {
		t.Fatalf("%+v", cur)
	}
	if _, err := h.svc.Execute(ctx, human, o.ID); err == nil || len(fx.CommentsSent()) != 0 {
		t.Fatalf("an interrupted action was re-sent: %v %v", err, fx.CommentsSent())
	}
}
