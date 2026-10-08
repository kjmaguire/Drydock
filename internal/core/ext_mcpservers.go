package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// MCP servers the human adds to a project, and which of the project's agents may use them (docs/mcp.md, "user-added").
//
// The server is the human's choice: a command that starts a stdio server, or an https URL (plain http only on loopback). The
// gateway runs it under the process manager with a minimal environment, reaches it on behalf of the agent only when a row in
// agent_mcp_access grants it, shapes and scans every result as untrusted text, and parks every tool the human did not declare
// read-only behind an approval. Tokens and secret environment values go to the credential vault, never to the database, an
// event or a log; the database holds only that they exist.

func init() {
	RegisterExtension(Extension{Name: "mcpservers", After: []string{"mcp"}, Start: startMCPServers})
}

var (
	envNameRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	mcpToolRe  = regexp.MustCompile(`^[A-Za-z0-9_.*?\-]{1,100}$`)
	errNoVault = errors.New("this computer has no credential store to keep a secret in; Drydock will not store it anywhere else")
)

// MCPServerInput creates or changes a server. Secrets are write-only: an empty value keeps what is stored.
type MCPServerInput struct {
	ProjectID string `json:"projectId"`
	ID        string `json:"id"`
	Display   string `json:"display"`
	Transport string `json:"transport"` // stdio | http
	URL       string `json:"url"`
	// Argv starts a stdio server. Cwd and Env (not secret) are optional.
	Argv []string          `json:"argv"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
	// SecretEnv are environment variables whose values go to the vault. Names not listed here are removed from the server.
	SecretEnv map[string]string `json:"secretEnv"`
	// BearerToken authenticates to an http server. Empty keeps the stored one; ClearBearer removes it.
	BearerToken string `json:"bearerToken"`
	ClearBearer bool   `json:"clearBearer"`
	// Guard limits what agents may send to the server (no code, files, secrets or long text). Default on.
	Guard *bool `json:"guard"`
	// ReadOnlyTools are tools the human declares read-only; every other tool needs an approval for each call.
	ReadOnlyTools []string `json:"readOnlyTools"`
	Enabled       *bool    `json:"enabled"`
}

// MCPServerView is what the UI shows. It never carries a secret value.
type MCPServerView struct {
	ProjectID     string            `json:"projectId"`
	ID            string            `json:"id"`
	Display       string            `json:"display"`
	Transport     string            `json:"transport"`
	URL           string            `json:"url,omitempty"`
	Argv          []string          `json:"argv,omitempty"`
	Cwd           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	SecretEnv     []string          `json:"secretEnv"` // names only
	HasToken      bool              `json:"hasToken"`
	Guard         bool              `json:"guard"`
	ReadOnlyTools []string          `json:"readOnlyTools"`
	Tools         []mcp.ToolInfo    `json:"tools"`
	DiscoveredAt  int64             `json:"discoveredAt"`
	Enabled       bool              `json:"enabled"`
	// Agents are the agent ids that may use the server, with the tool globs each may call.
	Agents    []MCPAccess `json:"agents"`
	CreatedAt int64       `json:"createdAt"`
	UpdatedAt int64       `json:"updatedAt"`
}

// MCPAccess is one agent's grant.
type MCPAccess struct {
	AgentID string   `json:"agentId"`
	Tools   []string `json:"tools"`
}

func mcpVaultRef(project, server, kind string) string {
	return "mcp:" + project + ":" + server + ":" + kind
}

type mcpServers struct{ c *Core }

var mcpServerServices sync.Map // *Core -> *mcpServers

func (c *Core) mcpServerSvc() *mcpServers {
	if v, ok := mcpServerServices.Load(c); ok {
		return v.(*mcpServers)
	}
	return nil
}

// lateMCP is what the shared gateway is built with (ext_mcp.go): it reaches the MCP-servers service when a call is made, so the
// order in which the two extensions start does not matter. Without the service a credential is unavailable and a write is refused.
type lateMCP struct{ c *Core }

func (l lateMCP) Credential(ctx context.Context, serverID, ref string) (string, error) {
	s := l.c.mcpServerSvc()
	if s == nil {
		return "", errors.New("MCP servers are not available")
	}
	return s.credential(ctx, serverID, ref)
}

func (l lateMCP) Approve(ctx context.Context, req mcp.ApprovalRequest) (bool, error) {
	s := l.c.mcpServerSvc()
	if s == nil {
		return false, errors.New("MCP approvals are not available")
	}
	return s.Approve(ctx, req)
}

func startMCPServers(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	s := &mcpServers{c: c}
	mcpServerServices.Store(c, s)
	// teach the scanner the exact secret values again after a restart, so tool results cannot echo them back
	s.registerSecrets(ctx)
	return map[string]Method{
		"mcpserver.list": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return s.list(ctx, p.ProjectID)
		}),
		"mcpserver.save": bind(func(ctx context.Context, p MCPServerInput) (any, error) { return s.save(ctx, p) }),
		"mcpserver.delete": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			ID        string `json:"id"`
		}) (any, error) {
			return nil, s.delete(ctx, p.ProjectID, p.ID)
		}),
		"mcpserver.discover": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			ID        string `json:"id"`
		}) (any, error) {
			return s.discover(ctx, p.ProjectID, p.ID)
		}),
		"mcpserver.access.set": bind(func(ctx context.Context, p struct {
			ProjectID string      `json:"projectId"`
			ServerID  string      `json:"serverId"`
			Agents    []MCPAccess `json:"agents"`
		}) (any, error) {
			return s.setAccess(ctx, p.ProjectID, p.ServerID, p.Agents)
		}),
		"mcpserver.approval.list": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return s.pendingApprovals(ctx, p.ProjectID)
		}),
		"mcpserver.approval.decide": bind(func(ctx context.Context, p struct {
			ApprovalID string `json:"approvalId"`
			Approve    bool   `json:"approve"`
		}) (any, error) {
			return nil, s.decide(ctx, p.ApprovalID, p.Approve)
		}),
	}, nil, nil
}

// ---- storage ----

type mcpRow struct {
	MCPServerView
	argvJSON, envJSON, secretJSON, roJSON, toolsJSON string
}

func (s *mcpServers) load(ctx context.Context, project, id string) (mcpRow, error) {
	var r mcpRow
	var tok, guard, en int
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT project_id,id,display,transport,url,argv,cwd,env,secret_env,has_token,guard,read_only_tools,tools,discovered_at,enabled,created_at,updated_at
FROM project_mcp_servers WHERE project_id=? AND id=?`, project, id).Scan(&r.ProjectID, &r.ID, &r.Display, &r.Transport, &r.URL, &r.argvJSON, &r.Cwd, &r.envJSON, &r.secretJSON,
		&tok, &guard, &r.roJSON, &r.toolsJSON, &r.DiscoveredAt, &en, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("there is no MCP server %q in this project", id)
	}
	if err != nil {
		return r, err
	}
	r.HasToken, r.Guard, r.Enabled = tok != 0, guard != 0, en != 0
	_ = json.Unmarshal([]byte(r.argvJSON), &r.Argv)
	_ = json.Unmarshal([]byte(r.envJSON), &r.Env)
	_ = json.Unmarshal([]byte(r.secretJSON), &r.SecretEnv)
	_ = json.Unmarshal([]byte(r.roJSON), &r.ReadOnlyTools)
	_ = json.Unmarshal([]byte(r.toolsJSON), &r.Tools)
	if r.SecretEnv == nil {
		r.SecretEnv = []string{}
	}
	if r.ReadOnlyTools == nil {
		r.ReadOnlyTools = []string{}
	}
	if r.Tools == nil {
		r.Tools = []mcp.ToolInfo{}
	}
	return r, nil
}

