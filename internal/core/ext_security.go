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
// Only a person starts a scan.

func init() {
	RegisterExtension(Extension{Name: "security", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		s := &securityExt{c: c, running: map[string]*scan.Run{}, unstored: map[string]scan.Report{}, jobs: map[string]*toolJob{}, latest: map[string]string{},
			inst: scan.NewInstaller(filepath.Join(c.cfg.DataDir, "tools"))}
		// a release becomes the one in use only once it ran the way a scan runs it (decision P95)
		s.inst.Check = func(ctx context.Context, tool string, t scan.Tool) error {
			return scan.SelfCheck(ctx, scan.Options{Procs: c.Deps.PM, Redact: c.Scanner.Redact, Env: checks.CommandEnv}, tool, t)
		}
		if c.cfg.ScanInstall != nil {
			c.cfg.ScanInstall(s.inst)
		}
		s.forgetChoices(ctx)
		s.seedBundled(ctx)
		securityRegistry.Store(c, s) // the `scan` check and the agents' security_findings tool read it (ext_security_check.go)
		wctx, stopWatch := context.WithCancel(context.Background())
		s.stopWatch = stopWatch
		s.watchUpdates(wctx)
		// the built-in rules (decision P77) are written once per version under the app's data; every scan runs them. A folder
		// that cannot be written leaves them out, and the page and the scan say so.
		if dir, err := scan.WriteBuiltinRules(filepath.Join(c.cfg.DataDir, "rules")); err != nil {
			c.logf("security: the built-in rules could not be written: %v", err)
		} else {
			s.builtinRules = dir
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
		methods := map[string]Method{
			// without a project: the tools and the rules folder of folder scans
			"security.tools": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				return s.tools(ctx, p.ProjectID), nil
			}),
			// Drydock installs the tool's newest release itself (decision P66); the same call updates an installed one
			"security.tool.install": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				Tool      string `json:"tool"`
			}) (any, error) {
				return s.installTool(ctx, p.ProjectID, p.Tool, c.Human())
			}),
			// the newest release of every installed tool, read now instead of at the next twice-daily check (decision P77)
			"security.tools.checkUpdates": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				s.checkUpdates(ctx, true) // checked now: a newer release is installed (decision P95)
				s.memoMu.Lock()
				s.memo.key = "" // the answer must carry what was just read
				s.memoMu.Unlock()
				return s.tools(ctx, p.ProjectID), nil
			}),
			"security.tool.install.cancel": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				Tool      string `json:"tool"`
			}) (any, error) {
				return s.cancelInstall(ctx, p.ProjectID, p.Tool)
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
			// the newest scans of every project and folder (the workspace Security page's history), in one read
			"security.scan.recent": bind(func(ctx context.Context, p struct {
				Limit int `json:"limit"`
			}) (any, error) {
				return s.recent(ctx, p.Limit)
			}),
			// every scan in progress, of a project or a folder (the sidebar's Running count, the inbox's Running tab)
			"security.scan.running": bind(func(ctx context.Context, _ struct{}) (any, error) {
				return s.runningScans(ctx), nil
			}),
			"security.scan.report": bind(func(ctx context.Context, p struct {
				ID string `json:"id"`
			}) (any, error) {
				return s.report(ctx, p.ID)
			}),
		}
		// one finding: dismiss, restore, the code around it, a task from it (ext_security_findings.go, decision P97)
		for name, m := range s.findingMethods() {
			methods[name] = m
		}
		return methods, s.close, nil
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
	// builtinRules is the folder the built-in rules were written to; empty when they could not be
	builtinRules string
	// latest is each installed tool's newest release, as the last update check read it (under mu)
	latest map[string]string
	// stopWatch ends the update check
	stopWatch context.CancelFunc
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

// Earlier versions let a person choose an Opengrep rules folder (per project, and one for folder scans) and a program for
// each external tool, on the Security page. The page no longer shows the tools (decision P95), so a choice stored then would
// apply where nobody can see, change or clear it: the scan uses Drydock's own tools and its built-in rules, and the stored
// choices are deleted at start (forgetChoices).
const (
	settingRules          = "security:rules:"
	settingWorkspaceRules = "security:workspace-rules"
	settingToolPath       = "security:toolpath:"
)

