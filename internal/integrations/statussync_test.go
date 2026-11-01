package integrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

// taskDouble is a labelled test double of the task service: statuses by task id, and the moves a person applied.
type taskDouble struct {
	mu     sync.Mutex
	status map[string]string
	moves  []string
	refs   map[string]string // issue reference -> task id
}

func (d *taskDouble) wire(o *integrations.Options) {
	o.TaskStatus = func(_ context.Context, id string) (string, error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if s, ok := d.status[id]; ok {
			return s, nil
		}
		return "BACKLOG", nil
	}
	o.MoveTask = func(_ context.Context, id, to, by string) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.moves = append(d.moves, id+">"+to+">"+by)
		d.status[id] = to
		return nil
	}
	o.TaskByRef = func(_ context.Context, _ string, ref string) (string, error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.refs[ref], nil
	}
	o.TaskInProject = func(_ context.Context, project, id string) error {
		if project == "prj1" && strings.HasPrefix(id, "task_") {
			return nil
		}
		return fmt.Errorf("no task %s", id)
	}
}

func publishTask(t *testing.T, h *harness, typ, taskID, to string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"from": "X", "to": to, "by": "agent:claude"})
	if _, err := h.bus.Publish(context.Background(), events.Event{Type: typ, ProjectID: "prj1", TaskID: taskID, Data: b}); err != nil {
		t.Fatal(err)
	}
}

