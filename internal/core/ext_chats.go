package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/mcp"
)

// This extension is the Chats section (decisions P29): the human's own conversations with an agent CLI, kept as history that
// can be read, searched, continued in the CLI's session, forked, and read by later chats. The service is internal/chats; this
// file wires it to the core (adapters, Connection gates, project folders, the secret scanner, the license) and registers the
// chat.* methods. It changes none of the base services.
//
// The chat.* methods and events are specified once, in docs/chats.md section 2 (parameters, results, events); keep that file in
// step with the bindings below.

func init() {
	RegisterExtension(Extension{Name: "chats", Start: startChats})
}

var chatRegistry sync.Map // *Core -> *chats.Service

// Chats returns the chats service (nil before the extension started or after Close).
func (c *Core) Chats() *chats.Service {
	if v, ok := chatRegistry.Load(c); ok {
		return v.(*chats.Service)
	}
	return nil
}

func startChats(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	svc := chats.New(chats.Deps{
		DB: c.DB, Bus: c.Bus, Runtime: c.Runtime,
		Redact: c.Scanner.Redact,
		Capabilities: func(id string) (agents.Capabilities, bool) {
			c.mu.Lock()
			a, ok := c.adapters[id]
			c.mu.Unlock()
			if !ok {
				return agents.Capabilities{}, false
			}
			return a.Capabilities(), true
		},
		CheckAdapter:   c.checkAdapterGates,
		ValidateFields: func(adapter, model, effort string) error { return c.validateAgentFields(adapter, model, effort, "") },
		ProjectDir:     c.chatProjectDir,
		ScratchDir:     func(id string) string { return filepath.Join(c.cfg.DataDir, "chats", id) },
		CheckWSL:       c.checkChatWSL,
		WSLFolder:      c.chatWSLFolder,
		Licensed:       func(ctx context.Context) error { return c.RequireLicensed(ctx, "chat") },
		Logf:           c.logf,
	}, chats.Options{})
	if err := svc.Start(ctx); err != nil {
		return nil, nil, err
	}
	chatRegistry.Store(c, svc)
	closeFn := func() {
		svc.Close()
		chatRegistry.Delete(c)
	}
	type idOnly struct {
		ID string `json:"id"`
	}
	type ruleParam struct {
		ID   string `json:"id"`
		Tool string `json:"tool"`
	}
	m := map[string]Method{
		"chat.list":   bind(func(ctx context.Context, p chats.ListReq) (any, error) { return svc.List(ctx, p) }),
		"chat.create": bind(func(ctx context.Context, p chats.CreateReq) (any, error) { return svc.Create(ctx, p) }),
		"chat.get":    bind(func(ctx context.Context, p idOnly) (any, error) { return svc.Detail(ctx, p.ID, 200) }),
		"chat.messages": bind(func(ctx context.Context, p struct {
			ID        string `json:"id"`
			BeforeSeq int64  `json:"beforeSeq"`
			Limit     int    `json:"limit"`
		}) (any, error) {
			ms, more, err := svc.Messages(ctx, p.ID, p.BeforeSeq, p.Limit)
			if err != nil {
				return nil, err
			}
			return chats.Page{Messages: ms, HasMore: more}, nil
		}),
		"chat.send": bind(func(ctx context.Context, p chats.SendReq) (any, error) {
			res, err := svc.Send(ctx, p)
			if err != nil {
				return nil, err
			}
			return res, nil
		}),
		"chat.stop": bind(func(ctx context.Context, p idOnly) (any, error) {
			ok, err := svc.Stop(ctx, p.ID)
			return map[string]bool{"stopped": ok}, err
		}),
		"chat.update": bind(func(ctx context.Context, p chats.UpdateReq) (any, error) { return svc.Update(ctx, p) }),
		"chat.delete": bind(func(ctx context.Context, p idOnly) (any, error) {
			if err := svc.Delete(ctx, p.ID); err != nil {
				return nil, err
			}
			return map[string]bool{"deleted": true}, nil
		}),
		"chat.search": bind(func(ctx context.Context, p struct {
			Query     string `json:"query"`
			ProjectID string `json:"projectId"`
			Limit     int    `json:"limit"`
		}) (any, error) {
			return svc.Search(ctx, p.Query, p.ProjectID, p.Limit)
		}),
		"chat.fork": bind(func(ctx context.Context, p struct {
			ID      string `json:"id"`
			UptoSeq int64  `json:"uptoSeq"`
		}) (any, error) {
			return svc.Fork(ctx, p.ID, p.UptoSeq)
		}),
		"chat.allowTool":    bind(func(ctx context.Context, p ruleParam) (any, error) { return svc.AllowTool(ctx, p.ID, p.Tool) }),
		"chat.disallowTool": bind(func(ctx context.Context, p ruleParam) (any, error) { return svc.DisallowTool(ctx, p.ID, p.Tool) }),
	}
	return m, closeFn, nil
}

