package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/skills"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Team management: change an agent's name, role, purpose, model, effort or permission mode after it was created, and remove it
// from the team. The purpose is the human's standing instructions for the agent; Runtime.Instructions (installed in Open)
// adds it to the system prompt of every session the agent starts, whatever started it.

func init() {
	RegisterExtension(Extension{Name: "agentmgmt", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"agent.update": bind(func(ctx context.Context, p struct {
				AgentUpdate
				// AddSkills and RemoveSkills give the agent project skills or take them away (Skills page: who gets each).
				AddSkills    []string `json:"addSkills"`
				RemoveSkills []string `json:"removeSkills"`
				// Everywhere also saves the change to the agent's template and the other agents made from it (decision P137).
				Everywhere bool `json:"everywhere"`
			}) (any, error) {
				ag, err := c.updateAgentWithSkills(ctx, p.AgentUpdate, p.AddSkills, p.RemoveSkills)
				if err != nil || !p.Everywhere {
					return ag, err
				}
				ev, err := c.updateEverywhere(ctx, p.AgentUpdate, ag)
				if err != nil {
					return nil, fmt.Errorf("%s was changed in this project, but %w", ag.Name, err)
				}
				ag.TemplateID = ev.TemplateID
				return UpdatedEverywhere{Agent: ag, Everywhere: &ev}, nil
			}),
			"agent.draft":   bind(func(ctx context.Context, p AgentDraftRequest) (any, error) { return c.DraftAgent(ctx, p) }),
			"agent.archive": bind(func(ctx context.Context, p idParam) (any, error) { return c.ArchiveAgent(ctx, p.ID) }),
			"claude.practices": bind(func(ctx context.Context, p struct {
				Role string `json:"role"`
			}) (any, error) {
				return claudePracticesFor(p.Role), nil
			}),
		}, nil, nil
	}})
}

// createAgentWithSkills makes an agent and gives it the named skills of its project. The names are checked first, so an
// unknown skill refuses the call before the agent exists. Giving a skill never makes it deliverable: only accepted skills
// reach an agent, and a whole-team skill keeps reaching the rest of the team.
func (c *Core) createAgentWithSkills(ctx context.Context, s AgentSpec, skillNames []string) (Agent, error) {
	names, err := c.projectSkillNames(ctx, s.ProjectID, skillNames, false)
	if err != nil {
		return Agent{}, err
	}
	ag, err := c.CreateAgent(ctx, s)
	if err != nil {
		return Agent{}, err
	}
	if err := c.giveSkills(ctx, ag, names, true); err != nil {
		return ag, fmt.Errorf("the agent %s was created, but %w", ag.Name, err)
	}
	return ag, nil
}

// updateAgentWithSkills changes an agent and then gives it the add skills and takes the remove skills away. The names to
// add are checked first; a skill to remove that the project no longer has is skipped (it reaches nobody).
func (c *Core) updateAgentWithSkills(ctx context.Context, u AgentUpdate, add, remove []string) (Agent, error) {
	if len(add) == 0 && len(remove) == 0 {
		return c.UpdateAgent(ctx, u)
	}
	cur, err := c.agent(ctx, u.ID)
	if err != nil {
		return Agent{}, fmt.Errorf("agent %s: %w", u.ID, err)
	}
	addN, err := c.projectSkillNames(ctx, cur.ProjectID, add, false)
	if err != nil {
		return Agent{}, err
	}
	removeN, err := c.projectSkillNames(ctx, cur.ProjectID, remove, true)
	if err != nil {
		return Agent{}, err
	}
	for _, n := range addN {
		if slices.Contains(removeN, n) {
			return Agent{}, fmt.Errorf("skill %s is both added and removed", n)
		}
	}
	ag, err := c.UpdateAgent(ctx, u)
	if err != nil {
		return Agent{}, err
	}
	if err := c.giveSkills(ctx, ag, addN, true); err != nil {
		return ag, err
	}
	if err := c.giveSkills(ctx, ag, removeN, false); err != nil {
		return ag, err
	}
	return ag, nil
}

