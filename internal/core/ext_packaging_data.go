package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/applog"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/projectio"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/supportbundle"
)

// Project export/import (docs/packaging.md s7) and the support bundle (s9). Both work in every license state: they are how a
// person gets their data out of an expired install.

func auditAppend(ctx context.Context, c *Core, action, subject string, detail any) error {
	_, err := security.NewAudit(c.DB, c.Scanner).Append(ctx, security.AuditEntry{Actor: c.Human(), Action: action, Subject: subject, Detail: detail})
	return err
}

func (p *packaging) source() projectio.Source {
	home, _ := os.UserHomeDir()
	return projectio.Source{DB: p.c.DB, DataDir: p.c.cfg.DataDir, Scanner: p.c.Scanner, AppVersion: AppVersion, Profile: home, Now: p.bnow}
}

var slugRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-.")
	if s == "" {
		return "project"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

type exportReq struct {
	ProjectID       string `json:"projectId"`
	Dest            string `json:"dest"`
	IncludeRuns     bool   `json:"includeRuns"`
	AcknowledgeRuns bool   `json:"acknowledgeRuns"`
}

const runsWarning = "Raw run output can contain your source code and anything an agent printed. It is scanned for secrets and findings are redacted, but scanning is not a guarantee."

func (p *packaging) exportPreview(ctx context.Context, r exportReq) (any, error) {
	res, err := p.source().Preview(ctx, projectio.ExportOptions{ProjectID: r.ProjectID, IncludeRuns: r.IncludeRuns})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"manifest": res.Manifest, "totalBytes": res.Size, "includeRuns": r.IncludeRuns}
	if r.IncludeRuns {
		out["warning"] = runsWarning
	}
	return out, nil
}

func (p *packaging) export(ctx context.Context, r exportReq) (any, error) {
	if r.IncludeRuns && !r.AcknowledgeRuns {
		return nil, errors.New(runsWarning + " Review the secret-scan result with project.exportPreview (includeRuns), then export again with acknowledgeRuns set.")
	}
	proj, err := p.c.GetProject(ctx, r.ProjectID)
	if err != nil {
		return nil, err
	}
	dest := r.Dest
	if dest != "" {
		if err := needFullPath("the file to save to", dest); err != nil {
			return nil, err
		}
	}
	if dest == "" {
		dest = filepath.Join(p.c.cfg.DataDir, "exports", fmt.Sprintf("%s-%s.drydock-project.zip", slug(proj.Name), p.bnow().UTC().Format("20060102-150405")))
	}
	res, err := p.source().Export(ctx, projectio.ExportOptions{ProjectID: r.ProjectID, Dest: dest, IncludeRuns: r.IncludeRuns})
	if err != nil {
		return nil, err
	}
	p.audit(ctx, "project.export", r.ProjectID, map[string]any{"file": filepath.Base(res.Path), "includeRuns": r.IncludeRuns, "redactions": res.Manifest.Scan.DBRedactions + res.Manifest.Scan.RunsRedactions})
	p.c.publish(ctx, "project.exported", r.ProjectID, map[string]any{"file": filepath.Base(res.Path), "bytes": res.Size, "includeRuns": r.IncludeRuns})
	return res, nil
}

type importReq struct {
	Path     string `json:"path"`
	RepoPath string `json:"repoPath"`
	Name     string `json:"name"`
}

func (p *packaging) importPreview(ctx context.Context, r importReq) (any, error) {
	if r.Path == "" {
		return nil, errors.New("choose a .drydock-project.zip file")
	}
	if err := needFullPath("the archive", r.Path); err != nil {
		return nil, err
	}
	return projectio.Inspect(ctx, r.Path)
}

// importProject imports an archive as a NEW project, linked to a local repository or (repoPath empty) to none yet: the
// repository is attached later in the project's settings. Importing creates data and starts nothing, so it works in every
// license state.
func (p *packaging) importProject(ctx context.Context, r importReq) (any, error) {
	c := p.c
	if r.Path == "" {
		return nil, errors.New("choose a .drydock-project.zip file")
	}
	if err := needFullPath("the archive", r.Path); err != nil {
		return nil, err
	}
	root, branch := "", ""
	if strings.TrimSpace(r.RepoPath) != "" {
		var err error
		if root, branch, err = openProjectRepo(ctx, r.RepoPath); err != nil {
			return nil, err
		}
	}
	res, err := projectio.Import(ctx, projectio.Target{DB: c.DB, DataDir: c.cfg.DataDir, Human: c.Human()},
		projectio.ImportOptions{Archive: r.Path, RepoPath: root, Branch: branch, Name: r.Name, Now: storage.Now()})
	if err != nil {
		return nil, err
	}
	p.audit(ctx, "project.import", res.ProjectID, map[string]any{"file": filepath.Base(r.Path), "source": res.SourceID, "idsRemapped": res.IDsRemapped})
	c.publish(ctx, "project.imported", res.ProjectID, map[string]any{"name": res.Name, "from": filepath.Base(r.Path)})
	// tell the human what is waiting for their decision, in the new project's own room
	if rooms, err := c.Room.ListRooms(ctx, res.ProjectID, false); err == nil {
		for _, rm := range rooms {
			if rm.Kind == room.KindChannel && rm.Name == "general" {
				if _, err := c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: room.TypeSystem, Author: room.System, Body: importSummary(res)}); err != nil {
					c.logf("import notice: %v", err)
				}
			}
		}
	}
	return res, nil
}

