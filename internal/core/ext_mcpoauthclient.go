package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/kjmaguire/drydock/internal/storage"
)

// OAuth clients the human registers (decision P122). The sign-in of P22 registers Drydock with the provider dynamically; a
// provider without dynamic client registration (GitHub's hosted MCP server: its OAuth needs a GitHub OAuth app the host
// registers) cannot be signed in to that way. The human registers an OAuth app with the provider (redirect address
// http://127.0.0.1/callback; a loopback redirect may use any port, which GitHub documents for OAuth apps) and enters its client
// id and, for a confidential client, its secret here. They are kept in the same vault as the tokens (mcp:<id>:oauth-client), the
// database keeps only that a client is set, the scanner is taught the secret, and the sign-in uses the client instead of
// registering one (the SDK's PreregisteredClient). Changing or removing the client signs the server out: tokens belong to the
// client that obtained them.

// OAuthClientReq sets (or, with an empty ClientID, removes) the registered OAuth client of a library server.
type OAuthClientReq struct {
	ID           string `json:"id"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// oauthClientSaved is what the vault holds under mcp:<id>:oauth-client.
type oauthClientSaved struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

var oauthClientIDRe = regexp.MustCompile(`^[\x21-\x7e]{1,256}$`)

// oauthClient returns the registered client of a server (nil when none).
func (s *mcpServers) oauthClient(ctx context.Context, id string) (*oauthClientSaved, error) {
	if s.c.secrets == nil {
		return nil, errNoVault
	}
	raw, err := s.c.secrets.Get(ctx, mcpVaultRef(id, "oauth-client"))
	if err != nil || raw == "" {
		return nil, nil
	}
	var cl oauthClientSaved
	if err := json.Unmarshal([]byte(raw), &cl); err != nil || cl.ClientID == "" {
		return nil, fmt.Errorf("the stored OAuth client of %s is unreadable: enter it again", id)
	}
	return &cl, nil
}

func (s *mcpServers) teachClientSecret(id string, cl oauthClientSaved) {
	if s.c.Scanner != nil && cl.ClientSecret != "" {
		_ = s.c.Scanner.RegisterSecret("mcp-"+id+"-oauth-registered-client", cl.ClientSecret)
	}
}

// setOAuthClient keeps (or removes) the registered client and signs the server out.
func (s *mcpServers) setOAuthClient(ctx context.Context, req OAuthClientReq) (MCPLibraryServer, error) {
	if err := s.c.RequireLicensed(ctx, "add_connection"); err != nil {
		return MCPLibraryServer{}, err
	}
	r, err := s.load(ctx, strings.TrimSpace(req.ID))
	if err != nil {
		return MCPLibraryServer{}, err
	}
	if r.Transport != "http" || !r.OAuth {
		return MCPLibraryServer{}, errors.New("set the server to sign in with OAuth first")
	}
	if s.c.secrets == nil {
		return MCPLibraryServer{}, errNoVault
	}
	id, secret := strings.TrimSpace(req.ClientID), strings.TrimSpace(req.ClientSecret)
	if id == "" && secret != "" {
		return MCPLibraryServer{}, errors.New("give the client id with its secret")
	}
	if id != "" && !oauthClientIDRe.MatchString(id) {
		return MCPLibraryServer{}, errors.New("the client id is 1 to 256 printable characters without spaces")
	}
	if secret != "" && !oauthClientIDRe.MatchString(secret) {
		return MCPLibraryServer{}, errors.New("the client secret is 1 to 256 printable characters without spaces")
	}
	s.oauthCancel(r.ID)
	ref := mcpVaultRef(r.ID, "oauth-client")
	if id == "" {
		_ = s.c.secrets.Delete(ctx, ref)
	} else {
		b, _ := json.Marshal(oauthClientSaved{ClientID: id, ClientSecret: secret})
		if err := s.c.secrets.Put(ctx, ref, string(b)); err != nil {
			return MCPLibraryServer{}, fmt.Errorf("could not keep the OAuth client: %w", err)
		}
		s.teachClientSecret(r.ID, oauthClientSaved{ClientID: id, ClientSecret: secret})
	}
	// the tokens were issued to the previous client (or to a dynamically registered one): they are forgotten
	_ = s.c.secrets.Delete(ctx, mcpVaultRef(r.ID, "oauth"))
	now := storage.Now()
	if err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE mcp_library SET oauth_client=?, oauth_at=0, oauth_scope='', account='', updated_at=? WHERE id=?`, b2i(id != ""), now, r.ID)
		return err
	}); err != nil {
		return MCPLibraryServer{}, err
	}
	rt := s.orun()
	rt.mu.Lock()
	delete(rt.sources, r.ID)
	delete(rt.flows, r.ID)
	rt.mu.Unlock()
	if gw := s.c.MCPGateway(); gw != nil {
		gw.StopUpstream(r.ID)
	}
	// the event says that a client was set or removed, never which
	s.c.publish(ctx, "mcp.server_oauth_client", "", map[string]any{"id": r.ID, "set": id != "", "confidential": secret != "", "by": s.c.Human()})
	out, err := s.load(ctx, r.ID)
	return out.MCPLibraryServer, err
}
