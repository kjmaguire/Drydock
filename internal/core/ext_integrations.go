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

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The Integrations extension (docs/integrations.md, decision P42): Linear, Notion, Sentry, Aikido and GitHub connected once at
// the workspace level and assigned to projects, app sync through each vendor's own API, human-authored task rules,
// human-action imports, and approval-gated outbound writes. It lives in this file so it adds to the app without editing the
// base wiring.
func init() {
	RegisterExtension(Extension{Name: "integrations", Start: startIntegrations})
}

// The OAuth apps Drydock signs in with. A release build sets them (-ldflags "-X .../core.LinearOAuthClientID=..."); a
// developer can set DRYDOCK_LINEAR_OAUTH_CLIENT_ID or DRYDOCK_SENTRY_OAUTH_CLIENT_ID. Empty: that sign-in is off and the
// person pastes a key. These are public client ids (PKCE and the device flow need no secret).
var (
	LinearOAuthClientID string
	SentryOAuthClientID string
)

func integrationOAuthApps() map[string]integrations.OAuthApp {
	out := map[string]integrations.OAuthApp{}
	for kind, id := range map[string]string{integrations.KindLinear: LinearOAuthClientID, integrations.KindSentry: SentryOAuthClientID} {
		if v := strings.TrimSpace(os.Getenv("DRYDOCK_" + strings.ToUpper(kind) + "_OAUTH_CLIENT_ID")); v != "" {
			id = v
		}
		if id != "" {
			out[kind] = integrations.OAuthApp{ClientID: id}
		}
	}
	return out
}

// integrationServices keeps each running Core's integrations service (core.go is not edited to add a field for it).
var integrationServices sync.Map // *Core -> *integrations.Service

// Integrations returns the integrations service of a running Core (nil when the extension did not start).
func (c *Core) Integrations() *integrations.Service {
	if v, ok := integrationServices.Load(c); ok {
		return v.(*integrations.Service)
	}
	return nil
}

// projectGitHubRepo is a project's GitHub repository (owner/name), from its origin remote.
func (c *Core) projectGitHubRepo(ctx context.Context, projectID string) (string, error) {
	cfg, err := c.prSource(ctx, projectID)
	if err != nil {
		return "", err
	}
	if cfg.GitHub.Owner == "" || cfg.GitHub.Repo == "" {
		return "", errors.New("the project has no GitHub repository")
	}
	return cfg.GitHub.Owner + "/" + cfg.GitHub.Repo, nil
}

