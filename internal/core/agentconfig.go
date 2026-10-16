package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The configuration an agent CLI finds in the folder it works in (agents.ScanRepoConfig, audit 2026-10-16, M3). A CLI that acts
// on files in its working folder (Gemini CLI's settings and hooks, OpenCode's plugins, ...) is not started in a worktree that
// has any until a person has read them and trusted exactly those. The trusted state is a list of fingerprints per project and
// adapter in settings, so a changed file is not trusted. A refused start is published as agent.failed with the files and the
// fingerprint, and a notice in the project's #alerts channel says what to do; project.agentConfig shows what each adapter would
// find, and project.trustAgentConfig trusts what was shown.

const agentConfigKey = "agentconfig."

var fingerprintRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func init() {
	RegisterExtension(Extension{Name: "agentconfig", Start: startAgentConfig})
}

// AgentConfigFinding is what an adapter's CLI would act on in one place.
type AgentConfigFinding struct {
	Adapter     string   `json:"adapter"`
	Where       string   `json:"where"`  // "the repository", or the task whose worktree it is
	TaskID      string   `json:"taskId"` // empty for the repository
	Files       []string `json:"files"`
	Fingerprint string   `json:"fingerprint"`
	Trusted     bool     `json:"trusted"`
}

func startAgentConfig(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	bg, cancel := context.WithCancel(context.WithoutCancel(ctx))
	safe.Go("core.startAgentConfig", func() { c.watchAgentConfigRefusals(bg) })
	return map[string]Method{
		"project.agentConfig": bind(func(ctx context.Context, p idParam) (any, error) { return c.AgentConfigStatus(ctx, p.ID) }),
		"project.trustAgentConfig": bind(func(ctx context.Context, p struct {
			ID          string `json:"id"`
			Adapter     string `json:"adapter"`
			Fingerprint string `json:"fingerprint"`
		}) (any, error) {
			if err := c.TrustAgentConfig(ctx, p.ID, p.Adapter, p.Fingerprint, c.Human()); err != nil {
				return nil, err
			}
			return c.AgentConfigStatus(ctx, p.ID)
		}),
	}, cancel, nil
}

// repoConfigAdapters are the adapters that list files their CLI acts on, by id.
func (c *Core) repoConfigAdapters() map[string][]string {
	out := map[string][]string{}
	for id, a := range c.adapters {
		if rc, ok := a.(agents.RepoConfigReader); ok {
			if paths := rc.RepoConfigPaths(); len(paths) > 0 {
				out[id] = paths
			}
		}
	}
	return out
}

func (c *Core) trustedAgentConfig(ctx context.Context, projectID, adapter string) []string {
	var raw string
	if c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, agentConfigKey+projectID+":"+adapter).Scan(&raw) != nil {
		return nil
	}
	var fps []string
	if json.Unmarshal([]byte(raw), &fps) != nil {
		return nil
	}
	return fps
}

// agentConfigTrusted is Runtime.RepoConfigTrusted.
func (c *Core) agentConfigTrusted(ctx context.Context, projectID, adapter, fingerprint string) bool {
	return fingerprint != "" && slices.Contains(c.trustedAgentConfig(ctx, projectID, adapter), fingerprint)
}

