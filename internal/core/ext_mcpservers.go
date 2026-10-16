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
	"unicode"

	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// MCP servers the human connects once in the workspace library, puts into projects, and grants to agents (docs/mcp.md,
// "User-added servers"). Connecting happens once (credentials included); a project only ever reaches a server it was put into, and
// an agent only a server it was granted inside its own project.
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

// MCPServerInput creates or changes a library server. Secrets are write-only: an empty value keeps what is stored.
type MCPServerInput struct {
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
	// OAuth makes an http server use OAuth: the human signs in once (mcplib.oauth.begin) and Drydock keeps and refreshes the tokens.
	// Nil keeps the setting; false turns it off and forgets the tokens.
	OAuth *bool `json:"oauth"`
	// Guard limits what agents may send to the server (no code, files, secrets or long text). Default on.
	Guard *bool `json:"guard"`
	// ReadOnlyTools are tools the human declares read-only; every other tool needs an approval for each call.
	ReadOnlyTools []string `json:"readOnlyTools"`
}

// MCPLibraryServer is a connected server as the library shows it. It never carries a secret value.
type MCPLibraryServer struct {
	ID            string            `json:"id"`
	Display       string            `json:"display"`
	Transport     string            `json:"transport"`
	URL           string            `json:"url,omitempty"`
	Argv          []string          `json:"argv,omitempty"`
	Cwd           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	SecretEnv     []string          `json:"secretEnv"` // names only
	HasToken      bool              `json:"hasToken"`
	OAuth         bool              `json:"oauth"`                // the server uses OAuth
	OAuthAt       int64             `json:"oauthAt,omitempty"`    // when the human last signed in (0: not signed in)
	OAuthScope    string            `json:"oauthScope,omitempty"` // what the sign-in granted
	Guard         bool              `json:"guard"`
	ReadOnlyTools []string          `json:"readOnlyTools"`
	Tools         []mcp.ToolInfo    `json:"tools"`
	DiscoveredAt  int64             `json:"discoveredAt"`
	// Projects are the ids of the projects that have the server.
	Projects  []string `json:"projects"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
	// CatalogID is the catalog entry the server was connected from ("" for a server the human described).
	CatalogID string `json:"catalogId,omitempty"`
	// ServerInfo is what the server said about itself the last time Drydock reached it (its identity, decision P43).
	ServerInfo *mcp.ServerIdentity `json:"serverInfo,omitempty"`
	// Account is who the credential belongs to, as the server reported it ("" when unknown).
	Account string `json:"account,omitempty"`
	// LastError is why the last connection failed ("" after a success).
	LastError   string `json:"lastError,omitempty"`
	LastErrorAt int64  `json:"lastErrorAt,omitempty"`
}

// MCPServerView is a library server as one project has it.
type MCPServerView struct {
	MCPLibraryServer
	ProjectID string `json:"projectId"`
	Enabled   bool   `json:"enabled"`
	// Agents are the agents of this project that may use the server, with the tool globs each may call.
	Agents []MCPAccess `json:"agents"`
}

// MCPAccess is one agent's grant.
type MCPAccess struct {
	AgentID string   `json:"agentId"`
	Tools   []string `json:"tools"`
}

func mcpVaultRef(id, kind string) string { return "mcp:" + id + ":" + kind }

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

type idOnly struct {
	ID string `json:"id"`
}

type projectServer struct {
	ProjectID string `json:"projectId"`
	ServerID  string `json:"serverId"`
}

func startMCPServers(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	s := &mcpServers{c: c}
	mcpServerServices.Store(c, s)
	// teach the scanner the exact secret values again after a restart, so tool results cannot echo them back
	s.registerSecrets(ctx)
	return map[string]Method{
		// the library: connect once
		"mcplib.list":     bind(func(ctx context.Context, _ struct{}) (any, error) { return s.libList(ctx) }),
		"mcplib.save":     bind(func(ctx context.Context, p MCPServerInput) (any, error) { return s.save(ctx, p) }),
		"mcplib.delete":   bind(func(ctx context.Context, p idOnly) (any, error) { return nil, s.libDelete(ctx, p.ID) }),
		"mcplib.discover": bind(func(ctx context.Context, p idOnly) (any, error) { return s.discover(ctx, p.ID) }),
		// OAuth sign-in of an http server (ext_mcpoauth.go): begin returns the address the human opens; status is polled
		"mcplib.oauth.begin":   bind(func(ctx context.Context, p idOnly) (any, error) { return s.oauthBegin(ctx, p.ID) }),
		"mcplib.oauth.status":  bind(func(ctx context.Context, p idOnly) (any, error) { return s.oauthStatus(ctx, p.ID) }),
		"mcplib.oauth.cancel":  bind(func(ctx context.Context, p idOnly) (any, error) { s.oauthCancel(p.ID); return s.oauthStatus(ctx, p.ID) }),
		"mcplib.oauth.signout": bind(func(ctx context.Context, p idOnly) (any, error) { return s.oauthSignOut(ctx, p.ID) }),
		// a project's servers: put into the project, switch on or off, take out
		"mcpserver.list": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return s.list(ctx, p.ProjectID)
		}),
		"mcpserver.add": bind(func(ctx context.Context, p projectServer) (any, error) {
			return s.addToProject(ctx, p.ProjectID, p.ServerID)
		}),
		"mcpserver.remove": bind(func(ctx context.Context, p projectServer) (any, error) {
			return nil, s.removeFromProject(ctx, p.ProjectID, p.ServerID)
		}),
		"mcpserver.setEnabled": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			ServerID  string `json:"serverId"`
			Enabled   bool   `json:"enabled"`
		}) (any, error) {
			return s.setEnabled(ctx, p.ProjectID, p.ServerID, p.Enabled)
		}),
		// agents: the whole set, or one agent (granting also puts the server into the project)
		"mcpserver.access.set": bind(func(ctx context.Context, p struct {
			ProjectID string      `json:"projectId"`
			ServerID  string      `json:"serverId"`
			Agents    []MCPAccess `json:"agents"`
		}) (any, error) {
			return s.setAccess(ctx, p.ProjectID, p.ServerID, p.Agents)
		}),
		"mcpserver.grant": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			ServerID  string `json:"serverId"`
			AgentID   string `json:"agentId"`
			On        bool   `json:"on"`
		}) (any, error) {
			return s.grant(ctx, p.ProjectID, p.ServerID, p.AgentID, p.On)
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
	MCPLibraryServer
}

func (s *mcpServers) load(ctx context.Context, id string) (mcpRow, error) {
	var r mcpRow
	var argvJSON, envJSON, secretJSON, roJSON, toolsJSON, infoJSON string
	var tok, guard int
	var oauth int
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT id,display,transport,url,argv,cwd,env,secret_env,has_token,guard,read_only_tools,tools,discovered_at,created_at,updated_at,oauth,oauth_at,oauth_scope,
catalog_id,server_info,account,last_error,last_error_at
FROM mcp_library WHERE id=?`, id).Scan(&r.ID, &r.Display, &r.Transport, &r.URL, &argvJSON, &r.Cwd, &envJSON, &secretJSON,
		&tok, &guard, &roJSON, &toolsJSON, &r.DiscoveredAt, &r.CreatedAt, &r.UpdatedAt, &oauth, &r.OAuthAt, &r.OAuthScope,
		&r.CatalogID, &infoJSON, &r.Account, &r.LastError, &r.LastErrorAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("there is no MCP server %q in the library", id)
	}
	if err != nil {
		return r, err
	}
	r.HasToken, r.Guard, r.OAuth = tok != 0, guard != 0, oauth != 0
	_ = json.Unmarshal([]byte(argvJSON), &r.Argv)
	_ = json.Unmarshal([]byte(envJSON), &r.Env)
	_ = json.Unmarshal([]byte(secretJSON), &r.SecretEnv)
	_ = json.Unmarshal([]byte(roJSON), &r.ReadOnlyTools)
	_ = json.Unmarshal([]byte(toolsJSON), &r.Tools)
	if r.SecretEnv == nil {
		r.SecretEnv = []string{}
	}
	if r.ReadOnlyTools == nil {
		r.ReadOnlyTools = []string{}
	}
	if r.Tools == nil {
		r.Tools = []mcp.ToolInfo{}
	}
	if infoJSON != "" {
		var info mcp.ServerIdentity
		if json.Unmarshal([]byte(infoJSON), &info) == nil && info != (mcp.ServerIdentity{}) {
			r.ServerInfo = &info
		}
	}
	r.Projects = s.projectsOf(ctx, id)
	return r, nil
}

