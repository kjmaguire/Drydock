package manifests_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

// A custom manifest's mcp section (decision P117): read strictly, its flags given the config file's path, and the adapter the
// registry builds says it takes MCP only when the section is there.
func TestUserCustomManifestsDeclareHowTheyTakeMCP(t *testing.T) {
	m, err := manifests.ParseUserCustom([]byte(userManifest("mycli") + "mcp: {via: arg, format: mcpServers, args: [\"--mcp-config={{mcp_config}}\", --strict]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	how, err := m.CustomMCP()
	if err != nil || how == nil || how.Via != manifests.CustomMCPViaArg {
		t.Fatalf("mcp section %+v: %v", how, err)
	}
	if got := how.ArgsFor(`C:\Drydock\runs\arun_1\mcp.json`); strings.Join(got, " ") != `--mcp-config=C:\Drydock\runs\arun_1\mcp.json --strict` {
		t.Fatalf("args %q", got)
	}
	r, err := manifests.NewRegistry(manifests.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ad, err := r.AddCustom(m, func(string) bool { return false })
	if err != nil || !ad.Capabilities().MCP {
		t.Fatalf("an adapter with an mcp section: %v", err)
	}
	plain, _ := manifests.ParseUserCustom([]byte(userManifest("plaincli")))
	if how, err := plain.CustomMCP(); how != nil || err != nil {
		t.Fatalf("no section: %+v %v", how, err)
	}
	ad, _ = r.AddCustom(plain, func(string) bool { return false })
	if ad.Capabilities().MCP {
		t.Fatal("an adapter without an mcp section claims MCP")
	}
	file, err := manifests.ParseUserCustom([]byte(userManifest("filecli") + "mcp: {via: file, format: mcpServers, file: .filecli/servers.json}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if how, _ := file.CustomMCP(); how == nil || how.File != ".filecli/servers.json" {
		t.Fatalf("file route %+v", how)
	}
	for _, bad := range []string{"a/../b.json", "./m.json", "C:/m.json", `a\\m.json`, ".GIT/m.json", ""} {
		if _, err := manifests.ParseUserCustom([]byte(userManifest("filecli") + "mcp: {via: file, format: mcpServers, file: \"" + bad + "\"}\n")); err == nil {
			t.Errorf("file %q was accepted", bad)
		}
	}
}
