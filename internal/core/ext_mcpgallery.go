package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/mcp"
)

// The MCP connector gallery (decision P43): every server of the catalog as a card, the way other apps present connectors. A
// card says who publishes the server, what it gives agents, how it signs in and whether it is connected (and as whom). One
// Connect button runs the OAuth sign-in, takes a token, or starts the program a template names, and leaves an ordinary library
// server behind (decision P15: private data, guard on, every tool not declared read-only waits for an approval), so "Add to
// project" and "Grant to agents" are the library's own. Connecting needs no project, and nothing is connected on its own.

func init() {
	RegisterExtension(Extension{Name: "mcpgallery", After: []string{"mcpservers", "mcpcatalog"}, Start: startMCPGallery})
}

// Gallery card states.
const (
	GalleryNotConnected = "not_connected"
	GalleryConnected    = "connected"
	GalleryNeedsSignIn  = "needs_sign_in"
	GalleryNeedsToken   = "needs_token"
	GalleryError        = "error"
)

// MCPGalleryCard is one catalog server as the gallery shows it. It never carries a secret value.
type MCPGalleryCard struct {
	ID          string `json:"id"`
	Display     string `json:"display"`
	Publisher   string `json:"publisher"`
	Official    bool   `json:"official"`
	Brand       string `json:"brand"`
	Group       string `json:"group,omitempty"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	DocsURL     string `json:"docsUrl,omitempty"`
	Transport   string `json:"transport"`
	URL         string `json:"url,omitempty"`
	// Command is what a template runs on this computer (shown before the human connects it).
	Command []string `json:"command,omitempty"`
	// Auth is none, bearer (a token the human pastes), oauth (the human signs in in the browser) or env (a template's secret
	// environment variable, named by SecretEnv).
	Auth         string `json:"auth"`
	AuthOptional bool   `json:"authOptional"`
	SecretEnv    string `json:"secretEnv,omitempty"`
	TokenHelp    string `json:"tokenHelp,omitempty"`
	// OAuthApp is set when the server can also sign in with OAuth through an app the person registers with the provider
	// (decision P122: no dynamic client registration); Connect then takes the app's client id and secret.
	OAuthApp *MCPOAuthApp `json:"oauthApp,omitempty"`
	// OAuth is true when the connected library server signs in with OAuth (an oauth entry, or an oauth_app one connected through
	// the person's app); OAuthClient when that sign-in uses a client the person registered.
	OAuth       bool `json:"oauth,omitempty"`
	OAuthClient bool `json:"oauthClient,omitempty"`
	// Template is a program the human installs; Drydock never ships it (D14).
	Template     bool   `json:"template"`
	License      string `json:"license"`
	LicenseNotes string `json:"licenseNotes,omitempty"`
	Integration  string `json:"integration,omitempty"`
	// SkillRegistries are the skill registries this vendor publishes (ids of the Skills page's allowlist), offered next to
	// the server on the Integrations page (decision P47).
	SkillRegistries []string `json:"skillRegistries"`
	Trust           string   `json:"trust"`
	ReadOnlyTools   []string `json:"readOnlyTools"`
	// Tools are the tools the server offers: the ones it listed when it was last reached, or the catalog's declaration.
	Tools []mcp.ToolInfo `json:"tools"`
	// ToolsListed is true when Tools came from the server itself.
	ToolsListed bool `json:"toolsListed"`

	Status string `json:"status"`
	// Detail is the reason behind Error (or what Needs sign-in and Needs token mean).
	Detail     string              `json:"detail,omitempty"`
	LibraryID  string              `json:"libraryId,omitempty"`
	Account    string              `json:"account,omitempty"`
	ServerInfo *mcp.ServerIdentity `json:"serverInfo,omitempty"`
	Projects   []string            `json:"projects"`
}

// MCPConnectReq connects a catalog server. Token is the bearer token or a template's secret value; it goes to the credential
// store and is never returned.
type MCPConnectReq struct {
	CatalogID string `json:"catalogId"`
	Token     string `json:"token"`
	// ClientID and ClientSecret are an OAuth app the person registered with the provider (decision P122): the server is then
	// connected to sign in with OAuth through that app. They go to the credential store and are never returned.
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// MCPOAuthApp says where the person registers the OAuth app a catalog server's sign-in needs.
type MCPOAuthApp struct {
	RegisterURL string `json:"registerUrl"`
	Help        string `json:"help,omitempty"`
}

// MCPConnectResult is the card after Connect; OAuth is set when the human has to sign in in the browser.
type MCPConnectResult struct {
	Card  MCPGalleryCard `json:"card"`
	OAuth *OAuthStatus   `json:"oauth,omitempty"`
}

func startMCPGallery(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	if _, err := c.mcpCatalogSource(); err != nil {
		return nil, nil, err
	}
	return map[string]Method{
		"mcplib.gallery": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.mcpGallery(ctx) }),
		"mcplib.connect": bind(func(ctx context.Context, p MCPConnectReq) (any, error) { return c.mcpConnect(ctx, p) }),
	}, nil, nil
}

// galleryAuth is how the gallery connects an entry; "" means it cannot (a header or scheme the library does not send).
func galleryAuth(s mcp.ServerSpec) (auth, secretEnv string) {
	if s.Transport == mcp.TransportStdio {
		if s.Command == nil {
			return "", ""
		}
		names := make([]string, 0, len(s.Command.SecretEnv))
		for n := range s.Command.SecretEnv {
			names = append(names, n)
		}
		sort.Strings(names)
		switch len(names) {
		case 0:
			return mcp.AuthNone, ""
		case 1:
			return "env", names[0]
		default:
			return "", "" // more than one secret: connect it by hand
		}
	}
	s.Template = false
	return catalogAuth(s), ""
}

// libraryForCatalog is the library server connected from the entry: the one marked with its id, else (http) the one at its URL.
func (c *Core) libraryForCatalog(ctx context.Context, s mcp.ServerSpec) (string, error) {
	var id string
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT id FROM mcp_library WHERE catalog_id=? ORDER BY id LIMIT 1`, s.ID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if s.Transport == mcp.TransportHTTP {
		return c.libraryByURL(ctx, s.URL)
	}
	return "", nil
}