func importSummary(r projectio.ImportResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Imported \"%s\" as a new project. Nothing from the file is trusted yet:", r.Name)
	st := r.Pending
	if st.MemoryPending > 0 {
		fmt.Fprintf(&b, " %d memory item(s) wait for your acceptance;", st.MemoryPending)
	}
	if st.SkillsPending > 0 {
		fmt.Fprintf(&b, " %d skill(s) are offered, not accepted;", st.SkillsPending)
	}
	if st.RequirementChunks > 0 {
		fmt.Fprintf(&b, " %d requirement chunk(s) are quarantined until released;", st.RequirementChunks)
	}
	if st.RoutingRulesOff > 0 {
		fmt.Fprintf(&b, " %d routing rule(s) are disabled;", st.RoutingRulesOff)
	}
	if st.RunsCancelled > 0 || st.TasksBlocked > 0 {
		fmt.Fprintf(&b, " %d run(s) that were in progress were not resumed and %d task(s) are blocked;", st.RunsCancelled, st.TasksBlocked)
	}
	if st.ApprovalsCancelled > 0 {
		fmt.Fprintf(&b, " %d open approval(s) were cancelled;", st.ApprovalsCancelled)
	}
	for _, pr := range st.Providers {
		fmt.Fprintf(&b, " provider \"%s\" came without its key and is unauthenticated and disabled;", pr.Name)
	}
	fmt.Fprintf(&b, " automatic scheduling and the e2e and preview commands are switched off.")
	return b.String()
}

// ---- support bundle ----

type bundleReq struct {
	Dest           string `json:"dest"`
	EventLimit     int    `json:"eventLimit"`
	IncludeRun     string `json:"includeRun"`
	AcknowledgeRun bool   `json:"acknowledgeRun"`
}

func (p *packaging) toolVersions(ctx context.Context) map[string]string {
	out := map[string]string{}
	for name, args := range map[string][]string{"git": {"--version"}, "node": {"--version"}, "gh": {"--version"}} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		b, err := exec.CommandContext(cctx, path, args...).Output()
		cancel()
		if err != nil {
			continue
		}
		line := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
		out[name] = line
	}
	return out
}

// bundleInputs gathers the explicit allow-list of inputs. Nothing is read by globbing a directory except the application's own
// log files, whose names are fixed.
func (p *packaging) bundleInputs(ctx context.Context, r bundleReq) (supportbundle.Inputs, error) {
	c := p.c
	in := supportbundle.Inputs{AppVersion: AppVersion, Build: AppBuild, OS: runtime.GOOS + "/" + runtime.GOARCH, OSVersion: supportbundle.OSVersion(),
		InstallScope: InstallScope, DiskPath: c.cfg.DataDir}
	in.SchemaVersion, _ = c.DB.SchemaVersion(ctx)
	in.Tools = p.toolVersions(ctx)
	for _, cn := range c.Connections.DetectAll(ctx) {
		in.Connections = append(in.Connections, supportbundle.Connection{ID: cn.Adapter, State: string(cn.Status), Version: cn.Version, Verdict: cn.CompatClass, Detail: strings.Join(cn.Problems, "; ")})
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT key,value FROM settings`)
	if err != nil {
		return in, err
	}
	in.Settings = map[string]string{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			in.Settings[k] = v
		}
	}
	rows.Close()
	for _, f := range applog.Files(filepath.Join(c.cfg.DataDir, "logs"), 3) {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		in.Logs = append(in.Logs, supportbundle.LogFile{Name: filepath.Base(f), Text: string(b)})
	}
	limit := r.EventLimit
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	if head, err := c.Bus.LastSeq(ctx); err == nil {
		after := uint64(0)
		if head > uint64(limit) {
			after = head - uint64(limit)
		}
		evs, err := c.Bus.Replay(ctx, after, events.Filter{}, limit)
		if err != nil {
			return in, err
		}
		for _, e := range evs { // the payload is dropped here: EventRef has no field for it
			in.Events = append(in.Events, supportbundle.EventRef{Seq: e.Seq, Type: e.Type, TS: e.Time.UnixMilli(), ProjectID: e.ProjectID, RunID: e.RunID, TaskID: e.TaskID, AgentID: e.AgentID})
		}
	}
	wr, err := c.DB.Reader().QueryContext(ctx, `SELECT signal, stage, COUNT(*), MAX(created_at) FROM watchdog_trips GROUP BY signal, stage ORDER BY signal, stage`)
	if err == nil {
		for wr.Next() {
			var w supportbundle.WatchdogSignature
			if wr.Scan(&w.Signal, &w.Stage, &w.Count, &w.Last) == nil {
				in.Watchdog = append(in.Watchdog, w)
			}
		}
		wr.Close()
	}
	in.DiskFree, in.DiskTotal, _ = supportbundle.DiskFree(c.cfg.DataDir)
	for _, kv := range os.Environ() { // names only: the value is never read into the bundle
		if i := strings.IndexByte(kv, '='); i > 0 {
			in.EnvNames = append(in.EnvNames, kv[:i])
		}
	}
	sort.Strings(in.EnvNames)
	if r.IncludeRun != "" {
		run, err := p.runInclusion(ctx, r.IncludeRun)
		if err != nil {
			return in, err
		}
		in.Run = run
	}
	return in, nil
}

const (
	maxRunFiles     = 500
	maxRunFileBytes = 1 << 20
	maxRunTotal     = 16 << 20
)

func (p *packaging) runInclusion(ctx context.Context, runID string) (*supportbundle.RunInclusion, error) {
	c := p.c
	if strings.ContainsAny(runID, `/\`) || runID == "" {
		return nil, errors.New("invalid run id")
	}
	var n int
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs WHERE id=?`, runID).Scan(&n); err != nil || n == 0 {
		return nil, fmt.Errorf("there is no run %s", runID)
	}
	ri := &supportbundle.RunInclusion{RunID: runID}
	evs, err := c.Bus.Replay(ctx, 0, events.Filter{RunID: runID}, 5000)
	if err != nil {
		return nil, err
	}
	for _, e := range evs {
		ri.Events = append(ri.Events, supportbundle.RunEvent{Seq: e.Seq, Type: e.Type, TS: e.Time.UnixMilli(), Data: string(e.Data)})
	}
	root := filepath.Join(c.cfg.DataDir, "runs", runID)
	total := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() || len(ri.Files) >= maxRunFiles || total >= maxRunTotal {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxRunFileBytes {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		ri.Files = append(ri.Files, supportbundle.RunFile{Name: filepath.ToSlash(rel), Content: string(b)})
		total += len(b)
		return nil
	})
	return ri, nil
}