func (s *mcpServers) access(ctx context.Context, project, server string) []MCPAccess {
	out := []MCPAccess{}
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT agent_id, tools FROM agent_mcp_access WHERE project_id=? AND server_id=? ORDER BY agent_id`, project, server)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var a MCPAccess
		var tj string
		if rows.Scan(&a.AgentID, &tj) == nil {
			_ = json.Unmarshal([]byte(tj), &a.Tools)
			out = append(out, a)
		}
	}
	return out
}

func (s *mcpServers) list(ctx context.Context, project string) ([]MCPServerView, error) {
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT id FROM project_mcp_servers WHERE project_id=? ORDER BY id`, project)
	if err != nil {
		return nil, err
	}
	var idsList []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			idsList = append(idsList, id)
		}
	}
	rows.Close()
	out := []MCPServerView{}
	for _, id := range idsList {
		r, err := s.load(ctx, project, id)
		if err != nil {
			return nil, err
		}
		r.Agents = s.access(ctx, project, id)
		out = append(out, r.MCPServerView)
	}
	return out, nil
}

// spec builds the gateway's description of a server. Credentials are referenced, never inlined.
func (r mcpRow) spec() mcp.ServerSpec {
	sp := mcp.ServerSpec{
		ID: r.ID, Display: r.Display, Transport: r.Transport, URL: r.URL,
		Auth: mcp.AuthSpec{Kind: mcp.AuthNone}, Trust: mcp.TrustPrivateData, DataClass: mcp.ContentPrivate,
		ReadOnlyTools: append([]string(nil), r.ReadOnlyTools...),
		Cache:         mcp.CacheSpec{Scope: "project"}, Shape: mcp.ShapeSpec{MaxInlineBytes: 8192},
		License: mcp.LicenseSpec{Server: "user-provided"}, TermsStatus: "unverified",
	}
	if r.Transport == mcp.TransportStdio {
		cs := &mcp.CommandSpec{Argv: append([]string(nil), r.Argv...), Cwd: r.Cwd, Env: map[string]string{}}
		for k, v := range r.Env {
			cs.Env[k] = v
		}
		if len(r.SecretEnv) > 0 {
			cs.SecretEnv = map[string]string{}
			for _, name := range r.SecretEnv {
				cs.SecretEnv[name] = mcpVaultRef(r.ProjectID, r.ID, "env:"+name)
			}
		}
		sp.Command = cs
	}
	if r.HasToken {
		sp.Auth = mcp.AuthSpec{Kind: mcp.AuthBearer, Keyring: mcpVaultRef(r.ProjectID, r.ID, "token")}
	}
	if r.Guard {
		sp.Egress = &mcp.EgressSpec{MaxArgBytes: 4096, ScanSecrets: true, DenyCodeFences: true}
	}
	for _, t := range r.Tools {
		sp.Tools = append(sp.Tools, mcp.ToolDecl{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return sp
}

// credential is the gateway's CredentialSource: the keyring reference names a vault entry.
func (s *mcpServers) credential(ctx context.Context, serverID, ref string) (string, error) {
	if s.c.secrets == nil {
		return "", errNoVault
	}
	if !strings.HasPrefix(ref, "mcp:") {
		return "", fmt.Errorf("unknown credential reference for %s", serverID)
	}
	return s.c.secrets.Get(ctx, ref)
}

func (s *mcpServers) registerSecrets(ctx context.Context) {
	if s.c.secrets == nil || s.c.Scanner == nil {
		return
	}
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT project_id,id,has_token,secret_env FROM project_mcp_servers`)
	if err != nil {
		return
	}
	type ref struct{ name, ref string }
	var refs []ref
	for rows.Next() {
		var p, id, sj string
		var tok int
		if rows.Scan(&p, &id, &tok, &sj) != nil {
			continue
		}
		if tok != 0 {
			refs = append(refs, ref{"mcp-" + id + "-token", mcpVaultRef(p, id, "token")})
		}
		var names []string
		_ = json.Unmarshal([]byte(sj), &names)
		for _, n := range names {
			refs = append(refs, ref{"mcp-" + id + "-" + n, mcpVaultRef(p, id, "env:"+n)})
		}
	}
	rows.Close()
	for _, r := range refs {
		if v, err := s.c.secrets.Get(ctx, r.ref); err == nil && v != "" {
			_ = s.c.Scanner.RegisterSecret(r.name, v)
		}
	}
}

// ---- save / delete ----

func cleanList(in []string, what string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range in {
		x = strings.TrimSpace(x)
		if x == "" || seen[x] {
			continue
		}
		if !mcpToolRe.MatchString(x) {
			return nil, fmt.Errorf("%s %q is not a tool name", what, x)
		}
		seen[x] = true
		out = append(out, x)
	}
	sort.Strings(out)
	return out, nil
}

func (s *mcpServers) save(ctx context.Context, in MCPServerInput) (MCPServerView, error) {
	if err := s.c.RequireLicensed(ctx, "add_connection"); err != nil {
		return MCPServerView{}, err
	}
	if _, err := s.c.GetProject(ctx, in.ProjectID); err != nil {
		return MCPServerView{}, err
	}
	in.ID = strings.TrimSpace(in.ID)
	existing, err := s.load(ctx, in.ProjectID, in.ID)
	isNew := err != nil
	if isNew {
		existing = mcpRow{MCPServerView: MCPServerView{ProjectID: in.ProjectID, ID: in.ID, Guard: true, Enabled: true, SecretEnv: []string{}}}
	}
	r := existing
	r.Display, r.Transport, r.URL = strings.TrimSpace(in.Display), strings.TrimSpace(in.Transport), strings.TrimSpace(in.URL)
	r.Argv, r.Cwd = nil, strings.TrimSpace(in.Cwd)
	for _, a := range in.Argv {
		if strings.TrimSpace(a) != "" {
			r.Argv = append(r.Argv, a)
		}
	}
	r.Env = map[string]string{}
	for k, v := range in.Env {
		k = strings.TrimSpace(k)
		if !envNameRe.MatchString(k) {
			return MCPServerView{}, fmt.Errorf("%q is not a valid environment variable name", k)
		}
		r.Env[k] = v
	}
	if in.Guard != nil {
		r.Guard = *in.Guard
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	if r.ReadOnlyTools, err = cleanList(in.ReadOnlyTools, "read-only tool"); err != nil {
		return MCPServerView{}, err
	}
	if r.Transport == mcp.TransportHTTP {
		r.Argv, r.Cwd = nil, ""
	} else {
		r.URL = ""
	}

	// secrets: names listed in SecretEnv are kept; their non-empty values replace the stored ones; unlisted names are removed
	var removeRefs []string
	newNames := []string{}
	putSecrets := map[string]string{}
	for name, val := range in.SecretEnv {
		name = strings.TrimSpace(name)
		if !envNameRe.MatchString(name) {
			return MCPServerView{}, fmt.Errorf("%q is not a valid environment variable name", name)
		}
		if _, dup := r.Env[name]; dup {
			return MCPServerView{}, fmt.Errorf("%s is both a plain and a secret environment variable", name)
		}
		newNames = append(newNames, name)
		known := false
		for _, old := range existing.SecretEnv {
			known = known || old == name
		}
		if val != "" {
			putSecrets[mcpVaultRef(r.ProjectID, r.ID, "env:"+name)] = val
		} else if !known {
			return MCPServerView{}, fmt.Errorf("give a value for the secret variable %s", name)
		}
	}
	sort.Strings(newNames)
	for _, old := range existing.SecretEnv {
		kept := false
		for _, n := range newNames {
			kept = kept || n == old
		}
		if !kept {
			removeRefs = append(removeRefs, mcpVaultRef(r.ProjectID, r.ID, "env:"+old))
		}
	}
	r.SecretEnv = newNames
	switch {
	case in.ClearBearer:
		if existing.HasToken {
			removeRefs = append(removeRefs, mcpVaultRef(r.ProjectID, r.ID, "token"))
		}
		r.HasToken = false
	case strings.TrimSpace(in.BearerToken) != "":
		putSecrets[mcpVaultRef(r.ProjectID, r.ID, "token")] = strings.TrimSpace(in.BearerToken)
		r.HasToken = true
	}
	if r.Transport != mcp.TransportHTTP && (r.HasToken || in.BearerToken != "") {
		return MCPServerView{}, errors.New("a bearer token is for http servers; a stdio server gets secrets through environment variables")
	}
	if r.Transport == mcp.TransportHTTP && len(r.SecretEnv) > 0 {
		return MCPServerView{}, errors.New("secret environment variables are for stdio servers; an http server uses a bearer token")
	}
	if len(putSecrets) > 0 && s.c.secrets == nil {
		return MCPServerView{}, errNoVault
	}

	// the gateway's own validation decides whether the server is acceptable (id, url, command, secrets in arguments, ...)
	sp := r.spec()
	if err := sp.Normalize(); err != nil {
		return MCPServerView{}, err
	}
	// a changed command or url invalidates the discovered tools and the human's read-only declarations stay as typed
	if !isNew && (existing.Transport != r.Transport || existing.URL != r.URL || strings.Join(existing.Argv, "\x00") != strings.Join(r.Argv, "\x00")) {
		r.Tools, r.DiscoveredAt = []mcp.ToolInfo{}, 0
	}

	for ref, val := range putSecrets {
		if err := s.c.secrets.Put(ctx, ref, val); err != nil {
			return MCPServerView{}, fmt.Errorf("could not keep the secret: %w", err)
		}
		if s.c.Scanner != nil {
			_ = s.c.Scanner.RegisterSecret(strings.NewReplacer(":", "-").Replace(ref), val)
		}
	}
	argv, _ := json.Marshal(nonNil(r.Argv))
	env, _ := json.Marshal(r.Env)
	sec, _ := json.Marshal(r.SecretEnv)
	ro, _ := json.Marshal(r.ReadOnlyTools)
	tools, _ := json.Marshal(r.Tools)
	now := storage.Now()
	err = s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO project_mcp_servers(project_id,id,display,transport,url,argv,cwd,env,secret_env,has_token,guard,read_only_tools,tools,discovered_at,enabled,created_by,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(project_id,id) DO UPDATE SET display=excluded.display, transport=excluded.transport, url=excluded.url, argv=excluded.argv, cwd=excluded.cwd, env=excluded.env,
 secret_env=excluded.secret_env, has_token=excluded.has_token, guard=excluded.guard, read_only_tools=excluded.read_only_tools, tools=excluded.tools,
 discovered_at=excluded.discovered_at, enabled=excluded.enabled, updated_at=excluded.updated_at`,
			r.ProjectID, r.ID, r.Display, r.Transport, r.URL, string(argv), r.Cwd, string(env), string(sec), b2i(r.HasToken), b2i(r.Guard), string(ro), string(tools), r.DiscoveredAt, b2i(r.Enabled), s.c.Human(), now, now)
		return err
	})
	if err != nil {
		return MCPServerView{}, err
	}
	for _, ref := range removeRefs {
		if s.c.secrets != nil {
			_ = s.c.secrets.Delete(ctx, ref)
		}
	}
	if gw := s.c.MCPGateway(); gw != nil {
		gw.StopUpstream(r.ID) // a changed command, url or credential must take effect on the next call
	}
	s.c.publish(ctx, "mcp.server_saved", r.ProjectID, map[string]any{"id": r.ID, "transport": r.Transport, "new": isNew, "by": s.c.Human()})
	out, err := s.load(ctx, r.ProjectID, r.ID)
	if err != nil {
		return MCPServerView{}, err
	}
	out.Agents = s.access(ctx, r.ProjectID, r.ID)
	return out.MCPServerView, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *mcpServers) delete(ctx context.Context, project, id string) error {
	r, err := s.load(ctx, project, id)
	if err != nil {
		return err
	}
	err = s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_mcp_access WHERE project_id=? AND server_id=?`, project, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM project_mcp_servers WHERE project_id=? AND id=?`, project, id)
		return err
	})
	if err != nil {
		return err
	}
	if s.c.secrets != nil {
		if r.HasToken {
			_ = s.c.secrets.Delete(ctx, mcpVaultRef(project, id, "token"))
		}
		for _, n := range r.SecretEnv {
			_ = s.c.secrets.Delete(ctx, mcpVaultRef(project, id, "env:"+n))
		}
	}
	if gw := s.c.MCPGateway(); gw != nil {
		gw.StopUpstream(id)
	}
	s.c.publish(ctx, "mcp.server_removed", project, map[string]any{"id": id, "by": s.c.Human()})
	return nil
}

