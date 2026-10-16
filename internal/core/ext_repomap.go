package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/repomap"
)

// The repo map (decisions D5, P62): Aider's map of a repository's most important code, built with tree-sitter (CGO). It
// reaches agents three ways: the Workspace MCP tool repo_map (every role, read-only, in a CGO build), a small section of
// AGENTS.md in repositories big enough to need one, and the person's Repo map page (repomap.get). A build without CGO has
// no parser: the tool answers mcp.RepoMapGatedMessage, AGENTS.md gets no section and the page says so.

func init() {
	RegisterExtension(Extension{Name: "repomap", Start: startRepoMap})
}

// Repo map section of AGENTS.md: it goes into every turn, so it is small and only in repositories where reading around
// costs more than the map (token rules 9 and 11, docs/token-control.md).
const (
	RepoMapAgentsBudget   = 1024             // tokens of the map in AGENTS.md
	RepoMapAgentsMinFiles = 25               // fewer source files than this: no section (the agent reads what it needs)
	repoMapAgentsTimeout  = 20 * time.Second // a first map of a huge repository must not hold up the task
)

// repoMapper is the Core's one Mapper: its cache is shared by the tool, AGENTS.md and the page.
func (c *Core) repoMapper() *repomap.Mapper {
	c.rmOnce.Do(func() { c.rm = repomap.New(repomap.Options{}) })
	return c.rm
}

// mapRepo maps root, the project's repository or one of its task worktrees ("": the repository). Files are listed by the
// project's own git (a repository inside a WSL distro by the distro's git), so .gitignore is honoured and git's
// configuration is checked like every other git call. The tags cache lives in the project's data folder and goes with it.
func (c *Core) mapRepo(ctx context.Context, projectID, root string, focus []string, budget int) (repomap.Result, error) {
	if !repomap.Available() {
		return repomap.Result{}, repomap.ErrNoParser
	}
	r, err := c.repo(ctx, projectID)
	if err != nil {
		return repomap.Result{}, err
	}
	if root == "" {
		root = r.Root
	}
	return c.repoMapper().Map(ctx, repomap.Request{
		Root: root, Focus: focus, TokenBudget: budget,
		List:     func(ctx context.Context, dir string) ([]string, error) { return r.ListFiles(ctx, dir) },
		CacheDir: filepath.Join(c.cfg.DataDir, "projects", projectID, "repomap"),
		// one cache file for the repository and all its task worktrees: they share most files, and a worktree is gone
		// after its task, so a file per worktree would be left behind for every task
		CacheKey: "repository",
	})
}

// mcpRepoMap backs the Workspace tool repo_map for an agent run: the map of the run's worktree.
type mcpRepoMap struct{ c *Core }

func (m mcpRepoMap) Map(ctx context.Context, run mcp.RunInfo, focus []string, budget int) (string, error) {
	if run.ProjectID == "" {
		return "", fmt.Errorf("%w: the repo map needs a project's repository", mcp.ErrNotAvailable)
	}
	res, err := m.c.mapRepo(ctx, run.ProjectID, run.Worktree, focus, budget)
	if err != nil {
		return "", err
	}
	if res.Map == "" {
		return emptyMapText(res), nil
	}
	return fmt.Sprintf("Repo map: %d of %d source files, most important first (about %d of %d tokens). Read code with read_range.\n\n%s",
		res.Shown, res.Files, res.Tokens, res.Budget, res.Map), nil
}

// emptyMapText says why a map came back empty: no source files it reads, or none of what it found fit the budget.
func emptyMapText(res repomap.Result) string {
	if res.Files == 0 {
		return "The repo map is empty: this repository has no source files in the languages it reads (" + strings.Join(repomap.Languages(), ", ") + ")."
	}
	return fmt.Sprintf("The repo map is empty: %d source files were read, but not even the most important definition fits in %d tokens. Ask again with a larger token_budget, or read code with read_range.", res.Files, res.Budget)
}

// repoMapSection is the AGENTS.md section of a worktree (context engine hook, ext_packs.go). A section with no body is
// recorded in the manifest as left out, with the reason.
func (c *Core) repoMapSection(ctx context.Context, projectID, worktree string) ctxeng.Section {
	sec := ctxeng.Section{Title: ctxeng.RepoMapSectionTitle, Source: "repo_map", Priority: 40}
	if !repomap.Available() {
		sec.Reason = repomap.NoParserMessage
		return sec
	}
	ctx, cancel := context.WithTimeout(ctx, repoMapAgentsTimeout)
	defer cancel()
	res, err := c.mapRepo(ctx, projectID, worktree, nil, RepoMapAgentsBudget)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		sec.Reason = fmt.Sprintf("not in AGENTS.md: mapping took longer than %s; the agent can ask for it with the repo_map tool", repoMapAgentsTimeout)
		return sec
	case err != nil:
		sec.Reason = "not in AGENTS.md: " + err.Error()
		return sec
	case res.Files < RepoMapAgentsMinFiles:
		sec.Reason = fmt.Sprintf("not in AGENTS.md: %d source files, fewer than %d (a small repository is read directly; the repo_map tool still answers)", res.Files, RepoMapAgentsMinFiles)
		return sec
	case res.Map == "":
		sec.Reason = "not in AGENTS.md: the map is empty"
		return sec
	}
	sec.Body = fmt.Sprintf("The most important code of this repository: %d of its %d source files, ranked by how much the rest of the code uses them (tree-sitter definitions and references, PageRank), about %d tokens. Each line is a definition; ⋮ marks code left out. For a bigger map, or one centred on the files you work on, call Drydock's repo_map tool with focus_paths.\n\n````text\n%s````",
		res.Shown, res.Files, res.Tokens, strings.TrimRight(res.Map, "\n")+"\n")
	sec.Reason = fmt.Sprintf("token-budgeted repo map (%d of %d tokens, %d of %d files; token rule 9)", res.Tokens, RepoMapAgentsBudget, res.Shown, res.Files)
	return sec
}

// RepoMapView is what the Repo map page shows.
type RepoMapView struct {
	Available bool            `json:"available"`
	Message   string          `json:"message,omitempty"` // why there is no map
	Languages []string        `json:"languages"`
	Result    *repomap.Result `json:"result,omitempty"`
}

func startRepoMap(_ context.Context, c *Core) (map[string]Method, func(), error) {
	return map[string]Method{
		// repomap.get maps the project's repository (its main checkout) for the person. Read-only.
		"repomap.get": bind(func(ctx context.Context, p struct {
			ProjectID   string   `json:"projectId"`
			Focus       []string `json:"focus"`
			TokenBudget int      `json:"tokenBudget"`
		}) (any, error) {
			if _, err := c.GetProject(ctx, p.ProjectID); err != nil {
				return nil, err
			}
			v := RepoMapView{Available: repomap.Available(), Languages: repomap.Languages()}
			if !v.Available {
				v.Message = repomap.NoParserMessage
				return v, nil
			}
			res, err := c.mapRepo(ctx, p.ProjectID, "", p.Focus, p.TokenBudget)
			if err != nil {
				return nil, err
			}
			v.Result = &res
			return v, nil
		}),
	}, nil, nil
}
