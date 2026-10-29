package manifests_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

// A person's manifest, as the Agents page would add it: the template with its own id, program and gate.
func userManifest(id string) string {
	tpl, err := manifests.CustomTemplate()
	if err != nil {
		panic(err)
	}
	s := strings.Replace(string(tpl), "id: custom,", "id: "+id+",", 1)
	s = strings.Replace(s, "display_name: Custom CLI (template)", "display_name: "+id, 1)
	return strings.Replace(s, "gate: {id: custom-runs-arbitrary-code-v1,", "gate: {id: custom-"+id+",", 1)
}

func TestUserCustomManifestsAreStrict(t *testing.T) {
	if _, err := manifests.ParseUserCustom([]byte(userManifest("mycli"))); err != nil {
		t.Fatalf("the template made the person's own: %v", err)
	}
	tpl, _ := manifests.CustomTemplate()
	for name, c := range map[string]struct{ yaml, want string }{
		"the template itself":  {string(tpl), "not the template's"},
		"shared gate":          {strings.Replace(userManifest("mycli"), "id: custom-mycli,", "id: custom-runs-arbitrary-code-v1,", 1), "gate.id must be custom-mycli"},
		"not custom":           {strings.Replace(userManifest("mycli"), "custom: true, ", "", 1) + "versions: {min: \"1.0.0\"}\n", "meta.custom must be true"},
		"too large":            {userManifest("mycli") + "# " + strings.Repeat("x", manifests.MaxCustomBytes), "larger than"},
		"two documents":        {userManifest("mycli") + "---\n" + userManifest("other"), "more than one YAML document"},
		"an unknown key":       {userManifest("mycli") + "surprise: true\n", "surprise"},
		"an mcp section":       {userManifest("mycli") + "mcp: {inject: env}\n", "cannot carry provider_config, mcp or usage"},
		"a built-in gate text": {strings.Replace(userManifest("mycli"), `text: "This Connection runs a program you chose with the permissions of your user account. Only enable CLIs you trust."`, `text: ""`, 1), "gate.text"},
	} {
		if _, err := manifests.ParseUserCustom([]byte(c.yaml)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestUserCustomFolderLoadsWhatValidatesAndSaysWhyNot(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mycli.yaml"), []byte(userManifest("mycli")), 0o644)
	os.WriteFile(filepath.Join(dir, "wrong-name.yaml"), []byte(userManifest("other")), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("meta: ["), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644)
	ms, files := manifests.LoadUserCustom(dir)
	if len(ms) != 1 || ms[0].Meta.ID != "mycli" {
		t.Fatalf("loaded %v", ms)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.ID] = f.Error
	}
	if len(got) != 3 || got["mycli"] != "" || !strings.Contains(got["wrong-name"], "must match the file name") || got["broken"] == "" {
		t.Fatalf("files %+v", files)
	}
	if ms, files := manifests.LoadUserCustom(filepath.Join(dir, "missing")); ms != nil || files != nil {
		t.Fatal("no folder, no CLIs")
	}

	r := newRegistry(t, manifests.Options{})
	builtin := func(id string) bool { return id == "claude" || id == "custom" }
	a, err := r.AddCustom(ms[0], builtin)
	if err != nil || a.ID() != "mycli" {
		t.Fatalf("add: %v %v", a, err)
	}
	if caps := a.Capabilities(); !caps.ToolCallVisibility || !caps.TokenUsage || !caps.SessionResume || !caps.ModelSelection || caps.EffortControl || !caps.Interrupt || caps.MCP {
		t.Fatalf("the runner's capabilities follow the manifest: %+v", caps)
	}
	if _, err := r.AddCustom(ms[0], builtin); err == nil {
		t.Fatal("added twice")
	}
	cl, err := manifests.ParseUserCustom([]byte(userManifest("claude")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddCustom(cl, builtin); !errors.Is(err, manifests.ErrBuiltinID) {
		t.Fatalf("a built-in id: %v", err)
	}
	if err := r.RemoveCustom("claude", builtin); !errors.Is(err, manifests.ErrBuiltinID) {
		t.Fatalf("a built-in cannot be removed: %v", err)
	}
	if err := r.RemoveCustom("mycli", builtin); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Set.Get("mycli"); ok {
		t.Fatal("still in the set")
	}
	if err := r.RemoveCustom("mycli", builtin); err == nil {
		t.Fatal("removed twice")
	}
}
