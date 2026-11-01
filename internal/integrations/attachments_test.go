package integrations_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

// fileHost is a labelled test double of a vendor's file storage on another host (Notion's signed S3 addresses): TLS, and it
// records whether a request carried an Authorization header.
type fileHost struct {
	mu    sync.Mutex
	auth  []string
	hits  int
	files map[string][]byte
}

func newFileHost(t *testing.T, files map[string][]byte) (*fileHost, *httptest.Server) {
	fh := &fileHost{files: files}
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fh.mu.Lock()
		fh.hits++
		fh.auth = append(fh.auth, r.Header.Get("Authorization"))
		b, ok := fh.files[strings.TrimPrefix(r.URL.Path, "/")]
		fh.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(b)
	}))
	t.Cleanup(ts.Close)
	return fh, ts
}

func TestAttachmentsAreListedAndDownloadedOnlyOnAPersonsAction(t *testing.T) {
	h := newHarness(t, func(o *integrations.Options) { o.MaxAttachmentBytes = 4096 })
	ctx := context.Background()
	fx, url := linearFixture(t, 1)
	fx.PutFile("crash.log", []byte("starting\nusing key "+linearToken+"\nAKIAZ7Q4VJ2NWX3KLM5P\npanic: nil map\n"))
	fx.PutFile("big.bin", []byte(strings.Repeat("x", 5000)))
	fx.PutAttachment("uuid-0000", itfixture.LinearAttachment{ID: "att-log", Title: "crash.log", URL: url + "/files/crash.log"})
	fx.PutAttachment("uuid-0000", itfixture.LinearAttachment{ID: "att-pr", Title: "PR 12", URL: "https://github.com/acme/web/pull/12"})
	fx.PutAttachment("uuid-0000", itfixture.LinearAttachment{ID: "att-big", Title: "core dump", URL: url + "/files/big.bin"})
	c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})
	h.sync(c.ID, integrations.SyncOpts{})
	it := h.items(c.ID)[0]
	if fx.Calls("attachments") != 0 {
		t.Fatal("sync read attachments")
	}
	if _, err := h.svc.RefreshAttachments(ctx, "agent:claude", it.ID); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatal(err)
	}
	atts, err := h.svc.RefreshAttachments(ctx, human, it.ID)
	if err != nil || len(atts) != 3 {
		t.Fatalf("%+v %v", atts, err)
	}
	byName := map[string]integrations.Attachment{}
	for _, a := range atts {
		byName[a.Name] = a
	}
	if !byName["crash.log"].Downloadable || byName["PR 12"].Downloadable || !byName["PR 12"].Untrusted {
		t.Fatalf("%+v", atts)
	}
	if _, err := h.svc.DownloadAttachment(ctx, human, byName["PR 12"].ID); !errors.Is(err, integrations.ErrOpUnsupported) {
		t.Fatalf("a link was fetched: %v", err)
	}
	if _, err := h.svc.DownloadAttachment(ctx, "agent:claude", byName["crash.log"].ID); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatal(err)
	}
	if _, err := h.svc.DownloadAttachment(ctx, human, byName["core dump"].ID); !errors.Is(err, integrations.ErrTooLarge) {
		t.Fatalf("the size cap did not hold: %v", err)
	}
	got, err := h.svc.DownloadAttachment(ctx, human, byName["crash.log"].ID)
	if err != nil || !got.TextKept || got.Redactions < 1 || got.Downloaded == 0 || got.DownloadedBy != human || got.SHA256 == "" {
		t.Fatalf("%+v %v", got, err)
	}
	// the stored copy is the redacted one; that is the only text anyone gets
	txt, err := h.svc.AttachmentText(ctx, got.ID)
	if err != nil || !strings.Contains(txt.Text, "panic: nil map") || strings.Contains(txt.Text, linearToken) || strings.Contains(txt.Text, "AKIAZ7Q4VJ2NWX3KLM5P") || !txt.Untrusted {
		t.Fatalf("%+v %v", txt, err)
	}
	files, _ := filepath.Glob(filepath.Join(h.dir, "integrations", c.ID, "attachments", it.ID, "*"))
	if len(files) != 1 {
		t.Fatalf("%v", files)
	}
	b, _ := os.ReadFile(files[0])
	if strings.Contains(string(b), linearToken) || strings.Contains(string(b), "AKIAZ7Q4VJ2NWX3KLM5P") {
		t.Fatal("a secret was written to disk")
	}
	if len(h.events(integrations.EventAttachmentDownloaded)) != 1 || strings.Contains(h.allEventsJSON(), "panic: nil map") {
		t.Fatal("the download event is missing, or carries the content")
	}
	// unassigning removes the downloads of items that never became tasks
	if err := h.svc.Unassign(ctx, human, c.Assignments[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatal("the download outlived its item")
	}
}

