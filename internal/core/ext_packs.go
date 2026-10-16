package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/libver"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/memory"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/requirements"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/security/secretadapt"
	"github.com/kjmaguire/drydock/internal/skills"
)

// The "knowledge" extension (Slice 3): Expert Packs, Requirements, Milestones, Skills and the memory page, as UI methods
// (pack.*, requirement.*, milestone.*, skill.*, memory.*) plus the providers that fill the Workspace tools search_pack,
// search_requirements, search_memory and skills. Every mutation is human-only: the methods act as c.Human(), the services
// refuse any other principal, and security-relevant actions are written to the audit log.

func init() {
	RegisterExtension(Extension{Name: "knowledge", Start: startKnowledge})
}

// Knowledge bundles the Slice 3 services of one Core.
type Knowledge struct {
	Packs        *packs.Store
	Requirements *requirements.Service
	Skills       *skills.Service
	Memory       memory.Store
	Audit        *security.Audit

	c        *Core
	live     *lazyGateway
	scanMu   sync.Mutex
	scanned  map[string]time.Time
	stopSked func()
}

var knowledgeRegistry sync.Map // *Core -> *Knowledge

// Knowledge returns the Slice 3 services of this Core (nil if the extension failed to start).
func (c *Core) Knowledge() *Knowledge {
	if v, ok := knowledgeRegistry.Load(c); ok {
		return v.(*Knowledge)
	}
	return nil
}

// Workspace returns the Workspace providers for mcp.Options.Workspace: search_pack, search_requirements, search_memory and
// skills. The wiring layer that builds the gateway copies these fields into its Workspace value.
func (k *Knowledge) Workspace() mcp.Workspace {
	return mcp.Workspace{
		Packs:        packs.NewWorkspaceProvider(k.Packs),
		Requirements: requirements.NewSearchProvider(k.Requirements),
		Skills:       skills.NewProvider(k.Skills),
		Memory:       knowledgeMemory{k.Memory},
	}
}

// UseGateway makes live packs query MCP docs servers through g (the app's shared gateway) instead of the private gateway this
// extension starts on first use.
func (k *Knowledge) UseGateway(g packs.RunGateway) { k.live.set(g) }

// knowledgeMemory implements mcp.MemorySearcher over project memory.
type knowledgeMemory struct{ s memory.Store }

func (m knowledgeMemory) Search(ctx context.Context, run mcp.RunInfo, query, scope string, k int) ([]mcp.TextHit, error) {
	var es []memory.Entry
	var err error
	if scope == "workspace" {
		es, err = m.s.SearchWorkspace(ctx, query, k)
	} else {
		if run.ProjectID == "" {
			return nil, errors.New("this run has no project")
		}
		es, err = m.s.Search(ctx, run.ProjectID, query, k)
	}
	if err != nil {
		return nil, err
	}
	out := make([]mcp.TextHit, 0, len(es))
	for _, e := range es {
		out = append(out, mcp.TextHit{ID: e.ID, Source: e.Kind, Title: e.Title, Text: e.Body})
	}
	return out, nil
}

// lazyGateway starts a private loopback MCP gateway on first use for live packs, unless the app supplies its own.
type lazyGateway struct {
	c  *Core
	mu sync.Mutex
	g  packs.RunGateway
	gw *mcp.Gateway
}

func (l *lazyGateway) set(g packs.RunGateway) {
	l.mu.Lock()
	l.g = g
	l.mu.Unlock()
}

func (l *lazyGateway) get() (packs.RunGateway, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.g != nil {
		return l.g, nil
	}
	gw, err := mcp.New(mcp.Options{
		DataDir: filepath.Join(l.c.cfg.DataDir, "packs-live"), DB: l.c.DB, Bus: l.c.Bus, Proc: l.c.cfg.Procs,
		Scanner: secretadapt.MCP(l.c.Scanner),
	})
	if err != nil {
		return nil, err
	}
	if err := gw.Start(); err != nil {
		return nil, err
	}
	l.gw, l.g = gw, gw
	return gw, nil
}

func (l *lazyGateway) StartRun(cfg mcp.RunConfig) (*mcp.Endpoint, error) {
	g, err := l.get()
	if err != nil {
		return nil, err
	}
	return g.StartRun(cfg)
}

