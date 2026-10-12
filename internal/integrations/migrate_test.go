package integrations_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Migration 00114 (decision P42) moves connections made when a connection carried one project to workspace connections with
// assignments. It starts here from the old shape (schema 111), with rows as the old code wrote them, and checks that nothing is
// lost: the same credential reference, the same scope, the same cursor, the same items (with their tasks), the same rules,
// and that the service carries on from there (a sync resumes from the old watermark and re-creates nothing).
func TestMigrationFromProjectConnectionsToWorkspaceConnectionsKeepsEverything(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := linearFixture(t, 5)
	if err := h.db.MigrateTo(ctx, 111); err != nil {
		t.Fatal(err)
	}
	wm := baseTime.Add(-10 * time.Minute).UnixMilli()
	for _, q := range []string{
		// a Linear connection for project prj1, connected to the hosted MCP server with a pasted key
		`INSERT INTO integrations(id,kind,name,project_id,status,auth_kind,credential_ref,base_url,config_json,poll_interval_s,enabled,created_at,updated_at,version)
		 VALUES('int_lin','linear','My Linear','prj1','ok','api_key','integrations/int_lin/token','https://mcp.linear.app/mcp','{"team":"ENG"}',0,1,1,1,3)`,
		// an Aikido connection without a project, run as the stdio MCP server (argv in its config)
		`INSERT INTO integrations(id,kind,name,project_id,status,auth_kind,credential_ref,base_url,config_json,poll_interval_s,enabled,created_at,updated_at,version)
		 VALUES('int_aik','aikido','Aikido','','ok','api_key','integrations/int_aik/token','','{"argv":"[\"npx\",\"@aikidosec/mcp\"]","repo":"web"}',0,1,2,2,1)`,
		// a self-hosted Sentry for prj2
		`INSERT INTO integrations(id,kind,name,project_id,status,auth_kind,credential_ref,base_url,config_json,poll_interval_s,enabled,created_at,updated_at,version)
		 VALUES('int_sen','sentry','Sentry','prj2','ok','api_key','integrations/int_sen/token','https://sentry.example.com','{"organization":"acme"}',0,1,3,3,1)`,
		`INSERT INTO integration_cursors(integration_id,scope,cursor_json,watermark,last_success_at,last_attempt_at,items_last_sync) VALUES('int_lin','default','{}',` + itoa(wm) + `,5,5,5)`,
		`INSERT INTO integration_cursors(integration_id,scope,cursor_json,watermark,last_success_at,last_attempt_at) VALUES('int_aik','default','{}',7,7,7)`,
		// items of the old shape: one of them became a task
		`INSERT INTO external_items(id,integration_id,source,project_id,external_id,kind,issue_ref,title,content_hash,fetched_at,external_updated_at,task_id,imported_by,imported_at)
		 VALUES('ext_a','int_lin','linear','prj1','uuid-0000','issue','ENG-1000','Issue number 0','h0',1,` + itoa(baseTime.UnixMilli()) + `,'task_old','human:kyle',4)`,
		`INSERT INTO external_items(id,integration_id,source,project_id,external_id,kind,issue_ref,title,content_hash,fetched_at,external_updated_at)
		 VALUES('ext_b','int_lin','linear','prj1','uuid-0009','issue','ENG-991','An older issue','h9',1,1)`,
		`INSERT INTO external_items(id,integration_id,source,project_id,external_id,kind,issue_ref,title,content_hash,fetched_at)
		 VALUES('ext_c','int_aik','aikido','','77','finding','AIK-77','SQL injection','hc',1)`,
		`INSERT INTO integration_rules(id,integration_id,project_id,name,when_cel,action,created_by,created_at,updated_at) VALUES('rule_1','int_lin','prj1','bugs','item.state == "Todo"','create_task','human:kyle',1,1)`,
		// an outbound write proposed through the MCP tool and waiting for approval, and one already sent
		`INSERT INTO approvals(id,project_id,kind,title,status,created_at) VALUES('apr_wait','prj1','approval','comment','REQUESTED',1)`,
		`INSERT INTO integration_outbox(id,integration_id,project_id,op,target_external_id,tool,payload_json,payload_hash,approval_id,status,idempotency_key,created_by,created_at)
		 VALUES('out_wait','int_lin','prj1','comment','uuid-0000','create_comment','{"issueId":"uuid-0000","body":"x"}','ph','apr_wait','awaiting_approval','k1','human:kyle',1)`,
		`INSERT INTO integration_outbox(id,integration_id,project_id,op,target_external_id,tool,payload_json,payload_hash,status,idempotency_key,created_by,created_at,receipt)
		 VALUES('out_sent','int_lin','prj1','comment','uuid-0000','create_comment','{}','ph2','sent','k2','human:kyle',1,'ok')`,
	} {
		if _, err := h.db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// the old code kept the key in the credential store under the connection's reference
	if err := h.vault.Put(ctx, "integrations/int_lin/token", linearToken); err != nil {
		t.Fatal(err)
	}
	if err := h.db.MigrateTo(ctx, storage.LatestVersion()); err != nil { // 00114, then 00115 (legacy_adopted)
		t.Fatal(err)
	}

	// connections: the vendor's fixed name, the same credential reference, retested, MCP endpoints dropped, argv dropped
	lin, err := h.svc.Get(ctx, "int_lin")
	if err != nil {
		t.Fatal(err)
	}
	if lin.Name != "Linear" || lin.CredentialRef != "integrations/int_lin/token" || lin.Status != integrations.StatusUntested || lin.BaseURL != "" || !lin.HasCredential {
		t.Fatalf("%+v", lin.Connection)
	}
	if len(lin.Assignments) != 1 || lin.Assignments[0].ID != "asg_int_lin" || lin.Assignments[0].ProjectID != "prj1" || lin.Assignments[0].Config["team"] != "ENG" {
		t.Fatalf("assignments %+v", lin.Assignments)
	}
	if a := lin.Assignments[0].Sync; a.Watermark != wm || a.ItemsSeen != 2 || a.LastSuccessAt != 5 {
		t.Fatalf("the cursor did not move to the assignment: %+v", a)
	}
	sen, _ := h.svc.Get(ctx, "int_sen")
	if sen.BaseURL != "https://sentry.example.com" || len(sen.Assignments) != 1 || sen.Assignments[0].ProjectID != "prj2" || sen.Assignments[0].Config["organization"] != "acme" {
		t.Fatalf("%+v", sen)
	}
	aik, _ := h.svc.Get(ctx, "int_aik")
	if len(aik.Assignments) != 0 || aik.LegacyItems != 1 || aik.Config["repo"] != "web" || aik.Config["argv"] != "" {
		t.Fatalf("%+v", aik)
	}
	// items keep their ids, tasks and projects, now under the assignment
	for _, it := range h.items("int_lin") {
		if it.AssignmentID != "asg_int_lin" || it.ProjectID != "prj1" {
			t.Fatalf("%+v", it)
		}
		if it.ID == "ext_a" && (it.TaskID != "task_old" || it.ImportedBy != "human:kyle") {
			t.Fatalf("%+v", it)
		}
	}
	// rules are untouched; the unsent MCP write is cancelled with its approval, the sent one stays history
	if n := h.count(`SELECT COUNT(*) FROM integration_rules WHERE id='rule_1' AND project_id='prj1'`); n != 1 {
		t.Fatal("rule lost")
	}
	if w, _ := h.svc.Store().GetOutbox(ctx, "out_wait"); w.Status != integrations.OutCancelled || w.Error == "" {
		t.Fatalf("%+v", w)
	}
	if s, _ := h.svc.Store().GetOutbox(ctx, "out_sent"); s.Status != integrations.OutSent {
		t.Fatalf("%+v", s)
	}
	if ap, _ := h.appr.Get(ctx, "apr_wait"); ap.Status != "CANCELLED" {
		t.Fatalf("%+v", ap)
	}

	// the service carries on: the same key (only the API URL is new, for the fixture), the sync resumes from the old
	// watermark, and the item that already had a task is not imported again
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{ID: "int_lin", BaseURL: url}); err != nil {
		t.Fatal(err)
	}
	r := h.sync("int_lin", integrations.SyncOpts{})
	if r.New != 4 || r.Changed != 1 || !r.Complete {
		t.Fatalf("%+v", r) // uuid-0001..0004 are new to the store; uuid-0000 is the stored one, now read from the API
	}
	if since := fx.ListArgs()[0]["since"]; since != time.UnixMilli(wm).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("the first sync after the migration did not start at the old watermark: %v", since)
	}
	if n := h.count(`SELECT COUNT(*) FROM external_items WHERE integration_id='int_lin' AND task_id='task_old'`); n != 1 {
		t.Fatal("the imported item lost its task")
	}
	// the first assignment of the project-less Aikido connection adopts its items, cursor and stored scope
	av, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: "int_aik", ProjectID: "prj3"})
	if err != nil {
		t.Fatal(err)
	}
	if av.Config["repo"] != "web" || av.Sync.ItemsSeen != 1 || av.Sync.LastSuccessAt != 7 {
		t.Fatalf("%+v", av)
	}
	if it, _ := h.svc.Store().GetItem(ctx, "ext_c"); it.AssignmentID != av.ID || it.ProjectID != "prj3" {
		t.Fatalf("%+v", it)
	}

	// and back down: the schema of 111 again, with the first assignment's project on each connection
	if err := h.db.MigrateTo(ctx, 111); err != nil {
		t.Fatal(err)
	}
	var proj, cfg string
	if err := h.db.Reader().QueryRow(`SELECT project_id, config_json FROM integrations WHERE id='int_aik'`).Scan(&proj, &cfg); err != nil || proj != "prj3" || cfg != `{"repo":"web"}` {
		t.Fatalf("%q %q %v", proj, cfg, err)
	}
	if err := h.db.MigrateTo(ctx, storage.LatestVersion()); err != nil {
		t.Fatal(err)
	}
}