func TestNotionFilesAreFetchedWithoutTheCredentialAndSentryEventAttachments(t *testing.T) {
	fh, fts := newFileHost(t, map[string][]byte{"spec.txt": []byte("the spec text")})
	h := newHarness(t, func(o *integrations.Options) { o.Downloads = fts.Client() })
	ctx := context.Background()
	const ntoken = "ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc"
	nfx := itfixture.NewNotion(ntoken)
	nts, nurl := itfixture.Serve(nfx.Server())
	t.Cleanup(nts.Close)
	nfx.Put(itfixture.NotionPage{ID: "row-1", Title: "Spec row", Text: "x", Edited: baseTime, DataSource: "ds-1",
		Props: map[string]any{"Files": itfixture.NotionFiles("spec.txt", fts.URL+"/spec.txt", "Figma", "https://figma.example/file/1")}})
	nc := h.connect(integrations.KindNotion, ntoken, nurl, map[string]string{"data_source": "ds-1"})
	h.sync(nc.ID, integrations.SyncOpts{})
	row := h.items(nc.ID)[0]
	atts, err := h.svc.RefreshAttachments(ctx, human, row.ID)
	if err != nil || len(atts) != 2 {
		t.Fatalf("%+v %v", atts, err)
	}
	var hosted integrations.Attachment
	for _, a := range atts {
		if a.Name == "spec.txt" {
			hosted = a
		}
	}
	if !hosted.Downloadable || hosted.URL != "" { // the signed address expires: it is not kept
		t.Fatalf("%+v", hosted)
	}
	got, err := h.svc.DownloadAttachment(ctx, human, hosted.ID)
	if err != nil || !got.TextKept {
		t.Fatalf("%+v %v", got, err)
	}
	if fh.hits != 1 || fh.auth[0] != "" {
		t.Fatalf("the file host saw %d requests, Authorization %q", fh.hits, fh.auth)
	}

	// without a test client the download goes through internal/netguard, which refuses an address on this computer
	h2 := newHarness(t, nil)
	nc2 := h2.connect(integrations.KindNotion, ntoken, nurl, map[string]string{"data_source": "ds-1"})
	h2.sync(nc2.ID, integrations.SyncOpts{})
	atts2, _ := h2.svc.RefreshAttachments(ctx, human, h2.items(nc2.ID)[0].ID)
	for _, a := range atts2 {
		if a.Downloadable {
			if _, err := h2.svc.DownloadAttachment(ctx, human, a.ID); err == nil {
				t.Fatal("a file on this computer was fetched")
			}
		}
	}
	if fh.hits != 1 {
		t.Fatal("the guarded client reached the loopback file host")
	}

	// Sentry: the latest event's attachments, downloaded through the API; a binary file is never offered as text
	sfx, surl := sentryFixture(t)
	putSentry(sfx, "301", "error", "web", 1, 1)
	sfx.PutAttachment("301", itfixture.SentryAttachment{ID: "a1", Name: "minidump.dmp", Mime: "application/octet-stream", Data: []byte{0x4d, 0x44, 0x4d, 0x50, 0x93, 0xa7, 0x00, 0xff}})
	sc := h.connect(integrations.KindSentry, sentryToken, surl, map[string]string{"organization": "acme"})
	h.sync(sc.ID, integrations.SyncOpts{})
	sit := h.items(sc.ID)[0]
	satts, err := h.svc.RefreshAttachments(ctx, human, sit.ID)
	if err != nil || len(satts) != 1 || satts[0].Mime != "application/octet-stream" || satts[0].Size != 8 {
		t.Fatalf("%+v %v", satts, err)
	}
	sd, err := h.svc.DownloadAttachment(ctx, human, satts[0].ID)
	if err != nil || sd.TextKept || sd.Size != 8 {
		t.Fatalf("%+v %v", sd, err)
	}
	if _, err := h.svc.AttachmentText(ctx, sd.ID); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatal("a binary attachment was offered as text")
	}
	if sfx.Calls("attachment_download") != 1 {
		t.Fatal(sfx.Calls("attachment_download"))
	}
}

