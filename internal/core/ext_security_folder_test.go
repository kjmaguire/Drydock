package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/scan"
)

// Decision P61: a person scans any folder on this computer without a project (a repository just downloaded), with the same
// engines, events, stored report and history as a project's scan, under the folder's own history and the workspace's rules
// folder. Nothing of it is attached to a project.
func TestAFolderScanRunsTheEnginesWithoutAProjectAndKeepsItsHistory(t *testing.T) {
	a, _ := securityCore(t, "--sleep-ms=300")
	folder := realDir(t, t.TempDir())
	os.WriteFile(filepath.Join(folder, "notes.txt"), []byte("key = "+plantedKey+"\n"), 0o644)
	os.WriteFile(filepath.Join(folder, "go.mod"), []byte("module example.com/downloaded\n"), 0o644)

	// without a project the tools say what folder scans use: Drydock's own tools and the built-in rules
	var tools scan.Tools
	a.call("security.tools", map[string]any{}, &tools)
	if !tools.OSV.Found || !tools.Opengrep.Found || tools.RulesOK || tools.RulesDir != "" || !tools.Ready || !tools.BuiltinRules {
		t.Fatalf("%+v", tools)
	}
	var p struct{ ID string }
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)

	var cur *scan.Report
	a.call("security.scan.get", map[string]any{"folder": folder}, &cur)
	if cur != nil {
		t.Fatalf("nothing scanned yet: %+v", cur)
	}

	sub, stop := a.c.Bus.Subscribe(events.Filter{Types: []string{"security.*"}})
	defer stop()
	var started scan.Report
	// a trailing separator and Explorer's quotes are the same folder
	a.call("security.scan.start", map[string]any{"folder": `"` + folder + string(filepath.Separator) + `"`}, &started)
	if started.Status != scan.StatusRunning || started.Dir != folder || started.ProjectID != "" || started.By != "human:kyle" {
		t.Fatalf("%+v", started)
	}
	if err := a.callErr("security.scan.start", map[string]any{"folder": folder}); err == nil || !strings.Contains(err.Error(), "this folder is already running") {
		t.Fatal(err)
	}
	// the folder's running scan, while a project's own scan is a different one
	var running []scan.Summary
	a.call("security.scan.folders", map[string]any{"limit": 5}, &running)
	if len(running) != 1 || running[0].ID != started.ID || running[0].Status != scan.StatusRunning || running[0].Folder != folder {
		t.Fatalf("%+v", running)
	}
	a.waitFor("the folder scan to finish", func() bool {
		a.call("security.scan.get", map[string]any{"folder": folder}, &cur)
		return cur != nil && cur.Status == scan.StatusDone
	})
	if cur.ID != started.ID || cur.Counts.Total != 4 || len(cur.Findings) != 4 || !strings.Contains(cur.Summary, "1 secret") {
		t.Fatalf("%+v", cur)
	}
	for _, e := range cur.Engines {
		if e.Status != scan.Done {
			t.Fatalf("every engine runs on a folder: %+v", e)
		}
	}
	b, _ := json.Marshal(cur)
	if strings.Contains(string(b), plantedKey) {
		t.Fatal("the report carries the secret")
	}

	// the events carry no project and name the folder
	seen := map[string]int{}
	deadline := time.After(10 * time.Second)
	for seen["security.scan.finished"] == 0 {
		select {
		case e := <-sub:
			seen[e.Type]++
			var d struct {
				Folder string `json:"folder"`
			}
			_ = json.Unmarshal(e.Data, &d)
			if e.ProjectID != "" || d.Folder != folder {
				t.Fatalf("%s %s %s", e.Type, e.ProjectID, e.Data)
			}
		case <-deadline:
			t.Fatalf("no finished event: %v", seen)
		}
	}
	if seen["security.scan.started"] != 1 || seen["security.scan.engine"] != 6 {
		t.Fatalf("%v", seen)
	}

	// the history: by folder, across folders, and the report by id; none of it is the project's
	var list []scan.Summary
	a.call("security.scan.list", map[string]any{"folder": folder, "limit": 10}, &list)
	if len(list) != 1 || list[0].ID != cur.ID || list[0].Folder != folder || list[0].ProjectID != "" || list[0].Counts.Total != 4 {
		t.Fatalf("%+v", list)
	}
	a.call("security.scan.folders", map[string]any{}, &list)
	if len(list) != 1 || list[0].ID != cur.ID || list[0].Folder != folder || list[0].Status != scan.StatusDone {
		t.Fatalf("%+v", list)
	}
	var byID scan.Report
	a.call("security.scan.report", map[string]any{"id": cur.ID}, &byID)
	if byID.ID != cur.ID || byID.Dir != folder || len(byID.Findings) != 4 {
		t.Fatalf("%+v", byID)
	}
	a.call("security.scan.list", map[string]any{"projectId": p.ID}, &list)
	if len(list) != 0 {
		t.Fatalf("a folder scan joined a project's history: %+v", list)
	}

	// another folder has its own history; the recent folder scans list both, newest first
	other := realDir(t, t.TempDir())
	os.WriteFile(filepath.Join(other, "readme.txt"), []byte("hello\n"), 0o644)
	a.call("security.scan.start", map[string]any{"folder": other}, &started)
	a.waitFor("the second folder's scan", func() bool {
		a.call("security.scan.get", map[string]any{"folder": other}, &cur)
		return cur != nil && cur.Status == scan.StatusDone
	})
	if cur.Dir != other || cur.Counts.Total != 3 { // the stand-ins' dependency and code findings, no secret
		t.Fatalf("%+v", cur)
	}
	a.call("security.scan.list", map[string]any{"folder": folder}, &list)
	if len(list) != 1 {
		t.Fatalf("%+v", list)
	}
	a.call("security.scan.folders", map[string]any{"limit": 10}, &list)
	if len(list) != 2 || list[0].Folder != other || list[1].Folder != folder {
		t.Fatalf("%+v", list)
	}
}

