package contract_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/contract"
)

func TestReportWritesJSONAndMarkdown(t *testing.T) {
	spent := 0.0123
	r := &contract.Report{Generated: "2026-10-10T00:00:00Z", Commit: "abc1234", Platform: "linux/amd64", BudgetUSD: 1.5, SpentUSD: 0.0123, EstimatedUSD: 0.05,
		CLIs: []contract.CLIInfo{{Name: "claude", Version: "2.1.296", Path: "/opt/claude", Source: "path", Status: "ready"}},
		Updates: []contract.Update{{CLI: "claude", Package: "@anthropic-ai/claude-code", Kind: "cli", Pinned: "2.1.295", Installed: "2.1.296", Latest: "2.1.297",
			Change: "2.1.297 is newer than the installed 2.1.296", Newer: true},
			{CLI: "claude-agent-acp", Package: "@agentclientprotocol/claude-agent-acp", Kind: "bridge", Pinned: "0.88.0", Installed: "0.88.0", Latest: "0.89.0",
				Change: "0.89.0 is newer than the installed 0.88.0; run the suite with --latest before adopting it", Newer: true, Tested: true}},
		Rows: []contract.Row{
			{Contract: "CLAUDE-STREAM-01", CLI: "claude", CLIVersion: "2.1.296", Status: contract.StatusPass, Duration: 12.3, SpendUSD: &spent, Assumption: "shapes",
				Notes: []string{"answered by claude-haiku-5-5"}},
			{Contract: "CLAUDE-RESTRICTED-02", CLI: "claude", CLIVersion: "2.1.296", Status: contract.StatusFail, Assumption: "obeyed",
				Failure: "TestX:\nthe reply did not end with PINEAPPLE-1234 | not obeyed", Warnings: []string{"CLAUDE.md is in context natively"}},
			{Contract: "ACP-INIT-01", CLI: "codex-acp", Status: contract.StatusSkip, Reason: "codex-acp is not installed here"},
		}}
	r.Finish()
	if !r.Failed() || r.Summary["pass"] != 1 || r.Summary["fail"] != 1 || r.Summary["skip"] != 1 || r.Summary["warnings"] != 1 {
		t.Fatalf("summary %v", r.Summary)
	}
	dir := t.TempDir()
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "contract-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var back contract.Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Rows) != 3 || back.Rows[0].SpendUSD == nil || *back.Rows[0].SpendUSD != spent || back.Updates[0].Latest != "2.1.297" || back.Summary["fail"] != 1 {
		t.Fatalf("round trip %+v", back)
	}
	md, err := os.ReadFile(filepath.Join(dir, "contract-report.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(md)
	for _, want := range []string{
		"1 passed, 1 failed, 1 skipped, 1 with a warning",
		"$0.0123 reported", "about $0.05 estimated", "budget $1.50",
		"| CLAUDE-STREAM-01 | claude | 2.1.296 | pass | 12.3s | $0.0123 |",
		`not obeyed`, `\|`, // a pipe inside a cell is escaped
		"| ACP-INIT-01 | codex-acp | - | skip |",
		"### CLAUDE-RESTRICTED-02 (claude 2.1.296)",
		"**CLAUDE-RESTRICTED-02 (claude), warning:** CLAUDE.md is in context natively",
		"- CLAUDE-STREAM-01 (claude): answered by claude-haiku-5-5",
		"| claude | @anthropic-ai/claude-code | 2.1.295 | 2.1.296 | 2.1.297 |",
		"0.89.0 is newer than the installed 0.88.0; this run tested it |",
		"| claude | 2.1.296 | /opt/claude (path) | ready |",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("markdown lacks %q:\n%s", want, s)
		}
	}
}

func TestLedgerAddsUpAcrossWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spend.jsonl")
	if total, err := contract.LedgerTotal(path); err != nil || total != 0 {
		t.Fatalf("a missing ledger is empty: %v %v", total, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := contract.AppendSpend(path, contract.Spend{Test: fmt.Sprintf("TestX/%d", i%2), Contract: "CLAUDE-X-01", USD: 0.25}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	ss, err := contract.ReadLedger(path)
	if err != nil || len(ss) != 20 {
		t.Fatalf("%d lines, %v", len(ss), err)
	}
	if total, _ := contract.LedgerTotal(path); total != 5 {
		t.Fatalf("total %v", total)
	}
	by := contract.SpendByTest(ss)
	if by["TestX/0"] != 2.5 || by["TestX"] != 5 {
		t.Fatalf("%v", by)
	}
	if err := os.WriteFile(path, []byte("{\"usd\":1}\nnot json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.LedgerTotal(path); err == nil {
		t.Fatal("a ledger that is partly unreadable must not yield a total")
	}
}

func TestBudgetComesFromTheEnvironment(t *testing.T) {
	t.Setenv(contract.EnvBudget, "")
	if _, ok, err := contract.Budget(); ok || err != nil {
		t.Fatalf("no budget set: %v %v", ok, err)
	}
	t.Setenv(contract.EnvBudget, "1.50")
	if usd, ok, err := contract.Budget(); !ok || err != nil || usd != 1.5 {
		t.Fatalf("%v %v %v", usd, ok, err)
	}
	t.Setenv(contract.EnvBudget, "lots")
	if _, _, err := contract.Budget(); err == nil {
		t.Fatal("a budget that is not a number is an error")
	}
}

func TestWithoutVarsRemovesTheEnclosingSession(t *testing.T) {
	env := []string{"PATH=/bin", "CLAUDECODE=1", "claude_code_remote_session_id=x", "CLAUDE_CODE_OAUTH_TOKEN=t", "CLAUDE_CODE_SESSION_ID=s"}
	got := strings.Join(contract.WithoutVars(env, contract.EnclosingSessionEnv...), ",")
	if got != "PATH=/bin,CLAUDE_CODE_OAUTH_TOKEN=t" {
		t.Fatalf("%s", got)
	}
}