func (p *packaging) buildBundle(ctx context.Context, r bundleReq) (supportbundle.Bundle, error) {
	in, err := p.bundleInputs(ctx, r)
	if err != nil {
		return supportbundle.Bundle{}, err
	}
	return supportbundle.Build(ctx, in, supportbundle.Options{Scanner: p.c.Scanner, Identity: p.identity, Now: p.bnow})
}

func (p *packaging) dataMethods(m map[string]Method) {
	m["project.exportPreview"] = bind(func(ctx context.Context, r exportReq) (any, error) { return p.exportPreview(ctx, r) })
	m["project.export"] = bind(func(ctx context.Context, r exportReq) (any, error) { return p.export(ctx, r) })
	m["project.importPreview"] = bind(func(ctx context.Context, r importReq) (any, error) { return p.importPreview(ctx, r) })
	m["project.import"] = bind(func(ctx context.Context, r importReq) (any, error) { return p.importProject(ctx, r) })
	m["supportbundle.preview"] = bind(func(ctx context.Context, r bundleReq) (any, error) {
		b, err := p.buildBundle(ctx, r)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"files": b.Files, "report": b.Report, "notice": supportbundle.Notice, "includesRun": r.IncludeRun}
		if r.IncludeRun != "" {
			out["warning"] = "Including a run adds its events with payloads and its raw output. They are scanned and findings are redacted, but check the report first."
		}
		return out, nil
	})
	m["supportbundle.create"] = bind(func(ctx context.Context, r bundleReq) (any, error) {
		if r.IncludeRun != "" && !r.AcknowledgeRun {
			return nil, errors.New("including a run adds its payloads and raw output. Review the secret-scan result with supportbundle.preview (includeRun), then create the bundle again with acknowledgeRun set")
		}
		b, err := p.buildBundle(ctx, r)
		if err != nil {
			return nil, err
		}
		dest := r.Dest
		if dest != "" {
			if err := needFullPath("the file to save to", dest); err != nil {
				return nil, err
			}
		}
		if dest == "" {
			dest = filepath.Join(p.c.cfg.DataDir, "support", "drydock-support-"+p.bnow().UTC().Format("20060102-150405")+".zip")
		}
		if err := supportbundle.Write(b, dest); err != nil {
			return nil, err
		}
		st, _ := os.Stat(dest)
		var size int64
		if st != nil {
			size = st.Size()
		}
		p.audit(ctx, "supportbundle.create", filepath.Base(dest), map[string]any{"runIncluded": r.IncludeRun, "secretsRedacted": b.Report.TotalSecrets, "refused": len(b.Report.Refused)})
		return map[string]any{"path": dest, "size": size, "files": b.Files, "report": b.Report, "notice": supportbundle.Notice}, nil
	})
}
