package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The repository configuration guard (internal/git/guard.go, docs/security.md s3): Drydock refuses to run git in a repository
// whose own git configuration, hooks or info/attributes changed since a person last trusted them. The trusted state is kept
// in settings (one hash per item, never a value) so a change made while Drydock was not running is caught at the next start.
// A run that meets a change raises an approval (workflow.GateError); approving it in Respond trusts exactly the state shown.
// Outside a run, project.gitConfig shows the changed items and project.trustGitConfig trusts them.

const gitTrustKey = "git.trusted."

func init() {
	RegisterExtension(Extension{Name: "gitguard", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"project.gitConfig": bind(func(ctx context.Context, p idParam) (any, error) { return c.GitConfigStatus(ctx, p.ID) }),
			"project.trustGitConfig": bind(func(ctx context.Context, p struct {
				ID          string `json:"id"`
				Fingerprint string `json:"fingerprint"`
			}) (any, error) {
				return c.TrustGitConfig(ctx, p.ID, p.Fingerprint)
			}),
		}, nil, nil
	}})
}

// GitConfig is the guard's view of a project's repository.
type GitConfig struct {
	Trusted     bool     `json:"trusted"`
	Changed     []string `json:"changed"` // config keys, "hooks/<file>", "info/attributes": names only
	Fingerprint string   `json:"fingerprint"`
}

// GitConfigStatus reports whether the project's repository configuration is the trusted one.
func (c *Core) GitConfigStatus(ctx context.Context, projectID string) (GitConfig, error) {
	r, err := c.repo(ctx, projectID)
	if err != nil {
		return GitConfig{}, err
	}
	changed, fp, err := r.ConfigStatus(ctx)
	if err != nil {
		return GitConfig{}, err
	}
	if changed == nil {
		changed = []string{}
	}
	return GitConfig{Trusted: len(changed) == 0, Changed: changed, Fingerprint: fp}, nil
}

// TrustGitConfig trusts the repository's configuration as the person reviewed it (fingerprint from GitConfigStatus). Only the
// human calls it: no agent-facing path reaches the core's RPC.
func (c *Core) TrustGitConfig(ctx context.Context, projectID, fingerprint string) (GitConfig, error) {
	if err := c.RequireLicensed(ctx, "approve"); err != nil {
		return GitConfig{}, err
	}
	r, err := c.repo(ctx, projectID)
	if err != nil {
		return GitConfig{}, err
	}
	if err := r.Trust(ctx, fingerprint); err != nil {
		return GitConfig{}, err
	}
	if err := c.saveGitTrust(ctx, projectID, r); err != nil {
		return GitConfig{}, err
	}
	c.publish(ctx, "security.repo_config_trusted", projectID, map[string]any{"by": c.Human()})
	return c.GitConfigStatus(ctx, projectID)
}

// restoreGitTrust gives a freshly opened repository the state trusted before (or records the current one the first time).
func (c *Core) restoreGitTrust(ctx context.Context, projectID string, r *git.Repo) error {
	var v string
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, gitTrustKey+projectID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return c.saveGitTrust(ctx, projectID, r)
	}
	if err != nil {
		return err
	}
	var st git.RepoState
	if err := json.Unmarshal([]byte(v), &st); err != nil {
		return fmt.Errorf("the trusted git configuration of project %s is unreadable: %w", projectID, err)
	}
	r.SetTrustedState(st)
	return nil
}

func (c *Core) saveGitTrust(ctx context.Context, projectID string, r *git.Repo) error {
	b, err := json.Marshal(r.TrustedState())
	if err != nil {
		return err
	}
	return c.setSetting(ctx, gitTrustKey+projectID, string(b))
}

// trustOnApproval runs before a human's answer is recorded: approving a repo_config_changed gate trusts the state the person
// was shown. If the configuration changed again since, nothing is trusted and the node, run again, asks about the newer change.
func (c *Core) trustOnApproval(ctx context.Context, runID, requestID, decision string) {
	if decision != "approved" {
		return
	}
	pending, err := c.Engine.Pending(ctx, workflow.RunID(runID))
	if err != nil {
		return
	}
	for _, p := range pending {
		if string(p.ID) != requestID {
			continue
		}
		var g struct {
			Gate        string `json:"gate"`
			Fingerprint string `json:"fingerprint"`
		}
		if json.Unmarshal(p.Spec.Payload, &g) != nil {
			return
		}
		if g.Gate == pipeline.GateCIPush {
			// a person approved pushing a branch that changes CI definitions: remember exactly that push (head and paths)
			if info, err := c.Engine.Status(ctx, workflow.RunID(runID)); err == nil && info.TaskID != "" {
				if _, err := pipeline.RecordGateApproval(ctx, c.Tasks, string(info.TaskID), p.Spec.Payload, c.Human()); err != nil {
					c.logf("record the approval of a CI push: %v", err)
				}
			}
			return
		}
		if g.Gate != git.GateRepoConfigChanged {
			return
		}
		info, err := c.Engine.Status(ctx, workflow.RunID(runID))
		if err != nil {
			return
		}
		r, err := c.repo(ctx, info.ProjectID)
		if err != nil {
			return
		}
		if r.Trust(ctx, g.Fingerprint) == nil {
			_ = c.saveGitTrust(ctx, info.ProjectID, r)
			c.publish(ctx, "security.repo_config_trusted", info.ProjectID, map[string]any{"by": c.Human(), "run": runID})
		}
		return
	}
}
