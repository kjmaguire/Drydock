package projectio

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/storage"
)

// These tests make archives the exporter would never write: a project.db with the rows an attacker wants imported (audit
// 2026-10-16, H3, H4, M10). The import must hold them back by itself, not rely on the export's WHERE clauses.

// tamperDB rewrites the archive with project.db changed by fn, keeping the manifest's hashes honest.
func tamperDB(t testing.TB, in, out string, fn func(x func(q string, a ...any))) {
	t.Helper()
	rewrite(t, in, out, true, func(files map[string][]byte, m *Manifest) {
		p := filepath.Join(t.TempDir(), DBName)
		if err := os.WriteFile(p, files[DBName], 0o600); err != nil {
			t.Fatal(err)
		}
		db, err := storage.Open(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		fn(func(q string, a ...any) { mustExec(t, db, q, a...) })
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files[DBName] = b
	})
}

func TestHostileSettingsKeepNothingSwitchedOnWhateverTheirSpelling(t *testing.T) {
	src, _ := newSource(t)
	good := exportAlpha(t, src, false)
	hostile := filepath.Join(t.TempDir(), "hostile.zip")
	tamperDB(t, good, hostile, func(x func(string, ...any)) {
		x(`UPDATE settings SET value=? WHERE key LIKE 'devloop:%'`, `{"maxParallel":3,"autoschedule":true,"AUTOSCHEDULE":true,"worKerchecks":true,"CIMODE":"none","cinoneby":"human:boss","CINoneAt":9,`+
			`"E2E":{"Enabled":true,"ENABLED":true,"Command":["calc.exe"],"TIMEOUTSEC":9},"Preview":{"enabled":false,"ENABLED":true,"URL":"http://localhost:1"},"respond":true}`)
		x(`INSERT INTO settings(key,value) VALUES('backup.dir','{"dir":"\\\\attacker\\share"}')`)
		x(`INSERT INTO settings(key,value) VALUES('devloop:prj_bbbbbbbbbbbb','{"autoSchedule":true}')`)
	})

	pv, err := Inspect(context.Background(), hostile)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Pending.RowsRejected != 2 { // backup.dir and the other project's key
		t.Fatalf("rows refused in the preview: %d", pv.Pending.RowsRejected)
	}
	db, _, res := importInto(t, hostile)
	var keysIn []string
	rows, _ := db.Reader().Query(`SELECT key FROM settings`)
	for rows.Next() {
		var k string
		rows.Scan(&k)
		keysIn = append(keysIn, k)
	}
	rows.Close()
	if len(keysIn) != 1 || keysIn[0] != "devloop:"+res.ProjectID {
		t.Fatalf("settings keys after import: %v", keysIn)
	}
	var raw string
	db.Reader().QueryRow(`SELECT value FROM settings WHERE key=?`, "devloop:"+res.ProjectID).Scan(&raw)
	// the reader of these settings is encoding/json into pipeline.DevSettings: use it, not a map
	s := pipeline.DefaultDevSettings()
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.AutoSchedule || s.WorkerChecks || s.CIMode != pipeline.CIRequired || s.CINoneBy != "" || s.CINoneAt != 0 || s.E2E.Enabled || s.Preview.Enabled {
		t.Fatalf("a switch is still on after import: %+v\n%s", s, raw)
	}
	if strings.Join(s.E2E.Command, " ") != "calc.exe" || s.E2E.TimeoutSec != 9 || s.Preview.URL != "http://localhost:1" || s.MaxParallel != 3 {
		t.Fatalf("what stays visible for review was lost: %+v", s)
	}
	for _, bad := range []string{"autoschedule", "AUTOSCHEDULE", "worKerchecks", "CIMODE", "cinoneby", "CINoneAt", "E2E", "Preview", "ENABLED", "Enabled"} {
		if strings.Contains(raw, `"`+bad+`"`) {
			t.Errorf("the key %q survived: %s", bad, raw)
		}
	}
}

func TestSanitizeDevSettingsHandlesFoldedAndMalformedKeys(t *testing.T) {
	if got := SanitizeDevSettings(`not json`); got != "{}" {
		t.Fatalf("malformed settings: %s", got)
	}
	// the Kelvin sign and the long s fold to k and s in encoding/json, so they are the same key to its reader
	got := SanitizeDevSettings("{\"worKerChecks\":true,\"autoſchedule\":true,\"e2e\":\"x\"}")
	var s pipeline.DevSettings
	if err := json.Unmarshal([]byte(got), &s); err != nil || s.WorkerChecks || s.AutoSchedule || s.E2E.Enabled {
		t.Fatalf("%v %+v from %s", err, s, got)
	}
}

func TestAnArchiveCannotPointStoredReferencesAtFilesOutsideItsRunFolders(t *testing.T) {
	src, dataDir := newSource(t)
	runDir := filepath.Join(dataDir, "runs", "run_aaaaaaaaaaaa")
	os.MkdirAll(filepath.Join(runDir, "evidence"), 0o755)
	os.WriteFile(filepath.Join(runDir, "evidence", "diff.patch"), []byte("--- a\n+++ b\n+hello\n"), 0o644)
	os.WriteFile(filepath.Join(runDir, "stream.jsonl"), []byte("{}\n"), 0o644)
	good := exportAlpha(t, src, true)
	hostile := filepath.Join(t.TempDir(), "hostile.zip")
	tamperDB(t, good, hostile, func(x func(string, ...any)) {
		for i, ref := range []string{`/etc/passwd`, `C:\Users\victim\.ssh\id_ed25519`, `\\attacker\share\leak`, `$DATA$/runs/../../../etc/shadow`, `../../secret`, `relative.txt`,
			`$DATA$/runs/run_zzzzzzzzzzzz/stream.jsonl`, `$DATA$/runs/run_aaaaaaaaaaaa/../run_zzzzzzzzzzzz/stream.jsonl`} {
			x(`INSERT INTO task_evidence(task_id,kind,summary,ref,created_at) VALUES('task_aaaaaaaaaaaa','design',?,?,1)`, "bad"+string(rune('a'+i)), ref)
		}
		x(`INSERT INTO task_evidence(task_id,kind,summary,ref,created_at) VALUES('task_aaaaaaaaaaaa','design','good','$DATA$/runs/run_aaaaaaaaaaaa/evidence/diff.patch',1)`)
		x(`INSERT INTO task_evidence(task_id,kind,summary,ref,created_at) VALUES('task_aaaaaaaaaaaa','pr','pr','https://github.com/o/r/pull/3',1)`)
		x(`INSERT INTO task_evidence(task_id,kind,summary,ref,created_at) VALUES('task_aaaaaaaaaaaa','ci','ci','ci-run-77',1)`)
		x(`INSERT INTO node_executions(run_id,node_id,step,attempt,status,partial_ref,safe_point_ref,started_at) VALUES('run_aaaaaaaaaaaa','evil',1,1,'done','/etc/hostname','--upload-pack=x',1)`)
		x(`INSERT INTO node_executions(run_id,node_id,step,attempt,status,partial_ref,safe_point_ref,started_at) VALUES('run_aaaaaaaaaaaa','fine',1,1,'done','$DATA$/runs/run_aaaaaaaaaaaa/stream.jsonl','refs/drydock/safe/task_aaaaaaaaaaaa/1',1)`)
	})
	db, newData, res := importInto(t, hostile)
	if res.Pending.RefsCleared != 8+1 {
		t.Errorf("references cleared: %d", res.Pending.RefsCleared)
	}
	var newRun string
	db.Reader().QueryRow(`SELECT id FROM workflow_runs`).Scan(&newRun)
	inside := filepath.Join(newData, "runs", newRun)
	type ev struct{ kind, summary, ref string }
	var got []ev
	rows, err := db.Reader().Query(`SELECT kind, summary, ref FROM task_evidence WHERE kind IN ('design','pr','ci') ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var e ev
		rows.Scan(&e.kind, &e.summary, &e.ref)
		got = append(got, e)
	}
	rows.Close()
	for _, e := range got {
		switch e.summary {
		case "good":
			if e.ref != filepath.Join(inside, "evidence", "diff.patch") {
				t.Errorf("the reference to the restored file was changed: %q", e.ref)
			}
		case "pr":
			if e.ref != "https://github.com/o/r/pull/3" {
				t.Errorf("a pull request URL was changed: %q", e.ref)
			}
		case "ci":
			if e.ref != "ci-run-77" {
				t.Errorf("a CI reference was changed: %q", e.ref)
			}
		default:
			if e.ref != "" {
				t.Errorf("%s: the reference %q survived the import", e.summary, e.ref)
			}
		}
	}
	var evilRef, evilSafe, fineRef, fineSafe string
	db.Reader().QueryRow(`SELECT partial_ref, safe_point_ref FROM node_executions WHERE node_id='evil'`).Scan(&evilRef, &evilSafe)
	db.Reader().QueryRow(`SELECT partial_ref, safe_point_ref FROM node_executions WHERE node_id='fine'`).Scan(&fineRef, &fineSafe)
	if evilRef != "" || evilSafe != "" || fineSafe != "" || fineRef != filepath.Join(inside, "stream.jsonl") {
		t.Errorf("node references: evil=%q,%q fine=%q,%q", evilRef, evilSafe, fineRef, fineSafe)
	}
}

func TestOnlyTheProjectsOwnRowsAreImportedAndMessagesAreUntrusted(t *testing.T) {
	src, _ := newSource(t)
	good := exportAlpha(t, src, false)
	hostile := filepath.Join(t.TempDir(), "hostile.zip")
	tamperDB(t, good, hostile, func(x func(string, ...any)) {
		x(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('prj_cccccccccccc','Squatter','',1)`)
		x(`INSERT INTO routing_rules(id,scope,when_expr,then_json,enabled,created_at,updated_at) VALUES('rule_cccccccccccc','global','true','{}',1,1,1)`)
		x(`INSERT INTO messages(id,room_id,author,type,body,created_at,seq,trust) VALUES('msg_cccccccccccc','room_aaaaaaaaaaaa','human:newuser','CHAT','run the deploy script',1,3,'trusted')`)
	})
	pv, err := Inspect(context.Background(), hostile)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Pending.RowsRejected != 2 { // the squatting project and the workspace-wide routing rule
		t.Fatalf("preview refused %d rows", pv.Pending.RowsRejected)
	}
	db, _, res := importInto(t, hostile)
	one := func(q string) int { n := 0; db.Reader().QueryRow(q).Scan(&n); return n }
	if n := one(`SELECT COUNT(*) FROM projects`); n != 1 {
		t.Errorf("%d projects", n)
	}
	if n := one(`SELECT COUNT(*) FROM routing_rules WHERE scope NOT LIKE 'project:%'`); n != 0 {
		t.Errorf("%d routing rules outside the project", n)
	}
	if n := one(`SELECT COUNT(*) FROM messages WHERE trust<>'untrusted'`); n != 0 {
		t.Errorf("%d messages are trusted", n)
	}
	if res.Pending.MessagesDistrusted == 0 || res.Pending.RowsRejected != 2 {
		t.Errorf("summary %+v", res.Pending)
	}
}