// ---- discovery ----

// discover starts the server (or connects to it), lists its tools and stores them, so agents see the tools without waiting for
// the server to start. It is also the "test connection" of the page.
func (s *mcpServers) discover(ctx context.Context, project, id string) (MCPServerView, error) {
	gw := s.c.MCPGateway()
	if gw == nil {
		return MCPServerView{}, errors.New("the MCP gateway is not running")
	}
	r, err := s.load(ctx, project, id)
	if err != nil {
		return MCPServerView{}, err
	}
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	sp := r.spec()
	sp.Tools = nil
	up, err := gw.AppUpstream(tctx, sp)
	if err != nil {
		return MCPServerView{}, fmt.Errorf("could not reach %s: %s", r.Display, s.c.scrubSecrets(err.Error()))
	}
	tools, err := up.ToolList(tctx)
	if err != nil {
		return MCPServerView{}, fmt.Errorf("could not list the tools of %s: %s", r.Display, s.c.scrubSecrets(err.Error()))
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	tj, _ := json.Marshal(tools)
	now := storage.Now()
	err = s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE project_mcp_servers SET tools=?, discovered_at=?, updated_at=? WHERE project_id=? AND id=?`, string(tj), now, now, project, id)
		return err
	})
	if err != nil {
		return MCPServerView{}, err
	}
	s.c.publish(ctx, "mcp.server_discovered", project, map[string]any{"id": id, "tools": len(tools)})
	out, err := s.load(ctx, project, id)
	if err != nil {
		return MCPServerView{}, err
	}
	out.Agents = s.access(ctx, project, id)
	return out.MCPServerView, nil
}

func (c *Core) scrubSecrets(s string) string {
	if c.Scanner == nil {
		return s
	}
	return c.Scanner.Redact(s)
}

// ---- access ----

func (s *mcpServers) setAccess(ctx context.Context, project, server string, grants []MCPAccess) (MCPServerView, error) {
	if _, err := s.load(ctx, project, server); err != nil {
		return MCPServerView{}, err
	}
	seen := map[string]bool{}
	type row struct{ agent, tools string }
	var rows []row
	for _, g := range grants {
		a, err := s.c.agent(ctx, g.AgentID)
		if err != nil || a.ProjectID != project || a.Archived {
			return MCPServerView{}, fmt.Errorf("agent %s is not on this project's team", g.AgentID)
		}
		if seen[g.AgentID] {
			continue
		}
		seen[g.AgentID] = true
		tools, err := cleanList(g.Tools, "tool")
		if err != nil {
			return MCPServerView{}, err
		}
		if len(tools) == 0 {
			tools = []string{"*"}
		}
		tj, _ := json.Marshal(tools)
		rows = append(rows, row{g.AgentID, string(tj)})
	}
	now := storage.Now()
	err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_mcp_access WHERE project_id=? AND server_id=?`, project, server); err != nil {
			return err
		}
		for _, r := range rows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_mcp_access(project_id,server_id,agent_id,tools,granted_by,granted_at) VALUES(?,?,?,?,?,?)`, project, server, r.agent, r.tools, s.c.Human(), now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return MCPServerView{}, err
	}
	s.c.publish(ctx, "mcp.access_changed", project, map[string]any{"server": server, "agents": len(rows), "by": s.c.Human()})
	out, err := s.load(ctx, project, server)
	if err != nil {
		return MCPServerView{}, err
	}
	out.Agents = s.access(ctx, project, server)
	return out.MCPServerView, nil
}

// forRun returns the servers an agent may use in a run and the tool globs it may call on each. Disabled servers and servers
// without a grant are not included.
func (c *Core) mcpServersForRun(ctx context.Context, project, agentID string) ([]mcp.ServerSpec, map[string][]string, []string) {
	s := c.mcpServerSvc()
	if s == nil {
		return nil, nil, nil
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT s.id, a.tools FROM project_mcp_servers s JOIN agent_mcp_access a ON a.project_id=s.project_id AND a.server_id=s.id
WHERE s.project_id=? AND a.agent_id=? AND s.enabled=1 ORDER BY s.id`, project, agentID)
	if err != nil {
		return nil, nil, nil
	}
	type g struct {
		id    string
		tools []string
	}
	var grants []g
	for rows.Next() {
		var x g
		var tj string
		if rows.Scan(&x.id, &tj) == nil {
			_ = json.Unmarshal([]byte(tj), &x.tools)
			grants = append(grants, x)
		}
	}
	rows.Close()
	var specs []mcp.ServerSpec
	allow := map[string][]string{}
	var exposed []string
	for _, x := range grants {
		r, err := s.load(ctx, project, x.id)
		if err != nil {
			continue
		}
		sp := r.spec()
		if err := sp.Normalize(); err != nil {
			continue // a stored server that no longer validates is left out rather than failing the run
		}
		specs = append(specs, sp)
		allow[sp.ID] = x.tools
		for _, t := range sp.Tools {
			exposed = append(exposed, sp.ID+mcp.ToolSeparator+t.Name)
		}
		if len(sp.Tools) == 0 {
			exposed = append(exposed, sp.ID+mcp.ToolSeparator+"*")
		}
	}
	return specs, allow, exposed
}

