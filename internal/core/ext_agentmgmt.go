package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/storage"
)

// Team management: change an agent's name, role, purpose, model, effort or permission mode after it was created, and remove it
// from the team. The purpose is the human's standing instructions for the agent; Runtime.Instructions (installed in Open)
// adds it to the system prompt of every session the agent starts, whatever started it.

func init() {
	RegisterExtension(Extension{Name: "agentmgmt", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"agent.update":  bind(func(ctx context.Context, p AgentUpdate) (any, error) { return c.UpdateAgent(ctx, p) }),
			"agent.archive": bind(func(ctx context.Context, p idParam) (any, error) { return c.ArchiveAgent(ctx, p.ID) }),
		}, nil, nil
	}})
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
		c.mu.Lock()
		ad, ok := c.adapters[a.Adapter]
		c.mu.Unlock()
		if ok {
			if modes := ad.Capabilities().PermissionModes; len(modes) > 0 {
				found := a.PermissionMode == ""
				for _, m := range modes {
					found = found || m == a.PermissionMode
				}
				if !found {
					return Agent{}, fmt.Errorf("%s accepts these permission modes: %s (got %q)", a.Adapter, strings.Join(modes, ", "), a.PermissionMode)
				}
			}
		}
	}
	err = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agents SET name=?, role=?, purpose=?, model=?, effort=?, permission_mode=?, version=version+1 WHERE id=? AND archived_at=0`,
			a.Name, a.Role, a.Purpose, a.Model, a.Effort, a.PermissionMode, a.ID)
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
	c.publish(ctx, "agent.archived", a.ProjectID, map[string]any{"id": a.ID, "name": a.Name, "by": c.Human()})
	return a, nil
}

// agentInstructions is Runtime.Instructions: the human's standing instructions for the agent. The framing was chosen by measuring
// the real CLI (Claude Code 2.1.295, haiku, low effort, a style instruction after a tool call): "do not override the task" wording
// was followed in about 2 of 9 runs, "always in force... how you work" wording in 7 of 8. The review and approval rules do not
// depend on this text: they are enforced by the workflow, not by the prompt. Empty when the agent has no purpose.
func (c *Core) agentInstructions(ctx context.Context, agentID string) string {
	a, err := c.agent(ctx, agentID)
	if err != nil || strings.TrimSpace(a.Purpose) == "" {
		return ""
	}
	return fmt.Sprintf("You are %s, a member of an engineering team that works through Drydock. The person who set up this team gave you the standing instructions below. "+
		"They are always in force: they apply to everything you do on this project, in every task and every reply, including your final answer after you have used tools. "+
		"The task you are given each turn says what to work on; these instructions say how you work. They do not change the rule that a different agent reviews your work and a human approves it.\n\n%s", a.Name, strings.TrimSpace(a.Purpose))
}
