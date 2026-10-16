package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/scan"
)

// The security scan (decision P44, docs/security-scan.md): one button runs the three engines of internal/scan over the
// project's working tree, or (decision P61) over any folder on this computer, without a project: a repository just
// downloaded can be scanned before it becomes one. A scan in progress lives here in memory (its progress counters change many
// times a second and are read, not published); it is stored when it ends. Events: security.scan.started, security.scan.engine
// (an engine changed state), security.scan.finished, security.settings; a folder scan's carry no project and name the folder.
// Only a person starts a scan or chooses the rules folder.

func init() {
	RegisterExtension(Extension{Name: "security", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		s := &securityExt{c: c, running: map[string]*scan.Run{}, unstored: map[string]scan.Report{}, jobs: map[string]*toolJob{},
			inst: scan.NewInstaller(filepath.Join(c.cfg.DataDir, "tools"))}
		if c.cfg.ScanInstall != nil {
			c.cfg.ScanInstall(s.inst)
		}
		// a project is not deleted under a running scan; a scan report the database refused goes with the project
		c.onProjectBusy(func(projectID string) string {
			s.mu.Lock()
			defer s.mu.Unlock()
			if _, ok := s.running[projectID]; ok {
				return "a security scan is running"
			}
			return ""
		})
		// a scan that started just before the delete (the busy check runs before its transaction) is stopped; its report
		// finds no project and is not stored (store)
		c.onProjectDeleted(func(_ context.Context, projectID string, _ []string) {
			s.mu.Lock()
			delete(s.unstored, projectID)
			r := s.running[projectID]
			s.mu.Unlock()
			if r != nil {
				r.Cancel()
			}
		})
		return map[string]Method{
			// without a project: the tools and the rules folder of folder scans
			"security.tools": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				return s.tools(ctx, p.ProjectID), nil
			}),
			// {projectId, rulesDir} for a project, {workspace: true, rulesDir} for folder scans
			"security.settings.set": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				Workspace bool   `json:"workspace"`
				RulesDir  string `json:"rulesDir"`
			}) (any, error) {
				if p.Workspace {
					if p.ProjectID != "" {
						return nil, errors.New("give a project or the workspace, not both")
					}
					return s.setWorkspaceRulesDir(ctx, p.RulesDir, c.Human())
				}
				return s.setRulesDir(ctx, p.ProjectID, p.RulesDir, c.Human())
			}),
			"security.tool.set": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				Tool      string `json:"tool"`
				Path      string `json:"path"`
			}) (any, error) {
				return s.setToolPath(ctx, p.ProjectID, p.Tool, p.Path, c.Human())
			}),
			"security.tool.choose": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				Tool      string `json:"tool"`
			}) (any, error) {
				return s.chooseTool(ctx, p.ProjectID, p.Tool, c.Human())
			}),
			// Drydock installs the tool's newest release itself (decision P66); the same call updates an installed one
			"security.tool.install": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				Tool      string `json:"tool"`
			}) (any, error) {
				return s.installTool(ctx, p.ProjectID, p.Tool, c.Human())
			}),
			"security.tool.install.cancel": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				Tool      string `json:"tool"`
			}) (any, error) {
				return s.cancelInstall(ctx, p.ProjectID, p.Tool)
			}),
			"security.tool.remove": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				Tool      string `json:"tool"`
			}) (any, error) {
				return s.removeTool(ctx, p.ProjectID, p.Tool, c.Human())
			}),
			// the scan methods take {projectId} (the project's working tree) or {folder} (a full path, decision P61)
			"security.scan.start": bind(func(ctx context.Context, p scanTarget) (any, error) {
				return s.start(ctx, p, c.Human())
			}),
			"security.scan.cancel": bind(func(ctx context.Context, p scanTarget) (any, error) {
				t, err := s.target(ctx, p, false)
				if err != nil {
					return nil, err
				}
				return s.cancel(t)
			}),
			"security.scan.get": bind(func(ctx context.Context, p scanTarget) (any, error) {
				t, err := s.target(ctx, p, false)
				if err != nil {
					return nil, err
				}
				return s.current(ctx, t)
			}),
			"security.scan.list": bind(func(ctx context.Context, p struct {
				scanTarget
				Limit int `json:"limit"`
			}) (any, error) {
				t, err := s.target(ctx, p.scanTarget, false)
				if err != nil {
					return nil, err
				}
				return s.list(ctx, t, p.Limit)
			}),
			// the recent folder scans of every folder, running ones first (the workspace Security page's history)
			"security.scan.folders": bind(func(ctx context.Context, p struct {
				Limit int `json:"limit"`
			}) (any, error) {
				return s.folders(ctx, p.Limit)
			}),
			"security.scan.report": bind(func(ctx context.Context, p struct {
				ID string `json:"id"`
			}) (any, error) {
				return s.report(ctx, p.ID)
			}),
		}, s.close, nil
	}})
}