// checkAdapterGates refuses an adapter whose Connection has a gate the human has not acknowledged (as CreateAgent does).
func (c *Core) checkAdapterGates(ctx context.Context, adapter string) error {
	conn, err := c.Connections.Check(ctx, adapter)
	if err != nil {
		return nil // an adapter without a manifest (a test agent) has no gates
	}
	for _, g := range conn.Gates {
		if !g.Acked {
			return fmt.Errorf("%s needs your acknowledgement first (%s): %s", adapter, g.ID, g.Text)
		}
	}
	return nil
}

// chatProjectDir is the folder a project's chats run in: the repository root, or the project's own workspace folder when the
// project has no repository.
func (c *Core) chatProjectDir(ctx context.Context, projectID string) (string, error) {
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	if p.RepoPath != "" {
		st, err := os.Stat(p.RepoPath)
		if err != nil || !st.IsDir() {
			return "", fmt.Errorf("the project's folder %s is not available", p.RepoPath)
		}
		return p.RepoPath, nil
	}
	return c.projectDir(p)
}

// planChatMCP is the MCP plan of a chat session: only the read-only chats tool, and only when the chat reads history. A chat is
// not a team member, so it gets none of the project's Workspace tools or library servers.
func (c *Core) planChatMCP(ctx context.Context, cfg *rt.Config) mcpPlan {
	pl := mcpPlan{DataClass: mcp.DataClassPrivate}
	no := func(format string, a ...any) mcpPlan { pl.Reason = fmt.Sprintf(format, a...); return pl }
	if c.MCPGateway() == nil {
		return no("the MCP gateway is not running")
	}
	if ok, canTry, why := adapterMCPKind(cfg.Adapter); !ok {
		if !canTry || !c.mcpTryAllowed(ctx, cfg.Adapter) {
			return no("%s", why)
		}
		pl.Unverified = true
	}
	svc := c.Chats()
	if svc == nil {
		return no("chats are not running")
	}
	ch, err := svc.Get(ctx, cfg.AgentID)
	if err != nil {
		return no("unknown chat %q", cfg.AgentID)
	}
	if !ch.ReadHistory {
		return no("reading earlier chats is switched off for this chat")
	}
	pl.Info = mcp.RunInfo{ProjectID: ch.ProjectID, AgentID: ch.ID, Role: "chat", Worktree: cfg.Spec.Cwd}
	pl.Policy = mcp.RunPolicy{Allow: map[string][]string{mcp.WorkspaceServerID: {"chats"}}}
	pl.Tools = []string{"chats", mcp.ToolFetchRange}
	pl.Attach = true
	return pl
}

// mcpChats backs the chats workspace tool with the chats service; the calling chat is RunInfo.AgentID.
type mcpChats struct{ c *Core }

func (m mcpChats) svc() (*chats.Service, error) {
	if s := m.c.Chats(); s != nil {
		return s, nil
	}
	return nil, errors.New("chats are not running")
}

func (m mcpChats) List(ctx context.Context, run mcp.RunInfo, limit int) (any, error) {
	s, err := m.svc()
	if err != nil {
		return nil, err
	}
	return s.ToolList(ctx, run.AgentID, limit)
}

func (m mcpChats) Search(ctx context.Context, run mcp.RunInfo, query string, k int) (any, error) {
	s, err := m.svc()
	if err != nil {
		return nil, err
	}
	return s.ToolSearch(ctx, run.AgentID, query, k)
}

func (m mcpChats) Read(ctx context.Context, run mcp.RunInfo, chatID string, fromSeq, limit int) (any, error) {
	s, err := m.svc()
	if err != nil {
		return nil, err
	}
	return s.ToolRead(ctx, run.AgentID, chatID, int64(fromSeq), limit)
}
