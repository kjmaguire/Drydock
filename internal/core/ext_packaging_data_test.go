package core_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

func readZipFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

// a token the secret scanner recognizes
const plantedToken = "ghp_aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5"

func openAt(t *testing.T, dataDir string) *app {
	t.Helper()
	c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}
}

func (a *app) sql(q string, args ...any) {
	a.t.Helper()
	if _, err := a.c.DB.Writer().Exec(q, args...); err != nil {
		a.t.Fatalf("%v\n%s", err, q)
	}
}

func (a *app) alertsRoom(projectID string) string {
	a.t.Helper()
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": projectID}, &rooms)
	for _, r := range rooms {
		if r.Name == "alerts" {
			return r.ID
		}
	}
	a.t.Fatal("no #alerts channel")
	return ""
}

// ---- support bundle ----

func TestSupportBundleIsScrubbedAllowListed(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	p := gitProject(t, a, "gocalc")
	a.sql(`INSERT INTO settings(key,value) VALUES('custom.note',?)`, "my token is "+plantedToken)
	a.sql(`INSERT INTO settings(key,value) VALUES('custom.password','hunter2-but-longer-than-eight')`)
	// an event whose payload carries a secret and a marker: only type, seq, time and ids may reach the bundle
	if _, err := a.c.Bus.Publish(context.Background(), events.Event{Type: "test.payload", ProjectID: p.ID, TaskID: "task_x", Data: json.RawMessage(`{"secret":"` + plantedToken + `","note":"PAYLOAD-MARKER-123"}`)}); err != nil {
		t.Fatal(err)
	}
	// a log file as an older version (or a crash) could have left it: raw, with a secret, the profile path and the machine name
	home, _ := os.UserHomeDir()
	host, _ := os.Hostname()
	os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755)
	os.WriteFile(filepath.Join(dataDir, "logs", "drydock.log.1"), []byte("started in "+filepath.Join(home, "AppData", "Local", "Drydock")+" on "+host+" token="+plantedToken+"\n"), 0o644)
	t.Setenv("DRYDOCK_TEST_ENV_NAME", "ENV-VALUE-MARKER-456")

	_ = filepath.Join(dataDir, "support")
	var pv struct {
		Files []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"files"`
		Report struct {
			TotalSecrets int `json:"totalSecretsRedacted"`
		} `json:"report"`
		Notice string `json:"notice"`
	}
	a.call("supportbundle.preview", map[string]any{}, &pv)
	if len(pv.Files) < 8 || pv.Report.TotalSecrets < 2 || !strings.Contains(pv.Notice, "never uploads") {
		t.Fatalf("preview %+v", pv)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "support")); err == nil {
		t.Fatal("previewing wrote something to disk")
	}

	dest := filepath.Join(t.TempDir(), "bundle.zip")
	var made struct {
		Path string `json:"path"`
	}
	a.call("supportbundle.create", map[string]any{"dest": dest}, &made)
	files := readZipFiles(t, dest)
	for name, body := range files {
		for _, bad := range []string{plantedToken, "hunter2-but-longer", "PAYLOAD-MARKER-123", "ENV-VALUE-MARKER-456"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
		if len(home) > 3 && strings.Contains(body, home) {
			t.Errorf("%s contains the user profile path", name)
		}
		if len(host) >= 3 && strings.Contains(strings.ToLower(body), strings.ToLower(host)) {
			t.Errorf("%s contains the machine name", name)
		}
	}
	if !strings.Contains(files["environment.json"], "DRYDOCK_TEST_ENV_NAME") {
		t.Error("environment variable NAMES are expected in the bundle")
	}
	if !strings.Contains(files["events.json"], "test.payload") || !strings.Contains(files["events.json"], p.ID) {
		t.Errorf("events.json lacks type and ids: %s", files["events.json"])
	}
	if !strings.Contains(files["settings.json"], "custom.note") || !strings.Contains(files["settings.json"], "REDACTED") {
		t.Errorf("settings.json: %s", files["settings.json"])
	}
	for _, want := range []string{"summary.json", "connections.json", "watchdog.json", "scrub-report.json", "manifest.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	var sum struct {
		App struct {
			Schema int64 `json:"databaseSchemaVersion"`
		} `json:"app"`
		Disk struct {
			Free uint64 `json:"freeBytes"`
		} `json:"disk"`
	}
	json.Unmarshal([]byte(files["summary.json"]), &sum)
	if sum.App.Schema < 80 || sum.Disk.Free == 0 {
		t.Errorf("summary %s", files["summary.json"])
	}
	// the app's own log, if it has one, is scrubbed
	for name, body := range files {
		if strings.HasPrefix(name, "logs/") && strings.Contains(body, plantedToken) {
			t.Errorf("%s leaks", name)
		}
	}
	if _, ok := files["logs/drydock.log.1"]; !ok {
		t.Error("the rotated log was not included")
	}
}

// A bundle is never written over a file nobody said could go (audit 2026-10-16, L39): a file already at the path is left alone and
// the answer says so; the bundle replaces it only when the request says it may; a folder, and Drydock's own data folder, are
// refused. The answer names what was replaced.
func TestASupportBundleIsNeverWrittenOverAFileWithoutLeave(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	const mine = "the person's own notes, not a zip"
	if err := os.WriteFile(dest, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Path     string `json:"path"`
		Exists   bool   `json:"exists"`
		Replaced bool   `json:"replaced"`
		Size     int64  `json:"size"`
		Files    []any  `json:"files"`
	}
	a.call("supportbundle.create", map[string]any{"dest": dest}, &got)
	if !got.Exists || got.Path != dest || len(got.Files) != 0 {
		t.Fatalf("a file at the path: %+v", got)
	}
	if b, _ := os.ReadFile(dest); string(b) != mine {
		t.Fatalf("the file was written over: %q", b)
	}
	got = struct {
		Path     string `json:"path"`
		Exists   bool   `json:"exists"`
		Replaced bool   `json:"replaced"`
		Size     int64  `json:"size"`
		Files    []any  `json:"files"`
	}{}
	a.call("supportbundle.create", map[string]any{"dest": dest, "overwrite": true}, &got)
	if got.Exists || !got.Replaced || len(got.Files) == 0 {
		t.Fatalf("replaced with leave: %+v", got)
	}
	if files := readZipFiles(t, dest); len(files) == 0 {
		t.Fatal("the replaced file is not a bundle")
	}
	// a folder is not a file to replace, even with leave
	if err := a.callErr("supportbundle.create", map[string]any{"dest": t.TempDir(), "overwrite": true}); err == nil || !strings.Contains(err.Error(), "is a folder") {
		t.Errorf("a folder as the destination: %v", err)
	}
	// the app's own data folder holds the database and the logs: not a place for a bundle, with or without leave
	db := filepath.Join(dataDir, "drydock.db")
	for _, over := range []bool{false, true} {
		if err := a.callErr("supportbundle.create", map[string]any{"dest": db, "overwrite": over}); err == nil || !strings.Contains(err.Error(), "data folder") {
			t.Errorf("the database as the destination (overwrite %v): %v", over, err)
		}
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("the database is gone: %v", err)
	}
	// its support folder is the place it uses itself
	var in struct {
		Path string `json:"path"`
	}
	a.call("supportbundle.create", map[string]any{"dest": filepath.Join(dataDir, "support", "mine.zip")}, &in)
	if in.Path == "" {
		t.Fatal("the support folder was refused")
	}
}

func TestSupportBundleRunInclusionIsOptInAndScanned(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	p := gitProject(t, a, "gocalc")
	runID := "run_aaaaaaaaaaaa"
	a.sql(`INSERT INTO workflow_runs(id,workflow,status,project_id,topology_hash,created_at,updated_at) VALUES(?,'w','COMPLETED',?,'h',1,1)`, runID, p.ID)
	os.MkdirAll(filepath.Join(dataDir, "runs", runID), 0o755)
	os.WriteFile(filepath.Join(dataDir, "runs", runID, "stream.jsonl"), []byte(`{"text":"key `+plantedToken+` and RAW-OUTPUT-MARKER"}`), 0o644)
	// A type the bus does not scan: the bundle's own scan is what is under test here. (An agent.* event is scanned before it is
	// stored, since audit 2026-10-16 H7, so the bundle would find nothing in it; events stored before that change still hold what
	// they held, and the bundle scans them the same way.)
	if _, err := a.c.Bus.Publish(context.Background(), events.Event{Type: "test.chunk", RunID: runID, Data: json.RawMessage(`{"text":"` + plantedToken + ` RUN-EVENT-MARKER"}`)}); err != nil {
		t.Fatal(err)
	}
	var pv struct {
		Files  []struct{ Name string } `json:"files"`
		Report struct {
			RunIncluded string `json:"runIncluded"`
			Total       int    `json:"totalSecretsRedacted"`
		} `json:"report"`
		Warning string `json:"warning"`
	}
	a.waitFor("run event", func() bool {
		a.call("supportbundle.preview", map[string]any{"includeRun": runID}, &pv)
		for _, f := range pv.Files {
			if strings.HasPrefix(f.Name, "run/"+runID+"/events-with-payloads") {
				return true
			}
		}
		return false
	})
	if pv.Report.RunIncluded != runID || pv.Report.Total < 2 || pv.Warning == "" {
		t.Fatalf("preview %+v", pv)
	}
	dest := filepath.Join(t.TempDir(), "b.zip")
	if err := a.callErr("supportbundle.create", map[string]any{"dest": dest, "includeRun": runID}); err == nil || !strings.Contains(err.Error(), "acknowledgeRun") {
		t.Fatalf("a run was included without the human seeing the scan result first: %v", err)
	}
	if err := a.callErr("supportbundle.create", map[string]any{"dest": dest, "includeRun": "run_nonexistent", "acknowledgeRun": true}); err == nil {
		t.Fatal("an unknown run was accepted")
	}
	a.call("supportbundle.create", map[string]any{"dest": dest, "includeRun": runID, "acknowledgeRun": true}, nil)
	files := readZipFiles(t, dest)
	if !strings.Contains(files["run/"+runID+"/files/stream.jsonl"], "RAW-OUTPUT-MARKER") || !strings.Contains(files["run/"+runID+"/events-with-payloads.json"], "RUN-EVENT-MARKER") {
		t.Fatalf("run content missing: %v", files)
	}
	for name, body := range files {
		if strings.Contains(body, plantedToken) {
			t.Errorf("%s leaks the secret", name)
		}
	}
	if !strings.Contains(files["manifest.json"], runID) {
		t.Error("the manifest does not record that a run was included")
	}
}

// ---- project export and import through the methods the UI calls ----

func TestProjectExportImportThroughTheMethods(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "gocalc")
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "R", Adapter: "claude", Role: "reviewer"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Add Subtract", Description: "plain"}, &task)
	a.sql(`UPDATE tasks SET description=? WHERE id=?`, "deploy with "+plantedToken, task.ID)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	other := gitProject(t, a, "other")
	var otherTask tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: other.ID, Title: "OTHER-PROJECT-MARKER"}, &otherTask)
	a.sql(`INSERT INTO settings(key,value) VALUES('license.token','LICENSE-TOKEN-MARKER')`)
	a.sql(`INSERT INTO memory(id,project_id,kind,title,body,status,created_at,updated_at) VALUES('mem_aaaaaaaaaaaa',?,'convention','Tabs','use tabs','accepted',1,1)`, p.ID)

	dest := filepath.Join(t.TempDir(), "gocalc.drydock-project.zip")
	var pv struct {
		Manifest struct {
			Tables map[string]int `json:"tables"`
			Scan   struct {
				DBRedactions int `json:"dbRedactions"`
			} `json:"scan"`
		} `json:"manifest"`
	}
	a.call("project.exportPreview", map[string]any{"projectId": p.ID}, &pv)
	if pv.Manifest.Tables["tasks"] != 1 || pv.Manifest.Tables["agents"] != 2 || pv.Manifest.Scan.DBRedactions < 1 {
		t.Fatalf("preview %+v", pv)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("preview wrote the archive")
	}
	if err := a.callErr("project.export", map[string]any{"projectId": p.ID, "dest": dest, "includeRuns": true}); err == nil || !strings.Contains(err.Error(), "acknowledgeRuns") {
		t.Fatalf("raw runs were exported without the warning step: %v", err)
	}
	a.call("project.export", map[string]any{"projectId": p.ID, "dest": dest}, nil)
	// a path that already holds a file is never written over, nor is a folder (review 2026-10-27)
	before, _ := os.ReadFile(dest)
	if err := a.callErr("project.export", map[string]any{"projectId": p.ID, "dest": dest}); err == nil || !strings.Contains(err.Error(), "already at") {
		t.Fatalf("an export over an existing file: %v", err)
	}
	if after, _ := os.ReadFile(dest); !bytes.Equal(before, after) {
		t.Fatal("the existing file was changed")
	}
	if err := a.callErr("project.export", map[string]any{"projectId": p.ID, "dest": filepath.Dir(dest)}); err == nil || !strings.Contains(err.Error(), "is a folder") {
		t.Fatalf("an export onto a folder: %v", err)
	}
	for name, body := range readZipFiles(t, dest) {
		if name == "project.db" {
			continue
		}
		for _, bad := range []string{plantedToken, "OTHER-PROJECT-MARKER", "LICENSE-TOKEN-MARKER"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
	}

	// import: the repository path is required, and the archive is checked first
	var ipv struct {
		Counts  map[string]int `json:"counts"`
		Pending struct {
			MemoryPending int `json:"memoryPending"`
		} `json:"pending"`
	}
	a.call("project.importPreview", map[string]any{"path": dest}, &ipv)
	if ipv.Counts["tasks"] != 1 || ipv.Pending.MemoryPending != 1 {
		t.Fatalf("import preview %+v", ipv)
	}
	// without a repository the import makes a project whose repository is attached later
	var norepo struct {
		ProjectID string `json:"projectId"`
	}
	a.call("project.import", map[string]any{"path": dest, "name": "gocalc (no repo)"}, &norepo)
	var np core.Project
	a.call("project.get", map[string]string{"id": norepo.ProjectID}, &np)
	// the archive has no key (it is unique to an installation): the imported project gets one from its name (P97)
	if np.HasRepo || np.RepoPath != "" || np.Name != "gocalc (no repo)" || np.Key != "GNR" {
		t.Fatalf("import without a repository: %+v", np)
	}
	if err := a.callErr("project.import", map[string]any{"path": dest, "repoPath": t.TempDir()}); err == nil || !strings.Contains(err.Error(), "not a usable Git repository") {
		t.Fatalf("import into a non-repository: %v", err)
	}
	repo2 := newRepo(t)
	var res struct {
		ProjectID string `json:"projectId"`
	}
	a.call("project.import", map[string]any{"path": dest, "repoPath": repo2, "name": "gocalc (copy)"}, &res)
	if res.ProjectID == "" || res.ProjectID == p.ID {
		t.Fatalf("import result %+v", res)
	}
	var imported core.Project
	a.call("project.get", map[string]string{"id": res.ProjectID}, &imported)
	if imported.Name != "gocalc (copy)" || imported.RepoPath == "" || imported.RepoPath == p.RepoPath || imported.Key != "GC" {
		t.Fatalf("imported project %+v", imported)
	}
	var ts []tasks.Task
	a.call("task.list", map[string]any{"projectId": res.ProjectID}, &ts)
	if len(ts) != 1 || ts[0].ID == task.ID || ts[0].Title != "Add Subtract" || strings.Contains(ts[0].Description, plantedToken) {
		t.Fatalf("imported tasks %+v", ts)
	}
	var orig []tasks.Task
	a.call("task.list", map[string]any{"projectId": p.ID}, &orig)
	if len(orig) != 1 || orig[0].ID != task.ID {
		t.Fatalf("the original project was touched: %+v", orig)
	}
	// the new project has working rooms and a notice about what waits for acceptance
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": res.ProjectID}, &rooms)
	if len(rooms) == 0 {
		t.Fatal("no rooms were imported")
	}
	noticed := false
	for _, rm := range rooms {
		var msgs []room.Message
		a.call("room.messages", map[string]any{"roomId": rm.ID, "limit": 50}, &msgs)
		for _, m := range msgs {
			if strings.Contains(m.Body, "Nothing from the file is trusted yet") && strings.Contains(m.Body, "memory item") {
				noticed = true
			}
		}
	}
	if !noticed {
		t.Error("no import notice in the new project's room")
	}
	var agentsOut []core.Agent
	a.call("agent.list", map[string]string{"projectId": res.ProjectID}, &agentsOut)
	if len(agentsOut) != 2 || agentsOut[0].Status != "STOPPED" {
		t.Fatalf("agents %+v", agentsOut)
	}
	// a different installation can import it too, and an export of an expired install still works (covered in the license test)
	b := open(t, "")
	var res2 struct {
		ProjectID string `json:"projectId"`
	}
	b.call("project.import", map[string]any{"path": dest, "repoPath": newRepo(t)}, &res2)
	var ps []core.Project
	b.call("project.list", nil, &ps)
	if len(ps) != 1 || ps[0].ID != res2.ProjectID {
		t.Fatalf("second install projects %+v", ps)
	}
	var cnt int
	if err := b.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM settings WHERE key LIKE 'license%'`).Scan(&cnt); err != nil && err != sql.ErrNoRows || cnt != 0 {
		t.Fatalf("license settings leaked into the other install: %d", cnt)
	}
}