type securityExt struct {
	c  *Core
	mu sync.Mutex
	// running holds each scan in progress by its key: the project id, or folderKey of a folder scan
	running map[string]*scan.Run
	// unstored holds a key's last finished report when the database refused it (StoreError set), until the next scan
	unstored map[string]scan.Report
	// memo keeps the last tools answer for a moment (toolsMemoTTL): a change to a tool's program is answered to its caller
	// and announced to every open Security page, and each of those reads must not probe the programs again. It serves only
	// while the programs' files are as they were (stamp): one deleted or replaced meanwhile is probed again at once.
	memoMu sync.Mutex
	memo   struct {
		key, stamp string
		at         time.Time
		v          scan.Tools
	}
	// inst installs the external tools (decision P66); jobs holds each tool's install in progress or how its last one
	// ended (under mu)
	inst *scan.Installer
	jobs map[string]*toolJob
}

// toolsMemoTTL is how long a tools answer serves the reads that follow one change.
const toolsMemoTTL = 3 * time.Second

// toolsStamp names the files a tools answer rests on (each program's path, size and modification time, or that it is
// missing), so the memo can tell whether the answer still holds without running anything.
func toolsStamp(t scan.Tools) string {
	var b strings.Builder
	for _, info := range []scan.ToolInfo{t.OSV, t.Opengrep} {
		path := info.Chosen
		if path == "" {
			path = info.Path
		}
		if st, err := os.Stat(path); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d|", path, st.Size(), st.ModTime().UnixNano())
		} else {
			fmt.Fprintf(&b, "%s:missing|", path)
		}
	}
	return b.String()
}

// scanTarget is what a scan method is asked about: a project, or a folder by its full path (decision P61).
type scanTarget struct {
	ProjectID string `json:"projectId"`
	Folder    string `json:"folder"`
}

// target is a resolved scanTarget. Key names its scans in memory and in security_scans.project_id: the project id, or
// "folder:" + the folder (lower case on Windows, where paths are case-insensitive), which no project id can be.
type target struct {
	Key       string
	ProjectID string
	Folder    string
	Dir       string // the folder the engines read (set for a scan that starts)
}

const folderPrefix = "folder:"

func folderKey(folder string) string {
	if runtime.GOOS == "windows" {
		folder = strings.ToLower(folder)
	}
	return folderPrefix + folder
}

