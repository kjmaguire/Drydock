package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/storage"
)

// The security scan (decision P44, docs/security-scan.md): one button runs the three engines of internal/scan over the
// project's working tree. A scan in progress lives here in memory (its progress counters change many times a second and are
// read, not published); it is stored when it ends. Events: security.scan.started, security.scan.engine (an engine changed
// state), security.scan.finished, security.settings. Only a person starts a scan or chooses the rules folder.

func init() {
	RegisterExtension(Extension{Name: "security", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		s := &securityExt{c: c, running: map[string]*scan.Run{}}
		return map[string]Method{
			"security.tools": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				return s.tools(ctx, p.ProjectID), nil
			}),
			"security.settings.set": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				RulesDir  string `json:"rulesDir"`
			}) (any, error) {
				return s.setRulesDir(ctx, p.ProjectID, p.RulesDir, c.Human())
			}),
			"security.scan.start": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				return s.start(ctx, p.ProjectID, c.Human())
			}),
			"security.scan.cancel": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				return s.cancel(p.ProjectID)
			}),
			"security.scan.get": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				return s.current(ctx, p.ProjectID)
			}),
			"security.scan.list": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				Limit     int    `json:"limit"`
			}) (any, error) {
				return s.list(ctx, p.ProjectID, p.Limit)
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
	// running holds each project's scan in progress, by project id
	running map[string]*scan.Run
}

// settingRules + project id holds the project's Opengrep rules folder.
const settingRules = "security:rules:"

func (s *securityExt) rulesDir(ctx context.Context, projectID string) string {
	var v string
	if err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingRules+projectID).Scan(&v); err == nil {
		return v
	}
	return s.c.cfg.ScanRulesDir
}

// toolEnv is what the external tools get: the scrubbed tool environment plus the variables the person passes to project
// commands, never the app's own (docs/security.md s8).
func toolEnv() []string {
	var extra []string
	if checks.PassEnv != nil {
		extra = checks.PassEnv()
	}
	return proc.ToolEnv(os.Environ(), extra)
}

func (s *securityExt) tools(ctx context.Context, projectID string) scan.Tools {
	return scan.Detect(ctx, s.c.Deps.PM, toolEnv(), s.c.cfg.ScanTools, s.rulesDir(ctx, projectID))
}

// setRulesDir stores the project's rules folder. Only a person may: the folder decides which rules read the whole project.
func (s *securityExt) setRulesDir(ctx context.Context, projectID, dir, by string) (scan.Tools, error) {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return scan.Tools{}, errors.New("only a person may choose the rules folder")
	}
	if _, err := s.c.GetProject(ctx, projectID); err != nil {
		return scan.Tools{}, err
	}
	dir = strings.TrimSpace(dir)
	if err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingRules+projectID, dir)
		return err
	}); err != nil {
		return scan.Tools{}, err
	}
	s.c.publish(ctx, "security.settings", projectID, map[string]any{"rulesDir": dir, "by": by})
	return s.tools(ctx, projectID), nil
}

// start begins a scan of the project's working tree; one per project at a time.
func (s *securityExt) start(ctx context.Context, projectID, by string) (scan.Report, error) {
	p, err := s.c.GetProject(ctx, projectID)
	if err != nil {
		return scan.Report{}, err
	}
	if p.RepoPath == "" {
		return scan.Report{}, errors.New("this project has no repository yet: attach one in Project settings, then scan it")
	}
	if st, err := os.Stat(p.RepoPath); err != nil || !st.IsDir() {
		return scan.Report{}, fmt.Errorf("the repository folder %s is not there", p.RepoPath)
	}
	s.mu.Lock()
	if r, ok := s.running[projectID]; ok {
		s.mu.Unlock()
		return r.Snapshot(), errors.New("a scan of this project is already running")
	}
	id := ids.New("scn")
	// the scan outlives the request; close() cancels what is still running when the core closes
	run := scan.Start(context.Background(), scan.Options{ID: id, ProjectID: projectID, By: by, Dir: p.RepoPath, Procs: s.c.Deps.PM, Redact: s.c.Scanner.Redact,
		Env: toolEnv, Paths: s.c.cfg.ScanTools, RulesDir: s.rulesDir(ctx, projectID), OnChange: func(rep scan.Report) { s.changed(projectID, rep) }})
	s.running[projectID] = run
	s.mu.Unlock()
	s.c.publish(ctx, "security.scan.started", projectID, map[string]any{"id": id, "by": by})
	return run.Snapshot(), nil
}

