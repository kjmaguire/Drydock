package security

import (
	"context"
	"errors"
	"testing"

	"github.com/kjmaguire/drydock/internal/wsl"
)

var ubuntu = wsl.Paths{Distro: "Ubuntu", MountRoot: "/mnt/"}

// The path guard for an agent inside a WSL distro (decision P41): Linux paths are mapped to where they really are on Windows,
// a drive under the distro's mount root or the distro's own files, and checked against the worktree there.
func TestResolveWSLMapsMountedDrivesAndDistroPathsToTheirRealLocations(t *testing.T) {
	g := Guard{Windows: true, NoFS: true} // the Windows guard's rules, without a Windows filesystem
	drive := `D:\src\app\.drydock\wt\T1`
	distro := `\\wsl.localhost\Ubuntu\home\k\code\.drydock-worktrees\p1\T1`
	cases := []struct {
		root, cand, want string
		escape           bool
	}{
		{drive, "/mnt/d/src/app/.drydock/wt/T1/main.go", `D:\src\app\.drydock\wt\T1\main.go`, false},
		{drive, "main.go", `D:\src\app\.drydock\wt\T1\main.go`, false},
		{drive, `D:\src\app\.drydock\wt\T1\x\y.go`, `D:\src\app\.drydock\wt\T1\x\y.go`, false}, // a Windows path from an interop program
		{drive, "/mnt/d/src/app/secrets.txt", "", true},
		{drive, "/mnt/c/Users/k/.ssh/id_ed25519", "", true},
		{drive, "/home/k/notes.txt", "", true}, // the distro's own disk is not the worktree's drive
		{drive, "../../../etc/passwd", "", true},
		{distro, "/home/k/code/.drydock-worktrees/p1/T1/src/a.ts", `\\wsl.localhost\Ubuntu\home\k\code\.drydock-worktrees\p1\T1\src\a.ts`, false},
		{distro, "src/a.ts", `\\wsl.localhost\Ubuntu\home\k\code\.drydock-worktrees\p1\T1\src\a.ts`, false},
		{distro, "/home/k/code/app/main.go", "", true},                // the repository's main checkout is not this task's worktree
		{distro, "/home/k/code/.drydock-worktrees/p1/t1/a", "", true}, // Linux names are case-sensitive
		{distro, "/mnt/c/Windows/win.ini", "", true},
		{distro, "~/.bashrc", "", true},
		{distro, `\\wsl.localhost\Debian\home\k\code\.drydock-worktrees\p1\T1\a`, "", true}, // another distro
	}
	for _, c := range cases {
		got, err := g.ResolveWSL(ubuntu, c.root, c.cand)
		if c.escape {
			if err == nil {
				t.Errorf("%s under %s: allowed as %s", c.cand, c.root, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s under %s: %q %v, want %q", c.cand, c.root, got, err, c.want)
		}
	}
	if _, err := g.ResolveWSL(ubuntu, `\\fileserver\share\repo`, "a"); !errors.Is(err, ErrPathRoot) {
		t.Errorf("a root the distro cannot reach: %v", err)
	}
}

// The command analyzer treats a WSL agent's command as bash with Unix paths, checked against the worktree's real location.
func TestAnalyzerReadsAWSLAgentsCommandsAsBashWithTheDistrosPaths(t *testing.T) {
	an, err := NewAnalyzer(WithGuard(Guard{Windows: true, NoFS: true}))
	if err != nil {
		t.Fatal(err)
	}
	root := `\\wsl.localhost\Ubuntu\home\k\code\.drydock-worktrees\p1\T1`
	inside := func(cmd string) (bool, CommandReport) {
		rep, err := an.Analyze(context.Background(), CommandInput{Raw: cmd, Shell: ShellPowerShell, Root: root, Cwd: root, WSL: &ubuntu})
		if err != nil {
			t.Fatal(err)
		}
		return rep.AllInside(), rep
	}
	for _, cmd := range []string{"ls -la src", "rm -rf /home/k/code/.drydock-worktrees/p1/T1/build", "cd src && cat a.ts > copy.ts", "go test ./..."} {
		if ok, rep := inside(cmd); !ok || rep.Shell != ShellBash || rep.Confidence != ParsedExact {
			t.Errorf("%q: inside=%v shell=%s conf=%s paths=%+v", cmd, ok, rep.Shell, rep.Confidence, rep.Paths)
		}
	}
	for _, cmd := range []string{"cat /home/k/.ssh/id_ed25519", "rm -rf /mnt/c/Users/k/Documents", "cat ~/.profile", "cp a.ts /tmp/x", "cd /home/k && rm -rf code",
		`/mnt/c/Windows/System32/cmd.exe /c "type C:\Users\k\secret.txt"`} {
		if ok, rep := inside(cmd); ok {
			t.Errorf("%q passed as inside the worktree: %+v", cmd, rep.Paths)
		}
	}
	// a path inside is reported where it really is on Windows
	_, rep := inside("cat /home/k/code/.drydock-worktrees/p1/T1/README.md")
	if len(rep.Paths) != 1 || rep.Paths[0].Canonical != root+`\README.md` {
		t.Fatalf("%+v", rep.Paths)
	}
	// a known CLI credential file is refused outright inside the distro too
	if _, rep := inside("cat /home/k/.claude/.credentials.json"); len(rep.HardDeny) == 0 {
		t.Fatal("the distro's Claude sign-in must be refused like the Windows one")
	}
}

func TestPermissionRequestsOfAWSLAgentAreCheckedInTheDistrosTerms(t *testing.T) {
	an, err := NewAnalyzer(WithGuard(Guard{Windows: true, NoFS: true}))
	if err != nil {
		t.Fatal(err)
	}
	root := `D:\src\app-wt\T1`
	ask := func(tool string, in any) ToolDecision {
		d, err := ClassifyToolRequest(context.Background(), an, nil, ToolRequest{Tool: tool, Input: input(t, in), Root: root, WSL: &ubuntu})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := ask("Edit", map[string]any{"file_path": "/mnt/d/src/app-wt/T1/calc.go", "old_string": "a", "new_string": "b"}); d.Verdict != VerdictAllow ||
		d.Action.Targets[0].Path != `D:\src\app-wt\T1\calc.go` {
		t.Fatalf("an edit inside: %s %+v %v", d.Verdict, d.Action.Targets, d.Reasons)
	}
	if d := ask("Read", map[string]any{"file_path": "/home/k/.aws/credentials"}); d.Verdict == VerdictAllow {
		t.Fatalf("a read of the distro's home: %s", d.Verdict)
	}
	if d := ask("Write", map[string]any{"file_path": "/mnt/d/src/app-wt/T1/.git/hooks/pre-commit", "content": "x"}); d.Verdict != VerdictAsk || d.Kind != KindAgentTool {
		t.Fatalf("a write into .git: %s %s", d.Verdict, d.Kind)
	}
	if d := ask("Bash", map[string]any{"command": "mkdir -p out"}); d.Verdict != VerdictAllow || d.Action.Shell != string(ShellBash) {
		t.Fatalf("a write inside by command: %s %s %v", d.Verdict, d.Action.Shell, d.Reasons)
	}
	if d := ask("Bash", map[string]any{"command": "rm -rf /mnt/d/src/app"}); d.Verdict == VerdictAllow {
		t.Fatal("deleting the main checkout from inside the distro")
	}
}
