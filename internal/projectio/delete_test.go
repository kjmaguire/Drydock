package projectio

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// every table in the schema is either deleted with a project or kept with a reason
func TestEverySchemaTableIsDecidedForProjectDeletion(t *testing.T) {
	db := newDB(t)
	problems, err := UndecidedOnDelete(context.Background(), db.Reader())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// the guard really fails when a future migration adds a table
func TestAFutureTableMustBeDecidedForProjectDeletion(t *testing.T) {
	db := newDB(t)
	mustExec(t, db, `CREATE TABLE future_feature(id TEXT PRIMARY KEY, project_id TEXT)`)
	problems, err := UndecidedOnDelete(context.Background(), db.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], `"future_feature"`) {
		t.Errorf("problems: %v", problems)
	}
}

// populateMore adds project A's rows in the tables the export fixture leaves empty, so the deletion is tested on every kind
// of project data.
func populateMore(t testing.TB, db *sql.DB) {
	const now = 1760000000000
	x := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	for _, k := range []string{"devloop:", "sandbox:", "runmode:", "security:rules:", "mcp.act_tools:", "git.trusted."} {
		x(`INSERT OR REPLACE INTO settings(key,value) VALUES(?,'{}')`, k+"prj_aaaaaaaaaaaa")
	}
	x(`INSERT INTO settings(key,value) VALUES('devloop:prj_bbbbbbbbbbbb','{}')`)
	x(`INSERT INTO chats(id,project_id,title,adapter,created_at,updated_at) VALUES('chat_cccccccccccc','prj_aaaaaaaaaaaa','my chat','claude',?,?)`, now, now)
	x(`INSERT INTO integrations(id,kind,project_id,credential_ref,created_at,updated_at) VALUES('int_legacyxxxxxx','notion','prj_aaaaaaaaaaaa','',?,?)`, now, now)
	x(`INSERT INTO integration_assignments(id,integration_id,project_id,created_at,updated_at) VALUES('asg_aaaaaaaaaaaa','int_aaaaaaaaaaaa','prj_aaaaaaaaaaaa',?,?)`, now, now)
	x(`INSERT INTO integration_cursors(integration_id,scope) VALUES('int_aaaaaaaaaaaa','asg_aaaaaaaaaaaa')`)
	x(`INSERT INTO integration_rules(id,integration_id,project_id,name,when_cel,action,created_by,created_at,updated_at) VALUES('irule_aaaaaaaaaaaa','int_aaaaaaaaaaaa','prj_aaaaaaaaaaaa','r','true','notify','human:x',?,?)`, now, now)
	x(`INSERT INTO external_items(id,integration_id,assignment_id,source,project_id,external_id,kind,body_ref,content_hash,fetched_at) VALUES('item_aaaaaaaaaaaa','int_aaaaaaaaaaaa','asg_aaaaaaaaaaaa','linear','prj_aaaaaaaaaaaa','LIN-1','issue','item_aaaaaaaaaaaa.md','h',?)`, now)
	x(`INSERT INTO integration_mappings(assignment_id,scope,remote_id,local,created_by,created_at) VALUES('asg_aaaaaaaaaaaa','status','st_done','DONE','human:x',?)`, now)
	x(`INSERT INTO integration_auto_approve(integration_id,project_id,class,enabled,created_by,created_at,updated_at) VALUES('int_aaaaaaaaaaaa','prj_aaaaaaaaaaaa','status_sync',1,'human:x',?,?)`, now, now)
	x(`INSERT INTO integration_attachments(id,item_id,integration_id,external_id,local_path,listed_at) VALUES('iat_aaaaaaaaaaaa','item_aaaaaaaaaaaa','int_aaaaaaaaaaaa','a1','int_aaaaaaaaaaaa/attachments/item_aaaaaaaaaaaa/iat_aaaaaaaaaaaa.txt',?)`, now)
	x(`INSERT INTO integration_rule_matches(rule_id,item_id,outcome,first_at,last_at) VALUES('irule_aaaaaaaaaaaa','item_aaaaaaaaaaaa','would_create',?,?)`, now, now)
	x(`INSERT INTO integration_outbox(id,integration_id,project_id,task_id,op,tool,payload_json,payload_hash,status,idempotency_key,created_by,created_at) VALUES('out_aaaaaaaaaaaa','int_aaaaaaaaaaaa','prj_aaaaaaaaaaaa','task_aaaaaaaaaaaa','comment','t','{}','h','planned','k_aaaaaaaaaaaa','human:x',?)`, now)
	x(`INSERT INTO skill_uses(ts,project_id,skill_name,how) VALUES(?,'prj_aaaaaaaaaaaa','style','tool')`, now)
	x(`INSERT INTO skill_deliveries(ts,project_id,skill_name,method,fidelity) VALUES(?,'prj_aaaaaaaaaaaa','style','tool','on_demand')`, now)
	x(`INSERT INTO security_scans(id,project_id,status,started_at,finished_at,report_json) VALUES('scan_aaaaaaaaaaaa','prj_aaaaaaaaaaaa','done',?,?,'{}')`, now, now)
	x(`INSERT INTO security_dismissals(target_key,fingerprint,reason,dismissed_by,dismissed_at,scan_id) VALUES('prj_aaaaaaaaaaaa','0123456789abcdef01234567','not_real','human:x',?,'scan_aaaaaaaaaaaa')`, now)
	x(`INSERT INTO recommendations(id,project_id,agent_id,trigger,status,created_at) VALUES('rec_aaaaaaaaaaaa','prj_aaaaaaaaaaaa','agt_aaaaaaaaaaaa','manual','done',?)`, now)
	x(`INSERT INTO lead_interventions(id,project_id,lead_agent_id,trigger_kind,action,created_at) VALUES('lead_aaaaaaaaaaaa','prj_aaaaaaaaaaaa','agt_aaaaaaaaaaaa','stall','nudge',?)`, now)
	x(`INSERT INTO generated_files(project_id,path,body_sha,generated_at) VALUES('prj_aaaaaaaaaaaa','AGENTS.md','h',?)`, now)
	x(`INSERT INTO llm_cache(key,project_id,provider,model,response,data_class,created_at,expires_at) VALUES('k_aaaaaaaaaaaa','prj_aaaaaaaaaaaa','p','m',x'00','private',?,?)`, now, now)
	x(`INSERT INTO mcp_cache(key,server_id,tool,scope,raw_ref,created_at,expires_at) VALUES('k_aaaaaaaaaaaa','srv','t','project:prj_aaaaaaaaaaaa','raw_aaaaaaaaaaaa',?,?)`, now, now)
	x(`INSERT INTO mcp_usage(call_id,run_id,created_at) VALUES('call_aaaaaaaaaaaa','run_aaaaaaaaaaaa',?)`, now)
	x(`INSERT INTO mcp_usage(call_id,run_id,created_at) VALUES('call_aaaaaaaaaaab','arun_aaaaaaaaaaaa',?)`, now)
	x(`INSERT INTO mcp_library(id,display,transport,created_at,updated_at) VALUES('mcp_global','Global','stdio',?,?)`, now, now)
	x(`INSERT INTO project_mcp_servers(project_id,server_id,added_at) VALUES('prj_aaaaaaaaaaaa','mcp_global',?)`, now)
	x(`INSERT INTO agent_mcp_access(project_id,server_id,agent_id,granted_at) VALUES('prj_aaaaaaaaaaaa','mcp_global','agt_aaaaaaaaaaaa',?)`, now)
	x(`INSERT INTO packs(id,title,kind,visibility,manifest,manifest_hash,created_at,updated_at) VALUES('pack_global','docs','indexed','public','x','h',?,?)`, now, now)
	x(`INSERT INTO pack_attachments(project_id,pack_id,created_at) VALUES('prj_aaaaaaaaaaaa','pack_global',?)`, now)
	x(`INSERT INTO room_attachments(id,room_id,name,size,sha256,path,created_at) VALUES('att_aaaaaaaaaaaa','room_aaaaaaaaaaaa','a.txt',1,'h','room_aaaaaaaaaaaa/att_aaaaaaaaaaaa/a.txt',?)`, now)
	x(`INSERT INTO room_inbox_acks(item_key,acked_by,acked_at) VALUES('question:msg_aaaaaaaaaaab','human:x',?)`, now)
	x(`INSERT INTO room_inbox_acks(item_key,acked_by,acked_at) VALUES('watchdog:task_aaaaaaaaaaaa','human:x',?)`, now)
	x(`INSERT INTO run_leases(run_id,owner,expires_at) VALUES('run_aaaaaaaaaaaa','me',?)`, now)
	x(`INSERT INTO sessions(id,agent_id,created_at) VALUES('ses_aaaaaaaaaaaa','agt_aaaaaaaaaaaa',?)`, now)
	// a routed reply in a channel (P63): its decision belongs to no task or workflow run, only to the room turn
	x(`INSERT INTO routing_decisions(id,ts,run_id,task_id,node_id,run_kind,decided_by,request,decision) VALUES('dec_replyxxxxxxx',?,'turn_aaaaaaaaaaaa','','reply','room_reply','rules',x'7b7d',x'7b7d')`, now)
}

