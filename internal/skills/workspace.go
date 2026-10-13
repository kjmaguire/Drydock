package skills

import (
	"context"
	"errors"
	"fmt"

	"github.com/kjmaguire/drydock/internal/mcp"
)

// MaxToolBodyChars bounds one skill body returned through the tool (about 5000 tokens, the spec's guidance).
const MaxToolBodyChars = 20000

// Provider implements mcp.SkillsProvider (the skills tool). It serves accepted skills of the run's own project whose roles
// match the run's role, never anything offered, rejected or blocked.
type Provider struct{ S *Service }

// NewProvider returns the provider to assign to mcp.Workspace.Skills.
func NewProvider(s *Service) mcp.SkillsProvider { return &Provider{S: s} }

func (p *Provider) eligible(ctx context.Context, run mcp.RunInfo) ([]Skill, error) {
	if run.ProjectID == "" {
		return nil, errors.New("this run has no project")
	}
	return p.S.Eligible(ctx, run.ProjectID, run.AgentID, run.Role)
}

func toMCP(k Skill, withBody bool) mcp.Skill {
	out := mcp.Skill{Name: k.Name, Description: k.Description, PinnedCommit: k.PinnedCommit}
	if withBody {
		out.Body = truncate(k.Body, MaxToolBodyChars)
	}
	return out
}

// List implements mcp.SkillsProvider.
func (p *Provider) List(ctx context.Context, run mcp.RunInfo) ([]mcp.Skill, error) {
	ks, err := p.eligible(ctx, run)
	if err != nil {
		return nil, err
	}
	out := make([]mcp.Skill, 0, len(ks))
	for _, k := range ks {
		out = append(out, toMCP(k, false))
	}
	return out, nil
}

// Get implements mcp.SkillsProvider.
func (p *Provider) Get(ctx context.Context, run mcp.RunInfo, name string) (mcp.Skill, error) {
	ks, err := p.eligible(ctx, run)
	if err != nil {
		return mcp.Skill{}, err
	}
	for _, k := range ks {
		if k.Name == name {
			p.S.RecordDelivery(ctx, targetOf(run), run.RunID, k.Name, k.ContentHash, MethodTool, FidelityOnDemand, "")
			p.S.RecordUse(ctx, targetOf(run), run.RunID, k.Name, UseTool)
			return toMCP(k, true), nil
		}
	}
	return mcp.Skill{}, fmt.Errorf("%w: no accepted skill named %q applies to this run", mcp.ErrNotFound, name)
}

// ForPaths implements mcp.SkillsProvider: the accepted skills whose globs match any of the paths (a skill with no globs
// applies everywhere). It records a delivery per skill, which is how evals learn whether the agent called the tool.
func (p *Provider) ForPaths(ctx context.Context, run mcp.RunInfo, paths []string) ([]mcp.Skill, error) {
	ks, err := p.eligible(ctx, run)
	if err != nil {
		return nil, err
	}
	var norm []string
	for _, x := range paths {
		if n, ok := NormalizePath(x); ok {
			norm = append(norm, n)
		}
	}
	if len(norm) == 0 {
		return nil, errors.New("no valid worktree-relative paths were given")
	}
	out := []mcp.Skill{}
	for _, k := range ks {
		if PathsMatch(k.Globs, norm) {
			p.S.RecordDelivery(ctx, targetOf(run), run.RunID, k.Name, k.ContentHash, MethodTool, FidelityOnDemand, "")
			p.S.RecordUse(ctx, targetOf(run), run.RunID, k.Name, UseTool)
			out = append(out, toMCP(k, true))
		}
	}
	return out, nil
}

func targetOf(run mcp.RunInfo) Target {
	return Target{ProjectID: run.ProjectID, TaskID: run.TaskID, AgentID: run.AgentID, Role: run.Role}
}
