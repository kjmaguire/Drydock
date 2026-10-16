package manifests_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

// The Cline manifest (decisions P29) against what the cline/cline 3.0.70 source and the live handshake showed. The live part is
// TestLiveACPHandshake/cline (internal/agents/acp), TestLiveACPConnections/cline (internal/connections) and
// TestLiveACPHandshakes/cline (internal/agents/conformance); each skips with a reason when cline is not installed.
func TestClineManifestFacts(t *testing.T) {
	m := get(t, "cline")
	if m.Transports.Primary != manifests.TransportACP || !m.ACPEnabled() || !reflect.DeepEqual(m.Transports.ACP.Argv, []string{"cline", "--acp"}) {
		t.Fatalf("transport %+v", m.Transports)
	}
	if m.Transports.Headless != nil {
		t.Error("only the ACP transport is declared")
	}
	for _, a := range m.Transports.ACP.Argv {
		if strings.Contains(a, "auto-approve") || a == "-y" || a == "--yolo" {
			t.Fatalf("tool approval must stay with Drydock: %v", m.Transports.ACP.Argv)
		}
	}
	if m.InstallHint() != "cmd: npm i -g cline" {
		t.Errorf("install hint %q", m.InstallHint())
	}
	if len(m.Requires) != 1 || m.Requires[0].Tool != "node" || m.Requires[0].Min != "22" {
		t.Errorf("the npm launcher is a Node script: Node 22+ is required to run it (D9): %+v", m.Requires)
	}
	if m.Versions.Min != "3.0.70" || m.Versions.Recommended != "3.0.71" || !reflect.DeepEqual(m.Detect.Version.Argv, []string{"--version"}) {
		t.Errorf("versions %+v detect %+v", m.Versions, m.Detect)
	}
	if m.Auth.Probe != nil || m.Auth.Login == nil || !reflect.DeepEqual(m.Auth.Login.Argv, []string{"auth"}) || !m.Auth.Login.Terminal {
		t.Errorf("auth %+v", m.Auth)
	}
	if m.MCP["inject"] != "acp_session_new" || m.MCP["unverified"] != true {
		t.Errorf("mcp %+v", m.MCP)
	}
	c := manifests.DeclaredCapabilities(m)
	if !c.SessionResume || !c.ImageInput || !c.Interrupt || !c.ToolCallVisibility || c.EffortControl || c.TokenUsage || c.CostReporting || c.MCP {
		t.Errorf("declared %+v", c)
	}
	if m.Meta.License != "Apache-2.0" {
		t.Errorf("license %q", m.Meta.License)
	}
}

func TestClineBuildsAsTheGenericACPAdapterWithDrydocksPolicy(t *testing.T) {
	r := newRegistry(t, manifests.Options{ScrubEnv: true})
	a, err := r.Build("cline")
	if err != nil {
		t.Fatal(err)
	}
	ad, ok := a.(*acp.Adapter)
	if !ok {
		t.Fatalf("%T", a)
	}
	cfg := ad.Config()
	if cfg.Command != "cline" || strings.Join(cfg.Args, " ") != "--acp" || len(cfg.Env) != 0 || !cfg.ScrubEnv || cfg.Terminal || cfg.Permission != nil {
		t.Fatalf("config %+v", cfg) // nil Permission: every request is refused until a human answers (acp.RejectOnce)
	}
	if got := acp.RejectOnce(context.Background(), acp.PermissionRequest{Options: []acp.PermissionOption{
		{ID: "allow_once", Kind: acp.KindAllowOnce}, {ID: "allow_always", Kind: acp.KindAllowAlways}, {ID: "reject_once", Kind: acp.KindRejectOnce}}}); got != "reject_once" {
		t.Fatalf("Cline's own options must be refused by default, got %q", got)
	}
	t.Setenv("PATH", t.TempDir())
	d, err := a.Detect(context.Background())
	if err != nil || d.Installed || d.Status != agents.StatusNotInstalled || d.InstallHint != "cmd: npm i -g cline" {
		t.Fatalf("detect without cline: %+v %v", d, err)
	}
}