func (l *lazyGateway) EndRun(runID string) {
	l.mu.Lock()
	g := l.g
	l.mu.Unlock()
	if g != nil {
		g.EndRun(runID)
	}
}

func (l *lazyGateway) InvalidateCache(ctx context.Context, serverID string) (int, error) {
	l.mu.Lock()
	g := l.g
	l.mu.Unlock()
	if g == nil {
		return 0, nil
	}
	return g.InvalidateCache(ctx, serverID)
}

func (l *lazyGateway) close() {
	l.mu.Lock()
	gw := l.gw
	l.gw = nil
	l.mu.Unlock()
	if gw != nil {
		_ = gw.Close()
	}
}

func startKnowledge(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	k := &Knowledge{c: c, scanned: map[string]time.Time{}}
	k.live = &lazyGateway{c: c}
	k.Memory = memory.Store{DB: c.DB}
	k.Audit = security.NewAudit(c.DB, c.Scanner)
	var err error
	k.Packs, err = packs.New(packs.Options{
		DB: c.DB, Bus: c.Bus, Dir: filepath.Join(c.cfg.DataDir, "packs"), Scanner: c.Scanner,
		Live: &packs.GatewayLive{GW: k.live}, Logf: c.logf,
	})
	if err != nil {
		return nil, nil, err
	}
	k.Requirements = &requirements.Service{DB: c.DB, Bus: c.Bus, Scanner: c.Scanner}
	k.Skills = &skills.Service{DB: c.DB, Bus: c.Bus, Scanner: c.Scanner, Fetcher: skills.NewGitHubFetcher(), Repo: c.repo}
	// Repository skills are offered, never accepted automatically, unless the human turns that off with the setting
	// skills.manual_accept_repo = 0 (decision P11: an agent-authored skill merged through an approved diff would otherwise become
	// standing instructions without a separate decision).
	k.Skills.ManualAcceptRepoSkills = true
	var manual string
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key='skills.manual_accept_repo'`).Scan(&manual); err == nil && (manual == "0" || manual == "false") {
		k.Skills.ManualAcceptRepoSkills = false
	}
	if err := k.Skills.EnsureRegistries(ctx); err != nil {
		return nil, nil, err
	}
	k.wireContext()
	k.stopSked = k.Packs.Start(ctx, 15*time.Minute)
	knowledgeRegistry.Store(c, k)
	closeFn := func() {
		k.stopSked()
		k.live.close()
		knowledgeRegistry.Delete(c)
	}
	return k.methods(), closeFn, nil
}

// ---- context engine hooks: attached pack instructions, the skills note, and skill files ----

// projectTarget fills in what every skill plan of a project shares: the skill delivery its development settings choose,
// unless the caller named one. Every plan, the real run's and the preview's, goes through it, so they cannot drift apart.
func (k *Knowledge) projectTarget(ctx context.Context, t skills.Target) skills.Target {
	if t.Delivery == "" {
		t.Delivery = pipeline.LoadDevSettings(ctx, k.c.DB, t.ProjectID).SkillDelivery
	}
	return t
}

// worktreeTarget finds the task and agent a worktree belongs to.
func (k *Knowledge) worktreeTarget(ctx context.Context, projectID, worktree string) skills.Target {
	t := k.projectTarget(ctx, skills.Target{ProjectID: projectID})
	if worktree == "" {
		return t
	}
	var files, assignee string
	err := k.c.DB.Reader().QueryRowContext(ctx, `SELECT id, assignee, files FROM tasks WHERE project_id=? AND worktree=?`, projectID, worktree).Scan(&t.TaskID, &assignee, &files)
	if err != nil {
		return t
	}
	_ = json.Unmarshal([]byte(files), &t.Hints)
	if assignee != "" {
		t.AgentID = assignee
		_ = k.c.DB.Reader().QueryRowContext(ctx, `SELECT adapter, role FROM agents WHERE id=?`, assignee).Scan(&t.Adapter, &t.Role)
	}
	return t
}

// refreshSkills rescans the project's .aet/skills at most every 30 seconds.
func (k *Knowledge) refreshSkills(ctx context.Context, projectID string) {
	k.scanMu.Lock()
	last := k.scanned[projectID]
	if time.Since(last) < 30*time.Second {
		k.scanMu.Unlock()
		return
	}
	k.scanned[projectID] = time.Now()
	k.scanMu.Unlock()
	if _, err := k.Skills.ScanRepo(ctx, projectID); err != nil {
		k.c.logf("skills scan: %v", err)
	}
}

func (k *Knowledge) wireContext() {
	k.c.Context.Sections = func(ctx context.Context, projectID, worktree string) ([]ctxeng.Section, error) {
		if worktree == "" {
			return nil, nil
		}
		t := k.worktreeTarget(ctx, projectID, worktree)
		var out []ctxeng.Section
		if sec, ok := libraryVersionsSection(worktree); ok {
			out = append(out, sec)
		}
		ins, err := k.Packs.Instructions(ctx, projectID, t.AgentID, t.Role)
		if err != nil {
			return nil, err
		}
		if len(ins) > 0 {
			var lines, ids []string
			for _, in := range ins {
				ids = append(ids, in.PackID)
				lines = append(lines, fmt.Sprintf("- **%s** (`%s`, %s): %s", in.Title, in.PackID, in.Kind, in.Text))
			}
			out = append(out, ctxeng.Section{Title: "Expert packs", Body: strings.Join(lines, "\n"), Source: "pack:" + strings.Join(ids, ","),
				Reason: "attached expert packs: instructions only, documentation is retrieved on demand with search_pack", Priority: 35})
		}
		k.refreshSkills(ctx, projectID)
		plan, err := k.Skills.Plan(ctx, t)
		if err != nil {
			return nil, err
		}
		if plan.Note != "" {
			reason := "this CLI has no native path-scoped rules: skills arrive on demand"
			if plan.Index {
				reason = "skills Claude reads when the work needs them (skill delivery: relevant)"
			}
			out = append(out, ctxeng.Section{Title: "Skills", Body: plan.Note, Source: "skills:index", Reason: reason, Priority: 36})
		}
		out = append(out, k.c.repoMapSection(ctx, projectID, worktree)) // ext_repomap.go; recorded as left out when it has no body
		return out, nil
	}
	k.c.Context.Files = func(ctx context.Context, projectID, worktree string) ([]ctxeng.ExtraFile, error) {
		t := k.worktreeTarget(ctx, projectID, worktree)
		k.refreshSkills(ctx, projectID)
		plan, err := k.Skills.Plan(ctx, t)
		if err != nil {
			return nil, err
		}
		var out []ctxeng.ExtraFile
		for _, f := range plan.Files {
			f := f
			out = append(out, ctxeng.ExtraFile{
				Path: f.Path, Content: f.Content, Source: "skill:" + strings.Join(f.Skills, ","), Reason: "skill delivered as " + f.Method + " (" + f.Fidelity + ")",
				Done: func(ctx context.Context, written bool) {
					if !written {
						return
					}
					for _, name := range f.Skills {
						k.Skills.RecordDelivery(ctx, t, "", name, f.Hash, f.Method, f.Fidelity, f.Path)
					}
				},
			})
		}
		return out, nil
	}
}

// ---- RPC ----

type knowProject struct {
	ProjectID string `json:"projectId"`
}

func (k *Knowledge) audit(ctx context.Context, action, subject string, detail any) {
	if _, err := k.Audit.Append(ctx, security.AuditEntry{Actor: k.c.Human(), Action: action, Subject: subject, Detail: detail}); err != nil {
		k.c.logf("audit %s: %v", action, err)
	}
}

func (k *Knowledge) methods() map[string]Method {
	h := k.c.Human
	m := map[string]Method{}

	// ---- packs ----
	m["pack.templates"] = bind(func(ctx context.Context, _ struct{}) (any, error) { return packs.Templates() })
	m["pack.list"] = bind(func(ctx context.Context, _ struct{}) (any, error) { return k.Packs.List(ctx) })
	m["pack.get"] = bind(func(ctx context.Context, p idParam) (any, error) { return k.Packs.Get(ctx, p.ID) })
	m["pack.install"] = bind(func(ctx context.Context, p struct {
		YAML string `json:"yaml"`
	}) (any, error) {
		pk, err := k.Packs.Install(ctx, h(), []byte(p.YAML))
		if err == nil {
			k.audit(ctx, "pack.install", pk.ID, map[string]any{"kind": pk.Kind, "visibility": pk.Visibility})
		}
		return pk, err
	})
	m["pack.remove"] = bind(func(ctx context.Context, p idParam) (any, error) {
		err := k.Packs.Remove(ctx, h(), p.ID)
		if err == nil {
			k.audit(ctx, "pack.remove", p.ID, nil)
		}
		return nil, err
	})
	m["pack.refresh"] = bind(func(ctx context.Context, p struct {
		ID    string `json:"id"`
		Force bool   `json:"force"`
	}) (any, error) {
		return k.Packs.Refresh(ctx, h(), p.ID, packs.RefreshOptions{Force: p.Force})
	})
	m["pack.refreshDue"] = bind(func(ctx context.Context, _ struct{}) (any, error) {
		res, errs := k.Packs.RunDue(ctx)
		msgs := map[string]string{}
		for id, e := range errs {
			msgs[id] = e.Error()
		}
		return map[string]any{"refreshed": res, "errors": msgs}, nil
	})
	m["pack.pages"] = bind(func(ctx context.Context, p idParam) (any, error) { return k.Packs.Pages(ctx, p.ID) })
	m["pack.versions"] = bind(func(ctx context.Context, p idParam) (any, error) { return k.Packs.Versions(ctx, p.ID) })
	m["pack.search"] = bind(func(ctx context.Context, p struct {
		PackIDs []string `json:"packIds"`
		Query   string   `json:"query"`
		K       int      `json:"k"`
	}) (any, error) {
		return k.Packs.Search(ctx, packs.SearchRequest{PackIDs: p.PackIDs, Query: p.Query, K: p.K})
	})
	m["pack.consult"] = bind(func(ctx context.Context, p packs.ConsultRequest) (any, error) { return k.Packs.Consult(ctx, p) })
	m["pack.quarantined"] = bind(func(ctx context.Context, p idParam) (any, error) { return k.Packs.Quarantined(ctx, p.ID) })
	m["pack.releaseChunk"] = bind(func(ctx context.Context, p struct {
		ChunkID int64 `json:"chunkId"`
	}) (any, error) {
		err := k.Packs.ReleaseChunk(ctx, h(), p.ChunkID)
		if err == nil {
			k.audit(ctx, "pack.release_chunk", fmt.Sprint(p.ChunkID), nil)
		}
		return nil, err
	})
	m["pack.citation"] = bind(func(ctx context.Context, p struct {
		PackID  string `json:"packId"`
		Version string `json:"version"`
		ChunkID int64  `json:"chunkId"`
	}) (any, error) {
		return k.Packs.ResolveCitation(ctx, p.PackID, p.Version, p.ChunkID)
	})
	type attachParam struct {
		ProjectID string `json:"projectId"`
		PackID    string `json:"packId"`
		AgentID   string `json:"agentId"`
		Role      string `json:"role"`
	}
	m["pack.attach"] = bind(func(ctx context.Context, p attachParam) (any, error) {
		err := k.Packs.Attach(ctx, h(), p.ProjectID, p.PackID, p.AgentID, p.Role)
		if err == nil {
			k.audit(ctx, "pack.attach", p.PackID, p)
		}
		return nil, err
	})
	m["pack.attachDefaults"] = bind(func(ctx context.Context, p attachParam) (any, error) {
		err := k.Packs.AttachDefaults(ctx, h(), p.ProjectID, p.PackID)
		if err == nil {
			k.audit(ctx, "pack.attach", p.PackID, p)
		}
		return nil, err
	})
	m["pack.detach"] = bind(func(ctx context.Context, p attachParam) (any, error) {
		err := k.Packs.Detach(ctx, h(), p.ProjectID, p.PackID, p.AgentID, p.Role)
		if err == nil {
			k.audit(ctx, "pack.detach", p.PackID, p)
		}
		return nil, err
	})
	m["pack.attachments"] = bind(func(ctx context.Context, p knowProject) (any, error) { return k.Packs.Attachments(ctx, p.ProjectID) })

	// ---- requirements ----
	m["requirement.list"] = bind(func(ctx context.Context, p struct {
		ProjectID       string `json:"projectId"`
		IncludeArchived bool   `json:"includeArchived"`
	}) (any, error) {
		return k.Requirements.List(ctx, p.ProjectID, p.IncludeArchived)
	})
	m["requirement.get"] = bind(func(ctx context.Context, p idParam) (any, error) { return k.Requirements.Get(ctx, p.ID) })
	m["requirement.text"] = bind(func(ctx context.Context, p struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}) (any, error) {
		return k.Requirements.Text(ctx, p.ID, p.Version)
	})
	m["requirement.versions"] = bind(func(ctx context.Context, p idParam) (any, error) { return k.Requirements.Versions(ctx, p.ID) })
	m["requirement.add"] = bind(func(ctx context.Context, p requirements.AddReq) (any, error) {
		p.By = h()
		r, err := k.Requirements.Add(ctx, p)
		if err == nil {
			k.audit(ctx, "requirement.add", r.ID, map[string]any{"project": r.ProjectID, "title": r.Title, "source": r.Source})
		}
		return r, err
	})
	m["requirement.update"] = bind(func(ctx context.Context, p struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Format string `json:"format"`
	}) (any, error) {
		r, changed, err := k.Requirements.Update(ctx, h(), p.ID, p.Text, p.Format)
		if err == nil && changed {
			k.audit(ctx, "requirement.update", r.ID, map[string]any{"version": r.Version})
		}
		return map[string]any{"requirement": r, "changed": changed}, err
	})
	m["requirement.archive"] = bind(func(ctx context.Context, p struct {
		ID       string `json:"id"`
		Archived bool   `json:"archived"`
	}) (any, error) {
		err := k.Requirements.Archive(ctx, h(), p.ID, p.Archived)
		if err == nil {
			k.audit(ctx, "requirement.archive", p.ID, map[string]any{"archived": p.Archived})
		}
		return nil, err
	})
	m["requirement.search"] = bind(func(ctx context.Context, p struct {
		ProjectID string `json:"projectId"`
		Query     string `json:"query"`
		DocID     string `json:"docId"`
		K         int    `json:"k"`
	}) (any, error) {
		return k.Requirements.Search(ctx, p.ProjectID, p.Query, p.DocID, p.K)
	})
	m["requirement.citation"] = bind(func(ctx context.Context, p struct {
		Citation string `json:"citation"`
	}) (any, error) {
		return k.Requirements.ResolveCitation(ctx, p.Citation)
	})
	m["requirement.link"] = bind(func(ctx context.Context, p struct {
		RequirementID string   `json:"requirementId"`
		TaskID        string   `json:"taskId"`
		Criteria      []string `json:"criteria"`
	}) (any, error) {
		return k.Requirements.LinkCriteria(ctx, h(), p.RequirementID, p.TaskID, p.Criteria)
	})
	m["requirement.unlink"] = bind(func(ctx context.Context, p struct {
		RequirementID string `json:"requirementId"`
		TaskID        string `json:"taskId"`
	}) (any, error) {
		return nil, k.Requirements.Unlink(ctx, h(), p.RequirementID, p.TaskID)
	})
	m["requirement.links"] = bind(func(ctx context.Context, p struct {
		RequirementID string `json:"requirementId"`
		TaskID        string `json:"taskId"`
	}) (any, error) {
		if p.TaskID != "" {
			return k.Requirements.TaskLinks(ctx, p.TaskID)
		}
		return k.Requirements.Links(ctx, p.RequirementID)
	})
	m["requirement.stale"] = bind(func(ctx context.Context, p knowProject) (any, error) {
		return k.Requirements.StaleLinks(ctx, p.ProjectID)
	})
	m["requirement.trace"] = bind(func(ctx context.Context, p idParam) (any, error) { return k.Requirements.Trace(ctx, p.ID) })
	m["requirement.coverage"] = bind(func(ctx context.Context, p knowProject) (any, error) {
		return k.Requirements.Coverage(ctx, p.ProjectID)
	})
	m["requirement.quarantined"] = bind(func(ctx context.Context, p idParam) (any, error) { return k.Requirements.Quarantined(ctx, p.ID) })
	m["requirement.releaseChunk"] = bind(func(ctx context.Context, p struct {
		ChunkID int64 `json:"chunkId"`
	}) (any, error) {
		err := k.Requirements.ReleaseChunk(ctx, h(), p.ChunkID)
		if err == nil {
			k.audit(ctx, "requirement.release_chunk", fmt.Sprint(p.ChunkID), nil)
		}
		return nil, err
	})

	// ---- milestones ----
	m["milestone.list"] = bind(func(ctx context.Context, p knowProject) (any, error) {
		return k.Requirements.Milestones(ctx, p.ProjectID)
	})
	m["milestone.get"] = bind(func(ctx context.Context, p idParam) (any, error) { return k.Requirements.GetMilestone(ctx, p.ID) })
	m["milestone.create"] = bind(func(ctx context.Context, p requirements.MilestoneReq) (any, error) {
		return k.Requirements.CreateMilestone(ctx, h(), p)
	})
	m["milestone.update"] = bind(func(ctx context.Context, p struct {
		ID string `json:"id"`
		requirements.MilestoneReq
	}) (any, error) {
		return k.Requirements.UpdateMilestone(ctx, h(), p.ID, p.MilestoneReq)
	})
	m["milestone.setStatus"] = bind(func(ctx context.Context, p struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Force  bool   `json:"force"`
	}) (any, error) {
		return k.Requirements.SetMilestoneStatus(ctx, h(), p.ID, p.Status, p.Force)
	})
	m["milestone.setTasks"] = bind(func(ctx context.Context, p struct {
		ID     string   `json:"id"`
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}) (any, error) {
		return k.Requirements.SetMilestoneTasks(ctx, h(), p.ID, p.Add, p.Remove)
	})
	m["milestone.delete"] = bind(func(ctx context.Context, p idParam) (any, error) {
		return nil, k.Requirements.DeleteMilestone(ctx, h(), p.ID)
	})

	// ---- skills ----
	type skillRef struct {
		ProjectID string `json:"projectId"`
		Name      string `json:"name"`
	}
	m["skill.list"] = bind(func(ctx context.Context, p struct {
		ProjectID string   `json:"projectId"`
		Statuses  []string `json:"statuses"`
	}) (any, error) {
		return k.Skills.List(ctx, p.ProjectID, p.Statuses...)
	})
	m["skill.get"] = bind(func(ctx context.Context, p skillRef) (any, error) { return k.Skills.Get(ctx, p.ProjectID, p.Name) })
	m["skill.scan"] = bind(func(ctx context.Context, p knowProject) (any, error) { return k.Skills.ScanRepo(ctx, p.ProjectID) })
	m["skill.registries"] = bind(func(ctx context.Context, _ struct{}) (any, error) { return k.Skills.Registries(ctx) })
	m["skill.registryAdd"] = bind(func(ctx context.Context, p struct {
		Registry     skills.Registry `json:"registry"`
		Acknowledged bool            `json:"acknowledged"`
	}) (any, error) {
		r, err := k.Skills.AddRegistry(ctx, h(), p.Registry, p.Acknowledged)
		if err == nil {
			k.audit(ctx, "skill.registry_add", r.ID, map[string]any{"repo": r.Repo, "root": r.Root})
		}
		return r, err
	})
	m["skill.registryEnable"] = bind(func(ctx context.Context, p struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}) (any, error) {
		err := k.Skills.SetRegistryEnabled(ctx, h(), p.ID, p.Enabled)
		if err == nil {
			k.audit(ctx, "skill.registry_enable", p.ID, map[string]any{"enabled": p.Enabled})
		}
		return nil, err
	})
	m["skill.registryRemove"] = bind(func(ctx context.Context, p idParam) (any, error) {
		err := k.Skills.RemoveRegistry(ctx, h(), p.ID)
		if err == nil {
			k.audit(ctx, "skill.registry_remove", p.ID, nil)
		}
		return nil, err
	})
	m["skill.candidates"] = bind(func(ctx context.Context, p struct {
		RegistryID string `json:"registryId"`
		Ref        string `json:"ref"`
	}) (any, error) {
		commit, cands, err := k.Skills.RegistryCandidates(ctx, h(), p.RegistryID, p.Ref)
		return map[string]any{"commit": commit, "skills": cands}, err
	})
	m["skill.import"] = bind(func(ctx context.Context, p skills.ImportReq) (any, error) {
		s, err := k.Skills.Import(ctx, h(), p)
		if err == nil {
			k.audit(ctx, "skill.import", p.Name, map[string]any{"registry": p.RegistryID, "commit": s.PinnedCommit, "status": s.Status})
		}
		return s, err
	})
	decide := func(action string, f func(ctx context.Context, by, projectID, name string) (skills.Skill, error)) Method {
		return bind(func(ctx context.Context, p skillRef) (any, error) {
			s, err := f(ctx, h(), p.ProjectID, p.Name)
			if err == nil {
				k.audit(ctx, "skill."+action, p.Name, map[string]any{"project": p.ProjectID, "origin": s.Origin, "commit": s.PinnedCommit, "hash": s.ContentHash})
			}
			return s, err
		})
	}
	// who gets a skill: give it to one agent or take it away (skill.assign), or set the whole audience at once (skill.audience):
	// the whole team (the roles it names, plus the agents given it) or only the chosen agents
	m["skill.assign"] = bind(func(ctx context.Context, p struct {
		ProjectID string `json:"projectId"`
		Name      string `json:"name"`
		AgentID   string `json:"agentId"`
		On        bool   `json:"on"`
	}) (any, error) {
		s, err := k.Skills.Assign(ctx, h(), p.ProjectID, p.Name, p.AgentID, p.On)
		if err == nil {
			k.audit(ctx, "skill.assign", p.Name, map[string]any{"project": p.ProjectID, "agent": p.AgentID, "on": p.On})
		}
		return s, err
	})
	m["skill.audience"] = bind(func(ctx context.Context, p struct {
		ProjectID string   `json:"projectId"`
		Name      string   `json:"name"`
		Audience  string   `json:"audience"`
		Agents    []string `json:"agents"`
	}) (any, error) {
		s, err := k.Skills.SetAudience(ctx, h(), p.ProjectID, p.Name, p.Audience, p.Agents)
		if err == nil {
			k.audit(ctx, "skill.audience", p.Name, map[string]any{"project": p.ProjectID, "audience": s.Audience, "agents": s.Agents})
		}
		return s, err
	})
	m["skill.accept"] = decide("accept", k.Skills.Accept)
	m["skill.reject"] = decide("reject", k.Skills.Reject)
	m["skill.release"] = decide("release", k.Skills.Release)
	m["skill.deliveries"] = bind(func(ctx context.Context, p struct {
		ProjectID string `json:"projectId"`
		TaskID    string `json:"taskId"`
	}) (any, error) {
		return k.Skills.Deliveries(ctx, p.ProjectID, p.TaskID)
	})
	m["skill.coverage"] = bind(func(ctx context.Context, p struct {
		ProjectID string `json:"projectId"`
		TaskID    string `json:"taskId"`
	}) (any, error) {
		return k.Skills.TaskCoverage(ctx, p.ProjectID, p.TaskID)
	})
	m["skill.plan"] = bind(func(ctx context.Context, p skills.Target) (any, error) {
		return k.Skills.Plan(ctx, k.projectTarget(ctx, p)) // the preview shows what a run of this project gets
	})

	// ---- memory ----
	m["memory.list"] = bind(func(ctx context.Context, p struct {
		ProjectID string `json:"projectId"`
		Status    string `json:"status"`
		Kind      string `json:"kind"`
	}) (any, error) {
		es, err := k.Memory.List(ctx, p.ProjectID, p.Status, p.Kind)
		if es == nil {
			es = []memory.Entry{}
		}
		return es, err
	})
	m["memory.get"] = bind(func(ctx context.Context, p idParam) (any, error) { return k.Memory.Get(ctx, p.ID) })
	m["memory.search"] = bind(func(ctx context.Context, p struct {
		ProjectID string `json:"projectId"` // empty: every project
		Query     string `json:"query"`
		Status    string `json:"status"`
		K         int    `json:"k"`
	}) (any, error) {
		es, err := k.Memory.Find(ctx, p.ProjectID, p.Query, p.Status, p.K)
		if es == nil {
			es = []memory.Entry{}
		}
		return es, err
	})
	m["memory.propose"] = bind(func(ctx context.Context, p struct {
		ProjectID string `json:"projectId"`
		Kind      string `json:"kind"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		Source    string `json:"source"`
	}) (any, error) {
		return k.ProposeMemory(ctx, p.ProjectID, p.Kind, p.Title, p.Body, firstNonEmpty(p.Source, h()))
	})
	m["memory.accept"] = bind(func(ctx context.Context, p idParam) (any, error) {
		return k.DecideMemory(ctx, h(), p.ID, memory.Accepted)
	})
	m["memory.reject"] = bind(func(ctx context.Context, p idParam) (any, error) {
		return k.DecideMemory(ctx, h(), p.ID, memory.RejectedSt)
	})
	m["memory.delete"] = bind(func(ctx context.Context, p idParam) (any, error) {
		if err := security.RequireHuman(h()); err != nil {
			return nil, err
		}
		e, err := k.Memory.Get(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		if err := k.Memory.Delete(ctx, p.ID); err != nil {
			return nil, err
		}
		k.audit(ctx, "memory.delete", p.ID, map[string]any{"project": e.ProjectID, "kind": e.Kind, "status": e.Status})
		k.c.publish(ctx, "memory.deleted", e.ProjectID, map[string]any{"id": e.ID, "kind": e.Kind})
		return nil, nil
	})
	return m
}

