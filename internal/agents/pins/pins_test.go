package pins_test

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/agents/pins"
)

// The pins come from the shipped manifests: every npm-installed CLI at its recommended version and every npx bridge at its
// exact version. The package names are the ones drydock-audit used to keep in a hand-written table.
func TestEmbeddedPinsMatchTheManifests(t *testing.T) {
	ps, err := pins.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	set, err := manifests.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	wantPkg := map[string]string{
		"cli:claude": "@anthropic-ai/claude-code", "cli:codex": "@openai/codex", "cli:gemini": "@google/gemini-cli",
		"cli:goose": "@aaif/goose-acp", "cli:opencode": "opencode-ai", "cli:cline": "cline",
		"bridge:@agentclientprotocol/claude-agent-acp": "@agentclientprotocol/claude-agent-acp",
		"bridge:@agentclientprotocol/codex-acp":        "@agentclientprotocol/codex-acp",
	}
	wantBin := map[string]string{
		"cli:claude": "claude", "cli:codex": "codex", "cli:gemini": "gemini", "cli:goose": "goose", "cli:opencode": "opencode", "cli:cline": "cline",
		"bridge:@agentclientprotocol/claude-agent-acp": "claude-agent-acp", "bridge:@agentclientprotocol/codex-acp": "codex-acp",
	}
	seen := map[string]bool{}
	for _, p := range ps {
		id := p.ID()
		if seen[id] {
			t.Errorf("duplicate pin %s", id)
		}
		seen[id] = true
		if wantPkg[id] != p.Package || wantBin[id] != p.Bin {
			t.Errorf("%s: package %q bin %q, want %q %q", id, p.Package, p.Bin, wantPkg[id], wantBin[id])
		}
		m, ok := set.Get(p.Adapter)
		if !ok {
			t.Fatalf("%s: no manifest %s", id, p.Adapter)
		}
		if p.Kind == pins.KindCLI && (p.Version != m.Versions.Recommended || p.Min != m.Versions.Min) {
			t.Errorf("%s: version %s min %s, the manifest says %s %s", id, p.Version, p.Min, m.Versions.Recommended, m.Versions.Min)
		}
		if p.Kind == pins.KindBridge {
			argv := m.Transports.ACP.Argv
			if want := p.Package + "@" + p.Version; argv[len(argv)-1] != want {
				t.Errorf("%s: %s is not what the manifest runs (%v)", id, want, argv)
			}
		}
		if p.File != "internal/agents/manifests/cli/"+p.Adapter+".yaml" {
			t.Errorf("%s: file %s", id, p.File)
		}
	}
	for id := range wantPkg {
		if !seen[id] {
			t.Errorf("missing pin %s", id)
		}
	}
	// a CLI comes before its own bridge, and the order follows the manifest ids
	if len(ps) > 2 && (ps[0].Adapter != "claude" || ps[0].Kind != pins.KindCLI || ps[1].Kind != pins.KindBridge) {
		t.Errorf("order: %+v %+v", ps[0], ps[1])
	}
}

func TestNPMPackageReadsInstallLines(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"npm install -g @anthropic-ai/claude-code", "@anthropic-ai/claude-code", true},
		{"npm i -g opencode-ai@latest", "opencode-ai", true},
		{"npm i --global cline@3.0.70", "cline", true},
		{"npm install -g @scope/name@1.2.3", "@scope/name", true},
		{"npm install cline", "", false}, // not global: not how a CLI is installed
		{"winget install Anthropic.ClaudeCode", "", false},
		{"irm https://claude.ai/install.ps1 | iex", "", false},
		{"npm", "", false},
	} {
		got, ok := pins.NPMPackage(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("NPMPackage(%q) = %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
