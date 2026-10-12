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
	// OAuth resolves an OAuthRefPrefix reference to a current access token (nil: OAuth references do not resolve).
	OAuth func(ctx context.Context, ref string) (string, error)
}

func (c Credentials) resolve(ctx context.Context, ref string) (string, error) {
	if strings.HasPrefix(ref, OAuthRefPrefix) {
		if c.OAuth == nil {
			return "", fmt.Errorf("%w: OAuth is not available", ErrNoCredential)
		}
		return c.OAuth(ctx, ref)
	}
	if c.Vault == nil {
		return "", ErrCredentialsLocked
	}
	v, err := c.Vault.Get(ctx, ref)
	if err != nil {
		return "", err
	}
	if sc, ok := parseStored(v); ok {
		if sc.Type == "oauth" && sc.AccessToken != "" {
			return sc.AccessToken, nil // the API sign-in's access token (refreshed by the integration when it syncs)
		}
		// Aikido's REST client credentials are not a key its MCP server takes
		return "", fmt.Errorf("%w: this credential is not a token an MCP server takes", ErrNoCredential)
	}
	return v, nil
}

// Credential implements mcp.CredentialSource.
func (c Credentials) Credential(ctx context.Context, serverID, ref string) (string, error) {
	if strings.HasPrefix(ref, "integrations/") || strings.HasPrefix(ref, OAuthRefPrefix) {
		return c.resolve(ctx, ref)
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
				if tok, err := c.resolve(ctx, cn.CredentialRef); err == nil {
					return tok, nil
				}
			}
		}
		return "", fmt.Errorf("%w: no %s connection has a token", ErrNoCredential, kind)
	}
	return "", fmt.Errorf("%w: %s", ErrNoCredential, serverID)
}