func (c *Core) galleryCard(ctx context.Context, s mcp.ServerSpec) (MCPGalleryCard, error) {
	auth, secretEnv := galleryAuth(s)
	card := MCPGalleryCard{
		ID: s.ID, Display: s.Display, Publisher: s.Publisher, Official: s.Official, Brand: s.Brand, Group: s.Group, Summary: s.Summary,
		Description: s.Description, DocsURL: s.DocsURL, Transport: s.Transport, URL: s.URL, Auth: auth, AuthOptional: s.Auth.Optional,
		SecretEnv: secretEnv, TokenHelp: s.TokenHelp, Template: s.Template, License: s.License.Server, LicenseNotes: s.License.Notes,
		Integration: s.Integration, Trust: s.Trust, ReadOnlyTools: nonNil(append([]string(nil), s.ReadOnlyTools...)),
		SkillRegistries: nonNil(append([]string(nil), s.SkillRegistries...)),
		Tools:           []mcp.ToolInfo{}, Status: GalleryNotConnected, Projects: []string{},
	}
	if s.Command != nil {
		card.Command = append([]string(nil), s.Command.Argv...)
	}
	if a := s.Auth.OAuthApp; a != nil && s.Transport == mcp.TransportHTTP {
		card.OAuthApp = &MCPOAuthApp{RegisterURL: a.RegisterURL, Help: a.Help}
	}
	for _, t := range s.Tools {
		card.Tools = append(card.Tools, mcp.ToolInfo{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema, ReadOnlyHint: matchesAnyGlob(s.ReadOnlyTools, t.Name)})
	}
	if auth == "" {
		card.Status, card.Detail = GalleryError, "Drydock cannot connect this entry from the gallery; connect it by hand below."
		return card, nil
	}
	libID, err := c.libraryForCatalog(ctx, s)
	if err != nil || libID == "" {
		return card, err
	}
	svc := c.mcpServerSvc()
	if svc == nil {
		return card, nil
	}
	r, err := svc.load(ctx, libID)
	if err != nil {
		return card, nil // removed meanwhile
	}
	card.LibraryID, card.Account, card.ServerInfo, card.Projects = r.ID, r.Account, r.ServerInfo, r.Projects
	card.OAuth, card.OAuthClient = r.OAuth, r.OAuthClient
	card.ReadOnlyTools = r.ReadOnlyTools
	if r.DiscoveredAt > 0 {
		card.Tools, card.ToolsListed = r.Tools, true
	} else if len(r.Tools) > 0 {
		card.Tools = r.Tools
	}
	switch {
	case r.OAuth && r.OAuthAt == 0 && card.OAuthApp != nil && !r.OAuthClient:
		card.Status, card.Detail = GalleryNeedsSignIn, "Register an OAuth app with "+orName(s.Publisher, s.Display)+" and enter its client id to sign in."
	case r.OAuth && r.OAuthAt == 0:
		card.Status, card.Detail = GalleryNeedsSignIn, "Sign in with your "+orName(s.Publisher, s.Display)+" account to finish connecting."
	case auth == "bearer" && !s.Auth.Optional && !r.HasToken && !r.OAuth: // an entry signed in through the person's OAuth app needs no token
		card.Status, card.Detail = GalleryNeedsToken, "Paste a token to finish connecting."
	case auth == "env" && !contains(r.SecretEnv, secretEnv):
		card.Status, card.Detail = GalleryNeedsToken, "Give "+secretEnv+" to finish connecting."
	case r.LastError != "":
		card.Status, card.Detail = GalleryError, r.LastError
	default:
		card.Status = GalleryConnected
	}
	return card, nil
}

