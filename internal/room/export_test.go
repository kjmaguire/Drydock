package room

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// seedRoom builds a task room with a realistic mix of messages.
func seedRoom(t *testing.T, e *env) (Room, string, string) {
	t.Helper()
	worker, rev := e.agent("worker"), e.agent("rev")
	r, err := e.svc.CreateTaskChannel(e.ctx, TaskChannelSpec{ProjectID: e.project, TaskID: "task_e", Number: 7, Title: "Add login", Owner: worker, Reviewers: []string{rev}, CreatedBy: kyle})
	if err != nil {
		t.Fatal(err)
	}
	e.post(PostParams{RoomID: r.ID, Author: kyle, Type: TypeTask, Body: "Add login", Payload: map[string]any{"title": "Add login with sessions"}})
	e.post(PostParams{RoomID: r.ID, Author: Agent(worker), Body: "Plan ready.\n[DECISION] Use server-side sessions\n[TODO] Add a logout test"})
	e.post(PostParams{RoomID: r.ID, Author: Agent(rev), Type: TypeQuestion, Body: "Do we need SSO now?", Payload: map[string]any{"blocking": true}})
	e.post(PostParams{RoomID: r.ID, Author: Agent(worker), Type: TypeResult, Body: "Implemented login", Payload: map[string]any{"summary": "Implemented login", "diff_stat": "3 files changed, 120 insertions(+)"}, Refs: Refs{Commit: "abc123", RunID: "run_77"}})
	e.svc.PostCheckResult(e.ctx, r.ID, CheckResult{Kind: "test", Status: CheckFail, FailureSignature: "TestLogout", LogRef: "runs/1/test.log"})
	e.svc.PostCheckResult(e.ctx, r.ID, CheckResult{Kind: "test", Status: CheckPass, LogRef: "runs/2/test.log"})
	e.svc.PostCheckResult(e.ctx, r.ID, CheckResult{Kind: "lint", Status: CheckPass, LogRef: "runs/2/lint.log"})
	return r, worker, rev
}

func TestExportMinutes(t *testing.T) {
	e := newEnv(t)
	r, worker, _ := seedRoom(t, e)
	ex, created, err := e.svc.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "room:" + r.ID, ReceiptID: "min-1", Summary: "We chose sessions.", By: kyle})
	if err != nil || !created {
		t.Fatalf("%v %v", err, created)
	}
	c := ex.Content
	for _, want := range []string{
		"# Minutes: task-7-add-login",
		"## Summary", "Supplied by the caller", "We chose sessions.",
		"## Participants", "- human:kyle: 1 messages", "- " + worker + ": 4 messages", "- tool:tests: 2 messages",
		"## Decisions", "[proposed] Use server-side sessions",
		"## TODOs and tasks", "Add a logout test", "Add login with sessions",
		"## Results and checks", "Implemented login", "tests test: FAIL", "tests test: PASS", "lint lint: PASS", "log runs/2/test.log",
		"## Open questions", "Do we need SSO now?",
		"## References", "- commit abc123", "- run run_77",
		"## Transcript", "> Plan ready.", "(untrusted)",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("minutes lack %q", want)
		}
	}
	if ex.Kind != ExportMinutes || ex.Format != "markdown" || ex.FromSeq != 1 || ex.ToSeq < 8 || len(ex.SHA256) != 64 || ex.CreatedBy != "human:kyle" {
		t.Fatalf("%+v", ex)
	}
	if ex.EventSeq == 0 {
		t.Fatal("export_created event not recorded")
	}
	if n := len(e.events("room.export_created")); n != 1 {
		t.Fatalf("events %d", n)
	}

	// idempotent by (scope, receipt): the second call returns the stored export, even after the room moved on
	e.say(r, kyle, "more talk after the export")
	again, created, err := e.svc.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "room:" + r.ID, ReceiptID: "min-1", Summary: "different summary", By: kyle})
	if err != nil || created || again.ID != ex.ID || again.Content != ex.Content {
		t.Fatalf("not idempotent: %v created=%v", err, created)
	}
	if n := len(e.events("room.export_created")); n != 1 {
		t.Fatal("idempotent call published an event")
	}
	// a new receipt is a new snapshot that includes the later message
	next, created, _ := e.svc.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "room:" + r.ID, ReceiptID: "min-2", By: kyle})
	if !created || next.ID == ex.ID || !strings.Contains(next.Content, "more talk after the export") || !strings.Contains(next.Content, "_No summary supplied._") {
		t.Fatal("second export")
	}
	if strings.Contains(ex.Content, "more talk after the export") {
		t.Fatal("first export changed after the fact")
	}
	// the same receipt id in another kind or room is a clash, not a silent reuse
	if _, _, err := e.svc.ExportPRDescription(e.ctx, r.ID, ExportRequest{Scope: "room:" + r.ID, ReceiptID: "min-1", By: kyle}); !errors.Is(err, ErrReceiptClash) {
		t.Fatalf("clash: %v", err)
	}
	// ranges
	part, _, _ := e.svc.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "s", ReceiptID: "range", FromSeq: 2, ToSeq: 3, By: kyle})
	if part.FromSeq != 2 || part.ToSeq != 3 || strings.Contains(part.Content, "Implemented login") {
		t.Fatalf("range export: %d-%d", part.FromSeq, part.ToSeq)
	}
	// requesters
	if _, _, err := e.svc.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "s", ReceiptID: "x", By: Agent(worker)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent export: %v", err)
	}
	if _, _, err := e.svc.ExportMinutes(e.ctx, r.ID, ExportRequest{ReceiptID: "x", By: kyle}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no scope: %v", err)
	}
	if _, _, err := e.svc.ExportMinutes(e.ctx, "room_none", ExportRequest{Scope: "s", ReceiptID: "x", By: kyle}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no room: %v", err)
	}
	list, _ := e.svc.ListExports(e.ctx, r.ID)
	if len(list) != 3 {
		t.Fatalf("exports %d", len(list))
	}
}