func startIntegrations(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	var vault providers.SecretStore
	cred := c.cfg.Credentials
	if cred == nil {
		if cs, err := providers.NewCredentialStore(); err == nil {
			cred = cs
		}
	}
	if cred != nil {
		vault = providers.NewVault(cred, c.DB)
	}
	audit := security.NewAudit(c.DB, c.Scanner)
	approvals := security.NewApprovals(c.DB, c.Bus, audit, security.WithApprovalRedactor(c.Scanner))
	svc, err := integrations.New(integrations.Options{
		DB: c.DB, Bus: c.Bus, Vault: vault, Scanner: c.Scanner, Approvals: approvals,
		BodyDir: filepath.Join(c.cfg.DataDir, "integrations"), Logf: c.logf,
		RegisterSecret:   func(name, v string) { _ = c.Scanner.RegisterSecret(name, v) },
		UnregisterSecret: c.Scanner.UnregisterSecret,
		OAuthToken:       c.integrationOAuthToken,
		OAuth:            integrationOAuthApps(),
		// GitHub goes through the user's own gh (Drydock never holds a GitHub token)
		GitHub: func(context.Context) (host.API, error) {
			api, ok := c.githubAPI()
			if !ok {
				return nil, errors.New("the GitHub CLI (gh) is not installed")
			}
			return api, nil
		},
		ProjectRepo:   c.projectGitHubRepo,
		ProjectExists: func(ctx context.Context, id string) error { _, err := c.GetProject(ctx, id); return err },
		CreateTask: func(ctx context.Context, in integrations.TaskInput) (string, error) {
			t, err := c.CreateTask(ctx, tasks.CreateReq{ProjectID: in.ProjectID, Title: in.Title, Description: in.Description, Priority: in.Priority, IssueRef: in.IssueRef, Template: in.Template})
			return t.ID, err
		},
	})
	if err != nil {
		return nil, nil, err
	}
	integrationServices.Store(c, svc)
	svc.RecoverInterrupted(ctx)
	// teach the scanner the exact credential values again after a restart, so they are redacted from anything that passes it
	svc.RegisterStoredSecrets(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc.Run(ctx, 15*time.Second)
	}()
	closeFn := func() {
		wg.Wait() // ctx is cancelled by Core.Close before the extension hooks run
		svc.Close()
		integrationServices.Delete(c)
	}

	human := func() string { return c.Human() }
	m := map[string]Method{
		"integration.kinds": bind(func(ctx context.Context, _ struct{}) (any, error) { return svc.Kinds(), nil }),
		"integration.list":  bind(func(ctx context.Context, _ struct{}) (any, error) { return svc.List(ctx) }),
		"integration.get":   bind(func(ctx context.Context, p idParam) (any, error) { return svc.Get(ctx, p.ID) }),
		"integration.oauthStart": bind(func(ctx context.Context, p struct {
			Kind    string `json:"kind"`
			BaseURL string `json:"baseUrl"`
		}) (any, error) {
			return svc.OAuthStart(ctx, human(), p.Kind, p.BaseURL)
		}),
		"integration.oauthStatus": bind(func(ctx context.Context, p idParam) (any, error) { return svc.OAuthStatus(p.ID) }),
		"integration.oauthBegin": bind(func(ctx context.Context, p struct {
			Kind    string `json:"kind"`
			BaseURL string `json:"baseUrl"`
		}) (any, error) {
			return c.integrationOAuthBegin(ctx, p.Kind, p.BaseURL)
		}),
		"integration.connect": bind(func(ctx context.Context, p struct {
			integrations.ConnectReq
			SkipTest bool `json:"skipTest"`
		}) (any, error) {
			v, err := svc.Connect(ctx, human(), p.ConnectReq)
			if err != nil {
				return nil, err
			}
			out := map[string]any{"connection": v}
			if !p.SkipTest {
				rep, terr := svc.Test(ctx, v.ID)
				if terr == nil {
					out["test"] = rep
				}
				if nv, gerr := svc.Get(ctx, v.ID); gerr == nil {
					out["connection"] = nv
				}
			}
			return out, nil
		}),
		"integration.disconnect": bind(func(ctx context.Context, p idParam) (any, error) { return nil, svc.Disconnect(ctx, human(), p.ID) }),
		"integration.test":       bind(func(ctx context.Context, p idParam) (any, error) { return svc.Test(ctx, p.ID) }),
		// Check against your account (P59): the same read-only checks as the live tests, with the credential Drydock holds
		"integration.verify":   bind(func(ctx context.Context, p idParam) (any, error) { return svc.Verify(ctx, human(), p.ID) }),
		"integration.assign":   bind(func(ctx context.Context, p integrations.AssignReq) (any, error) { return svc.Assign(ctx, human(), p) }),
		"integration.unassign": bind(func(ctx context.Context, p idParam) (any, error) { return nil, svc.Unassign(ctx, human(), p.ID) }),
		"integration.assignments": bind(func(ctx context.Context, p struct {
			IntegrationID string `json:"integrationId"`
		}) (any, error) {
			return svc.Assignments(ctx, p.IntegrationID)
		}),
		"integration.choices": bind(func(ctx context.Context, p struct {
			IntegrationID string            `json:"integrationId"`
			Key           string            `json:"key"`
			Config        map[string]string `json:"config"`
		}) (any, error) {
			return svc.Choices(ctx, p.IntegrationID, p.Key, p.Config)
		}),
		"integration.sync": bind(func(ctx context.Context, p struct {
			ID           string `json:"id"`
			AssignmentID string `json:"assignmentId"`
			Full         bool   `json:"full"`
		}) (any, error) {
			if p.AssignmentID != "" {
				return svc.SyncAssignment(ctx, p.AssignmentID, integrations.SyncOpts{Force: true, Full: p.Full})
			}
			return svc.Sync(ctx, p.ID, integrations.SyncOpts{Force: true, Full: p.Full})
		}),
		"integration.items": bind(func(ctx context.Context, p struct {
			IntegrationID string `json:"integrationId"`
			AssignmentID  string `json:"assignmentId"`
			ProjectID     string `json:"projectId"`
			Kind          string `json:"kind"`
			State         string `json:"state"`
			Imported      *bool  `json:"imported"`
			Gone          *bool  `json:"gone"`
			Limit         int    `json:"limit"`
			Offset        int    `json:"offset"`
		}) (any, error) {
			return svc.Items(ctx, integrations.ItemFilter{IntegrationID: p.IntegrationID, AssignmentID: p.AssignmentID, ProjectID: p.ProjectID, Kind: p.Kind, State: p.State,
				Imported: p.Imported, Gone: p.Gone, Limit: p.Limit, Offset: p.Offset})
		}),
		"integration.item": bind(func(ctx context.Context, p idParam) (any, error) { return svc.ItemDetail(ctx, p.ID) }),
		"integration.rules.list": bind(func(ctx context.Context, p struct {
			IntegrationID string `json:"integrationId"`
		}) (any, error) {
			return svc.Rules(ctx, p.IntegrationID)
		}),
		"integration.rules.set": bind(func(ctx context.Context, p integrations.RuleReq) (any, error) { return svc.SetRule(ctx, human(), p) }),
		"integration.import": bind(func(ctx context.Context, p struct {
			ItemID    string `json:"itemId"`
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return svc.Import(ctx, human(), p.ItemID, p.ProjectID)
		}),
		"integration.notion.page": bind(func(ctx context.Context, p struct {
			ItemID string `json:"itemId"`
		}) (any, error) {
			return svc.NotionPage(ctx, p.ItemID)
		}),
		"integration.outbound.propose": bind(func(ctx context.Context, p integrations.ProposeReq) (any, error) { return svc.Propose(ctx, human(), p) }),
		"integration.outbound.decide": bind(func(ctx context.Context, p struct {
			ID         string `json:"id"`         // the outbound action id, or
			ApprovalID string `json:"approvalId"` // the approval id shown in Needs you
			Approve    bool   `json:"approve"`
		}) (any, error) {
			id := p.ID
			if id == "" && p.ApprovalID != "" {
				o, err := svc.Store().OutboxByApproval(ctx, p.ApprovalID)
				if err != nil {
					return nil, err
				}
				id = o.ID
			}
			return svc.Decide(ctx, human(), id, p.Approve)
		}),
		"integration.outbound.execute": bind(func(ctx context.Context, p idParam) (any, error) { return svc.Execute(ctx, human(), p.ID) }),
		"integration.outbound.list": bind(func(ctx context.Context, p struct {
			IntegrationID string `json:"integrationId"`
			Status        string `json:"status"`
			Limit         int    `json:"limit"`
		}) (any, error) {
			return svc.ListOutbox(ctx, p.IntegrationID, p.Status, p.Limit)
		}),
		"integration.linkback": bind(func(ctx context.Context, p struct {
			TaskID string `json:"taskId"`
			PRURL  string `json:"prUrl"`
		}) (any, error) {
			return svc.LinkBack(ctx, human(), p.TaskID, p.PRURL)
		}),
	}
	return m, closeFn, nil
}

// OAuth for integrations. Linear, Notion and Sentry run hosted MCP servers with OAuth (dynamic client registration), the
// same servers the integrations sync through. Signing in to an integration is therefore an MCP library sign-in: Drydock keeps
// one library server per vendor ("linauth", "notauth", "senauth": the catalog's URL, OAuth on), runs the library's OAuth flow for it
// (ext_mcpoauth.go), and the connection's credential reference ("oauth/mcplib/<id>") resolves to that sign-in's access
// token, refreshed when it expires. The tokens stay in the credential vault under the library server.

// IntegrationOAuthStart is what the page needs to finish a sign-in.
type IntegrationOAuthStart struct {
	LibraryID string      `json:"libraryId"`
	OAuthRef  string      `json:"oauthRef"`
	Status    OAuthStatus `json:"status"`
}

// baseURL replaces the catalog's URL (a self-hosted Sentry), as a connection's BaseURL does.
func (c *Core) integrationOAuthBegin(ctx context.Context, kind, baseURL string) (IntegrationOAuthStart, error) {
	var url string
	switch kind {
	case integrations.KindLinear, integrations.KindNotion, integrations.KindSentry:
		cat, err := mcp.LoadBuiltin()
		if err != nil {
			return IntegrationOAuthStart{}, err
		}
		spec, ok := cat.Get(kind)
		if !ok || spec.URL == "" {
			return IntegrationOAuthStart{}, fmt.Errorf("the catalog has no hosted server for %s", kind)
		}
		url = spec.URL
		if strings.TrimSpace(baseURL) != "" {
			url = strings.TrimSpace(baseURL)
		}
	default:
		return IntegrationOAuthStart{}, fmt.Errorf("%s has no hosted server to sign in to; paste its API key", kind)
	}
	v, ok := mcpServerServices.Load(c)
	if !ok {
		return IntegrationOAuthStart{}, errors.New("the MCP library is not running")
	}
	s := v.(*mcpServers)
	id := map[string]string{integrations.KindLinear: "linauth", integrations.KindNotion: "notauth", integrations.KindSentry: "senauth"}[kind] // library ids are short slugs
	on := true
	if cur, err := s.load(ctx, id); err != nil || cur.URL != url {
		if _, err := s.save(ctx, MCPServerInput{ID: id, Display: strings.ToUpper(kind[:1]) + kind[1:] + " (integration sign-in)", Transport: "http", URL: url, OAuth: &on}); err != nil {
			return IntegrationOAuthStart{}, err
		}
	}
	st, err := s.oauthBegin(ctx, id)
	return IntegrationOAuthStart{LibraryID: id, OAuthRef: "oauth/mcplib/" + id, Status: st}, err
}

// integrationOAuthToken resolves "oauth/mcplib/<id>" to the library sign-in's current access token.
func (c *Core) integrationOAuthToken(ctx context.Context, ref string) (string, error) {
	id, ok := strings.CutPrefix(ref, "oauth/mcplib/")
	if !ok || id == "" {
		return "", fmt.Errorf("not an OAuth reference: %q", ref)
	}
	v, ok := mcpServerServices.Load(c)
	if !ok {
		return "", errors.New("the MCP library is not running")
	}
	return v.(*mcpServers).oauthAccessToken(ctx, id)
}