// projectSkillNames trims and de-duplicates skill names and checks that each is a skill of the project; with skipMissing a
// name the project does not have is dropped instead of refused.
func (c *Core) projectSkillNames(ctx context.Context, projectID string, in []string, skipMissing bool) ([]string, error) {
	out := []string{}
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" || slices.Contains(out, n) {
			continue
		}
		k := c.Knowledge()
		if k == nil {
			return nil, errors.New("skills are not available")
		}
		if _, err := k.Skills.Get(ctx, projectID, n); err != nil {
			if skipMissing && errors.Is(err, skills.ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// giveSkills gives the agent each named skill (on) or takes it away, with an audit entry for each.
func (c *Core) giveSkills(ctx context.Context, ag Agent, names []string, on bool) error {
	k := c.Knowledge()
	for _, n := range names {
		if _, err := k.Skills.Assign(ctx, c.Human(), ag.ProjectID, n, ag.ID, on); err != nil {
			verb := "giving it"
			if !on {
				verb = "taking away"
			}
			return fmt.Errorf("%s the skill %s failed: %w", verb, n, err)
		}
		k.audit(ctx, "skill.assign", n, map[string]any{"project": ag.ProjectID, "agent": ag.ID, "on": on, "via": "agent form"})
	}
	return nil
}

// MaxPurposeRunes bounds the purpose text: it rides in every run's system prompt.
const MaxPurposeRunes = 4000

// AgentUpdate changes the fields that are set. The adapter and provider cannot change: make a new agent for a different CLI.
type AgentUpdate struct {
	ID             string  `json:"id"`
	Name           *string `json:"name"`
	Role           *string `json:"role"`
	Purpose        *string `json:"purpose"`
	Model          *string `json:"model"`
	Effort         *string `json:"effort"`
	PermissionMode *string `json:"permissionMode"`
	Practices      *bool   `json:"practices"`
	// RouteModels replaces the models the router may choose between (an empty list turns routing off).
	RouteModels *[]RouteModel `json:"routeModels"`
	// RunMode ("" follows the project, native, wsl) and WSLDistro change where the CLI runs (decision P41).
	RunMode   *string `json:"runMode"`
	WSLDistro *string `json:"wslDistro"`
	// ConfirmSkipsApprovals is the person's explicit confirmation of a change to a permission mode that lets the agent act
	// without asking (bypassPermissions, auto, dontAsk); without it such a change is refused (NeedsConfirmationError).
	ConfirmSkipsApprovals bool `json:"confirmSkipsApprovals,omitempty"`
}

// validateAgentFields checks what would otherwise only fail (or misbehave) when the CLI starts: an effort the adapter does not
// accept, a model or effort that could be read as a command-line option, an oversized or binary purpose.
func (c *Core) validateAgentFields(adapter, model, effort, purpose string) error {
	for what, v := range map[string]string{"model": model, "effort": effort} {
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "-") || strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return fmt.Errorf("the %s %q is not valid: it must be a single word that does not start with '-'", what, v)
		}
	}
	if e := strings.TrimSpace(effort); e != "" {
		c.mu.Lock()
		a, ok := c.adapters[adapter]
		c.mu.Unlock()
		if ok {
			if list := a.Capabilities().Efforts; len(list) > 0 {
				found := false
				for _, x := range list {
					found = found || x == e
				}
				if !found {
					return fmt.Errorf("%s accepts these effort levels: %s (got %q)", adapter, strings.Join(list, ", "), e)
				}
			}
		}
	}
	if utf8.RuneCountInString(purpose) > MaxPurposeRunes {
		return fmt.Errorf("the purpose is %d characters; the limit is %d (it is sent with every run)", utf8.RuneCountInString(purpose), MaxPurposeRunes)
	}
	if strings.ContainsRune(purpose, 0) {
		return errors.New("the purpose contains a NUL character")
	}
	return nil
}