func TestExportsAreImmutableAndVerifiable(t *testing.T) {
	e := newEnv(t)
	r, _, _ := seedRoom(t, e)
	ex, _, _ := e.svc.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "s", ReceiptID: "v1", By: kyle})
	if err := e.svc.VerifyExport(e.ctx, ex.ID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE room_exports SET content='edited' WHERE id=?`,
		`UPDATE room_exports SET sha256='0' WHERE id=?`,
		`UPDATE room_exports SET to_seq=1 WHERE id=?`,
	} {
		if _, err := e.db.Writer().Exec(q, ex.ID); err == nil {
			t.Fatalf("%q succeeded", q)
		}
	}
	// If someone bypasses the trigger the verifier still catches it, both by hash and by re-rendering the messages.
	e.exec(`DROP TRIGGER room_exports_immutable`)
	e.exec(`UPDATE room_exports SET content=content||'tampered' WHERE id=?`, ex.ID)
	if err := e.svc.VerifyExport(e.ctx, ex.ID); !errors.Is(err, ErrExportInvalid) {
		t.Fatalf("hash tamper: %v", err)
	}
	ex2, _, _ := e.svc.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "s", ReceiptID: "v2", By: kyle})
	e.exec(`UPDATE room_exports SET content='forged', sha256=? WHERE id=?`, sha256hex("forged"), ex2.ID)
	if err := e.svc.VerifyExport(e.ctx, ex2.ID); !errors.Is(err, ErrExportInvalid) {
		t.Fatalf("forged content with a matching hash: %v", err)
	}
	if err := e.svc.VerifyExport(e.ctx, "exp_none"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestExportPRDescription(t *testing.T) {
	e := newEnv(t)
	r, _, _ := seedRoom(t, e)
	d, _ := e.svc.GetMessage(e.ctx, mustDecisionID(t, e, r.ID))
	if _, err := e.svc.AcceptDecision(e.ctx, r.ID, d.ID, kyle); err != nil {
		t.Fatal(err)
	}
	ex, created, err := e.svc.ExportPRDescription(e.ctx, r.ID, ExportRequest{Scope: "pr", ReceiptID: "1", By: kyle})
	if err != nil || !created {
		t.Fatal(err)
	}
	c := ex.Content
	for _, want := range []string{
		"# Add login with sessions", "## Summary", "Implemented login",
		"## Changes", "3 files changed, 120 insertions(+)",
		"## Checks", "| test | pass | runs/2/test.log |", "| lint | pass | runs/2/lint.log |", // only the latest result per kind
		"## Decisions", "Use server-side sessions",
		"needs approval",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("PR draft lacks %q\n%s", want, c)
		}
	}
	if strings.Contains(c, "runs/1/test.log") {
		t.Error("an old failing run is listed as the current check")
	}
	if err := e.svc.VerifyExport(e.ctx, ex.ID); err != nil {
		t.Fatal(err)
	}
	// secret scan applies to exports
	scan := New(e.db, e.bus, Options{Redact: func(s string) (string, error) { return strings.ReplaceAll(s, "runs/2/lint.log", "[redacted]"), nil }})
	rx, _, err := scan.ExportPRDescription(e.ctx, r.ID, ExportRequest{Scope: "pr", ReceiptID: "2", By: kyle})
	if err != nil || strings.Contains(rx.Content, "runs/2/lint.log") || !strings.Contains(rx.Content, "[redacted]") {
		t.Fatalf("%v redaction not applied", err)
	}
	block := New(e.db, e.bus, Options{Redact: func(s string) (string, error) { return "", errors.New("secret") }})
	if _, _, err := block.ExportPRDescription(e.ctx, r.ID, ExportRequest{Scope: "pr", ReceiptID: "3", By: kyle}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked export: %v", err)
	}
}

func mustDecisionID(t *testing.T, e *env, roomID string) string {
	t.Helper()
	ms, _ := e.svc.Messages(e.ctx, roomID, 0, 100)
	for _, m := range ms {
		if m.Type == TypeDecision {
			return m.ID
		}
	}
	t.Fatal("no decision")
	return ""
}

func TestADRCandidates(t *testing.T) {
	e := newEnv(t)
	r, worker, _ := seedRoom(t, e)
	cands, err := e.svc.ADRCandidates(e.ctx, r.ID, 0, 0)
	if err != nil || len(cands) != 1 || cands[0].Status != "proposed" || cands[0].Title != "Use server-side sessions" || cands[0].ProposedBy != worker || len(cands[0].SourceMessageIDs) != 1 {
		t.Fatalf("%v %+v", err, cands)
	}
	// a typed agent decision with alternatives and consequences
	full := e.post(PostParams{RoomID: r.ID, Author: Agent(worker), Type: TypeDecision, Body: "Cache sessions in memory", Payload: map[string]any{
		"title": "Cache sessions", "decision": "Keep sessions in an LRU", "rationale": "Login latency", "consequences": "Lost on restart", "alternatives": []string{"Redis", "SQLite"}}}).Message
	if _, err := e.svc.AcceptDecision(e.ctx, r.ID, full.ID, Agent(worker)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent accepting: %v", err)
	}
	acc, err := e.svc.AcceptDecision(e.ctx, r.ID, full.ID, kyle)
	if err != nil || acc.SupersedesID != full.ID || acc.PayloadString("status") != "accepted" || acc.Author.Key() != "human:kyle" {
		t.Fatalf("%v %+v", err, acc)
	}
	cands, _ = e.svc.ADRCandidates(e.ctx, r.ID, 0, 0)
	if len(cands) != 2 {
		t.Fatalf("a superseded proposal must fold into its acceptance: %+v", cands)
	}
	c := cands[1]
	if c.Status != "accepted" || c.Title != "Cache sessions" || c.Context != "Login latency" || c.Consequences != "Lost on restart" || len(c.Alternatives) != 2 ||
		len(c.SourceMessageIDs) != 2 || c.SourceMessageIDs[0] != full.ID || c.SourceMessageIDs[1] != acc.ID || c.ProposedBy != "human:kyle" {
		t.Fatalf("%+v", c)
	}
	md := RenderADRMarkdown(c)
	for _, want := range []string{"# Cache sessions", "Status: accepted", "## Context", "Login latency", "## Consequences", "- Redis", "Source messages: " + full.ID} {
		if !strings.Contains(md, want) {
			t.Errorf("ADR markdown lacks %q", want)
		}
	}
	ex, _, err := e.svc.ExportADRCandidates(e.ctx, r.ID, ExportRequest{Scope: "adr", ReceiptID: "1", By: kyle})
	if err != nil || ex.Format != "json" || !strings.Contains(ex.Content, `"title": "Cache sessions"`) || e.svc.VerifyExport(e.ctx, ex.ID) != nil {
		t.Fatalf("%v %s", err, ex.Content)
	}
	// a room with no decisions exports an empty list, not null
	empty := e.channel("nodecisions")
	ee, _, _ := e.svc.ExportADRCandidates(e.ctx, empty.ID, ExportRequest{Scope: "adr", ReceiptID: "2", By: kyle})
	if strings.TrimSpace(ee.Content) != "[]" {
		t.Fatalf("%q", ee.Content)
	}
}

// What an export is made from is rendered and scanned outside the write transaction (audit 2026-10-16, L18): the database has one
// writer, and every other write in the app waited for the room's messages to be read, rendered and scanned. The secret scanner is
// called while the export is made; a write started from inside it is a write that would wait for the writer, so it only succeeds
// when the export does not hold the writer.
func TestAnExportDoesNotHoldTheWriterWhileItIsRendered(t *testing.T) {
	e := newEnv(t)
	r := e.channel("export-writer")
	e.say(r, kyle, "something to export")
	var writeErr error
	called := false
	svc := New(e.db, e.bus, Options{Redact: func(s string) (string, error) {
		called = true
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		writeErr = e.db.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE rooms SET name=name WHERE id=?`, r.ID)
			return err
		})
		return s, nil
	}})
	ex, created, err := svc.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "w", ReceiptID: "1", By: kyle})
	if err != nil || !created || ex.ID == "" {
		t.Fatalf("export: %v %v %+v", err, created, ex)
	}
	if !called {
		t.Fatal("the scanner was not run")
	}
	if writeErr != nil {
		t.Fatalf("a write made while the export was rendered had to wait for it: %v", writeErr)
	}
	// the same receipt again is the same export, rendered or not
	again, created2, err := svc.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "w", ReceiptID: "1", By: kyle})
	if err != nil || created2 || again.ID != ex.ID {
		t.Fatalf("the same receipt: %v %v %+v", err, created2, again)
	}
}

