package integrations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/integrations"
)

// A CONNECT refusal by a proxy contains "403 Forbidden" but is not the vendor rejecting the token.
func TestAProxyRefusalIsNotReportedAsABadToken(t *testing.T) {
	f := integrations.Classify(integrations.KindLinear, errors.New(`Post "https://mcp.linear.app/mcp": proxyconnect: Proxy refused to open a tunnel: 403 Forbidden`), "")
	if f.Code != integrations.CodeUnreachable || strings.Contains(f.Message, "rejected the token") || !strings.Contains(f.Message, "proxy") {
		t.Fatalf("%+v", f)
	}
	g := integrations.Classify(integrations.KindLinear, nil, "401 Unauthorized: invalid token")
	if g.Code != integrations.CodeUnauthorized {
		t.Fatalf("a real token rejection must still be reported as one: %+v", g)
	}
}
