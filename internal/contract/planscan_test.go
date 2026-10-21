package contract_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/contract"
)

// The scanner finds every way the code base writes plan mode: in a literal, in an assignment, paired with ReadOnly in the same
// statement or block or not at all. Test files and testdata are not product code.
func TestFindPlanModeSitesSeesEveryForm(t *testing.T) {
	root := t.TempDir()
	src := `package x

type RunSpec struct{ PermissionMode string; ReadOnly bool }
type Agent struct{ PermissionMode string }

func a() RunSpec { return RunSpec{PermissionMode: "plan", ReadOnly: true} }
func b() RunSpec { return RunSpec{PermissionMode: "plan"} }
func c(s *RunSpec) { s.PermissionMode, s.ReadOnly = "plan", true }
func d(s *RunSpec) {
	s.PermissionMode = "plan"
	s.ReadOnly = true
}
func e(s, o *RunSpec) {
	s.PermissionMode = "plan"
	o.ReadOnly = true
}
func f(role string, s *RunSpec) {
	switch role {
	case "review":
		s.PermissionMode = "plan"
	}
}
func g() Agent { return Agent{PermissionMode: "plan"} }
func h(s *RunSpec) { s.PermissionMode = "default" }
`
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/x/x.go", src)
	write("internal/x/x_test.go", "package x\n\nfunc t1(s *RunSpec) { s.PermissionMode = \"plan\" }\n")
	write("internal/x/testdata/y.go", "package y\n\nvar v = struct{ PermissionMode string }{PermissionMode: \"plan\"}\n")
	sites, err := contract.FindPlanModeSites(root, "internal")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range sites {
		got = append(got, strings.Join([]string{s.File, strconv.Itoa(s.Line), s.Code, map[bool]string{true: "ro", false: "NOT-RO"}[s.ReadOnly]}, " "))
	}
	want := []string{
		`internal/x/x.go 6 RunSpec{PermissionMode: "plan"} ro`,
		`internal/x/x.go 7 RunSpec{PermissionMode: "plan"} NOT-RO`,
		`internal/x/x.go 8 s.PermissionMode = "plan" ro`,
		`internal/x/x.go 10 s.PermissionMode = "plan" ro`,
		`internal/x/x.go 14 s.PermissionMode = "plan" NOT-RO`, // another variable's ReadOnly does not count
		`internal/x/x.go 20 s.PermissionMode = "plan" NOT-RO`,
		`internal/x/x.go 23 Agent{PermissionMode: "plan"} NOT-RO`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sites\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// CLAUDE-PLAN-01, the static half: Drydock's own code asks for plan mode only together with ReadOnly, and the adapter turns a
// read-only spec into the default mode with the writing tools denied. Free; no CLI needed.
func TestDrydockNeverStartsClaudeInPlanMode(t *testing.T) {
	root := repoRoot(t)
	sites, err := contract.FindPlanModeSites(root, "internal", "cmd", "app")
	if err != nil {
		t.Fatal(err)
	}
	var where []string
	for _, s := range sites {
		where = append(where, s.File+":"+strconv.Itoa(s.Line))
		if !s.ReadOnly {
			t.Errorf("%s:%d asks for plan mode without ReadOnly (%s): Claude Code would answer a haiku session with a pricier model", s.File, s.Line, s.Code)
		}
	}
	if len(sites) == 0 {
		t.Fatal("no place asks for plan mode: the scan found nothing, so it is probably looking in the wrong place")
	}
	t.Logf("%s%d places ask for plan mode, all read-only: %s", contract.NotePrefix, len(sites), strings.Join(where, ", "))

	args := claude.New().Args(agents.RunSpec{Model: "haiku", PermissionMode: "plan", ReadOnly: true})
	if slices.Contains(args, "plan") {
		t.Fatalf("a read-only spec reached the CLI as plan mode: %v", args)
	}
	if i := slices.Index(args, "--permission-mode"); i < 0 || args[i+1] != "default" {
		t.Fatalf("a read-only spec must run in the default mode: %v", args)
	}
	if i := slices.Index(args, "--disallowedTools"); i < 0 || !strings.HasPrefix(args[i+1], strings.Join(claude.ReadOnlyDenied, ",")) {
		t.Fatalf("a read-only spec must deny the writing tools: %v", args)
	}
	// What the scan cannot see: a mode the human picks at run time. Say whether the adapter still offers plan mode for that.
	if slices.Contains(claude.New().Capabilities().PermissionModes, "plan") {
		t.Logf("%sthe Claude adapter still offers plan mode to the human (chats and agents can choose it), and a spec that is not read-only passes it through as --permission-mode plan", contract.NotePrefix)
	}
}