// target resolves a project or a folder. forStart checks what a scan needs: the project's repository folder is there, or the
// folder is one Drydock will read (checkFolder). Without forStart a folder only has to be a full path: its history stays
// readable after the folder is gone.
func (s *securityExt) target(ctx context.Context, p scanTarget, forStart bool) (target, error) {
	p.Folder = cleanFolderInput(p.Folder)
	switch {
	case p.ProjectID != "" && p.Folder != "":
		return target{}, errors.New("give a project or a folder to scan, not both")
	case p.ProjectID != "":
		t := target{Key: p.ProjectID, ProjectID: p.ProjectID}
		if !forStart {
			return t, nil
		}
		pr, err := s.c.GetProject(ctx, p.ProjectID)
		if err != nil {
			return target{}, err
		}
		if pr.RepoPath == "" {
			return target{}, errors.New("this project has no repository yet: attach one in Project settings, then scan it")
		}
		if st, err := os.Stat(pr.RepoPath); err != nil || !st.IsDir() {
			return target{}, fmt.Errorf("the repository folder %s is not there", pr.RepoPath)
		}
		t.Dir = pr.RepoPath
		return t, nil
	case p.Folder != "":
		if !filepath.IsAbs(p.Folder) {
			return target{}, fmt.Errorf("give the full path of the folder to scan, for example C:\\code\\shop (%q is not one)", p.Folder)
		}
		folder := filepath.Clean(p.Folder)
		if !forStart {
			// the same folder as the scan was started with: a short (8.3) or linked path names the folder it resolves to
			if real, err := filepath.EvalSymlinks(folder); err == nil {
				folder = filepath.Clean(real)
			}
			return target{Key: folderKey(folder), Folder: folder}, nil
		}
		dir, err := s.checkFolder(folder)
		if err != nil {
			return target{}, err
		}
		return target{Key: folderKey(dir), Folder: dir, Dir: dir}, nil
	}
	return target{}, errors.New("say which project or folder to scan")
}

