package manifests_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/mcp"
)

// Agent-specific ACP opt-ins come from the manifest and only OpenCode's declares one: clientCapabilities._meta
// "opencode/child-session-updates" (internal/agents/acp/ext.go). OpenCode's inline configuration states its compaction, the
// same section the MCP configuration writes (decision P39, docs/token-control.md).
func TestOnlyTheOpenCodeManifestOptsInToAgentExtensions(t *testing.T) {
	all, errs := newRegistry(t, manifests.Options{}).BuildAll()
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	for id, ad := range all {
		a, ok := ad.(*acp.Adapter)
		if !ok {
			continue
		}
		meta := a.Config().ClientMeta
		if id == "opencode" {
			if len(meta) != 1 || meta[acp.ChildSessionUpdatesMeta] != true {
				t.Errorf("opencode client meta %v", meta)
			}
			continue
		}
		if len(meta) != 0 {
			t.Errorf("%s declares %v; an opt-in belongs to one agent's manifest", id, meta)
		}
	}
	env := all["opencode"].(*acp.Adapter).Config().Env
	if len(env) != 1 || !strings.HasPrefix(env[0], "OPENCODE_CONFIG_CONTENT=") {
		t.Fatalf("opencode env %v", env)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(env[0], "OPENCODE_CONFIG_CONTENT=")), &doc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc, map[string]any{"compaction": mcp.OpenCodeCompaction()}) {
		t.Fatalf("opencode inline config %v, want only the compaction section %v", doc, mcp.OpenCodeCompaction())
	}
}

// A client_meta key must be namespaced, as ACP's _meta extensions are.
func TestClientMetaKeysMustBeNamespaced(t *testing.T) {
	y := strings.Replace(baseACP, "{argv: [demo, acp]}", "{argv: [demo, acp], client_meta: {childUpdates: true}}", 1)
	if _, err := manifests.Parse([]byte(y)); err == nil || !strings.Contains(err.Error(), "client_meta") {
		t.Fatalf("err %v", err)
	}
	y = strings.Replace(baseACP, "{argv: [demo, acp]}", "{argv: [demo, acp], client_meta: {vendor/flag: true}}", 1)
	if _, err := manifests.Parse([]byte(y)); err != nil {
		t.Fatal(err)
	}
}