// ProposeMemory stores a proposal (never accepted by its author). Secrets are redacted: memory feeds generated instruction
// files, which agents read.
func (k *Knowledge) ProposeMemory(ctx context.Context, projectID, kind, title, body, source string) (memory.Entry, error) {
	if !memory.ValidKind(kind) {
		return memory.Entry{}, fmt.Errorf("memory: kind must be one of decision, convention, api_contract, rejected, known_issue, preference")
	}
	if _, err := k.c.GetProject(ctx, projectID); err != nil {
		return memory.Entry{}, err
	}
	e := memory.Normalize(memory.Entry{ProjectID: projectID, Kind: kind, Title: title, Body: body, Source: source})
	e.Title, e.Body = k.c.Scanner.Redact(e.Title), k.c.Scanner.Redact(e.Body)
	e, err := k.Memory.Add(ctx, e, false)
	if err != nil {
		return e, err
	}
	k.c.publish(ctx, "memory.proposed", projectID, map[string]any{"id": e.ID, "kind": e.Kind, "title": e.Title, "source": e.Source})
	return e, nil
}

// DecideMemory accepts or rejects a proposal. Only a human principal can; an accepted entry reaches the generated
// instruction files of every later task.
func (k *Knowledge) DecideMemory(ctx context.Context, by, id, status string) (memory.Entry, error) {
	if err := security.RequireHuman(by); err != nil {
		return memory.Entry{}, err
	}
	e, err := k.Memory.Get(ctx, id)
	if err != nil {
		return e, err
	}
	if e.Status != memory.Proposed && !(status == memory.Accepted && e.Status == memory.RejectedSt) {
		return e, fmt.Errorf("memory: %s is %s and cannot become %s", id, e.Status, status)
	}
	if err := k.Memory.SetStatus(ctx, id, status); err != nil {
		return e, err
	}
	k.audit(ctx, "memory."+map[string]string{memory.Accepted: "accept", memory.RejectedSt: "reject"}[status], id, map[string]any{"project": e.ProjectID, "kind": e.Kind})
	k.c.publish(ctx, "memory."+map[string]string{memory.Accepted: "accepted", memory.RejectedSt: "rejected"}[status], e.ProjectID, map[string]any{"id": id, "kind": e.Kind, "by": by})
	return k.Memory.Get(ctx, id)
}

// libraryVersionsSection lists the versions of the SDKs and frameworks the worktree's manifests and lockfiles pin, so the
// agent writes code for them and asks documentation for them (decision P54). A worktree whose files cannot be read, or that
// uses none of the curated libraries, gets no section.
func libraryVersionsSection(worktree string) (ctxeng.Section, bool) {
	r, err := libver.Scan(worktree, libver.Options{})
	if err != nil || r.Libs() == 0 {
		return ctxeng.Section{}, false
	}
	return ctxeng.Section{Title: "Library versions", Body: r.Section(), Source: "libver:" + strings.Join(r.Sources(), ","),
		Reason: fmt.Sprintf("%d SDK and framework versions read from the repository's manifests and lockfiles", r.Libs()), Priority: 34}, true
}