// cleanFolderInput trims spaces and the quotes Explorer's "Copy as path" adds.
func cleanFolderInput(s string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`))
}

// checkFolder says whether a folder may be scanned and returns the folder the engines read (symbolic links resolved, so the
// tree walk enters it). Refused: what is not there, a file, a whole drive, Drydock's own data folder, a folder inside it
// (its worktrees and database) and a folder that holds it.
func (s *securityExt) checkFolder(folder string) (string, error) {
	st, err := os.Stat(folder)
	if err != nil {
		return "", fmt.Errorf("there is no folder at %s", folder)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is a file: give the folder that holds the repository", folder)
	}
	dir := folder
	if real, err := filepath.EvalSymlinks(folder); err == nil {
		dir = filepath.Clean(real)
	}
	if filepath.Dir(dir) == dir || filepath.Dir(folder) == folder {
		return "", fmt.Errorf("%s is a whole drive: give the repository's own folder", folder)
	}
	if data := s.c.cfg.DataDir; data != "" {
		if real, err := filepath.EvalSymlinks(data); err == nil {
			data = real
		}
		data = filepath.Clean(data)
		if withinFolder(dir, data) {
			return "", fmt.Errorf("%s is Drydock's own data folder or inside it: choose the project from the list instead, or give the repository's own folder", folder)
		}
		if withinFolder(data, dir) {
			return "", fmt.Errorf("%s holds Drydock's own data folder (%s): give the repository's own folder", folder, data)
		}
	}
	return dir, nil
}

// withinFolder reports whether path is dir or inside it, case-insensitive on Windows (within, project_delete.go, is not).
func withinFolder(path, dir string) bool {
	if runtime.GOOS == "windows" {
		path, dir = strings.ToLower(path), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// settingRules + project id holds the project's Opengrep rules folder; settingWorkspaceRules the folder scans' (P61).
const (
	settingRules          = "security:rules:"
	settingWorkspaceRules = "security:workspace-rules"
)

// rulesDir is the project's rules folder, or with no project the folder scans'.
func (s *securityExt) rulesDir(ctx context.Context, projectID string) string {
	key := settingRules + projectID
	if projectID == "" {
		key = settingWorkspaceRules
	}
	var v string
	if err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v); err == nil {
		return v
	}
	return s.c.cfg.ScanRulesDir
}

func (s *securityExt) tools(ctx context.Context, projectID string) scan.Tools {
	paths, rules := s.paths(ctx), s.rulesDir(ctx, projectID)
	key := fmt.Sprintf("%s|%v|%v|%s|%v|%v|%s", paths.OSV.Path, paths.OSV.Chosen, paths.OSV.Installed, paths.Opengrep.Path, paths.Opengrep.Chosen, paths.Opengrep.Installed, rules)
	s.memoMu.Lock()
	if s.memo.key == key && time.Since(s.memo.at) < toolsMemoTTL && toolsStamp(s.memo.v) == s.memo.stamp {
		t := s.memo.v
		s.memoMu.Unlock()
		return s.withInstalls(t)
	}
	s.memoMu.Unlock()
	// the external tools read the whole project: they get what every project command gets (checks.CommandEnv), no more
	t := scan.Detect(ctx, s.c.Deps.PM, checks.CommandEnv(), paths, rules)
	t.CanChoose = s.c.cfg.PickFile != nil
	s.memoMu.Lock()
	s.memo.key, s.memo.stamp, s.memo.at, s.memo.v = key, toolsStamp(t), time.Now(), t
	s.memoMu.Unlock()
	return s.withInstalls(t)
}

// The programs a person chose for the external tools, workspace-wide (a program is on the computer, not in a project):
// settingToolPath + tool name.
const settingToolPath = "security:toolpath:"

// scanTools are the external tools a person may choose a program for.
var scanTools = map[string]string{"osv-scanner": "osv-scanner", "opengrep": "Opengrep"}

func (s *securityExt) chosenTool(ctx context.Context, tool string) string {
	var v string
	_ = s.c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingToolPath+tool).Scan(&v)
	return v
}

// paths are the configured tools with each program a person chose in place of the default, else the one Drydock installed
// (decision P66). A choice replaces the program only: the leading arguments stay what the configuration says (none in the
// product; the e2e harness's stand-ins need them).
func (s *securityExt) paths(ctx context.Context) scan.Paths {
	p := s.c.cfg.ScanTools
	pick := func(tool string, dflt scan.Tool) scan.Tool {
		if v := s.chosenTool(ctx, tool); v != "" {
			return scan.Tool{Path: v, Args: dflt.Args, Chosen: true}
		}
		if in, ok := s.inst.Installed(tool); ok {
			return s.inst.ToolFor(in)
		}
		return dflt
	}
	p.OSV = pick("osv-scanner", p.OSV)
	p.Opengrep = pick("opengrep", p.Opengrep)
	return p
}

// setToolPath stores the program a person chose for a tool; "" goes back to finding it on PATH. Only a person may: the
// program reads the whole project.
func (s *securityExt) setToolPath(ctx context.Context, projectID, tool, path, by string) (scan.Tools, error) {
	if !isPerson(by) {
		return scan.Tools{}, errors.New("only a person may choose a scan tool's program")
	}
	label, ok := scanTools[tool]
	if !ok {
		return scan.Tools{}, fmt.Errorf("%q is not a scan tool Drydock runs (osv-scanner or opengrep)", tool)
	}
	path = strings.TrimSpace(strings.Trim(strings.TrimSpace(path), `"`))
	if path != "" {
		if !filepath.IsAbs(path) {
			return scan.Tools{}, fmt.Errorf("give the full path of %s's program, for example C:\\tools\\%s.exe", label, tool)
		}
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			return scan.Tools{}, fmt.Errorf("there is no file at %s", path)
		}
		if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe") {
			return scan.Tools{}, fmt.Errorf("%s is not a program (.exe)", filepath.Base(path))
		}
	}
	if err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if path == "" {
			_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, settingToolPath+tool)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingToolPath+tool, path)
		return err
	}); err != nil {
		return scan.Tools{}, err
	}
	// the programs are probed once, here; the pages that re-read on the event get this answer from the memo
	t := s.tools(ctx, projectID)
	s.c.publish(ctx, "security.settings", projectID, map[string]any{"tool": tool, "path": path, "by": by})
	return t, nil
}