// Deleting project A leaves nothing of it outside the tables that are kept on purpose, keeps project B whole, keeps the
// event log, the audit log and the usage, and keeps the human's chats, unlinked.
func TestDeleteRemovesEveryRowOfTheProjectAndNothingElse(t *testing.T) {
	db := newDB(t)
	populate(t, db, t.TempDir())
	populateMore(t, db.Writer())
	ctx := context.Background()

	var res DeleteResult
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		res, err = Delete(ctx, tx, "prj_aaaaaaaaaaaa")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows < 60 {
		t.Errorf("deleted %d rows", res.Rows)
	}
	if strings.Join(res.MCPCache, ",") != "raw_aaaaaaaaaaaa" || strings.Join(res.ItemBodies, ",") != "int_aaaaaaaaaaaa/item_aaaaaaaaaaaa.txt" ||
		strings.Join(res.Attachments, ",") != "int_aaaaaaaaaaaa/attachments/item_aaaaaaaaaaaa" {
		t.Errorf("files %+v", res)
	}

	// every cell of every table that is not kept: nothing names project A or its rows (the global connection int_aaaa... is
	// the workspace's and stays)
	tables, err := db.Reader().Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND COALESCE(sql,'') NOT LIKE 'CREATE VIRTUAL%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var n string
		tables.Scan(&n)
		names = append(names, n)
	}
	tables.Close()
	for _, n := range names {
		if _, kept := KeptOnDelete[n]; kept || strings.Contains(n, "_fts") {
			continue
		}
		for _, cell := range dump(t, db.Reader(), n) {
			if strings.Contains(cell, "aaaaaaaaaaaa") && !(n == "integrations" && strings.Contains(cell, "int_aaaaaaaaaaaa")) {
				t.Errorf("%s still holds %q", n, cell)
			}
		}
	}

	for _, tc := range []struct {
		q    string
		want int
	}{
		{`SELECT COUNT(*) FROM projects WHERE id='prj_bbbbbbbbbbbb'`, 1},
		{`SELECT COUNT(*) FROM tasks WHERE project_id='prj_bbbbbbbbbbbb'`, 1},
		{`SELECT COUNT(*) FROM messages WHERE room_id='room_bbbbbbbbbbbb'`, 1},
		{`SELECT COUNT(*) FROM settings WHERE key='devloop:prj_bbbbbbbbbbbb'`, 1},
		{`SELECT COUNT(*) FROM settings WHERE key='license.token'`, 1},
		{`SELECT COUNT(*) FROM events WHERE project_id='prj_aaaaaaaaaaaa'`, 1},
		{`SELECT COUNT(*) FROM model_usage`, 1},
		{`SELECT COUNT(*) FROM providers`, 1},
		{`SELECT COUNT(*) FROM mcp_library`, 1},
		{`SELECT COUNT(*) FROM packs`, 1},
		{`SELECT COUNT(*) FROM integrations`, 2},
		{`SELECT COUNT(*) FROM chats WHERE id='chat_cccccccccccc' AND project_id=''`, 1},
	} {
		if got := count(t, db, tc.q); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.q, got, tc.want)
		}
	}

	// a second delete, or an unknown project, is an error and changes nothing
	err = db.WriteTx(ctx, func(tx *sql.Tx) error { _, err := Delete(ctx, tx, "prj_aaaaaaaaaaaa"); return err })
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("second delete: %v", err)
	}
}

// dump returns every cell of a table as text.
func dump(t testing.TB, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM ` + table)
	if err != nil {
		t.Fatalf("%s: %v", table, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			switch v := v.(type) {
			case []byte:
				out = append(out, cols[i]+"="+string(v))
			case nil:
			default:
				out = append(out, fmt.Sprintf("%s=%v", cols[i], v))
			}
		}
	}
	return out
}
