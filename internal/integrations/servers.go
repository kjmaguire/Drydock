package integrations

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/mcp"
)

// ServerSpecFor derives the MCP server spec for a connection from the catalog entry of its kind. The catalog stays the single
// source of truth for URLs, auth schemes and the user-install stdio templates (D14: the Sentry, Aikido and Notion-local servers
// are never bundled); the connection only adds its credential reference and optional overrides.
//
//   - Linear, Notion and Sentry are hosted HTTP servers. The credential is a token the user pastes (docs say Linear accepts an
//     API key or OAuth token as a Bearer token; the Sentry server accepts "Sentry-Bearer <token>"; whether Notion's hosted server
//     accepts an integration token instead of an interactive OAuth token is unverified). No OAuth flow is built in this slice.
//   - Aikido has no hosted MCP server: it is the user-installed stdio package from the catalog template, which receives the key
//     through the environment variable AIKIDO_API_KEY (never argv).
//   - write selects the endpoint used for outbound writes. Linear's catalog URL is the read-only endpoint; writes use the same
//     URL without "/readonly". A connection BaseURL replaces the URL for both.
func ServerSpecFor(cat *mcp.Catalog, c Connection, write bool) (mcp.ServerSpec, error) {
	spec, ok := cat.Get(c.Kind)
	if !ok {
		return mcp.ServerSpec{}, fmt.Errorf("integrations: the MCP catalog has no entry %q", c.Kind)
	}
	if c.CredentialRef == "" {
		return mcp.ServerSpec{}, ErrNoCredential
	}
	spec.Template = false // the user's explicit Connection is what enables a template entry
	switch spec.Transport {
	case mcp.TransportHTTP:
		if c.BaseURL != "" {
			spec.URL = c.BaseURL
		} else if write && c.Kind == KindLinear {
			spec.URL = strings.TrimSuffix(spec.URL, "/readonly")
		}
		spec.Auth.Kind = mcp.AuthBearer // the token entry replaces the catalog's OAuth kinds
		spec.Auth.Keyring = c.CredentialRef
	case mcp.TransportStdio:
		cmd := *spec.Command
		if raw := c.Config["argv"]; raw != "" {
			var argv []string
			if err := json.Unmarshal([]byte(raw), &argv); err != nil || len(argv) == 0 {
				return mcp.ServerSpec{}, fmt.Errorf("integrations: config argv must be a JSON array of strings: %v", err)
			}
			cmd.Argv = argv
			cmd.Pin = ""
		}
		cmd.SecretEnv = map[string]string{}
		for name := range spec.Command.SecretEnv {
			cmd.SecretEnv[name] = c.CredentialRef
		}
		spec.Command = &cmd
	}
	return spec, nil
}

// SecretEnvName is the environment variable a stdio kind receives its token in ("" for hosted kinds).
func SecretEnvName(kind string) string {
	if kind == KindAikido {
		return "AIKIDO_API_KEY"
	}
	return ""
}
