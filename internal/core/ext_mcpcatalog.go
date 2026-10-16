package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/storage"
)

// The built-in MCP catalog (internal/mcp/catalog.yaml) as a shelf the human picks from: a catalog server is added to the MCP
// library like one the human typed (decision P15: private data, guard on, every tool not declared read-only waits for an
// approval), with the catalog's read-only declarations filled in. Nothing is added on its own: mcplib.addFromCatalog is a human
// action, and adding is idempotent by URL so the same server is never connected twice.

func init() {
	RegisterExtension(Extension{Name: "mcpcatalog", After: []string{"mcpservers"}, Start: startMCPCatalog})
}

// MCPCatalogEntry is one catalog server a human can add to the library.
type MCPCatalogEntry struct {
	ID          string `json:"id"`
	Display     string `json:"display"`
	URL         string `json:"url"`
	Description string `json:"description"`
	// Auth is none, bearer (a token the human pastes) or oauth (the human signs in); AuthOptional means it works without one.
	Auth             string   `json:"auth"`
	AuthOptional     bool     `json:"authOptional"`
	ReadOnlyTools    []string `json:"readOnlyTools"`
	RecommendedRoles []string `json:"recommendedRoles"`
	Trust            string   `json:"trust"`
	// InLibrary is true when a library server already points at this URL (LibraryID).
	InLibrary bool   `json:"inLibrary"`
	LibraryID string `json:"libraryId,omitempty"`
}

// MCPCatalogAdded is the result of mcplib.addFromCatalog.
type MCPCatalogAdded struct {
	Server MCPLibraryServer `json:"server"`
	// Created is false when the library already had a server with this URL (it is returned unchanged).
	Created bool `json:"created"`
	// Next says what the human still has to do before agents can use it ("" when nothing).
	Next string `json:"next,omitempty"`
}

var (
	builtinCatalogOnce sync.Once
	builtinCatalog     *mcp.Catalog
	builtinCatalogErr  error
)

func loadBuiltinCatalog() (*mcp.Catalog, error) {
	builtinCatalogOnce.Do(func() { builtinCatalog, builtinCatalogErr = mcp.LoadBuiltin() })
	return builtinCatalog, builtinCatalogErr
}

// mcpCatalogSource is the catalog this core offers: the built-in one, or the one a test harness configured.
func (c *Core) mcpCatalogSource() (*mcp.Catalog, error) {
	if c.cfg.MCPCatalog != nil {
		return c.cfg.MCPCatalog, nil
	}
	return loadBuiltinCatalog()
}

// catalogIdentity is the identity tool of a catalog entry (nil when the entry has none or the id is not in the catalog).
func (c *Core) catalogIdentity(catalogID string) *mcp.IdentitySpec {
	if catalogID == "" {
		return nil
	}
	cat, err := c.mcpCatalogSource()
	if err != nil {
		return nil
	}
	if s, ok := cat.Get(catalogID); ok {
		return s.Identity
	}
	return nil
}