// ---- approvals of write-class calls ----

func (s *mcpServers) approvals() *security.Approvals {
	return security.NewApprovals(s.c.DB, s.c.Bus, security.NewAudit(s.c.DB, s.c.Scanner), security.WithApprovalRedactor(s.c.Scanner))
}

const mcpApprovalKind = "mcp_call"

// Approve is the gateway's Approver: it asks the human, in Needs you and on the MCP page, whether this exact call may be made,
// and waits for the answer. The agent's tool call stays pending meanwhile; an expired, rejected or cancelled approval refuses it.
func (s *mcpServers) Approve(ctx context.Context, req mcp.ApprovalRequest) (bool, error) {
	ap := s.approvals()
	args := string(req.Args)
	if len(args) > 2000 {
		args = args[:2000] + "...[truncated]"
	}
	action := security.ActionDescriptor{
		Kind: mcpApprovalKind, Project: req.Run.ProjectID, TaskID: req.Run.TaskID, RunID: req.Run.RunID, AgentID: req.Run.AgentID, Cwd: req.Run.Worktree,
		Tool: "mcp:" + req.Server + "/" + req.Tool, Command: args, ArgvDigest: req.ArgsHash,
	}
	id, err := ap.Request(ctx, action, "An agent wants to call a tool that is not marked read-only. Approve only if you want this exact call made.")
	if err != nil {
		return false, err
	}
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	for {
		info, err := ap.Get(ctx, id)
		if err != nil {
			return false, err
		}
		switch info.Status {
		case "REQUESTED":
			if info.ExpiresAt > 0 && time.Now().UnixMilli() > info.ExpiresAt {
				_, _ = ap.ExpireDue(ctx)
			}
		case "APPROVED":
			if err := ap.Consume(ctx, id, action); err != nil {
				return false, err
			}
			return true, nil
		default:
			return false, nil
		}
		select {
		case <-ctx.Done():
			_ = ap.Cancel(ctx, id, "system")
			return false, ctx.Err()
		case <-tick.C:
		}
	}
}