func orName(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func matchesAnyGlob(globs []string, name string) bool {
	for _, g := range globs {
		if g == name || g == "*" || (strings.HasSuffix(g, "*") && strings.HasPrefix(name, strings.TrimSuffix(g, "*"))) {
			return true
		}
	}
	return false
}

func (c *Core) mcpGallery(ctx context.Context) ([]MCPGalleryCard, error) {
	cat, err := c.mcpCatalogSource()
	if err != nil {
		return nil, err
	}
	out := make([]MCPGalleryCard, 0, len(cat.Servers))
	for _, s := range cat.Servers {
		card, err := c.galleryCard(ctx, s)
		if err != nil {
			return nil, err
		}
		out = append(out, card)
	}
	return out, nil
}

// mcpConnect connects one catalog server: it adds it to the library (or finds the one already there), keeps the token in the
// credential store, and then either starts the browser sign-in (OAuth) or reaches the server once to learn its tools, its
// identity and the account. A server that cannot be reached is still connected, and its card says why it failed.
func (c *Core) mcpConnect(ctx context.Context, req MCPConnectReq) (MCPConnectResult, error) {
	if err := c.RequireLicensed(ctx, "add_connection"); err != nil {
		return MCPConnectResult{}, err
	}
	svc := c.mcpServerSvc()
	if svc == nil {
		return MCPConnectResult{}, errors.New("MCP servers are not available")
	}
	cat, err := c.mcpCatalogSource()
	if err != nil {
		return MCPConnectResult{}, err
	}
	s, ok := cat.Get(strings.TrimSpace(req.CatalogID))
	if !ok {
		return MCPConnectResult{}, fmt.Errorf("the MCP catalog has no server %q", req.CatalogID)
	}
	auth, secretEnv := galleryAuth(s)
	token := strings.TrimSpace(req.Token)
	guard := true
	var libID string
	if strings.TrimSpace(req.ClientID) != "" || strings.TrimSpace(req.ClientSecret) != "" {
		// sign in through an OAuth app the person registered (decision P122)
		if s.Transport != mcp.TransportHTTP || (s.Auth.OAuthApp == nil && auth != "oauth") {
			return MCPConnectResult{}, fmt.Errorf("%s does not sign in with an OAuth app", s.Display)
		}
		if token != "" {
			return MCPConnectResult{}, fmt.Errorf("give either a token or an OAuth app for %s, not both", s.Display)
		}
		id, err := c.libraryForCatalog(ctx, s)
		if err != nil {
			return MCPConnectResult{}, err
		}
		if id == "" {
			added, err := c.addMCPFromCatalog(ctx, s.ID)
			if err != nil {
				return MCPConnectResult{}, err
			}
			id = added.Server.ID
		}
		r, err := svc.load(ctx, id)
		if err != nil {
			return MCPConnectResult{}, err
		}
		if !r.OAuth {
			if r.HasToken {
				return MCPConnectResult{}, fmt.Errorf("%s is connected with a token; remove the token on the Library page first", r.Display)
			}
			on, g := true, r.Guard
			if _, err := svc.save(ctx, MCPServerInput{ID: r.ID, Display: r.Display, Transport: r.Transport, URL: r.URL, Guard: &g, ReadOnlyTools: r.ReadOnlyTools, OAuth: &on}); err != nil {
				return MCPConnectResult{}, err
			}
		}
		if _, err := svc.setOAuthClient(ctx, OAuthClientReq{ID: id, ClientID: req.ClientID, ClientSecret: req.ClientSecret}); err != nil {
			return MCPConnectResult{}, err
		}
		libID = id
		auth = "client"
	}
	switch auth {
	case "client":
	case "":
		return MCPConnectResult{}, fmt.Errorf("%s cannot be connected from the gallery: connect it by hand", s.Display)
	case "env":
		existing, err := c.libraryForCatalog(ctx, s)
		if err != nil {
			return MCPConnectResult{}, err
		}
		in := MCPServerInput{Display: s.Display, Transport: mcp.TransportStdio, Argv: s.Command.Argv, Env: s.Command.Env, Guard: &guard,
			ReadOnlyTools: s.ReadOnlyTools, SecretEnv: map[string]string{secretEnv: token}}
		if existing != "" {
			r, err := svc.load(ctx, existing)
			if err != nil {
				return MCPConnectResult{}, err
			}
			if token == "" && !contains(r.SecretEnv, secretEnv) {
				return MCPConnectResult{}, fmt.Errorf("give %s to connect %s", secretEnv, s.Display)
			}
			in.ID, in.Display, in.Argv, in.Cwd, in.Env, in.ReadOnlyTools = r.ID, r.Display, r.Argv, r.Cwd, r.Env, r.ReadOnlyTools
			in.Guard = &r.Guard
		} else {
			if token == "" {
				return MCPConnectResult{}, fmt.Errorf("give %s to connect %s", secretEnv, s.Display)
			}
			if in.ID, err = c.freeLibraryID(ctx, s.ID); err != nil {
				return MCPConnectResult{}, err
			}
		}
		if _, err := svc.save(ctx, in); err != nil {
			return MCPConnectResult{}, err
		}
		if err := c.markFromCatalog(ctx, in.ID, s.ID); err != nil {
			return MCPConnectResult{}, err
		}
		libID = in.ID
	case "none", "bearer", "oauth":
		if auth == "bearer" && !s.Auth.Optional && token == "" {
			if id, _ := c.libraryForCatalog(ctx, s); id != "" {
				if r, err := svc.load(ctx, id); err == nil && r.HasToken {
					libID = id
				}
			}
			if libID == "" {
				return MCPConnectResult{}, fmt.Errorf("paste a token to connect %s", s.Display)
			}
		}
		if auth == "oauth" && token != "" {
			return MCPConnectResult{}, fmt.Errorf("%s signs in in the browser; no token is needed", s.Display)
		}
		if libID == "" {
			added, err := c.addMCPFromCatalog(ctx, s.ID)
			if err != nil {
				return MCPConnectResult{}, err
			}
			libID = added.Server.ID
		}
		if token != "" {
			r, err := svc.load(ctx, libID)
			if err != nil {
				return MCPConnectResult{}, err
			}
			if r.OAuth {
				return MCPConnectResult{}, fmt.Errorf("%s is set up to sign in with OAuth; sign out of it on the Library page first", r.Display)
			}
			g := r.Guard
			if _, err := svc.save(ctx, MCPServerInput{ID: r.ID, Display: r.Display, Transport: r.Transport, URL: r.URL, Guard: &g, ReadOnlyTools: r.ReadOnlyTools, BearerToken: token}); err != nil {
				return MCPConnectResult{}, err
			}
		}
	}
	c.publish(ctx, "mcp.server_connected", "", map[string]any{"id": libID, "catalog": s.ID, "by": c.Human()})
	var oauth *OAuthStatus
	r, err := svc.load(ctx, libID)
	if err != nil {
		return MCPConnectResult{}, err
	}
	if r.OAuth {
		if r.OAuthAt == 0 {
			st, err := svc.oauthBegin(ctx, libID)
			if err != nil {
				return MCPConnectResult{}, err
			}
			oauth = &st
		} else {
			_, _ = svc.discover(ctx, libID) // signed in already: refresh what the card knows; a failure is kept on the card
		}
	} else {
		_, _ = svc.discover(ctx, libID)
	}
	card, err := c.galleryCard(ctx, s)
	if err != nil {
		return MCPConnectResult{}, err
	}
	return MCPConnectResult{Card: card, OAuth: oauth}, nil
}