func outboxByOp(t *testing.T, h *harness, id, op string) []integrations.Outbox {
	t.Helper()
	rows, err := h.svc.ListOutbox(context.Background(), id, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []integrations.Outbox
	for _, o := range rows {
		if o.Op == op {
			out = append(out, o)
		}
	}
	return out
}

// Status sync from Drydock to Linear goes through the ordinary outbox: a task entering a mapped status proposes the vendor write
// and it waits for a person, unless the person turned on the status_sync class; then it is approved in their name and sent.
func TestStatusSyncProposesThroughTheOutboxAndAutoApprovesOnlyWhenTurnedOn(t *testing.T) {
	td := &taskDouble{status: map[string]string{}, refs: map[string]string{}}
	h := newHarness(t, td.wire)
	ctx := context.Background()
	fx, url := linearFixture(t, 2)
	c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})
	asg := c.Assignments[0]
	h.sync(c.ID, integrations.SyncOpts{})
	var item integrations.Item
	for _, it := range h.items(c.ID) {
		if it.ExternalID == "uuid-0000" {
			item = it
		}
	}
	tr, err := h.svc.Import(ctx, human, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	// mappings: only a person, validated
	maps := []integrations.Mapping{{Remote: "In Progress", Local: "IN_PROGRESS", Direction: integrations.DirBoth}, {Remote: "Done", Local: "COMPLETED", Direction: integrations.DirToVendor}}
	if _, err := h.svc.SetMappings(ctx, "agent:claude", integrations.MappingsReq{AssignmentID: asg.ID, Scope: integrations.MapStatus, Mappings: maps}); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatal(err)
	}
	for _, bad := range [][]integrations.Mapping{{{Remote: "", Local: "COMPLETED"}}, {{Remote: "Done", Local: "SHIPPED"}}, {{Remote: "Done", Local: "COMPLETED"}, {Remote: "Closed", Local: "COMPLETED"}},
		{{Remote: "Done", Local: "COMPLETED", Direction: "sideways"}}} {
		if _, err := h.svc.SetMappings(ctx, human, integrations.MappingsReq{AssignmentID: asg.ID, Scope: integrations.MapStatus, Mappings: bad}); !errors.Is(err, integrations.ErrBadRequest) {
			t.Fatalf("%+v accepted: %v", bad, err)
		}
	}
	if _, err := h.svc.SetMappings(ctx, human, integrations.MappingsReq{AssignmentID: asg.ID, Scope: integrations.MapProperty, Mappings: []integrations.Mapping{{Remote: "state", Local: "Status"}}}); err == nil {
		t.Fatal("a Linear assignment took a property mapping")
	}
	got, err := h.svc.SetMappings(ctx, human, integrations.MappingsReq{AssignmentID: asg.ID, Scope: integrations.MapStatus, Mappings: maps})
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	fields, err := h.svc.MappingFields(ctx, asg.ID, integrations.MapStatus)
	if err != nil || len(fields) != 3 || fields[2].Name != "Done" {
		t.Fatalf("%+v %v", fields, err)
	}

	// the first run only remembers where the event log is; then a task move proposes, nothing is sent
	if n := h.svc.StatusSync(ctx); n != 0 {
		t.Fatal(n)
	}
	publishTask(t, h, "task.started", tr.TaskID, "IN_PROGRESS")
	publishTask(t, h, "task.updated", tr.TaskID, "") // not a status change
	if n := h.svc.StatusSync(ctx); n != 1 {
		t.Fatalf("proposed %d", n)
	}
	rows := outboxByOp(t, h, c.ID, "set_state")
	if len(rows) != 1 || rows[0].Status != integrations.OutAwaiting || rows[0].Payload["state"] != "In Progress" || rows[0].AutoClass != "" || rows[0].CreatedBy != "integration:status-sync" {
		t.Fatalf("%+v", rows)
	}
	if len(fx.Updates) != 0 {
		t.Fatal("a status was written without approval")
	}
	if n := h.svc.StatusSync(ctx); n != 0 {
		t.Fatal("the same event was handled twice")
	}

	// the person turns the class on: the next move is approved in their name and sent
	if _, err := h.svc.SetAutoApprove(ctx, "agent:claude", integrations.AutoApproveReq{IntegrationID: c.ID, ProjectID: "prj1", Class: integrations.ClassStatusSync, Enabled: true}); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatal(err)
	}
	if _, err := h.svc.SetAutoApprove(ctx, human, integrations.AutoApproveReq{IntegrationID: c.ID, ProjectID: "prj1", Class: "delete_everything", Enabled: true}); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatal(err)
	}
	classes, _ := h.svc.AutoApprovals(ctx, c.ID, "prj1")
	if len(classes) != 2 || classes[0].Enabled || classes[1].Enabled {
		t.Fatalf("classes are not off by default: %+v", classes)
	}
	aa, err := h.svc.SetAutoApprove(ctx, human, integrations.AutoApproveReq{IntegrationID: c.ID, ProjectID: "prj1", Class: integrations.ClassStatusSync, Enabled: true})
	if err != nil || !aa.Enabled || aa.Version != 1 {
		t.Fatalf("%+v %v", aa, err)
	}
	if _, err := h.svc.SetAutoApprove(ctx, human, integrations.AutoApproveReq{IntegrationID: c.ID, ProjectID: "prj1", Class: integrations.ClassStatusSync, Enabled: false, Version: 7}); !errors.Is(err, integrations.ErrConflict) {
		t.Fatal(err)
	}
	publishTask(t, h, "task.completed", tr.TaskID, "COMPLETED")
	if n := h.svc.StatusSync(ctx); n != 1 {
		t.Fatal(n)
	}
	rows = outboxByOp(t, h, c.ID, "set_state")
	var sent integrations.Outbox
	for _, o := range rows {
		if o.Payload["state"] == "Done" {
			sent = o
		}
	}
	if sent.Status != integrations.OutSent || sent.AutoClass != integrations.ClassStatusSync || sent.DecidedBy != human {
		t.Fatalf("%+v", sent)
	}
	if ap, _ := h.appr.Get(ctx, sent.ApprovalID); ap.Status != "CONSUMED" || ap.DecidedBy != human {
		t.Fatalf("%+v", ap)
	}
	if fx.Get("uuid-0000").State != "Done" {
		t.Fatalf("%+v", fx.Get("uuid-0000"))
	}
	var autoEvent bool
	for _, e := range h.events(integrations.EventOutboundSent) {
		if strings.Contains(string(e.Data), `"auto":true`) && strings.Contains(string(e.Data), integrations.ClassStatusSync) {
			autoEvent = true
		}
	}
	if !autoEvent {
		t.Fatal("the automatic send is not recorded as such")
	}
	// the earlier proposal still waits for a person: a class never approves what it did not propose
	for _, o := range outboxByOp(t, h, c.ID, "set_state") {
		if o.Payload["state"] == "In Progress" && o.Status != integrations.OutAwaiting {
			t.Fatalf("%+v", o)
		}
	}

	// the link-back comment is its own class: off, it waits; on, it is sent. A comment an agent proposes is never auto-approved.
	lb, err := h.svc.LinkBack(ctx, human, tr.TaskID, "https://github.com/acme/web/pull/7")
	if err != nil || lb.Status != integrations.OutAwaiting {
		t.Fatalf("%+v %v", lb, err)
	}
	if _, err := h.svc.SetAutoApprove(ctx, human, integrations.AutoApproveReq{IntegrationID: c.ID, ProjectID: "prj1", Class: integrations.ClassLinkback, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	lb2, err := h.svc.LinkBack(ctx, human, tr.TaskID, "https://github.com/acme/web/pull/8")
	if err != nil || lb2.Status != integrations.OutSent || lb2.AutoClass != integrations.ClassLinkback {
		t.Fatalf("%+v %v", lb2, err)
	}
	ag, err := h.svc.Propose(ctx, "agent:claude", integrations.ProposeReq{IntegrationID: c.ID, Op: "comment", Target: "uuid-0000", TaskID: tr.TaskID, Params: map[string]string{"body": "Drydock opened a pull request for this item: https://evil.example"}})
	if err != nil || ag.Status != integrations.OutAwaiting || ag.AutoClass != "" {
		t.Fatalf("%+v %v", ag, err)
	}
}

// A vendor's state is offered to the person as a change to the task; it is never applied by itself.
func TestUpstreamStatusIsOfferedToThePersonAndAppliedOnlyByThem(t *testing.T) {
	td := &taskDouble{status: map[string]string{}, refs: map[string]string{}}
	h := newHarness(t, td.wire)
	ctx := context.Background()
	fx, url := linearFixture(t, 1)
	c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})
	h.sync(c.ID, integrations.SyncOpts{})
	tr, err := h.svc.Import(ctx, human, h.items(c.ID)[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	td.status[tr.TaskID] = "IN_PROGRESS"
	if _, err := h.svc.SetMappings(ctx, human, integrations.MappingsReq{AssignmentID: c.Assignments[0].ID, Scope: integrations.MapStatus,
		Mappings: []integrations.Mapping{{Remote: "Canceled", Local: "CANCELLED", Direction: integrations.DirFromVendor}}}); err != nil {
		t.Fatal(err)
	}
	is := fx.Get("uuid-0000")
	is.State, is.Updated = "Canceled", baseTime.Add(time.Hour)
	fx.Put(is)
	h.sync(c.ID, integrations.SyncOpts{})
	it := h.items(c.ID)[0]
	if it.StatusSuggestion == nil || it.StatusSuggestion.To != "CANCELLED" || it.StatusSuggestion.TaskStatus != "IN_PROGRESS" || it.StatusSuggestion.Upstream != "Canceled" {
		t.Fatalf("%+v", it.StatusSuggestion)
	}
	if len(td.moves) != 0 {
		t.Fatal("the task was moved without the person")
	}
	evs := h.events(integrations.EventStatusUpstream)
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), "CANCELLED") || evs[0].TaskID != tr.TaskID {
		t.Fatalf("%+v", evs)
	}
	if _, err := h.svc.ApplyStatus(ctx, "agent:claude", it.ID); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatal(err)
	}
	out, err := h.svc.ApplyStatus(ctx, human, it.ID)
	if err != nil || out.StatusSuggestion != nil || len(td.moves) != 1 || td.moves[0] != tr.TaskID+">CANCELLED>"+human {
		t.Fatalf("%+v %v %v", out, err, td.moves)
	}
	if _, err := h.svc.ApplyStatus(ctx, human, it.ID); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatal("applied twice")
	}
	// Sentry maps only resolved and unresolved: an ignore is never written on Drydock's own
	sfx, surl := sentryFixture(t)
	putSentry(sfx, "201", "error", "web", 1, 1)
	sc := h.connect(integrations.KindSentry, sentryToken, surl, map[string]string{"organization": "acme"})
	if _, err := h.svc.SetMappings(ctx, human, integrations.MappingsReq{AssignmentID: sc.Assignments[0].ID, Scope: integrations.MapStatus,
		Mappings: []integrations.Mapping{{Remote: "ignored", Local: "CANCELLED"}}}); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatal(err)
	}
	if classes, _ := h.svc.AutoApprovals(ctx, sc.ID, "prj1"); len(classes) != 1 || classes[0].Class != integrations.ClassStatusSync {
		t.Fatalf("%+v", classes)
	}
}