// MCPApproval is one pending call for the MCP page.
type MCPApproval struct {
	ID        string `json:"id"`
	Server    string `json:"server"`
	Tool      string `json:"tool"`
	AgentID   string `json:"agentId"`
	TaskID    string `json:"taskId,omitempty"`
	Args      string `json:"args"`
	Created   int64  `json:"created"`
	Expires   int64  `json:"expiresAt"`
	ProjectID string `json:"projectId"`
}

func (s *mcpServers) pendingApprovals(ctx context.Context, project string) ([]MCPApproval, error) {
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT id FROM approvals WHERE status='REQUESTED' AND action_kind=? AND (?='' OR project_id=?) ORDER BY created_at`, mcpApprovalKind, project, project)
	if err != nil {
		return nil, err
	}
	var idsList []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			idsList = append(idsList, id)
		}
	}
	rows.Close()
	ap := s.approvals()
	out := []MCPApproval{}
	for _, id := range idsList {
		info, err := ap.Get(ctx, id)
		if err != nil {
			continue
		}
		server, tool, _ := strings.Cut(strings.TrimPrefix(info.Action.Tool, "mcp:"), "/")
		out = append(out, MCPApproval{ID: id, Server: server, Tool: tool, AgentID: info.Action.AgentID, TaskID: info.Action.TaskID, Args: info.Action.Command,
			Created: info.Created, Expires: info.ExpiresAt, ProjectID: info.Action.Project})
	}
	return out, nil
}

func (s *mcpServers) decide(ctx context.Context, approvalID string, approve bool) error {
	ap := s.approvals()
	info, err := ap.Get(ctx, approvalID)
	if err != nil {
		return err
	}
	if info.ActionKind != mcpApprovalKind {
		return errors.New("that approval is not for an MCP tool call")
	}
	d := security.DecisionReject
	if approve {
		d = security.DecisionApprove
	}
	return ap.Decide(ctx, approvalID, d, s.c.Human())
}