// UpdateAgent applies the change to the agent's next session; a session that is already running keeps what it started with.
func (c *Core) UpdateAgent(ctx context.Context, u AgentUpdate) (Agent, error) {
	a, err := c.agent(ctx, u.ID)
	if err != nil {
		return Agent{}, fmt.Errorf("agent %s: %w", u.ID, err)
	}
	if a.Archived {
		return Agent{}, errors.New("this agent was removed from the team")
	}
	var changed []string
	fromMode := a.PermissionMode // what a change of permission mode is recorded as coming from
	set := func(name string, dst *string, src *string) {
		if src != nil && strings.TrimSpace(*src) != *dst {
			*dst = strings.TrimSpace(*src)
			changed = append(changed, name)
		}
	}
	set("name", &a.Name, u.Name)
	set("role", &a.Role, u.Role)
	set("purpose", &a.Purpose, u.Purpose)
	set("model", &a.Model, u.Model)
	set("effort", &a.Effort, u.Effort)
	set("permissionMode", &a.PermissionMode, u.PermissionMode)
	if u.Practices != nil && *u.Practices != a.Practices {
		a.Practices = *u.Practices
		changed = append(changed, "practices")
	}
	if u.RunMode != nil || u.WSLDistro != nil {
		mode, distro := a.RunMode, a.WSLDistro
		if u.RunMode != nil {
			mode = *u.RunMode
		}
		if u.WSLDistro != nil {
			distro = *u.WSLDistro
		}
		m, d, err := c.checkRunMode(ctx, a.ProjectID, a.Adapter, mode, distro)
		if err != nil {
			return Agent{}, err
		}
		if m != a.RunMode || d != a.WSLDistro {
			a.RunMode, a.WSLDistro = m, d
			changed = append(changed, "runMode")
		}
	}
	if u.RouteModels != nil {
		rm, err := c.validateRouteModels(a.Adapter, *u.RouteModels)
		if err != nil {
			return Agent{}, err
		}
		if !sameRoutes(rm, a.RouteModels) {
			a.RouteModels = rm
			changed = append(changed, "routeModels")
		}
	}
	if len(changed) == 0 {
		return a, nil
	}
	if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Role) == "" {
		return Agent{}, errors.New("an agent needs a name and a role")
	}
	if err := c.validateAgentFields(a.Adapter, a.Model, a.Effort, a.Purpose); err != nil {
		return Agent{}, err
	}
	if u.PermissionMode != nil {
		if err := c.checkPermissionMode(a.Adapter, a.PermissionMode); err != nil {
			return Agent{}, err
		}
		// only a change into such a mode asks: an agent that has it already keeps it while its other fields are edited
		if slices.Contains(changed, "permissionMode") {
			if err := c.confirmSkip(ctx, a.ProjectID, a.ID, "this agent", fromMode, a.PermissionMode, u.ConfirmSkipsApprovals); err != nil {
				return Agent{}, err
			}
		}
	}
	err = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		routes, _ := json.Marshal(a.RouteModels)
		if len(a.RouteModels) == 0 {
			routes = nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE agents SET name=?, role=?, purpose=?, model=?, effort=?, permission_mode=?, practices=?, route_models=?, run_mode=?, wsl_distro=?, version=version+1 WHERE id=? AND archived_at=0`,
			a.Name, a.Role, a.Purpose, a.Model, a.Effort, a.PermissionMode, b2i(a.Practices), string(routes), a.RunMode, a.WSLDistro, a.ID)
		return err
	})
	if err != nil {
		return Agent{}, err
	}
	c.publish(ctx, "agent.updated", a.ProjectID, map[string]any{"id": a.ID, "changed": changed, "by": c.Human()})
	return a, nil
}

// ArchiveAgent removes an agent from the team. Its history (runs, usage, room messages) stays; it no longer appears in the
// team, cannot be assigned, and is not mentioned or scheduled. It is refused while the agent still owns unfinished work.
func (c *Core) ArchiveAgent(ctx context.Context, id string) (Agent, error) {
	a, err := c.agent(ctx, id)
	if err != nil {
		return Agent{}, fmt.Errorf("agent %s: %w", id, err)
	}
	if a.Archived {
		return a, nil
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT title FROM tasks WHERE (assignee=? OR reviewer=?) AND status NOT IN ('COMPLETED','FAILED','CANCELLED') ORDER BY created_at LIMIT 4`, id, id)
	if err != nil {
		return Agent{}, err
	}
	var titles []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			titles = append(titles, t)
		}
	}
	rows.Close()
	if len(titles) > 0 {
		more := ""
		if len(titles) > 3 {
			titles, more = titles[:3], " and more"
		}
		return Agent{}, fmt.Errorf("%s still has unfinished tasks (%s%s): reassign or cancel them first", a.Name, strings.Join(titles, "; "), more)
	}
	var live int
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_runs WHERE agent_id=? AND ended_at=0`, id).Scan(&live); err == nil && live > 0 {
		return Agent{}, fmt.Errorf("%s is running right now: wait for it to finish or cancel its task first", a.Name)
	}
	err = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agents SET archived_at=?, version=version+1 WHERE id=?`, storage.Now(), id)
		return err
	})
	if err != nil {
		return Agent{}, err
	}
	a.Archived = true
	// it leaves every room: no longer offered for @-mentions, listed to other agents, or eligible as a moderator; its
	// messages stay (room-protocol s10)
	var roomIDs []string
	if rows, err := c.DB.Reader().QueryContext(ctx, `SELECT room_id FROM room_members WHERE member_id=? AND kind='agent'`, id); err == nil {
		for rows.Next() {
			var r string
			if rows.Scan(&r) == nil {
				roomIDs = append(roomIDs, r)
			}
		}
		rows.Close()
	}
	for _, r := range roomIDs {
		if err := c.Room.RemoveMember(ctx, r, room.Agent(id), room.Human(c.cfg.User)); err != nil {
			c.logf("remove %s from room %s: %v", a.Name, r, err)
		}
	}
	c.publish(ctx, "agent.archived", a.ProjectID, map[string]any{"id": a.ID, "name": a.Name, "by": c.Human()})
	return a, nil
}

