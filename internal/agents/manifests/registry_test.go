package manifests_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/proc"
)

func newRegistry(t *testing.T, o manifests.Options) *manifests.Registry {
	t.Helper()
	r, err := manifests.NewRegistry(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRegistryBuildsEveryManifest(t *testing.T) {
	r := newRegistry(t, manifests.Options{})
	all, errs := r.BuildAll()
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(all) != 8 {
		t.Fatalf("built %d adapters", len(all))
	}
	for id, ad := range all {
		if ad.ID() != id {
			t.Errorf("%s: adapter id %q", id, ad.ID())
		}
	}
	// ACP manifests become the generic ACP adapter with the manifest's command, args and env
	for id, want := range map[string]struct {
		cmd  string
		args []string
	}{
		"codex":    {"npx", []string{"-y", "@agentclientprotocol/codex-acp@2.2.2"}},
		"gemini":   {"gemini", []string{"--acp"}},
		"opencode": {"opencode", []string{"acp"}},
		"goose":    {"goose", []string{"acp"}},
		"cline":    {"cline", []string{"--acp"}},
	} {
		a, ok := all[id].(*acp.Adapter)
		if !ok {
			t.Errorf("%s is %T, want *acp.Adapter", id, all[id])
			continue
		}
		cfg := a.Config()
		if cfg.Command != want.cmd || strings.Join(cfg.Args, " ") != strings.Join(want.args, " ") {
			t.Errorf("%s: %s %v", id, cfg.Command, cfg.Args)
		}
		if cfg.InstallHint == "" || !cfg.Declared.Streaming {
			t.Errorf("%s: declared/hint not carried: %+v", id, cfg)
		}
		if cfg.Terminal {
			t.Errorf("%s: terminal must be off by default", id)
		}
	}
	if env := all["gemini"].(*acp.Adapter).Config().Env; len(env) != 1 || env[0] != "GEMINI_CLI_TRUST_WORKSPACE=true" {
		t.Errorf("gemini env %v", env)
	}
	// the CLIs that act on configuration in their working folder say which files (audit 2026-10-16, M3)
	for id, want := range map[string]string{"gemini": ".gemini/settings.json", "opencode": "opencode.json", "codex": ".codex/", "cline": ".clinerules/hooks/"} {
		rc, ok := all[id].(agents.RepoConfigReader)
		if !ok || !slices.Contains(rc.RepoConfigPaths(), want) {
			t.Errorf("%s does not list %s: %v", id, want, ok)
		}
	}
	if paths := all["gemini"].(agents.RepoConfigReader).RepoConfigPaths(); !slices.Contains(paths, ".env") || !slices.Contains(paths, ".gemini/policies/") {
		t.Errorf("gemini paths %v", paths)
	}
	if env := all["goose"].(*acp.Adapter).Config().Env; len(env) != 1 || env[0] != "GOOSE_MODE=approve" {
		t.Errorf("goose env %v", env)
	}
	// claude is the existing headless adapter
	caps := all["claude"].Capabilities()
	if !caps.Headless || !caps.Interrupt || caps.ImageInput {
		t.Errorf("claude caps %+v", caps)
	}
}

func TestRegistryPassesCallerPolicyToACPAdapters(t *testing.T) {
	called := false
	r := newRegistry(t, manifests.Options{Terminal: true, ScrubEnv: true, Permission: func(context.Context, acp.PermissionRequest) string { called = true; return "" }})
	a, err := r.Build("gemini")
	if err != nil {
		t.Fatal(err)
	}
	cfg := a.(*acp.Adapter).Config()
	if !cfg.Terminal || !cfg.ScrubEnv || cfg.Permission == nil {
		t.Fatalf("%+v", cfg)
	}
	cfg.Permission(context.Background(), acp.PermissionRequest{})
	if !called {
		t.Fatal("handler not wired")
	}
}

func TestClaudeACPBridgeStaysOffUntilConfirmed(t *testing.T) {
	// the shipped claude manifest, but with the ACP bridge as the primary transport: the registry must refuse while enabled: false
	b, err := fs.ReadFile(manifests.EmbeddedFS(), "cli/claude.yaml")
	if err != nil {
		t.Fatal(err)
	}
	y := strings.Replace(string(b), "primary: headless", "primary: acp", 1)
	s, err := manifests.Load(fstest.MapFS{"m/claude.yaml": {Data: []byte(y)}}, "m")
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, manifests.Options{Set: s, Compat: mustCompat(t, "claude")})
	if _, err := r.Build("claude"); err == nil || !strings.Contains(err.Error(), "disabled until written_confirmation_recorded") {
		t.Fatalf("err %v", err)
	}
}

func mustCompat(t *testing.T, ids ...string) *manifests.Compat {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("schema: 1\nnode: {min: '22'}\nadapters:\n")
	for _, id := range ids {
		sb.WriteString("  " + id + ": {min: '0.0.1', entries: []}\n")
	}
	c, err := manifests.LoadCompat([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Antigravity is built as the headless adapter of its documented stream-json mode (decision P115); with no agy installed it
// says so at Start, and what its manifest leaves unknown does not read as a capability.
func TestAntigravityBuildsTheDocumentedHeadlessAdapter(t *testing.T) {
	r := newRegistry(t, manifests.Options{})
	a, err := r.Build("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err = a.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "x", Cwd: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err %v", err)
	}
	d, err := a.Detect(context.Background())
	if err != nil || d.Installed || d.Status != agents.StatusNotInstalled || d.InstallHint == "" || d.Adapter != "antigravity" {
		t.Fatalf("%+v %v", d, err)
	}
	caps := a.Capabilities()
	if caps.ToolCallVisibility || caps.SessionResume || caps.Interrupt || !caps.Headless || !caps.EffortControl {
		t.Fatalf("unknown capabilities must not read as true: %+v", caps)
	}
	type argser interface{ Args(agents.RunSpec) []string }
	got := strings.Join(a.(argser).Args(agents.RunSpec{Effort: "high"}), " ")
	if got != "-p --input-format stream-json --output-format stream-json --mode default --effort high" {
		t.Fatalf("argv %q", got)
	}
}

func TestCustomManifestAdapterIsExplicitlyNotImplemented(t *testing.T) {
	r := newRegistry(t, manifests.Options{})
	a, err := r.Build("custom")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "x", Cwd: t.TempDir()}); !errors.Is(err, manifests.ErrNotImplemented) {
		t.Fatalf("err %v", err)
	}
}

func TestRegistryDetectIsRoutedThroughTheManifestDetector(t *testing.T) {
	var seen []string
	r := newRegistry(t, manifests.Options{Detect: func(_ context.Context, m *manifests.Manifest) (agents.Detection, error) {
		seen = append(seen, m.Meta.ID)
		return agents.Detection{Adapter: m.Meta.ID, Status: agents.StatusDegraded, Problems: []string{"from the manifest detector"}}, nil
	}})
	all, _ := r.BuildAll()
	for _, id := range []string{"claude", "gemini", "antigravity"} {
		d, err := all[id].Detect(context.Background())
		if err != nil || d.Adapter != id || len(d.Problems) != 1 {
			t.Errorf("%s: %+v %v", id, d, err)
		}
	}
	if strings.Join(seen, ",") != "claude,gemini,antigravity" {
		t.Errorf("seen %v", seen)
	}
}

func TestRegistryRejectsUnknownIDsAndInconsistentCompat(t *testing.T) {
	r := newRegistry(t, manifests.Options{})
	if _, err := r.Build("nope"); err == nil {
		t.Fatal("unknown id")
	}
	s, _ := manifests.Load(fstest.MapFS{"m/demo.yaml": {Data: []byte(baseACP)}}, "m")
	if _, err := manifests.NewRegistry(manifests.Options{Set: s}); err == nil || !strings.Contains(err.Error(), "no manifest") {
		t.Fatalf("compat naming an adapter without a manifest must fail: %v", err)
	}
}

func TestACPPlaceholderEnvMustBeResolvedBeforeBuilding(t *testing.T) {
	y := strings.Replace(baseACP, "{argv: [demo, acp]}", "{argv: [demo, acp], env: {EXE: '{{resolved_binary}}'}}", 1)
	s, err := manifests.Load(fstest.MapFS{"m/demo.yaml": {Data: []byte(y)}}, "m")
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, manifests.Options{Set: s, Compat: mustCompat(t, "demo")})
	if _, err := r.Build("demo"); err == nil || !strings.Contains(err.Error(), "unresolved placeholder") {
		t.Fatalf("err %v", err)
	}
}