// Two requests with the same receipt that render at the same moment make one export: the later one finds the earlier one's when
// it writes (L18).
func TestTwoExportsWithOneReceiptMakeOneExport(t *testing.T) {
	e := newEnv(t)
	r := e.channel("export-twice")
	e.say(r, kyle, "one")
	release := make(chan struct{})
	var arrived sync.WaitGroup
	arrived.Add(2)
	svc := New(e.db, e.bus, Options{Redact: func(s string) (string, error) {
		arrived.Done()
		<-release
		return s, nil
	}})
	type result struct {
		ex      Export
		created bool
		err     error
	}
	out := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			ex, created, err := svc.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "w", ReceiptID: "same", By: kyle})
			out <- result{ex, created, err}
		}()
	}
	arrived.Wait() // both are rendering
	close(release)
	a, b := <-out, <-out
	if a.err != nil || b.err != nil {
		t.Fatalf("%v %v", a.err, b.err)
	}
	if a.ex.ID != b.ex.ID || a.created == b.created {
		t.Fatalf("one export, made by one of them: %+v / %+v", a, b)
	}
	var n int
	if err := e.db.Reader().QueryRow(`SELECT COUNT(*) FROM room_exports WHERE room_id=?`, r.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d exports stored (%v)", n, err)
	}
}

// An export takes a bounded number of messages, and says so (L18).
func TestAnExportOfTooManyMessagesIsRefused(t *testing.T) {
	e := newEnv(t)
	r := e.channel("export-many")
	for i := 0; i < 12; i++ {
		e.say(r, kyle, fmt.Sprintf("message %d", i))
	}
	small := New(e.db, e.bus, Options{MaxExportMessages: 10})
	if _, _, err := small.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "w", ReceiptID: "1", By: kyle}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "12 messages") {
		t.Fatalf("an export over the cap: %v", err)
	}
	if ex, _, err := small.ExportMinutes(e.ctx, r.ID, ExportRequest{Scope: "w", ReceiptID: "2", By: kyle, FromSeq: 3, ToSeq: 12}); err != nil || ex.ID == "" {
		t.Fatalf("a range within the cap: %v", err)
	}
}