// agentInstructions is Runtime.Instructions: what is added to the system prompt of every session an agent starts. For a Claude Code
// agent that is first the best-practices text for the role the session is playing (internal/agents/claude/practices.go; role is
// "reviewer" for a review turn), then the human's own standing instructions. The second result is the version of the practices
// text that was included, 0 when none was.
//
// The purpose framing was chosen by measuring the real CLI (Claude Code 2.1.295, haiku, low effort, a style instruction after a
// tool call): "do not override the task" wording was followed in about 2 of 9 runs, "always in force... how you work" wording in 7
// of 8. The review and approval rules do not depend on this text: they are enforced by the workflow, not by the prompt.
func (c *Core) agentInstructions(ctx context.Context, agentID, role string) (string, int) {
	a, err := c.agent(ctx, agentID)
	if err != nil {
		return "", 0
	}
	if strings.TrimSpace(role) == "" {
		role = a.Role
	}
	var parts []string
	version := 0
	if a.Adapter == "claude" && a.Practices {
		parts = append(parts, claude.Practices(role))
		version = claude.PracticesVersion
	}
	if strings.TrimSpace(a.Purpose) != "" {
		parts = append(parts, fmt.Sprintf("You are %s, a member of an engineering team that works through Drydock. The person who set up this team gave you the standing instructions below. "+
			"They are always in force: they apply to everything you do on this project, in every task and every reply, including your final answer after you have used tools. "+
			"The task you are given each turn says what to work on; these instructions say how you work. They do not change the rule that a different agent reviews your work and a human approves it.\n\n%s", a.Name, strings.TrimSpace(a.Purpose)))
	}
	return strings.Join(parts, "\n\n"), version
}

// ClaudePractices is the claude.practices method: the text a Claude agent in a role is given, and a suggested model and effort.
type ClaudePractices struct {
	Version int           `json:"version"`
	Role    string        `json:"role"`
	Family  string        `json:"family"`
	Text    string        `json:"text"`
	Preset  claude.Preset `json:"preset"`
}

func claudePracticesFor(role string) ClaudePractices {
	return ClaudePractices{Version: claude.PracticesVersion, Role: role, Family: claude.FamilyOf(role), Text: claude.Practices(role), Preset: claude.PresetFor(role)}
}

// validateRouteModels checks the models the router may choose for an agent: each a single word the CLI can take, a known
// tier, no duplicates, at most six, and at least one of each tier when there are any (the router's ladder starts cheap and
// escalates to premium).
func (c *Core) validateRouteModels(adapter string, in []RouteModel) ([]RouteModel, error) {
	if len(in) == 0 {
		return []RouteModel{}, nil
	}
	if len(in) > 6 {
		return nil, errors.New("the router can choose between at most 6 models for one agent")
	}
	seen := map[string]bool{}
	tiers := map[string]bool{}
	out := make([]RouteModel, 0, len(in))
	for _, r := range in {
		r.Model, r.Tier = strings.TrimSpace(r.Model), strings.TrimSpace(r.Tier)
		if r.Model == "" {
			return nil, errors.New("a routed model needs a name")
		}
		if err := c.validateAgentFields(adapter, r.Model, "", ""); err != nil {
			return nil, err
		}
		if r.Tier != "cheap" && r.Tier != "premium" {
			return nil, fmt.Errorf("the tier of %s must be cheap or premium", r.Model)
		}
		if seen[r.Model] {
			return nil, fmt.Errorf("%s is listed twice", r.Model)
		}
		seen[r.Model], tiers[r.Tier] = true, true
		out = append(out, r)
	}
	if !tiers["cheap"] || !tiers["premium"] {
		return nil, errors.New("give the router at least one cheap and one premium model: it starts cheap and escalates to premium when the work gets stuck")
	}
	return out, nil
}

