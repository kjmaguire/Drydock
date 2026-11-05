package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SetupRequest is what the new-project wizard collected. One call makes the whole project: repository (none, a local folder, or
// a GitHub clone), brief, delivery mode, connectors and team. Every step after the project exists is best effort: a step that
// fails becomes a warning and the rest still runs, so the human gets a usable project and a list of what to fix.
type SetupRequest struct {
	Name  string    `json:"name"`
	Brief string    `json:"brief"`
	Repo  SetupRepo `json:"repo"`
	Mode  string    `json:"mode"` // local_merge (default) | pr_flow
	// LibraryServers are MCP library server ids to put into the project; CatalogServers are built-in catalog ids (cfdocs,
	// mslearn...) that are added to the library first.
	LibraryServers []string     `json:"libraryServers"`
	CatalogServers []string     `json:"catalogServers"`
	Agents         []SetupAgent `json:"agents"`
	// InstallSkills imports and assigns the skills of the prebuilt agents and templates chosen.
	InstallSkills bool `json:"installSkills"`
}

// SetupRepo picks the project's repository.
type SetupRepo struct {
	Kind string `json:"kind"` // none | local | github
	Path string `json:"path"` // local
	// Init makes a Git repository at Path first when there is none (repo.init: the folder when missing, branch main, a first
	// commit); a folder that is already a repository is used as it is (decision P58).
	Init     bool   `json:"init"`
	GitHub   string `json:"github"`   // github: owner/name
	CloneDir string `json:"cloneDir"` // github: parent folder (default DefaultCloneDir)
	// Later makes the project at once and attaches the clone when it lands (github only, B12): the answer carries Landing
	// instead of Clone, and the project has no repository until then (project_clone_landing.go).
	Later bool `json:"later"`
}

// SetupAgent is one team member: a prebuilt agent, a saved template, or a custom agent.
type SetupAgent struct {
	Prebuilt string `json:"prebuilt"`
	Template string `json:"template"`

	Name           string `json:"name"`
	Role           string `json:"role"`
	Adapter        string `json:"adapter"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permissionMode"`
	Purpose        string `json:"purpose"`
	Practices      *bool  `json:"practices"`
	// Servers are library or catalog server ids granted to a custom agent (they are put into the project too).
	Servers []string `json:"servers"`
	// ConfirmSkipsApprovals is the person's explicit confirmation of a permission mode that lets the agent act without asking.
	ConfirmSkipsApprovals bool `json:"confirmSkipsApprovals,omitempty"`
}

// SetupResult is the new project and what happened.
type SetupResult struct {
	Project Project      `json:"project"`
	Agents  []Agent      `json:"agents"`
	Clone   *CloneResult `json:"clone,omitempty"`
	// Landing is the clone the project waits for (repo.later): it is attached when it lands.
	Landing  *LandingClone `json:"landing,omitempty"`
	Warnings []string      `json:"warnings"`
	// Problems are the same warnings, one each, with what kind of thing failed, so the project's Work page can show each as
	// its own line with the action that fixes it (B2). Warnings stays for older readers.
	Problems []SetupWarning `json:"problems"`
}

// SetupWarning is one thing that failed after the project was made.
type SetupWarning struct {
	// Kind is cli (an agent's CLI is not usable: Adapter and Status say which and why), decision (something waits for the
	// person's decision), connector (a connector was not added or granted) or other.
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Adapter string `json:"adapter,omitempty"`
	Status  string `json:"status,omitempty"`
}

// MaxSetupAgents bounds one wizard run.
const MaxSetupAgents = 20

// SetupProject runs the wizard's request.
func (c *Core) SetupProject(ctx context.Context, r SetupRequest) (SetupResult, error) {
	res := SetupResult{Agents: []Agent{}, Warnings: []string{}, Problems: []SetupWarning{}}
	if err := c.RequireLicensed(ctx, "create_project"); err != nil {
		return res, err
	}
	if len(r.Agents) > MaxSetupAgents {
		return res, fmt.Errorf("at most %d agents in one go (add more from the Team page afterwards)", MaxSetupAgents)
	}
	if len(strings.TrimSpace(r.Brief)) > MaxBriefLen {
		return res, fmt.Errorf("the project brief is at most %d characters", MaxBriefLen)
	}
	if r.Mode != "" && r.Mode != "local_merge" && r.Mode != "pr_flow" {
		return res, fmt.Errorf("mode must be local_merge or pr_flow, not %q", r.Mode)
	}
	for i, a := range r.Agents {
		if (a.Prebuilt != "") && (a.Template != "") {
			return res, fmt.Errorf("agent %d: choose a prebuilt agent or a template, not both", i+1)
		}
		if a.Prebuilt == "" && a.Template == "" && (strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Role) == "" || a.Adapter == "") {
			return res, fmt.Errorf("agent %d needs a name, a role and an agent CLI", i+1)
		}
	}
	repoPath := ""
	var later struct{ repo, parent string }
	switch r.Repo.Kind {
	case "", "none":
		if strings.TrimSpace(r.Name) == "" {
			return res, errors.New("a project without a repository needs a name")
		}
	case "local":
		if strings.TrimSpace(r.Repo.Path) == "" {
			return res, errors.New("choose the local repository folder")
		}
		repoPath = r.Repo.Path
		if r.Repo.Init && c.CheckRepoFolder(ctx, repoPath).State != FolderRepository {
			if _, err := c.InitRepository(ctx, repoPath, c.Human()); err != nil {
				return res, err
			}
		}
	case "github":
		if r.Repo.Later {
			repo, parent, err := c.checkLaterClone(r.Repo.GitHub, r.Repo.CloneDir)
			if err != nil {
				return res, err
			}
			later.repo, later.parent = repo, parent
			if strings.TrimSpace(r.Name) == "" {
				r.Name = repo[strings.IndexByte(repo, '/')+1:]
			}
			break
		}
		cl, err := c.GitHubClone(ctx, r.Repo.GitHub, r.Repo.CloneDir)
		if err != nil {
			return res, err
		}
		res.Clone = &cl
		repoPath = cl.Path
	default:
		return res, fmt.Errorf("repository kind must be none, local or github, not %q", r.Repo.Kind)
	}
	p, err := c.CreateProject(ctx, r.Name, repoPath)
	if err != nil {
		if res.Clone != nil {
			return res, fmt.Errorf("%w (the repository was cloned to %s; choose it as a local repository to try again)", err, res.Clone.Path)
		}
		return res, err
	}
	res.Project = p
	if r.Repo.Kind == "local" && r.Repo.Init {
		c.rememberCodeDir(ctx, repoPath)
	}
	warnAs := func(w SetupWarning, format string, a ...any) {
		w.Text = fmt.Sprintf(format, a...)
		res.Warnings = append(res.Warnings, w.Text)
		res.Problems = append(res.Problems, w)
	}
	warn := func(format string, a ...any) { warnAs(SetupWarning{Kind: "other"}, format, a...) }
	connWarn := func(format string, a ...any) { warnAs(SetupWarning{Kind: "connector"}, format, a...) }
	if strings.TrimSpace(r.Brief) != "" {
		if p2, err := c.SetBrief(ctx, p.ID, r.Brief); err != nil {
			warn("brief not saved: %v", err)
		} else {
			res.Project = p2
		}
	}
	if r.Mode == "pr_flow" {
		if p2, err := c.UpdateProject(ctx, p.ID, "pr_flow", ""); err != nil {
			warn("pull-request delivery not switched on: %v", err)
		} else {
			res.Project = p2
		}
	}
	call := func(method string, params, out any) error {
		b, _ := json.Marshal(params)
		raw, err := c.Call(ctx, method, b)
		if err != nil || out == nil {
			return err
		}
		return json.Unmarshal(raw, out)
	}
	// servers: catalog ids map to the library server they became
	libraryID := map[string]string{}
	addCatalog := func(id string) (string, error) {
		if lid, ok := libraryID[id]; ok {
			return lid, nil
		}
		var out struct {
			Server struct {
				ID string `json:"id"`
			} `json:"server"`
			Next string `json:"next"`
		}
		if err := call("mcplib.addFromCatalog", map[string]string{"catalogId": id}, &out); err != nil {
			return "", err
		}
		if out.Next != "" {
			connWarn("connector %s: %s", id, out.Next)
		}
		libraryID[id] = out.Server.ID
		return out.Server.ID, nil
	}
	for _, id := range r.CatalogServers {
		lid, err := addCatalog(id)
		if err == nil {
			err = call("mcpserver.add", map[string]string{"projectId": p.ID, "serverId": lid}, nil)
		}
		if err != nil {
			connWarn("connector %s not added: %v", id, err)
		}
	}
	for _, id := range r.LibraryServers {
		if err := call("mcpserver.add", map[string]string{"projectId": p.ID, "serverId": id}, nil); err != nil {
			connWarn("connector %s not added: %v", id, err)
		} else {
			libraryID[id] = id
		}
	}
	for _, a := range r.Agents {
		var made Agent
		var err error
		switch {
		case a.Prebuilt != "":
			var out struct {
				Agent Agent `json:"agent"`
				MCP   struct {
					Skipped []struct{ ID, Reason string } `json:"skipped"`
				} `json:"mcp"`
				Skills struct {
					Pending []struct{ Name, Reason string } `json:"pending"`
					Failed  []struct{ Name, Reason string } `json:"failed"`
				} `json:"skills"`
			}
			err = call("agentcatalog.apply", map[string]any{"id": a.Prebuilt, "projectId": p.ID, "name": a.Name, "adapter": a.Adapter, "model": a.Model,
				"effort": a.Effort, "permissionMode": a.PermissionMode, "installSkills": r.InstallSkills, "connectMcp": true, "confirmSkipsApprovals": a.ConfirmSkipsApprovals}, &out)
			made = out.Agent
			for _, s := range out.MCP.Skipped {
				connWarn("%s: connector %s not granted: %s", made.Name, s.ID, s.Reason)
			}
			for _, s := range out.Skills.Failed {
				warn("%s: skill %s not installed: %s", made.Name, s.Name, s.Reason)
			}
			for _, s := range out.Skills.Pending {
				warnAs(SetupWarning{Kind: "decision"}, "%s: skill %s waits for your decision: %s", made.Name, s.Name, s.Reason)
			}
		case a.Template != "":
			var out AppliedTemplate
			err = call("agenttemplate.apply", map[string]any{"templateId": a.Template, "projectId": p.ID, "name": a.Name, "confirmSkipsApprovals": a.ConfirmSkipsApprovals}, &out)
			made = out.Agent
			for _, s := range out.Skipped {
				warn("%s: %s", made.Name, s)
			}
		default:
			made, err = c.CreateAgent(ctx, AgentSpec{ProjectID: p.ID, Name: a.Name, Role: a.Role, Adapter: a.Adapter, Model: a.Model, Effort: a.Effort,
				PermissionMode: a.PermissionMode, Purpose: a.Purpose, Practices: a.Practices, ConfirmSkipsApprovals: a.ConfirmSkipsApprovals})
			if err == nil {
				for _, sid := range a.Servers {
					lid, ok := libraryID[sid]
					if !ok {
						if c.isLibraryServer(ctx, sid) {
							lid = sid
						} else if lid, err = addCatalog(sid); err != nil {
							connWarn("%s: connector %s not granted: %v", made.Name, sid, err)
							err = nil
							continue
						}
					}
					if gerr := call("mcpserver.grant", map[string]any{"projectId": p.ID, "serverId": lid, "agentId": made.ID, "on": true}, nil); gerr != nil {
						connWarn("%s: connector %s not granted: %v", made.Name, sid, gerr)
					}
				}
			}
		}
		if err != nil {
			who := firstNonEmpty(a.Name, a.Prebuilt, a.Template)
			warn("agent %s not added: %v", who, err)
			continue
		}
		res.Agents = append(res.Agents, made)
	}
	// an agent whose CLI is not usable on this machine cannot do anything yet: say so now, not at its first turn
	byAdapter := map[string][]string{}
	var adapters []string
	for _, a := range res.Agents {
		if _, seen := byAdapter[a.Adapter]; !seen {
			adapters = append(adapters, a.Adapter)
		}
		byAdapter[a.Adapter] = append(byAdapter[a.Adapter], a.Name)
	}
	for _, ad := range adapters {
		conn, err := c.Connections.Check(ctx, ad)
		if err != nil || conn.Usable {
			continue
		}
		why := string(conn.Status)
		if len(conn.Problems) > 0 {
			why = conn.Problems[0]
		}
		warnAs(SetupWarning{Kind: "cli", Adapter: ad, Status: string(conn.Status)}, "%s cannot work yet: the %s CLI is not usable on this machine (%s). Set it up in Settings > Agent CLIs: install it, sign in and accept its notice", strings.Join(byAdapter[ad], ", "), ad, why)
	}
	if later.repo != "" {
		l := c.landClone(p.ID, later.repo, later.parent)
		res.Landing = &l
	}
	c.publish(ctx, "project.setup", p.ID, map[string]any{"agents": len(res.Agents), "warnings": len(res.Warnings), "repo": r.Repo.Kind})
	return res, nil
}

func (c *Core) isLibraryServer(ctx context.Context, id string) bool {
	var n int
	_ = c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_library WHERE id=?`, id).Scan(&n)
	return n > 0
}
