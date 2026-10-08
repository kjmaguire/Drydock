package integrations

import (
	"context"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/providers"
)

// Credentials is the MCP gateway's CredentialSource for integrations. A connection's own references
// ("integrations/<id>/token") resolve directly; the catalog's references ("mcp/linear", "mcp/sentry", "mcp/notion",
// "mcp/aikido", "mcp/sentryx") resolve to the token of the first enabled connection of that kind, so the token a user pasted
// once serves both app sync and agent access through the gateway. Agents never see it: the gateway attaches it upstream.
type Credentials struct {
	Store *Store
	Vault providers.SecretStore
}

// Credential implements mcp.CredentialSource.
func (c Credentials) Credential(ctx context.Context, serverID, ref string) (string, error) {
	if c.Vault == nil {
		return "", ErrCredentialsLocked
	}
	if strings.HasPrefix(ref, "integrations/") {
		return c.Vault.Get(ctx, ref)
	}
	if kind, ok := strings.CutPrefix(ref, "mcp/"); ok {
		if kind == "sentryx" {
			kind = KindSentry
		}
		conns, err := c.Store.ListConnections(ctx)
		if err != nil {
			return "", err
		}
		for _, cn := range conns {
			if cn.Kind == kind && cn.Enabled && cn.CredentialRef != "" {
				if tok, err := c.Vault.Get(ctx, cn.CredentialRef); err == nil {
					return tok, nil
				}
			}
		}
		return "", fmt.Errorf("%w: no %s connection has a token", ErrNoCredential, kind)
	}
	return "", fmt.Errorf("%w: %s", ErrNoCredential, serverID)
}