// Only the first assignment ever of a connection migrated without a project adopts its old scope, items, cursor and rules.
// Assigned, unassigned and assigned again, the connection has no assignment either time, but the second assignment must not
// adopt the old scope again (it was adopted, and maybe deliberately changed, the first time) nor move anything.
func TestLegacyScopeIsAdoptedOnlyByTheFirstAssignmentEver(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	if err := h.db.MigrateTo(ctx, 111); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO integrations(id,kind,name,project_id,status,auth_kind,credential_ref,base_url,config_json,poll_interval_s,enabled,created_at,updated_at,version)
		 VALUES('int_aik','aikido','Aikido','','ok','api_key','integrations/int_aik/token','','{"repo":"web"}',0,1,2,2,1)`,
		`INSERT INTO external_items(id,integration_id,source,project_id,external_id,kind,issue_ref,title,content_hash,fetched_at)
		 VALUES('ext_c','int_aik','aikido','','77','finding','AIK-77','SQL injection','hc',1)`,
		`INSERT INTO integration_cursors(integration_id,scope,cursor_json,watermark,last_success_at,last_attempt_at) VALUES('int_aik','default','{}',7,7,7)`,
	} {
		if _, err := h.db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := h.db.MigrateTo(ctx, storage.LatestVersion()); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.svc.Get(ctx, "int_aik"); v.LegacyItems != 1 {
		t.Fatalf("%+v", v)
	}
	a, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: "int_aik", ProjectID: "prj3"})
	if err != nil || a.Config["repo"] != "web" || a.Sync.ItemsSeen != 1 {
		t.Fatalf("the first assignment must adopt: %+v %v", a, err)
	}
	if err := h.svc.Unassign(ctx, human, a.ID); err != nil {
		t.Fatal(err)
	}
	// rows that would move if the adoption ran again: a project-less item, a pre-P42 cursor and a project-less rule
	for _, q := range []string{
		`INSERT INTO external_items(id,integration_id,assignment_id,source,project_id,external_id,kind,issue_ref,title,content_hash,fetched_at)
		 VALUES('ext_d','int_aik','','aikido','','78','finding','AIK-78','Another','hd',1)`,
		`INSERT INTO integration_cursors(integration_id,scope,cursor_json,watermark,last_success_at,last_attempt_at) VALUES('int_aik','default','{}',9,9,9)`,
		`INSERT INTO integration_rules(id,integration_id,project_id,name,when_cel,action,created_by,created_at,updated_at) VALUES('rule_x','int_aik','','x','true','create_task','human:kyle',1,1)`,
	} {
		if _, err := h.db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if v, _ := h.svc.Get(ctx, "int_aik"); v.LegacyItems != 0 || len(v.Assignments) != 0 {
		t.Fatalf("an unassigned connection is not waiting to adopt anything: %+v", v)
	}
	b, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: "int_aik", ProjectID: "prj4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Config) != 0 || b.Sync.ItemsSeen != 0 || b.Sync.LastSuccessAt != 0 {
		t.Fatalf("the second assignment adopted the old scope again: %+v", b)
	}
	if n := h.count(`SELECT COUNT(*) FROM external_items WHERE id='ext_d' AND assignment_id=''`); n != 1 {
		t.Fatal("the item adoption ran again")
	}
	if n := h.count(`SELECT COUNT(*) FROM integration_cursors WHERE integration_id='int_aik' AND scope='default'`); n != 1 {
		t.Fatal("the cursor adoption ran again")
	}
	if n := h.count(`SELECT COUNT(*) FROM integration_rules WHERE id='rule_x' AND project_id=''`); n != 1 {
		t.Fatal("the rule adoption ran again")
	}
	// 00114's data is intact: the connection still holds its old scope
	var cfg string
	if err := h.db.Reader().QueryRow(`SELECT config_json FROM integrations WHERE id='int_aik'`).Scan(&cfg); err != nil || cfg != `{"repo":"web"}` {
		t.Fatalf("%q %v", cfg, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