// chooseTool opens the window's file dialog for a tool's program and stores what the person chose; cancelling changes nothing.
func (s *securityExt) chooseTool(ctx context.Context, projectID, tool, by string) (scan.Tools, error) {
	label, ok := scanTools[tool]
	if !ok {
		return scan.Tools{}, fmt.Errorf("%q is not a scan tool Drydock runs (osv-scanner or opengrep)", tool)
	}
	if s.c.cfg.PickFile == nil {
		return scan.Tools{}, fmt.Errorf("this window cannot open a file dialog: type the full path of %s's program instead", label)
	}
	if !isPerson(by) {
		return scan.Tools{}, errors.New("only a person may choose a scan tool's program")
	}
	path, err := s.c.cfg.PickFile(ctx, FilePick{Title: "Choose " + label + "'s program (" + tool + ".exe)", FilterName: "Programs", FilterPattern: "*.exe"})
	if err != nil {
		return scan.Tools{}, fmt.Errorf("the file dialog: %w", err)
	}
	if strings.TrimSpace(path) == "" {
		return s.tools(ctx, projectID), nil
	}
	return s.setToolPath(ctx, projectID, tool, path, by)
}

// setRulesDir stores the project's rules folder. Only a person may: the folder decides which rules read the whole project.
func (s *securityExt) setRulesDir(ctx context.Context, projectID, dir, by string) (scan.Tools, error) {
	if !isPerson(by) {
		return scan.Tools{}, errors.New("only a person may choose the rules folder")
	}
	if _, err := s.c.GetProject(ctx, projectID); err != nil {
		return scan.Tools{}, err
	}
	return s.saveRulesDir(ctx, settingRules+projectID, projectID, dir, by)
}

// setWorkspaceRulesDir stores the rules folder of folder scans (decision P61); each project keeps its own.
func (s *securityExt) setWorkspaceRulesDir(ctx context.Context, dir, by string) (scan.Tools, error) {
	if !isPerson(by) {
		return scan.Tools{}, errors.New("only a person may choose the rules folder")
	}
	return s.saveRulesDir(ctx, settingWorkspaceRules, "", dir, by)
}

func (s *securityExt) saveRulesDir(ctx context.Context, key, projectID, dir, by string) (scan.Tools, error) {
	dir = cleanFolderInput(dir)
	if dir != "" && !filepath.IsAbs(dir) {
		return scan.Tools{}, errors.New("give the full path of the rules folder (Opengrep runs in the repository, so a relative path would point elsewhere)")
	}
	if err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, dir)
		return err
	}); err != nil {
		return scan.Tools{}, err
	}
	data := map[string]any{"rulesDir": dir, "by": by}
	if projectID == "" {
		data["workspace"] = true
	}
	s.c.publish(ctx, "security.settings", projectID, data)
	return s.tools(ctx, projectID), nil
}

// isPerson reports whether an identity is a person's ("human:<name>"): only a person starts a scan or chooses what it runs.
func isPerson(by string) bool { return strings.HasPrefix(by, "human:") && len(by) > len("human:") }

// publish sends a scan event: with the project id for a project's scan, with no project and the folder for a folder scan.
func (s *securityExt) publish(ctx context.Context, t target, typ string, data map[string]any) {
	if t.Folder != "" {
		data["folder"] = t.Folder
	}
	s.c.publish(ctx, typ, t.ProjectID, data)
}