func sameRoutes(a, b []RouteModel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkPermissionMode refuses a permission mode the agent's adapter does not list ("" is the adapter's default).
func (c *Core) checkPermissionMode(adapter, mode string) error {
	c.mu.Lock()
	ad, ok := c.adapters[adapter]
	c.mu.Unlock()
	if !ok || mode == "" {
		return nil
	}
	modes := ad.Capabilities().PermissionModes
	if len(modes) == 0 {
		return nil
	}
	for _, m := range modes {
		if m == mode {
			return nil
		}
	}
	return fmt.Errorf("%s accepts these permission modes: %s (got %q)", adapter, strings.Join(modes, ", "), mode)
}

// UpdatedEverywhere is agent.update's answer with everywhere set: the agent (its fields at the top level, as without it) and
// what was saved beyond this project.
type UpdatedEverywhere struct {
	Agent
	Everywhere *EverywhereResult `json:"everywhere,omitempty"`
}

// EverywhereResult says where an "Everywhere" change went: the template (made now when the agent had none) and how many other
// agents made from it took the change; Skipped names an agent that refused it and why.
type EverywhereResult struct {
	TemplateID   string   `json:"templateId"`
	TemplateName string   `json:"templateName"`
	Created      bool     `json:"created"`
	Agents       int      `json:"agents"`
	Skipped      []string `json:"skipped"`
}

// linkTemplate records the template an agent was made from (decision P137).
func (c *Core) linkTemplate(ctx context.Context, agentID, templateID string) error {
	return c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agents SET template_id=? WHERE id=?`, templateID, agentID)
		return err
	})
}

// updateEverywhere is the editor's "Everywhere" (decision P137), after the agent itself was changed. The fields the update set
// among instructions, model, effort and permission mode are saved to the agent's template, and to every other agent made from
// that template, in any project. An agent without a template (or whose template was deleted) is linked to the template of its
// name and CLI when there is one, else saved as a new template, which every later "Everywhere" then updates. Agents are never
// linked live: a template change reaches the others only through this call.
func (c *Core) updateEverywhere(ctx context.Context, u AgentUpdate, ag Agent) (EverywhereResult, error) {
	t := &agentTemplates{c: c}
	res := EverywhereResult{Skipped: []string{}}
	var tpl AgentTemplate
	found := false
	if ag.TemplateID != "" {
		if x, err := t.get(ctx, ag.TemplateID); err == nil {
			tpl, found = x, true
		}
	}
	if !found {
		all, err := t.list(ctx)
		if err != nil {
			return res, err
		}
		for _, x := range all {
			if x.Name == ag.Name && x.Adapter == ag.Adapter {
				tpl, found = x, true
				break
			}
		}
	}
	if !found {
		spec, err := t.specOf(ctx, ag)
		if err != nil {
			return res, err
		}
		spec.ConfirmSkipsApprovals = u.ConfirmSkipsApprovals
		if tpl, err = t.save(ctx, spec); err != nil {
			return res, fmt.Errorf("saving it as a template failed: %w", err)
		}
		res.Created = true
	} else {
		apply := func(dst *string, src *string) {
			if src != nil {
				*dst = strings.TrimSpace(*src)
			}
		}
		apply(&tpl.Purpose, u.Purpose)
		apply(&tpl.Model, u.Model)
		apply(&tpl.Effort, u.Effort)
		apply(&tpl.PermissionMode, u.PermissionMode)
		tpl.Skills = nil // kept as they are
		tpl.ConfirmSkipsApprovals = u.ConfirmSkipsApprovals
		var err error
		if tpl, err = t.save(ctx, tpl); err != nil {
			return res, fmt.Errorf("the template %s was not changed: %w", tpl.Name, err)
		}
	}
	res.TemplateID, res.TemplateName = tpl.ID, tpl.Name
	if ag.TemplateID != tpl.ID {
		if err := c.linkTemplate(ctx, ag.ID, tpl.ID); err != nil {
			return res, err
		}
	}
	if res.Created {
		return res, nil
	}
	others, err := c.agentsWhere(ctx, `template_id=? AND id<>? AND archived_at=0 ORDER BY project_id, created_at, id`, tpl.ID, ag.ID)
	if err != nil {
		return res, err
	}
	for _, o := range others {
		su := AgentUpdate{ID: o.ID, Purpose: u.Purpose, Model: u.Model, Effort: u.Effort, PermissionMode: u.PermissionMode, ConfirmSkipsApprovals: u.ConfirmSkipsApprovals}
		if _, err := c.UpdateAgent(ctx, su); err != nil {
			res.Skipped = append(res.Skipped, o.Name+": "+err.Error())
			continue
		}
		res.Agents++
	}
	return res, nil
}
