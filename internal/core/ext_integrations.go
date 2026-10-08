package core

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/security/secretadapt"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The Integrations extension (Slice 5, docs/integrations.md): Linear, Notion, Sentry and Aikido Connections with app sync
// through the MCP gateway client, human-authored task rules, human-action imports, and approval-gated outbound writes. It lives
// in this file so it adds to the app without editing the base wiring.
func init() {
	RegisterExtension(Extension{Name: "integrations", Start: startIntegrations})
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
	store := &integrations.Store{DB: c.DB}
	// The gateway is used here as a client only (no listener): the app's own calls to the vendors' MCP servers share the pooled
	// upstreams, credential handling and circuit breaker that agent runs use.
	gw, err := mcp.New(mcp.Options{DataDir: c.cfg.DataDir, DB: c.DB, Bus: c.Bus, Proc: c.cfg.Procs, Scanner: secretadapt.MCP(c.Scanner),
		Credentials: integrations.Credentials{Store: store, Vault: vault}})
	if err != nil {
		return nil, nil, err
	}
	audit := security.NewAudit(c.DB, c.Scanner)
	approvals := security.NewApprovals(c.DB, c.Bus, audit, security.WithApprovalRedactor(c.Scanner))
	svc, err := integrations.New(integrations.Options{
		DB: c.DB, Bus: c.Bus, Vault: vault, Callers: integrations.GatewayFactory{Gateway: gw}, Scanner: c.Scanner, Approvals: approvals,
		BodyDir: filepath.Join(c.cfg.DataDir, "integrations"), Logf: c.logf,
		RegisterSecret:     func(name, v string) { _ = c.Scanner.RegisterSecret(name, v) },
		UnregisterSecret:   c.Scanner.UnregisterSecret,
		OnCredentialChange: gw.StopUpstream,
		ProjectExists:      func(ctx context.Context, id string) error { _, err := c.GetProject(ctx, id); return err },
		CreateTask: func(ctx context.Context, in integrations.TaskInput) (string, error) {
			t, err := c.CreateTask(ctx, tasks.CreateReq{ProjectID: in.ProjectID, Title: in.Title, Description: in.Description, Priority: in.Priority, IssueRef: in.IssueRef, Template: in.Template})
			return t.ID, err
		},
	})
	if err != nil {
		gw.Close()
		return nil, nil, err
	}
	integrationServices.Store(c, svc)
	svc.RecoverInterrupted(ctx)
	// teach the scanner the exact token values again after a restart, so they are redacted from anything that passes through it
	if vault != nil {
		if conns, err := store.ListConnections(ctx); err == nil {
			for _, cn := range conns {
				if tok, err := vault.Get(ctx, cn.CredentialRef); err == nil {
					_ = c.Scanner.RegisterSecret("integration:"+cn.ID, tok)
				}
			}
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		svc.Run(ctx, 15*time.Second)
	}()
	go func() { // the gateway has no listener here, so its idle reaper does not run by itself
		defer wg.Done()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				gw.ReapIdle()
			}
		}
	}()
	closeFn := func() {
		wg.Wait() // ctx is cancelled by Core.Close before the extension hooks run
		integrationServices.Delete(c)
		gw.Close()
	}

	human := func() string { return c.Human() }
	m := map[string]Method{
		"integration.list": bind(func(ctx context.Context, _ struct{}) (any, error) { return svc.List(ctx) }),
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
		"integration.sync": bind(func(ctx context.Context, p struct {
			ID   string `json:"id"`
			Full bool   `json:"full"`
		}) (any, error) {
			return svc.Sync(ctx, p.ID, integrations.SyncOpts{Force: true, Full: p.Full})
		}),
		"integration.items": bind(func(ctx context.Context, p struct {
			IntegrationID string `json:"integrationId"`
			Kind          string `json:"kind"`
			State         string `json:"state"`
			Imported      *bool  `json:"imported"`
			Gone          *bool  `json:"gone"`
			Limit         int    `json:"limit"`
			Offset        int    `json:"offset"`
		}) (any, error) {
			return svc.Items(ctx, integrations.ItemFilter{IntegrationID: p.IntegrationID, Kind: p.Kind, State: p.State, Imported: p.Imported, Gone: p.Gone, Limit: p.Limit, Offset: p.Offset})
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