// start begins a scan of a project's working tree or of a folder (decision P61); one per project or folder at a time. Only a
// person may start one.
func (s *securityExt) start(ctx context.Context, p scanTarget, by string) (scan.Report, error) {
	if !isPerson(by) {
		return scan.Report{}, errors.New("only a person may start a security scan")
	}
	t, err := s.target(ctx, p, true)
	if err != nil {
		return scan.Report{}, err
	}
	s.mu.Lock()
	if r, ok := s.running[t.Key]; ok {
		s.mu.Unlock()
		return r.Snapshot(), fmt.Errorf("a scan of this %s is already running", t.noun())
	}
	id := ids.New("scn")
	delete(s.unstored, t.Key)
	// the scan outlives the request; close() cancels what is still running when the core closes
	run := scan.Start(context.Background(), scan.Options{ID: id, ProjectID: t.ProjectID, By: by, Dir: t.Dir, Procs: s.c.Deps.PM, Redact: s.c.Scanner.Redact,
		Env: checks.CommandEnv, Paths: s.paths(ctx), RulesDir: s.rulesDir(ctx, t.ProjectID), NoGit: t.Folder != "", OnChange: func(rep scan.Report, e scan.EngineID) { s.changed(t, rep, e) }})
	s.running[t.Key] = run
	// published under the lock: changed() takes it before it can publish an engine or finished event, so started comes first
	s.publish(ctx, t, "security.scan.started", map[string]any{"id": id, "by": by})
	s.mu.Unlock()
	return run.Snapshot(), nil
}

func (t target) noun() string {
	if t.Folder != "" {
		return "folder"
	}
	return "project"
}

// changed is called by the scan after one engine changed state (one event for that engine) and when it ends: the end is
// stored and published. A report the database refuses is kept in memory with StoreError set, so the page still shows the
// scan the person watched and says it was not kept.
func (s *securityExt) changed(t target, rep scan.Report, engine scan.EngineID) {
	ctx := context.Background()
	if rep.Status == scan.StatusRunning {
		if engine != "" {
			s.mu.Lock() // after start() has published security.scan.started
			st := rep.Engines
			s.mu.Unlock()
			for _, e := range st {
				if e.ID == engine {
					s.publish(ctx, t, "security.scan.engine", map[string]any{"id": rep.ID, "engine": e.ID, "status": e.Status})
				}
			}
		}
		return
	}
	stored := true
	if err := s.store(ctx, t, rep); err != nil {
		s.c.logf("security scan %s: store: %v", rep.ID, err)
		rep.StoreError = "this report could not be kept in the database (" + s.c.Scanner.Redact(err.Error()) + "); it is shown until the next scan of this " + t.noun()
		stored = false
	}
	s.mu.Lock()
	delete(s.running, t.Key)
	if !stored {
		s.unstored[t.Key] = rep
	}
	s.mu.Unlock()
	s.publish(ctx, t, "security.scan.finished", map[string]any{"id": rep.ID, "status": rep.Status, "counts": rep.Counts, "summary": rep.Summary, "stored": stored})
}

// store writes the finished report, redacted once more as a whole (nothing that looks like a secret survives, even in a
// file name). A report of a project that was deleted meanwhile is dropped: it would outlive its project. A folder scan's
// report is kept under the folder's key and belongs to no project.
func (s *securityExt) store(ctx context.Context, t target, rep scan.Report) error {
	b, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	doc := s.c.Scanner.Redact(string(b))
	counts, _ := json.Marshal(rep.Counts)
	return s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if t.Folder != "" {
			_, err := tx.ExecContext(ctx, `INSERT INTO security_scans(id,project_id,status,started_by,started_at,finished_at,counts_json,summary,report_json)
				VALUES(?,?,?,?,?,?,?,?,?)`,
				rep.ID, t.Key, rep.Status, rep.By, rep.StartedAt, rep.FinishedAt, string(counts), s.c.Scanner.Redact(rep.Summary), doc)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO security_scans(id,project_id,status,started_by,started_at,finished_at,counts_json,summary,report_json)
			SELECT ?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM projects WHERE id=?)`,
			rep.ID, rep.ProjectID, rep.Status, rep.By, rep.StartedAt, rep.FinishedAt, string(counts), s.c.Scanner.Redact(rep.Summary), doc, rep.ProjectID)
		return err
	})
}

func (s *securityExt) cancel(t target) (any, error) {
	s.mu.Lock()
	r, ok := s.running[t.Key]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no scan of this %s is running", t.noun())
	}
	r.Cancel()
	return r.Snapshot(), nil
}

// current is the running scan, else a finished one the database refused, else the last stored one, else nil.
func (s *securityExt) current(ctx context.Context, t target) (any, error) {
	s.mu.Lock()
	r, ok := s.running[t.Key]
	kept, held := s.unstored[t.Key]
	s.mu.Unlock()
	if ok {
		return r.Snapshot(), nil
	}
	if held {
		return kept, nil
	}
	var doc string
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT report_json FROM security_scans WHERE project_id=? ORDER BY started_at DESC, id DESC LIMIT 1`, t.Key).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeReport(doc)
}

