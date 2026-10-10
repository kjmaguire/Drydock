package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/agentcatalog"
	"github.com/kjmaguire/drydock/internal/skills"
)

// Prebuilt agents (decision P27): the embedded catalog of ready team members (internal/agentcatalog) dropped into a project.
// Applying one creates an ordinary agent through CreateAgent and, when the human asks for it, connects the entry's MCP catalog
// servers in the library and grants them to the agent, and installs the entry's registry skills at their pinned commits and
// assigns them to the agent. Because the human explicitly chose this prebuilt agent with "install skills", a skill that passes
// the license check and the scan is accepted on that choice; a blocked skill stays blocked and is reported. Nothing here runs
// on its own: every method is a human action, and a failure on one skill or server is reported without undoing the rest.

func init() {
	RegisterExtension(Extension{Name: "agentcatalog", After: []string{"agentmgmt", "mcpservers", "mcpcatalog", "knowledge"}, Start: startAgentCatalog})
}

// PrebuiltAgent is a catalog entry with what is already in place on this machine (and in a project, when one is given).
type PrebuiltAgent struct {
	agentcatalog.Entry
	Availability PrebuiltAvailability `json:"availability"`
}

// PrebuiltAvailability says what applying the entry would find.
type PrebuiltAvailability struct {
	// Adapters are the preferred CLI first, then the alternatives, with the Connections state of each.
	Adapters []AdapterAvailability `json:"adapters"`
	// PreferredUsable is true when the preferred CLI is installed and usable.
	PreferredUsable bool                 `json:"preferredUsable"`
	MCP             []PrebuiltMCPState   `json:"mcp"`
	Skills          []PrebuiltSkillState `json:"skills"`
}

// AdapterAvailability is one CLI's Connections state.
type AdapterAvailability struct {
	Adapter   string `json:"adapter"`
	Preferred bool   `json:"preferred"`
	Installed bool   `json:"installed"`
	Usable    bool   `json:"usable"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

// PrebuiltMCPState is one MCP catalog server of the entry: whether the library already has it.
type PrebuiltMCPState struct {
	ID        string `json:"id"`
	InLibrary bool   `json:"inLibrary"`
	LibraryID string `json:"libraryId,omitempty"`
}

// PrebuiltSkillState is one skill of the entry: whether the project already has it (only with a projectId).
type PrebuiltSkillState struct {
	agentcatalog.SkillRef
	Imported bool   `json:"imported"`
	Status   string `json:"status,omitempty"` // offered | accepted | rejected | blocked
	// SamePin is true when the project's copy is pinned to the entry's commit.
	SamePin bool `json:"samePin"`
}

// PrebuiltApplyReq applies one entry. Empty fields take the entry's values (for another adapter: the listed alternative's, or
// the CLI's defaults).
type PrebuiltApplyReq struct {
	ID             string `json:"id"`
	ProjectID      string `json:"projectId"`
	Name           string `json:"name"`
	Adapter        string `json:"adapter"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permissionMode"`
	InstallSkills  bool   `json:"installSkills"`
	ConnectMCP     bool   `json:"connectMcp"`
}

// PrebuiltApplied is what applying an entry did.
type PrebuiltApplied struct {
	ID     string          `json:"id"`
	Agent  Agent           `json:"agent"`
	MCP    MCPGrantResult  `json:"mcp"`
	Skills SkillsInstalled `json:"skills"`
	// Notes are things the human should know (an MCP server still needs a sign-in, a CLI that gets MCP only when allowed, ...).
	Notes []string `json:"notes"`
}

// MCPGrantResult lists the library servers granted to the agent and the catalog servers that were not.
type MCPGrantResult struct {
	Granted []string  `json:"granted"`
	Skipped []Skipped `json:"skipped"`
}