func (s *mcpServers) projectsOf(ctx context.Context, server string) []string {
	out := []string{}
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT project_id FROM project_mcp_servers WHERE server_id=? ORDER BY project_id`, server)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out = append(out, p)
		}
	}
	return out
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

func (s *mcpServers) libList(ctx context.Context) ([]MCPLibraryServer, error) {
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT id FROM mcp_library ORDER BY id`)
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
	out := []MCPLibraryServer{}
	for _, id := range idsList {
		r, err := s.load(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r.MCPLibraryServer)
	}
	return out, nil
}

// view is the library server as the project has it; the second result is false when the project does not have it.
func (s *mcpServers) view(ctx context.Context, project, server string) (MCPServerView, bool, error) {
	r, err := s.load(ctx, server)
	if err != nil {
		return MCPServerView{}, false, err
	}
	var en int
	err = s.c.DB.Reader().QueryRowContext(ctx, `SELECT enabled FROM project_mcp_servers WHERE project_id=? AND server_id=?`, project, server).Scan(&en)
	if errors.Is(err, sql.ErrNoRows) {
		return MCPServerView{MCPLibraryServer: r.MCPLibraryServer, ProjectID: project, Agents: []MCPAccess{}}, false, nil
	}
	if err != nil {
		return MCPServerView{}, false, err
	}
	return MCPServerView{MCPLibraryServer: r.MCPLibraryServer, ProjectID: project, Enabled: en != 0, Agents: s.access(ctx, project, server)}, true, nil
}