// forgetChoices deletes the rules folders and programs chosen in earlier versions.
func (s *securityExt) forgetChoices(ctx context.Context) {
	var n int64
	err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=? OR substr(key,1,?)=? OR substr(key,1,?)=?`,
			settingWorkspaceRules, len(settingRules), settingRules, len(settingToolPath), settingToolPath)
		if err == nil {
			n, _ = r.RowsAffected()
		}
		return err
	})
	switch {
	case err != nil:
		s.c.logf("security: forgetting the rules folders and programs chosen before: %v", err)
	case n > 0:
		s.c.logf("security: %d rules folder or program choices from an earlier version forgotten: the scan uses Drydock's own tools and rules", n)
	}
}

// rulesDir is the folder of extra Opengrep rules: the configuration's (the e2e harness's; the product leaves it empty and
// runs the built-in rules alone).
func (s *securityExt) rulesDir(_ context.Context, _ string) string { return s.c.cfg.ScanRulesDir }

func (s *securityExt) tools(ctx context.Context, projectID string) scan.Tools {
	jobs := s.installJobs() // first: see installJobs
	paths, rules := s.paths(ctx), s.rulesDir(ctx, projectID)
	key := fmt.Sprintf("%s|%v|%v|%s|%v|%v|%s", paths.OSV.Path, paths.OSV.Chosen, paths.OSV.Installed, paths.Opengrep.Path, paths.Opengrep.Chosen, paths.Opengrep.Installed, rules)
	s.memoMu.Lock()
	if s.memo.key == key && time.Since(s.memo.at) < toolsMemoTTL && toolsStamp(s.memo.v) == s.memo.stamp {
		t := s.memo.v
		s.memoMu.Unlock()
		return s.withInstalls(t, jobs)
	}
	s.memoMu.Unlock()
	// the external tools read the whole project: they get what every project command gets (checks.CommandEnv), no more
	t := scan.Detect(ctx, s.c.Deps.PM, checks.CommandEnv(), paths, rules)
	t.CanChoose = s.c.cfg.PickFile != nil
	s.memoMu.Lock()
	s.memo.key, s.memo.stamp, s.memo.at, s.memo.v = key, toolsStamp(t), time.Now(), t
	s.memoMu.Unlock()
	return s.withInstalls(t, jobs)
}

// paths are the configured tools with the one Drydock installed in place of each (decision P66).
func (s *securityExt) paths(_ context.Context) scan.Paths {
	p := s.c.cfg.ScanTools
	pick := func(tool string, dflt scan.Tool) scan.Tool {
		if in, ok := s.inst.Installed(tool); ok {
			return s.inst.ToolFor(in)
		}
		return dflt
	}
	p.OSV = pick("osv-scanner", p.OSV)
	p.Opengrep = pick("opengrep", p.Opengrep)
	p.BuiltinRules = s.builtinRules
	return p
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
		Env: checks.CommandEnv, Paths: s.paths(ctx), BuiltinRules: s.builtinRules, RulesDir: s.rulesDir(ctx, t.ProjectID), NoGit: t.Folder != "", OnChange: func(rep scan.Report, e scan.EngineID) { s.changed(t, rep, e) }})
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
	// the findings a person dismissed before are marked and left out of the counts and the summary it is stored with
	s.overlay(ctx, t.Key, &rep)
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

// current is the running scan, else a finished one the database refused, else the last stored one, else nil; its findings
// marked with the dismissals as they are now.
func (s *securityExt) current(ctx context.Context, t target) (any, error) {
	rep, err := s.lastReport(ctx, t)
	if err != nil || rep == nil {
		return nil, err
	}
	s.overlay(ctx, t.Key, rep)
	return rep, nil
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

// report is a stored scan's report, its findings marked with the dismissals as they are now.
func (s *securityExt) report(ctx context.Context, id string) (*scan.Report, error) {
	var key, doc string
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT project_id, report_json FROM security_scans WHERE id=?`, id).Scan(&key, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("there is no scan %s", id)
	}
	if err != nil {
		return nil, err
	}
	rep, err := decodeReport(doc)
	if err != nil {
		return nil, err
	}
	s.overlay(ctx, key, rep)
	return rep, nil
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
	out := s.runningFolderScans()
	// read after the running scans: a scan that finishes meanwhile is stored before it leaves them, so it is in one or both
	stored, err := s.summaries(ctx, `substr(project_id,1,?)=? ORDER BY started_at DESC, id DESC LIMIT ?`, len(folderPrefix), folderPrefix, limit)
	if err != nil {
		return nil, err
	}
	return mergeFolderScans(out, stored, limit), nil
}

