package core

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/storage"
)

// What environment an agent process gets. Agent CLIs start from an allowlist (proc.BaseEnvNames, plus CLAUDE_* and ANTHROPIC_*
// for Claude Code), not from the app's whole environment, so a token another tool left in the environment (GITHUB_TOKEN,
// AWS_*, CLOUDFLARE_*) is not handed to an agent that a prompt could ask to print it. A setup the allowlist cannot know about
// (a Bedrock or Vertex login, a company variable) names the variables to pass on here. Names only: Drydock never stores or
// shows a value.

const settingPassEnv = "agent.pass_env"

func init() {
	RegisterExtension(Extension{Name: "agentenv", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"agentenv.get": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.agentEnvView(ctx), nil }),
			"agentenv.set": bind(func(ctx context.Context, p struct {
				Names []string `json:"names"`
			}) (any, error) {
				return c.setAgentPassEnv(ctx, p.Names)
			}),
		}, nil, nil
	}})
}

// AgentEnvView is what the settings page shows.
type AgentEnvView struct {
	// Pass are the extra variable names the human chose to pass to agents.
	Pass []string `json:"pass"`
	// Kept are the names every agent process keeps, and Prefixes the families kept for Claude Code.
	Kept     []string `json:"kept"`
	Prefixes []string `json:"prefixes"`
}

func passEnvNames(ctx context.Context, db *storage.DB) []string {
	var v string
	if err := db.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingPassEnv).Scan(&v); err != nil {
		return nil
	}
	var out []string
	for _, n := range strings.Split(v, "\n") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func (c *Core) agentEnvView(ctx context.Context) AgentEnvView {
	pass := passEnvNames(ctx, c.DB)
	if pass == nil {
		pass = []string{}
	}
	return AgentEnvView{Pass: pass, Kept: append([]string(nil), proc.BaseEnvNames...), Prefixes: []string{"CLAUDE_", "ANTHROPIC_"}}
}

func (c *Core) setAgentPassEnv(ctx context.Context, names []string) (AgentEnvView, error) {
	seen := map[string]bool{}
	var clean []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[strings.ToUpper(n)] {
			continue
		}
		if !envNameRe.MatchString(n) {
			return AgentEnvView{}, fmt.Errorf("%q is not an environment variable name (give names only, never values)", n)
		}
		seen[strings.ToUpper(n)] = true
		clean = append(clean, n)
	}
	sort.Strings(clean)
	if err := c.setSetting(ctx, settingPassEnv, strings.Join(clean, "\n")); err != nil {
		return AgentEnvView{}, err
	}
	c.publish(ctx, "agentenv.changed", "", map[string]any{"names": clean, "by": c.Human()})
	return c.agentEnvView(ctx), nil
}

func (c *Core) setSetting(ctx context.Context, key, value string) error {
	return c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
		return err
	})
}

// Isolation describes what keeps an agent inside its project. Level is "guarded" when the CLI's permission checks are on and
// "open" when the agent's permission mode lets it skip them. No level is an operating-system sandbox: an agent process runs with
// the Windows user's rights (docs/security.md).
type Isolation struct {
	Level   string   `json:"level"`
	Summary string   `json:"summary"`
	Details []string `json:"details"`
}

func isolationOf(a Agent) Isolation {
	in := Isolation{Level: "guarded", Details: []string{
		"Its working folder is its own Git worktree for the task, so its changes are separate from every other task and project and are reviewed before they are merged.",
		"It starts with a trimmed environment: your other tools' tokens (GitHub, cloud providers) are not handed to it. Settings > Agents lists what is kept and lets you pass named variables on.",
		"It can only use the MCP servers you granted it in its own project.",
	}}
	switch a.PermissionMode {
	case "bypassPermissions", "auto", "dontAsk":
		in.Level = "open"
		in.Summary = "Its permission mode (" + a.PermissionMode + ") lets it act without asking. Nothing keeps it inside its working folder except its own behavior."
		return in
	}
	switch a.Adapter {
	case "claude":
		in.Summary = "Claude Code's own permission checks keep it to its working folder and hold shell commands for approval, which a background run cannot give. This is not an operating-system sandbox."
		in.Details = append(in.Details, "Measured with Claude Code 2.1.295 on Linux in acceptEdits mode: reading or writing a file outside the working folder and running a shell command on one were all refused. Not measured on Windows.")
	default:
		in.Summary = "Drydock confines the file and terminal requests it serves for this agent to its working folder. The agent's own tools are not intercepted, and this is not an operating-system sandbox."
	}
	return in
}