func (s *mcpServers) list(ctx context.Context, project string) ([]MCPServerView, error) {
	if _, err := s.c.GetProject(ctx, project); err != nil {
		return nil, err
	}
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT server_id FROM project_mcp_servers WHERE project_id=? ORDER BY server_id`, project)
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
		v, ok, err := s.view(ctx, project, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, v)
		}
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
				cs.SecretEnv[name] = mcpVaultRef(r.ID, "env:"+name)
			}
		}
		sp.Command = cs
	}
	if r.HasToken {
		sp.Auth = mcp.AuthSpec{Kind: mcp.AuthBearer, Keyring: mcpVaultRef(r.ID, "token")}
	}
	if r.OAuth {
		sp.Auth = mcp.AuthSpec{Kind: mcp.AuthPKCE, Keyring: mcpVaultRef(r.ID, "oauth")}
	}
	if r.Guard {
		sp.Egress = &mcp.EgressSpec{MaxArgBytes: 4096, ScanSecrets: true, DenyCodeFences: true}
	}
	for _, t := range r.Tools {
		// what a person reviewed when they listed the tools: a run keeps the model from a tool whose text no longer matches, from one
		// marked as instructions, and from one that was not listed, unless the grant names it (mcp/toolreview.go)
		sp.Tools = append(sp.Tools, mcp.ToolDecl{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema, Digest: t.Digest, Flagged: len(t.Flags) > 0})
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
	if ref == mcpVaultRef(serverID, "oauth") {
		return s.oauthAccessToken(ctx, serverID)
	}
	return s.c.secrets.Get(ctx, ref)
}

func (s *mcpServers) registerSecrets(ctx context.Context) {
	if s.c.secrets == nil || s.c.Scanner == nil {
		return
	}
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT id,has_token,secret_env FROM mcp_library`)
	if err != nil {
		return
	}
	type ref struct{ name, ref string }
	var refs []ref
	for rows.Next() {
		var id, sj string
		var tok int
		if rows.Scan(&id, &tok, &sj) != nil {
			continue
		}
		if tok != 0 {
			refs = append(refs, ref{"mcp-" + id + "-token", mcpVaultRef(id, "token")})
		}
		var names []string
		_ = json.Unmarshal([]byte(sj), &names)
		for _, n := range names {
			refs = append(refs, ref{"mcp-" + id + "-" + n, mcpVaultRef(id, "env:"+n)})
		}
	}
	rows.Close()
	for _, r := range refs {
		if v, err := s.c.secrets.Get(ctx, r.ref); err == nil && v != "" {
			_ = s.c.Scanner.RegisterSecret(r.name, v)
		}
	}
}

// ---- library: save / delete / discover ----

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