func decodeReport(doc string) (*scan.Report, error) {
	var rep scan.Report
	if err := json.Unmarshal([]byte(doc), &rep); err != nil {
		return nil, fmt.Errorf("a stored scan report could not be read: %w", err)
	}
	if rep.Engines == nil {
		rep.Engines = []scan.EngineResult{}
	}
	if rep.Findings == nil {
		rep.Findings = []scan.Finding{}
	}
	return &rep, nil
}

func (s *securityExt) report(ctx context.Context, id string) (*scan.Report, error) {
	var doc string
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT report_json FROM security_scans WHERE id=?`, id).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("there is no scan %s", id)
	}
	if err != nil {
		return nil, err
	}
	return decodeReport(doc)
}

func historyLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}

// summaryCols read a history row; a folder scan's folder is the one its report names (the key may be lower case).
const summaryCols = `id,project_id,status,started_by,started_at,finished_at,counts_json,summary,COALESCE(json_extract(report_json,'$.dir'),'')`

func (s *securityExt) summaries(ctx context.Context, where string, args ...any) ([]scan.Summary, error) {
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT `+summaryCols+` FROM security_scans WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []scan.Summary{}
	for rows.Next() {
		var sm scan.Summary
		var key, counts, dir string
		if err := rows.Scan(&sm.ID, &key, &sm.Status, &sm.By, &sm.StartedAt, &sm.FinishedAt, &counts, &sm.Summary, &dir); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(counts), &sm.Counts)
		if strings.HasPrefix(key, folderPrefix) {
			sm.Folder = dir
		} else {
			sm.ProjectID = key
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

func (s *securityExt) list(ctx context.Context, t target, limit int) ([]scan.Summary, error) {
	return s.summaries(ctx, `project_id=? ORDER BY started_at DESC, id DESC LIMIT ?`, t.Key, historyLimit(limit))
}

// folders lists the recent folder scans of every folder (decision P61): those running first, then the stored ones, newest
// first.
func (s *securityExt) folders(ctx context.Context, limit int) ([]scan.Summary, error) {
	limit = historyLimit(limit)
	out := []scan.Summary{}
	s.mu.Lock()
	for key, r := range s.running {
		if !strings.HasPrefix(key, folderPrefix) {
			continue
		}
		rep := r.Snapshot()
		out = append(out, scan.Summary{ID: rep.ID, Folder: rep.Dir, Status: rep.Status, By: rep.By, StartedAt: rep.StartedAt, Counts: rep.Counts})
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	stored, err := s.summaries(ctx, `substr(project_id,1,?)=? ORDER BY started_at DESC, id DESC LIMIT ?`, len(folderPrefix), folderPrefix, limit)
	if err != nil {
		return nil, err
	}
	out = append(out, stored...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// close cancels the scans still running and waits for them, so no tool outlives the core.
func (s *securityExt) close() {
	s.mu.Lock()
	runs := make([]*scan.Run, 0, len(s.running))
	for _, r := range s.running {
		runs = append(runs, r)
	}
	for _, j := range s.jobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
	s.mu.Unlock()
	for _, r := range runs {
		r.Cancel()
	}
	for _, r := range runs {
		<-r.Done()
	}
}