// recent lists the newest scans of every project and every folder, the running folder scans first (as folders), in one read
// however many projects there are: the workspace Security page asked each project for its history (review 2026-10-27).
func (s *securityExt) recent(ctx context.Context, limit int) ([]scan.Summary, error) {
	limit = historyLimit(limit)
	out := s.runningFolderScans()
	stored, err := s.summaries(ctx, `1=1 ORDER BY started_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return mergeFolderScans(out, stored, limit), nil
}

// runningFolderScans are the folder scans in progress, as history rows.
func (s *securityExt) runningFolderScans() []scan.Summary {
	out := []scan.Summary{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, r := range s.running {
		if !strings.HasPrefix(key, folderPrefix) {
			continue
		}
		rep := r.Snapshot()
		out = append(out, scan.Summary{ID: rep.ID, Folder: rep.Dir, Status: rep.Status, By: rep.By, StartedAt: rep.StartedAt, Counts: rep.Counts})
	}
	return out
}

// mergeFolderScans lists the running folder scans, newest first, then the stored ones. A scan that has just finished is
// stored a moment before it leaves the running ones (changed()), so in that moment it is in both: its stored row, which has
// its end and summary, is the one shown, once.
func mergeFolderScans(running, stored []scan.Summary, limit int) []scan.Summary {
	have := make(map[string]bool, len(stored))
	for _, sm := range stored {
		have[sm.ID] = true
	}
	out := make([]scan.Summary, 0, len(running)+len(stored))
	for _, sm := range running {
		if !have[sm.ID] {
			out = append(out, sm)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	out = append(out, stored...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// RunningScan is a scan in progress as the workspace lists it: what it reads, by whom, and each engine's state and count.
type RunningScan struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId,omitempty"`
	Folder    string `json:"folder,omitempty"` // a folder scan's folder (decision P61)
	// Name is what the scan reads, as a person names it: the project's name, or the folder's last element.
	Name      string              `json:"name"`
	By        string              `json:"by"`
	StartedAt int64               `json:"startedAt"`
	Engines   []scan.EngineResult `json:"engines"`
	Counts    scan.Counts         `json:"counts"`
}

// runningScans lists every scan in progress, newest first.
func (s *securityExt) runningScans(ctx context.Context) []RunningScan {
	type entry struct {
		key string
		rep scan.Report
	}
	var live []entry
	s.mu.Lock()
	for key, r := range s.running {
		live = append(live, entry{key, r.Snapshot()})
	}
	s.mu.Unlock()
	out := []RunningScan{}
	for _, e := range live {
		if e.rep.Status != scan.StatusRunning { // finished a moment ago, about to leave the running ones
			continue
		}
		rs := RunningScan{ID: e.rep.ID, ProjectID: e.rep.ProjectID, By: e.rep.By, StartedAt: e.rep.StartedAt, Engines: e.rep.Engines, Counts: e.rep.Counts}
		if strings.HasPrefix(e.key, folderPrefix) {
			rs.Folder = e.rep.Dir
			rs.Name = filepath.Base(e.rep.Dir)
		} else if p, err := s.c.GetProject(ctx, e.rep.ProjectID); err == nil {
			rs.Name = p.Name
		}
		if rs.Engines == nil {
			rs.Engines = []scan.EngineResult{}
		}
		out = append(out, rs)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt > out[j].StartedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// close cancels the scans still running and waits for them, so no tool outlives the core.
func (s *securityExt) close() {
	securityRegistry.Delete(s.c)
	if s.stopWatch != nil {
		s.stopWatch()
	}
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