func (s *mcpServers) save(ctx context.Context, in MCPServerInput) (MCPLibraryServer, error) {
	if err := s.c.RequireLicensed(ctx, "add_connection"); err != nil {
		return MCPLibraryServer{}, err
	}
	in.ID = strings.TrimSpace(in.ID)
	existing, err := s.load(ctx, in.ID)
	isNew := err != nil
	if isNew {
		existing = mcpRow{MCPLibraryServer{ID: in.ID, Guard: true, SecretEnv: []string{}}}
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
			return MCPLibraryServer{}, fmt.Errorf("%q is not a valid environment variable name", k)
		}
		r.Env[k] = v
	}
	if in.Guard != nil {
		r.Guard = *in.Guard
	}
	if r.ReadOnlyTools, err = cleanList(in.ReadOnlyTools, "read-only tool"); err != nil {
		return MCPLibraryServer{}, err
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
			return MCPLibraryServer{}, fmt.Errorf("%q is not a valid environment variable name", name)
		}
		if _, dup := r.Env[name]; dup {
			return MCPLibraryServer{}, fmt.Errorf("%s is both a plain and a secret environment variable", name)
		}
		newNames = append(newNames, name)
		known := false
		for _, old := range existing.SecretEnv {
			known = known || old == name
		}
		if val != "" {
			putSecrets[mcpVaultRef(r.ID, "env:"+name)] = val
		} else if !known {
			return MCPLibraryServer{}, fmt.Errorf("give a value for the secret variable %s", name)
		}
	}
	sort.Strings(newNames)
	for _, old := range existing.SecretEnv {
		kept := false
		for _, n := range newNames {
			kept = kept || n == old
		}
		if !kept {
			removeRefs = append(removeRefs, mcpVaultRef(r.ID, "env:"+old))
		}
	}
	r.SecretEnv = newNames
	switch {
	case in.ClearBearer:
		if existing.HasToken {
			removeRefs = append(removeRefs, mcpVaultRef(r.ID, "token"))
		}
		r.HasToken = false
	case strings.TrimSpace(in.BearerToken) != "":
		putSecrets[mcpVaultRef(r.ID, "token")] = strings.TrimSpace(in.BearerToken)
		r.HasToken = true
	}
	if in.OAuth != nil {
		switch {
		case *in.OAuth && r.Transport != mcp.TransportHTTP:
			return MCPLibraryServer{}, errors.New("OAuth is for http servers")
		case *in.OAuth && (r.HasToken || strings.TrimSpace(in.BearerToken) != ""):
			return MCPLibraryServer{}, errors.New("a server uses either a bearer token or OAuth, not both")
		case *in.OAuth:
			r.OAuth = true
		default:
			if existing.OAuth {
				removeRefs = append(removeRefs, mcpVaultRef(r.ID, "oauth"))
			}
			r.OAuth, r.OAuthAt, r.OAuthScope = false, 0, ""
		}
	} else if r.OAuth && (r.HasToken || strings.TrimSpace(in.BearerToken) != "") {
		return MCPLibraryServer{}, errors.New("a server uses either a bearer token or OAuth, not both: turn OAuth off first")
	}
	if r.Transport != mcp.TransportHTTP && (r.HasToken || in.BearerToken != "") {
		return MCPLibraryServer{}, errors.New("a bearer token is for http servers; a stdio server gets secrets through environment variables")
	}
	if r.Transport == mcp.TransportHTTP && len(r.SecretEnv) > 0 {
		return MCPLibraryServer{}, errors.New("secret environment variables are for stdio servers; an http server uses a bearer token")
	}
	if len(putSecrets) > 0 && s.c.secrets == nil {
		return MCPLibraryServer{}, errNoVault
	}

	// the gateway's own validation decides whether the server is acceptable (id, url, command, secrets in arguments, ...)
	sp := r.spec()
	if err := sp.Normalize(); err != nil {
		return MCPLibraryServer{}, err
	}
	// a changed command or url invalidates the discovered tools (and an OAuth sign-in, which belongs to that address); the
	// human's read-only declarations stay as typed
	if !isNew && r.OAuth && existing.URL != r.URL {
		r.OAuthAt, r.OAuthScope = 0, ""
		removeRefs = append(removeRefs, mcpVaultRef(r.ID, "oauth"))
	}
	moved := !isNew && (existing.Transport != r.Transport || existing.URL != r.URL || strings.Join(existing.Argv, "\x00") != strings.Join(r.Argv, "\x00"))
	if moved {
		r.Tools, r.DiscoveredAt = []mcp.ToolInfo{}, 0
	}
	// what the server said about itself belongs to that address, and the account to that credential
	forgetIdentity := moved
	forgetAccount := moved || len(putSecrets) > 0 || in.ClearBearer || (in.OAuth != nil && !*in.OAuth)

	for ref, val := range putSecrets {
		if err := s.c.secrets.Put(ctx, ref, val); err != nil {
			return MCPLibraryServer{}, fmt.Errorf("could not keep the secret: %w", err)
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
		_, err := tx.ExecContext(ctx, `INSERT INTO mcp_library(id,display,transport,url,argv,cwd,env,secret_env,has_token,guard,read_only_tools,tools,discovered_at,created_by,created_at,updated_at,oauth,oauth_at,oauth_scope)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET display=excluded.display, transport=excluded.transport, url=excluded.url, argv=excluded.argv, cwd=excluded.cwd, env=excluded.env,
 secret_env=excluded.secret_env, has_token=excluded.has_token, guard=excluded.guard, read_only_tools=excluded.read_only_tools, tools=excluded.tools,
 discovered_at=excluded.discovered_at, updated_at=excluded.updated_at, oauth=excluded.oauth, oauth_at=excluded.oauth_at, oauth_scope=excluded.oauth_scope`,
			r.ID, r.Display, r.Transport, r.URL, string(argv), r.Cwd, string(env), string(sec), b2i(r.HasToken), b2i(r.Guard), string(ro), string(tools), r.DiscoveredAt, s.c.Human(), now, now, b2i(r.OAuth), r.OAuthAt, r.OAuthScope)
		if err != nil {
			return err
		}
		if forgetIdentity {
			if _, err := tx.ExecContext(ctx, `UPDATE mcp_library SET server_info='' WHERE id=?`, r.ID); err != nil {
				return err
			}
		}
		if forgetAccount {
			_, err = tx.ExecContext(ctx, `UPDATE mcp_library SET account='', last_error='', last_error_at=0 WHERE id=?`, r.ID)
		}
		return err
	})
	if err != nil {
		return MCPLibraryServer{}, err
	}
	for _, ref := range removeRefs {
		if s.c.secrets != nil {
			_ = s.c.secrets.Delete(ctx, ref)
		}
	}
	if gw := s.c.MCPGateway(); gw != nil {
		gw.StopUpstream(r.ID) // a changed command, url or credential must take effect on the next call
	}
	s.c.publish(ctx, "mcp.server_saved", "", map[string]any{"id": r.ID, "transport": r.Transport, "new": isNew, "by": s.c.Human()})
	out, err := s.load(ctx, r.ID)
	return out.MCPLibraryServer, err
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