// SkillsInstalled is the outcome of installing skills for one agent.
type SkillsInstalled struct {
	// Assigned skills are accepted and assigned to the agent: they reach it from its next run.
	Assigned []string `json:"assigned"`
	// Shared skills were already accepted in the project for every agent whose role matches, this one included; they were
	// left as they were so the rest of the team keeps them.
	Shared []string `json:"shared"`
	// Pending skills are in the project and given to the agent but wait for the human: blocked by the scan or rejected earlier.
	Pending []Skipped `json:"pending"`
	// Failed skills could not be installed (network, registry, license, a repository skill of the same name).
	Failed []Skipped `json:"failed"`
}

// Skipped names an item and why it was not done.
type Skipped struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason"`
}

// PrebuiltApplyManyReq applies several entries into one project (the new-project wizard).
type PrebuiltApplyManyReq struct {
	ProjectID     string              `json:"projectId"`
	Items         []PrebuiltApplyItem `json:"items"`
	InstallSkills bool                `json:"installSkills"`
	ConnectMCP    bool                `json:"connectMcp"`
}

// PrebuiltApplyItem is one entry of applyMany.
type PrebuiltApplyItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Adapter string `json:"adapter"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

// PrebuiltApplyManyResult is one entry's outcome in applyMany: Applied, or Error.
type PrebuiltApplyManyResult struct {
	ID      string           `json:"id"`
	Applied *PrebuiltApplied `json:"applied,omitempty"`
	Error   string           `json:"error,omitempty"`
}

func startAgentCatalog(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	if _, err := agentcatalog.Builtin(); err != nil {
		return nil, nil, err
	}
	return map[string]Method{
		"agentcatalog.list": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return c.prebuiltList(ctx, p.ProjectID)
		}),
		"agentcatalog.apply": bind(func(ctx context.Context, p PrebuiltApplyReq) (any, error) { return c.prebuiltApply(ctx, p) }),
		"agentcatalog.applyMany": bind(func(ctx context.Context, p PrebuiltApplyManyReq) (any, error) {
			return c.prebuiltApplyMany(ctx, p)
		}),
	}, nil, nil
}

func (c *Core) prebuiltList(ctx context.Context, projectID string) ([]PrebuiltAgent, error) {
	all, err := agentcatalog.Builtin()
	if err != nil {
		return nil, err
	}
	var have map[string]skills.Skill
	if projectID != "" {
		if _, err := c.GetProject(ctx, projectID); err != nil {
			return nil, err
		}
		have = map[string]skills.Skill{}
		if k := c.Knowledge(); k != nil {
			list, err := k.Skills.List(ctx, projectID)
			if err != nil {
				return nil, err
			}
			for _, s := range list {
				have[s.Name] = s
			}
		}
	}
	conns := map[string]AdapterAvailability{}
	adapterState := func(id string) AdapterAvailability {
		if a, ok := conns[id]; ok {
			return a
		}
		a := AdapterAvailability{Adapter: id, Status: "unknown"}
		if conn, err := c.Connections.Check(ctx, id); err == nil {
			a.Installed, a.Usable, a.Status, a.Reason = conn.Installed, conn.Usable, string(conn.Status), conn.Reason
		} else {
			a.Reason = err.Error()
		}
		conns[id] = a
		return a
	}
	libByURL := map[string]string{}
	cat, err := loadBuiltinCatalog()
	if err != nil {
		return nil, err
	}
	out := make([]PrebuiltAgent, 0, len(all))
	for _, e := range all {
		av := PrebuiltAvailability{Adapters: []AdapterAvailability{}, MCP: []PrebuiltMCPState{}, Skills: []PrebuiltSkillState{}}
		pref := adapterState(e.Preferred.Adapter)
		pref.Preferred = true
		av.Adapters = append(av.Adapters, pref)
		av.PreferredUsable = pref.Installed && pref.Usable
		for _, alt := range e.Alternatives {
			av.Adapters = append(av.Adapters, adapterState(alt.Adapter))
		}
		for _, id := range e.MCP {
			st := PrebuiltMCPState{ID: id}
			if s, ok := cat.Get(id); ok {
				lib, seen := libByURL[s.URL]
				if !seen {
					if lib, err = c.libraryByURL(ctx, s.URL); err != nil {
						return nil, err
					}
					libByURL[s.URL] = lib
				}
				st.InLibrary, st.LibraryID = lib != "", lib
			}
			av.MCP = append(av.MCP, st)
		}
		for _, ref := range e.Skills {
			st := PrebuiltSkillState{SkillRef: ref}
			if k, ok := have[ref.Name]; ok && k.Origin == skills.OriginRegistry && k.RegistryID == ref.Registry {
				st.Imported, st.Status, st.SamePin = true, k.Status, k.PinnedCommit == ref.Ref
			}
			av.Skills = append(av.Skills, st)
		}
		out = append(out, PrebuiltAgent{Entry: e, Availability: av})
	}
	return out, nil
}

// uniqueAgentName returns name, or name with a number appended when an agent of the project already has it.
func (c *Core) uniqueAgentName(ctx context.Context, projectID, name string) (string, error) {
	team, err := c.Agents(ctx, projectID)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, m := range team {
		taken[strings.ToLower(m.Name)] = true
	}
	final := name
	for n := 2; taken[strings.ToLower(final)]; n++ {
		final = fmt.Sprintf("%s %d", name, n)
	}
	return final, nil
}

func (c *Core) prebuiltApply(ctx context.Context, p PrebuiltApplyReq) (PrebuiltApplied, error) {
	e, ok := agentcatalog.Get(strings.TrimSpace(p.ID))
	if !ok {
		return PrebuiltApplied{}, fmt.Errorf("there is no prebuilt agent %q", p.ID)
	}
	adapter := strings.TrimSpace(p.Adapter)
	if adapter == "" {
		adapter = e.Preferred.Adapter
	}
	model, effort, _ := e.Setup(adapter)
	if m := strings.TrimSpace(p.Model); m != "" {
		model = m
	}
	if x := strings.TrimSpace(p.Effort); x != "" {
		effort = x
	}
	mode := strings.TrimSpace(p.PermissionMode)
	if mode == "" && adapter == e.Preferred.Adapter {
		mode = e.Preferred.PermissionMode
	}
	// the working-practices text was written for, and measured against, Claude (decision P17)
	practices := adapter == "claude" && e.Preferred.PracticesOn()
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = e.Name
	}
	final, err := c.uniqueAgentName(ctx, p.ProjectID, name)
	if err != nil {
		return PrebuiltApplied{}, err
	}
	ag, err := c.CreateAgent(ctx, AgentSpec{ProjectID: p.ProjectID, Name: final, Adapter: adapter, Role: e.Role, Model: model, Effort: effort,
		PermissionMode: mode, Purpose: e.Purpose, Practices: &practices})
	if err != nil {
		return PrebuiltApplied{}, err
	}
	res := PrebuiltApplied{ID: e.ID, Agent: ag, MCP: MCPGrantResult{Granted: []string{}, Skipped: []Skipped{}}, Skills: newSkillsInstalled(), Notes: []string{}}
	if len(e.MCP) > 0 {
		if p.ConnectMCP {
			res.MCP, res.Notes = c.grantCatalogServers(ctx, ag, e.MCP, res.Notes)
		} else {
			for _, id := range e.MCP {
				res.MCP.Skipped = append(res.MCP.Skipped, Skipped{ID: id, Reason: "not connected: you chose not to connect MCP servers"})
			}
		}
	}
	if len(e.Skills) > 0 {
		if p.InstallSkills {
			res.Skills = c.installSkillsFor(ctx, ag, e.Skills, "prebuilt agent "+e.ID)
		} else {
			for _, s := range e.Skills {
				res.Skills.Pending = append(res.Skills.Pending, Skipped{Name: s.Name, Reason: "not installed: you chose not to install skills"})
			}
		}
	}
	c.publish(ctx, "agentcatalog.applied", p.ProjectID, map[string]any{"entry": e.ID, "agent": ag.ID, "adapter": adapter, "skills": len(res.Skills.Assigned),
		"mcp": len(res.MCP.Granted), "by": c.Human()})
	return res, nil
}

func (c *Core) prebuiltApplyMany(ctx context.Context, p PrebuiltApplyManyReq) ([]PrebuiltApplyManyResult, error) {
	if _, err := c.GetProject(ctx, p.ProjectID); err != nil {
		return nil, err
	}
	if len(p.Items) > 50 {
		return nil, errors.New("apply at most 50 prebuilt agents at once")
	}
	out := make([]PrebuiltApplyManyResult, 0, len(p.Items))
	for _, it := range p.Items {
		r := PrebuiltApplyManyResult{ID: it.ID}
		applied, err := c.prebuiltApply(ctx, PrebuiltApplyReq{ID: it.ID, ProjectID: p.ProjectID, Name: it.Name, Adapter: it.Adapter, Model: it.Model, Effort: it.Effort,
			InstallSkills: p.InstallSkills, ConnectMCP: p.ConnectMCP})
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Applied = &applied
		}
		out = append(out, r)
	}
	return out, nil
}

// grantCatalogServers adds each catalog server to the library (or finds it there) and grants it to the agent in its project.
func (c *Core) grantCatalogServers(ctx context.Context, ag Agent, ids []string, notes []string) (MCPGrantResult, []string) {
	res := MCPGrantResult{Granted: []string{}, Skipped: []Skipped{}}
	svc := c.mcpServerSvc()
	for _, id := range ids {
		if svc == nil {
			res.Skipped = append(res.Skipped, Skipped{ID: id, Reason: "MCP servers are not available"})
			continue
		}
		added, err := c.addMCPFromCatalog(ctx, id)
		if err != nil {
			res.Skipped = append(res.Skipped, Skipped{ID: id, Reason: err.Error()})
			continue
		}
		if _, err := svc.grant(ctx, ag.ProjectID, added.Server.ID, ag.ID, true); err != nil {
			res.Skipped = append(res.Skipped, Skipped{ID: id, Reason: err.Error()})
			continue
		}
		res.Granted = append(res.Granted, added.Server.ID)
		if added.Next != "" {
			notes = append(notes, added.Next)
		}
	}
	if len(res.Granted) > 0 && ag.Adapter != "claude" && !c.mcpTryAllowed(ctx, ag.Adapter) {
		notes = append(notes, fmt.Sprintf("%s reaches MCP servers only after you allow MCP for that CLI (Settings, MCP); the grants are in place for when you do", ag.Adapter))
	}
	return res, notes
}

func newSkillsInstalled() SkillsInstalled {
	return SkillsInstalled{Assigned: []string{}, Shared: []string{}, Pending: []Skipped{}, Failed: []Skipped{}}
}

// installSkillsFor imports each registry skill into the agent's project at its pinned ref and assigns it to the agent. The
// human chose these skills by choosing the agent (a prebuilt agent, or a template they saved), so a skill the import leaves
// "offered" (license recorded, scan passed) is accepted on that choice. A blocked skill stays blocked, a skill the human
// rejected earlier stays rejected, and both are reported as pending. A skill the project already delivers by role to the whole
// team is left unassigned, so adding one agent never takes a skill away from the others.
func (c *Core) installSkillsFor(ctx context.Context, ag Agent, refs []agentcatalog.SkillRef, via string) SkillsInstalled {
	res := newSkillsInstalled()
	k := c.Knowledge()
	for _, ref := range refs {
		if k == nil {
			res.Failed = append(res.Failed, Skipped{Name: ref.Name, Reason: "skills are not available"})
			continue
		}
		before, err := k.Skills.Get(ctx, ag.ProjectID, ref.Name)
		existed := err == nil
		switch {
		case err != nil && !errors.Is(err, skills.ErrNotFound):
			res.Failed = append(res.Failed, Skipped{Name: ref.Name, Reason: err.Error()})
			continue
		case existed && before.Origin != skills.OriginRegistry:
			res.Failed = append(res.Failed, Skipped{Name: ref.Name, Reason: "the repository has its own skill with this name; it was not replaced"})
			continue
		case existed && before.RegistryID != ref.Registry:
			res.Failed = append(res.Failed, Skipped{Name: ref.Name, Reason: fmt.Sprintf("the project already has a skill with this name from %s", before.RegistryID)})
			continue
		}
		cur := before
		if !existed || before.PinnedCommit != ref.Ref || ref.Ref == "" {
			cur, err = k.Skills.Import(ctx, c.Human(), skills.ImportReq{ProjectID: ag.ProjectID, RegistryID: ref.Registry, Name: ref.Name, Ref: ref.Ref})
			if err != nil {
				res.Failed = append(res.Failed, Skipped{Name: ref.Name, Reason: c.scrubSecrets(err.Error())})
				continue
			}
			k.audit(ctx, "skill.import", ref.Name, map[string]any{"registry": ref.Registry, "commit": cur.PinnedCommit, "status": cur.Status, "via": via})
		}
		if cur.Status == skills.StatusOffered {
			if acc, err := k.Skills.Accept(ctx, c.Human(), ag.ProjectID, ref.Name); err == nil {
				cur = acc
				k.audit(ctx, "skill.accept", ref.Name, map[string]any{"project": ag.ProjectID, "origin": cur.Origin, "commit": cur.PinnedCommit, "hash": cur.ContentHash, "via": via})
			} else {
				res.Pending = append(res.Pending, Skipped{Name: ref.Name, Reason: err.Error()})
				continue
			}
		}
		// Who gets it is left as the project has it whenever the skill already reached agents: a whole-team skill whose roles
		// match the newcomer is shared as it is, and otherwise the newcomer is given it as well, which takes it from nobody. A
		// skill that reached nobody before this choice (new to the project, or never accepted) comes with this agent, so it goes
		// to this agent (and to any agent it was already given to); the human can give it to the team on the Skills page.
		reached := existed && before.Status == skills.StatusAccepted
		switch {
		case reached && cur.Audience != skills.AudienceAgents && cur.Status == skills.StatusAccepted && skills.RolesMatch(cur.Roles, ag.Role):
			res.Shared = append(res.Shared, ref.Name)
			continue
		case reached || cur.Audience == skills.AudienceAgents:
			if cur, err = k.Skills.Assign(ctx, c.Human(), ag.ProjectID, ref.Name, ag.ID, true); err != nil {
				res.Failed = append(res.Failed, Skipped{Name: ref.Name, Reason: err.Error()})
				continue
			}
			k.audit(ctx, "skill.assign", ref.Name, map[string]any{"project": ag.ProjectID, "agent": ag.ID, "on": true, "via": via})
		default:
			given := append(append([]string{}, cur.Agents...), ag.ID)
			if cur, err = k.Skills.SetAudience(ctx, c.Human(), ag.ProjectID, ref.Name, skills.AudienceAgents, given); err != nil {
				res.Failed = append(res.Failed, Skipped{Name: ref.Name, Reason: err.Error()})
				continue
			}
			k.audit(ctx, "skill.audience", ref.Name, map[string]any{"project": ag.ProjectID, "audience": skills.AudienceAgents, "agents": cur.Agents, "via": via})
		}
		switch cur.Status {
		case skills.StatusAccepted:
			res.Assigned = append(res.Assigned, ref.Name)
		case skills.StatusBlocked:
			res.Pending = append(res.Pending, Skipped{Name: ref.Name, Reason: "blocked: " + cur.BlockReason + "; it stays blocked until you review it on the Skills page"})
		case skills.StatusRejected:
			res.Pending = append(res.Pending, Skipped{Name: ref.Name, Reason: "you rejected this skill earlier; accept it on the Skills page to use it"})
		default:
			res.Pending = append(res.Pending, Skipped{Name: ref.Name, Reason: "waiting for your decision on the Skills page (" + cur.Status + ")"})
		}
	}
	return res
}
