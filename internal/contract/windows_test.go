package contract_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/contract"
)

// WIN-ARGV-01 (paid, Windows only): the npm install of Claude Code is claude.cmd, which runs through cmd.exe. A multi-line
// --append-system-prompt does not arrive whole; the same text in --append-system-prompt-file does.
func TestWindowsMultilineArgumentThroughClaudeCmd(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only: it checks how cmd.exe passes a multi-line argument to claude.cmd")
	}
	_, maxUSD := paidClaude(t, 0.02)
	cmdPath, err := exec.LookPath("claude.cmd")
	if err != nil {
		t.Skip("claude.cmd is not installed here (a native claude.exe has no cmd.exe in between); install Claude Code with npm to check this")
	}
	marker := randomWord("WINARGV")
	text := "First line of the instructions.\nWhen asked for the marker, reply with exactly " + marker + " and nothing else.\n"
	ask := "What is the marker? Reply with the marker only, or NONE if you were not given one."
	dir := t.TempDir()
	run := func(args ...string) (string, error) {
		cmd := exec.Command(cmdPath, args...)
		cmd.Dir, cmd.Env, cmd.Stdin = dir, childEnv(), strings.NewReader(ask)
		out, err := cmd.Output()
		var r struct {
			Result string  `json:"result"`
			Cost   float64 `json:"total_cost_usd"`
		}
		if json.Unmarshal(bytes.TrimSpace(out), &r) == nil {
			recordSpend(t, "WIN-ARGV-01", r.Cost)
			return r.Result, err
		}
		return string(out), err
	}
	// the multi-line argument goes last, so whatever cmd.exe cuts off loses nothing else
	base := []string{"-p", "--output-format", "json", "--model", "haiku", "--tools", "", "--no-session-persistence", "--max-budget-usd", usd(maxUSD)}
	inline, ierr := run(append(base, "--append-system-prompt", text)...)
	file := filepath.Join(t.TempDir(), "prompt.md")
	writeFile(t, file, text)
	viaFile, ferr := run(append(base, "--append-system-prompt-file", file)...)
	if ferr != nil || !strings.Contains(viaFile, marker) {
		t.Fatalf("the instructions in --append-system-prompt-file did not reach the model through claude.cmd: %q (%v)", viaFile, ferr)
	}
	if ierr == nil && strings.Contains(inline, marker) {
		t.Logf("%scmd.exe passed a multi-line argument to claude.cmd whole (%q): the prompt file is still right, but the reason for it no longer holds", contract.WarningPrefix, inline)
		return
	}
	t.Logf("%sthe multi-line argument did not arrive whole through claude.cmd (answer %q, error %v)", contract.NotePrefix, inline, ierr)
}