// A folder scan reads only folders a person means: a full path to a folder that is there, not a file, a whole drive or
// Drydock's own data folder (its database and the task worktrees), inside it or around it.
func TestAFolderScanRefusesFoldersItMustNotRead(t *testing.T) {
	a, _ := securityCore(t)
	var info struct {
		DataDir string `json:"dataDir"`
	}
	a.call("app.info", map[string]any{}, &info)
	if info.DataDir == "" {
		t.Fatal("no data folder")
	}
	file := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(file, []byte("x"), 0o644)
	inside := filepath.Join(info.DataDir, "projects")
	os.MkdirAll(inside, 0o755)
	root := filepath.VolumeName(info.DataDir) + string(filepath.Separator)

	for _, c := range []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{}, "say which project or folder"},
		{map[string]any{"folder": "  "}, "say which project or folder"},
		{map[string]any{"folder": filepath.Join("code", "shop")}, "full path"},
		{map[string]any{"folder": filepath.Join(t.TempDir(), "missing")}, "no folder at"},
		{map[string]any{"folder": file}, "is a file"},
		{map[string]any{"folder": root}, "whole drive"},
		{map[string]any{"folder": info.DataDir}, "Drydock's own data folder or inside it"},
		{map[string]any{"folder": inside}, "Drydock's own data folder or inside it"},
		{map[string]any{"folder": filepath.Dir(info.DataDir)}, "holds Drydock's own data folder"},
		{map[string]any{"folder": t.TempDir(), "projectId": "prj_x"}, "not both"},
	} {
		if err := a.callErr("security.scan.start", c.params); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: %v", c.params, err)
		}
	}
	if err := a.callErr("security.scan.get", map[string]any{"folder": "relative"}); err == nil || !strings.Contains(err.Error(), "full path") {
		t.Fatal(err)
	}
	if err := a.callErr("security.scan.cancel", map[string]any{"folder": t.TempDir()}); err == nil || !strings.Contains(err.Error(), "no scan of this folder") {
		t.Fatal(err)
	}
	var list []scan.Summary
	a.call("security.scan.folders", map[string]any{}, &list)
	if len(list) != 0 {
		t.Fatalf("a refused folder left a scan: %+v", list)
	}
}

// A folder scan can be cancelled like a project's; the cancelled report is kept in the folder's history.
func TestAFolderScanCanBeCancelled(t *testing.T) {
	a, _ := securityCore(t, "--sleep-ms=20000")
	folder := realDir(t, t.TempDir())
	var rep scan.Report
	a.call("security.scan.start", map[string]any{"folder": folder}, &rep)
	time.Sleep(200 * time.Millisecond)
	// a linked path to the same folder (on Windows also a short 8.3 path) finds the running scan
	link := filepath.Join(t.TempDir(), "link")
	byLink := folder
	if err := os.Symlink(folder, link); err == nil {
		byLink = link
	}
	a.call("security.scan.cancel", map[string]any{"folder": byLink}, &rep)
	var cur *scan.Report
	a.waitFor("the cancelled folder scan to end", func() bool {
		a.call("security.scan.get", map[string]any{"folder": folder}, &cur)
		return cur != nil && cur.Status != scan.StatusRunning
	})
	if cur.Status != scan.StatusCancelled {
		t.Fatalf("%+v", cur)
	}
	var list []scan.Summary
	a.call("security.scan.list", map[string]any{"folder": folder}, &list)
	if len(list) != 1 || list[0].Status != scan.StatusCancelled {
		t.Fatalf("%+v", list)
	}
}

// realDir is the folder a scan names: t.TempDir() can be a short (8.3) or linked path, and Drydock names the folder it resolves to.
func realDir(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(real)
}
