package core

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/manifests"

	"github.com/kjmaguire/drydock/internal/terminal"
)

// X27: the shell reused is the newest one still running in the folder, never a sign-in (no project) or one that ended.
func TestRunningShellIn(t *testing.T) {
	list := []terminal.Info{
		{ID: "a", Cwd: "/r", ProjectID: "p"},
		{ID: "b", Cwd: "/r/", ProjectID: "p"},
		{ID: "c", Cwd: "/r", ProjectID: "p", Exited: true},
		{ID: "d", Cwd: "/r"},
		{ID: "e", Cwd: "/other", ProjectID: "p"},
	}
	if in, ok := runningShellIn(list, "/r"); !ok || in.ID != "b" {
		t.Fatalf("got %+v %v, want b", in, ok)
	}
	if _, ok := runningShellIn(list[2:4], "/r"); ok {
		t.Fatal("reused an exited shell or a sign-in")
	}
}

// A5: what is typed for the person is one line with no line break, so typing it runs nothing.
func TestTypedLineNeverEndsALine(t *testing.T) {
	for in, want := range map[string]string{
		"npm install -g x":  "npm install -g x",
		"  npm i x\n":       "npm i x",
		"first\r\nrm -rf /": "first",
		"":                  "",
		"\nsecond":          "",
	} {
		if got := typedLine(in); got != want {
			t.Errorf("typedLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// A5: the line typed in the install shell is the manifest's command for that shell, never the display label
// ("powershell: ...", "cmd: ...", "inside the distro: ..."), which no shell runs as typed.
func TestInstallLineIsTheRunnableCommand(t *testing.T) {
	claude := []manifests.Install{
		{Shell: "powershell", Command: "irm https://claude.ai/install.ps1 | iex"},
		{Shell: "cmd", Command: "curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd"},
		{Shell: "sh", RunMode: "wsl", Command: "curl -fsSL https://claude.ai/install.sh | bash"},
	}
	gemini := []manifests.Install{{Shell: "cmd", Command: "npm install -g @google/gemini-cli"}}
	quoted := []manifests.Install{{Shell: "cmd", Command: "echo 'x'"}}
	for _, tc := range []struct {
		installs []manifests.Install
		wsl      bool
		shell    string
		want     string
	}{
		{claude, false, `C:\Program Files\PowerShell\7\pwsh.exe`, "irm https://claude.ai/install.ps1 | iex"},
		{claude, false, `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "irm https://claude.ai/install.ps1 | iex"},
		{claude, false, `C:\Windows\system32\cmd.exe`, "curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd"},
		{claude, true, `C:\Program Files\PowerShell\7\pwsh.exe`, "curl -fsSL https://claude.ai/install.sh | bash"},
		{gemini, false, `C:\Program Files\PowerShell\7\pwsh.exe`, "cmd /c 'npm install -g @google/gemini-cli'"},
		{gemini, false, `C:\Windows\system32\cmd.exe`, "npm install -g @google/gemini-cli"},
		{gemini, false, "/bin/sh", "npm install -g @google/gemini-cli"},
		{gemini, true, "/bin/sh", ""},
		{quoted, false, "pwsh.exe", "cmd /c 'echo ''x'''"},
		{nil, false, "pwsh.exe", ""},
	} {
		got := installLine(tc.installs, tc.wsl, tc.shell)
		if got != tc.want {
			t.Errorf("installLine(wsl=%v, %s) = %q, want %q", tc.wsl, tc.shell, got, tc.want)
		}
		for _, label := range []string{"powershell: ", "cmd: ", "sh: ", "inside the distro: "} {
			if strings.HasPrefix(got, label) {
				t.Errorf("installLine typed the display label %q", got)
			}
		}
	}
}