// libDelete disconnects a server everywhere: every project loses it, every grant goes, and its secrets leave the credential store.
func (s *mcpServers) libDelete(ctx context.Context, id string) error {
	r, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	err = s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		// foreign keys cascade, but the deletes are explicit so the order does not depend on the pragma
		for _, q := range []string{`DELETE FROM agent_mcp_access WHERE server_id=?`, `DELETE FROM project_mcp_servers WHERE server_id=?`, `DELETE FROM mcp_library WHERE id=?`} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.c.secrets != nil {
		if r.HasToken {
			_ = s.c.secrets.Delete(ctx, mcpVaultRef(id, "token"))
		}
		_ = s.c.secrets.Delete(ctx, mcpVaultRef(id, "oauth"))
		for _, n := range r.SecretEnv {
			_ = s.c.secrets.Delete(ctx, mcpVaultRef(id, "env:"+n))
		}
	}
	if gw := s.c.MCPGateway(); gw != nil {
		gw.StopUpstream(id)
	}
	s.c.publish(ctx, "mcp.server_removed", "", map[string]any{"id": id, "projects": len(r.Projects), "by": s.c.Human()})
	return nil
}

// discover starts the server (or connects to it), lists its tools and stores them, so agents see the tools without waiting for
// the server to start. It is also the "test connection" of the page.
func (s *mcpServers) discover(ctx context.Context, id string) (MCPLibraryServer, error) {
	gw := s.c.MCPGateway()
	if gw == nil {
		return MCPLibraryServer{}, errors.New("the MCP gateway is not running")
	}
	r, err := s.load(ctx, id)
	if err != nil {
		return MCPLibraryServer{}, err
	}
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	sp := r.spec()
	sp.Tools = nil
	up, err := gw.AppUpstream(tctx, sp)
	if err != nil {
		return MCPLibraryServer{}, s.noteFailure(ctx, id, fmt.Errorf("could not reach %s: %s", r.Display, s.c.scrubSecrets(err.Error())))
	}
	tools, err := up.ToolList(tctx)
	if err != nil {
		return MCPLibraryServer{}, s.noteFailure(ctx, id, fmt.Errorf("could not list the tools of %s: %s", r.Display, s.c.scrubSecrets(err.Error())))
	}
	// the server's own identity (decision P43): serverInfo and instructions from initialize, and for a catalog server with an
	// identity tool, the account the credential belongs to. Both are the server's claims, kept as text.
	info, _ := up.Identity(tctx)
	info.Instructions = s.c.scrubSecrets(info.Instructions)
	info.Description = s.c.scrubSecrets(info.Description)
	account := ""
	if idSpec := s.c.catalogIdentity(r.CatalogID); idSpec != nil {
		for _, t := range tools {
			if t.Name == idSpec.Tool {
				actx, acancel := context.WithTimeout(tctx, 20*time.Second)
				if who, err := up.Account(actx, *idSpec); err == nil {
					account = s.c.scrubSecrets(who)
				}
				acancel()
				break
			}
		}
	}
	infoJSON := ""
	if info != (mcp.ServerIdentity{}) {
		b, _ := json.Marshal(info)
		infoJSON = string(b)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	flagged := 0
	for i := range tools {
		if tools[i].Flags = toolFlags(tools[i]); len(tools[i].Flags) > 0 {
			flagged++
		}
	}
	tj, _ := json.Marshal(tools)
	now := storage.Now()
	err = s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE mcp_library SET tools=?, discovered_at=?, updated_at=?, server_info=?, account=?, last_error='', last_error_at=0 WHERE id=?`,
			string(tj), now, now, infoJSON, account, id)
		return err
	})
	if err != nil {
		return MCPLibraryServer{}, err
	}
	s.c.publish(ctx, "mcp.server_discovered", "", map[string]any{"id": id, "tools": len(tools), "flagged": flagged})
	out, err := s.load(ctx, id)
	return out.MCPLibraryServer, err
}

// noteFailure keeps why a connection failed, for the card, and returns the error. The text was scrubbed of known secrets.
func (s *mcpServers) noteFailure(ctx context.Context, id string, failure error) error {
	msg := failure.Error()
	if len(msg) > 600 {
		msg = msg[:600] + "..."
	}
	now := storage.Now()
	_ = s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE mcp_library SET last_error=?, last_error_at=?, updated_at=? WHERE id=?`, msg, now, now, id)
		return err
	})
	s.c.publish(ctx, "mcp.server_unreachable", "", map[string]any{"id": id})
	return failure
}