func TestLinkTaskRulesLinkToAnExistingTask(t *testing.T) {
	td := &taskDouble{status: map[string]string{}, refs: map[string]string{"ENG-998": "task_ref"}}
	h := newHarness(t, td.wire)
	ctx := context.Background()
	_, url := linearFixture(t, 3) // ENG-1000, ENG-999, ENG-998
	c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})
	tru := true
	fls := false
	// params.task belongs to link_task rules and must be a task of the project
	if _, err := h.svc.SetRule(ctx, human, integrations.RuleReq{IntegrationID: c.ID, ProjectID: "prj1", Name: "x", When: "true", Action: integrations.ActionCreateTask, Params: map[string]any{"task": "task_9"}}); err == nil {
		t.Fatal("a create_task rule took params.task")
	}
	if _, err := h.svc.SetRule(ctx, human, integrations.RuleReq{IntegrationID: c.ID, ProjectID: "prj1", Name: "x", When: "true", Action: integrations.ActionLinkTask, Params: map[string]any{"task": "other_9"}}); err == nil {
		t.Fatal("a rule linked to a task outside the project")
	}
	fixed, err := h.svc.SetRule(ctx, human, integrations.RuleReq{IntegrationID: c.ID, ProjectID: "prj1", Name: "to task 9", When: `item.ref == "ENG-999"`, Action: integrations.ActionLinkTask, Params: map[string]any{"task": "task_9"}})
	if err != nil {
		t.Fatal(err)
	}
	byRef, err := h.svc.SetRule(ctx, human, integrations.RuleReq{IntegrationID: c.ID, ProjectID: "prj1", Name: "by reference", When: `item.ref.startsWith("ENG-99")`, Action: integrations.ActionLinkTask})
	if err != nil {
		t.Fatal(err)
	}
	// dry run: listed, not linked
	h.sync(c.ID, integrations.SyncOpts{})
	for _, it := range h.items(c.ID) {
		if it.TaskID != "" {
			t.Fatal("a dry-run rule linked an item")
		}
	}
	ms, _ := h.svc.Rules(ctx, c.ID)
	outcomes := map[string]string{}
	for _, r := range ms {
		for _, m := range r.Matches {
			outcomes[r.Name+"/"+m.ItemID] = m.Outcome
		}
	}
	var wouldLink, noTask int
	for _, o := range outcomes {
		switch o {
		case "would_link":
			wouldLink++
		case "no_task":
			noTask++
		}
	}
	if wouldLink != 2 || noTask != 1 { // ENG-999 (fixed), ENG-998 (by reference); ENG-999 has no task by reference
		t.Fatalf("%v", outcomes)
	}
	for _, r := range []integrations.Rule{fixed, byRef} {
		if _, err := h.svc.SetRule(ctx, human, integrations.RuleReq{ID: r.ID, Version: r.Version, Enabled: &tru, DryRun: &fls}); err != nil {
			t.Fatal(err)
		}
	}
	h.sync(c.ID, integrations.SyncOpts{Force: true})
	h.svc.EvaluateRules(ctx, mustConn(t, h, c.ID), c.Assignments[0].Assignment)
	linked := map[string]string{}
	for _, it := range h.items(c.ID) {
		linked[it.IssueRef] = it.TaskID + " " + it.ImportedBy
	}
	if !strings.HasPrefix(linked["ENG-999"], "task_9 rule:") || !strings.HasPrefix(linked["ENG-998"], "task_ref rule:") || linked["ENG-1000"] != " " {
		t.Fatalf("%v", linked)
	}
	if len(h.taskList()) != 0 {
		t.Fatal("a link_task rule created a task")
	}
	if len(h.events(integrations.EventTaskLinked)) != 2 {
		t.Fatal("links are not recorded")
	}
}

