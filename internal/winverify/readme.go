package winverify

import (
	"fmt"
	"strings"
)

// Readme is README-VERIFY.txt: what the bundle is, the exact steps to run it, what each step proves, what it costs and
// what the result zip holds. Windows bundles get CRLF line ends so Notepad shows them on any version.
func Readme(m *Manifest) string {
	exe := m.Verify
	run := `.\` + exe
	if m.GOOS != "windows" {
		run = "./" + exe
	}
	nNeedsGo, nReal := 0, 0
	for _, p := range m.Packages {
		nNeedsGo += len(p.NeedsGo)
		nReal += len(p.RealClaude)
	}
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	step := 0
	n := func() int { step++; return step }
	w("DRYDOCK VERIFICATION BUNDLE")
	w("===========================")
	w("")
	w("Built from commit %s on %s for %s/%s with %s.", m.Commit, m.Built.Format("2006-01-02"), m.GOOS, m.GOARCH, m.GoVersion)
	w("")
	w("This folder holds Drydock's own tests, compiled for %s, and a small program that runs them and writes a report.", m.GOOS)
	w("It checks, on this computer, the parts of Drydock that behave differently on Windows: stopping whole process trees,")
	w("the terminal, the Credential Manager, the SQLite database, how Claude Code is started, finding installed CLIs, Git")
	w("worktrees, and the core running a whole task. It installs nothing and changes no settings.")
	w("")
	w("HOW TO RUN IT")
	w("-------------")
	if m.GOOS == "windows" {
		w("%d. Right-click the zip and choose Extract All. Open the extracted folder.", n())
		w("%d. Click the folder's address bar, type powershell and press Enter. A PowerShell window opens in this folder.", n())
		w("   If Windows refuses to start the programs later (\"Windows protected your PC\" or Smart App Control),")
		w("   run this once in that window, then try again:   Get-ChildItem -Recurse | Unblock-File")
	} else {
		w("%d. Unzip the bundle (unzip keeps the programs executable) and open a terminal in the unzipped folder.", n())
	}
	w("%d. Run:   %s", n(), run)
	w("   It prints one line per step. The whole run takes about 6 minutes on the Linux development host and longer on")
	w("   Windows: allow 10 to 30 minutes, and keep the computer awake.")
	w("%d. At the end it prints the name of a zip file in this folder, for example drydock-verify-2026-10-10-153012.zip.", n())
	w("   Send that zip back. It holds the report (drydock-verify-report.md and .json) and the log of every step.")
	w("")
	w("Options:")
	w("   %s --with-claude   also run the tests that use the real Claude Code CLI (see COST below)", run)
	w("   %s --list          show the steps and what each proves, without running anything", run)
	w("   %s --steps core-smoke,core-terminal,core-shutdown   run only the steps named (\"tests:*\" means all package tests)", run)
	w("")
	w("WHAT IT NEEDS")
	w("-------------")
	w("- Git (on Windows: Git for Windows, https://git-scm.com). The core and many tests create real repositories.")
	if m.GoBundled != "" {
		w("- Nothing else. The %d tests that run go build / go test / go vet themselves, and the core smoke (which runs the", nNeedsGo)
		folder := "go/"
		if m.GOOS == "windows" {
			folder = `go\`
		}
		w("  fixture project's checks), use the copy of Go inside this folder (%s, in %s) when Go is not installed. Its", m.GoBundled, folder)
		w("  caches go to a temporary folder that is removed at the end. An installed Go on PATH is preferred when there is one.")
	} else {
		w("- Go (optional; https://go.dev/dl/). The core smoke and %d tests run go build / go test / go vet on a fixture project,", nNeedsGo)
		w("  exactly as Drydock does for a Go project. Without Go on PATH they are skipped and the report says so.")
	}
	w("- gh, node, claude and docker are only reported when they are there. claude is needed only for --with-claude.")
	w("")
	w("COST")
	w("----")
	w("Without --with-claude nothing is paid for: every agent in the normal steps is a scripted test double.")
	w("With --with-claude %d tests start the real claude CLI with the account it is signed in to (mostly the haiku", nReal)
	w("model). Measured pieces: a full worker-and-reviewer task cost about $0.10; most single turns cost under one cent;")
	w("one plan-mode turn cost $0.09. The whole set has not been measured: expect roughly one dollar, or the same share of a")
	w("subscription's usage limit.")
	w("")
	w("WHAT EACH STEP PROVES")
	w("---------------------")
	for _, s := range Steps(m) {
		w("%s", s.ID)
		for _, line := range wrap(s.Proves, 100) {
			w("    %s", line)
		}
	}
	if len(m.Untested) > 0 {
		w("")
		w("Packages with Windows-specific code but no tests on %s (compiled, not run): %s.", m.GOOS, strings.Join(m.Untested, ", "))
	}
	w("")
	w("WHAT THE ZIP YOU SEND BACK HOLDS")
	w("--------------------------------")
	w("The system name, version and build, CPU, memory, whether this ran as administrator, the versions of git, gh, node,")
	w("npm, claude, docker and go, each step's result and its log. Paths under your user folder are shown as ~. No")
	w("passwords, tokens, keys or environment variables are written. The files are plain text: read them first if you like.")
	w("")
	w("WHAT IT DOES ON THIS COMPUTER")
	w("-----------------------------")
	w("- Creates temporary folders under %%TEMP%% (or $TMPDIR) and removes them at the end.")
	w("- Starts and stops test programs, the scripted Drydock core and one shell (the terminal check). Everything listens on")
	w("  127.0.0.1 only, so no firewall question is expected.")
	w("- The Credential Manager test stores and then deletes a test entry under the name \"drydock\". It leaves Drydock's")
	w("  own key-encryption entry alone when a Drydock build on this computer already has one (it decrypts the provider keys")
	w("  that build keeps), and deletes only one it created itself.")
	w("- Writes the results folder and its zip next to %s.", exe)
	text := b.String()
	if m.GOOS == "windows" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

func wrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
