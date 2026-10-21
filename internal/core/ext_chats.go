package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/skills"
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

// chatAskTTL is how long a chat's permission request waits for the person (0: chats.AskTTL). Tests shorten it.
var chatAskTTL time.Duration

// Chats returns the chats service (nil before the extension started or after Close).
func (c *Core) Chats() *chats.Service {
	if v, ok := chatRegistry.Load(c); ok {
		return v.(*chats.Service)
	}
	return nil
}

// NeedsYou is the inbox (room.NeedsYou) plus every chat whose CLI waits for the person to allow a tool call, so the count in
// the top bar is everything that waits for a person. projectID "" lists all of them.
func (c *Core) NeedsYou(ctx context.Context, projectID string) ([]room.NeedsYouItem, error) {
	out, err := c.Room.NeedsYou(ctx, projectID)
	if err != nil {
		return nil, err
	}
	svc := c.Chats()
	if svc == nil {
		return out, nil
	}
	for _, a := range svc.Waiting(projectID) {
		title := firstNonEmpty(a.ChatTitle, "A chat")
		out = append(out, room.NeedsYouItem{Key: "chat-ask:" + a.ID, Kind: room.NeedsChatPermission, Title: "Asks to use " + a.Summary,
			Detail: "In the chat " + title, ProjectID: a.ProjectID, ChatID: a.ChatID, CreatedAt: time.UnixMilli(a.Created).UTC()})
	}
	room.SortNeedsYou(out)
	return out, nil
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
		CheckSkills:    c.checkChatSkills,
		CheckMCP:       c.checkChatMCP,
		Instructions:   c.chatInstructions,
		Licensed:       func(ctx context.Context) error { return c.RequireLicensed(ctx, "chat") },
		Logf:           c.logf,
	}, chats.Options{AskTTL: chatAskTTL})
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
		// permission requests a chat's CLI is waiting on (chats/permission.go); only the person's UI calls these methods
		"chat.asks": bind(func(ctx context.Context, p idOnly) (any, error) { return svc.Asks(ctx, p.ID) }),
		"chat.answer": bind(func(ctx context.Context, p struct {
			ID    string `json:"id"`
			AskID string `json:"askId"`
			Allow bool   `json:"allow"`
		}) (any, error) {
			return map[string]bool{"answered": true}, svc.Answer(ctx, p.ID, p.AskID, p.Allow)
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

// planChatMCP is the MCP plan of a chat session. A chat is not a team member, so it gets none of the project's Workspace tools
// or a project's servers. It gets what the person chose for it (decision P60): the read-only chats tool when it reads history,
// the skills tool serving the library skills chosen for it, and the library MCP servers granted to it with the tools named in
// each grant, under the gateway's usual rules (results scanned and marked untrusted, every tool not declared read-only behind
// an approval). With none of these the session has no MCP entry at all.
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
	allow := []string{}
	if ch.ReadHistory {
		allow = append(allow, "chats")
	}
	if len(ch.Skills) > 0 {
		allow = append(allow, "skills")
	}
	specs, grants, exposed := c.mcpServersForChat(ctx, ch.MCPServers)
	if len(allow) == 0 && len(specs) == 0 {
		return no("this chat reads no other chats and has no skills or MCP servers")
	}
	pl.Info = mcp.RunInfo{ProjectID: ch.ProjectID, AgentID: ch.ID, Role: "chat", Worktree: cfg.Spec.Cwd}
	pl.Policy = mcp.RunPolicy{Allow: map[string][]string{mcp.WorkspaceServerID: allow}}
	pl.Tools = append(append([]string(nil), allow...), mcp.ToolFetchRange)
	if len(specs) > 0 {
		pl.Servers = specs
		for id, tools := range grants {
			pl.Policy.Allow[id] = tools
		}
		pl.Tools = append(pl.Tools, exposed...)
	}
	pl.Attach = true
	return pl
}

// chatGateway refuses skills and MCP servers for a chat whose agent CLI cannot be given Drydock's MCP gateway: both reach the
// chat's session only through it.
func (c *Core) chatGateway(ctx context.Context, adapter string) error {
	if ok, canTry, why := adapterMCPKind(adapter); !ok && (!canTry || !c.mcpTryAllowed(ctx, adapter)) {
		return fmt.Errorf("skills and MCP servers reach a chat only through Drydock's MCP gateway, and %s", why)
	}
	return nil
}

// checkChatSkills is chats.Deps.CheckSkills: each chosen skill must be in the workspace library, available (not blocked) and
// still in its registry; two chosen skills may not share a name (the skills tool reads them by name).
func (c *Core) checkChatSkills(ctx context.Context, adapter string, refs []chats.SkillRef) ([]chats.SkillRef, error) {
	if err := c.chatGateway(ctx, adapter); err != nil {
		return nil, err
	}
	k := c.Knowledge()
	if k == nil || k.Skills == nil {
		return nil, errors.New("the skill library is not available")
	}
	byName := map[string]string{}
	out := []chats.SkillRef{}
	for _, r := range refs {
		r.RegistryID, r.Name = strings.TrimSpace(r.RegistryID), strings.TrimSpace(r.Name)
		if r.RegistryID == "" || r.Name == "" {
			return nil, errors.New("a skill is chosen by its registry and its name")
		}
		if reg, ok := byName[r.Name]; ok {
			if reg == r.RegistryID {
				continue
			}
			return nil, fmt.Errorf("two chosen skills are named %s (from %s and %s): choose one", r.Name, reg, r.RegistryID)
		}
		lk, _, err := k.Skills.LibraryGet(ctx, r.RegistryID, r.Name)
		if err != nil {
			return nil, err
		}
		switch {
		case lk.Gone:
			return nil, fmt.Errorf("the skill %s is no longer in its registry %s", r.Name, r.RegistryID)
		case !lk.Usable():
			return nil, fmt.Errorf("the skill %s is blocked in the library: %s", r.Name, firstNonEmpty(lk.BlockReason, "it did not pass the checks"))
		}
		byName[r.Name] = r.RegistryID
		out = append(out, r)
	}
	return out, nil
}

// checkChatMCP is chats.Deps.CheckMCP: each server must be connected in the workspace library. A grant that names no tools
// gets the server's tools as they are known now, by name, without the flagged ones (listing them first when they were never
// listed), as an agent's grant does; a tool the server adds later is not granted until the person chooses the server again.
func (c *Core) checkChatMCP(ctx context.Context, adapter string, grants []chats.MCPGrant) ([]chats.MCPGrant, error) {
	if err := c.chatGateway(ctx, adapter); err != nil {
		return nil, err
	}
	s := c.mcpServerSvc()
	if s == nil {
		return nil, errors.New("MCP servers are not available")
	}
	seen := map[string]bool{}
	out := []chats.MCPGrant{}
	for _, g := range grants {
		id := strings.TrimSpace(g.ServerID)
		if id == "" || seen[id] {
			continue
		}
		if _, err := s.load(ctx, id); err != nil {
			return nil, err
		}
		tools, err := cleanList(g.Tools, "tool")
		if err != nil {
			return nil, err
		}
		if len(tools) == 0 {
			if tools, err = s.grantable(ctx, id); err != nil {
				return nil, err
			}
		}
		seen[id] = true
		out = append(out, chats.MCPGrant{ServerID: id, Tools: tools})
	}
	return out, nil
}

// chatSkillList is the library skills chosen for a chat that can still reach it (a skill blocked or gone since is left out),
// in the order chosen, with their text.
func (c *Core) chatSkillList(ctx context.Context, ch chats.Chat) []skillWithText {
	k := c.Knowledge()
	if k == nil || k.Skills == nil {
		return nil
	}
	var out []skillWithText
	for _, r := range ch.Skills {
		lk, body, err := k.Skills.LibraryGet(ctx, r.RegistryID, r.Name)
		if err != nil || !lk.Usable() {
			continue
		}
		out = append(out, skillWithText{lk, body})
	}
	return out
}

type skillWithText struct {
	skills.LibrarySkill
	body string
}

// chatInstructions is chats.Deps.Instructions: the index of the chat's skills, for every turn's system prompt.
func (c *Core) chatInstructions(ctx context.Context, ch chats.Chat) string {
	var ks []skills.LibrarySkill
	for _, k := range c.chatSkillList(ctx, ch) {
		ks = append(ks, k.LibrarySkill)
	}
	return skills.ChatIndex(ks)
}

// chatSkillsTool serves the skills workspace tool. A chat's session (agent id chat_*, role "chat") gets the library skills
// chosen for the chat; every other run gets the project's accepted skills from the wrapped provider, as before.
type chatSkillsTool struct {
	c     *Core
	inner mcp.SkillsProvider
}

func isChatRun(run mcp.RunInfo) bool {
	return strings.HasPrefix(run.AgentID, chats.AgentPrefix) && run.Role == "chat"
}

func (p chatSkillsTool) chosen(ctx context.Context, run mcp.RunInfo) ([]skillWithText, error) {
	svc := p.c.Chats()
	if svc == nil {
		return nil, errors.New("chats are not running")
	}
	ch, err := svc.Get(ctx, run.AgentID)
	if err != nil {
		return nil, err
	}
	return p.c.chatSkillList(ctx, ch), nil
}

func chatSkill(k skillWithText, withBody bool) mcp.Skill {
	out := mcp.Skill{Name: k.Name, Description: k.Description, PinnedCommit: k.PinnedCommit}
	if withBody {
		out.Body = k.body
		if len(out.Body) > skills.MaxToolBodyChars {
			out.Body = strings.ToValidUTF8(out.Body[:skills.MaxToolBodyChars], "") + "\n[cut: the skill is longer than the tool returns]"
		}
	}
	return out
}

func (p chatSkillsTool) List(ctx context.Context, run mcp.RunInfo) ([]mcp.Skill, error) {
	if !isChatRun(run) {
		if p.inner == nil {
			return nil, errors.New("the skills library is not built yet")
		}
		return p.inner.List(ctx, run)
	}
	ks, err := p.chosen(ctx, run)
	if err != nil {
		return nil, err
	}
	out := []mcp.Skill{}
	for _, k := range ks {
		out = append(out, chatSkill(k, false))
	}
	return out, nil
}

func (p chatSkillsTool) Get(ctx context.Context, run mcp.RunInfo, name string) (mcp.Skill, error) {
	if !isChatRun(run) {
		if p.inner == nil {
			return mcp.Skill{}, errors.New("the skills library is not built yet")
		}
		return p.inner.Get(ctx, run, name)
	}
	ks, err := p.chosen(ctx, run)
	if err != nil {
		return mcp.Skill{}, err
	}
	for _, k := range ks {
		if k.Name == name {
			return chatSkill(k, true), nil
		}
	}
	return mcp.Skill{}, fmt.Errorf("%w: no skill named %q was chosen for this chat", mcp.ErrNotFound, name)
}

func (p chatSkillsTool) ForPaths(ctx context.Context, run mcp.RunInfo, paths []string) ([]mcp.Skill, error) {
	if !isChatRun(run) {
		if p.inner == nil {
			return nil, errors.New("the skills library is not built yet")
		}
		return p.inner.ForPaths(ctx, run, paths)
	}
	ks, err := p.chosen(ctx, run)
	if err != nil {
		return nil, err
	}
	var norm []string
	for _, x := range paths {
		if n, ok := skills.NormalizePath(x); ok {
			norm = append(norm, n)
		}
	}
	if len(norm) == 0 {
		return nil, errors.New("no valid paths relative to the chat's folder were given")
	}
	out := []mcp.Skill{}
	for _, k := range ks {
		if skills.PathsMatch(k.Globs, norm) {
			out = append(out, chatSkill(k, true))
		}
	}
	return out, nil
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