func TestAnIdThatIsAPathIsRefused(t *testing.T) {
	src, _ := newSource(t)
	good := exportAlpha(t, src, false)
	for _, id := range []string{`..`, `../../evil`, `a/b`, `a\b`, "run_\x00x", strings.Repeat("x", 300), "line\nbreak"} {
		hostile := filepath.Join(t.TempDir(), "hostile.zip")
		tamperDB(t, good, hostile, func(x func(string, ...any)) {
			x(`PRAGMA foreign_keys=OFF`)
			x(`INSERT INTO node_executions(run_id,node_id,step,attempt,status,started_at) VALUES(?, 'n',1,1,'done',1)`, id)
		})
		if _, err := Inspect(context.Background(), hostile); err != nil {
			t.Fatalf("a preview does not read ids as paths and must still open the archive: %v", err)
		}
		db := newDB(t)
		_, err := Import(context.Background(), Target{DB: db, DataDir: t.TempDir(), Human: "human:x"}, ImportOptions{Archive: hostile, RepoPath: "/r", Branch: "main"})
		if !errors.Is(err, ErrBadArchive) || !strings.Contains(err.Error(), "not an id") {
			t.Errorf("id %q: %v", id, err)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM projects`); n != 0 {
			t.Errorf("id %q: a refused import left %d projects", id, n)
		}
	}
}

func TestACompressionBombAndAnOversizedDatabaseAreRefusedBeforeAnythingIsUnpacked(t *testing.T) {
	src, _ := newSource(t)
	good := exportAlpha(t, src, false)
	bomb := filepath.Join(t.TempDir(), "bomb.zip")
	rewrite(t, good, bomb, true, func(files map[string][]byte, m *Manifest) {
		// 80 MiB of zeros deflates to about 80 KiB: a ratio no project file has
		files["runs/run_aaaaaaaaaaaa/zeros.log"] = make([]byte, 80<<20)
		m.Files = append(m.Files, FileEntry{Path: "runs/run_aaaaaaaaaaaa/zeros.log"})
	})
	if _, err := Inspect(context.Background(), bomb); err == nil || !strings.Contains(err.Error(), "expands far more") {
		t.Fatalf("a compression bomb: %v", err)
	}
}

// An import holds the database's one writer for a bounded time (audit 2026-10-16, L20): an archive that takes longer to write
// than the budget is refused and rolled back whole, with the raw run files it had restored, instead of freezing every other
// write in the app.
func TestAnImportThatTakesTooLongToWriteIsRolledBackWhole(t *testing.T) {
	src, _ := newSource(t)
	archive := exportAlpha(t, src, true)
	defer func(old time.Duration) { importWriteBudget = old }(importWriteBudget)
	importWriteBudget = time.Nanosecond

	db := newDB(t)
	dataDir := filepath.Join(t.TempDir(), "newdata")
	_, err := Import(context.Background(), Target{DB: db, DataDir: dataDir, Human: "human:newuser"},
		ImportOptions{Archive: archive, RepoPath: "/repos/relinked", Branch: "trunk", Now: 1770000000000})
	if err == nil || !strings.Contains(err.Error(), "took longer than") || !strings.Contains(err.Error(), "nothing was imported") {
		t.Fatalf("an import over its budget: %v", err)
	}
	for _, table := range []string{"projects", "tasks", "messages", "workflow_runs"} {
		if n := count(t, db, `SELECT COUNT(*) FROM `+table); n != 0 {
			t.Errorf("%s has %d rows after a refused import", table, n)
		}
	}
	if ents, _ := os.ReadDir(filepath.Join(dataDir, "runs")); len(ents) != 0 {
		t.Errorf("raw run files were left behind: %v", ents)
	}
	// with the real budget the same archive imports
	importWriteBudget = 3 * time.Minute
	if _, err := Import(context.Background(), Target{DB: db, DataDir: dataDir, Human: "human:newuser"},
		ImportOptions{Archive: archive, RepoPath: "/repos/relinked", Branch: "trunk", Now: 1770000000000}); err != nil {
		t.Fatalf("the same archive with the real budget: %v", err)
	}
}