// changed is called by the scan after an engine changed state and when it ends: the end is stored and published.
func (s *securityExt) changed(projectID string, rep scan.Report) {
	ctx := context.Background()
	if rep.Status == scan.StatusRunning {
		for _, e := range rep.Engines {
			if e.Status != scan.Pending {
				s.c.publish(ctx, "security.scan.engine", projectID, map[string]any{"id": rep.ID, "engine": e.ID, "status": e.Status})
			}
		}
		return
	}
	if err := s.store(ctx, rep); err != nil {
		s.c.logf("security scan %s: store: %v", rep.ID, err)
	}
	s.mu.Lock()
	delete(s.running, projectID)
	s.mu.Unlock()
	s.c.publish(ctx, "security.scan.finished", projectID, map[string]any{"id": rep.ID, "status": rep.Status, "counts": rep.Counts, "summary": rep.Summary})
}

// store writes the finished report, redacted once more as a whole (nothing that looks like a secret survives, even in a
// file name).
func (s *securityExt) store(ctx context.Context, rep scan.Report) error {
	b, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	doc := s.c.Scanner.Redact(string(b))
	counts, _ := json.Marshal(rep.Counts)
	return s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO security_scans(id,project_id,status,started_by,started_at,finished_at,counts_json,summary,report_json) VALUES(?,?,?,?,?,?,?,?,?)`,
			rep.ID, rep.ProjectID, rep.Status, rep.By, rep.StartedAt, rep.FinishedAt, string(counts), s.c.Scanner.Redact(rep.Summary), doc)
		return err
	})
}

func (s *securityExt) cancel(projectID string) (any, error) {
	s.mu.Lock()
	r, ok := s.running[projectID]
	s.mu.Unlock()
	if !ok {
		return nil, errors.New("no scan of this project is running")
	}
	r.Cancel()
	return r.Snapshot(), nil
}

// current is the running scan, else the last stored one, else nil.
func (s *securityExt) current(ctx context.Context, projectID string) (any, error) {
	s.mu.Lock()
	r, ok := s.running[projectID]
	s.mu.Unlock()
	if ok {
		return r.Snapshot(), nil
	}
	var doc string
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT report_json FROM security_scans WHERE project_id=? ORDER BY started_at DESC, id DESC LIMIT 1`, projectID).Scan(&doc)
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

func (s *securityExt) list(ctx context.Context, projectID string, limit int) ([]scan.Summary, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT id,status,started_by,started_at,finished_at,counts_json,summary FROM security_scans WHERE project_id=? ORDER BY started_at DESC, id DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []scan.Summary{}
	for rows.Next() {
		var sm scan.Summary
		var counts string
		if err := rows.Scan(&sm.ID, &sm.Status, &sm.By, &sm.StartedAt, &sm.FinishedAt, &counts, &sm.Summary); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(counts), &sm.Counts)
		sm.ProjectID = projectID
		out = append(out, sm)
	}
	return out, rows.Err()
}

// close cancels the scans still running and waits for them, so no tool outlives the core.
func (s *securityExt) close() {
	s.mu.Lock()
	runs := make([]*scan.Run, 0, len(s.running))
	for _, r := range s.running {
		runs = append(runs, r)
	}
	s.mu.Unlock()
	for _, r := range runs {
		r.Cancel()
	}
	for _, r := range runs {
		<-r.Done()
	}
}

var _ = storage.Now
