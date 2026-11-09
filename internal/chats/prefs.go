package chats

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents"
)

// ---- what a new chat starts with (D2, D9) ----
//
// Two workspace settings, kept in the settings table:
//
//   - chats.lastChoice.<scope>.<adapter> (D2): the model, effort and permission mode the person last chose for a chat with
//     chat.update, per scope (a project's id, "-" for chats without one) and agent CLI. chat.create fills what it was not given
//     from it. A mode that skips approvals is never written, so it is never carried; what the CLI reported (cliModel) is never
//     written either: only the person's own choice.
//   - chats.defaultAllowedTools (D9): allow rules the person set for every new chat, each checked by ValidateRule. chat.create
//     copies them into a new chat of an agent that takes rules, where they are listed and removable like any other, and records
//     them in a note.

const (
	keyLastChoice   = "chats.lastChoice."
	keyDefaultRules = "chats.defaultAllowedTools"
)

// Choice is the person's last explicit choice for a scope and agent CLI. A nil field was never chosen; "" was chosen as the
// CLI's own default.
type Choice struct {
	Model          *string `json:"model,omitempty"`
	Effort         *string `json:"effort,omitempty"`
	PermissionMode *string `json:"permissionMode,omitempty"`
}

// LastChoice is chat.lastChoice's result: what a new chat of the scope and agent CLI would start with ("" = the CLI's default).
type LastChoice struct {
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permissionMode"`
}

func choiceKey(projectID, adapter string) string {
	scope := strings.TrimSpace(projectID)
	if scope == "" {
		scope = "-"
	}
	return keyLastChoice + scope + "." + strings.TrimSpace(adapter)
}

func (s *Service) setting(ctx context.Context, key string) string {
	var v string
	if err := s.d.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

func setSettingTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Service) choice(ctx context.Context, projectID, adapter string) Choice {
	var c Choice
	_ = json.Unmarshal([]byte(s.setting(ctx, choiceKey(projectID, adapter))), &c)
	return c
}

// rememberChoiceTx records the fields chat.update was given, as they were stored, for the chat's scope and agent CLI. A mode
// that skips approvals is not recorded (the one recorded before stays).
func rememberChoiceTx(ctx context.Context, tx *sql.Tx, c Chat, r UpdateReq) error {
	if r.Model == nil && r.Effort == nil && r.PermissionMode == nil {
		return nil
	}
	key := choiceKey(c.ProjectID, c.Adapter)
	var ch Choice
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &ch)
	}
	if r.Model != nil {
		v := c.Model
		ch.Model = &v
	}
	if r.Effort != nil {
		v := c.Effort
		ch.Effort = &v
	}
	if r.PermissionMode != nil && !SkipsApprovals(c.PermissionMode) {
		v := c.PermissionMode
		ch.PermissionMode = &v
	}
	b, _ := json.Marshal(ch)
	return setSettingTx(ctx, tx, key, string(b))
}

// carried is what a new chat takes from the last choice: the fields the request left empty, each still valid for the agent
// CLI (a model or effort it no longer takes, a mode it does not have or one that skips approvals, is left out). from names the
// fields filled ("model", "effort", "permissionMode").
func (s *Service) carried(ctx context.Context, projectID, adapter string, caps agents.Capabilities, model, effort, mode string) (string, string, string, []string) {
	ch := s.choice(ctx, projectID, adapter)
	var from []string
	if model == "" && ch.Model != nil && *ch.Model != "" && (s.d.ValidateFields == nil || s.d.ValidateFields(adapter, *ch.Model, effort) == nil) {
		model = *ch.Model
		from = append(from, "model")
	}
	if effort == "" && ch.Effort != nil && *ch.Effort != "" && (s.d.ValidateFields == nil || s.d.ValidateFields(adapter, model, *ch.Effort) == nil) {
		effort = *ch.Effort
		from = append(from, "effort")
	}
	if strings.TrimSpace(mode) == "" && ch.PermissionMode != nil && *ch.PermissionMode != "" && !SkipsApprovals(*ch.PermissionMode) {
		if m, err := permissionMode(caps, *ch.PermissionMode); err == nil {
			mode = m
			from = append(from, "permissionMode")
		}
	}
	return model, effort, mode, from
}

// LastChoice is what a new chat of the scope and agent CLI starts with when it is given nothing (chat.lastChoice): Home's start
// box reads it too.
func (s *Service) LastChoice(ctx context.Context, projectID, adapter string) (LastChoice, error) {
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = "claude"
	}
	caps, ok := s.caps(adapter)
	if !ok {
		return LastChoice{}, fmt.Errorf("unknown adapter %q", adapter)
	}
	m, e, p, _ := s.carried(ctx, projectID, adapter, caps, "", "", "")
	return LastChoice{Model: m, Effort: e, PermissionMode: p}, nil
}

// ---- allow rules for every new chat (D9) ----

// DefaultRules are the allow rules every new chat of an agent that takes rules starts with (chat.defaultRules).
func (s *Service) DefaultRules(ctx context.Context) []string {
	out := []string{}
	_ = json.Unmarshal([]byte(s.setting(ctx, keyDefaultRules)), &out)
	clean := []string{}
	for _, r := range out {
		if v, err := ValidateRule(r); err == nil && !contains(clean, v) {
			clean = append(clean, v)
		}
	}
	return clean
}

// SetDefaultRule adds (allow) or removes a rule for every new chat; chats that exist keep their own rules. The rule is checked
// as chat.allowTool checks it. It publishes chat.default_rules with the list.
func (s *Service) SetDefaultRule(ctx context.Context, rule string, allow bool) ([]string, error) {
	var out []string
	changed := false
	err := s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		changed = false
		var raw string
		cur := []string{}
		if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, keyDefaultRules).Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &cur)
		}
		out = cur
		if allow {
			r, err := ValidateRule(rule)
			if err != nil {
				return err
			}
			if contains(cur, r) {
				return nil
			}
			if len(cur) >= s.o.MaxRules {
				return fmt.Errorf("every new chat can start with at most %d allow rules", s.o.MaxRules)
			}
			out = append(append([]string{}, cur...), r)
		} else {
			rule = strings.TrimSpace(rule)
			out = []string{}
			for _, r := range cur {
				if r != rule {
					out = append(out, r)
				}
			}
			if len(out) == len(cur) {
				return nil
			}
		}
		b, _ := json.Marshal(out)
		changed = true
		return setSettingTx(ctx, tx, keyDefaultRules, string(b))
	})
	if err != nil {
		return nil, err
	}
	if changed {
		s.publish(ctx, "chat.default_rules", "", map[string]any{"rules": out})
	}
	return out, nil
}