// markFromCatalog records which catalog entry a library server is (kept when it already names one).
func (c *Core) markFromCatalog(ctx context.Context, libraryID, catalogID string) error {
	return c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE mcp_library SET catalog_id=? WHERE id=? AND catalog_id=''`, catalogID, libraryID)
		return err
	})
}

// catalogAuth maps a catalog entry to what the library can do: "" means the library cannot connect it as listed (a stdio
// template, or a header or scheme other than the Authorization: Bearer the library sends).
func catalogAuth(s mcp.ServerSpec) string {
	if s.Transport != mcp.TransportHTTP || s.Template {
		return ""
	}
	switch s.Auth.Kind {
	case mcp.AuthNone:
		return "none"
	case mcp.AuthBearer:
		if (s.Auth.Header == "" || s.Auth.Header == "Authorization") && (s.Auth.Scheme == "" || s.Auth.Scheme == "Bearer") {
			return "bearer"
		}
		return ""
	case mcp.AuthPKCE, mcp.AuthDCR:
		return "oauth"
	}
	return ""
}

func startMCPCatalog(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	if _, err := c.mcpCatalogSource(); err != nil {
		return nil, nil, err
	}
	return map[string]Method{
		"mcplib.catalog": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.mcpCatalog(ctx) }),
		"mcplib.addFromCatalog": bind(func(ctx context.Context, p struct {
			CatalogID string `json:"catalogId"`
		}) (any, error) {
			return c.addMCPFromCatalog(ctx, p.CatalogID)
		}),
	}, nil, nil
}

func sameURL(a, b string) bool {
	return strings.TrimRight(strings.TrimSpace(a), "/") == strings.TrimRight(strings.TrimSpace(b), "/")
}

// libraryByURL returns the id of the library server that points at url ("" when none).
func (c *Core) libraryByURL(ctx context.Context, url string) (string, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id, url FROM mcp_library WHERE transport='http' ORDER BY id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id, u string
		if err := rows.Scan(&id, &u); err != nil {
			return "", err
		}
		if sameURL(u, url) {
			return id, nil
		}
	}
	return "", rows.Err()
}

func (c *Core) mcpCatalog(ctx context.Context) ([]MCPCatalogEntry, error) {
	cat, err := c.mcpCatalogSource()
	if err != nil {
		return nil, err
	}
	out := []MCPCatalogEntry{}
	for _, s := range cat.Servers {
		auth := catalogAuth(s)
		if auth == "" {
			continue
		}
		e := MCPCatalogEntry{ID: s.ID, Display: s.Display, URL: s.URL, Description: s.Description, Auth: auth, AuthOptional: s.Auth.Optional,
			ReadOnlyTools: nonNil(append([]string(nil), s.ReadOnlyTools...)), RecommendedRoles: nonNil(append([]string(nil), s.RecommendedRoles...)), Trust: s.Trust}
		if id, err := c.libraryByURL(ctx, s.URL); err != nil {
			return nil, err
		} else if id != "" {
			e.InLibrary, e.LibraryID = true, id
		}
		out = append(out, e)
	}
	return out, nil
}

// freeLibraryID picks the catalog id, or the id with a digit when a different server already uses it.
func (c *Core) freeLibraryID(ctx context.Context, base string) (string, error) {
	for n := 1; n <= 9; n++ {
		id := base
		if n > 1 {
			if len(base) >= 8 {
				base = base[:7]
			}
			id = fmt.Sprintf("%s%d", base, n)
		}
		var x int
		err := c.DB.Reader().QueryRowContext(ctx, `SELECT 1 FROM mcp_library WHERE id=?`, id).Scan(&x)
		if errors.Is(err, sql.ErrNoRows) {
			return id, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("the library already has servers named %s to %s9; remove one first", base, base)
}

// addMCPFromCatalog connects a catalog server in the library. A library server with the same URL is returned as it is, so
// adding twice (or from two prebuilt agents) never makes two connections.
func (c *Core) addMCPFromCatalog(ctx context.Context, catalogID string) (MCPCatalogAdded, error) {
	svc := c.mcpServerSvc()
	if svc == nil {
		return MCPCatalogAdded{}, errors.New("MCP servers are not available")
	}
	cat, err := c.mcpCatalogSource()
	if err != nil {
		return MCPCatalogAdded{}, err
	}
	s, ok := cat.Get(strings.TrimSpace(catalogID))
	if !ok {
		return MCPCatalogAdded{}, fmt.Errorf("the MCP catalog has no server %q", catalogID)
	}
	auth := catalogAuth(s)
	if auth == "" {
		return MCPCatalogAdded{}, fmt.Errorf("%s cannot be added from the catalog: add it by hand on the MCP page", s.Display)
	}
	next := map[string]string{
		"bearer": "add your token for " + s.Display + " on the MCP page before agents can use it",
		"oauth":  "sign in to " + s.Display + " on the MCP page before agents can use it",
	}[auth]
	if auth == "bearer" && s.Auth.Optional {
		next = s.Display + " works without a token; adding one on the MCP page raises its limits"
	}
	if id, err := c.libraryByURL(ctx, s.URL); err != nil {
		return MCPCatalogAdded{}, err
	} else if id != "" {
		if err := c.markFromCatalog(ctx, id, s.ID); err != nil {
			return MCPCatalogAdded{}, err
		}
		r, err := svc.load(ctx, id)
		if err != nil {
			return MCPCatalogAdded{}, err
		}
		if r.HasToken || r.OAuthAt > 0 || auth == "none" {
			next = ""
		}
		return MCPCatalogAdded{Server: r.MCPLibraryServer, Next: next}, nil
	}
	id, err := c.freeLibraryID(ctx, s.ID)
	if err != nil {
		return MCPCatalogAdded{}, err
	}
	guard := true
	in := MCPServerInput{ID: id, Display: s.Display, Transport: mcp.TransportHTTP, URL: s.URL, Guard: &guard, ReadOnlyTools: s.ReadOnlyTools}
	if auth == "oauth" {
		on := true
		in.OAuth = &on
	}
	if _, err := svc.save(ctx, in); err != nil {
		return MCPCatalogAdded{}, err
	}
	if err := c.markFromCatalog(ctx, id, s.ID); err != nil {
		return MCPCatalogAdded{}, err
	}
	// the catalog's tool declarations stand in until the human (or a first call) discovers the server's real list
	if len(s.Tools) > 0 {
		tools := make([]mcp.ToolInfo, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, mcp.ToolInfo{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
		tj, _ := json.Marshal(tools)
		if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE mcp_library SET tools=?, updated_at=? WHERE id=? AND discovered_at=0`, string(tj), storage.Now(), id)
			return err
		}); err != nil {
			return MCPCatalogAdded{}, err
		}
	}
	c.publish(ctx, "mcp.server_added_from_catalog", "", map[string]any{"id": id, "catalog": s.ID, "by": c.Human()})
	r, err := svc.load(ctx, id)
	if err != nil {
		return MCPCatalogAdded{}, err
	}
	return MCPCatalogAdded{Server: r.MCPLibraryServer, Created: true, Next: next}, nil
}