func (c *Core) scrubSecrets(s string) string {
	if c.Scanner == nil {
		return s
	}
	return c.Scanner.Redact(s)
}

// ---- projects and agents ----

func (s *mcpServers) addToProject(ctx context.Context, project, server string) (MCPServerView, error) {
	if _, err := s.c.GetProject(ctx, project); err != nil {
		return MCPServerView{}, err
	}
	if _, err := s.load(ctx, server); err != nil {
		return MCPServerView{}, err
	}
	err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO project_mcp_servers(project_id,server_id,enabled,added_by,added_at) VALUES(?,?,1,?,?) ON CONFLICT(project_id,server_id) DO NOTHING`, project, server, s.c.Human(), storage.Now())
		return err
	})
	if err != nil {
		return MCPServerView{}, err
	}
	s.c.publish(ctx, "mcp.server_added", project, map[string]any{"server": server, "by": s.c.Human()})
	v, _, err := s.view(ctx, project, server)
	return v, err
}

func (s *mcpServers) removeFromProject(ctx context.Context, project, server string) error {
	if _, ok, err := s.view(ctx, project, server); err != nil || !ok {
		if err != nil {
			return err
		}
		return fmt.Errorf("this project does not have the MCP server %q", server)
	}
	err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_mcp_access WHERE project_id=? AND server_id=?`, project, server); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM project_mcp_servers WHERE project_id=? AND server_id=?`, project, server)
		return err
	})
	if err != nil {
		return err
	}
	s.c.publish(ctx, "mcp.server_taken_out", project, map[string]any{"server": server, "by": s.c.Human()})
	return nil
}

func (s *mcpServers) setEnabled(ctx context.Context, project, server string, on bool) (MCPServerView, error) {
	if _, ok, err := s.view(ctx, project, server); err != nil || !ok {
		if err != nil {
			return MCPServerView{}, err
		}
		return MCPServerView{}, fmt.Errorf("this project does not have the MCP server %q", server)
	}
	err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE project_mcp_servers SET enabled=? WHERE project_id=? AND server_id=?`, b2i(on), project, server)
		return err
	})
	if err != nil {
		return MCPServerView{}, err
	}
	v, _, err := s.view(ctx, project, server)
	return v, err
}