func mustConn(t *testing.T, h *harness, id string) integrations.Connection {
	t.Helper()
	c, err := h.svc.Store().GetConnection(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A comment that failed may still have reached the vendor: a retry looks for it first and posts it again only when it is not
// there, as a new proposal that needs a new approval.
func TestRetryLooksForTheCommentBeforeProposingAgain(t *testing.T) {
	h, fx, c := linearWithComment(t)
	ctx := context.Background()
	o := propose(t, h, c, human, "Fixed in the release branch.")
	if o.Marker == "" {
		t.Fatal("no marker")
	}
	if _, err := h.svc.Decide(ctx, human, o.ID, true); err != nil {
		t.Fatal(err)
	}
	fx.FailNextComment("after")
	if _, err := h.svc.Execute(ctx, human, o.ID); !errors.Is(err, integrations.ErrSendFailed) {
		t.Fatalf("%v", err)
	}
	if _, err := h.svc.Retry(ctx, "agent:claude", o.ID); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatal(err)
	}
	r, err := h.svc.Retry(ctx, human, o.ID)
	if err != nil || r.ID != o.ID || r.Status != integrations.OutSent || !strings.Contains(r.Receipt, "found on Linear") {
		t.Fatalf("%+v %v", r, err)
	}
	if len(fx.CommentsSent()) != 1 {
		t.Fatal("posted twice")
	}
	if _, err := h.svc.Retry(ctx, human, o.ID); err == nil {
		t.Fatal("a sent write was retried")
	}

	// the comment never arrived: proposed again with the same call, waiting for a new approval
	o2 := propose(t, h, c, human, "A second note.")
	if _, err := h.svc.Decide(ctx, human, o2.ID, true); err != nil {
		t.Fatal(err)
	}
	fx.FailNextComment("before")
	if _, err := h.svc.Execute(ctx, human, o2.ID); err == nil {
		t.Fatal("expected a failure")
	}
	r2, err := h.svc.Retry(ctx, human, o2.ID)
	if err != nil || r2.ID == o2.ID || r2.Status != integrations.OutAwaiting || r2.PayloadHash != o2.PayloadHash || r2.ApprovalID == o2.ApprovalID {
		t.Fatalf("%+v %v", r2, err)
	}
	if _, err := h.svc.Execute(ctx, human, r2.ID); !errors.Is(err, integrations.ErrNotApproved) {
		t.Fatal("a retry was sent without a new approval")
	}
	if _, err := h.svc.Decide(ctx, human, r2.ID, true); err != nil {
		t.Fatal(err)
	}
	if done, err := h.svc.Execute(ctx, human, r2.ID); err != nil || done.Status != integrations.OutSent {
		t.Fatalf("%+v %v", done, err)
	}
	if n := len(fx.CommentsSent()); n != 2 {
		t.Fatalf("%d comments", n)
	}
	_ = itfixture.LinearIssue{}
}
