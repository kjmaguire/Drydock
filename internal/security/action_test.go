package security

import (
	"strings"
	"testing"
)

func TestCanonicalJSONGolden(t *testing.T) {
	got, err := CanonicalJSON(map[string]any{
		"z": 1, "a": []any{true, nil, "x<y>& \n", 1.5}, "m": map[string]any{"b": 2, "a": "é\x7f"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":[true,null,"x<y>& \n",1.5],"m":{"a":"é\u007f","b":2},"z":1}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// struct field order does not matter, keys are sorted
	type s struct {
		B int    `json:"b"`
		A string `json:"a"`
	}
	got, _ = CanonicalJSON(s{B: 1, A: "q"})
	if string(got) != `{"a":"q","b":1}` {
		t.Fatalf("struct: %s", got)
	}
}

func baseAction() ActionDescriptor {
	return ActionDescriptor{
		Kind: KindShellDestructive, Project: "proj_1", TaskID: "task_1", RunID: "run_1", NodeID: "n1", Step: 3, Attempt: 1,
		AgentID: "agent_1", WorktreeID: "wt_1", Cwd: "/work/repo", Tool: "shell", Shell: "bash", Command: "rm -rf build",
		Program: "rm", Argv: []string{"-rf", "build"}, ArgvDigest: "abc",
		Targets: []Target{{Path: "/work/repo/build", VolumeSerial: "1234", FileIndex: "99"}},
		EnvKeys: []string{"PATH", "HOME"},
	}
}

func TestActionCanonicalGolden(t *testing.T) {
	a := baseAction()
	b, err := a.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"agentId":"agent_1","argv":["-rf","build"],"argvDigest":"abc","attempt":1,"command":"rm -rf build","cwd":"/work/repo",` +
		`"envDigest":"` + EnvDigest([]string{"HOME", "PATH"}) + `","envKeys":["HOME","PATH"],"kind":"shell_destructive","nodeId":"n1","program":"rm","project":"proj_1",` +
		`"runId":"run_1","shell":"bash","step":3,"targets":[{"fileIndex":"99","newOid":"","oldOid":"","path":"/work/repo/build","ref":"","remote":"","volumeSerial":"1234"}],` +
		`"taskId":"task_1","tool":"shell","v":1,"worktreeId":"wt_1"}`
	if string(b) != want {
		t.Fatalf("canonical form changed:\n got %s\nwant %s", b, want)
	}
	if len(a.Hash()) != 64 {
		t.Fatalf("hash %q", a.Hash())
	}
}

func TestActionHashProperties(t *testing.T) {
	a := baseAction()
	h := a.Hash()
	if h != baseAction().Hash() {
		t.Fatal("hash is not deterministic")
	}
	// order-insensitive: env keys and targets
	b := baseAction()
	b.EnvKeys = []string{"HOME", "PATH", "HOME"}
	if b.Hash() != h {
		t.Error("env key order or duplicates changed the hash")
	}
	c := baseAction()
	c.Targets = append(c.Targets, Target{Path: "/work/repo/a"}, Target{Path: "/work/repo/b"})
	d := baseAction()
	d.Targets = []Target{{Path: "/work/repo/b"}, {Path: "/work/repo/build", VolumeSerial: "1234", FileIndex: "99"}, {Path: "/work/repo/a"}}
	if c.Hash() != d.Hash() {
		t.Error("target order changed the hash")
	}
	// every field is bound
	mut := map[string]func(*ActionDescriptor){
		"kind":     func(x *ActionDescriptor) { x.Kind = KindDeploy },
		"project":  func(x *ActionDescriptor) { x.Project = "p2" },
		"task":     func(x *ActionDescriptor) { x.TaskID = "t2" },
		"run":      func(x *ActionDescriptor) { x.RunID = "run_2" },
		"node":     func(x *ActionDescriptor) { x.NodeID = "n2" },
		"step":     func(x *ActionDescriptor) { x.Step = 4 },
		"attempt":  func(x *ActionDescriptor) { x.Attempt = 2 },
		"agent":    func(x *ActionDescriptor) { x.AgentID = "a2" },
		"worktree": func(x *ActionDescriptor) { x.WorktreeID = "w2" },
		"cwd":      func(x *ActionDescriptor) { x.Cwd = "/work/other" },
		"tool":     func(x *ActionDescriptor) { x.Tool = "git" },
		"shell":    func(x *ActionDescriptor) { x.Shell = "cmd" },
		"command":  func(x *ActionDescriptor) { x.Command = "rm -rf build " },
		"program":  func(x *ActionDescriptor) { x.Program = "del" },
		"argv":     func(x *ActionDescriptor) { x.Argv = []string{"build", "-rf"} },
		"digest":   func(x *ActionDescriptor) { x.ArgvDigest = "abd" },
		"target":   func(x *ActionDescriptor) { x.Targets[0].FileIndex = "100" },
		"envkeys":  func(x *ActionDescriptor) { x.EnvKeys = append(x.EnvKeys, "GH_TOKEN"); x.EnvDigest = "" },
		"version":  func(x *ActionDescriptor) { x.V = 2 },
	}
	for name, f := range mut {
		x := baseAction()
		f(&x)
		if x.Hash() == h {
			t.Errorf("changing %s did not change the hash", name)
		}
	}
	// zero V defaults to the current version
	z := baseAction()
	z.V = DescriptorVersion
	if z.Hash() != h {
		t.Error("explicit V=1 differs from default")
	}
	// nil vs empty slices hash alike
	n1, n2 := ActionDescriptor{Kind: "k"}, ActionDescriptor{Kind: "k", Argv: []string{}, Targets: []Target{}, EnvKeys: []string{}}
	if n1.Hash() != n2.Hash() {
		t.Error("nil and empty slices differ")
	}
}

func TestEnvDigestIsValueFree(t *testing.T) {
	if EnvDigest([]string{"B", "A", "A"}) != EnvDigest([]string{"A", "B"}) {
		t.Fatal("digest depends on order or duplicates")
	}
	if strings.Contains(string(mustCanon(t, baseAction())), "secret") {
		t.Fatal("unexpected content")
	}
}

func mustCanon(t *testing.T, a ActionDescriptor) []byte {
	t.Helper()
	b, err := a.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