// AgentConfigStatus lists, for each adapter that acts on configuration in its working folder, what it would find in the
// project's repository and in the worktrees of its unfinished tasks. Only places with something in them are listed.
func (c *Core) AgentConfigStatus(ctx context.Context, projectID string) ([]AgentConfigFinding, error) {
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	type place struct{ where, taskID, dir string }
	var places []place
	if p.HasRepo && p.RepoPath != "" {
		places = append(places, place{"the repository", "", p.RepoPath})
	}
	ts, err := c.Tasks.List(ctx, tasks.Filter{ProjectID: projectID, Statuses: []tasks.Status{tasks.Ready, tasks.Claimed, tasks.InProgress, tasks.Review, tasks.Verification, tasks.Blocked, tasks.WaitingHuman}, Limit: 200})
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.Worktree != "" {
			places = append(places, place{fmt.Sprintf("the worktree of task %d, %s", t.Seq, t.Title), t.ID, t.Worktree})
		}
	}
	adapters := c.repoConfigAdapters()
	ids := make([]string, 0, len(adapters))
	for id := range adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := []AgentConfigFinding{}
	for _, id := range ids {
		trusted := c.trustedAgentConfig(ctx, projectID, id)
		for _, pl := range places {
			files, fp, err := agents.ScanRepoConfig(pl.dir, adapters[id])
			if err != nil || len(files) == 0 {
				continue // a folder that is gone has nothing to show
			}
			out = append(out, AgentConfigFinding{Adapter: id, Where: pl.where, TaskID: pl.taskID, Files: files, Fingerprint: fp, Trusted: slices.Contains(trusted, fp)})
		}
	}
	return out, nil
}

// TrustAgentConfig trusts the configuration with this fingerprint (from AgentConfigStatus, or the refusal's event) for the
// adapter in the project. Only a human may: no agent-facing path reaches the core's RPC.
func (c *Core) TrustAgentConfig(ctx context.Context, projectID, adapter, fingerprint, by string) error {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return errors.New("only a human may trust the configuration an agent CLI finds in a repository")
	}
	if err := c.RequireLicensed(ctx, "approve"); err != nil {
		return err
	}
	if _, err := c.GetProject(ctx, projectID); err != nil {
		return err
	}
	if _, ok := c.repoConfigAdapters()[adapter]; !ok {
		return fmt.Errorf("%s does not act on configuration in its working folder, so there is nothing to trust", adapter)
	}
	if !fingerprintRe.MatchString(fingerprint) {
		return errors.New("trust the configuration by the fingerprint that was shown with it")
	}
	fps := c.trustedAgentConfig(ctx, projectID, adapter)
	if !slices.Contains(fps, fingerprint) {
		fps = append(fps, fingerprint)
		if len(fps) > 20 { // the newest twenty: a place that changes often does not grow the setting without bound
			fps = fps[len(fps)-20:]
		}
	}
	b, _ := json.Marshal(fps)
	if _, err := c.DB.Writer().ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, agentConfigKey+projectID+":"+adapter, string(b)); err != nil {
		return err
	}
	if err := auditAppend(ctx, c, "agentconfig.trust", projectID, map[string]any{"adapter": adapter, "fingerprint": fingerprint}); err != nil {
		c.logf("audit agentconfig.trust: %v", err)
	}
	c.publish(ctx, "security.agent_config_trusted", projectID, map[string]any{"adapter": adapter, "by": by})
	return nil
}

// watchAgentConfigRefusals tells the project when an agent could not start because of configuration nobody trusted, once per
// fingerprint, and puts it in the inbox: a person has to read the files.
func (c *Core) watchAgentConfigRefusals(ctx context.Context) {
	f := events.Filter{Types: []string{"agent.failed"}}
	ch, cancel := c.Bus.Subscribe(f)
	defer func() { cancel() }()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				ch, cancel = c.Bus.Subscribe(f)
				continue
			}
			var d struct {
				RepoConfig *struct {
					Adapter     string   `json:"adapter"`
					Files       []string `json:"files"`
					Fingerprint string   `json:"fingerprint"`
				} `json:"repoConfig"`
			}
			if json.Unmarshal(e.Data, &d) != nil || d.RepoConfig == nil || e.ProjectID == "" {
				continue
			}
			rc := d.RepoConfig
			shown := rc.Files
			if len(shown) > 6 {
				shown = shown[:6]
			}
			c.alertsNotice(ctx, e.ProjectID, "agentconfig:"+e.ProjectID+":"+rc.Adapter+":"+rc.Fingerprint,
				fmt.Sprintf("%s was not started: the folder it works in has configuration that the repository brought (%s), which the CLI would act on. "+
					"Read the files, then trust them in the project's settings (agent configuration), or remove them and try again.", rc.Adapter, strings.Join(shown, ", ")), true)
		}
	}
}