// toolFlags scores a listed tool's name, description and input schema with the same patterns as outside text
// (integrations.Score) and returns the flags when the score reaches integrations.RiskThreshold. A server's tool list is
// read by the model like instructions, so a description that tells it to ignore its rules, claims approval or hides markup
// is a way in (tool poisoning).
func toolFlags(t mcp.ToolInfo) []string {
	schema, _ := json.Marshal(t.InputSchema)
	risk, flags := integrations.Score(t.Name+"\n"+t.Description+"\n"+string(schema), hiddenRunes(t.Description+string(schema)))
	if risk < integrations.RiskThreshold {
		return nil
	}
	return flags
}

// hiddenRunes counts invisible and bidirectional-control characters.
func hiddenRunes(s string) int {
	n := 0
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) {
			n++
		}
	}
	return n
}

// grants validates and normalizes one agent's grant. A grant that names no tools gets the server's tools as they are known
// now, by name, without the flagged ones (the server's tools are listed first if they were never listed): a tool the server
// adds later, or one whose description reads like instructions, is not granted until a human names it. Before, such a grant
// was "*", every tool the server would ever offer.
func (s *mcpServers) checkGrant(ctx context.Context, project, server string, g MCPAccess) (string, error) {
	a, err := s.c.agent(ctx, g.AgentID)
	if err != nil || a.ProjectID != project || a.Archived {
		return "", fmt.Errorf("agent %s is not on this project's team", g.AgentID)
	}
	tools, err := cleanList(g.Tools, "tool")
	if err != nil {
		return "", err
	}
	if len(tools) == 0 {
		if tools, err = s.grantable(ctx, server); err != nil {
			return "", err
		}
	}
	tj, _ := json.Marshal(tools)
	return string(tj), nil
}