func TestNotionDataSourceRowsAreTypedAndFollowTheMapping(t *testing.T) {
	td := &taskDouble{status: map[string]string{}, refs: map[string]string{"TASK-12": "task_12"}}
	h := newHarness(t, td.wire)
	ctx := context.Background()
	const ntoken = "ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc"
	nfx := itfixture.NewNotion(ntoken)
	nts, nurl := itfixture.Serve(nfx.Server())
	t.Cleanup(nts.Close)
	nfx.PutDataSource("ds-1", map[string]any{
		"Name":     map[string]any{"name": "Name", "type": "title", "title": map[string]any{}},
		"Status":   map[string]any{"name": "Status", "type": "status", "status": map[string]any{"options": []any{map[string]any{"name": "Not started"}, map[string]any{"name": "In progress"}, map[string]any{"name": "Done"}}}},
		"Priority": map[string]any{"name": "Priority", "type": "select", "select": map[string]any{"options": []any{map[string]any{"name": "P1"}, map[string]any{"name": "P2"}}}},
		"Tags":     map[string]any{"name": "Tags", "type": "multi_select", "multi_select": map[string]any{}},
	})
	nfx.Put(itfixture.NotionPage{ID: "row-12", Title: "Add the login rate limit", Text: "details", Edited: baseTime, DataSource: "ds-1", Props: map[string]any{
		"Status":   map[string]any{"type": "status", "status": map[string]any{"name": "In progress"}},
		"Priority": map[string]any{"type": "select", "select": map[string]any{"name": "P1"}},
		"Owner":    map[string]any{"type": "people", "people": []any{map[string]any{"object": "user", "name": "Ada"}}},
		"Tags":     map[string]any{"type": "multi_select", "multi_select": []any{map[string]any{"name": "api"}, map[string]any{"name": "auth"}}},
		"ID":       map[string]any{"type": "unique_id", "unique_id": map[string]any{"prefix": "TASK", "number": 12}},
		"Estimate": map[string]any{"type": "number", "number": 3.5},
		"Notes":    map[string]any{"type": "rich_text", "rich_text": []any{map[string]any{"plain_text": "key AKIAZ7Q4VJ2NWX3KLM5P"}}},
	}})
	nfx.Put(itfixture.NotionPage{ID: "page-plain", Title: "A plain page", Text: "y", Edited: baseTime})
	nc := h.connect(integrations.KindNotion, ntoken, nurl, map[string]string{"data_source": "ds-1"})
	asg := nc.Assignments[0]
	h.sync(nc.ID, integrations.SyncOpts{})
	its := h.items(nc.ID)
	if len(its) != 1 {
		t.Fatalf("%+v", its)
	}
	row := its[0]
	p := row.Properties
	if row.State != "In progress" || row.Assignee != "Ada" || row.IssueRef != "TASK-12" || p["Estimate"].Text != "3.5" || p["Estimate"].Type != "number" ||
		p["Tags"].Text != "api, auth" || p["Priority"].Text != "P1" || row.Meta["type"] != "row" {
		t.Fatalf("%+v", row)
	}
	if strings.Contains(jsonOf(row), "AKIAZ7Q4VJ2NWX3KLM5P") || row.Redactions == 0 {
		t.Fatalf("a secret in a property was kept: %+v", p["Notes"])
	}
	// the person maps priority and labels; the next sync applies it to the unchanged row
	if _, err := h.svc.SetMappings(ctx, human, integrations.MappingsReq{AssignmentID: asg.ID, Scope: integrations.MapProperty,
		Mappings: []integrations.Mapping{{Remote: "priority", Local: "Priority"}, {Remote: "labels", Local: "Tags"}}}); err != nil {
		t.Fatal(err)
	}
	h.sync(nc.ID, integrations.SyncOpts{Force: true})
	row = h.items(nc.ID)[0]
	if row.Priority != "P1" || strings.Join(row.Labels, ",") != "api,auth" {
		t.Fatalf("%+v", row)
	}
	fields, err := h.svc.MappingFields(ctx, asg.ID, integrations.MapStatus)
	if err != nil {
		t.Fatal(err)
	}
	var done bool
	for _, f := range fields {
		if f.Name == "Done" && f.Property == "Status" {
			done = true
		}
	}
	if !done {
		t.Fatalf("%+v", fields)
	}
	// rules see the typed properties; a link_task rule links the row to the task with its reference
	r, err := h.svc.SetRule(ctx, human, integrations.RuleReq{IntegrationID: nc.ID, ProjectID: "prj1", Name: "estimated", When: `item.props["Estimate"] == "3.5"`, Action: integrations.ActionLinkTask})
	if err != nil {
		t.Fatal(err)
	}
	tru, fls := true, false
	if _, err := h.svc.SetRule(ctx, human, integrations.RuleReq{ID: r.ID, Version: r.Version, Enabled: &tru, DryRun: &fls}); err != nil {
		t.Fatal(err)
	}
	h.svc.EvaluateRules(ctx, mustConn(t, h, nc.ID), asg.Assignment)
	if row = h.items(nc.ID)[0]; row.TaskID != "task_12" {
		t.Fatalf("%+v", row)
	}
	// status sync writes the row's status property, through the approval
	if _, err := h.svc.SetMappings(ctx, human, integrations.MappingsReq{AssignmentID: asg.ID, Scope: integrations.MapStatus,
		Mappings: []integrations.Mapping{{Remote: "Done", Local: "COMPLETED", Direction: integrations.DirToVendor}}}); err != nil {
		t.Fatal(err)
	}
	h.svc.StatusSync(ctx)
	publishTask(t, h, "task.completed", "task_12", "COMPLETED")
	if n := h.svc.StatusSync(ctx); n != 1 {
		t.Fatal(n)
	}
	rows := outboxByOp(t, h, nc.ID, "set_status")
	if len(rows) != 1 || rows[0].Status != integrations.OutAwaiting || !strings.Contains(rows[0].Preview, `"Status" property`) {
		t.Fatalf("%+v", rows)
	}
	if len(nfx.PageUpdates()) != 0 {
		t.Fatal("written before approval")
	}
	if _, err := h.svc.Decide(ctx, human, rows[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Execute(ctx, human, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	up := nfx.PageUpdates()
	if len(up) != 1 || up[0]["id"] != "row-12" || !strings.Contains(jsonOf(up[0]), `"Status":{"status":{"name":"Done"}}`) {
		t.Fatalf("%+v", up)
	}
}

// The weekly automatic sweep turns a due poll into a full sweep once a week, so an item deleted upstream is noticed; it can be
// turned off.
func TestWeeklySweepMarksDeletedItemsGoneAndCanBeTurnedOff(t *testing.T) {
	for _, on := range []bool{true, false} {
		h := newHarness(t, nil)
		ctx := context.Background()
		if !h.svc.SweepSettings(ctx).Enabled {
			t.Fatal("the weekly sweep is not on by default")
		}
		if _, err := h.svc.SetSweep(ctx, "agent:claude", false); !errors.Is(err, integrations.ErrNotHuman) {
			t.Fatal(err)
		}
		if !on {
			if s, err := h.svc.SetSweep(ctx, human, false); err != nil || s.Enabled {
				t.Fatalf("%+v %v", s, err)
			}
		}
		fx, url := linearFixture(t, 3)
		c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})
		h.sync(c.ID, integrations.SyncOpts{})
		cur, _ := h.svc.Store().GetCursor(ctx, c.ID, c.Assignments[0].ID)
		if cur.LastFullAt == 0 {
			t.Fatal("the first complete listing did not start the weekly count")
		}
		fx.Delete("uuid-0002")
		h.clk.Advance(10 * time.Minute)
		h.svc.Tick(ctx)
		if len(h.items(c.ID)) != 3 {
			t.Fatal("an ordinary poll noticed a deletion (it cannot)")
		}
		h.clk.Advance(8 * 24 * time.Hour)
		h.svc.Tick(ctx)
		n := len(h.items(c.ID))
		if on && n != 2 {
			t.Fatalf("the weekly sweep did not mark the deleted issue gone: %d", n)
		}
		if !on && n != 3 {
			t.Fatalf("the sweep ran while it was off: %d", n)
		}
	}
}