// grantable is the server's known tools without the flagged ones, listing them first when they were never listed.
func (s *mcpServers) grantable(ctx context.Context, server string) ([]string, error) {
	r, err := s.load(ctx, server)
	if err != nil {
		return nil, err
	}
	if r.DiscoveredAt == 0 {
		lib, err := s.discover(ctx, server)
		if err != nil {
			return nil, fmt.Errorf("Drydock grants a server's tools by name and could not list them: %w", err)
		}
		r.MCPLibraryServer = lib
	}
	var out []string
	for _, t := range r.Tools {
		if len(t.Flags) == 0 {
			out = append(out, t.Name)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s offers no tool Drydock can grant: it lists none, or every tool's description reads like instructions", r.Display)
	}
	return out, nil
}

func (s *mcpServers) setAccess(ctx context.Context, project, server string, grants []MCPAccess) (MCPServerView, error) {
	if _, ok, err := s.view(ctx, project, server); err != nil || !ok {
		if err != nil {
			return MCPServerView{}, err
		}
		return MCPServerView{}, fmt.Errorf("this project does not have the MCP server %q: put it into the project first", server)
	}
	seen := map[string]bool{}
	type row struct{ agent, tools string }
	var rows []row
	for _, g := range grants {
		tj, err := s.checkGrant(ctx, project, server, g)
		if err != nil {
			return MCPServerView{}, err
		}
		if seen[g.AgentID] {
			continue
		}
		seen[g.AgentID] = true
		rows = append(rows, row{g.AgentID, tj})
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
	v, _, err := s.view(ctx, project, server)
	return v, err
}

// grant gives one agent a server, or takes it away, leaving every other agent's grant as it is. Granting also puts the server
// into the agent's project when the project did not have it yet (that is what dropping a server onto an agent means).
func (s *mcpServers) grant(ctx context.Context, project, server, agentID string, on bool) (MCPServerView, error) {
	if on {
		if _, err := s.addToProject(ctx, project, server); err != nil {
			return MCPServerView{}, err
		}
	}
	cur, ok, err := s.view(ctx, project, server)
	if err != nil {
		return MCPServerView{}, err
	}
	if !ok {
		return cur, nil // nothing to take away
	}
	var next []MCPAccess
	has := false
	for _, a := range cur.Agents {
		if a.AgentID == agentID {
			has = true
			if !on {
				continue
			}
		}
		next = append(next, a)
	}
	if on && !has {
		// no tools named: setAccess names the ones listed now, without the marked ones, never "*" (a tool added later, or one
		// whose description reads like instructions, would reach the model unreviewed; audit 2026-10-16, M2)
		next = append(next, MCPAccess{AgentID: agentID})
	}
	return s.setAccess(ctx, project, server, next)
}

// mcpServersForRun returns the servers an agent may use in a run and the tool globs it may call on each. A server counts only if
// the agent's project has it and has it switched on, and the agent was granted it.
func (c *Core) mcpServersForRun(ctx context.Context, project, agentID string) ([]mcp.ServerSpec, map[string][]string, []string) {
	s := c.mcpServerSvc()
	if s == nil {
		return nil, nil, nil
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT p.server_id, a.tools FROM project_mcp_servers p JOIN agent_mcp_access a ON a.project_id=p.project_id AND a.server_id=p.server_id
WHERE p.project_id=? AND a.agent_id=? AND p.enabled=1 ORDER BY p.server_id`, project, agentID)
	if err != nil {
		return nil, nil, nil
	}
	var grants []serverGrant
	for rows.Next() {
		var x serverGrant
		var tj string
		if rows.Scan(&x.id, &tj) == nil {
			_ = json.Unmarshal([]byte(tj), &x.tools)
			grants = append(grants, x)
		}
	}
	rows.Close()
	return s.specsFor(ctx, grants)
}

// serverGrant is one library server a session may use, with the tool globs it may call there.
type serverGrant struct {
	id    string
	tools []string
}

// mcpServersForChat returns the library servers granted to a chat (decision P60), as mcpServersForRun does for an agent. A
// server disconnected from the library since is left out.
func (c *Core) mcpServersForChat(ctx context.Context, grants []chats.MCPGrant) ([]mcp.ServerSpec, map[string][]string, []string) {
	s := c.mcpServerSvc()
	if s == nil || len(grants) == 0 {
		return nil, nil, nil
	}
	var gs []serverGrant
	for _, g := range grants {
		if len(g.Tools) > 0 {
			gs = append(gs, serverGrant{id: g.ServerID, tools: g.Tools})
		}
	}
	return s.specsFor(ctx, gs)
}

// specsFor builds the gateway's servers, the tools each may call and the tool names the CLI is told about.
func (s *mcpServers) specsFor(ctx context.Context, grants []serverGrant) ([]mcp.ServerSpec, map[string][]string, []string) {
	var specs []mcp.ServerSpec
	allow := map[string][]string{}
	var exposed []string
	for _, x := range grants {
		r, err := s.load(ctx, x.id)
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
	// what the person reads: the short fields (where the call goes) first, the whole input up to security.MaxReviewBytes, a long
	// value cut in the middle with the size of the gap written in; the approval binds the hash of the whole input (audit
	// 2026-10-16, H8). An input a person cannot be shown is refused.
	rv, err := security.ReviewInput(req.Args)
	if err != nil {
		return false, err
	}
	action := security.ActionDescriptor{
		Kind: mcpApprovalKind, Project: req.Run.ProjectID, TaskID: req.Run.TaskID, RunID: req.Run.RunID, AgentID: req.Run.AgentID, Cwd: req.Run.Worktree,
		Tool: "mcp:" + req.Server + "/" + req.Tool, Command: rv.Text, ArgvDigest: req.ArgsHash, Targets: rv.Targets, Shortened: rv.Shortened,
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
	ID      string `json:"id"`
	Server  string `json:"server"`
	Tool    string `json:"tool"`
	AgentID string `json:"agentId"`
	TaskID  string `json:"taskId,omitempty"`
	Args    string `json:"args"`
	// Targets are the paths and addresses the call names, each whole; Shortened is set when a long value is cut in Args (the
	// approval still covers the whole call).
	Targets   []string `json:"targets"`
	Shortened bool     `json:"shortened,omitempty"`
	Created   int64    `json:"created"`
	Expires   int64    `json:"expiresAt"`
	ProjectID string   `json:"projectId"`
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
		targets := []string{}
		for _, t := range info.Action.Targets {
			if x := firstNonEmpty(t.Path, t.Remote); x != "" {
				targets = append(targets, x)
			}
		}
		out = append(out, MCPApproval{ID: id, Server: server, Tool: tool, AgentID: info.Action.AgentID, TaskID: info.Action.TaskID, Args: info.Action.Command,
			Targets: targets, Shortened: info.Action.Shortened, Created: info.Created, Expires: info.ExpiresAt, ProjectID: info.Action.Project})
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
